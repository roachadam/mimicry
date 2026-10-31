package main

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// earlyData derives http3.earlyData from the h3-early and h3-too-early
// captures; nil unless q.ZeroRTT. Each method must be consistently sent
// in 0-RTT or held until Finished, and 425 retries consistent too. With
// h.RequestsAfterConfirmed every held request must leave once the
// handshake is confirmed (confirmedBy), as the profile sends it.
func (s *edgeSet) earlyData(q *profile.QUIC, h *profile.HTTP3, e *collector) *profile.H3EarlyData {
	if !q.ZeroRTT {
		return nil
	}

	early := map[string]bool{}

	var (
		withFinished   []bool
		withFinishedAt []string
	)

	retried, notRetried := 0, 0

	for _, p := range slices.Concat(s.scenario(scenarioEarly), s.scenario(scenarioTooEarly)) {
		seen := map[string]bool{}
		for _, a := range p.attempts {
			c := a.c
			if !c.EarlyData || c.H3 == nil {
				continue
			}

			finished, finishedAt := c.FinishedDatagram()
			first := c.StreamStarts()
			reqs := slices.Clone(c.H3.Requests)
			slices.SortFunc(reqs, func(x, y quictrace.H3Request) int {
				return int(x.Stream) - int(y.Stream)
			})

			for _, r := range reqs {
				l := label(r.Path)
				if seen[l] {
					continue
				}

				seen[l] = true

				req := p.request(nameOf(l))
				pos, ok := first[r.Stream]

				if req == nil || req.made < 0 || finished < 0 || !ok || float64(req.made) >= a.millis(finishedAt) {
					// Made after the handshake (or no time to tell): it
					// went in 1-RTT whatever its method.
					continue
				}

				sent := pos.PacketType == quictrace.Packet0RTT
				switch was, ok := early[r.Method]; {
				case !ok:
					early[r.Method] = sent
				case was != sent && !s.gap(p.at, "earlyData.methods "+r.Method):
					edgeErr(e, p.at, "%s %s sent in 0-RTT %v, another %s %v", r.Method, nameOf(l), sent, r.Method, was)
				}

				if !sent {
					if !s.gap(p.at, "earlyData.heldWithFinished "+nameOf(l)) {
						withFinished = append(withFinished, pos.Datagram == finished)
						withFinishedAt = append(withFinishedAt, p.at+" "+nameOf(l))
					}

					if h.RequestsAfterConfirmed && !confirmedBy(c, finished, pos.Datagram) {
						edgeErr(e, p.at, "held %s left before the handshake was confirmed (requestsAfterConfirmed)", nameOf(l))
					}

					continue
				}

				if p.scenario != scenarioTooEarly {
					continue
				}

				// Answered 425: sent again on this connection, in 1-RTT?
				again := slices.ContainsFunc(reqs, func(o quictrace.H3Request) bool {
					f, ok := first[o.Stream]
					return o.Stream != r.Stream && label(o.Path) == l && ok && f.PacketType == quictrace.Packet1RTT
				})
				if again {
					retried++
				} else {
					notRetried++
				}
			}
		}
	}

	out := &profile.H3EarlyData{}
	for _, m := range slices.Sorted(maps.Keys(early)) {
		if early[m] {
			out.Methods = append(out.Methods, m)
		}
	}

	if len(out.Methods) == 0 {
		edgeErr(e, s.set.name, "no request went in 0-RTT on the h3-early pages")
	}

	if len(withFinished) == 0 {
		edgeErr(e, s.set.name, "no request was held for the handshake on the h3-early pages")
	} else {
		out.HeldWithFinished = withFinished[0]
		for i, v := range withFinished {
			if v != out.HeldWithFinished {
				edgeErr(e, withFinishedAt[i], "held request with the Finished %v, %s's %v", v, withFinishedAt[0], out.HeldWithFinished)
			}
		}
	}

	switch {
	case retried+notRetried == 0:
		edgeErr(e, s.set.name, "no 0-RTT request on the h3-too-early pages")
	case retried > 0 && notRetried > 0:
		edgeErr(e, s.set.name, "%d requests answered 425 went again on their connection, %d did not", retried, notRetried)
	}

	out.RetryTooEarly = retried > 0

	return out
}

// nameOf is a label without its query.
func nameOf(l string) string {
	name, _, _ := strings.Cut(l, "?")
	return name
}

// earlyStreamsWithRequest derives http3.earlyStreamsWithRequest from the
// h3-early and h3-too-early connections dialled for a request sent in
// 0-RTT (the connection's first request stream went early) with no
// request held for the handshake beside it: whether the control stream's
// first STREAM frame went in that request's datagram, in every one or in
// none. Not measured with h.EarlyRequestFirst, whose request leads the
// streams.
func (s *edgeSet) earlyStreamsWithRequest(h *profile.HTTP3, e *collector) bool {
	if h.EarlyData == nil || h.EarlyRequestFirst {
		return false
	}

	var (
		vs []bool
		at []string
	)

	for _, p := range slices.Concat(s.scenario(scenarioEarly), s.scenario(scenarioTooEarly)) {
		for _, a := range p.attempts {
			c := a.c
			if !c.EarlyData || c.H3 == nil || len(c.H3.Requests) == 0 {
				continue
			}

			starts := c.StreamStarts()
			first, control := uint64(math.MaxUint64), uint64(math.MaxUint64)
			held := false

			for _, r := range c.H3.Requests {
				first = min(first, r.Stream)
				held = held || !slices.Contains(h.EarlyData.Methods, r.Method)
			}

			for _, st := range c.H3.Streams {
				if st.Type == quictrace.StreamControl {
					control = st.ID
				}
			}

			req, ok := starts[first]
			ctl, cok := starts[control]

			if held || !ok || !cok || req.PacketType != quictrace.Packet0RTT {
				continue
			}

			vs = append(vs, req.Datagram == ctl.Datagram)
			at = append(at, fmt.Sprintf("%s, QUIC connection %d", p.at, a.index+1))
		}
	}

	for i, v := range vs {
		if v != vs[0] {
			edgeErr(e, at[i], "the streams went with the 0-RTT request %v, %s's %v", v, at[0], vs[0])
		}
	}

	return len(vs) > 0 && vs[0]
}

// confirmedBy reports whether c's client datagram until arrived after the
// server answered the client's Finished (datagram finished): after the
// server's first datagram sent once the Finished had arrived, which
// carries HANDSHAKE_DONE.
func confirmedBy(c *quictrace.Conn, finished, until int) bool {
	if finished < 0 || until >= len(c.Datagrams) {
		return false
	}

	at := c.Datagrams[finished].Micros
	for _, d := range c.Server {
		if d.Micros > at {
			return c.Datagrams[until].Micros > d.Micros
		}
	}

	return false
}
