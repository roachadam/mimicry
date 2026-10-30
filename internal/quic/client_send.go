package quic

// mimicry: how a client connection sends: packets numbered, coalesced,
// padded and cut as its profile's browser does. Not in upstream; see
// UPSTREAM.md.

import (
	"encoding/binary"
	"time"

	"github.com/roachadam/mimicry/internal/quic/quicwire"
	"github.com/roachadam/mimicry/profile"
)

// plannedPacket is a first-flight Initial packet laid out in advance:
// its frames in order. The layout fills the packet exactly.
type plannedPacket struct {
	frames []plannedFrame
	// pnLen2 encodes the packet number in 2 bytes (Chrome's second
	// Initial without a token).
	pnLen2 bool
}

// plannedFrame is a CRYPTO frame (off, n), a PING, or n bytes of PADDING.
type plannedFrame struct {
	kind byte
	off  int64
	n    int64
}

// maybeSendClient is maybeSend for a client connection with a profile.
func (c *Conn) maybeSendClient(now time.Time) (next time.Time) {
	cc := c.client
	underutilized := false

	defer func() {
		c.loss.cc.setUnderutilized(c.log, underutilized)
	}()

	for {
		limit, next := c.loss.sendLimit(now)
		if limit == ccBlocked {
			return next
		}

		if !c.sendOK(now) {
			return time.Time{}
		}

		if c.corkHolds() {
			// Uncork wakes the loop.
			return time.Time{}
		}

		if !cc.planned && c.keysInitial.canWrite() && c.crypto[initialSpace].out.end > 0 {
			cc.planned = true
			cc.flight = c.planFirstFlight()
		}

		// closeSize is what a datagram carrying an Initial packet is
		// padded to: the profile's size, or its give-up close's.
		closeSize := cc.p.DatagramSize
		if c.lifetime.localErr != nil && cc.closeSize > 0 {
			closeSize = cc.closeSize
		}

		c.w.reset(c.loss.maxSendSize())

		dstConnID, ok := c.connIDState.dstConnID()
		if !ok {
			return time.Time{}
		}

		// ackOnly sends the ACKs of the server's first flight before the
		// client's Finished.
		ackOnly := cc.ackFirst
		spaceLimit := limit

		if ackOnly {
			spaceLimit = ccLimited
		}

		coalesce := cc.p.Coalesce
		packets := 0

		// Initial packet. Chrome sends none once it can send Handshake
		// packets, apart from a CONNECTION_CLOSE.
		var (
			sentInitial  *sentPacket
			initialLen   int
			initialNum   packetNumber
			paddedIn1RTT bool
			initialKeys  fixedKeys
			sendInitial  = (cc.p.AckInitial || !c.keysHandshake.canWrite() || c.lifetime.localErr != nil) && !cc.closeOnly0RTT
		)
		if c.keysInitial.canWrite() && sendInitial {
			pnum := c.clientPacketNumber(now, initialSpace)
			pnumMaxAcked := c.loss.spaces[initialSpace].maxAcked

			var plan *plannedPacket

			if len(cc.flight) > 0 && limit == ccOK && !ackOnly && c.lifetime.localErr == nil {
				plan = &cc.flight[0]
				if plan.pnLen2 {
					// Any largest-acked 0x80 below the number encodes it
					// in 2 bytes.
					pnumMaxAcked = pnum - 0x80
				}
			}

			p := longPacket{
				ptype:     packetTypeInitial,
				version:   cc.version,
				num:       pnum,
				dstConnID: dstConnID,
				srcConnID: c.connIDState.srcConnID(),
				extra:     c.initialToken(),
			}
			c.w.startProtectedLongHeaderPacket(pnumMaxAcked, p)

			if plan != nil {
				c.appendPlanned(plan)

				cc.flight = cc.flight[1:]
			} else {
				c.appendFrames(now, initialSpace, pnum, spaceLimit)
			}

			if !coalesce && cc.p.Padding != profile.PaddingTrailing && len(c.w.payload()) > 0 {
				// The packet is the datagram: pad it now.
				c.w.appendPaddingTo(closeSize)
			}

			if c.logEnabled(QLogLevelPacket) && len(c.w.payload()) > 0 {
				c.logPacketSent(packetTypeInitial, pnum, p.srcConnID, p.dstConnID, c.w.packetLen(), c.w.payload())
			}

			// Kept for repadding: a coalesced Handshake packet discards
			// the Initial keys.
			initialKeys = c.keysInitial.w
			sentInitial = c.w.finishProtectedLongHeaderPacket(pnumMaxAcked, initialKeys, p)

			if sentInitial != nil {
				packets++

				initialLen = len(c.w.datagram())
				initialNum = pnum
				cc.packetNumberUsed(pnum)
			}
		}

		// Handshake packet.
		if c.keysHandshake.canWrite() && (coalesce || packets == 0) && !cc.closeOnly0RTT {
			pnum := c.clientPacketNumber(now, handshakeSpace)
			pnumMaxAcked := c.loss.spaces[handshakeSpace].maxAcked
			p := longPacket{
				ptype:     packetTypeHandshake,
				version:   cc.version,
				num:       pnum,
				dstConnID: dstConnID,
				srcConnID: c.connIDState.srcConnID(),
			}
			c.w.startProtectedLongHeaderPacket(pnumMaxAcked, p)
			c.appendFrames(now, handshakeSpace, pnum, spaceLimit)

			if c.logEnabled(QLogLevelPacket) && len(c.w.payload()) > 0 {
				c.logPacketSent(packetTypeHandshake, pnum, p.srcConnID, p.dstConnID, c.w.packetLen(), c.w.payload())
			}

			if sent := c.w.finishProtectedLongHeaderPacket(pnumMaxAcked, c.keysHandshake.w, p); sent != nil {
				packets++

				cc.packetNumberUsed(pnum)
				c.packetSent(now, handshakeSpace, sent)

				if c.keysInitial.canWrite() {
					// "[...] a client MUST discard Initial keys when it
					// first sends a Handshake packet [...]"
					// https://www.rfc-editor.org/rfc/rfc9001.html#section-4.9.1-2
					c.discardKeys(now, initialSpace)
				}
			}
		}

		// 0-RTT packet: application data before the handshake completes.
		if cc.earlyOK.Load() && !c.keysAppData.canWrite() && !ackOnly && (coalesce || packets == 0) {
			pnum := c.clientPacketNumber(now, appDataSpace)
			pnumMaxAcked := c.loss.spaces[appDataSpace].maxAcked
			p := longPacket{
				ptype:     packetType0RTT,
				version:   cc.originalVersion, // even after an upgrade (RFC 9369, section 4.1)
				num:       pnum,
				dstConnID: dstConnID,
				srcConnID: c.connIDState.srcConnID(),
			}
			c.w.startProtectedLongHeaderPacket(pnumMaxAcked, p)
			c.appendFrames(now, appDataSpace, pnum, limit)

			if c.logEnabled(QLogLevelPacket) && len(c.w.payload()) > 0 {
				c.logPacketSent(packetType0RTT, pnum, p.srcConnID, p.dstConnID, c.w.packetLen(), c.w.payload())
			}

			if sent := c.w.finishProtectedLongHeaderPacket(pnumMaxAcked, cc.keys0RTT, p); sent != nil {
				packets++

				cc.packetNumberUsed(pnum)
				c.packetSent(now, appDataSpace, sent)
			}
		}

		// 1-RTT packet.
		if c.keysAppData.canWrite() && !ackOnly && (coalesce || packets == 0) {
			pnum := c.clientPacketNumber(now, appDataSpace)
			pnumMaxAcked := c.loss.spaces[appDataSpace].maxAcked
			c.w.start1RTTPacket(pnum, pnumMaxAcked, dstConnID)
			c.appendFrames(now, appDataSpace, pnum, limit)

			if sentInitial != nil && cc.p.Padding == profile.PaddingTrailing && len(c.w.payload()) > 0 {
				// 1-RTT packets extend to the end of the datagram: the
				// padding goes inside (Firefox).
				c.w.appendPaddingTo(closeSize)

				paddedIn1RTT = true
			}

			if c.logEnabled(QLogLevelPacket) && len(c.w.payload()) > 0 {
				c.logPacketSent(packetType1RTT, pnum, nil, dstConnID, c.w.packetLen(), c.w.payload())
			}

			c.w.spin = c.spinBit()

			sent := c.w.finish1RTTPacket(pnum, pnumMaxAcked, dstConnID, &c.keysAppData)

			c.w.spin = false

			if sent != nil {
				cc.packetNumberUsed(pnum)
				c.packetSent(now, appDataSpace, sent)
			}
		}

		buf := c.w.datagram()
		if len(buf) == 0 {
			if cc.ackFirst {
				// The first flight's ACKs are out: the Finished next.
				cc.ackFirst = false
				continue
			}

			if limit == ccOK {
				underutilized = true
			}

			return next
		}

		if sentInitial != nil {
			switch short := closeSize - len(buf); {
			case short <= 0 || paddedIn1RTT:
			case cc.p.Padding == profile.PaddingTrailing:
				// Zeros after the packets: not in any packet, but
				// accounted to the Initial one for loss recovery.
				for range short {
					buf = append(buf, 0)
				}

				sentInitial.size += short

				sentInitial.inFlight = true

			default:
				buf = repadInitial(buf, initialLen, short, sentInitial, initialNum, initialKeys)
			}

			// A Handshake packet coalesced after it discarded the Initial
			// keys: the Initial packet is not in flight.
			if c.keysInitial.canWrite() {
				c.packetSent(now, initialSpace, sentInitial)
			}
		}

		_ = c.endpoint.sendDatagram(datagram{
			b:    buf,
			path: pathAddrs{peer: c.path.peer},
		})
		c.noteFinishedSent()

		if f := cc.onFirstDatagram; f != nil {
			cc.onFirstDatagram = nil
			f()
		}
	}
}

// noteFinishedSent closes FinishedSent once the datagram just handed to
// the socket left none of the client's Finished unsent: the handshake is
// complete (the Finished written) and the Handshake CRYPTO stream has
// nothing waiting.
func (c *Conn) noteFinishedSent() {
	cc := c.client
	hs := &c.crypto[handshakeSpace]

	if cc.finishedSentClosed || !cc.handshakeComplete || hs.out.end == 0 || hs.outunsent.numRanges() > 0 {
		return
	}

	cc.finishedSentClosed = true
	close(cc.finishedSent)
}

// corkHolds reports whether a Cork holds the connection's packets: from
// the start when sending 0-RTT, otherwise once the handshake is complete;
// or a HoldFinished does, once the handshake is complete. A closing
// connection is never held.
func (c *Conn) corkHolds() bool {
	if c.lifetime.localErr != nil {
		return false
	}

	cc := c.client
	if cc.handshakeComplete && cc.finishedHolds.Load() > 0 {
		return true
	}

	return c.cork.Load() > 0 && (cc.early || cc.handshakeComplete)
}

// initialToken is the token of the connection's Initial packets.
func (c *Conn) initialToken() []byte {
	if c.retryToken != nil {
		return c.retryToken
	}

	return c.client.token
}

// clientPacketNumber returns the number of the next packet in space:
// with shared numbers, the connection's next number.
func (c *Conn) clientPacketNumber(now time.Time, space numberSpace) packetNumber {
	cc := c.client
	if cc.skipNext {
		// A PTO probe follows: one number left out first (Chrome).
		cc.skipNext = false
		if cc.p.PacketNumbers == profile.PacketNumbersShared {
			cc.nextPN++
		} else {
			c.loss.skipNumber(now, space)
		}
	}

	if cc.p.PacketNumbers == profile.PacketNumbersShared {
		c.loss.alignNumber(now, space, cc.nextPN)
	}

	return c.loss.nextNumber(space)
}

// packetNumberUsed records that a packet numbered pnum was sent.
func (cc *clientState) packetNumberUsed(pnum packetNumber) {
	if pnum >= cc.nextPN {
		cc.nextPN = pnum + 1
	}
}

// alignNumber moves space's next packet number up to num, the numbers
// skipped recorded as never sent.
func (c *lossState) alignNumber(now time.Time, space numberSpace, num packetNumber) {
	s := &c.spaces[space]
	if s.size == 0 {
		s.nextNum = max(s.nextNum, num)
		return
	}

	for s.nextNum < num {
		c.skipNumber(now, space)
	}
}

// spinBit is the latency spin bit of the next 1-RTT packet.
func (c *Conn) spinBit() bool {
	cc := c.client
	switch cc.p.SpinBit {
	case profile.SpinBitRandom:
		return cc.rng.IntN(2) == 1
	case profile.SpinBitSpin:
		return cc.spinSeen && !cc.spinRecv
	}

	return false
}

// appendPlanned writes a planned packet's frames.
func (c *Conn) appendPlanned(pp *plannedPacket) {
	for _, f := range pp.frames {
		switch f.kind {
		case frameTypeCrypto:
			if b, ok := c.w.appendCryptoFrame(f.off, int(f.n)); ok {
				c.crypto[initialSpace].sendData(f.off, b)
			}

		case frameTypePing:
			c.w.appendPingFrame()
		case frameTypePadding:
			c.w.appendPadding(int(f.n))
		}
	}
}

// appendPadding appends n PADDING frames, as far as the packet has room.
func (w *packetWriter) appendPadding(n int) {
	n = min(n, w.avail())
	if n <= 0 {
		return
	}

	for range n {
		w.b = append(w.b, frameTypePadding)
	}

	w.sent.inFlight = true
}

// repadInitial pads the Initial packet that starts buf (pktLen bytes
// long) with short more PADDING bytes, so a datagram that coalesces it
// with other packets reaches the profile's size with the padding inside the
// Initial packet (Chrome). The packet was protected already: it is opened
// and protected again.
func repadInitial(buf []byte, pktLen, short int, sent *sentPacket, pnum packetNumber, k fixedKeys) []byte {
	pkt := buf[:pktLen]
	off := 1 + 4

	off += 1 + int(pkt[off])
	off += 1 + int(pkt[off])

	tokenLen, n := quicwire.ConsumeVarint(pkt[off:])

	if n < 0 {
		return buf
	}

	off += n + int(tokenLen) // a token in a packet we wrote

	lengthOff := off
	pnumOff := off + 2

	pay, _, err := k.unprotect(pkt, pnumOff, pnum-1)
	if err != nil {
		return buf
	}

	pnumLen := int(pkt[0]&0x03) + 1
	plain := make([]byte, len(pay), len(pay)+short)
	copy(plain, pay)

	for range short {
		plain = append(plain, frameTypePadding)
	}

	out := make([]byte, 0, len(buf)+short)

	out = append(out, pkt[:pnumOff+pnumLen]...)

	plen := pnumLen + len(plain) + aeadOverhead

	out[lengthOff] = 0x40 | byte(plen>>8) // a 2-byte varint: plen < 16384
	out[lengthOff+1] = byte(plen)
	out = k.protect(out, plain, pnumOff, pnum)
	out = append(out, buf[pktLen:]...)

	sent.size += short

	sent.inFlight = true

	return out
}

// planFirstFlight lays out the Initial packets carrying the ClientHello
// as the profile cuts it; nil leaves them to the in-order path.
func (c *Conn) planFirstFlight() []plannedPacket {
	cc := c.client
	s := &c.crypto[initialSpace]

	if s.out.start != 0 {
		return nil
	}

	hello := make([]byte, s.out.end)
	s.out.copy(0, hello)

	switch cc.p.Crypto.Split {
	case profile.CryptoSplitShuffle:
		return cc.planShuffle(int64(len(hello)))
	case profile.CryptoSplitSNICut:
		return cc.planSNICut(hello)
	case profile.CryptoSplitOrdered:
		if cc.p.Crypto.FirstPacket <= 0 {
			return nil
		}

		return []plannedPacket{{
			frames: []plannedFrame{{
				kind: frameTypeCrypto,
				n:    min(int64(cc.p.Crypto.FirstPacket), int64(len(hello))),
			}},
		}}
	}

	return nil
}

// initialRoom is how many payload bytes the client's Initial packet with a
// pnLen-byte packet number holds in a datagram of the profile's size.
func (cc *clientState) initialRoom(pnLen int) int {
	hdr := 1 + 4 + 1 + len(cc.dcid) + 1 + len(cc.scid) +
		quicwire.SizeVarint(uint64(len(cc.token))) + len(cc.token) + 2 + pnLen

	return cc.p.DatagramSize - hdr - aeadOverhead
}

// cryptoFrameOverhead is a CRYPTO frame's size without its data.
func cryptoFrameOverhead(off, n int64) int {
	return 1 + quicwire.SizeVarint(uint64(off)) + quicwire.SizeVarint(uint64(n))
}

// planShuffle is CryptoSplitShuffle (Chrome): two packets, the first with
// the hello's head and tail, the second its middle, each then cut,
// pinged, padded and shuffled.
func (cc *clientState) planShuffle(total int64) []plannedPacket {
	cr := cc.p.Crypto
	if cr.Head == nil || cr.AddedFrames == nil || cr.Pings == nil {
		return nil
	}

	head := min(int64(drawRange(cc.rng, *cr.Head)), total)
	tail := max((total-4)/2-head, 0)
	mid := total - tail
	packets := [][]plannedFrame{
		{
			{kind: frameTypeCrypto, off: 0, n: head},
			{kind: frameTypeCrypto, off: mid, n: tail},
		},
		{
			{kind: frameTypeCrypto, off: head, n: mid - head},
		},
	}

	var out []plannedPacket

	for i, frames := range packets {
		frames = deleteEmpty(frames)

		pn := cc.initialPN + packetNumber(i)
		pnLen := packetNumberLength(pn, -1)
		pnLen2 := i == 1 && len(cc.token) == 0

		if pnLen2 {
			pnLen = 2
		}

		pp, ok := cc.chaosProtect(frames, cc.initialRoom(pnLen), total)
		if !ok {
			return nil
		}

		pp.pnLen2 = pnLen2
		out = append(out, pp)
	}

	return out
}

func deleteEmpty(frames []plannedFrame) []plannedFrame {
	out := frames[:0]
	for _, f := range frames {
		if f.n > 0 {
			out = append(out, f)
		}
	}

	return out
}

// chaosProtect fills a packet of room payload bytes with frames, as
// quiche's QuicChaosProtector does: the CRYPTO data split at random points,
// PING frames added, the padding spread in random runs, everything
// shuffled. Splits and PINGs only use padding there is. total is the
// hello's length.
func (cc *clientState) chaosProtect(frames []plannedFrame, room int, total int64) (plannedPacket, bool) {
	cr := cc.p.Crypto
	rng := cc.rng
	budget := room

	for _, f := range frames {
		budget -= cryptoFrameOverhead(f.off, f.n) + int(f.n)
	}

	if budget < 0 {
		return plannedPacket{}, false
	}

	maxOverhead := cryptoFrameOverhead(total, total)
	for range drawRange(rng, *cr.AddedFrames) {
		if budget < maxOverhead {
			break
		}

		j := rng.IntN(len(frames))
		f := frames[j]

		if f.n <= 1 {
			continue
		}

		cut := 1 + rng.Int64N(f.n-1)
		a := plannedFrame{
			kind: frameTypeCrypto,
			off:  f.off,
			n:    cut,
		}
		b := plannedFrame{
			kind: frameTypeCrypto,
			off:  f.off + cut,
			n:    f.n - cut,
		}

		budget -= cryptoFrameOverhead(a.off, a.n) + cryptoFrameOverhead(b.off, b.n) - cryptoFrameOverhead(f.off, f.n)

		frames[j] = a
		frames = append(frames, b)
	}

	for range min(drawRange(rng, *cr.Pings), budget) {
		frames = append(frames, plannedFrame{
			kind: frameTypePing,
		})
		budget--
	}

	var out []plannedFrame
	for _, f := range frames {
		if budget > 0 {
			if pad := rng.IntN(budget + 1); pad > 0 {
				out = append(out, plannedFrame{
					kind: frameTypePadding,
					n:    int64(pad),
				})

				budget -= pad
			}
		}

		out = append(out, f)
	}

	if budget > 0 {
		out = append(out, plannedFrame{
			kind: frameTypePadding,
			n:    int64(budget),
		})
	}

	rng.Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})

	return plannedPacket{
		frames: out,
	}, true
}

// planSNICut is CryptoSplitSNICut (Firefox, neqo's SNI slicing): the
// hello cut at the middle of its server_name host; if the two pieces make
// more than len/ceil(len/datagram size) bytes, the shorter stays whole and
// the longer is cut to fill that (the head piece keeps its start, the tail
// piece its end). The first packet carries the tail piece as planned, then
// as much of the head piece as fits after it (a resumed hello's token
// leaves less room); the rest follows in order.
func (cc *clientState) planSNICut(hello []byte) []plannedPacket {
	start, n, ok := sniHost(hello)
	if !ok {
		return nil
	}

	total := int64(len(hello))
	mid := int64(start + n/2)
	size := int64(cc.p.DatagramSize)
	limit := total / ((total + size - 1) / size)
	left, right := mid, total-mid

	if left+right > limit {
		if left <= right {
			right = max(limit-left, 0)
		} else {
			left = max(limit-right, 0)
		}
	}

	room := int64(cc.initialRoom(packetNumberLength(cc.initialPN, -1)))

	right = min(right, max(room-int64(cryptoFrameOverhead(total, total)), 0))

	if right > 0 {
		room -= int64(cryptoFrameOverhead(total-right, right)) + right
	}

	left = min(left, cryptoFit(0, room))

	frames := deleteEmpty([]plannedFrame{
		{kind: frameTypeCrypto, off: total - right, n: right},
		{kind: frameTypeCrypto, off: 0, n: left},
	})

	return []plannedPacket{{
		frames: frames,
	}}
}

// cryptoFit is the most data a CRYPTO frame at off holds in room bytes.
func cryptoFit(off, room int64) int64 {
	n := max(room-int64(cryptoFrameOverhead(off, max(room, 0))), 0)
	for int64(cryptoFrameOverhead(off, n+1))+n+1 <= room {
		n++
	}

	return n
}

// sniHost finds the host name in a ClientHello handshake message's
// server_name extension: its offset and length.
func sniHost(m []byte) (off, n int, ok bool) {
	// Handshake header 4, legacy_version 2, random 32.
	p := 4 + 2 + 32
	skip := func(lenBytes int) bool {
		if p+lenBytes > len(m) {
			return false
		}

		l := 0
		for _, b := range m[p : p+lenBytes] {
			l = l<<8 | int(b)
		}

		p += lenBytes + l

		return p <= len(m)
	}
	// session ID, cipher suites, compression methods.
	if !skip(1) || !skip(2) || !skip(1) || p+2 > len(m) {
		return 0, 0, false
	}

	end := p + 2 + int(binary.BigEndian.Uint16(m[p:]))

	p += 2

	for p+4 <= end && end <= len(m) {
		typ := binary.BigEndian.Uint16(m[p:])
		l := int(binary.BigEndian.Uint16(m[p+2:]))
		body := p + 4

		if typ == uint16(profile.ExtServerName) {
			// server_name_list length 2, name_type 1, host_name length 2.
			if l < 5 || body+5 > len(m) {
				return 0, 0, false
			}

			hl := int(binary.BigEndian.Uint16(m[body+3:]))
			if body+5+hl > len(m) {
				return 0, 0, false
			}

			return body + 5, hl, true
		}

		p = body + l
	}

	return 0, 0, false
}

// clientAckOnly makes an ACK-only 1-RTT packet ack-eliciting when the
// profile does: after QUIC.AckOnlyLimit consecutive ones, the next carries
// a MAX_DATA with the connection's current limit (Chrome). Nothing else
// does: no browser sent a PING after the server's key update.
func (c *Conn) clientAckOnly(space numberSpace) {
	p := c.client.p
	if c.w.sent.ackEliciting || space != appDataSpace || p.AckOnlyLimit <= 0 ||
		c.loss.consecutiveNonAckElicitingPackets < p.AckOnlyLimit {
		return
	}

	if p.AckOnlyFrame == profile.AckOnlyMaxData {
		c.w.appendMaxDataFrame(c.streams.inflow.sentLimit)
	}
}
