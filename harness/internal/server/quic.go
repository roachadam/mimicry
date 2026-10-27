package server

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"os"
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

// quicLog records a page's QUIC side: what each client address sent and
// was sent, datagram by datagram, the HTTP/3 connections the server
// served, and the TLS key log that opens their packets. It has its own
// lock, so the UDP read loop never waits on the page log's.
type quicLog struct {
	mu sync.Mutex
	// clients are the client addresses in the order their first datagram
	// arrived; datagrams each one's datagrams, both ways.
	clients   []string
	datagrams map[string][]quictrace.Received
	conns     []*h3conn
	keyLog    quictrace.KeyLog
	// silenced are the client addresses whose datagrams the server drops
	// both ways (a connection gone silent, or one started after a
	// blackhole). Once blackholed is set, every address without an open
	// connection (live counts them) is silenced as its next datagram
	// arrives, so no new connection is answered.
	silenced   map[string]bool
	blackholed bool
	live       map[string]int
	// silencedOnce is set once a request has silenced its connection.
	silencedOnce bool
	// keyLogFile is where a server other than quic-go (aioquic) logs its
	// TLS secrets, read into keyLog when the page is taken.
	keyLogFile string
}

// received records a datagram a client sent and reports whether the server
// drops it.
func (q *quicLog) received(addr string, at time.Time, data []byte) bool {
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
	return q.droppedLocked(addr)
}

// sent records a datagram the server sends to a client, unless the server
// drops it; it reports whether it does.
func (q *quicLog) sent(addr string, at time.Time, data []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.droppedLocked(addr) {
		return true
	}
	if _, ok := q.datagrams[addr]; !ok {
		// Nothing came from there first; not a client of this page.
		return false
	}
	q.datagrams[addr] = append(q.datagrams[addr], quictrace.Received{
		At:     at,
		Data:   append([]byte(nil), data...),
		Server: true,
	})
	return false
}

func (q *quicLog) droppedLocked(addr string) bool {
	if q.silenced[addr] {
		return true
	}
	if q.blackholed && q.live[addr] == 0 {
		q.silenceLocked(addr)
		return true
	}
	return false
}

func (q *quicLog) silenceLocked(addr string) {
	if q.silenced == nil {
		q.silenced = map[string]bool{}
	}
	q.silenced[addr] = true
}

// silence drops every later datagram from and to addr, and reports
// whether it did: only the page's first silence does, so a browser that
// repeats the request on a new QUIC connection is answered there.
func (q *quicLog) silence(addr string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.silencedOnce {
		return false
	}
	q.silencedOnce = true
	q.silenceLocked(addr)
	return true
}

// blackhole silences addr and, from now on, every address that has no open
// connection.
func (q *quicLog) blackhole(addr string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.silenceLocked(addr)
	q.blackholed = true
}

func (q *quicLog) add(h *h3conn) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.conns = append(q.conns, h)
	if q.live == nil {
		q.live = map[string]int{}
	}
	q.live[h.addr]++
}

// ended records that h's connection ended.
func (q *quicLog) ended(h *h3conn) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.live[h.addr]--
}

// udpTap is the QUIC server's socket: it records every datagram it reads
// and writes, and drops those the page's quicLog says to. It passes the
// socket's buffer and syscall controls through (quic-go sizes buffers and
// sets the don't-fragment bit with them), but not its batch reads or
// writes, so every datagram goes through ReadFrom and WriteTo.
type udpTap struct {
	net.PacketConn
	log *quicLog
}

func (t *udpTap) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := t.PacketConn.ReadFrom(p)
		if err != nil || !t.log.received(addr.String(), time.Now(), p[:n]) {
			return n, addr, err
		}
	}
}

func (t *udpTap) WriteTo(p []byte, addr net.Addr) (int, error) {
	if t.log.sent(addr.String(), time.Now(), p) {
		return len(p), nil
	}
	return t.PacketConn.WriteTo(p, addr)
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
// which the recorder derives the 0-RTT keys (crypto/tls logs none). A
// blackhole scenario's socket answers nothing; an aioquic one relays to
// aioquic (serveAioquic).
func (s *Server) serveQUIC(ctx context.Context, sc *Scenario, log *pageLog, issue func([]string) (tls.Certificate, error)) error {
	pc := s.PacketConn
	q := &log.quic
	if sc.h3.aioquic {
		return s.serveAioquic(ctx, log, issue)
	}
	if sc.h3.blackhole {
		q.mu.Lock()
		q.blackholed = true
		q.mu.Unlock()
	}
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
	qcfg := &quic.Config{
		MaxIdleTimeout: cmp.Or(sc.h3.idleTimeout, h3IdleTimeout),
		// Accepting 0-RTT shows what a resuming browser sends before its
		// handshake completes.
		Allow0RTT: true,
		// nil is quic-go's own (versions 1 and 2, preferring 1).
		Versions: slices.Clone(sc.h3.versions),
	}
	if sc.h3.tooEarly {
		// Which requests came in 0-RTT, from quic-go's own handling of
		// each packet.
		tr.ConnContext = earlyContext
		qcfg.Tracer = earlyTracer
	}
	ln, err := tr.ListenEarly(cfg, qcfg)
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
// first datagram. The addresses are decoded together, so a connection
// the client moves to another address (a new port) keeps its datagrams.
// Each is rederived from its wire data (Conn.Rederive). An aioquic
// server's key log is read from its file first.
func (l *pageLog) quicConns() []*quictrace.Conn {
	q := &l.quic
	q.mu.Lock()
	var datagrams []quictrace.Received
	for _, a := range q.clients {
		for _, r := range q.datagrams[a] {
			r.Addr = a
			datagrams = append(datagrams, r)
		}
	}
	served := slices.Clone(q.conns)
	keyLogFile := q.keyLogFile
	q.mu.Unlock()
	if keyLogFile != "" {
		if b, err := os.ReadFile(keyLogFile); err == nil { //nolint:gosec // the server's own temporary file
			_, _ = q.keyLog.Write(b)
		}
	}
	slices.SortStableFunc(datagrams, func(a, b quictrace.Received) int {
		return a.At.Compare(b.At)
	})
	decoded := quictrace.Decode(l.start, datagrams, quicCIDLen, &q.keyLog)
	for _, h := range served {
		// The served connection is the latest decoded one from its address
		// that started before the server accepted it.
		var match *quictrace.Conn
		for _, d := range decoded {
			if d.Addr == h.addr && d.Millis <= h.acceptedMillis {
				match = d
			}
		}
		if match != nil && match.H3 == nil {
			match.H3, match.Close = h.snapshot()
		}
	}
	for _, d := range decoded {
		d.Rederive()
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
	slices.SortStableFunc(decoded, func(a, b *quictrace.Conn) int {
		return cmp.Compare(a.Millis, b.Millis)
	})
	return decoded
}
