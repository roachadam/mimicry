package udprelay

import (
	"container/heap"
	"errors"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"
)

// Dir is a direction of traffic.
type Dir int

// The two directions.
const (
	ToServer Dir = iota
	ToClient
)

// Impairment is what the relay does to one direction's datagrams. The
// probabilities are per datagram and independent; a dropped datagram is
// neither duplicated nor corrupted.
type Impairment struct {
	// Drop is the probability a datagram is lost.
	Drop float64
	// Duplicate is the probability a datagram is sent twice.
	Duplicate float64
	// Corrupt is the probability one bit of a datagram is flipped.
	Corrupt float64
	// Reorder is the probability a datagram is held for ReorderBy more
	// than the others, so the ones after it overtake it.
	Reorder   float64
	ReorderBy time.Duration
	// Delay is added to every datagram.
	Delay time.Duration
	// MaxSize drops every datagram larger than it (a path MTU); zero
	// means no cap.
	MaxSize int
	// MinSize drops every datagram smaller than it: a peer's datagrams
	// that carry nothing but ACKs, say.
	MinSize int
	// DropAt drops the datagrams at these positions in the direction's
	// sequence, per client (the first is 0).
	DropAt []int
	// Blackhole drops every datagram.
	Blackhole bool
}

// Target is the server: its IPv4 address, and its IPv6 one (optional:
// without it RebindAddress fails).
type Target struct {
	IPv4 netip.AddrPort
	IPv6 netip.AddrPort
}

// Stats is what the relay has seen.
type Stats struct {
	// Sessions is how many client addresses it has served.
	Sessions int
	// Forwarded and Dropped count datagrams per direction (a duplicate
	// counts once more).
	Forwarded [2]int
	Dropped   [2]int
	// Retries counts the server's Retry packets.
	Retries int
	// Amplification is the largest ratio, over any session, of the bytes
	// the server sent to the bytes it had been sent before the client's
	// first Handshake packet reached it (the address is unvalidated
	// until then; RFC 9000, section 8.1, allows 3).
	Amplification float64
}

// Relay forwards datagrams between the clients that send to its address
// and Target. Safe for concurrent use.
type Relay struct {
	pc   *net.UDPConn
	seed uint64

	mu       sync.Mutex
	target   Target
	imp      [2]Impairment
	sessions map[netip.AddrPort]*session
	stats    Stats
	closed   bool

	sched scheduler
	wg    sync.WaitGroup
}

// session is one client address and its upstream socket.
type session struct {
	client netip.AddrPort
	target Target
	// v6: the upstream socket is IPv6 (after RebindAddress).
	v6  bool
	up  *net.UDPConn
	rng [2]*rand.Rand
	// seen counts each direction's datagrams (Impairment.DropAt).
	seen [2]int
	// Amplification accounting until the client's first Handshake packet
	// has been forwarded.
	validated   bool
	clientBytes int
	serverBytes int
}

// New starts a relay to target on an IPv4 loopback port; the seed decides
// every impairment's draws.
func New(target Target, seed uint64) (*Relay, error) {
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.IPv4(127, 0, 0, 1),
	})
	if err != nil {
		return nil, err
	}
	r := &Relay{
		pc:       pc,
		seed:     seed,
		target:   target,
		sessions: map[netip.AddrPort]*session{},
	}
	r.sched.start(&r.wg)
	r.wg.Go(r.readClients)
	return r, nil
}

// Addr is the address clients send to.
func (r *Relay) Addr() netip.AddrPort {
	return r.pc.LocalAddr().(*net.UDPAddr).AddrPort()
}

// Set replaces direction d's impairment.
func (r *Relay) Set(d Dir, im Impairment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.imp[d] = im
}

// SetBoth sets im on both directions.
func (r *Relay) SetBoth(im Impairment) {
	r.Set(ToServer, im)
	r.Set(ToClient, im)
}

// SetTarget sends clients that are new from now on to t; existing
// sessions keep their server.
func (r *Relay) SetTarget(t Target) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.target = t
}

// Stats returns what the relay has seen so far.
func (r *Relay) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// RebindPort moves every session to a new upstream port on the same
// address: the server sees its client's port change (NAT rebinding).
func (r *Relay) RebindPort() error {
	return r.rebind(false)
}

// RebindAddress moves every session to a new upstream socket of the other
// address family: the server sees its client's address change.
func (r *Relay) RebindAddress() error {
	return r.rebind(true)
}

func (r *Relay) rebind(family bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		v6 := s.v6 != family
		if v6 && !s.target.IPv6.IsValid() {
			return errors.New("udprelay: the target has no IPv6 address")
		}
		up, err := listenUpstream(v6)
		if err != nil {
			return err
		}
		old := s.up
		s.up, s.v6 = up, v6
		_ = old.Close()
		r.wg.Go(func() {
			r.readServer(s, up)
		})
	}
	return nil
}

// Close stops the relay and closes its sockets.
func (r *Relay) Close() error {
	r.mu.Lock()
	r.closed = true
	err := r.pc.Close()
	for _, s := range r.sessions {
		_ = s.up.Close()
	}
	r.mu.Unlock()
	r.sched.stop()
	r.wg.Wait()
	return err
}

func listenUpstream(v6 bool) (*net.UDPConn, error) {
	if v6 {
		return net.ListenUDP("udp6", &net.UDPAddr{
			IP: net.IPv6loopback,
		})
	}
	return net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.IPv4(127, 0, 0, 1),
	})
}

// maxDatagram is larger than any UDP payload a QUIC peer sends here.
const maxDatagram = 64 << 10

func (r *Relay) readClients() {
	buf := make([]byte, maxDatagram)
	for {
		n, from, err := r.pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		s, err := r.session(from)
		if err != nil {
			continue
		}
		r.forward(s, ToServer, buf[:n])
	}
}

// session returns from's session, starting it on its first datagram.
func (r *Relay) session(from netip.AddrPort) (*session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.sessions[from]; s != nil {
		return s, nil
	}
	if r.closed {
		return nil, net.ErrClosed
	}
	up, err := listenUpstream(false)
	if err != nil {
		return nil, err
	}
	n := uint64(len(r.sessions))
	s := &session{
		client: from,
		target: r.target,
		up:     up,
		rng: [2]*rand.Rand{
			rand.New(rand.NewPCG(r.seed, 2*n)),   //nolint:gosec // impairments are seeded on purpose
			rand.New(rand.NewPCG(r.seed, 2*n+1)), //nolint:gosec // impairments are seeded on purpose
		},
	}
	r.sessions[from] = s
	r.stats.Sessions++
	r.wg.Go(func() {
		r.readServer(s, up)
	})
	return s, nil
}

func (r *Relay) readServer(s *session, up *net.UDPConn) {
	buf := make([]byte, maxDatagram)
	for {
		n, _, err := up.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		r.forward(s, ToClient, buf[:n])
	}
}

// forward applies direction d's impairment to b and sends what survives.
// Every datagram draws the same number of values, whatever the
// impairment, so a direction's n-th datagram always gets the same draws.
func (r *Relay) forward(s *session, d Dir, b []byte) {
	r.mu.Lock()
	im := r.imp[d]
	rng := s.rng[d]
	drop, dup, corrupt, reorder := rng.Float64(), rng.Float64(), rng.Float64(), rng.Float64()
	pos := rng.IntN(len(b))
	bit := byte(1) << rng.IntN(8)
	n := s.seen[d]
	s.seen[d]++
	if im.Blackhole || drop < im.Drop || (im.MaxSize > 0 && len(b) > im.MaxSize) || len(b) < im.MinSize || slices.Contains(im.DropAt, n) {
		r.stats.Dropped[d]++
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	out := append([]byte(nil), b...)
	if corrupt < im.Corrupt {
		out[pos] ^= bit
	}
	delay := im.Delay
	if reorder < im.Reorder {
		delay += im.ReorderBy
	}
	copies := 1
	if dup < im.Duplicate {
		copies = 2
	}
	send := func(p []byte) {
		r.send(s, d, p)
	}
	for range copies {
		if delay == 0 {
			send(out)
			continue
		}
		r.sched.add(time.Now().Add(delay), out, send)
	}
}

// send writes p in direction d, accounting for it.
func (r *Relay) send(s *session, d Dir, p []byte) {
	r.mu.Lock()
	r.stats.Forwarded[d]++
	types := longTypes(p)
	var up *net.UDPConn
	var to netip.AddrPort
	switch d {
	case ToServer:
		if !s.validated {
			s.clientBytes += len(p)
			for _, t := range types {
				if t == typeHandshake {
					s.validated = true
				}
			}
		}
		up, to = s.up, s.target.IPv4
		if s.v6 {
			to = s.target.IPv6
		}
	case ToClient:
		for _, t := range types {
			if t == typeRetry {
				r.stats.Retries++
			}
		}
		if !s.validated {
			s.serverBytes += len(p)
			if s.clientBytes > 0 {
				r.stats.Amplification = max(r.stats.Amplification, float64(s.serverBytes)/float64(s.clientBytes))
			}
		}
	}
	r.mu.Unlock()
	if d == ToServer {
		_, _ = up.WriteToUDPAddrPort(p, to)
		return
	}
	_, _ = r.pc.WriteToUDPAddrPort(p, s.client)
}

// Long header packet types (RFC 9000, section 17.2).
const (
	typeInitial   = 0
	typeHandshake = 2
	typeRetry     = 3
)

// longTypes returns the types of the long header packets coalesced at the
// start of b, stopping at a short header or anything it cannot parse.
func longTypes(b []byte) []byte {
	var types []byte
	for len(b) > 0 && b[0]&0x80 != 0 {
		if len(b) < 6 {
			break
		}
		t := (b[0] >> 4) & 3
		types = append(types, t)
		if t == typeRetry {
			break
		}
		off := 5
		for range 2 {
			if off >= len(b) {
				return types
			}
			off += 1 + int(b[off])
		}
		if t == typeInitial {
			n, l := varint(b[min(off, len(b)):])
			if l == 0 || n > uint64(len(b)) {
				return types
			}
			off += l + int(n) //nolint:gosec // bounded by len(b)
		}
		if off > len(b) {
			return types
		}
		n, l := varint(b[off:])
		if l == 0 || n > uint64(len(b)) {
			return types
		}
		off += l + int(n) //nolint:gosec // bounded by len(b)
		if off > len(b) {
			return types
		}
		b = b[off:]
	}
	return types
}

// varint decodes a QUIC variable-length integer; l is 0 if b is too short.
func varint(b []byte) (v uint64, l int) {
	if len(b) == 0 {
		return 0, 0
	}
	l = 1 << (b[0] >> 6)
	if len(b) < l {
		return 0, 0
	}
	v = uint64(b[0] & 0x3f)
	for _, c := range b[1:l] {
		v = v<<8 | uint64(c)
	}
	return v, l
}

// scheduler sends delayed datagrams when they are due, in due order, and
// in arrival order among equal times.
type scheduler struct {
	mu    sync.Mutex
	items items
	seq   uint64
	wake  chan struct{}
	done  chan struct{}
}

type item struct {
	due  time.Time
	seq  uint64
	data []byte
	send func([]byte)
}

type items []item

func (h items) Len() int {
	return len(h)
}

func (h items) Less(i, j int) bool {
	if !h[i].due.Equal(h[j].due) {
		return h[i].due.Before(h[j].due)
	}
	return h[i].seq < h[j].seq
}

func (h items) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *items) Push(x any) {
	*h = append(*h, x.(item))
}

func (h *items) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

func (s *scheduler) start(wg *sync.WaitGroup) {
	s.wake = make(chan struct{}, 1)
	s.done = make(chan struct{})
	wg.Go(s.run)
}

func (s *scheduler) stop() {
	close(s.done)
}

func (s *scheduler) add(due time.Time, data []byte, send func([]byte)) {
	s.mu.Lock()
	s.seq++
	heap.Push(&s.items, item{
		due:  due,
		seq:  s.seq,
		data: data,
		send: send,
	})
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *scheduler) run() {
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		s.mu.Lock()
		now := time.Now()
		var due []item
		for len(s.items) > 0 && !s.items[0].due.After(now) {
			due = append(due, heap.Pop(&s.items).(item))
		}
		wait := time.Hour
		if len(s.items) > 0 {
			wait = s.items[0].due.Sub(now)
		}
		s.mu.Unlock()
		for _, it := range due {
			it.send(it.data)
		}
		t.Reset(wait)
		select {
		case <-s.done:
			return
		case <-s.wake:
		case <-t.C:
		}
	}
}
