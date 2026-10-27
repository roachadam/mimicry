// Package proxy is the capture proxy: HTTP (CONNECT and absolute-form) and
// SOCKS5 on one port. It records how a client asks for each connection and
// hands the connection on to the capture server.
package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
)

// Proxy accepts proxy clients. Connections it lets through come out of
// Listener: CONNECT and SOCKS5 tunnels after the proxy's reply, or, when
// Plain, absolute-form HTTP requests as they are, first request included.
// With PlainPort, those for that port come out of PlainListener instead.
type Proxy struct {
	// Allow reports whether host is a capture host. Others are refused (or
	// passed through, with PassThrough) and not recorded: a browser's own
	// background traffic goes through its proxy too.
	Allow func(host string) bool
	// PassThrough carries requests for other hosts to the network, without
	// credentials or records, for a proxy the whole system uses while
	// capturing (Safari through a manual proxy).
	PassThrough bool
	// Plain means the server behind speaks plain HTTP: absolute-form
	// requests for allowed hosts are passed on (others get 403) and CONNECT
	// is recorded, then answered 502. Otherwise CONNECT tunnels are passed
	// on and absolute-form requests get 404, except for PlainPort.
	Plain bool
	// Port and PlainPort are the capture server's two ports, which the
	// browser's URLs name but the proxy never dials. Connections for
	// PlainPort come out of PlainListener. Zero: every connection comes out
	// of Listener.
	Port, PlainPort int
	// Auth, when set, is the Proxy-Authorization value a CONNECT must
	// carry: one without it is recorded and answered 407 (Basic), keeping
	// the connection open for the retry.
	Auth string
	// Logf receives connection errors. Nil discards them.
	Logf func(format string, args ...any)

	mu       sync.Mutex
	requests []capture.ProxyRequest
	conns    int
	ln       *listener
	plainLn  *listener
	once     sync.Once
}

// Listener returns the connections the proxy lets through, for the capture
// server to serve.
func (p *Proxy) Listener() net.Listener {
	p.listeners()

	return p.ln
}

// PlainListener returns the connections the proxy lets through to
// PlainPort.
func (p *Proxy) PlainListener() net.Listener {
	p.listeners()

	return p.plainLn
}

func (p *Proxy) listeners() {
	p.once.Do(func() {
		p.ln = newListener(&p.Port)
		p.plainLn = newListener(&p.PlainPort)
	})
}

// Requests returns what clients asked for, in order.
func (p *Proxy) Requests() []capture.ProxyRequest {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]capture.ProxyRequest(nil), p.requests...)
}

// Serve accepts proxy clients on ln until ctx is canceled.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	p.listeners()

	stop := context.AfterFunc(ctx, func() {
		_ = ln.Close()
		_ = p.ln.Close()
		_ = p.plainLn.Close()
	})

	defer stop()

	var wg sync.WaitGroup

	defer wg.Wait()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return err
		}

		wg.Go(func() {
			if err := p.handle(ctx, conn); err != nil {
				_ = conn.Close()
				if p.Logf != nil && !errors.Is(err, io.EOF) {
					p.Logf("%s: %v", conn.RemoteAddr(), err)
				}
			}
		})
	}
}

func (p *Proxy) handle(ctx context.Context, conn net.Conn) error {
	// Until the connection is handed on (or for good, when passed
	// through), stopping the proxy closes it.
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stop()

	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}

	br := bufio.NewReader(conn)

	first, err := br.Peek(1)
	if err != nil {
		return err
	}

	p.mu.Lock()

	p.conns++

	id := p.conns
	p.mu.Unlock()

	var (
		pass   io.Reader
		target string
	)

	if first[0] == 5 {
		pass, target, err = p.socks5(conn, br, id)
	} else {
		pass, target, err = p.http(conn, br, id)
	}

	if err != nil || pass == nil {
		return err
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		return err
	}

	out := p.ln
	if p.PlainPort != 0 && portOf(target) == p.PlainPort {
		out = p.plainLn
	}

	return out.hand(ctx, &passConn{
		Conn: conn,
		r:    pass,
		id:   id,
	})
}

// portOf is target's port ("host:port"), or 0.
func portOf(target string) int {
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		return 0
	}

	n, _ := strconv.Atoi(port)

	return n
}

// http reads requests until one is let through, and returns what to read
// it from and the host and port it goes to. CONNECT is recorded and answered
// (407 while Auth is missing); absolute-form requests are passed on or
// refused as Plain and PlainPort say. id numbers the connection.
func (p *Proxy) http(conn net.Conn, br *bufio.Reader, id int) (io.Reader, string, error) {
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, "", err
		}

		method, rest, _ := strings.Cut(line, " ")
		if method != "CONNECT" {
			target, _, _ := strings.Cut(rest, " ")
			u, err := url.Parse(target)
			refused := err == nil && !p.Allow(u.Hostname())

			if refused && p.PassThrough {
				return nil, "", forward(conn, bufio.NewReader(io.MultiReader(strings.NewReader(line), br)))
			}

			toPlain := err == nil && p.PlainPort != 0 && u.Port() == strconv.Itoa(p.PlainPort)
			if !p.Plain && !toPlain {
				_, err := io.WriteString(conn, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
				return nil, "", errors.Join(err, conn.Close())
			}

			if refused {
				_, err := io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
				return nil, "", errors.Join(err, conn.Close())
			}

			host := ""
			if err == nil {
				host = u.Host
			}

			return io.MultiReader(strings.NewReader(line), br), host, nil
		}

		target, _, _ := strings.Cut(rest, " ")
		req := capture.ProxyRequest{
			Kind:   capture.ProxyConnect,
			Conn:   id,
			Target: target,
			Lines: []string{
				strings.TrimRight(line, "\r\n"),
			},
		}
		auth := ""
		tp := textproto.NewReader(br)

		for {
			h, err := tp.ReadLine()
			if err != nil {
				return nil, "", err
			}

			if h == "" {
				break
			}

			req.Lines = append(req.Lines, h)
			if name, value, ok := strings.Cut(h, ":"); ok && strings.EqualFold(name, "proxy-authorization") {
				auth = strings.TrimSpace(value)
			}
		}

		host, _, err := net.SplitHostPort(target)
		if err != nil {
			return nil, "", fmt.Errorf("CONNECT %q: %w", target, err)
		}

		if !p.Allow(host) {
			if p.PassThrough {
				return nil, "", tunnel(conn, br, target)
			}

			_, err := io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")

			return nil, "", errors.Join(err, conn.Close())
		}

		switch {
		case p.Auth != "" && auth != p.Auth:
			req.Status = http.StatusProxyAuthRequired
			p.record(req)

			if _, err := io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"mimicry capture\"\r\nContent-Length: 0\r\n\r\n"); err != nil {
				return nil, "", err
			}

			continue

		case p.Plain:
			req.Status = http.StatusBadGateway
			p.record(req)

			_, err := io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")

			return nil, "", errors.Join(err, conn.Close())
		}

		req.Status = http.StatusOK
		p.record(req)

		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return nil, "", err
		}

		return br, target, nil
	}
}

// tunnel connects conn to target and copies both ways until either side
// closes.
func tunnel(conn net.Conn, br *bufio.Reader, target string) error {
	dst, err := net.DialTimeout("tcp", target, 10*time.Second) //nolint:noctx // bounded by the timeout; lives as long as the client's connection
	if err != nil {
		_, werr := io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return errors.Join(err, werr, conn.Close())
	}

	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return errors.Join(err, dst.Close())
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		return errors.Join(err, dst.Close())
	}

	done := make(chan struct{})

	go func() {
		_, _ = io.Copy(dst, br)
		_ = dst.Close()

		close(done)
	}()

	_, _ = io.Copy(conn, dst)
	_ = conn.Close()

	<-done

	return nil
}

// passTransport carries passed-through plain requests, directly.
var passTransport = &http.Transport{
	Proxy: nil,
}

// forward answers absolute-form requests on conn by sending them on to
// the network, until the client closes.
func forward(conn net.Conn, br *bufio.Reader) error {
	defer func() {
		_ = conn.Close()
	}()

	if err := conn.SetDeadline(time.Time{}); err != nil {
		return err
	}

	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return nil
		}

		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")

		resp, err := passTransport.RoundTrip(req)
		if err != nil {
			_, err := io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return err
		}

		err = resp.Write(conn)
		_ = resp.Body.Close()

		if err != nil {
			return err
		}
	}
}

// SOCKS5 (RFC 1928) constants.
const (
	socksVersion   = 5
	socksNoAuth    = 0
	socksNoMethod  = 0xff
	socksConnect   = 1
	socksIPv4      = 1
	socksDomain    = 3
	socksIPv6      = 4
	socksSucceeded = 0
	socksNotAllow  = 2
	socksBadCmd    = 7
)

// socks5 reads the method selection and the request, records both as
// sent, and answers; it returns what to read the tunnel from and its
// target. id numbers the connection.
func (p *Proxy) socks5(conn net.Conn, br *bufio.Reader, id int) (io.Reader, string, error) {
	greeting := &keeper{
		r: br,
	}

	head, err := greeting.take(2)
	if err != nil {
		return nil, "", err
	}

	methods, err := greeting.take(int(head[1]))
	if err != nil {
		return nil, "", err
	}

	req := capture.ProxyRequest{
		Kind:     capture.ProxySOCKS5,
		Conn:     id,
		Greeting: hex.EncodeToString(greeting.kept),
	}
	method := byte(socksNoMethod)

	if slices.Contains(methods, socksNoAuth) {
		method = socksNoAuth
	}

	if _, err := conn.Write([]byte{socksVersion, method}); err != nil {
		return nil, "", err
	}

	if method == socksNoMethod {
		p.record(req)

		return nil, "", conn.Close()
	}

	msg := &keeper{
		r: br,
	}

	head, err = msg.take(4)
	if err != nil {
		return nil, "", err
	}

	var host string
	switch head[3] {
	case socksIPv4, socksIPv6:
		n := net.IPv4len
		if head[3] == socksIPv6 {
			n = net.IPv6len
		}

		ip, err := msg.take(n)
		if err != nil {
			return nil, "", err
		}

		host = net.IP(ip).String()

	case socksDomain:
		n, err := msg.take(1)
		if err != nil {
			return nil, "", err
		}

		name, err := msg.take(int(n[0]))
		if err != nil {
			return nil, "", err
		}

		host = string(name)

	default:
		return nil, "", fmt.Errorf("SOCKS5 address type %d", head[3])
	}

	port, err := msg.take(2)
	if err != nil {
		return nil, "", err
	}

	req.Request = hex.EncodeToString(msg.kept)
	req.Target = net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))

	reply := func(code byte) error {
		_, err := conn.Write([]byte{socksVersion, code, 0, socksIPv4, 0, 0, 0, 0, 0, 0})
		return err
	}

	if !p.Allow(host) {
		return nil, "", errors.Join(reply(socksNotAllow), conn.Close())
	}

	p.record(req)

	if head[1] != socksConnect {
		return nil, "", errors.Join(reply(socksBadCmd), conn.Close())
	}

	if err := reply(socksSucceeded); err != nil {
		return nil, "", err
	}

	return br, req.Target, nil
}

// keeper reads a message in parts and keeps its bytes as sent.
type keeper struct {
	r    io.Reader
	kept []byte
}

func (k *keeper) take(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(k.r, b); err != nil {
		return nil, err
	}

	k.kept = append(k.kept, b...)

	return b, nil
}

func (p *Proxy) record(r capture.ProxyRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.requests = append(p.requests, r)
}

// passConn reads what the proxy buffered before reading the network again.
type passConn struct {
	net.Conn
	r io.Reader
	// id is the client's connection to the proxy (ProxyRequest.Conn).
	id int
}

func (c *passConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

// ProxyConn numbers the client's connection to the proxy that carries this
// one (ProxyRequest.Conn).
func (c *passConn) ProxyConn() int {
	return c.id
}

// listener hands passed connections to the capture server.
type listener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
	// port is the port it stands for (Proxy.Port or PlainPort, read when
	// asked: they may be set after the listener is made), 0 for none.
	port *int
}

func newListener(port *int) *listener {
	return &listener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
		port:   port,
	}
}

func (l *listener) hand(ctx context.Context, c net.Conn) error {
	select {
	case l.conns <- c:
		return nil
	case <-l.closed:
		return net.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})

	return nil
}

// Addr is the loopback address with the port the listener stands for, or
// else the proxy's address.
func (l *listener) Addr() net.Addr {
	if *l.port != 0 {
		return &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: *l.port,
		}
	}

	return proxyAddr{}
}

type proxyAddr struct{}

func (proxyAddr) Network() string {
	return "proxy"
}

func (proxyAddr) String() string {
	return "capture proxy"
}
