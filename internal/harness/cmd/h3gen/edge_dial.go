package main

import (
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// dialWindowMillis bounds how long before a QUIC attempt's first datagram
// the request that started it was made: an attempt with no request made
// to its host that shortly before followed something else (a dead
// connection's reconnection, a give-up).
const dialWindowMillis = 50

// dialClockMillis is the made time's resolution: ?t= is the page's whole
// milliseconds, shifted by the sample's offset, which rounds too. Delays
// no longer than this are a dial at once.
const dialClockMillis = 3

// edgeSteps are each edge scenario's steps.
var edgeSteps = map[string]func() []server.H3Step{
	scenarioBlackhole:      server.H3BlackholeSteps,
	scenarioBroken:         server.H3BrokenSteps,
	scenarioVN:             server.H3VNSteps,
	scenarioDownload:       server.H3DownloadSteps,
	scenarioEarly:          server.H3EarlySteps,
	scenarioTooEarly:       server.H3EarlySteps,
	scenarioStreamsBlocked: server.H3StreamsBlockedSteps,
}

// dialDelays derive http3.discovery.dialDelayMs and resumedDialDelayMs:
// how long after the request that waits for it (dialledBy) each fresh
// and each resumed QUIC attempt of the edge pages started. Each is nil
// where every delay of its kind is within dialClockMillis (or none was
// measured), otherwise every one of them, sorted: a dial draws one, so
// the delays and their outliers come as often as in the samples, which a
// range drawn uniformly would not (Firefox's 0–34 ms would average 17
// ms against the samples' 5).
func (s *edgeSet) dialDelays(e *collector) (fresh, resumed []int) {
	var delays [2][]int

	for _, p := range s.pages {
		steps := edgeSteps[p.scenario]
		if steps == nil {
			edgeErr(e, p.at, "no steps for scenario %s", p.scenario)

			continue
		}

		kinds := map[string]string{}
		for _, st := range steps() {
			kinds[st.Label] = st.Kind
		}

		for _, a := range p.attempts {
			r := p.dialledBy(a)
			if r == nil || kinds[r.name()] == "navigate" {
				// A navigation's own processing comes before its request
				// is made on the wire.
				continue
			}

			kind := 0
			if a.resumed {
				kind = 1
			}

			delays[kind] = append(delays[kind], int(a.start-r.made))
		}
	}

	for i := range delays {
		if len(delays[i]) == 0 || slices.Max(delays[i]) <= dialClockMillis {
			delays[i] = nil
		}

		slices.Sort(delays[i])
	}

	return delays[0], delays[1]
}

// dialledBy is the request that started a and waits for it: the last one
// to its host made within dialWindowMillis before a's first datagram that
// went on a, or on TCP at least backgroundMillis after a started (the
// race's wait), with no TCP connection to the host dialled in between
// (Discovery.RaceAfterGoAway's); nil for none. An attempt beside a
// request on TCP (Chrome's next_request, Firefox's validations) has
// none.
func (p *edgePage) dialledBy(a *attempt) *edgeReq {
	var out *edgeReq

	for i := range p.reqs {
		r := &p.reqs[i]
		if r.host != a.c.Host || r.made < 0 || r.made > a.start || a.start-r.made > dialWindowMillis {
			continue
		}

		if out == nil || r.made > out.made {
			out = r
		}
	}

	switch {
	case out == nil:
		return nil
	case len(p.tcpHellos(out.host, float64(out.made), float64(a.start))) > 0:
		return nil
	case out.quic == a.index:
		return out
	case out.quic < 0 && out.arrival-a.start >= backgroundMillis:
		return out
	}

	return nil
}
