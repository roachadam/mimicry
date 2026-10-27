package e2e

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// The h3-download page (server.H3DownloadSteps: a 50 MiB response over
// HTTP/3, then small requests) made through mimicry as each browser made
// it, against every sample of the browser's capture. The bulk receive's
// rules hold for every browser sample and for mimicry's connection:
//
//   - Key updates: the client switches keys only after the server did
//     (quic-go updates after 100 packets), never first, and follows each.
//   - ACKs (QUICAck): once warm, a client that does not batch
//     acknowledges exactly every Every-th packet (Chrome's 10 in 98.8% of
//     its ACKs), one that batches (Firefox, Safari) at least that many;
//     no ACK_FREQUENCY or IMMEDIATE_ACK frames.
//
// and mimicry's credit (QUICCredit) is the browser's: every increase of
// its MAX_STREAM_DATA on the download stream lies within the range of the
// browser's own increases over every sample, give or take creditSlack;
// so does every increase of its MAX_DATA where the profile raises it by a
// share of the connection's window (Chrome, Firefox). Where the
// connection's limit follows what arrived (ConnectionStreamWindow,
// Safari), how far one MAX_DATA raises the last is how much arrived in
// between, timing: the rule is checked instead, on every browser sample
// and on mimicry's connection (windowCreditViolations). The requests
// after the download go on its connection.
func TestHTTP3DownloadMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			if p.HTTP3 == nil || p.QUIC == nil {
				t.Fatal("the profile has no http3 or quic section")
			}
			want := h3Captures(t, p, server.H3Download)
			byWindow := p.QUIC.Credit.ConnectionStreamWindow
			refs := make([]*quictrace.Conn, 0, len(want))
			for i, page := range want {
				c := downloadConn(t, page)
				refs = append(refs, c)
				violations, summary := bulkRuleViolations(p.QUIC, c)
				if byWindow {
					v, s := windowCreditViolations(c)
					violations, summary = append(violations, v...), summary+"; "+s
				}
				for _, v := range violations {
					t.Errorf("sample %d: the profile's rule does not describe the browser: %s", i+1, v)
				}
				t.Logf("sample %d: %s", i+1, summary)
			}
			got := runH3Page(t, p, server.H3Download, want[0])
			c := downloadConn(t, got)
			violations, summary := bulkRuleViolations(p.QUIC, c)
			if byWindow {
				v, s := windowCreditViolations(c)
				violations, summary = append(violations, v...), summary+"; "+s
			}
			for _, v := range violations {
				t.Errorf("mimicry: %s", v)
			}
			t.Logf("mimicry: %s", summary)
			compareCredit(t, "MAX_STREAM_DATA", streamCredit(c), mapEachConn(refs, streamCredit))
			if !byWindow {
				compareCredit(t, "MAX_DATA", connCredit(c), mapEachConn(refs, connCredit))
			}
			afterDownload(t, got)
		})
	}
}

// downloadConn is the page's QUIC connection that carried the download:
// the one the server sent most to.
func downloadConn(t *testing.T, page *capture.Page) *quictrace.Conn {
	t.Helper()
	var best *quictrace.Conn
	for _, c := range page.QUIC {
		if best == nil || len(c.Server) > len(best.Server) {
			best = c
		}
	}
	if best == nil || len(best.Server) < downloadPackets {
		t.Fatal("no QUIC connection carried the download")
	}
	return best
}

// downloadPackets is fewer packets than 50 MiB takes (about 37,000).
const downloadPackets = 30000

func mapEachConn(conns []*quictrace.Conn, f func(*quictrace.Conn) []uint64) [][]uint64 {
	out := make([][]uint64, 0, len(conns))
	for _, c := range conns {
		out = append(out, f(c))
	}
	return out
}

// bulkEvent is one 1-RTT packet of the connection, either side's, by when
// the recorder saw it.
type bulkEvent struct {
	micros int64
	client bool
	phase  bool
	frames []quictrace.Frame
}

// bulkEvents are the connection's 1-RTT packets in order: the client's
// (opened) and the server's (header protection removed).
func bulkEvents(c *quictrace.Conn) []bulkEvent {
	var out []bulkEvent
	add := func(dgs []quictrace.Datagram, client bool) {
		for _, dg := range dgs {
			for _, pk := range dg.Packets {
				if pk.Type != quictrace.Packet1RTT || pk.Error != "" {
					continue
				}
				out = append(out, bulkEvent{
					micros: dg.Micros,
					client: client,
					phase:  pk.FirstByte&keyPhaseBit != 0,
					frames: pk.Frames,
				})
			}
		}
	}
	add(c.Datagrams, true)
	add(c.Server, false)
	slices.SortStableFunc(out, func(a, b bulkEvent) int {
		return int(a.micros - b.micros)
	})
	return out
}

// keyPhaseBit is a 1-RTT header's Key Phase bit (RFC 9000, section
// 17.3.1).
const keyPhaseBit = 0x04

// bulkRuleViolations lists where the connection breaks the profile's key
// update and ACK rules during the download, and sums up what it did.
func bulkRuleViolations(q *profile.QUIC, c *quictrace.Conn) (out []string, summary string) {
	events := bulkEvents(c)
	var serverPhase, clientPhase bool
	serverFlips, clientFlips := 0, 0
	for _, e := range events {
		switch {
		case !e.client && e.phase != serverPhase:
			serverPhase = e.phase
			serverFlips++
		case e.client && e.phase != clientPhase:
			if e.phase != serverPhase && !q.KeyUpdate.Initiate {
				out = append(out, fmt.Sprintf("the client started a key update at %d µs", e.micros))
			}
			clientPhase = e.phase
			clientFlips++
		}
	}
	switch {
	case serverFlips == 0:
		out = append(out, "the server never updated its keys (quic-go does after 100 packets): the rule is not exercised")
	case clientFlips < serverFlips:
		out = append(out, fmt.Sprintf("the client followed %d of the server's %d key updates", clientFlips, serverFlips))
	}

	var largest []uint64
	var maxServer uint64
	for _, dg := range c.Server {
		for _, pk := range dg.Packets {
			if pk.Type == quictrace.Packet1RTT {
				maxServer = max(maxServer, pk.PN)
			}
		}
	}
	for _, e := range events {
		if !e.client {
			continue
		}
		for _, f := range e.frames {
			switch f.Type {
			case quictrace.FrameAckFrequency, quictrace.FrameImmediateAck:
				out = append(out, "the client sent "+f.Type)
			case quictrace.FrameAck:
				largest = append(largest, f.Largest)
			}
		}
	}
	a := q.Ack
	// Past the warm-up and before the tail, where the server's sending
	// slows.
	from := uint64(max(a.WarmupPackets, 0)) + bulkMargin
	var n, match int
	var advanced uint64
	for i := 1; i < len(largest); i++ {
		prev, cur := largest[i-1], largest[i]
		if cur <= prev || prev < from || cur+bulkMargin > maxServer {
			continue
		}
		n++
		d := cur - prev
		advanced += d
		if every := uint64(max(a.Every, 0)); d == every || a.Batched && d > every {
			match++
		}
	}
	rule := fmt.Sprintf("every %d", a.Every)
	need := ackShareExact
	if a.Batched {
		rule = fmt.Sprintf("every %d or more", a.Every)
		need = ackShareBatched
	}
	switch {
	case n == 0:
		out = append(out, "no ACK during the bulk receive")
	case float64(match) < need*float64(n):
		out = append(out, fmt.Sprintf("%d of %d ACKs during the bulk receive acknowledge %s new packets; want %.0f%%", match, n, rule, need*100))
	}
	// How far a batching client's ACKs advance on average is not held to
	// the browser's: it is how many packets arrive while the client works
	// through the last (Firefox 5.0–5.7, Safari 27 4.3–4.6, Safari 26 on
	// an older Mac 6.1–10.8; ACKs 19–47 µs apart at the median, with no
	// fixed count or interval), the machine's timing, not a rule a profile
	// can hold.
	summary = fmt.Sprintf("%d of %d ACKs during the bulk receive acknowledge %s new packets (%.1f%%), %.2f on average; the server updated its keys %d times, the client %d", match, n, rule, 100*float64(match)/float64(max(n, 1)), float64(advanced)/float64(max(n, 1)), serverFlips, clientFlips)
	return out, summary
}

// Thresholds of the ACK rule: the browsers' own shares are 98.7–98.8%
// (Chrome, exactly 10) and 99.6–100% (Firefox, Safari, 2 or more).
const (
	bulkMargin      = 50
	ackShareExact   = 0.95
	ackShareBatched = 0.98
)

// streamCredit is the increases of the connection's MAX_STREAM_DATA on
// the stream that got most of them, from its initial window.
func streamCredit(c *quictrace.Conn) []uint64 {
	limits := map[uint64][]uint64{}
	var stream uint64
	for _, dg := range c.Datagrams {
		for _, pk := range dg.Packets {
			for _, f := range pk.Frames {
				if f.Type != quictrace.FrameMaxStreamData || f.Stream == nil {
					continue
				}
				limits[*f.Stream] = append(limits[*f.Stream], f.Max)
				if len(limits[*f.Stream]) > len(limits[stream]) {
					stream = *f.Stream
				}
			}
		}
	}
	return increases(transportParameter(c.TransportParameters, tpInitialMaxStreamDataBidiLocal, 0), limits[stream])
}

// connCredit is the increases of the connection's MAX_DATA from its
// initial limit (Chrome's repeats of its current one left out).
func connCredit(c *quictrace.Conn) []uint64 {
	var limits []uint64
	for _, dg := range c.Datagrams {
		for _, pk := range dg.Packets {
			for _, f := range pk.Frames {
				if f.Type == quictrace.FrameMaxData {
					limits = append(limits, f.Max)
				}
			}
		}
	}
	return increases(transportParameter(c.TransportParameters, tpInitialMaxData, 0), limits)
}

// windowCreditViolations checks a connection's MAX_DATA against the rule
// of a client whose limit is the bytes received on the connection plus
// the stream window (QUICCredit.ConnectionStreamWindow): what each raise
// sets, what had arrived when it was sent plus the window, not how far it
// moves (what arrived since the last, timing). What had arrived is read
// from the client's own ACKs (ackedData): at least what it had
// acknowledged by the packet, at most what its next ACK acknowledges;
// both give or take the estimate's error and a packet (creditSlack).
//
//   - No raise runs ahead of what the next ACK acknowledges by more than
//     the stream window. The window is the transport parameter's or,
//     where the client grew it, as far as its own MAX_STREAM_DATA ran
//     ahead (Safari 26's first sample: 1.1 MB more).
//   - Half the raises or more run ahead of what was acknowledged by then
//     by the window at least. Safari 27 sends a quarter to a third of its
//     limits from what had arrived well before (up to 1.7 MB less), the
//     rest on the rule.
//
// Neither holds that each MAX_DATA rides with a MAX_STREAM_DATA: Safari
// 26's first does not, nor 12–17 of Safari 27's 42–46 in each sample.
func windowCreditViolations(c *quictrace.Conn) (out []string, summary string) {
	window := float64(transportParameter(c.TransportParameters, tpInitialMaxStreamDataBidiLocal, 0))
	last := transportParameter(c.TransportParameters, tpInitialMaxData, 0)
	var pks []quictrace.Packet
	for _, dg := range c.Datagrams {
		for _, pk := range dg.Packets {
			if pk.Type == quictrace.Packet1RTT && pk.Error == "" {
				pks = append(pks, pk)
			}
		}
	}
	acked, estimateErr := ackedData(c, pks)
	if len(acked) == 0 {
		return []string{"no 1-RTT packet from the client"}, ""
	}
	slack := estimateErr + creditSlack
	// next[k] is what the first ACK from packet k on acknowledged.
	next := make([]float64, len(pks))
	after := acked[len(acked)-1]
	for k := len(pks) - 1; k >= 0; k-- {
		if slices.ContainsFunc(pks[k].Frames, func(f quictrace.Frame) bool { return f.Type == quictrace.FrameAck }) {
			after = acked[k]
		}
		next[k] = after
	}
	streamWindow := window
	for k, pk := range pks {
		for _, f := range pk.Frames {
			if f.Type == quictrace.FrameMaxStreamData {
				streamWindow = max(streamWindow, float64(f.Max)-next[k])
			}
		}
	}
	var leads []float64
	ahead, worst := 0, math.Inf(-1)
	for k, pk := range pks {
		for _, f := range pk.Frames {
			if f.Type != quictrace.FrameMaxData || f.Max <= last {
				continue
			}
			last = f.Max
			over := float64(f.Max) - next[k] - streamWindow
			worst = max(worst, over)
			if over > slack {
				ahead++
			}
			leads = append(leads, float64(f.Max)-acked[k]-window)
		}
	}
	if len(leads) == 0 {
		return []string{"MAX_DATA never raised"}, ""
	}
	if ahead > 0 {
		out = append(out, fmt.Sprintf("%d of %d MAX_DATA raises run ahead of what the next ACK acknowledged by more than the stream window (%.0f), up to %.0f more (%.0f allowed)", ahead, len(leads), streamWindow, worst, slack))
	}
	behind := median(leads)
	if behind < -slack {
		out = append(out, fmt.Sprintf("the median of %d MAX_DATA raises falls %.0f short of what was acknowledged by then plus the window (%.0f; %.0f allowed)", len(leads), -behind, window, slack))
	}
	summary = fmt.Sprintf("%d MAX_DATA raises, at most %.0f past what the next ACK acknowledged plus the stream window (%.0f); the median %.0f past what was acknowledged by then plus %.0f (the estimate within %.0f)", len(leads), worst, streamWindow, behind, window, estimateErr)
	return out, summary
}

// median is the middle of vs (the upper one of an even count).
func median(vs []float64) float64 {
	vs = slices.Sorted(slices.Values(vs))
	return vs[len(vs)/2]
}

// ackedData is, after each of the client's 1-RTT packets pks, how many
// bytes of stream data its ACKs had acknowledged, and how far that may be
// off. The recorder does not open the server's 1-RTT payloads: a packet's
// data is its size less the connection's mean overhead, taken from every
// data packet (dataPacketSize or more) the client acknowledged, all of
// which carried the download (it and the other responses' bytes differ
// by about 0.1%). A packet's overhead is its header, AEAD tag and STREAM
// frame header (streamOverhead), plus whatever else it carries (ACK
// frames): the error at any point is at most that extra summed over the
// connection (4.6–5.8 KB in the Safari samples).
func ackedData(c *quictrace.Conn, pks []quictrace.Packet) (acked []float64, maxErr float64) {
	type dataPacket struct{ size, least float64 }
	data := map[uint64]dataPacket{}
	for _, dg := range c.Server {
		for _, pk := range dg.Packets {
			if pk.Type == quictrace.Packet1RTT && pk.Size >= dataPacketSize {
				data[pk.PN] = dataPacket{float64(pk.Size), float64(1 + pk.DCIDLength + pk.PNLength + streamOverhead)}
			}
		}
	}
	ranges := func(f func(lo, hi uint64)) {
		for _, pk := range pks {
			for _, fr := range pk.Frames {
				if fr.Type == quictrace.FrameAck {
					for _, r := range fr.Ranges {
						f(r[0], r[1])
					}
				}
			}
		}
	}
	var all pnSet
	var sum, least float64
	n := 0
	ranges(func(lo, hi uint64) {
		all.add(lo, hi, func(pn uint64) {
			if d, ok := data[pn]; ok {
				sum += d.size
				least += d.least
				n++
			}
		})
	})
	if n == 0 {
		return make([]float64, len(pks)), 0
	}
	overhead := (sum - downloadSize()) / float64(n)
	maxErr = max(sum-least-downloadSize(), 0)
	var seen pnSet
	total := 0.0
	acked = make([]float64, 0, len(pks))
	for _, pk := range pks {
		for _, fr := range pk.Frames {
			if fr.Type != quictrace.FrameAck {
				continue
			}
			for _, r := range fr.Ranges {
				seen.add(r[0], r[1], func(pn uint64) {
					if d, ok := data[pn]; ok {
						total += d.size - overhead
					}
				})
			}
		}
		acked = append(acked, total)
	}
	return acked, maxErr
}

// streamOverhead is the least a server data packet carries besides its
// header's first byte, DCID and packet number: the 16-byte AEAD tag and a
// STREAM frame header (type, stream ID, a 4-byte offset, no length: the
// packet's last frame).
const streamOverhead = 16 + 6

// dataPacketSize is the smallest server packet taken to carry stream data
// (the download's are 1,366–1,441 bytes; ACK-only ones 22–66).
const dataPacketSize = 1000

// downloadSize is the h3-download page's large response, in bytes.
func downloadSize() float64 {
	for _, s := range server.H3DownloadSteps() {
		if s.Download > 0 {
			return float64(s.Download)
		}
	}
	return 0
}

// pnSet is a set of packet numbers: sorted, disjoint, non-adjacent
// inclusive ranges.
type pnSet [][2]uint64

// add adds lo..hi, calling f with each packet number not already in s.
func (s *pnSet) add(lo, hi uint64, f func(pn uint64)) {
	iv := *s
	i, _ := slices.BinarySearchFunc(iv, lo, func(r [2]uint64, lo uint64) int {
		if r[1]+1 < lo {
			return -1
		}
		return 1
	})
	merged := [2]uint64{lo, hi}
	pn, j := lo, i
	for ; j < len(iv) && iv[j][0] <= hi+1; j++ {
		for ; pn < iv[j][0]; pn++ {
			f(pn)
		}
		pn = max(pn, iv[j][1]+1)
		merged = [2]uint64{min(merged[0], iv[j][0]), max(merged[1], iv[j][1])}
	}
	for ; pn <= hi; pn++ {
		f(pn)
	}
	*s = slices.Replace(iv, i, j, merged)
}

// Transport parameter IDs (RFC 9000, section 18.2).
const (
	tpInitialMaxData                = 0x04
	tpInitialMaxStreamDataBidiLocal = 0x05
	tpAckDelayExponent              = 0x0a
	tpMaxAckDelay                   = 0x0b
)

// transportParameter is the integer transport parameter id of params, or
// def (its default) where absent.
func transportParameter(params []quictrace.TransportParameter, id, def uint64) uint64 {
	for _, tp := range params {
		if tp.ID == id && tp.Value != nil {
			return *tp.Value
		}
	}
	return def
}

// increases are the steps by which limits rise from first, in order.
func increases(first uint64, limits []uint64) []uint64 {
	var out []uint64
	last := first
	for _, l := range limits {
		if l > last {
			out = append(out, l-last)
			last = l
		}
	}
	return out
}

// creditSlack is how far mimicry's credit increases may stray outside the
// browser's: one packet's data. A client counts what it consumes in the
// steps its packets arrived in (as the browser does: Chrome's increases
// span 7 KB above its half window), so where the share falls inside the
// packet that crosses it is chance, up to a packet either way.
const creditSlack = 1500

// compareCredit checks every increase mimicry sent against the range of
// the browser's.
func compareCredit(t *testing.T, what string, got []uint64, refs [][]uint64) {
	t.Helper()
	var lo, hi uint64
	n := 0
	for _, r := range refs {
		n += len(r)
		for _, v := range r {
			if lo == 0 || v < lo {
				lo = v
			}
			hi = max(hi, v)
		}
	}
	if hi == 0 {
		if len(got) > 0 {
			t.Errorf("%s: mimicry raised it %d times; the browser never did", what, len(got))
		}
		return
	}
	if len(got) == 0 {
		t.Errorf("%s: mimicry never raised it; the browser did by %d–%d", what, lo, hi)
		return
	}
	for i, v := range got {
		if v+creditSlack < lo || v > hi+creditSlack {
			t.Errorf("%s: mimicry's increase %d of %d is %d; the browser's are %d–%d", what, i+1, len(got), v, lo, hi)
		}
	}
	sorted := slices.Sorted(slices.Values(got))
	t.Logf("%s: mimicry raised it %d times by %d–%d (median %d); the browser %d times over %d samples, by %d–%d", what, len(got), sorted[0], sorted[len(sorted)-1], sorted[len(sorted)/2], n, len(refs), lo, hi)
}

// afterDownload checks that the requests after the download went on its
// QUIC connection, as in every browser's sample.
func afterDownload(t *testing.T, page *capture.Page) {
	t.Helper()
	x := transports(page)
	find := func(label string) (h3Transport, bool) {
		for path, tr := range x {
			if strings.HasPrefix(path, "/h3/"+label) {
				return tr, true
			}
		}
		return h3Transport{}, false
	}
	download, ok := find("download.so")
	if !ok || download.proto != "h3" {
		t.Fatalf("the download went %v, not over HTTP/3", download)
	}
	for _, label := range []string{"after1.so", "after2.so", "after3.so"} {
		if tr, ok := find(label); !ok || tr != download {
			t.Errorf("%s went %v; the download's connection was %v", label, tr, download)
		}
	}
}
