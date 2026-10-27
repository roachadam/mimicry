package e2e

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/quic/quicwire"
	"github.com/roachadam/mimicry/profile"
)

// What a browser draws per connection (CRYPTO cuts, PINGs, parameter order,
// packet numbers, GREASE and ECH lengths, ACK delays, the spin bit) cannot
// be compared with three samples without failing at random. Each is held to
// the profile's rule instead, on mimicry's connections and on every browser
// connection alike, so a wrong rule fails on the captures.

// quicRuleViolations lists how c breaks p's rules for what is random per
// connection or depends on timing; logf logs what a rule allowed only for
// the process's stalls (stalls).
func quicRuleViolations(p *profile.Profile, c *quictrace.Conn, logf func(format string, args ...any)) []string {
	r := &ruleCheck{
		p:    p,
		q:    p.QUIC,
		c:    c,
		logf: logf,
	}
	r.packetNumbers()
	r.dcid()
	r.crypto()
	r.datagramPadding()
	r.transportParameters()
	r.spinBit()
	r.ackDelays()
	r.hello()
	r.h3Grease()
	r.requestsAfterConfirmed()
	return r.out
}

type ruleCheck struct {
	p    *profile.Profile
	q    *profile.QUIC
	c    *quictrace.Conn
	logf func(format string, args ...any)
	out  []string
}

func (r *ruleCheck) add(format string, args ...any) {
	r.out = append(r.out, fmt.Sprintf(format, args...))
}

// packets lists the connection's packets in wire order.
func (r *ruleCheck) packets() []*quictrace.Packet {
	var out []*quictrace.Packet
	for di := range r.c.Datagrams {
		for pi := range r.c.Datagrams[di].Packets {
			out = append(out, &r.c.Datagrams[di].Packets[pi])
		}
	}
	return out
}

// packetNumbers: PacketNumbersShared is one rising counter from 1 (the
// second Initial's number 2 bytes long without a token); otherwise each space
// starts at 0, 1-RTT after 0-RTT's, the Initial at 1 to 1024 with
// PacketNumbersRandomInitial; the first Initial's number is minimal length.
func (r *ruleCheck) packetNumbers() {
	pkts := r.packets()
	if r.q.PacketNumbers == profile.PacketNumbersShared {
		next := uint64(1)
		initials := 0
		for _, pk := range pkts {
			if pk.PN < next {
				r.add("packet numbers: %s %d after %d, not one rising counter from 1", pk.Type, pk.PN, next-1)
				return
			}
			next = pk.PN + 1
			if pk.Type != quictrace.PacketInitial {
				continue
			}
			initials++
			switch initials {
			case 1:
				if pk.PN != 1 {
					r.add("packet numbers: the first Initial's is %d, not 1", pk.PN)
				}
			case 2:
				want := 1
				if pk.TokenLength == nil || *pk.TokenLength == 0 {
					want = 2
				}
				if pk.PNLength != want {
					r.add("packet numbers: the second Initial's in %d bytes, not %d", pk.PNLength, want)
				}
			}
		}
		return
	}
	seen := map[string]bool{}
	early := uint64(0)
	for _, pk := range pkts {
		if pk.Type == quictrace.Packet0RTT {
			early++
		}
		if seen[pk.Type] {
			continue
		}
		seen[pk.Type] = true
		want := uint64(0)
		if pk.Type == quictrace.Packet1RTT {
			want = early
		}
		switch {
		case pk.Type == quictrace.PacketInitial && r.q.PacketNumbers == profile.PacketNumbersRandomInitial:
			if pk.PN < 1 || pk.PN > 1024 {
				r.add("packet numbers: the first Initial's is %d, not from 1 to 1024", pk.PN)
			}
		case pk.PN != want:
			r.add("packet numbers: the first %s packet's is %d, not %d", pk.Type, pk.PN, want)
		}
		if pk.Type == quictrace.PacketInitial {
			if want := pnLength(pk.PN); pk.PNLength != want {
				r.add("packet numbers: the first Initial's %d in %d bytes, not %d", pk.PN, pk.PNLength, want)
			}
		}
	}
}

// pnLength is how many bytes encode packet number pn with nothing
// acknowledged (RFC 9000, appendix A.2).
func pnLength(pn uint64) int {
	switch d := pn + 1; {
	case d < 0x80:
		return 1
	case d < 0x8000:
		return 2
	case d < 0x800000:
		return 3
	}
	return 4
}

// dcid: the first Initial's Destination Connection ID is DCIDLength long,
// or with DCIDLengthBiased from DCIDLength to 20.
func (r *ruleCheck) dcid() {
	for _, pk := range r.packets() {
		if pk.Type != quictrace.PacketInitial {
			continue
		}
		lo, hi := r.q.DCIDLength, r.q.DCIDLength
		if r.q.DCIDLengthRule == profile.DCIDLengthBiased {
			hi = 20
		}
		if pk.DCIDLength < lo || pk.DCIDLength > hi {
			r.add("first Initial's DCID length %d, not from %d to %d", pk.DCIDLength, lo, hi)
		}
		return
	}
}

// helloPacket is an Initial packet of the first flight that carries
// ClientHello bytes, with its datagram.
type helloPacket struct {
	d *quictrace.Datagram
	p *quictrace.Packet
}

// cryptoRange is a CRYPTO frame's [start, end).
type cryptoRange struct {
	start, end int
}

func (r *ruleCheck) helloPackets() []helloPacket {
	flight := firstFlight(r.c)
	var out []helloPacket
	for di := range flight {
		d := &flight[di]
		for pi := range d.Packets {
			pk := &d.Packets[pi]
			if pk.Type != quictrace.PacketInitial || !slices.ContainsFunc(pk.Frames, func(f quictrace.Frame) bool {
				return f.Type == quictrace.FrameCrypto
			}) {
				continue
			}
			out = append(out, helloPacket{
				d: d,
				p: pk,
			})
		}
	}
	return out
}

func cryptoRanges(pk *quictrace.Packet) []cryptoRange {
	var out []cryptoRange
	for _, f := range pk.Frames {
		if f.Type == quictrace.FrameCrypto {
			out = append(out, cryptoRange{
				start: int(f.Offset),
				end:   int(f.Offset + f.Length),
			})
		}
	}
	return out
}

// mergeRanges joins adjacent ranges into regions, sorted.
func mergeRanges(rs []cryptoRange) []cryptoRange {
	rs = slices.Clone(rs)
	slices.SortFunc(rs, func(a, b cryptoRange) int {
		return cmp.Compare(a.start, b.start)
	})
	var out []cryptoRange
	for _, x := range rs {
		if n := len(out); n > 0 && out[n-1].end == x.start {
			out[n-1].end = x.end
			continue
		}
		out = append(out, x)
	}
	return out
}

// crypto: the ClientHello is cut into the first flight's Initial packets
// as QUIC.Crypto says, with PINGs and PADDING frames only where its rule
// adds them, and padded as QUIC.Padding says.
func (r *ruleCheck) crypto() {
	hello := r.c.ClientHelloBytes()
	pkts := r.helloPackets()
	if len(hello) == 0 || len(pkts) == 0 {
		r.add("CRYPTO: no ClientHello in Initial packets")
		return
	}
	cr := r.q.Crypto
	switch cr.Split {
	case profile.CryptoSplitOrdered:
		next := 0
		for i, pk := range pkts {
			frames := cryptoRanges(pk.p)
			if len(frames) != 1 || frames[0].start != next {
				r.add("CRYPTO: packet %d carries %v, not one frame from %d", i+1, frames, next)
				return
			}
			if i == 0 && frames[0].end != min(cr.FirstPacket, len(hello)) {
				r.add("CRYPTO: the first packet carries %d bytes, not %d", frames[0].end, cr.FirstPacket)
			}
			next = frames[0].end
		}
		if next != len(hello) {
			r.add("CRYPTO: %d of the ClientHello's %d bytes sent", next, len(hello))
		}
	case profile.CryptoSplitSNICut:
		if err := sniCutRule(hello, r.c.Host, pkts, r.q.DatagramSize); err != nil {
			r.add("CRYPTO: %v", err)
		}
	case profile.CryptoSplitShuffle:
		r.shuffleRule(len(hello), pkts)
	default:
		r.add("CRYPTO: unknown split %q", cr.Split)
	}
	for i, pk := range pkts {
		pings, runs := 0, 0
		lastPadding := false
		for j, f := range pk.p.Frames {
			switch f.Type {
			case quictrace.FramePing:
				pings++
			case quictrace.FramePadding:
				runs++
				lastPadding = j == len(pk.p.Frames)-1
			}
		}
		if cr.Split != profile.CryptoSplitShuffle && pings > 0 {
			r.add("CRYPTO: packet %d has %d PINGs; only the shuffle adds them", i+1, pings)
		}
		switch r.q.Padding {
		case profile.PaddingTrailing:
			if runs > 0 {
				r.add("padding: packet %d has PADDING frames; the datagram is padded after its packets", i+1)
			}
		case profile.PaddingEnd:
			if runs > 1 || runs == 1 && !lastPadding {
				r.add("padding: packet %d's PADDING is not one run at its end", i+1)
			}
		}
	}
}

// datagramPadding: every datagram with an Initial packet is padded to
// QUIC.DatagramSize, with PaddingTrailing inside a final 1-RTT packet, else
// after the packets. Whether a 1-RTT packet shares the datagram is timing, so
// PADDING presence is not compared with the samples.
func (r *ruleCheck) datagramPadding() {
	giveUp := r.q.Loss.Fresh
	if r.c.Resumed {
		giveUp = r.q.Loss.Resumed
	}
	for i := range r.c.Datagrams {
		d := &r.c.Datagrams[i]
		initial, closes := false, false
		for _, pk := range d.Packets {
			initial = initial || pk.Type == quictrace.PacketInitial
			closes = closes || slices.ContainsFunc(pk.Frames, func(f quictrace.Frame) bool {
				return f.Type == quictrace.FrameConnectionClose
			})
		}
		if !initial {
			continue
		}
		last := &d.Packets[len(d.Packets)-1]
		switch {
		case d.Size != r.q.DatagramSize && (!closes || d.Size != giveUp.DatagramSize):
			r.add("padding: datagram %d carries an Initial packet in %d bytes, not %d", i+1, d.Size, r.q.DatagramSize)
		case r.q.Padding == profile.PaddingTrailing && last.Type == quictrace.Packet1RTT &&
			(d.Trailing > 0 || len(last.Frames) == 0 || last.Frames[len(last.Frames)-1].Type != quictrace.FramePadding):
			r.add("padding: datagram %d's 1-RTT packet after its Initial does not end in the padding", i+1)
		}
	}
}

// sniCutRule checks neqo's SNI slicing: the first packet carries the tail,
// then the head, of the ClientHello cut mid server_name, both together
// len/n bytes; the head piece fills the datagram (but for cryptoLeftover's
// byte); the rest follows in order, one frame per packet.
func sniCutRule(hello []byte, host string, pkts []helloPacket, datagramSize int) error {
	length := len(hello)
	at := bytes.Index(hello, []byte(host))
	if at < 0 {
		return fmt.Errorf("the host %s is not in the ClientHello", host)
	}
	mid := at + len(host)/2
	if len(pkts) < 2 {
		return fmt.Errorf("%d Initial packets carry the ClientHello", len(pkts))
	}
	first := cryptoRanges(pkts[0].p)
	if len(first) != 2 || first[0].end != length || first[1].start != 0 {
		return fmt.Errorf("the first packet's CRYPTO frames %v are not the tail, then the head", first)
	}
	right, left := first[0], first[1]
	full := pkts[0].d.Size == datagramSize && (pkts[0].d.Trailing == 0 || cryptoLeftover(pkts[0].d))
	ok := false
	for n := 1; n <= 4 && !ok; n++ {
		limit := length / n
		switch {
		case right.start == mid:
			// The tail piece is whole; the head was planned to fill the
			// limit and got that or what fit.
			planned := limit - (length - mid)
			ok = left.end == planned || left.end < planned && full
		case left.end == mid || left.end < mid && full:
			// The head piece is whole, or as much of it as fit; the tail
			// was cut to fill the limit.
			ok = length-right.start == limit-mid
		}
	}
	if !ok {
		return fmt.Errorf("the first packet's CRYPTO frames %v do not cut %d bytes at the host's middle %d", first, length, mid)
	}
	next := left.end
	for i, pk := range pkts[1:] {
		frames := cryptoRanges(pk.p)
		if len(frames) != 1 || frames[0].start != next {
			return fmt.Errorf("packet %d's CRYPTO frames %v do not continue at %d", i+2, frames, next)
		}
		next = frames[0].end
	}
	if next != right.start {
		return fmt.Errorf("the middle ends at %d, not %d", next, right.start)
	}
	return nil
}

// cryptoLeftover reports whether d's trailing bytes are too few for one more
// byte of CRYPTO data, whose Length field would grow (67 bytes hold 63 data
// bytes in a 66-byte frame). neqo then pads after the packets, leaving its
// first packet a byte short.
func cryptoLeftover(d *quictrace.Datagram) bool {
	if d.Trailing == 0 || len(d.Packets) == 0 {
		return false
	}
	frames := d.Packets[len(d.Packets)-1].Frames
	if len(frames) == 0 || frames[len(frames)-1].Type != quictrace.FrameCrypto {
		return false
	}
	n := frames[len(frames)-1].Length
	return d.Trailing < 1+quicwire.SizeVarint(n+1)-quicwire.SizeVarint(n)
}

// shuffleRule checks quiche's chaos protection: two packets, the first with
// the head [0, h) and the tail, floor((len-4)/2) bytes in all, the second the
// middle; each with at most AddedFrames.Max extra CRYPTO frames and
// Pings.Min to Pings.Max PINGs while padding is left.
func (r *ruleCheck) shuffleRule(length int, pkts []helloPacket) {
	cr := r.q.Crypto
	if cr.Head == nil || cr.AddedFrames == nil || cr.Pings == nil {
		r.add("CRYPTO: the shuffle has no head, added frames or PINGs range")
		return
	}
	if len(pkts) != 2 {
		r.add("CRYPTO: %d Initial packets carry the ClientHello, not 2", len(pkts))
		return
	}
	r1, r2 := mergeRanges(cryptoRanges(pkts[0].p)), mergeRanges(cryptoRanges(pkts[1].p))
	if len(r1) != 2 || len(r2) != 1 || r1[0].start != 0 || r1[0].end != r2[0].start || r2[0].end != r1[1].start || r1[1].end != length {
		r.add("CRYPTO: packets carry %v and %v, not head and tail, then the middle", r1, r2)
		return
	}
	if !cr.Head.Contains(r1[0].end) {
		r.add("CRYPTO: head of %d bytes, not %d to %d", r1[0].end, cr.Head.Min, cr.Head.Max)
	}
	if got := r1[0].end + length - r1[1].start; got != (length-4)/2 {
		r.add("CRYPTO: the first packet carries %d bytes, not %d", got, (length-4)/2)
	}
	for i, pk := range pkts {
		base := len(r1)
		if i == 1 {
			base = len(r2)
		}
		frames, pings, padded := 0, 0, 0
		for _, f := range pk.p.Frames {
			switch f.Type {
			case quictrace.FrameCrypto:
				frames++
			case quictrace.FramePing:
				pings++
			case quictrace.FramePadding:
				padded += f.Count
			}
		}
		if frames > base+cr.AddedFrames.Max {
			r.add("CRYPTO: packet %d has %d frames, more than %d regions + %d", i+1, frames, base, cr.AddedFrames.Max)
		}
		if pings > cr.Pings.Max || padded > 0 && pings < cr.Pings.Min {
			r.add("CRYPTO: packet %d has %d PINGs (padding %d), not %d to %d", i+1, pings, padded, cr.Pings.Min, cr.Pings.Max)
		}
	}
}

// transportParameters: the order TransportParameterOrder says (those
// marked Last last, in order), each GREASE parameter's value at most its
// MaxLength, version_information's versions (in order unless shuffled),
// initial_rtt a positive integer. Which parameters and their other values
// are features: every sample agrees on them.
func (r *ruleCheck) transportParameters() {
	if len(r.c.TransportParameters) == 0 {
		r.add("transport parameters: none")
		return
	}
	var want []profile.TransportParameter
	var movable, last []string
	var grease *profile.TransportParameter
	for i := range r.q.TransportParameters {
		tp := &r.q.TransportParameters[i]
		if tp.Resumed && !r.c.Resumed {
			continue
		}
		if tp.ID == profile.TPInitialRTT && !r.c.Resumed {
			continue
		}
		want = append(want, *tp)
		id := tpName(uint64(tp.ID), tp.ID == profile.GREASETransportParameter)
		if tp.Last {
			last = append(last, id)
		} else {
			movable = append(movable, id)
		}
		if tp.ID == profile.GREASETransportParameter {
			grease = tp
		}
	}
	var got []string
	for _, tp := range r.c.TransportParameters {
		got = append(got, tpName(tp.ID, tp.Grease))
	}
	if len(got) != len(want) {
		r.add("transport parameters: %v, not %d (%v then %v)", got, len(want), movable, last)
		return
	}
	gotMovable, gotLast := got[:len(movable)], got[len(movable):]
	if !slices.Equal(gotLast, last) {
		r.add("transport parameters: %v last, not %v", gotLast, last)
	}
	switch r.q.TransportParameterOrder {
	case profile.TransportParameterOrderFixed:
		if !slices.Equal(gotMovable, movable) {
			r.add("transport parameters: order %v, not %v", gotMovable, movable)
		}
	case profile.TransportParameterOrderShuffled:
		if !slices.Equal(slices.Sorted(slices.Values(gotMovable)), slices.Sorted(slices.Values(movable))) {
			r.add("transport parameters: %v, not a permutation of %v", gotMovable, movable)
		}
	case profile.TransportParameterOrderRotated:
		rotated := false
		for k := range movable {
			if slices.Equal(gotMovable, slices.Concat(movable[k:], movable[:k])) {
				rotated = true
			}
		}
		if !rotated {
			r.add("transport parameters: order %v, not a rotation of %v", gotMovable, movable)
		}
	default:
		r.add("transport parameters: unknown order %q", r.q.TransportParameterOrder)
	}
	for _, tp := range r.c.TransportParameters {
		switch {
		case tp.Grease:
			if grease == nil || tp.Length > grease.MaxLength {
				r.add("transport parameters: GREASE %#x of %d bytes, the profile's at most %v", tp.ID, tp.Length, grease)
			}
		case tp.ID == uint64(profile.TPInitialRTT):
			if tp.Value == nil || *tp.Value == 0 {
				r.add("transport parameters: initial_rtt %s is no positive integer", tp.Hex)
			}
		case tp.ID == uint64(profile.TPVersionInformation):
			r.versions(tp, want)
		}
	}
}

func tpName(id uint64, grease bool) string {
	if grease {
		return "GREASE"
	}
	return fmt.Sprintf("0x%x", id)
}

// versions checks version_information against the profile's: the chosen
// version, then the available ones in order, or in any order with
// ShuffleVersions; GREASE stands for any reserved version.
func (r *ruleCheck) versions(tp quictrace.TransportParameter, want []profile.TransportParameter) {
	i := slices.IndexFunc(want, func(w profile.TransportParameter) bool {
		return w.ID == profile.TPVersionInformation
	})
	if i < 0 {
		return
	}
	w := want[i]
	var got, wantV []string
	for _, v := range tp.Versions {
		n, err := strconv.ParseUint(v, 16, 32)
		switch {
		case err != nil:
			got = append(got, v)
		case profile.IsGREASEVersion(uint32(n)):
			got = append(got, "GREASE")
		default:
			got = append(got, fmt.Sprintf("%08x", n))
		}
	}
	for _, v := range w.Versions {
		if v == profile.GREASEVersion {
			wantV = append(wantV, "GREASE")
			continue
		}
		wantV = append(wantV, fmt.Sprintf("%08x", uint32(v)))
	}
	ok := len(got) == len(wantV) && len(got) > 0 && got[0] == wantV[0]
	if ok && w.ShuffleVersions {
		ok = slices.Equal(slices.Sorted(slices.Values(got[1:])), slices.Sorted(slices.Values(wantV[1:])))
	} else if ok {
		ok = slices.Equal(got, wantV)
	}
	if !ok {
		r.add("transport parameters: version_information %v, not %v (shuffled %v)", got, wantV, w.ShuffleVersions)
	}
}

// spinMinPackets is how many 1-RTT packets a connection needs before a
// random spin bit must have shown both values: all alike by chance once in
// 2^31.
const spinMinPackets = 32

// spinBit: SpinBitZero sends 0; SpinBitSpin, against a server that never
// spins (quic-go), 0 until some packet and 1 from it on; SpinBitRandom
// both values on a long enough connection.
func (r *ruleCheck) spinBit() {
	var spins []int
	for _, pk := range r.packets() {
		if pk.Type == quictrace.Packet1RTT && pk.FirstByte != 0 {
			spins = append(spins, int(pk.FirstByte>>5&1))
		}
	}
	switch r.q.SpinBit {
	case profile.SpinBitZero:
		if slices.Contains(spins, 1) {
			r.add("spin bit: %v, not always 0", spins)
		}
	case profile.SpinBitSpin:
		if i := slices.Index(spins, 1); i == 0 || i > 0 && slices.Contains(spins[i:], 0) {
			r.add("spin bit: %v, not 0 until some packet and 1 from it on", spins)
		}
	case profile.SpinBitRandom:
		if len(spins) >= spinMinPackets && (!slices.Contains(spins, 0) || !slices.Contains(spins, 1)) {
			r.add("spin bit: %d packets all %d, not random", len(spins), spins[0])
		}
	default:
		r.add("spin bit: unknown rule %q", r.q.SpinBit)
	}
}

// ackDelays: with AckDelayActual every 1-RTT ACK delay is at most twice the
// announced max_ack_delay (25 ms default; browser timers overshoot), less
// mimicry's stall time; with AckDelayConstant they all equal it.
func (r *ruleCheck) ackDelays() {
	type ack struct {
		delay uint64
		// at is when the datagram carrying it arrived (µs).
		at int64
	}
	var acks []ack
	for _, d := range r.c.Datagrams {
		for _, pk := range d.Packets {
			if pk.Type != quictrace.Packet1RTT {
				continue
			}
			for _, f := range pk.Frames {
				if f.Type == quictrace.FrameAck {
					acks = append(acks, ack{
						delay: f.Delay,
						at:    d.Micros,
					})
				}
			}
		}
	}
	if len(acks) == 0 {
		return
	}
	switch r.q.AckDelay {
	case profile.AckDelayActual:
		maxDelay, exponent := uint64(25000), uint64(3)
		for _, tp := range r.q.TransportParameters {
			switch {
			case tp.ID == profile.TPMaxAckDelay && tp.Value != nil:
				maxDelay = *tp.Value * 1000
			case tp.ID == profile.TPAckDelayExponent && tp.Value != nil:
				exponent = *tp.Value
			}
		}
		for _, a := range acks {
			us := a.delay << exponent
			if us <= 2*maxDelay {
				continue
			}
			var stalled uint64
			if !r.c.Start.IsZero() {
				sent := r.c.Start.Add(time.Duration(a.at) * time.Microsecond)
				stalled = uint64(stalls.stalled(sent.Add(-time.Duration(us)*time.Microsecond), sent).Microseconds())
			}
			if us-min(stalled, us) > 2*maxDelay {
				r.add("ACK delay %d µs, %d µs of it with the process stalled, more than twice max_ack_delay %d µs", us, stalled, maxDelay)
				return
			}
			r.logf("ACK delay %d µs, more than twice max_ack_delay %d µs, of which the process was stalled %d µs", us, maxDelay, stalled)
		}
	case profile.AckDelayConstant:
		rg := r.q.AckDelayConstant
		delays := make([]uint64, 0, len(acks))
		for _, a := range acks {
			delays = append(delays, a.delay)
		}
		for _, d := range delays {
			if d != delays[0] || rg == nil || !rg.Contains(int(d)) {
				r.add("ACK delays %v, not one value from %v", delays, rg)
				return
			}
		}
	default:
		r.add("ACK delay: unknown rule %q", r.q.AckDelay)
	}
}

// helloECH is the ECH extension the profile's QUIC hello sends.
func helloECH(p *profile.Profile) *profile.ECH {
	for _, e := range p.TLS.QUIC.Replace {
		if e.Type == profile.ExtEncryptedClientHello {
			return e.ECH
		}
	}
	for _, e := range p.TLS.Extensions {
		if e.Type == profile.ExtEncryptedClientHello {
			return e.ECH
		}
	}
	return nil
}

// hello: GREASE ECH's cipher suite and payload length are among the
// profile's (a resumed hello's lengths ResumedECHPayloadLengths), and a
// shuffled hello keeps tls.quic.last last, before pre_shared_key. The rest
// of the hello is compareHello's.
func (r *ruleCheck) hello() {
	h := r.c.Hello
	if h == nil {
		return
	}
	ech := helloECH(r.p)
	for _, e := range h.Extensions {
		if e.ECH == nil {
			continue
		}
		if ech == nil {
			r.add("ClientHello: ECH the profile does not send")
			continue
		}
		lengths := ech.PayloadLengths
		if r.c.Resumed {
			lengths = r.p.TLS.QUIC.ResumedECHPayloadLengths
		}
		for _, n := range e.ECH.PayloadLengths {
			if !slices.Contains(lengths, n) {
				r.add("ClientHello: ECH payload length %d, not one of %v", n, lengths)
			}
		}
		for _, s := range e.ECH.CipherSuites {
			if !slices.Contains(ech.CipherSuites, s) {
				r.add("ClientHello: ECH cipher suite %+v, not one of %+v", s, ech.CipherSuites)
			}
		}
	}
	q := r.p.TLS.QUIC
	if q.ExtensionOrder != profile.ExtensionOrderShuffled || len(q.Last) == 0 {
		return
	}
	var types []profile.Code
	for _, e := range h.Extensions {
		types = append(types, e.Type)
	}
	if n := len(types); n > 0 && types[n-1] == profile.ExtPreSharedKey {
		types = types[:n-1]
	}
	if len(types) < len(q.Last) || !slices.Equal(types[len(types)-len(q.Last):], q.Last) {
		r.add("ClientHello: extensions end %v, not %v", types[max(len(types)-len(q.Last), 0):], q.Last)
	}
}

// h3Grease: a GREASE frame on the control stream only where the profile
// sends one, at most its MaxLength long.
func (r *ruleCheck) h3Grease() {
	if r.c.H3 == nil || r.p.HTTP3 == nil {
		return
	}
	g := r.p.HTTP3.GreaseFrame
	for _, s := range r.c.H3.Streams {
		for _, f := range s.Frames {
			switch {
			case !f.Grease:
			case g == nil:
				r.add("HTTP/3: a GREASE frame the profile does not send")
			case int(f.Length) > g.MaxLength:
				r.add("HTTP/3: GREASE frame of %d bytes, the profile's at most %d", f.Length, g.MaxLength)
			}
		}
	}
}

// pendingRequest bounds how soon after the Finished a full handshake's
// first request leaves when it was waiting for the connection (Safari's
// 0.7–2.4 ms; requests made later by script or the parser come 47 ms on).
const pendingRequest = 20 * time.Millisecond

// requestsAfterConfirmed: with HTTP3.RequestsAfterConfirmed, a full
// handshake's first request that was waiting for the connection leaves
// only after the client acknowledged a 1-RTT packet newer than its first
// 1-RTT packet after the Finished did: one the server sent after the
// Finished, its HANDSHAKE_DONE (the server's packets are not captured).
func (r *ruleCheck) requestsAfterConfirmed() {
	if r.p.HTTP3 == nil || !r.p.HTTP3.RequestsAfterConfirmed || r.c.Resumed || r.c.EarlyData {
		return
	}
	finished := int64(-1)
	first, newer := true, false
	base := int64(-1)
	for di := range r.c.Datagrams {
		d := &r.c.Datagrams[di]
		for _, pk := range d.Packets {
			if pk.Type == quictrace.PacketHandshake && finished < 0 && slices.ContainsFunc(pk.Frames, func(f quictrace.Frame) bool {
				return f.Type == quictrace.FrameCrypto
			}) {
				finished = d.Micros
			}
			if pk.Type != quictrace.Packet1RTT || finished < 0 {
				continue
			}
			largest := int64(-1)
			request := false
			for _, f := range pk.Frames {
				switch {
				case f.Type == quictrace.FrameAck:
					largest = int64(f.Largest)
				case f.Type == quictrace.FrameStream && f.Stream != nil && *f.Stream%4 == 0:
					request = true
				}
			}
			if request {
				waiting := time.Duration(d.Micros-finished)*time.Microsecond <= pendingRequest
				if waiting && (first || !newer && largest <= base) {
					r.add("HTTP/3: the first request left before the client acknowledged a packet the server sent after its Finished (requests_after_confirmed)")
				}
				return
			}
			if first {
				base, first = largest, false
			} else if largest > base {
				newer = true
			}
		}
	}
}
