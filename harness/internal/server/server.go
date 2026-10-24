package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// Server accepts TLS connections, records what each client sends, and answers
// requests so the client finishes normally.
type Server struct {
	// TLSConfig must hold the certificate. The server offers h2 and http/1.1
	// over its own copy.
	TLSConfig *tls.Config
	// OnCapture receives each connection's capture once, as soon as the first
	// request is recorded, or when the connection ends without one. It is
	// called from the connection's goroutine.
	OnCapture func(*capture.Capture)
	// Logf receives connection errors. Nil discards them.
	Logf func(format string, args ...any)
	// ConnTimeout bounds each connection. Zero means one minute.
	ConnTimeout time.Duration
	// Scenario says how HTTP/2 connections are answered. Nil means
	// DefaultScenario.
	Scenario *Scenario
	// IssueCert issues a certificate for hosts. Replay scenarios need it:
	// they serve many hostnames, each with its own certificate.
	IssueCert func(hosts []string) (tls.Certificate, error)
}

// Serve handles connections from ln until ctx is canceled, then closes the
// open ones and waits for their handlers to return. Clients that stay running,
// like Safari, would otherwise hold connections open until ConnTimeout.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	sc := s.Scenario
	if sc == nil {
		sc = DefaultScenario
	}
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
	if sc.multi != nil {
		if s.IssueCert == nil {
			return errors.New("scenario " + sc.Name + " serves several hosts and needs Server.IssueCert")
		}
		cfg = multiHostTLS(sc.multi, cfg, s.IssueCert)
	}
	log := &pageLog{}
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
			if err := s.handle(ctx, conn, cfg, sc, log); err != nil && s.Logf != nil {
				s.Logf("%s: %v", conn.RemoteAddr(), err)
			}
		})
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn, cfg *tls.Config, sc *Scenario, log *pageLog) error {
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
	if sc.Plain {
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
	t, err := ParseClientHello(msg)
	if err != nil {
		return err
	}
	c.ClientHello = msg
	c.Profile.TLS = t

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
	log.connection(c, state.ServerName, state.DidResume)
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
