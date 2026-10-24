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

// Connect asks an HTTP proxy, over conn, for a tunnel to target
// ("host:port"): a CONNECT request with header lines as given, in order and
// spelled as sent ("Host: example.com:443"). On a 2xx answer the returned
// connection is the tunnel; otherwise it stays usable for another request
// unless the answer closes it.
func Connect(ctx context.Context, conn net.Conn, target string, header []string) (Answer, net.Conn, error) {
	defer watch(ctx, conn)()
	var b strings.Builder
	b.WriteString("CONNECT " + target + " HTTP/1.1\r\n")
	for _, h := range header {
		b.WriteString(h + "\r\n")
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
	// A 2xx answer has no body: what follows is the tunnel (RFC 9110
	// 9.3.6), which net/http would read, and its Close drain, as a body to
	// EOF; it is left alone. Any other answer's body is read, leaving the
	// connection at the next answer.
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
	socksVersion  = 5
	MethodNone    = 0
	MethodUser    = 2
	socksNoMethod = 0xff
	socksConnect  = 1
	socksIPv4     = 1
	socksDomain   = 3
	socksIPv6     = 4
)

// SOCKS5 asks a SOCKS5 proxy, over conn, for a connection to target
// ("host:port"), offering methods in order. A host name goes to the proxy
// to resolve; an IP address as such. user answers username/password
// authentication when the proxy picks it.
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
	if err := socks5(conn, host, uint16(port), methods, user); err != nil {
		return contextError(ctx, err)
	}
	return nil
}

func socks5(conn net.Conn, host string, port uint16, methods []byte, user *url.Userinfo) error {
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
		if err := socksUser(conn, user); err != nil {
			return err
		}
	case socksNoMethod:
		return errors.New("SOCKS5 proxy accepts none of the authentication methods offered")
	default:
		return fmt.Errorf("SOCKS5 proxy picked authentication method %d, which was not offered", reply[1])
	}

	req := []byte{socksVersion, socksConnect, 0}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(append(req, socksIPv4), ip4...)
		} else {
			req = append(append(req, socksIPv6), ip.To16()...)
		}
	} else {
		n, err := lengthByte(len(host), "host name")
		if err != nil {
			return err
		}
		req = append(append(req, socksDomain, n), host...)
	}
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return fmt.Errorf("SOCKS5 reply: %w", err)
	}
	if head[1] != 0 {
		return fmt.Errorf("SOCKS5 proxy refused the connection: %s", socksReplies[head[1]])
	}
	// The bound address, which a client does not need.
	var skip int
	switch head[3] {
	case socksIPv4:
		skip = net.IPv4len
	case socksIPv6:
		skip = net.IPv6len
	case socksDomain:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return err
		}
		skip = int(n[0])
	default:
		return fmt.Errorf("SOCKS5 reply address type %d", head[3])
	}
	_, err = io.ReadFull(conn, make([]byte, skip+2))
	return err
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

// contextError prefers the context's error when cancellation caused err.
func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
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
