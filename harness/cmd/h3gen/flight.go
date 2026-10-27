package main

import (
	"slices"

	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// flight derives how the client answers the server's first flight: Initial
// ACKs, ACKs before Finished on full handshakes, and packet coalescing.
func flight(s *captureSet, e *collector) (ackInitial, ackBefore, coalesce bool) {
	var acks, befores []bool
	var ackConns, beforeConns []*conn
	for _, c := range s.done() {
		acked := false
		ackAt, finishedAt := -1, -1
		for _, pk := range c.packets() {
			long := pk.p.Type == quictrace.PacketInitial || pk.p.Type == quictrace.PacketHandshake
			if long && hasFrame(pk.p, quictrace.FrameAck) {
				acked = acked || pk.p.Type == quictrace.PacketInitial
				if ackAt == -1 {
					ackAt = pk.datagram
				}
			}
			if pk.p.Type == quictrace.PacketHandshake && hasFrame(pk.p, quictrace.FrameCrypto) && finishedAt == -1 {
				finishedAt = pk.datagram
			}
		}
		acks = append(acks, acked)
		ackConns = append(ackConns, c)
		before := ackAt != -1 && ackAt < finishedAt
		if c.resumed {
			if before {
				e.add(c, "a resumed handshake acknowledges before its Finished")
			}
			continue
		}
		befores = append(befores, before)
		beforeConns = append(beforeConns, c)
	}
	ackInitial = majority(s, acks, ackConns, "ack_initial", e)
	ackBefore = majority(s, befores, beforeConns, "ack_before_finished", e)
	for _, c := range s.conns {
		for _, d := range c.c.Datagrams {
			coalesce = coalesce || len(d.Packets) > 1
		}
	}
	return ackInitial, ackBefore, coalesce
}

// majority is the value most connections show; every other must be
// pinned in sampleGaps.
func majority(s *captureSet, vs []bool, conns []*conn, feature string, e *collector) bool {
	yes := 0
	for _, v := range vs {
		if v {
			yes++
		}
	}
	out := yes*2 > len(vs)
	for i, v := range vs {
		if v != out && !s.gap(conns[i], feature) {
			e.add(conns[i], "%s is %v, %d of %d connections' %v", feature, v, max(yes, len(vs)-yes), len(vs), out)
		}
	}
	return out
}

// ackDelay: constant if every completed connection's 1-RTT ACKs carry one
// ACK Delay value, the range those values span; otherwise the actual
// delay.
func ackDelay(s *captureSet) (string, *profile.Range) {
	var constants []int
	for _, c := range s.done() {
		var delays []int
		for _, pk := range c.packets() {
			if pk.p.Type != quictrace.Packet1RTT {
				continue
			}
			for _, f := range pk.p.Frames {
				if f.Type == quictrace.FrameAck && !slices.Contains(delays, int(f.Delay)) { //nolint:gosec // an ACK delay field
					delays = append(delays, int(f.Delay)) //nolint:gosec // an ACK delay field
				}
			}
		}
		if len(delays) != 1 {
			return profile.AckDelayActual, nil
		}
		constants = append(constants, delays[0])
	}
	if len(constants) == 0 {
		return profile.AckDelayActual, nil
	}
	return profile.AckDelayConstant, span(constants)
}

// spinBit: zero if no 1-RTT packet sets it, spin if every connection's
// are 0 until some point and 1 after, random otherwise (some connection
// varying both ways).
func spinBit(s *captureSet) string {
	zero, spin := true, true
	for _, c := range s.done() {
		var bits []bool
		for _, pk := range c.packets() {
			if pk.p.Type == quictrace.Packet1RTT {
				bits = append(bits, pk.p.FirstByte&0x20 != 0)
			}
		}
		if slices.Contains(bits, true) {
			zero = false
		}
		i := slices.Index(bits, true)
		if i < 1 || slices.Contains(bits[i:], false) {
			spin = false
		}
	}
	switch {
	case zero:
		return profile.SpinBitZero
	case spin:
		return profile.SpinBitSpin
	}
	return profile.SpinBitRandom
}

// tokens: a connection carries a token when an earlier one in its sample
// went to the same host and received one; the first never does. A
// browser none of whose connections carries one does not use them.
func tokens(s *captureSet, e *collector) bool {
	used := false
	for _, c := range s.conns {
		if token(c) > 0 {
			used = true
		}
	}
	if !used {
		return false
	}
	for _, c := range s.conns {
		earlier := earlierToken(s, c)
		switch got := token(c) > 0; {
		case got && !earlier:
			e.add(c, "a token, but no earlier connection to %s received one", c.c.Host)
		case !got && earlier && !s.gap(c, "tokens"):
			e.add(c, "no token, though an earlier connection to %s received one", c.c.Host)
		}
	}
	return true
}

func token(c *conn) int {
	for _, pk := range c.packets() {
		if pk.p.Type == quictrace.PacketInitial && pk.p.TokenLength != nil {
			return *pk.p.TokenLength
		}
	}
	return 0
}

// earlierToken reports whether a completed connection of c's sample to
// its host started before it: the capture server (quic-go) sends every
// completed handshake a NEW_TOKEN.
func earlierToken(s *captureSet, c *conn) bool {
	for _, o := range s.conns {
		if o.page == c.page && o.done && o.c.Host == c.c.Host && o.c.Millis < c.c.Millis {
			return true
		}
	}
	return false
}

// zeroRTT: every resumed connection sent 0-RTT packets, or none did.
func zeroRTT(s *captureSet, p *profile.Profile, e *collector) bool {
	early, resumed := 0, 0
	for _, c := range s.conns {
		if c.resumed {
			resumed++
			if c.c.EarlyData {
				early++
			}
		}
	}
	if resumed > 0 && p.TLS.Resumption == nil {
		e.add(s.conns[0], "connections resume, but tls.resumption is empty")
	}
	if early != 0 && early != resumed {
		e.add(s.conns[0], "%d of %d resumed connections sent 0-RTT", early, resumed)
	}
	return resumed > 0 && early == resumed
}

// closeReply: every completed connection the server closed got the
// client's transport CONNECTION_CLOSE NO_ERROR for frame type 0x1d in
// reply, or none did.
func closeReply(s *captureSet, e *collector) bool {
	replies, closed := 0, 0
	for _, c := range s.done() {
		if c.c.Close == nil || c.c.Close.By != "server" {
			continue
		}
		closed++
		for _, pk := range c.packets() {
			for _, f := range pk.p.Frames {
				if f.Type != quictrace.FrameConnectionClose {
					continue
				}
				if f.Code != 0x1c || f.ErrorCode == nil || *f.ErrorCode != 0 || f.FrameType != 0x1d {
					e.add(c, "replies to the server's close with CONNECTION_CLOSE %#x, error %v, frame type %#x", f.Code, f.ErrorCode, f.FrameType)
				}
				replies++
			}
		}
	}
	if replies != 0 && replies != closed {
		e.add(s.conns[0], "%d of %d connections the server closed got a reply", replies, closed)
	}
	return closed > 0 && replies == closed
}

// ackOnly finds the run length of ACK-only 1-RTT packets after which the
// browser makes the next ack-eliciting; none without such packets.
func ackOnly(s *captureSet, e *collector) (int, string) {
	limit, longest := -1, 0
	for _, c := range s.done() {
		run := 0
		for _, pk := range c.packets() {
			if pk.p.Type != quictrace.Packet1RTT {
				continue
			}
			acks, maxData, other := 0, 0, 0
			for _, f := range pk.p.Frames {
				switch f.Type {
				case quictrace.FrameAck:
					acks++
				case quictrace.FrameMaxData:
					maxData++
				case quictrace.FramePadding:
				default:
					other++
				}
			}
			switch {
			case acks == 1 && maxData == 0 && other == 0:
				run++
				longest = max(longest, run)
				continue
			case acks == 1 && maxData == 1 && other == 0:
				if limit != -1 && run != limit {
					e.add(c, "ACK and MAX_DATA after %d ACK-only packets, elsewhere after %d", run, limit)
				}
				limit = run
			}
			run = 0
		}
	}
	if limit == -1 {
		return 0, ""
	}
	if longest > limit {
		e.add(s.conns[0], "a run of %d ACK-only packets, though MAX_DATA ends runs of %d", longest, limit)
	}
	return limit, profile.AckOnlyMaxData
}
