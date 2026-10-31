package main

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// streamsBlockedBidi is the STREAMS_BLOCKED frame type for bidirectional
// streams.
const streamsBlockedBidi = 0x16

// Where a STREAMS_BLOCKED sent with a waiting request's stream sits:
// right before or right after that stream's STREAM frame.
const (
	beforeStream = "before"
	afterStream  = "after"
)

// streamsBlocked derives quic.streamsBlocked from the h3-streams-blocked
// samples; nil without them (not measured). Each burst's six requests are
// made together while the server allows two more streams, so four wait at
// that limit (the first STREAMS_BLOCKED sent alone says which) and get
// their streams as MAX_STREAMS raises it. From each burst: how many
// frames the waiting requests sent alone (one per request, or one for the
// limit), and which of them carried one when they opened their stream,
// where (before or after its STREAM frame) and with which limit. Any
// other STREAMS_BLOCKED, a unidirectional one, or a burst or sample that
// says otherwise is refused.
func (s *edgeSet) streamsBlocked(e *collector) *profile.QUICStreamsBlocked {
	var out *profile.QUICStreamsBlocked
	for _, p := range s.scenario(scenarioStreamsBlocked) {
		got, ok := pageStreamsBlocked(p, e)
		if !ok {
			continue
		}

		if out != nil && *got != *out {
			edgeErr(e, p.at, "quic.streamsBlocked %+v, other samples %+v", *got, *out)

			continue
		}

		out = got
	}

	return out
}

// blockedBursts are the h3-streams-blocked page's bursts, each its
// requests' labels: a held-back step with a pause starts one, and the
// held-back steps after it without one join it.
func blockedBursts() [][]string {
	var out [][]string
	for _, st := range server.H3StreamsBlockedSteps() {
		if st.Delay == 0 {
			continue
		}

		if st.PauseMillis > 0 || len(out) == 0 {
			out = append(out, nil)
		}

		out[len(out)-1] = append(out[len(out)-1], st.Label)
	}

	return out
}

// blockedConn is a QUIC connection's client packets, in the order sent,
// and where each bidirectional stream opened.
type blockedConn struct {
	packets []blockedPacket
	// opened is the index in packets of each stream's first STREAM frame,
	// by stream number.
	opened map[uint64]int
}

// blockedPacket is a client packet and when it was sent.
type blockedPacket struct {
	ms float64
	p  *quictrace.Packet
	// alone: the only packet in its datagram.
	alone bool
}

func newBlockedConn(a *attempt) *blockedConn {
	bc := &blockedConn{
		opened: map[uint64]int{},
	}

	for di := range a.c.Datagrams {
		d := &a.c.Datagrams[di]
		for pi := range d.Packets {
			pk := &d.Packets[pi]
			for _, f := range pk.Frames {
				if n, ok := bidiStream(f); ok {
					if _, seen := bc.opened[n]; !seen {
						bc.opened[n] = len(bc.packets)
					}
				}
			}

			bc.packets = append(bc.packets, blockedPacket{
				ms:    a.millis(d.Micros),
				p:     pk,
				alone: len(d.Packets) == 1,
			})
		}
	}

	return bc
}

// bidiStream is the number of the client-initiated bidirectional stream f
// is a STREAM frame of.
func bidiStream(f quictrace.Frame) (uint64, bool) {
	if f.Type != quictrace.FrameStream || f.Stream == nil || *f.Stream%4 != 0 {
		return 0, false
	}

	return *f.Stream / 4, true
}

// pageStreamsBlocked is the rule one sample's bursts agree on.
func pageStreamsBlocked(p *edgePage, e *collector) (*profile.QUICStreamsBlocked, bool) {
	conns := map[int]*blockedConn{}
	total := 0

	for _, a := range p.attempts {
		for _, d := range a.c.Datagrams {
			for _, pk := range d.Packets {
				for _, f := range pk.Frames {
					if f.Type == quictrace.FrameStreamsBlocked {
						total++
					}
				}
			}
		}
	}

	var (
		out     *profile.QUICStreamsBlocked
		counted int
	)

	for bi, labels := range blockedBursts() {
		at := fmt.Sprintf("%s, burst %d", p.at, bi+1)

		got, n, ok := burstStreamsBlocked(p, labels, conns, at, e)
		if !ok {
			return nil, false
		}

		if out != nil && *got != *out {
			edgeErr(e, at, "quic.streamsBlocked %+v, the burst before %+v", *got, *out)

			return nil, false
		}

		out = got

		counted += n
	}

	if counted != total {
		edgeErr(e, p.at, "%d STREAMS_BLOCKED, %d of them sent by the bursts' waiting requests", total, counted)

		return nil, false
	}

	return out, true
}

// burstStreamsBlocked is the rule one burst shows, and how many
// STREAMS_BLOCKED it accounts for.
func burstStreamsBlocked(p *edgePage, labels []string, conns map[int]*blockedConn, at string, e *collector) (*profile.QUICStreamsBlocked, int, bool) {
	type req struct {
		label string
		made  int64
		num   uint64
	}

	qi := -1

	var reqs []req

	for _, l := range labels {
		r := p.request(l)
		if r == nil || r.quic < 0 || qi >= 0 && r.quic != qi {
			edgeErr(e, at, "%s did not go on the burst's QUIC connection", l)

			return nil, 0, false
		}

		qi = r.quic

		num, ok := streamOf(p.page.QUIC[qi].H3, l)
		if !ok {
			edgeErr(e, at, "%s: no stream", l)

			return nil, 0, false
		}

		reqs = append(reqs, req{
			label: l,
			made:  r.made,
			num:   num,
		})
	}

	bc := conns[qi]
	if bc == nil {
		bc = newBlockedConn(p.attempts[qi])
		conns[qi] = bc
	}

	slices.SortFunc(reqs, func(a, b req) int {
		return cmp.Compare(a.num, b.num)
	})

	for i, r := range reqs {
		if i > 0 && r.made < reqs[i-1].made {
			edgeErr(e, at, "%s got stream %d before %s, made earlier", reqs[i-1].label, reqs[i-1].num, r.label)

			return nil, 0, false
		}

		if _, ok := bc.opened[r.num]; !ok {
			edgeErr(e, at, "%s: no STREAM frame for stream %d", r.label, r.num)

			return nil, 0, false
		}
	}

	first, last := bc.opened[reqs[0].num], bc.opened[reqs[len(reqs)-1].num]

	// The STREAMS_BLOCKED sent alone: the limit the burst's waiting
	// requests were made at.
	var limit uint64

	alone := 0

	for i := first; i < last; i++ {
		pk := bc.packets[i]

		sb, others := framesOf(pk.p)
		if len(sb) == 0 || streamsIn(pk.p) {
			continue
		}

		var openedBefore uint64
		for _, idx := range bc.opened {
			if idx < i {
				openedBefore++
			}
		}

		if limit == 0 {
			limit = sb[0].Max
		}

		switch {
		case !pk.alone || len(sb) != 1 || others > 0:
			edgeErr(e, at, "a STREAMS_BLOCKED at %.1f ms shares its packet or datagram", pk.ms)
		case sb[0].Code != streamsBlockedBidi:
			edgeErr(e, at, "a STREAMS_BLOCKED for unidirectional streams at %.1f ms", pk.ms)
		case sb[0].Max != limit:
			edgeErr(e, at, "STREAMS_BLOCKED %d at %.1f ms, the burst's first %d", sb[0].Max, pk.ms, limit)
		case openedBefore != limit:
			edgeErr(e, at, "STREAMS_BLOCKED %d at %.1f ms with %d streams open", limit, pk.ms, openedBefore)
		default:
			alone++

			continue
		}

		return nil, 0, false
	}

	if limit == 0 {
		edgeErr(e, at, "no STREAMS_BLOCKED when a request found the stream limit reached: no value of quic.streamsBlocked says so")

		return nil, 0, false
	}

	raise, ok := bc.opened[limit]
	if !ok {
		edgeErr(e, at, "stream %d, the first past the limit, never opened", limit)

		return nil, 0, false
	}

	var waited []uint64

	for _, r := range reqs {
		if r.num < limit {
			continue
		}

		if raised := bc.packets[raise].ms; float64(r.made) >= raised {
			edgeErr(e, at, "%s made %.1f ms after the limit was raised: the burst's requests are made together", r.label, float64(r.made)-raised)

			return nil, 0, false
		}

		waited = append(waited, r.num)
	}

	out := &profile.QUICStreamsBlocked{}

	switch {
	case len(waited) < 2:
		edgeErr(e, at, "%d request waited: one STREAMS_BLOCKED per request and one per limit look alike", len(waited))

		return nil, 0, false

	case alone == 1:
		out.AtRequest = profile.StreamsBlockedFirstRequest
	case alone == len(waited):
		out.AtRequest = profile.StreamsBlockedEveryRequest
	default:
		edgeErr(e, at, "%d STREAMS_BLOCKED sent alone for %d waiting requests", alone, len(waited))

		return nil, 0, false
	}

	carried, ok := carriedBlocked(bc, waited, reqs[0].num, at, e)
	if !ok {
		return nil, 0, false
	}

	if out.AfterRaise, ok = afterRaise(bc, waited, carried, at, e); !ok {
		return nil, 0, false
	}

	return out, alone + len(carried), true
}

// carriedBlocked is where each waiting stream's opening packet put a
// STREAMS_BLOCKED, by stream number: right before its STREAM frame or
// right after, always with its number + 1 as the limit (the limit its
// stream took up). Any other STREAMS_BLOCKED in a burst stream's opening
// packet is refused.
func carriedBlocked(bc *blockedConn, waited []uint64, firstNum uint64, at string, e *collector) (map[uint64]string, bool) {
	carried := map[uint64]string{}

	var seen []int

	for n := firstNum; n <= waited[len(waited)-1]; n++ {
		idx := bc.opened[n]
		if slices.Contains(seen, idx) {
			continue
		}

		seen = append(seen, idx)

		frames := bc.packets[idx].p.Frames
		for fi, f := range frames {
			if f.Type != quictrace.FrameStreamsBlocked {
				continue
			}

			limit := f.Max
			prev, hasPrev := neighbourStream(frames[:fi], true)
			next, hasNext := neighbourStream(frames[fi+1:], false)

			var where string

			switch {
			case f.Code != streamsBlockedBidi:
				edgeErr(e, at, "a STREAMS_BLOCKED for unidirectional streams with stream %d", n)

				return nil, false

			case limit > 0 && hasPrev && prev == limit-1:
				where = afterStream
			case limit > 0 && hasNext && next == limit-1:
				where = beforeStream
			default:
				edgeErr(e, at, "STREAMS_BLOCKED %d next to neither stream %d's STREAM frame", limit, limit-1)

				return nil, false
			}

			if !slices.Contains(waited, limit-1) {
				edgeErr(e, at, "STREAMS_BLOCKED %d with stream %d, which did not wait", limit, limit-1)

				return nil, false
			}

			if _, dup := carried[limit-1]; dup {
				edgeErr(e, at, "two STREAMS_BLOCKED with stream %d", limit-1)

				return nil, false
			}

			carried[limit-1] = where
		}
	}

	return carried, true
}

// afterRaise is the quic.streamsBlocked.afterRaise carried shows: none;
// after the STREAM frame of each packet's last waiting stream, the
// burst's last too, others' optional (StreamsBlockedRaiseAlways); or
// before that of every waiting stream but the last
// (StreamsBlockedRaiseWaiting).
func afterRaise(bc *blockedConn, waited []uint64, carried map[uint64]string, at string, e *collector) (string, bool) {
	if len(carried) == 0 {
		return "", true
	}

	var places []string
	for _, w := range carried {
		if !slices.Contains(places, w) {
			places = append(places, w)
		}
	}

	if len(places) > 1 {
		edgeErr(e, at, "STREAMS_BLOCKED both before and after its stream's STREAM frame")

		return "", false
	}

	if places[0] == afterStream {
		for i, n := range waited {
			lastInPacket := i == len(waited)-1 || bc.opened[waited[i+1]] != bc.opened[n]
			if _, ok := carried[n]; lastInPacket && !ok {
				edgeErr(e, at, "no STREAMS_BLOCKED after stream %d, the last its packet opened", n)

				return "", false
			}
		}

		return profile.StreamsBlockedRaiseAlways, true
	}

	for i, n := range waited {
		_, ok := carried[n]
		if still := i < len(waited)-1; ok != still {
			edgeErr(e, at, "STREAMS_BLOCKED before stream %d: %v, a request still waiting: %v", n, ok, still)

			return "", false
		}
	}

	return profile.StreamsBlockedRaiseWaiting, true
}

// neighbourStream is the number of the bidirectional stream whose STREAM
// frame comes first in frames (last, with last), false for none.
func neighbourStream(frames []quictrace.Frame, last bool) (uint64, bool) {
	for i := range frames {
		f := frames[i]
		if last {
			f = frames[len(frames)-1-i]
		}

		if n, ok := bidiStream(f); ok {
			return n, true
		}
	}

	return 0, false
}

// framesOf are p's STREAMS_BLOCKED frames, and how many others it has
// besides PADDING.
func framesOf(p *quictrace.Packet) (sb []quictrace.Frame, others int) {
	for _, f := range p.Frames {
		switch f.Type {
		case quictrace.FrameStreamsBlocked:
			sb = append(sb, f)
		case quictrace.FramePadding:
		default:
			others++
		}
	}

	return sb, others
}

// streamsIn reports whether p carries a STREAM frame of a bidirectional
// stream.
func streamsIn(p *quictrace.Packet) bool {
	return slices.ContainsFunc(p.Frames, func(f quictrace.Frame) bool {
		_, ok := bidiStream(f)
		return ok
	})
}

// streamOf is the stream number of the request labelled l on h.
func streamOf(h *quictrace.H3, l string) (uint64, bool) {
	if h == nil {
		return 0, false
	}

	for _, r := range h.Requests {
		if nameOf(label(r.Path)) == l {
			return r.Stream / 4, true
		}
	}

	return 0, false
}
