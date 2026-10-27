package quic

// mimicry: a client's reaction to a dead path, per profile.QUIC.Loss
// (Chrome migrates, Safari closes, Firefox idles out). Not in upstream.

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/roachadam/mimicry/profile"
)

// ErrPathDegraded is the error of a client connection that closed itself
// because its path stopped answering (Safari: its keep-alives went
// unanswered). Requests pending on it never reached the server's answer;
// the browser sends them again on a new connection.
var ErrPathDegraded = errors.New("quic: path stopped answering")

// clientPath is a client connection's rules for a path that stops
// answering, from its profile.
type clientPath struct {
	// degradePTOs: the path is degrading once the oldest unacknowledged
	// 1-RTT packet has waited this many probe timeouts (zero: never);
	// the client then moves to a new local port (Chrome).
	degradePTOs int
	// closeAfter: the path is dead once nothing has arrived for this
	// long while a request waits; the client closes with closeReason
	// (Safari: 12 s, "keepalive limit reached"). Zero: never.
	closeAfter  time.Duration
	closeReason string
	// keepAlive: while a request waits, a PING this long after the
	// moment keepAliveFrom names (profile.KeepAliveFromSent, ...Idle or
	// ...Received), then again every keepAlive.
	keepAlive     time.Duration
	keepAliveFrom string
}

// newClientPath is p's path rules.
func newClientPath(p *profile.QUIC) clientPath {
	cp := clientPath{
		keepAlive:     time.Duration(p.KeepAlive.PeriodMillis) * time.Millisecond,
		keepAliveFrom: p.KeepAlive.From,
	}
	if d := p.Loss.PathDegrading; d != nil {
		switch d.Action {
		case profile.PathMigrate:
			cp.degradePTOs = d.AfterPTOs
		case profile.PathClose:
			cp.closeAfter = time.Duration(d.AfterMillis) * time.Millisecond
			cp.closeReason = d.CloseReason
		}
	}
	return cp
}

// clientPathState is a client connection's path watch, on its loop.
type clientPathState struct {
	// socket is the connection's socket when it may move (degradePTOs),
	// newPath opens another (DialConfig.NewPath).
	socket  *swapPacketConn
	newPath func(context.Context) (net.PacketConn, error)
	// degraded: the path counted as degrading and the client reacted;
	// again only once the server acknowledges a packet after
	// degradedAcked. moving: a new socket is being opened.
	degraded      bool
	degradedAcked packetNumber
	moving        bool
	// challenge is the PATH_CHALLENGE sent on the new path.
	challenge pathChallengeData
	// quietSince is the last packet received (or the request, if later),
	// which the client's close counts from; pingSince what its
	// keep-alives count from, pings how many fell due since, ping: one is
	// to be sent. lastSent is when the last ack-eliciting packet went,
	// firstSent the first one after the last packet received.
	quietSince time.Time
	pingSince  time.Time
	pings      int
	ping       bool
	lastSent   time.Time
	firstSent  time.Time
}

// clientPathTimer is when the path watch next acts; zero for never.
func (c *Conn) clientPathTimer() time.Time {
	cc := c.client
	if !c.handshakeConfirmed.isSet() || !c.isAlive() {
		return time.Time{}
	}
	var t time.Time
	if d := c.degradeDelay(); d > 0 {
		if cc.pathState.degraded && c.loss.spaces[appDataSpace].maxAcked > cc.pathState.degradedAcked {
			// The server answered since: watch again.
			cc.pathState.degraded = false
		}
		if sent := c.oldestUnacked(); sent != nil && !cc.pathState.degraded && !cc.pathState.moving {
			t = sent.time.Add(d)
		}
	}
	if cc.keepAlive.Load() {
		ps := &cc.pathState
		c.clientQuietSince()
		if every := c.keepAliveEvery(); every > 0 {
			t = firstTime(t, ps.pingSince.Add(time.Duration(ps.pings+1)*every))
		}
		if cc.path.closeAfter > 0 {
			t = firstTime(t, ps.quietSince.Add(cc.path.closeAfter))
		}
	}
	return t
}

// keepAliveEvery is the client's keep-alive period: its profile's, or half
// the negotiated idle timeout if that is shorter (a server's short one;
// neqo's stream keep-alive, which the interop rows rely on); zero for none.
func (c *Conn) keepAliveEvery() time.Duration {
	every := c.client.path.keepAlive
	if half := c.idle.idleDuration / 2; every > 0 && half > 0 {
		every = min(every, half)
	}
	return every
}

// degradeDelay is how long the oldest unacknowledged 1-RTT packet waits
// before the path counts as degrading; zero for never.
func (c *Conn) degradeDelay() time.Duration {
	n := c.client.path.degradePTOs
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * c.loss.ptoBasePeriod()
}

// clientQuietSince moves the close's count to the last packet received,
// or to when the requests started if that is later, and the keep-alives'
// to the moment the profile counts from; a count that moves starts again.
func (c *Conn) clientQuietSince() {
	cc := c.client
	ps := &cc.pathState
	recv := c.acks[appDataSpace].maxRecvTime
	var since time.Time
	if ns := cc.keepAliveSince.Load(); ns > 0 {
		since = time.Unix(0, ns)
	}
	ps.quietSince = later(recv, since)
	var from time.Time
	switch cc.path.keepAliveFrom {
	case profile.KeepAliveFromSent:
		from = ps.lastSent
	case profile.KeepAliveFromIdle:
		// The idle timer restarts on a receipt and on the first
		// ack-eliciting packet sent after it (RFC 9000, section 10.1).
		from = recv
		if ps.firstSent.After(recv) {
			from = ps.firstSent
		}
	default:
		from = recv
	}
	from = later(from, since)
	if !from.Equal(ps.pingSince) {
		ps.pingSince = from
		ps.pings = 0
	}
}

// later is the later of a and b.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// clientSent records an ack-eliciting packet sent, for the keep-alives.
func (c *Conn) clientSent(now time.Time) {
	ps := &c.client.pathState
	if !ps.lastSent.After(c.acks[appDataSpace].maxRecvTime) {
		ps.firstSent = now
	}
	ps.lastSent = now
}

// oldestUnacked is the oldest ack-eliciting 1-RTT packet still in flight.
func (c *Conn) oldestUnacked() *sentPacket {
	s := &c.loss.spaces[appDataSpace]
	for i := range s.size {
		sent := s.nth(i)
		if sent.state == sentPacketSent && sent.ackEliciting && sent.inFlight {
			return sent
		}
	}
	return nil
}

// clientPathAdvance acts on the path watch's timer.
func (c *Conn) clientPathAdvance(now time.Time) {
	cc := c.client
	if !c.handshakeConfirmed.isSet() || !c.isAlive() {
		return
	}
	if d := c.degradeDelay(); d > 0 && !cc.pathState.degraded && !cc.pathState.moving {
		if sent := c.oldestUnacked(); sent != nil && !now.Before(sent.time.Add(d)) {
			cc.pathState.degraded = true
			cc.pathState.degradedAcked = c.loss.spaces[appDataSpace].maxAcked
			c.clientMove()
		}
	}
	if !cc.keepAlive.Load() {
		return
	}
	ps := &cc.pathState
	c.clientQuietSince()
	if cc.path.closeAfter > 0 && !now.Before(ps.quietSince.Add(cc.path.closeAfter)) {
		c.setFinalError(ErrPathDegraded)
		c.enterClosing(now, localTransportError{
			code:   errNo,
			reason: cc.path.closeReason,
		})
		return
	}
	if every := c.keepAliveEvery(); every > 0 && !now.Before(ps.pingSince.Add(time.Duration(ps.pings+1)*every)) {
		ps.pings++
		ps.ping = true
	}
}

// appendClientKeepAlive adds the keep-alive PING that fell due, unless
// the packet is ack-eliciting already; false if it did not fit.
func (c *Conn) appendClientKeepAlive() bool {
	ps := &c.client.pathState
	if !ps.ping {
		return true
	}
	if !c.w.sent.ackEliciting && !c.w.appendPingFrame() {
		return false
	}
	ps.ping = false
	return true
}

// clientMove starts the move to a new local port (Chrome's port
// migration): a new socket, opened off the loop; clientMigrate switches
// to it. A connection without a spare connection ID of the server's stays.
func (c *Conn) clientMove() {
	ps := &c.client.pathState
	if ps.socket == nil || ps.newPath == nil || len(c.connIDState.remote) < 2 {
		return
	}
	ps.moving = true
	newPath := ps.newPath
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), clientMoveTimeout)
		defer cancel()
		pc, err := newPath(ctx)
		// The loop owns pc once the message runs; until then, this.
		ran := make(chan struct{})
		c.sendMsg(func(now time.Time, c *Conn) {
			defer close(ran)
			c.client.pathState.moving = false
			if err == nil {
				c.clientMigrate(now, pc)
			}
		})
		select {
		case <-ran:
		case <-c.donec:
			// The connection ended before it could move.
			select {
			case <-ran:
			default:
				if err == nil {
					_ = pc.Close()
				}
			}
		}
	}()
}

// clientMoveTimeout bounds opening the new socket.
const clientMoveTimeout = 5 * time.Second

// clientMigrate moves the connection to pc, as Chrome does: the next of
// the server's connection IDs, the old one retired, a PATH_CHALLENGE
// padded to the profile's datagram size, then a PING; what was in flight
// is sent again on the new path as its probe timeouts expire.
func (c *Conn) clientMigrate(now time.Time, pc net.PacketConn) {
	cc := c.client
	ps := &cc.pathState
	if !c.isAlive() || len(c.connIDState.remote) < 2 {
		_ = pc.Close()
		return
	}
	npc, err := wrapPacketConn(pc)
	if err != nil {
		_ = pc.Close()
		return
	}
	ps.socket.swap(npc)
	c.path.local = unmapAddrPort(npc.LocalAddr())
	c.connIDState.retireActiveRemote()
	if _, err := rand.Read(ps.challenge[:]); err != nil {
		return
	}
	c.sendPathProbe(now, func() bool {
		if !c.w.appendPathChallengeFrame(ps.challenge) {
			return false
		}
		c.w.appendPaddingTo(cc.p.DatagramSize)
		return true
	})
	c.sendPathProbe(now, c.w.appendPingFrame)
}

// sendPathProbe sends one 1-RTT packet of its own with the frames add
// writes.
func (c *Conn) sendPathProbe(now time.Time, add func() bool) {
	dstConnID, ok := c.connIDState.dstConnID()
	if !ok {
		return
	}
	c.w.reset(c.loss.maxSendSize())
	pnum := c.clientPacketNumber(now, appDataSpace)
	pnumMaxAcked := c.loss.spaces[appDataSpace].maxAcked
	c.w.start1RTTPacket(pnum, pnumMaxAcked, dstConnID)
	if !add() {
		c.w.abandonPacket()
		return
	}
	c.w.spin = c.spinBit()
	sent := c.w.finish1RTTPacket(pnum, pnumMaxAcked, dstConnID, &c.keysAppData)
	c.w.spin = false
	if sent == nil {
		return
	}
	c.client.packetNumberUsed(pnum)
	c.packetSent(now, appDataSpace, sent)
	_ = c.endpoint.sendDatagram(datagram{
		b:    c.w.datagram(),
		path: pathAddrs{peer: c.path.peer},
	})
}

// clientPathResponse handles a PATH_RESPONSE: the answer to the client's
// challenge validates its new path; any other is ignored.
func (c *Conn) clientPathResponse(data pathChallengeData) {
	ps := &c.client.pathState
	if data == ps.challenge {
		ps.challenge = pathChallengeData{}
	}
}

// retireActiveRemote stops using the server's connection ID in use: the
// next one takes over, the old one is retired (RETIRE_CONNECTION_ID).
func (s *connIDState) retireActiveRemote() {
	seq := s.remote[0].seq
	s.remote = s.remote[1:]
	s.remoteRetiring.add(seq, seq+1)
	s.needSend = true
}

// wrapPacketConn is pc as the endpoint reads and writes it.
func wrapPacketConn(pc net.PacketConn) (packetConn, error) {
	if uc, ok := pc.(*net.UDPConn); ok {
		return newNetUDPConn(uc)
	}
	return newNetPacketConn(pc)
}

// swapPacketConn is a client's socket a migration replaces: reads go on
// on the new socket, writes go to it, and the old one is closed.
type swapPacketConn struct {
	mu     sync.Mutex
	cur    packetConn
	gen    int
	closed bool
}

func (s *swapPacketConn) current() (packetConn, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur, s.gen
}

// swap makes pc the socket and closes the old one.
func (s *swapPacketConn) swap(pc packetConn) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = pc.Close()
		return
	}
	old := s.cur
	s.cur = pc
	s.gen++
	s.mu.Unlock()
	_ = old.Close()
}

func (s *swapPacketConn) Read(f func(*datagram)) error {
	for {
		pc, gen := s.current()
		err := pc.Read(f)
		s.mu.Lock()
		again := !s.closed && s.gen != gen
		s.mu.Unlock()
		if !again {
			return err
		}
	}
}

func (s *swapPacketConn) Write(d datagram) error {
	pc, _ := s.current()
	return pc.Write(d)
}

func (s *swapPacketConn) Close() error {
	s.mu.Lock()
	s.closed = true
	pc := s.cur
	s.mu.Unlock()
	return pc.Close()
}

func (s *swapPacketConn) LocalAddr() netip.AddrPort {
	pc, _ := s.current()
	return pc.LocalAddr()
}

func (s *swapPacketConn) LocalAddrFor(remote netip.AddrPort) (netip.AddrPort, error) {
	pc, _ := s.current()
	return pc.LocalAddrFor(remote)
}
