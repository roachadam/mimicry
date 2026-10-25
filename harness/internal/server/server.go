package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// Server accepts TLS connections, records what each client sends, and answers
// requests so the client finishes normally.
type Server struct {
	// TLSConfig must hold the certificate. The server offers h2 and http/1.1
	// over its own copy. Plain scenarios, which never do TLS, need none.
	TLSConfig *tls.Config
	// OnCapture receives each connection's capture once: when the client
	// asks for /done (with the whole page for scenarios that span several
	// connections), or else when the connection ends. It is called from the
	// connection's goroutine.
	OnCapture func(*capture.Capture)
	// Logf receives connection errors. Nil discards them.
	Logf func(format string, args ...any)
	// ConnTimeout bounds each connection. Zero means one minute.
	ConnTimeout time.Duration
	// Scenario says how connections, HTTP/2 and HTTP/1.1, are answered. Nil
	// means DefaultScenario.
	Scenario *Scenario
	// IssueCert issues a certificate for hosts. Scenarios serving several
	// hosts over TLS need it: each host has its own certificate.
	IssueCert func(hosts []string) (tls.Certificate, error)
	// PlainListener is the second port of a scenario that serves plain
	// HTTP on a port of its own (Scenario.PlainPort); Serve serves and
	// closes it along with the first. Other scenarios leave it nil.
	PlainListener net.Listener
	// PacketConn is the UDP socket, on the listener's port number, of a
	// scenario that serves HTTP/3 (Scenario.HTTP3): an Alt-Svc's h3=":port"
	// names the same port. Serve serves QUIC on it and closes it along
	// with the listener. Other scenarios leave it nil.
	PacketConn net.PacketConn
}

// Serve handles connections from ln until ctx is canceled, then closes the
// open ones and waits for their handlers to return. Clients that stay running,
// like Safari, would otherwise hold connections open until ConnTimeout.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	sc := s.Scenario
	if sc == nil {
		sc = DefaultScenario
	}
	issue := cachedIssuer(s.IssueCert)
	var cfg *tls.Config
	if !sc.Plain {
		var err error
		if cfg, err = s.tlsConfig(sc, issue); err != nil {
			return err
		}
	}
	log := &pageLog{
		start: time.Now(),
	}
	if sc.plainPort && s.PlainListener == nil {
		_ = ln.Close()
		return errors.New("scenario " + sc.Name + " serves plain HTTP on a port of its own and needs Server.PlainListener")
	}
	if sc.h3 != nil && s.PacketConn == nil {
		_ = ln.Close()
		return errors.New("scenario " + sc.Name + " serves HTTP/3 and needs Server.PacketConn")
	}
	if sc.plainPort {
		sc = sc.onPorts(ln.Addr(), s.PlainListener.Addr())
	}
	serves := []func(context.Context) error{
		func(ctx context.Context) error {
			return s.accept(ctx, ln, func(conn net.Conn) error {
				return s.handle(ctx, conn, cfg, sc, log, false)
			})
		},
	}
	if sc.plainPort {
		serves = append(serves, func(ctx context.Context) error {
			return s.accept(ctx, s.PlainListener, func(conn net.Conn) error {
				return s.handle(ctx, conn, nil, sc, log, true)
			})
		})
	}
	if sc.h3 != nil {
		serves = append(serves, func(ctx context.Context) error {
			return s.serveQUIC(ctx, sc, log, issue)
		})
	}
	if len(serves) == 1 {
		return serves[0](ctx)
	}
	// Any listener failing stops the others.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make([]error, len(serves))
	var wg sync.WaitGroup
	for i, serve := range serves {
		wg.Go(func() {
			defer cancel()
			errs[i] = serve(ctx)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// accept hands each connection from ln to handle, on its own goroutine,
// until ctx is canceled; then it closes ln and waits for the handlers.
func (s *Server) accept(ctx context.Context, ln net.Listener, handle func(net.Conn) error) error {
	stop := context.AfterFunc(ctx, func() {
		_ = ln.Close()
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
			if err := handle(conn); err != nil && s.Logf != nil {
				s.Logf("%s: %v", conn.RemoteAddr(), err)
			}
		})
	}
}

// tlsConfig is the TLS configuration sc's connections are served with;
// issue issues the certificates of a page across several hosts.
func (s *Server) tlsConfig(sc *Scenario, issue func([]string) (tls.Certificate, error)) (*tls.Config, error) {
	cfg := s.TLSConfig.Clone()
	cfg.NextProtos = []string{
		"h2",
		"http/1.1",
	}
	if sc.HTTP1 {
		cfg.NextProtos = []string{
			"http/1.1",
		}
	}
	cfg.MinVersion = tls.VersionTLS12
	if sc.maxVersion != 0 {
		cfg.MaxVersion = sc.maxVersion
	}
	if sc.multi == nil {
		return cfg, nil
	}
	if issue == nil {
		return nil, errors.New("scenario " + sc.Name + " serves several hosts and needs Server.IssueCert")
	}
	return multiHostTLS(sc.multi, cfg, issue), nil
}

// cachedIssuer issues each set of names one certificate, so a host's TCP
// and QUIC connections present the same one; nil stays nil.
func cachedIssuer(issue func([]string) (tls.Certificate, error)) func([]string) (tls.Certificate, error) {
	if issue == nil {
		return nil
	}
	var mu sync.Mutex
	certs := map[string]tls.Certificate{}
	return func(names []string) (tls.Certificate, error) {
		key := strings.Join(names, " ")
		mu.Lock()
		defer mu.Unlock()
		if c, ok := certs[key]; ok {
			return c, nil
		}
		c, err := issue(names)
		if err != nil {
			return tls.Certificate{}, err
		}
		certs[key] = c
		return c, nil
	}
}

// handle serves one connection. plainOnly is a connection to a PlainPort
// scenario's plain port, which answers TLS as an HTTP-only server would.
func (s *Server) handle(ctx context.Context, conn net.Conn, cfg *tls.Config, sc *Scenario, log *pageLog, plainOnly bool) error {
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer func() {
		stop()
		_ = conn.Close()
	}()
	timeout := s.ConnTimeout
	if timeout == 0 {
		timeout = time.Minute
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	c := &capture.Capture{
		Scenario: sc.Name,
	}
	emitted := false
	emit := func() {
		if !emitted && s.OnCapture != nil {
			emitted = true
			s.OnCapture(c)
		}
	}
	defer emit()
	// A mixed-scheme scenario serves http:// and https:// on one port: a
	// connection that does not start with a TLS handshake record is plain
	// HTTP/1.1. A plain port tells them apart only to refuse TLS.
	plain := sc.Plain
	if sc.mixedScheme || plainOnly {
		var first [1]byte
		if _, err := io.ReadFull(conn, first[:]); err != nil {
			if isClosed(err) {
				return nil
			}
			return err
		}
		conn = &replayConn{
			Conn: conn,
			r:    io.MultiReader(bytes.NewReader(first[:]), conn),
		}
		plain = first[0] != recordTypeHandshake
	}
	if plainOnly && !plain {
		return refuseTLS(conn)
	}
	if plain {
		c.Protocol = "http/1.1"
		if err := serveHTTP1(conn, "", c, sc, log, emit); !isClosed(err) {
			return err
		}
		return nil
	}
	raw, msg, err := readClientHello(conn)
	if err != nil {
		return err
	}
	helloAt := time.Now()
	t, err := ParseClientHello(msg)
	if err != nil {
		return err
	}
	c.ClientHello = msg
	c.Records = raw
	c.Profile.TLS = t
	c.Profile.TLS.RecordVersion = profile.Code(c.RecordVersion())

	tlsConn := tls.Server(&replayConn{
		Conn: conn,
		r:    io.MultiReader(bytes.NewReader(raw), conn),
	}, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		// The ClientHello is still worth recording; browsers abort here when
		// they reject the certificate.
		return err
	}
	state := tlsConn.ConnectionState()
	c.Protocol = state.NegotiatedProtocol
	log.connection(c, state.ServerName, state.DidResume, helloAt)
	if c.Protocol == "h2" {
		err = serveHTTP2(tlsConn, state.ServerName, c, sc, log, emit)
	} else {
		err = serveHTTP1(tlsConn, state.ServerName, c, sc, log, emit)
	}
	if isClosed(err) {
		return nil
	}
	return err
}

// replayConn serves the bytes already read for the ClientHello before reading
// from the network again.
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// isClosed reports errors that mean the peer went away or the connection timed
// out, which is how every capture connection eventually ends.
func isClosed(err error) bool {
	return err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, os.ErrDeadlineExceeded)
}
