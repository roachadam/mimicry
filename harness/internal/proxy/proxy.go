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

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// Proxy accepts proxy clients. Connections it lets through come out of
// Listener: CONNECT and SOCKS5 tunnels after the proxy's reply, or, when
// Plain, absolute-form HTTP requests as they are, first request included.
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
	// requests are passed on and CONNECT is recorded, then answered 502 (a
	// browser may try HTTPS first). Otherwise CONNECT tunnels are passed on
	// and absolute-form requests answered 404.
	Plain bool
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
	once     sync.Once
}

// Listener returns the connections the proxy lets through, for the capture
// server to serve.
func (p *Proxy) Listener() net.Listener {
	p.once.Do(func() {
		p.ln = &listener{
			conns:  make(chan net.Conn),
			closed: make(chan struct{}),
		}
	})
	return p.ln
}

// Requests returns what clients asked for, in order.
func (p *Proxy) Requests() []capture.ProxyRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]capture.ProxyRequest(nil), p.requests...)
}

// Serve accepts proxy clients on ln until ctx is canceled.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	out := p.Listener().(*listener)
	stop := context.AfterFunc(ctx, func() {
		_ = ln.Close()
		_ = out.Close()
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
			if err := p.handle(ctx, conn, out); err != nil {
				_ = conn.Close()
				if p.Logf != nil && !errors.Is(err, io.EOF) {
					p.Logf("%s: %v", conn.RemoteAddr(), err)
				}
			}
		})
	}
}

func (p *Proxy) handle(ctx context.Context, conn net.Conn, out *listener) error {
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
	var pass io.Reader
	if first[0] == 5 {
		pass, err = p.socks5(conn, br, id)
	} else {
		pass, err = p.http(conn, br, id)
	}
	if err != nil || pass == nil {
		return err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return err
	}
	return out.hand(ctx, &passConn{
		Conn: conn,
		r:    pass,
	})
}

// http reads requests until one is let through. A CONNECT is recorded and
// answered (407 and read on while Auth is missing); an absolute-form
// request is passed on whole, or refused, as Plain says. id numbers the
// connection.
func (p *Proxy) http(conn net.Conn, br *bufio.Reader, id int) (io.Reader, error) {
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		method, rest, _ := strings.Cut(line, " ")
		if method != "CONNECT" {
			target, _, _ := strings.Cut(rest, " ")
			if u, err := url.Parse(target); p.PassThrough && err == nil && !p.Allow(u.Hostname()) {
				return nil, forward(conn, bufio.NewReader(io.MultiReader(strings.NewReader(line), br)))
			}
			if !p.Plain {
				_, err := io.WriteString(conn, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
				return nil, errors.Join(err, conn.Close())
			}
			return io.MultiReader(strings.NewReader(line), br), nil
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
				return nil, err
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
			return nil, fmt.Errorf("CONNECT %q: %w", target, err)
		}
		if !p.Allow(host) {
			if p.PassThrough {
				return nil, tunnel(conn, br, target)
			}
			_, err := io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return nil, errors.Join(err, conn.Close())
		}
		switch {
		case p.Auth != "" && auth != p.Auth:
			req.Status = http.StatusProxyAuthRequired
			p.record(req)
			if _, err := io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"mimicry capture\"\r\nContent-Length: 0\r\n\r\n"); err != nil {
				return nil, err
			}
			continue
		case p.Plain:
			req.Status = http.StatusBadGateway
			p.record(req)
			_, err := io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return nil, errors.Join(err, conn.Close())
		}
		req.Status = http.StatusOK
		p.record(req)
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return nil, err
		}
		return br, nil
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
// sent, and answers. id numbers the connection.
func (p *Proxy) socks5(conn net.Conn, br *bufio.Reader, id int) (io.Reader, error) {
	greeting := &keeper{
		r: br,
	}
	head, err := greeting.take(2)
	if err != nil {
		return nil, err
	}
	methods, err := greeting.take(int(head[1]))
	if err != nil {
		return nil, err
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
		return nil, err
	}
	if method == socksNoMethod {
		p.record(req)
		return nil, conn.Close()
	}
	msg := &keeper{
		r: br,
	}
	head, err = msg.take(4)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		host = net.IP(ip).String()
	case socksDomain:
		n, err := msg.take(1)
		if err != nil {
			return nil, err
		}
		name, err := msg.take(int(n[0]))
		if err != nil {
			return nil, err
		}
		host = string(name)
	default:
		return nil, fmt.Errorf("SOCKS5 address type %d", head[3])
	}
	port, err := msg.take(2)
	if err != nil {
		return nil, err
	}
	req.Request = hex.EncodeToString(msg.kept)
	req.Target = net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
	reply := func(code byte) error {
		_, err := conn.Write([]byte{socksVersion, code, 0, socksIPv4, 0, 0, 0, 0, 0, 0})
		return err
	}
	if !p.Allow(host) {
		return nil, errors.Join(reply(socksNotAllow), conn.Close())
	}
	p.record(req)
	if head[1] != socksConnect {
		return nil, errors.Join(reply(socksBadCmd), conn.Close())
	}
	if err := reply(socksSucceeded); err != nil {
		return nil, err
	}
	return br, nil
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
}

func (c *passConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

// listener hands passed connections to the capture server.
type listener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
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

// Addr names the listener; its connections come from the proxy, not a port.
func (l *listener) Addr() net.Addr {
	return proxyAddr{}
}

type proxyAddr struct{}

func (proxyAddr) Network() string {
	return "proxy"
}

func (proxyAddr) String() string {
	return "capture proxy"
}
