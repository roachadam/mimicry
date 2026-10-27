package main

import (
	"maps"
	"math/bits"
	"slices"
	"strings"

	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// deriveHTTP3 derives the http3 section, Headers aside, from every
// connection that completed its handshake.
func deriveHTTP3(s *captureSet) (*profile.HTTP3, error) {
	e := &collector{}
	done := s.done()
	if len(done) == 0 {
		e.add(s.conns[0], "no QUIC connection completed its handshake")
		return &profile.HTTP3{}, e.err()
	}
	h := &profile.HTTP3{}
	h.Settings, h.GreaseSetting, h.GreaseFrame = control(done, e)
	h.UniStreams = uniStreams(done, e)
	h.StreamsWithFinished = streamsWithFinished(done, e)
	h.EarlyRequestFirst = earlyRequestFirst(done, e)
	h.RequestsAfterConfirmed = requestsAfterConfirmed(done, e)
	h.QPACK = qpackRules(done, e)
	h.PriorityUpdate = priorityUpdate(s, done, e)
	h.FirstDataFrame, h.MaxDataFrame = dataFrames(done, e)
	h.Discovery = discovery(s, e)
	return h, e.err()
}

// controlStream is c's control stream.
func controlStream(c *conn) *quictrace.H3Stream {
	for i := range c.c.H3.Streams {
		if c.c.H3.Streams[i].Type == quictrace.StreamControl {
			return &c.c.H3.Streams[i]
		}
	}
	return nil
}

// control derives the SETTINGS (in order, with one reserved setting last
// or none) and the reserved frame that follows them (or none), the same
// on every connection.
func control(done []*conn, e *collector) ([]profile.H3Setting, *profile.H3Grease, *profile.H3Grease) {
	var settings []profile.H3Setting
	var greaseIDs, greaseValues, frameIDs []uint64
	var frameLengths []int
	withSetting, withFrame := 0, 0
	for i, c := range done {
		cs := controlStream(c)
		if cs == nil || len(cs.Frames) == 0 || cs.Frames[0].Type != quictrace.FrameH3Settings {
			e.add(c, "no control stream starting with SETTINGS")
			continue
		}
		var got []profile.H3Setting
		for j, st := range cs.Frames[0].Settings {
			if st.Grease {
				if j != len(cs.Frames[0].Settings)-1 {
					e.add(c, "a reserved setting before the last")
				}
				withSetting++
				greaseIDs = append(greaseIDs, st.ID)
				greaseValues = append(greaseValues, st.Value)
				continue
			}
			got = append(got, profile.H3Setting{
				ID:    st.ID,
				Value: st.Value,
			})
		}
		if i == 0 {
			settings = got
		} else if !slices.Equal(got, settings) {
			e.add(c, "SETTINGS %v, another connection's %v", got, settings)
		}
		if len(cs.Frames) > 1 && cs.Frames[1].Grease {
			withFrame++
			frameIDs = append(frameIDs, cs.Frames[1].Type)
			frameLengths = append(frameLengths, int(cs.Frames[1].Length)) //nolint:gosec // a GREASE frame's length
		}
	}
	var setting, frame *profile.H3Grease
	switch withSetting {
	case 0:
	case len(done):
		setting = &profile.H3Grease{
			IDBits:    greaseBits(greaseIDs),
			ValueBits: bits.Len64(slices.Max(greaseValues)),
		}
	default:
		e.add(done[0], "%d of %d connections send a reserved setting", withSetting, len(done))
	}
	switch withFrame {
	case 0:
	case len(done):
		frame = &profile.H3Grease{
			IDBits:    greaseBits(frameIDs),
			MaxLength: slices.Max(frameLengths),
		}
	default:
		e.add(done[0], "%d of %d connections send a reserved frame after SETTINGS", withFrame, len(done))
	}
	return settings, setting, frame
}

// greaseBits is the bit length of the largest N among reserved
// identifiers 0x1f * N + 0x21, or 0 when it reaches the varint's range
// (N up to (2^62 - 0x22) / 0x1f, 57 bits).
func greaseBits(ids []uint64) int {
	n := 0
	for _, id := range ids {
		n = max(n, bits.Len64((id-0x21)/0x1f))
	}
	if n >= 56 {
		return 0
	}
	return n
}

// position is a frame's place on the wire: datagram, packet, frame.
type position struct {
	datagram, packet, frame int
}

// firstFrames is where each stream's first STREAM frame is on c's wire.
func firstFrames(c *conn) map[uint64]position {
	out := map[uint64]position{}
	for di, d := range c.c.Datagrams {
		for pi, p := range d.Packets {
			for fi, f := range p.Frames {
				if f.Type != quictrace.FrameStream || f.Stream == nil {
					continue
				}
				if _, ok := out[*f.Stream]; !ok {
					out[*f.Stream] = position{di, pi, fi}
				}
			}
		}
	}
	return out
}

// uniStreams derives the client's unidirectional streams by stream ID:
// their types the same on every connection, a stream ID never used taking
// the one type no other stream has; each opened at the handshake (with the
// control stream, on every connection) or with the first request that
// inserts into the dynamic table (on those connections only).
func uniStreams(done []*conn, e *collector) []profile.H3UniStream {
	types := map[int]uint64{}
	maxIndex := 0
	for _, c := range done {
		for _, st := range c.c.H3.Streams {
			i := int((st.ID - 2) / 4) //nolint:gosec // a stream index
			if t, ok := types[i]; ok && t != st.Type {
				e.add(c, "stream %d is %s, another connection's %s", st.ID, quictrace.StreamTypeName(st.Type), quictrace.StreamTypeName(t))
			}
			types[i] = st.Type
			maxIndex = max(maxIndex, i)
		}
	}
	known := []uint64{
		quictrace.StreamControl,
		quictrace.StreamQPACKEncoder,
		quictrace.StreamQPACKDecoder,
	}
	var out []profile.H3UniStream
	for i := 0; i <= maxIndex; i++ {
		t, ok := types[i]
		if !ok {
			var missing []uint64
			for _, k := range known {
				if !slices.Contains(slices.Collect(maps.Values(types)), k) {
					missing = append(missing, k)
				}
			}
			if len(missing) != 1 {
				e.add(done[0], "stream %d is never used and its type cannot be told", 2+4*i)
				continue
			}
			out = append(out, profile.H3UniStream{
				Type: quictrace.StreamTypeName(missing[0]),
				Open: profile.H3OpenNever,
			})
			continue
		}
		out = append(out, profile.H3UniStream{
			Type: quictrace.StreamTypeName(t),
			Open: openRule(done, uint64(2+4*i), t, e), //nolint:gosec // a stream ID
		})
	}
	return out
}

// openRule is when stream id opens: at the handshake if every connection
// opens it in the control stream's packet, on the first insert if it opens
// in the packet of the first request whose section uses the dynamic table
// and only where one does.
func openRule(done []*conn, id, typ uint64, e *collector) string {
	if typ == quictrace.StreamControl {
		return profile.H3OpenAtHandshake
	}
	handshake, insert := true, true
	for _, c := range done {
		first := firstFrames(c)
		ctl, hasCtl := first[2]
		at, opened := first[id]
		var inserting *quictrace.H3Request
		for i := range c.c.H3.Requests {
			if c.c.H3.Requests[i].Section.RequiredInsertCount > 0 {
				inserting = &c.c.H3.Requests[i]
				break
			}
		}
		if !opened || !hasCtl || at.datagram != ctl.datagram || at.packet != ctl.packet {
			handshake = false
		}
		switch {
		case inserting == nil:
			insert = insert && !opened
		case !opened:
			insert = false
		default:
			req := first[inserting.Stream]
			insert = insert && req.datagram == at.datagram && req.packet == at.packet
		}
	}
	switch {
	case handshake:
		return profile.H3OpenAtHandshake
	case insert:
		return profile.H3OpenOnFirstInsert
	}
	e.add(done[0], "stream %d opens neither at the handshake nor with the first insert", id)
	return ""
}

// streamsWithFinished: on every completed full handshake without 0-RTT,
// the control stream starts in the datagram that carries the Finished, or
// on none.
func streamsWithFinished(done []*conn, e *collector) bool {
	var vs []bool
	for _, c := range done {
		if c.c.EarlyData {
			continue
		}
		finished := -1
		for _, pk := range c.packets() {
			if pk.p.Type == quictrace.PacketHandshake && hasFrame(pk.p, quictrace.FrameCrypto) {
				finished = pk.datagram
				break
			}
		}
		ctl, ok := firstFrames(c)[2]
		vs = append(vs, ok && ctl.datagram == finished)
	}
	return allSame(vs, done[0], "control stream with the Finished", e)
}

// earlyRequestFirst: in 0-RTT, a request stream's first frame before the
// control stream's, on every connection that sent a request in 0-RTT.
func earlyRequestFirst(done []*conn, e *collector) bool {
	var vs []bool
	for _, c := range done {
		var order []uint64
		for _, pk := range c.packets() {
			if pk.p.Type != quictrace.Packet0RTT {
				continue
			}
			for _, f := range pk.p.Frames {
				if f.Type == quictrace.FrameStream && f.Stream != nil && !slices.Contains(order, *f.Stream) {
					order = append(order, *f.Stream)
				}
			}
		}
		req := slices.IndexFunc(order, func(id uint64) bool {
			return id%4 == 0
		})
		if req < 0 {
			continue
		}
		ctl := slices.Index(order, 2)
		vs = append(vs, ctl < 0 || req < ctl)
	}
	if len(vs) == 0 {
		return false
	}
	return allSame(vs, done[0], "0-RTT request before the control stream", e)
}

// pendingRequestMillis bounds how soon after the Finished a full
// handshake's first request must leave to have been waiting for the
// connection (Safari's 0.7–2.4 ms); the others' came from later script or
// parsing, 47 ms on.
const pendingRequestMillis = 20

// requestsAfterConfirmed: on every completed full handshake whose first
// request was waiting for it (sent within pendingRequestMillis of the
// Finished), that request leaves in a packet acknowledging a 1-RTT packet
// newer than any the client acknowledged before: one the server sent
// after the client's Finished, its HANDSHAKE_DONE (Safari). None waiting:
// not measured.
func requestsAfterConfirmed(done []*conn, e *collector) bool {
	var vs []bool
	for _, c := range done {
		if c.c.Resumed {
			continue
		}
		finished := int64(-1)
		acked := int64(-1)
		for _, pk := range c.packets() {
			if pk.p.Type == quictrace.PacketHandshake && hasFrame(pk.p, quictrace.FrameCrypto) && finished < 0 {
				finished = pk.d.Micros
			}
			if pk.p.Type != quictrace.Packet1RTT {
				continue
			}
			largest := int64(-1)
			request := false
			for _, f := range pk.p.Frames {
				switch {
				case f.Type == quictrace.FrameAck:
					largest = int64(f.Largest) //nolint:gosec // packet numbers are small
				case f.Type == quictrace.FrameStream && f.Stream != nil && *f.Stream%4 == 0:
					request = true
				}
			}
			if request {
				if finished >= 0 && pk.d.Micros-finished <= pendingRequestMillis*1000 {
					vs = append(vs, largest > acked)
				}
				break
			}
			acked = max(acked, largest)
		}
	}
	return allSame(vs, done[0], "first request after the handshake is confirmed", e)
}

func allSame(vs []bool, c *conn, what string, e *collector) bool {
	if len(vs) == 0 {
		return false
	}
	for _, v := range vs[1:] {
		if v != vs[0] {
			e.add(c, "%s differs between connections: %v", what, vs)
			break
		}
	}
	return vs[0]
}

// qpackRules derives the encoder policy from the connections whose
// server allowed a dynamic table, and the Huffman rule from every string
// sent.
func qpackRules(done []*conn, e *collector) profile.QPACK {
	out := profile.QPACK{
		Huffman: huffman(done, e),
	}
	indexAll, newNames, used := true, true, false
	var firsts []bool
	for _, c := range done {
		var inserts []quictrace.EncoderInstruction
		var encoder uint64
		for _, st := range c.c.H3.Streams {
			if st.Type != quictrace.StreamQPACKEncoder {
				continue
			}
			encoder = st.ID
			for _, in := range st.Encoder {
				if in.Op != quictrace.OpSetCapacity {
					inserts = append(inserts, in)
				}
			}
		}
		if len(inserts) == 0 {
			continue
		}
		used = true
		names := make([]string, 0, len(inserts))
		for _, in := range inserts {
			if in.Op != quictrace.OpInsertLiteral || slices.Contains(names, in.Name) {
				newNames = false
			}
			names = append(names, in.Name)
		}
		for _, q := range c.c.H3.Requests {
			for _, f := range q.Section.Fields {
				if f.Rep != quictrace.RepIndexed && f.Rep != quictrace.RepIndexedPostBase {
					indexAll = false
				}
			}
		}
		firsts = append(firsts, insertsFirst(c, encoder)...)
	}
	switch {
	case !used:
		return out
	case indexAll:
		out.Encoder = profile.QPACKIndexAll
	case newNames:
		out.Encoder = profile.QPACKNewNames
	default:
		e.add(done[0], "the dynamic table is used neither for every field nor for new names once")
	}
	out.InsertsFirst = allSame(firsts, done[0], "inserts before their request", e)
	return out
}

// insertsFirst reports, for every packet carrying both a request's first
// frame and encoder stream data, whether the encoder data comes first.
func insertsFirst(c *conn, encoder uint64) []bool {
	starts := map[uint64]bool{}
	var out []bool
	for _, d := range c.c.Datagrams {
		for _, p := range d.Packets {
			enc, req := -1, -1
			for fi, f := range p.Frames {
				if f.Type != quictrace.FrameStream || f.Stream == nil {
					continue
				}
				id := *f.Stream
				switch {
				case id == encoder && f.Offset > 0 && enc == -1:
					enc = fi
				case id%4 == 0 && !starts[id]:
					starts[id] = true
					if req == -1 {
						req = fi
					}
				}
			}
			if enc >= 0 && req >= 0 {
				out = append(out, enc < req)
			}
		}
	}
	return out
}

// huffman: always if every string is Huffman-coded, shorter if exactly
// those whose code is strictly shorter are.
func huffman(done []*conn, e *collector) profile.HuffmanRule {
	type str struct {
		s       string
		huffman bool
	}
	var all []str
	for _, c := range done {
		for _, q := range c.c.H3.Requests {
			for _, f := range q.Section.Fields {
				switch f.Rep {
				case quictrace.RepLiteral:
					all = append(all, str{f.Name, f.NameHuffman}, str{f.Value, f.ValueHuffman})
				case quictrace.RepLiteralRef, quictrace.RepLiteralPostBaseRef:
					all = append(all, str{f.Value, f.ValueHuffman})
				}
			}
		}
		for _, st := range c.c.H3.Streams {
			for _, in := range st.Encoder {
				switch in.Op {
				case quictrace.OpInsertLiteral:
					all = append(all, str{in.Name, in.NameHuffman}, str{in.Value, in.ValueHuffman})
				case quictrace.OpInsertNameRef:
					all = append(all, str{in.Value, in.ValueHuffman})
				}
			}
		}
	}
	always := true
	for _, s := range all {
		always = always && s.huffman
	}
	if always {
		return profile.HuffmanAlways
	}
	for _, s := range all {
		if shorter := hpack.HuffmanEncodeLength(s.s) < uint64(len(s.s)); s.huffman != shorter {
			e.add(done[0], "%q sent with Huffman %v: neither always nor only when shorter", s.s, s.huffman)
			break
		}
	}
	return profile.HuffmanShorter
}

// priorityUpdate: on every connection, each request with a priority
// header has a PRIORITY_UPDATE for its stream with the same value, read no
// later than its HEADERS (or later where sampleGaps pins
// "priority_update <path>"), and no other; or no connection sends any.
func priorityUpdate(s *captureSet, done []*conn, e *collector) bool {
	var vs []bool
	for _, c := range done {
		updates := map[uint64]quictrace.H3Frame{}
		cs := controlStream(c)
		for _, f := range cs.Frames {
			if f.Type == quictrace.FrameH3PriorityUpdateRequest && f.ID != nil {
				updates[*f.ID] = f
			}
		}
		if len(updates) == 0 {
			vs = append(vs, false)
			continue
		}
		vs = append(vs, true)
		matched := 0
		for _, q := range c.c.H3.Requests {
			value, ok := "", false
			for _, f := range q.Section.Fields {
				if f.Name == "priority" {
					value, ok = f.Value, true
				}
			}
			u, has := updates[q.Stream]
			switch {
			case ok != has:
				e.add(c, "%s: priority header %v, PRIORITY_UPDATE %v", q.Path, ok, has)
			case ok && u.Priority == value && u.Millis > q.Millis && s.gap(c, "priority_update "+q.Path):
				matched++
			case ok && (u.Priority != value || u.Millis > q.Millis):
				e.add(c, "%s: PRIORITY_UPDATE %q at %d ms for priority %q at %d ms", q.Path, u.Priority, u.Millis, value, q.Millis)
			case ok:
				matched++
			}
		}
		if matched != len(updates) {
			e.add(c, "%d PRIORITY_UPDATEs for %d requests with a priority", len(updates), matched)
		}
	}
	return allSame(vs, done[0], "PRIORITY_UPDATE", e)
}

// dataFrames derives how request bodies are framed: one DATA frame each,
// or frames of a cap after a first of its own size.
func dataFrames(done []*conn, e *collector) (first, most int) {
	type body struct {
		c      *conn
		path   string
		frames []int
	}
	var bodies []body
	for _, c := range done {
		for _, q := range c.c.H3.Requests {
			var frames []int
			for _, f := range q.Frames {
				if f.Type == quictrace.FrameH3Data {
					frames = append(frames, int(f.Length)) //nolint:gosec // a DATA frame's length
				}
			}
			if len(frames) > 0 {
				bodies = append(bodies, body{c, q.Path, frames})
			}
		}
	}
	for _, b := range bodies {
		if len(b.frames) > 1 {
			first = max(first, b.frames[0])
			for _, n := range b.frames[1:] {
				most = max(most, n)
			}
		}
	}
	if most == 0 {
		return 0, 0
	}
	for _, b := range bodies {
		total := 0
		for _, n := range b.frames {
			total += n
		}
		want := []int{min(total, first)}
		for left := total - want[0]; left > 0; left -= min(left, most) {
			want = append(want, min(left, most))
		}
		if !slices.Equal(b.frames, want) {
			e.add(b.c, "%s: DATA frames %v, not %v", b.path, b.frames, want)
		}
	}
	return first, most
}

// label is a request path's step label ("/h3/fetch1.so": "fetch1.so").
func label(path string) string {
	l, _ := strings.CutPrefix(path, "/h3/")
	return l
}
