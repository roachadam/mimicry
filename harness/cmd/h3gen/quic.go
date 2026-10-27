package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// deriveQUIC derives the quic section from every connection, checking
// each value against all of them.
func deriveQUIC(s *captureSet, p *profile.Profile) (*profile.QUIC, error) {
	q := &profile.QUIC{
		Version: 1,
	}
	e := &collector{}
	for _, c := range s.conns {
		if c.c.Version != "00000001" {
			e.add(c, "QUIC version %s, not 1", c.c.Version)
		}
		for _, pk := range c.packets() {
			if pk.p.Version != "" && pk.p.Version != "00000001" {
				e.add(c, "a %s packet of version %s", pk.p.Type, pk.p.Version)
			}
		}
	}
	q.DatagramSize, q.Padding = padding(s, e)
	q.Crypto = crypto(s, q.DatagramSize, e)
	q.PacketNumbers = packetNumbers(s, e)
	q.DCIDLength, q.DCIDLengthRule, q.SCIDLength = connectionIDs(s, e)
	q.NewConnectionIDs = newConnectionIDs(s, q.SCIDLength, e)
	q.TransportParameters, q.TransportParameterOrder = transportParameters(s, q.SCIDLength, e)
	q.AckInitial, q.AckBeforeFinished, q.Coalesce = flight(s, e)
	q.AckDelay, q.AckDelayConstant = ackDelay(s)
	q.AckOnlyLimit, q.AckOnlyFrame = ackOnly(s, e)
	q.SpinBit = spinBit(s)
	q.Tokens = tokens(s, e)
	q.ZeroRTT = zeroRTT(s, p, e)
	q.CloseReply = closeReply(s, e)
	return q, e.err()
}

// padding is the padded size of every Initial-carrying datagram and where
// the padding goes: trailing, one run per Initial, or among its frames.
func padding(s *captureSet, e *collector) (int, string) {
	size := 0
	for _, c := range s.conns {
		for _, d := range c.c.Datagrams {
			size = max(size, d.Size)
		}
	}
	trailing, end := false, true
	for _, c := range s.conns {
		for di, d := range c.c.Datagrams {
			initial := false
			for _, p := range d.Packets {
				initial = initial || p.Type == quictrace.PacketInitial
			}
			if !initial {
				continue
			}
			if d.Size != size {
				e.add(c, "datagram %d carries an Initial packet in %d bytes, not %d", di+1, d.Size, size)
			}
			trailing = trailing || d.Trailing > 0
			for _, p := range d.Packets {
				if p.Type != quictrace.PacketInitial {
					continue
				}
				runs := 0
				for _, f := range p.Frames {
					if f.Type == quictrace.FramePadding {
						runs++
					}
				}
				if runs > 1 || runs == 1 && p.Frames[len(p.Frames)-1].Type != quictrace.FramePadding {
					end = false
				}
			}
		}
	}
	if !trailing {
		if end {
			return size, profile.PaddingEnd
		}
		return size, profile.PaddingScattered
	}
	// Trailing: no PADDING frame but in a 1-RTT packet.
	for _, c := range s.conns {
		for di, d := range c.c.Datagrams {
			for _, p := range d.Packets {
				if p.Type != quictrace.Packet1RTT && hasFrame(&p, quictrace.FramePadding) {
					e.add(c, "datagram %d pads both after its packets and with PADDING frames in a %s packet", di+1, p.Type)
				}
			}
		}
	}
	return size, profile.PaddingTrailing
}

// cryptoRange is one CRYPTO frame's data.
type cryptoRange struct {
	start, end int
}

// cryptoFrames are the CRYPTO frames of one packet, in order.
func cryptoFrames(p *quictrace.Packet) []cryptoRange {
	var out []cryptoRange
	for _, f := range p.Frames {
		if f.Type == quictrace.FrameCrypto {
			out = append(out, cryptoRange{
				start: int(f.Offset), //nolint:gosec // a ClientHello offset
				end:   int(f.Offset + f.Length),
			})
		}
	}
	return out
}

// helloPackets are the Initial packets that first carry c's ClientHello,
// up to the one that completes it (retransmissions aside).
func helloPackets(c *conn) []packet {
	length := c.helloLength()
	covered := map[int]bool{}
	var out []packet
	for _, pk := range c.cryptoInitials() {
		if len(covered) == length {
			break
		}
		out = append(out, pk)
		for _, r := range cryptoFrames(pk.p) {
			for i := r.start; i < r.end; i++ {
				covered[i] = true
			}
		}
	}
	return out
}

// crypto derives how the ClientHello is cut, trying each rule against
// every connection.
func crypto(s *captureSet, datagramSize int, e *collector) profile.QUICCrypto {
	if first, ok := ordered(s); ok {
		return profile.QUICCrypto{
			Split:       profile.CryptoSplitOrdered,
			FirstPacket: first,
		}
	}
	var sniErrs []string
	for _, c := range s.conns {
		if err := sniCut(c, datagramSize); err != nil {
			sniErrs = append(sniErrs, c.at+": "+err.Error())
		}
	}
	if len(sniErrs) == 0 {
		return profile.QUICCrypto{
			Split: profile.CryptoSplitSNICut,
		}
	}
	out, shuffleErrs := shuffle(s)
	if len(shuffleErrs) == 0 {
		return out
	}
	for _, c := range s.conns[:1] {
		e.add(c, "the ClientHello's CRYPTO frames follow no rule:\nsni_cut: %v\nshuffle: %v", sniErrs, shuffleErrs)
	}
	return out
}

// ordered reports one CRYPTO frame per packet, in order, the first packet
// carrying the same length in every connection.
func ordered(s *captureSet) (int, bool) {
	first := 0
	for _, c := range s.conns {
		next := 0
		for i, pk := range helloPackets(c) {
			frames := cryptoFrames(pk.p)
			if len(frames) != 1 || frames[0].start != next {
				return 0, false
			}
			if i == 0 {
				if first != 0 && first != frames[0].end {
					return 0, false
				}
				first = frames[0].end
			}
			next = frames[0].end
		}
		if next != c.helloLength() {
			return 0, false
		}
	}
	return first, first > 0
}

// sniCut checks neqo's SNI slicing on c: the ClientHello is cut mid
// server_name host, the tail piece sent before the head, sized to len/n
// bytes over n packets, the rest in order one frame per packet.
func sniCut(c *conn, datagramSize int) error {
	hello := c.c.ClientHelloBytes()
	length := len(hello)
	at := bytes.Index(hello, []byte(c.c.Host))
	if at < 0 {
		return errors.New("its server_name is not in its ClientHello")
	}
	mid := at + len(c.c.Host)/2
	pkts := helloPackets(c)
	if len(pkts) < 2 {
		return fmt.Errorf("%d Initial packets", len(pkts))
	}
	first := cryptoFrames(pkts[0].p)
	if len(first) != 2 || first[0].end != length || first[1].start != 0 {
		return fmt.Errorf("first packet's CRYPTO frames %v are not the tail, then the head", first)
	}
	right, left := first[0], first[1]
	full := pkts[0].d.Size == datagramSize && pkts[0].d.Trailing == 0
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
		return fmt.Errorf("first packet's CRYPTO frames %v do not cut %d bytes at the host's middle %d", first, length, mid)
	}
	next := left.end
	for i, pk := range pkts[1:] {
		frames := cryptoFrames(pk.p)
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

// shuffle checks quiche's chaos protection: two packets, the first holding
// floor((len-4)/2) bytes (head [0, h) and tail), the second the middle;
// it measures h, the added CRYPTO frames and PINGs.
func shuffle(s *captureSet) (profile.QUICCrypto, []string) {
	out := profile.QUICCrypto{
		Split: profile.CryptoSplitShuffle,
	}
	var errs []string
	var heads, added, pings, cappedPings []int
	for _, c := range s.conns {
		length := c.helloLength()
		pkts := helloPackets(c)
		if len(pkts) != 2 {
			errs = append(errs, fmt.Sprintf("%s: %d Initial packets carry the ClientHello", c.at, len(pkts)))
			continue
		}
		regions := [2][]cryptoRange{}
		for i, pk := range pkts {
			regions[i] = merge(cryptoFrames(pk.p))
		}
		r1, r2 := regions[0], regions[1]
		if len(r1) != 2 || len(r2) != 1 || r1[0].start != 0 || r1[0].end != r2[0].start || r2[0].end != r1[1].start || r1[1].end != length {
			errs = append(errs, fmt.Sprintf("%s: packets carry %v and %v, not head and tail, then the middle", c.at, r1, r2))
			continue
		}
		if got := r1[0].end + length - r1[1].start; got != (length-4)/2 {
			errs = append(errs, fmt.Sprintf("%s: the first packet carries %d bytes, not %d", c.at, got, (length-4)/2))
		}
		heads = append(heads, r1[0].end)
		for i, pk := range pkts {
			frames, padded, ping := 0, 0, 0
			for _, f := range pk.p.Frames {
				switch f.Type {
				case quictrace.FrameCrypto:
					frames++
				case quictrace.FramePadding:
					padded += f.Count
				case quictrace.FramePing:
					ping++
				}
			}
			base := len(regions[i])
			if padded > 0 {
				// Splits and PINGs stop only when padding runs out.
				added = append(added, frames-base)
				pings = append(pings, ping)
			} else {
				cappedPings = append(cappedPings, ping)
			}
		}
	}
	if len(heads) == 0 || len(added) == 0 {
		return out, append(errs, "no packet to measure")
	}
	out.Head = span(heads)
	out.AddedFrames = span(added)
	out.Pings = span(pings)
	for _, n := range cappedPings {
		if n > out.Pings.Max {
			errs = append(errs, fmt.Sprintf("an unpadded packet has %d PINGs, more than %d", n, out.Pings.Max))
		}
	}
	return out, errs
}

// merge joins adjacent ranges into regions, sorted.
func merge(rs []cryptoRange) []cryptoRange {
	rs = slices.Clone(rs)
	slices.SortFunc(rs, func(a, b cryptoRange) int {
		return cmp.Compare(a.start, b.start)
	})
	var out []cryptoRange
	for _, r := range rs {
		if n := len(out); n > 0 && out[n-1].end == r.start {
			out[n-1].end = r.end
			continue
		}
		out = append(out, r)
	}
	return out
}

func span(vs []int) *profile.Range {
	return &profile.Range{
		Min: slices.Min(vs),
		Max: slices.Max(vs),
	}
}

// packetNumbers tries each numbering rule against every connection.
func packetNumbers(s *captureSet, e *collector) string {
	rules := []struct {
		name  string
		check func(*conn) error
	}{
		{profile.PacketNumbersShared, sharedNumbers},
		{profile.PacketNumbersPerSpace, func(c *conn) error {
			return perSpace(c, false)
		}},
		{profile.PacketNumbersRandomInitial, func(c *conn) error {
			return perSpace(c, true)
		}},
	}
	var tried []string
	for _, r := range rules {
		var errs []string
		for _, c := range s.conns {
			if err := r.check(c); err != nil {
				errs = append(errs, c.at+": "+err.Error())
			}
		}
		if len(errs) == 0 {
			return r.name
		}
		tried = append(tried, fmt.Sprintf("%s: %v", r.name, errs[0]))
	}
	e.add(s.conns[0], "packet numbers follow no rule: %v", tried)
	return ""
}

// sharedNumbers: one counter from 1 across every space, the second
// Initial's number in 2 bytes without a token, else 1.
func sharedNumbers(c *conn) error {
	next := uint64(1)
	initials := 0
	for _, pk := range c.packets() {
		if pk.p.PN < next {
			return fmt.Errorf("%s packet %d after %d", pk.p.Type, pk.p.PN, next-1)
		}
		next = pk.p.PN + 1
		if pk.p.Type != quictrace.PacketInitial {
			continue
		}
		initials++
		if initials == 1 && pk.p.PN != 1 {
			return fmt.Errorf("first packet number %d", pk.p.PN)
		}
		if initials == 2 {
			want := 1
			if pk.p.TokenLength == nil || *pk.p.TokenLength == 0 {
				want = 2
			}
			if pk.p.PNLength != want {
				return fmt.Errorf("second Initial's packet number in %d bytes, not %d", pk.p.PNLength, want)
			}
		}
	}
	return nil
}

// perSpace: each space from 0, 1-RTT continuing 0-RTT's; with
// randomInitial, the Initial space from neqo's random start instead.
func perSpace(c *conn, randomInitial bool) error {
	firsts := map[string]uint64{}
	early := uint64(0)
	for _, pk := range c.packets() {
		t := pk.p.Type
		if t == quictrace.Packet0RTT {
			early++
		}
		if _, ok := firsts[t]; !ok {
			firsts[t] = pk.p.PN
			want := uint64(0)
			if t == quictrace.Packet1RTT {
				want = early
			}
			switch {
			case t == quictrace.PacketInitial && randomInitial:
				if pk.p.PN < 1 || pk.p.PN > 1024 {
					return fmt.Errorf("first Initial packet number %d is not from 1 to 1024", pk.p.PN)
				}
			case pk.p.PN != want:
				return fmt.Errorf("first %s packet number %d, not %d", t, pk.p.PN, want)
			}
		}
	}
	return nil
}

// connectionIDs are the first DCID's length (fixed, or neqo's biased
// pick) and the SCID's.
func connectionIDs(s *captureSet, e *collector) (int, string, int) {
	var dcids []int
	scid := -1
	for _, c := range s.conns {
		for i, pk := range c.packets() {
			if i == 0 {
				dcids = append(dcids, pk.p.DCIDLength)
			}
			if pk.p.SCIDLength == nil {
				continue
			}
			if scid == -1 {
				scid = *pk.p.SCIDLength
			}
			if *pk.p.SCIDLength != scid {
				e.add(c, "a %d-byte SCID, not %d", *pk.p.SCIDLength, scid)
			}
		}
	}
	if slices.Min(dcids) == slices.Max(dcids) {
		return dcids[0], "", scid
	}
	eights := 0
	for _, n := range dcids {
		if n == 8 {
			eights++
		}
	}
	// neqo picks max(8, 5 + (v & v>>4)): 8 at least 9 times in 16, up to 20.
	if slices.Min(dcids) != 8 || slices.Max(dcids) > 20 || eights*2 < len(dcids) {
		e.add(s.conns[0], "first DCID lengths %v are neither one length nor neqo's biased pick", dcids)
	}
	return 8, profile.DCIDLengthBiased, scid
}

// newConnectionIDs is how many NEW_CONNECTION_ID frames every completed
// connection sent: sequence numbers from 1, SCID-long, retiring none.
func newConnectionIDs(s *captureSet, scid int, e *collector) int {
	count := -1
	for _, c := range s.done() {
		var seqs []uint64
		for _, pk := range c.packets() {
			for _, f := range pk.p.Frames {
				if f.Type != quictrace.FrameNewConnectionID {
					continue
				}
				seqs = append(seqs, f.Sequence)
				if f.CIDLength != scid || f.RetirePriorTo != 0 {
					e.add(c, "NEW_CONNECTION_ID %d of %d bytes retiring below %d", f.Sequence, f.CIDLength, f.RetirePriorTo)
				}
			}
		}
		for i, seq := range seqs {
			if seq != uint64(i)+1 {
				e.add(c, "NEW_CONNECTION_ID sequence numbers %v", seqs)
				break
			}
		}
		if count == -1 {
			count = len(seqs)
		}
		if len(seqs) != count {
			e.add(c, "%d NEW_CONNECTION_IDs, another connection %d", len(seqs), count)
		}
	}
	return max(count, 0)
}

// transportParameters derives the parameters and their order from every
// connection: one value per parameter, the same set in every full
// handshake and the resumed ones adding the same.
func transportParameters(s *captureSet, scid int, e *collector) ([]profile.TransportParameter, string) {
	var full, resumed [][]profile.TransportParameterID
	var fullConns []*conn
	byID := map[profile.TransportParameterID]*profile.TransportParameter{}
	var resumedOnly []profile.TransportParameterID
	for _, c := range s.conns {
		var ids []profile.TransportParameterID
		for _, tp := range c.c.TransportParameters {
			id := tpID(tp)
			ids = append(ids, id)
			got := profileParameter(c, tp, scid, e)
			want, ok := byID[id]
			if !ok {
				byID[id] = got
				continue
			}
			mergeParameter(c, want, got, e)
		}
		if c.resumed {
			resumed = append(resumed, ids)
		} else {
			full = append(full, ids)
			fullConns = append(fullConns, c)
		}
	}
	if len(full) == 0 {
		e.add(s.conns[0], "no full handshake")
		return nil, ""
	}
	reference := full[0]
	for i, ids := range full {
		if !sameSet(ids, reference) {
			e.add(fullConns[i], "transport parameters %v, another full handshake's %v", ids, reference)
		}
	}
	for _, ids := range resumed {
		for _, id := range ids {
			if !slices.Contains(reference, id) && !slices.Contains(resumedOnly, id) {
				resumedOnly = append(resumedOnly, id)
			}
		}
	}
	for _, ids := range resumed {
		if !sameSet(ids, append(slices.Clone(reference), resumedOnly...)) {
			e.add(s.conns[0], "resumed transport parameters %v are not %v and %v", ids, reference, resumedOnly)
		}
	}
	all := slices.Concat(full, resumed)
	stripped := make([][]profile.TransportParameterID, len(all))
	for i, ids := range all {
		stripped[i] = slices.DeleteFunc(slices.Clone(ids), func(id profile.TransportParameterID) bool {
			return slices.Contains(resumedOnly, id)
		})
	}
	order, reference, last := parameterOrder(stripped, reference)
	var out []profile.TransportParameter
	for _, id := range slices.Concat(reference, resumedOnly) {
		tp := *byID[id]
		tp.Resumed = slices.Contains(resumedOnly, id)
		tp.Last = slices.Contains(last, id)
		out = append(out, tp)
	}
	if order == profile.TransportParameterOrderFixed && len(resumedOnly) > 0 {
		e.add(s.conns[0], "a fixed order with parameters %v only resumed connections send: their place is not modelled", resumedOnly)
	}
	return out, order
}

// parameterOrder is fixed, rotated (same last parameter), or shuffled.
// Rotated orders are written from the smallest ID so the result doesn't
// depend on sample order.
func parameterOrder(all [][]profile.TransportParameterID, reference []profile.TransportParameterID) (string, []profile.TransportParameterID, []profile.TransportParameterID) {
	fixed := true
	for _, ids := range all {
		fixed = fixed && slices.Equal(ids, reference)
	}
	if fixed {
		return profile.TransportParameterOrderFixed, reference, nil
	}
	// The longest suffix every connection ends with stays last.
	n := len(reference)
	for _, ids := range all {
		k := 0
		for k < n && k < len(ids) && ids[len(ids)-1-k] == reference[len(reference)-1-k] {
			k++
		}
		n = min(n, k)
	}
	last := reference[len(reference)-n:]
	cycle := reference[:len(reference)-n]
	rotated := len(cycle) > 1
	for _, ids := range all {
		rotated = rotated && isRotation(ids[:len(ids)-n], cycle)
	}
	if rotated {
		start := slices.Index(cycle, slices.Min(cycle))
		return profile.TransportParameterOrderRotated, slices.Concat(cycle[start:], cycle[:start], last), last
	}
	return profile.TransportParameterOrderShuffled, reference, nil
}

func isRotation(ids, cycle []profile.TransportParameterID) bool {
	if len(ids) != len(cycle) {
		return false
	}
	start := slices.Index(cycle, ids[0])
	if start < 0 {
		return false
	}
	for i, id := range ids {
		if cycle[(start+i)%len(cycle)] != id {
			return false
		}
	}
	return true
}

func sameSet(a, b []profile.TransportParameterID) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// profileParameter is one captured parameter as the profile writes it.
func profileParameter(c *conn, tp quictrace.TransportParameter, scid int, e *collector) *profile.TransportParameter {
	out := &profile.TransportParameter{
		ID: tpID(tp),
	}
	switch {
	case out.ID == profile.TPInitialSourceConnectionID:
		if tp.Length != scid {
			e.add(c, "initial_source_connection_id of %d bytes, its SCID's %d", tp.Length, scid)
		}
	case out.ID == profile.TPInitialRTT:
	case tp.Grease:
		out.MaxLength = tp.Length
	case out.ID == profile.TPVersionInformation:
		for _, v := range tp.Versions {
			var version profile.QUICVersion
			if err := version.UnmarshalText([]byte("0x" + v)); err != nil {
				e.add(c, "version_information: %v", err)
			}
			out.Versions = append(out.Versions, version)
		}
	case tp.Value != nil && tp.Hex == "":
		out.Value = tp.Value
	default:
		// Not a known integer: its exact bytes.
		if err := out.Data.UnmarshalText([]byte(tp.Hex)); err != nil {
			e.add(c, "transport parameter %s: %v", out.ID, err)
		}
	}
	return out
}

// mergeParameter folds got, another connection's, into want: values must
// be equal, GREASE's longest length is kept, and version_information's
// available versions shuffle if their order differs.
func mergeParameter(c *conn, want, got *profile.TransportParameter, e *collector) {
	switch {
	case want.ID == profile.GREASETransportParameter:
		want.MaxLength = max(want.MaxLength, got.MaxLength)
	case want.ID == profile.TPVersionInformation:
		if len(want.Versions) == 0 || len(got.Versions) != len(want.Versions) || got.Versions[0] != want.Versions[0] {
			e.add(c, "version_information %v, another connection's %v", got.Versions, want.Versions)
			return
		}
		if !slices.Equal(got.Versions, want.Versions) {
			a, b := slices.Clone(got.Versions[1:]), slices.Clone(want.Versions[1:])
			slices.Sort(a)
			slices.Sort(b)
			if !slices.Equal(a, b) {
				e.add(c, "version_information %v, another connection's %v", got.Versions, want.Versions)
			}
			want.ShuffleVersions = true
		}
	case (want.Value == nil) != (got.Value == nil) || want.Value != nil && *want.Value != *got.Value || !bytes.Equal(want.Data, got.Data):
		e.add(c, "transport parameter %s is %s, another connection's %s", want.ID, describe(got), describe(want))
	}
}

func describe(tp *profile.TransportParameter) string {
	if tp.Value != nil {
		return fmt.Sprint(*tp.Value)
	}
	return fmt.Sprintf("%x", []byte(tp.Data))
}
