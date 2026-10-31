package main

import (
	"fmt"
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// zeroRTT derives quic.zeroRtt from every resumed connection, the h3
// captures' and the edge captures' (Safari resumed only on the edge
// pages): every one sent 0-RTT packets, or none did. Where the h3
// captures have no resumed connection to derive the first flight's rules
// from, the edge ones' first flights must follow the full handshakes'
// (checkResumed).
func (s *edgeSet) zeroRTT(q *profile.QUIC, e *collector) bool {
	early, all := 0, 0
	for _, c := range s.set.conns {
		if c.resumed {
			all++

			if sent0RTT(c.c) {
				early++
			}
		}
	}

	ruled := all > 0

	var firsts []*conn

	for _, p := range s.pages {
		for _, a := range p.attempts {
			if !a.resumed {
				continue
			}

			all++

			if sent0RTT(a.c) {
				early++
			}

			firsts = append(firsts, p.firstFlight(a))
		}
	}

	if early != 0 && early != all {
		edgeErr(e, s.set.name, "%d of %d resumed connections sent 0-RTT", early, all)
	}

	if !ruled {
		s.checkResumed(q, firsts, e)
	}

	return all > 0 && early == all
}

// firstFlight is a's connection with its first flight alone (its later
// probes, keep-alives and close follow rules of their own).
func (p *edgePage) firstFlight(a *attempt) *conn {
	c := *a.c

	c.Datagrams = nil
	for _, d := range a.flights[0] {
		c.Datagrams = append(c.Datagrams, *d)
	}

	out := p.conn(a)

	out.c = &c

	return out
}

// conn is a as the h3 captures' connections are, named alike.
func (p *edgePage) conn(a *attempt) *conn {
	return &conn{
		at:       fmt.Sprintf("%s, QUIC connection %d", p.at, a.index+1),
		scenario: p.scenario,
		page:     p.page,
		c:        a.c,
		resumed:  a.resumed,
		done:     a.c.H3 != nil,
	}
}

// sent0RTT reports whether c sent a 0-RTT packet.
func sent0RTT(c *quictrace.Conn) bool {
	for _, d := range c.Datagrams {
		for _, p := range d.Packets {
			if p.Type == quictrace.Packet0RTT {
				return true
			}
		}
	}

	return false
}

// checkResumed holds the first flights of the edge pages' resumed
// connections to q's rules, as the h3 captures' full handshakes gave
// them: padding, packet numbers, the ClientHello's cut into CRYPTO frames
// and the transport parameters (the set, values and order).
func (s *edgeSet) checkResumed(q *profile.QUIC, conns []*conn, e *collector) {
	if len(conns) == 0 {
		return
	}

	set := &captureSet{
		name:     s.set.name,
		p:        s.set.p,
		conns:    conns,
		usedGaps: s.set.usedGaps,
	}

	if size, pad := padding(set, e); size > q.DatagramSize || pad != q.Padding {
		edgeErr(e, s.set.name, "resumed connections pad to %d bytes (%s), the profile's %d (%s)", size, pad, q.DatagramSize, q.Padding)
	}

	if got := packetNumbers(set, e); got != q.PacketNumbers {
		edgeErr(e, s.set.name, "resumed connections number packets %q, the profile %q", got, q.PacketNumbers)
	}

	if got := crypto(set, q.DatagramSize, e); got.Split != q.Crypto.Split || got.FirstPacket != q.Crypto.FirstPacket {
		edgeErr(e, s.set.name, "resumed connections cut the ClientHello %s (first packet %d), the profile %s (%d)", got.Split, got.FirstPacket, q.Crypto.Split, q.Crypto.FirstPacket)
	}

	for _, c := range conns {
		resumedParameters(q, c, e)
	}
}

// resumedParameters holds c's transport parameters to q's: every one q
// lists (its Resumed ones too) and no other, each value as q has it, and
// the order q's rule allows.
func resumedParameters(q *profile.QUIC, c *conn, e *collector) {
	byID := map[profile.TransportParameterID]profile.TransportParameter{}
	for _, tp := range q.TransportParameters {
		byID[tp.ID] = tp
	}

	var ids, cycle, last, onlyResumed, sentResumed []profile.TransportParameterID
	for _, tp := range q.TransportParameters {
		switch {
		case tp.Resumed:
			onlyResumed = append(onlyResumed, tp.ID)
		case tp.Last:
			last = append(last, tp.ID)
		default:
			cycle = append(cycle, tp.ID)
		}
	}

	for _, tp := range c.c.TransportParameters {
		got := profileParameter(c, tp, q.SCIDLength, e)

		want, ok := byID[got.ID]
		if !ok {
			e.add(c, "sends transport parameter %s, which the profile does not", got.ID)

			continue
		}

		merged := want
		mergeParameter(c, &merged, got, e)

		switch {
		case merged.MaxLength != want.MaxLength:
			e.add(c, "GREASE transport parameter of %d bytes, beyond the profile's %d", got.MaxLength, want.MaxLength)
		case merged.ShuffleVersions != want.ShuffleVersions:
			e.add(c, "version_information's available versions %v in another order than the profile's", got.Versions)
		}

		if want.Resumed {
			sentResumed = append(sentResumed, got.ID)
		} else {
			ids = append(ids, got.ID)
		}
	}

	if !sameSet(sentResumed, onlyResumed) {
		e.add(c, "resumed-only transport parameters %v, not the profile's %v", sentResumed, onlyResumed)
	}

	if !sameSet(ids, slices.Concat(cycle, last)) {
		e.add(c, "resumed transport parameters %v, not the profile's %v", ids, slices.Concat(cycle, last))

		return
	}

	switch q.TransportParameterOrder {
	case profile.TransportParameterOrderFixed:
		if !slices.Equal(ids, cycle) {
			e.add(c, "transport parameters in order %v, not the profile's fixed %v", ids, cycle)
		}

	case profile.TransportParameterOrderRotated:
		n := len(ids) - len(last)
		if !slices.Equal(ids[n:], last) || !isRotation(ids[:n], cycle) {
			e.add(c, "transport parameters in order %v, not a rotation of %v then %v", ids, cycle, last)
		}
	}
}
