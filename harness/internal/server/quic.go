package server

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/roachadam/mimicry/harness/internal/quictrace"
)

// quicCIDLen is the length of the connection IDs the QUIC server issues:
// short header packets carry them without a length, so the recorder must
// know it.
const quicCIDLen = 8

// h3IdleTimeout is the QUIC server's max_idle_timeout. The idle timeout
// in force is the smaller of it and the client's, so a client's own
// shows.
const h3IdleTimeout = 60 * time.Second

// quicLog records a page's QUIC side: what each client address sent,
// datagram by datagram, the HTTP/3 connections the server served, and the
// TLS key log that opens their packets. It has its own lock, so the UDP
// read loop never waits on the page log's.
type quicLog struct {
	mu sync.Mutex
	// clients are the client addresses in the order their first datagram
	// arrived; datagrams each one's datagrams.
	clients   []string
	datagrams map[string][]quictrace.Received
	conns     []*h3conn
	keyLog    quictrace.KeyLog
}

func (q *quicLog) received(addr string, at time.Time, data []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.datagrams == nil {
		q.datagrams = map[string][]quictrace.Received{}
	}
	if _, ok := q.datagrams[addr]; !ok {
		q.clients = append(q.clients, addr)
	}
	q.datagrams[addr] = append(q.datagrams[addr], quictrace.Received{
		At:   at,
		Data: append([]byte(nil), data...),
	})
}

func (q *quicLog) add(h *h3conn) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.conns = append(q.conns, h)
}

// udpTap is the QUIC server's socket: it records every datagram it reads.
// It passes the socket's buffer and syscall controls through (quic-go
// sizes buffers and sets the don't-fragment bit with them), but not its
// batch reads, so every datagram goes through ReadFrom.
type udpTap struct {
	net.PacketConn
	log *quicLog
}

func (t *udpTap) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := t.PacketConn.ReadFrom(p)
	if err == nil {
		t.log.received(addr.String(), time.Now(), p[:n])
	}
	return n, addr, err
}

func (t *udpTap) SetReadBuffer(n int) error {
	if c, ok := t.PacketConn.(interface{ SetReadBuffer(int) error }); ok {
		return c.SetReadBuffer(n)
	}
	return errors.New("socket has no read buffer to set")
}

func (t *udpTap) SetWriteBuffer(n int) error {
	if c, ok := t.PacketConn.(interface{ SetWriteBuffer(int) error }); ok {
		return c.SetWriteBuffer(n)
	}
	return errors.New("socket has no write buffer to set")
}

func (t *udpTap) SyscallConn() (syscall.RawConn, error) {
	if c, ok := t.PacketConn.(syscall.Conn); ok {
		return c.SyscallConn()
	}
	return nil, errors.New("socket has no syscall connection")
}

// serveQUIC serves sc's HTTP/3 on s.PacketConn until ctx is canceled,
// then closes it and waits for the connections' handlers. Each host gets
// its TCP certificate (issue); ALPN offers only h3. The server accepts
// 0-RTT, and keeps the PSK of every ticket a client resumes with, from
// which the recorder derives the 0-RTT keys (crypto/tls logs none).
func (s *Server) serveQUIC(ctx context.Context, sc *Scenario, log *pageLog, issue func([]string) (tls.Certificate, error)) error {
	pc := s.PacketConn
	q := &log.quic
	base := s.TLSConfig.Clone()
	base.NextProtos = []string{
		"h3",
	}
	base.MinVersion = tls.VersionTLS13
	base.KeyLogWriter = &q.keyLog
	// Explicit ticket keys, so every host's configuration (a clone) and
	// base's DecryptTicket share them.
	var ticketKey [32]byte
	if _, err := rand.Read(ticketKey[:]); err != nil {
		_ = pc.Close()
		return err
	}
	base.SetSessionTicketKeys([][32]byte{
		ticketKey,
	})
	base.UnwrapSession = func(identity []byte, cs tls.ConnectionState) (*tls.SessionState, error) {
		state, err := base.DecryptTicket(identity, cs)
		if err != nil || state == nil {
			return state, err
		}
		if b, err := state.Bytes(); err == nil {
			if err := q.keyLog.AddTicket(identity, b); err != nil && s.Logf != nil {
				s.Logf("QUIC session ticket: %v", err)
			}
		}
		return state, nil
	}
	cfg := base
	if sc.multi != nil {
		if issue == nil {
			_ = pc.Close()
			return errors.New("scenario " + sc.Name + " serves several hosts and needs Server.IssueCert")
		}
		cfg = multiHostTLS(sc.multi, base, issue)
	}
	tr := &quic.Transport{
		Conn: &udpTap{
			PacketConn: pc,
			log:        q,
		},
		ConnectionIDLength: quicCIDLen,
	}
	ln, err := tr.ListenEarly(cfg, &quic.Config{
		MaxIdleTimeout: h3IdleTimeout,
		// Accepting 0-RTT shows what a resuming browser sends before its
		// handshake completes.
		Allow0RTT: true,
	})
	if err != nil {
		_ = pc.Close()
		return err
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	defer func() {
		_ = ln.Close()
		_ = tr.Close()
		_ = pc.Close()
	}()
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Go(func() {
			if err := s.serveH3(ctx, conn, sc, log); err != nil && s.Logf != nil {
				s.Logf("%s (QUIC): %v", conn.RemoteAddr(), err)
			}
		})
	}
}

// quicConns decodes every client address's datagrams into its QUIC
// connections, with the HTTP/3 the server read on each, ordered by their
// first datagram.
func (l *pageLog) quicConns() []*quictrace.Conn {
	q := &l.quic
	q.mu.Lock()
	clients := slices.Clone(q.clients)
	datagrams := make(map[string][]quictrace.Received, len(clients))
	for _, a := range clients {
		datagrams[a] = slices.Clone(q.datagrams[a])
	}
	served := slices.Clone(q.conns)
	q.mu.Unlock()
	var out []*quictrace.Conn
	for _, addr := range clients {
		decoded := quictrace.Decode(l.start, datagrams[addr], quicCIDLen, &q.keyLog)
		for _, h := range served {
			if h.addr != addr {
				continue
			}
			// The served connection is the latest decoded one that started
			// before the server accepted it.
			var match *quictrace.Conn
			for _, d := range decoded {
				if d.Millis <= h.acceptedMillis {
					match = d
				}
			}
			if match != nil && match.H3 == nil {
				match.H3, match.Close = h.snapshot()
			}
		}
		for _, d := range decoded {
			if b := d.ClientHelloBytes(); b != nil {
				t, err := ParseClientHello(b)
				switch {
				case err != nil && d.HelloError == "":
					d.HelloError = err.Error()
				case err == nil:
					d.Hello = &t
				}
			}
		}
		out = append(out, decoded...)
	}
	slices.SortStableFunc(out, func(a, b *quictrace.Conn) int {
		return cmp.Compare(a.Millis, b.Millis)
	})
	return out
}
