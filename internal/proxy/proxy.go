// Package proxy connects through SOCKS5 and HTTP CONNECT proxies, bounded
// by the caller's context, and carries UDP datagrams through SOCKS5 UDP
// associations (Association).
package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Answer is a proxy's response to CONNECT, its body read.
type Answer struct {
	StatusCode int
	// Status is the status line's code and text ("407 Proxy
	// Authentication Required").
	Status string
	Header http.Header
	// Close reports that the proxy closes the connection after it.
	Close bool
}

// maxAnswerHeader bounds the bytes read until a CONNECT answer's header
// ends.
const maxAnswerHeader = 64 << 10

// ErrAnswerTooLarge is returned for a CONNECT answer whose header is longer
// than 64 KiB.
var ErrAnswerTooLarge = errors.New("the proxy's answer header is longer than 64 KiB")

// headerLimit reads from r, failing with ErrAnswerTooLarge once left bytes
// are read; a negative left is no limit.
type headerLimit struct {
	r    io.Reader
	left int
}

func (l *headerLimit) Read(p []byte) (int, error) {
	if l.left < 0 {
		return l.r.Read(p)
	}

	if l.left == 0 {
		return 0, ErrAnswerTooLarge
	}

	if len(p) > l.left {
		p = p[:l.left]
	}

	n, err := l.r.Read(p)

	l.left -= n

	return n, err
}

// Connect asks an HTTP proxy over conn for a tunnel to target
// ("host:port") with header lines sent as given. On 2xx the returned conn
// is the tunnel; otherwise conn stays usable unless the answer closes it.
func Connect(ctx context.Context, conn net.Conn, target string, header []string) (Answer, net.Conn, error) {
	defer watch(ctx, conn)()

	var b strings.Builder
	b.WriteString("CONNECT ")
	b.WriteString(target)
	b.WriteString(" HTTP/1.1\r\n")

	for _, h := range header {
		b.WriteString(h)
		b.WriteString("\r\n")
	}

	b.WriteString("\r\n")

	if _, err := io.WriteString(conn, b.String()); err != nil {
		return Answer{}, nil, contextError(ctx, err)
	}

	limit := &headerLimit{
		r:    conn,
		left: maxAnswerHeader,
	}
	br := bufio.NewReader(limit)

	resp, err := http.ReadResponse(br, &http.Request{
		Method: http.MethodConnect,
	})
	if err != nil {
		return Answer{}, nil, contextError(ctx, fmt.Errorf("reading the proxy's answer to CONNECT: %w", err))
	}

	// The header is read: the body (bounded below) and the tunnel are not
	// limited.
	limit.left = -1

	a := Answer{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     resp.Header,
		Close:      resp.Close,
	}
	// A 2xx answer has no body: what follows is the tunnel (RFC 9110 9.3.6).
	// Other answers' bodies are read so the connection is at the next answer.
	if a.StatusCode/100 != 2 {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()

		if err != nil {
			return Answer{}, nil, contextError(ctx, err)
		}
	}

	if br.Buffered() > 0 {
		return a, &bufferedConn{
			Conn: conn,
			r:    br,
		}, nil
	}

	return a, conn, nil
}

// SOCKS5 (RFC 1928, RFC 1929) values.
const (
	socksVersion      = 5
	MethodNone        = 0
	MethodUser        = 2
	socksNoMethod     = 0xff
	socksConnect      = 1
	socksUDPAssociate = 3
	socksIPv4         = 1
	socksDomain       = 3
	socksIPv6         = 4
)

// RefusedError is a SOCKS5 proxy's refusal of a request: a reply other
// than "succeeded" to CONNECT or UDP ASSOCIATE.
type RefusedError struct {
	// UDP: the refused request was UDP ASSOCIATE.
	UDP bool
	// Reply is the reply code (RFC 1928, section 6).
	Reply byte
}

func (e *RefusedError) Error() string {
	what := "the connection"
	if e.UDP {
		what = "the UDP association"
	}

	reason, ok := socksReplies[e.Reply]
	if !ok {
		reason = fmt.Sprintf("reply %d", e.Reply)
	}

	return fmt.Sprintf("SOCKS5 proxy refused %s: %s", what, reason)
}

// SOCKS5 asks a SOCKS5 proxy over conn for a connection to target,
// offering methods in order; host names are resolved by the proxy. user
// answers username/password authentication.
func SOCKS5(ctx context.Context, conn net.Conn, target string, methods []byte, user *url.Userinfo) error {
	defer watch(ctx, conn)()

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return fmt.Errorf("SOCKS5 target port %q: %w", portStr, err)
	}

	if err := socksAuth(conn, methods, user); err != nil {
		return contextError(ctx, err)
	}

	if _, err := socksRequest(conn, socksConnect, host, uint16(port)); err != nil {
		return contextError(ctx, err)
	}

	return nil
}

// socksAuth offers methods and authenticates as the proxy picks.
func socksAuth(conn net.Conn, methods []byte, user *url.Userinfo) error {
	n, err := lengthByte(len(methods), "method list")
	if err != nil {
		return err
	}

	greeting := append([]byte{socksVersion, n}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return err
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("SOCKS5 method selection: %w", err)
	}

	if reply[0] != socksVersion {
		return fmt.Errorf("SOCKS5 proxy answered version %d", reply[0])
	}

	switch reply[1] {
	case MethodNone:
	case MethodUser:
		if user == nil {
			return errors.New("SOCKS5 proxy asks for a username and password; the proxy URL has none")
		}

		return socksUser(conn, user)

	case socksNoMethod:
		return errors.New("SOCKS5 proxy accepts none of the authentication methods offered")
	default:
		return fmt.Errorf("SOCKS5 proxy picked authentication method %d, which was not offered", reply[1])
	}

	return nil
}

// socksRequest sends request cmd for host at port and reads the reply,
// returning the bound address it gives ("host:port"). A refusal is a
// *RefusedError.
func socksRequest(conn net.Conn, cmd byte, host string, port uint16) (string, error) {
	req, err := appendAddr([]byte{socksVersion, cmd, 0}, host, port)
	if err != nil {
		return "", err
	}

	if _, err := conn.Write(req); err != nil {
		return "", err
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", fmt.Errorf("SOCKS5 reply: %w", err)
	}

	if head[1] != 0 {
		return "", &RefusedError{
			UDP:   cmd == socksUDPAssociate,
			Reply: head[1],
		}
	}

	var n int
	switch head[3] {
	case socksIPv4:
		n = net.IPv4len
	case socksIPv6:
		n = net.IPv6len
	case socksDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return "", err
		}

		n = int(l[0])

	default:
		return "", fmt.Errorf("SOCKS5 reply address type %d", head[3])
	}

	bound := make([]byte, n+2)
	if _, err := io.ReadFull(conn, bound); err != nil {
		return "", err
	}

	boundHost := string(bound[:n])
	if head[3] != socksDomain {
		boundHost = net.IP(bound[:n]).String()
	}

	return net.JoinHostPort(boundHost, strconv.Itoa(int(binary.BigEndian.Uint16(bound[n:])))), nil
}

// appendAddr appends a SOCKS5 address (ATYP, DST.ADDR, DST.PORT) to b:
// host as an IPv4 or IPv6 address when it is one, else as a host name.
func appendAddr(b []byte, host string, port uint16) ([]byte, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			b = append(append(b, socksIPv4), ip4...)
		} else {
			b = append(append(b, socksIPv6), ip.To16()...)
		}
	} else {
		n, err := lengthByte(len(host), "host name")
		if err != nil {
			return nil, err
		}

		b = append(append(b, socksDomain, n), host...)
	}

	return binary.BigEndian.AppendUint16(b, port), nil
}

// socksUser authenticates with a username and password (RFC 1929).
func socksUser(conn net.Conn, user *url.Userinfo) error {
	name := user.Username()
	password, _ := user.Password()

	nameLen, err := lengthByte(len(name), "username")
	if err != nil {
		return err
	}

	passwordLen, err := lengthByte(len(password), "password")
	if err != nil {
		return err
	}

	msg := append([]byte{1, nameLen}, name...)

	msg = append(append(msg, passwordLen), password...)

	if _, err := conn.Write(msg); err != nil {
		return err
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("SOCKS5 authentication: %w", err)
	}

	if reply[1] != 0 {
		return errors.New("SOCKS5 proxy rejected the username and password")
	}

	return nil
}

// lengthByte is a SOCKS5 length field: n, at most 255.
func lengthByte(n int, what string) (byte, error) {
	if n < 0 || n > 255 {
		return 0, fmt.Errorf("SOCKS5 %s is limited to 255 bytes", what)
	}

	return byte(n), nil
}

var socksReplies = map[byte]string{
	1: "general failure",
	2: "not allowed by ruleset",
	3: "network unreachable",
	4: "host unreachable",
	5: "connection refused",
	6: "TTL expired",
	7: "command not supported",
	8: "address type not supported",
}

// watch applies ctx's deadline to conn and closes it if ctx is canceled,
// until the returned function is called.
func watch(ctx context.Context, conn net.Conn) func() {
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}

	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Unix(1, 0))
	})

	return func() {
		stop()

		_ = conn.SetDeadline(time.Time{})
	}
}

// contextError prefers ctx's error when cancellation caused err. The
// conn deadline can pass before ctx's timer fires, so a timeout at or after
// it counts as ctx's.
func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	if d, ok := ctx.Deadline(); ok && errors.Is(err, os.ErrDeadlineExceeded) && !time.Now().Before(d) {
		return context.DeadlineExceeded
	}

	return err
}

// bufferedConn reads what was buffered past the proxy's answer first.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}
