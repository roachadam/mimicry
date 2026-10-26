package quic

// mimicry: how a client connection probes, gives up and abandons, as its
// profile's browser does (profile.QUIC.Loss). Not in upstream; see
// UPSTREAM.md.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/profile"
)

// ErrHandshakeTimeout is the error of a client connection whose handshake
// the profile gave up on (QUICLoss's give-up): Dial returns it, and a
// connection Dial returned at once (0-RTT) ends with it (Conn.Err,
// Conn.WaitHandshake). errors.Is matches it, whatever the connection sent.
var ErrHandshakeTimeout = errors.New("quic: handshake timed out")

// probeLayout is what a client's PTO probe carries while its first flight
// is unacknowledged.
type probeLayout int

const (
	// probeUnacked: every unacknowledged CRYPTO byte again, in order.
	probeUnacked probeLayout = iota
	// probeOldest (Chrome): the CRYPTO data of the oldest Initial packet
	// in flight not yet probed, cut and padded again as the first flight
	// was (chaos protection), one packet per probe.
	probeOldest
	// probeFlight (Firefox): the first flight's packets again, laid out
	// as they were.
	probeFlight
)

// clientLoss is a client connection's loss recovery, give-up and abandon
// rules, from its profile (QUICLoss).
type clientLoss struct {
	// initialPTO is the probe timeout before an RTT sample (zero:
	// upstream's, from a 333 ms initial RTT); cachedRTT, when set, the
	// RTT the connection starts from instead (QUICLoss.CachedRTT).
	// ackDelay: the peer's max_ack_delay counts in every probe timeout
	// (QUICLoss.PTOAckDelay).
	initialPTO time.Duration
	cachedRTT  time.Duration
	ackDelay   bool
	// probes is how many probe packets a PTO sends, layout what a probe
	// of the first flight carries.
	probes int
	layout probeLayout
	// skip: one packet number is skipped before each probe.
	skip bool
	// giveUpClose is what the client sends when its handshake times out
	// (Config.HandshakeTimeout).
	giveUpClose giveUpClose
	// abandon0RTT: Abandon sends an application close in a 0-RTT packet
	// of its own (QUICLoss.AbandonClose CloseZeroRTT).
	abandon0RTT bool
}

// clientProbesPerPTO is how many probe packets a PTO sends where the
// profile does not lay them out: the two RFC 9002 (section 6.2.4) allows,
// so one lost datagram does not cost another, doubled, timeout (Firefox's
// 1-RTT probes: the data and a PING).
const clientProbesPerPTO = 2

// giveUpClose is what a client sends when it gives up on its handshake.
type giveUpClose struct {
	// send: a CONNECTION_CLOSE; otherwise the connection ends silently.
	send bool
	// app: an application close (APPLICATION_ERROR in Initial packets,
	// the close itself in 0-RTT ones); otherwise a transport NO_ERROR
	// with reason, "{elapsed_us}" replaced by the microseconds since the
	// attempt started.
	app    bool
	reason string
	// size is what the datagram is padded to (zero: the profile's
	// datagram size).
	size int
}

// applyLoss takes p's loss rules (QUICLoss, the rest of the client's
// failure behaviour with them: keep-alives and paths, version
// negotiation's error) into cc, and the handshake's give-up into config.
func (cc *clientState) applyLoss(p *profile.QUIC, cfg DialConfig, config *Config) error {
	l := &p.Loss
	cl := clientLoss{
		initialPTO:  time.Duration(l.InitialPTOMillis) * time.Millisecond,
		probes:      clientProbesPerPTO,
		skip:        l.SkipPacketNumber,
		ackDelay:    l.PTOAckDelay,
		abandon0RTT: l.AbandonClose == profile.CloseZeroRTT,
	}
	if l.CachedRTT && cc.resumed && cfg.CachedRTT > 0 {
		cl.cachedRTT = cfg.CachedRTT
	}
	switch l.Probe {
	case profile.ProbeOnePacket:
		cl.layout, cl.probes = probeOldest, 1
	case profile.ProbeFlight:
		cl.layout = probeFlight
	case "":
	default:
		return fmt.Errorf("quic: probe %q", l.Probe)
	}
	g := l.Fresh
	if cc.resumed {
		g = l.Resumed
	}
	switch g.Close {
	case profile.CloseTransport:
		cl.giveUpClose = giveUpClose{
			send:   true,
			reason: g.Reason,
		}
	case profile.CloseApplication:
		cl.giveUpClose = giveUpClose{
			send: true,
			app:  true,
		}
	case "":
	default:
		return fmt.Errorf("quic: give-up close %q", g.Close)
	}
	cl.giveUpClose.size = g.DatagramSize
	// Without a give-up of its own the handshake lasts as long as the
	// connection may stay idle (Firefox's fresh attempts); Safari's is
	// not measured, and its idle timeout bounds it too.
	if giveUp := time.Duration(g.AfterMillis) * time.Millisecond; giveUp > 0 {
		config.HandshakeTimeout = giveUp
	} else if cc.local.maxIdleTimeout > 0 {
		config.HandshakeTimeout = cc.local.maxIdleTimeout
	}
	cc.loss = cl
	cc.path = newClientPath(p)
	cc.downgradeErr = errVersionNegotiationError
	if e := p.VersionNegotiation.DowngradeError; e != 0 {
		cc.downgradeErr = transportError(e)
	}
	return nil
}

// clientInitLoss starts the connection's probe timeouts where the profile
// does: from the RTT measured on the connection it resumes, or at the
// profile's first probe timeout until an RTT sample; in newConn, before
// anything is sent.
func (c *Conn) clientInitLoss(now time.Time) {
	cc := c.client
	cc.started = now
	cc.probedThrough = -1
	c.loss.ptoAckDelay = cc.loss.ackDelay
	switch {
	case cc.loss.cachedRTT > 0:
		// RFC 9002, section 6.2.2: rttvar half the cached RTT.
		c.loss.rtt.smoothedRTT = cc.loss.cachedRTT
		c.loss.rtt.rttvar = cc.loss.cachedRTT / 2
	case cc.loss.initialPTO > 0:
		c.loss.firstPTO = cc.loss.initialPTO
	}
}

// clientPTOExpired prepares a client's PTO probes as its profile sends
// them. Upstream's single probe resends the CRYPTO stream's first
// unacknowledged range alone, which a browser's ClientHello outgrows: with
// the server's ACKs lost, its end would wait for loss detection forever;
// so unless the profile lays the probe out, every unacknowledged CRYPTO
// byte is queued again.
func (c *Conn) clientPTOExpired() {
	cc := c.client
	c.loss.ptoProbes = cc.loss.probes
	if cc.loss.skip {
		cc.skipNext = true
	}
	if c.clientPlanProbe() {
		return
	}
	for space := range c.crypto {
		s := &c.crypto[space]
		if s.out.start == s.out.end {
			continue
		}
		s.outunsent.add(s.out.start, s.out.end)
		for _, a := range s.outacked {
			s.outunsent.sub(a.start, a.end)
		}
	}
}

// clientPlanProbe lays out the probe of a first flight nothing of which
// has been acknowledged, as the profile does; false leaves it to the
// in-order path.
func (c *Conn) clientPlanProbe() bool {
	cc := c.client
	if cc.loss.layout == probeUnacked || !c.keysInitial.canWrite() || c.keysHandshake.canWrite() ||
		c.loss.spaces[initialSpace].maxAcked >= 0 || len(c.crypto[initialSpace].outacked) > 0 {
		return false
	}
	switch cc.loss.layout {
	case probeFlight:
		if cc.firstFlight == nil {
			// The packets in flight at the first PTO are the first
			// flight.
			s := &c.loss.spaces[initialSpace]
			for i := range s.size {
				sent := s.nth(i)
				if sent.state != sentPacketSent {
					continue
				}
				if frames := sentCryptoFrames(sent); len(frames) > 0 {
					cc.firstFlight = append(cc.firstFlight, plannedPacket{
						frames: frames,
					})
				}
			}
		}
		if len(cc.firstFlight) == 0 {
			return false
		}
		cc.flight = append(cc.flight[:0], cc.firstFlight...)
		c.loss.ptoProbes = len(cc.flight)
		return true
	case probeOldest:
		pp, ok := c.planOldestProbe()
		if !ok {
			return false
		}
		cc.flight = append(cc.flight[:0], pp)
		return true
	}
	return false
}

// planOldestProbe lays out Chrome's probe: the CRYPTO data of the oldest
// Initial packet in flight after the last one probed, chaos-protected
// again in one packet.
func (c *Conn) planOldestProbe() (plannedPacket, bool) {
	cc := c.client
	s := &c.loss.spaces[initialSpace]
	for i := range s.size {
		sent := s.nth(i)
		if sent.state != sentPacketSent || sent.num <= cc.probedThrough {
			continue
		}
		frames := sentCryptoFrames(sent)
		if len(frames) == 0 {
			continue
		}
		cc.probedThrough = sent.num
		pn := c.loss.nextNumber(initialSpace)
		if cc.p.PacketNumbers == profile.PacketNumbersShared {
			pn = cc.nextPN
		}
		if cc.loss.skip {
			pn++
		}
		return cc.chaosProtect(frames, cc.initialRoom(packetNumberLength(pn, -1)), c.crypto[initialSpace].out.end)
	}
	return plannedPacket{}, false
}

// sentCryptoFrames reads back the CRYPTO frames an Initial packet carried
// (its other recorded frames are ACKs), leaving its record unread.
func sentCryptoFrames(sent *sentPacket) []plannedFrame {
	n := sent.n
	defer func() {
		sent.n = n
	}()
	var frames []plannedFrame
	for !sent.done() {
		switch sent.next() {
		case frameTypeAck, frameTypeAckECN:
			sent.nextInt()
		case frameTypeCrypto:
			start, end := sent.nextRange()
			frames = append(frames, plannedFrame{
				kind: frameTypeCrypto,
				off:  start,
				n:    end - start,
			})
		default:
			return nil
		}
	}
	return frames
}

// clientGiveUp ends a client connection whose handshake took longer than
// its profile allows, sending what the browser sends.
func (c *Conn) clientGiveUp(now time.Time) {
	cc := c.client
	g := cc.loss.giveUpClose
	if !g.send {
		c.abortImmediately(now, ErrHandshakeTimeout)
		return
	}
	c.setFinalError(ErrHandshakeTimeout)
	var err error
	if g.app {
		err = &ConnectionCloseError{
			Code: h3NoError,
		}
	} else {
		elapsed := strconv.FormatInt(now.Sub(cc.started).Microseconds(), 10)
		err = localTransportError{
			code:   errNo,
			reason: strings.ReplaceAll(g.reason, "{elapsed_us}", elapsed),
		}
	}
	cc.closeSize = g.size
	c.enterClosing(now, err)
}

// Abandon ends the connection as a browser drops a QUIC attempt it no
// longer needs, its TCP connection having won the race
// (QUICLoss.AbandonClose): before the handshake completes it sends at
// most an application CONNECTION_CLOSE (H3_NO_ERROR) in one 0-RTT packet
// of its own, where the profile does and the attempt sent 0-RTT (Safari),
// and ends at once; after, it is Abort(nil).
func (c *Conn) Abandon() {
	c.sendMsg(func(now time.Time, c *Conn) {
		cc := c.client
		switch {
		case !c.isAlive():
		case cc.handshakeComplete:
			c.enterClosing(now, localTransportError{
				code: errNo,
			})
		case cc.loss.abandon0RTT && cc.earlyOK.Load() && !c.keysAppData.canWrite():
			cc.closeOnly0RTT = true
			c.setFinalError(errConnClosed)
			c.enterClosing(now, &ConnectionCloseError{
				Code: h3NoError,
			})
		default:
			c.abortImmediately(now, errConnClosed)
		}
	})
}

// h3NoError is HTTP/3's H3_NO_ERROR (RFC 9114, section 8.1).
const h3NoError = 0x100

// WaitHandshake waits for the handshake to complete: nil once it has, the
// connection's error if it ended first (ErrHandshakeTimeout when the
// profile gave up), or ctx's.
func (c *Conn) WaitHandshake(ctx context.Context) error {
	return c.waitReady(ctx)
}
