// Package traffic counts the bytes mimicry's sockets send and receive
// (Config.Traffic, decision 0070). Each socket keeps its own Counter,
// which a Meter sums when read, so counting is one uncontended atomic add
// per read or write. A socket's protocol and peer come from the Tag its
// dial carried in its context, set once per connection.
package traffic

import (
	"context"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
)

// Protocol is what a socket carried.
type Protocol uint32

// The protocols counted apart. Other is a socket whose protocol was never
// learnt (a connection that ended before its TLS handshake did).
const (
	Other Protocol = iota
	HTTP1
	HTTP2
	HTTP3
	WebSocket
	DNS

	numProtocols
)

// Peer is where a socket went: the host it was dialed for and the proxy it
// went through, as ProxyName gives it ("" for none).
type Peer struct {
	Host, Proxy string
}

// ProxyName is p without its credentials, path or query: scheme, host and
// port. "" for nil.
func ProxyName(p *url.URL) string {
	if p == nil {
		return ""
	}

	return p.Scheme + "://" + p.Host
}

// Bytes is what was sent and received.
type Bytes struct {
	Sent, Received int64
}

func (b *Bytes) add(o Bytes) {
	b.Sent += o.Sent
	b.Received += o.Received
}

// A Tag says what the sockets a dial opens carry and where they went. A
// dial passes it in its context (With); the socket reads it when its
// counts are summed, so the protocol may be set after the dial, once the
// handshake has picked it. Methods are safe on nil.
type Tag struct {
	peer   Peer
	proto  atomic.Uint32
	follow atomic.Pointer[Tag]
}

// NewTag returns a tag for sockets to peer carrying p.
func NewTag(p Protocol, peer Peer) *Tag {
	t := &Tag{
		peer: peer,
	}

	t.proto.Store(uint32(p))

	return t
}

// SetProtocol sets what t's sockets carry.
func (t *Tag) SetProtocol(p Protocol) {
	if t != nil {
		t.proto.Store(uint32(p))
	}
}

// Follow makes t's sockets count as o's: an https proxy connection that
// carries one tunnel counts as that tunnel.
func (t *Tag) Follow(o *Tag) {
	if t != nil && o != nil && o != t {
		t.follow.Store(o)
	}
}

// resolve is the tag t's sockets count under.
func (t *Tag) resolve() *Tag {
	for t != nil {
		f := t.follow.Load()
		if f == nil {
			return t
		}

		t = f
	}

	return nil
}

type tagKey struct{}

// With returns ctx carrying t for the sockets dialed under it; ctx itself
// when t is nil.
func With(ctx context.Context, t *Tag) context.Context {
	if t == nil {
		return ctx
	}

	return context.WithValue(ctx, tagKey{}, t)
}

// TagOf is the tag ctx carries, or nil.
func TagOf(ctx context.Context) *Tag {
	t, _ := ctx.Value(tagKey{}).(*Tag)
	return t
}

// A Meter sums the counts of the sockets it opened. Its methods are safe
// on nil, which counts nothing.
type Meter struct {
	byHost bool

	mu sync.Mutex
	// live are the open sockets; closed ones are folded into total and
	// hosts.
	live  map[*Counter]struct{}
	total [numProtocols]Bytes
	hosts map[Peer]*Bytes
}

// New returns a meter; byHost keeps a total per Peer as well.
func New(byHost bool) *Meter {
	m := &Meter{
		byHost: byHost,
		live:   map[*Counter]struct{}{},
	}

	if byHost {
		m.hosts = map[Peer]*Bytes{}
	}

	return m
}

// A Counter counts one socket's bytes. Methods are safe on nil.
type Counter struct {
	sent, received atomic.Int64

	m   *Meter
	tag *Tag
	// base is what Reset already took of the counts (guarded by m.mu).
	base Bytes
	// Each counter on a cache line of its own (64 bytes: the counts, two
	// pointers, base), so sockets never share one.
	_ [16]byte
}

// Open returns a counter for a socket dialed under ctx, registered with m;
// nil when m is nil. Close it with the socket.
func (m *Meter) Open(ctx context.Context) *Counter {
	if m == nil {
		return nil
	}

	c := &Counter{
		m:   m,
		tag: TagOf(ctx),
	}

	m.mu.Lock()

	m.live[c] = struct{}{}
	m.mu.Unlock()

	return c
}

// Sent counts n bytes written.
func (c *Counter) Sent(n int) {
	if c != nil && n > 0 {
		c.sent.Add(int64(n))
	}
}

// Received counts n bytes read.
func (c *Counter) Received(n int) {
	if c != nil && n > 0 {
		c.received.Add(int64(n))
	}
}

// Close folds the counts into the meter's totals. A second Close does
// nothing.
func (c *Counter) Close() {
	if c == nil {
		return
	}

	m := c.m

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.live[c]; !ok {
		return
	}

	delete(m.live, c)

	m.fold(c, c.take())
}

// take is what c counted since the last Reset, and moves base to it; m.mu
// is held.
func (c *Counter) take() Bytes {
	now := Bytes{
		Sent:     c.sent.Load(),
		Received: c.received.Load(),
	}

	b := Bytes{
		Sent:     now.Sent - c.base.Sent,
		Received: now.Received - c.base.Received,
	}

	c.base = now

	return b
}

// fold adds b, c's counts, to m's totals; m.mu is held.
func (m *Meter) fold(c *Counter, b Bytes) {
	m.total[c.protocol()].add(b)

	if m.byHost && b != (Bytes{}) {
		peer := c.peer()

		h := m.hosts[peer]
		if h == nil {
			h = &Bytes{}
			m.hosts[peer] = h
		}

		h.add(b)
	}
}

func (c *Counter) protocol() Protocol {
	t := c.tag.resolve()
	if t == nil {
		return Other
	}

	p := Protocol(t.proto.Load())
	if p >= numProtocols {
		return Other
	}

	return p
}

func (c *Counter) peer() Peer {
	if t := c.tag.resolve(); t != nil {
		return t.peer
	}

	return Peer{}
}

// Snapshot is a meter's counts.
type Snapshot struct {
	Protocols [numProtocols]Bytes
	// Hosts is nil unless the meter keeps them.
	Hosts map[Peer]Bytes
}

// Snapshot returns the counts so far: the closed sockets' and the live
// ones'. With reset, the meter starts again from zero.
func (m *Meter) Snapshot(reset bool) Snapshot {
	var s Snapshot
	if m == nil {
		return s
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if reset {
		// Fold the live sockets' counts in and take them with the rest.
		for c := range m.live {
			m.fold(c, c.take())
		}
	}

	s.Protocols = m.total

	if m.byHost {
		s.Hosts = make(map[Peer]Bytes, len(m.hosts))
		for p, b := range m.hosts {
			s.Hosts[p] = *b
		}
	}

	if reset {
		m.total = [numProtocols]Bytes{}
		if m.byHost {
			m.hosts = map[Peer]*Bytes{}
		}

		return s
	}

	for c := range m.live {
		b := Bytes{
			Sent:     c.sent.Load() - c.base.Sent,
			Received: c.received.Load() - c.base.Received,
		}

		s.Protocols[c.protocol()].add(b)

		// A socket with nothing since Reset adds no host.
		if m.byHost && b != (Bytes{}) {
			peer := c.peer()

			h := s.Hosts[peer]

			h.add(b)

			s.Hosts[peer] = h
		}
	}

	return s
}

// Conn is a TCP (or DNS) connection whose bytes c counts.
type Conn struct {
	net.Conn

	c *Counter
}

// Wrap returns conn counted by c; conn itself when c is nil. A UDP socket
// stays a net.PacketConn (UDPConn, PacketConn), which a DNS client tells
// datagrams from a stream by.
func Wrap(conn net.Conn, c *Counter) net.Conn {
	if c == nil {
		return conn
	}

	switch pc := conn.(type) {
	case *net.UDPConn:
		return &UDPConn{
			UDPConn: pc,
			c:       c,
		}

	case net.PacketConn:
		return &PacketConn{
			Conn: Conn{
				Conn: conn,
				c:    c,
			},
			pc: pc,
		}
	}

	return &Conn{
		Conn: conn,
		c:    c,
	}
}

// PacketConn is a connected datagram socket other than a *net.UDPConn
// (a Resolver.Dial's) whose bytes c counts.
type PacketConn struct {
	Conn

	pc net.PacketConn
}

// ReadFrom reads a datagram and counts it.
func (p *PacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := p.pc.ReadFrom(b)
	p.c.Received(n)

	return n, addr, err
}

// WriteTo writes a datagram and counts it.
func (p *PacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := p.pc.WriteTo(b, addr)
	p.c.Sent(n)

	return n, err
}

func (c *Conn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.c.Received(n)

	return n, err
}

func (c *Conn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.c.Sent(n)

	return n, err
}

// Close closes the connection and folds its counts in.
func (c *Conn) Close() error {
	err := c.Conn.Close()
	c.c.Close()

	return err
}

// UDPConn is a UDP socket whose bytes c counts. QUIC reads and writes the
// *net.UDPConn itself (UDP), in batches, counting through Sent and
// Received; its own Read, Write, ReadFrom and WriteTo count too.
type UDPConn struct {
	*net.UDPConn

	c *Counter
}

// WrapUDP returns uc counted by c; uc itself when c is nil.
func WrapUDP(uc *net.UDPConn, c *Counter) net.PacketConn {
	if c == nil {
		return uc
	}

	return &UDPConn{
		UDPConn: uc,
		c:       c,
	}
}

// UDP is the socket, for a reader that counts with Sent and Received.
func (u *UDPConn) UDP() *net.UDPConn {
	return u.UDPConn
}

// Sent counts n bytes written on the socket.
func (u *UDPConn) Sent(n int) {
	u.c.Sent(n)
}

// Received counts n bytes read from the socket.
func (u *UDPConn) Received(n int) {
	u.c.Received(n)
}

func (u *UDPConn) Read(p []byte) (int, error) {
	n, err := u.UDPConn.Read(p)
	u.c.Received(n)

	return n, err
}

func (u *UDPConn) Write(p []byte) (int, error) {
	n, err := u.UDPConn.Write(p)
	u.c.Sent(n)

	return n, err
}

// ReadFrom reads a datagram and counts it.
func (u *UDPConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := u.UDPConn.ReadFrom(p)
	u.c.Received(n)

	return n, addr, err
}

// WriteTo writes a datagram and counts it.
func (u *UDPConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	n, err := u.UDPConn.WriteTo(p, addr)
	u.c.Sent(n)

	return n, err
}

// Close closes the socket and folds its counts in.
func (u *UDPConn) Close() error {
	err := u.UDPConn.Close()
	u.c.Close()

	return err
}
