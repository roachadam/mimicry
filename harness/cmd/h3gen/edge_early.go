package main

import (
	"maps"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// earlyData derives http3.early_data from the h3-early and h3-too-early
// captures, for a profile that resumes with 0-RTT (q.ZeroRTT); nil
// otherwise. Every request the page made before its connection's
// Finished, the first time its URL arrived, counts: one whose stream
// started in a 0-RTT packet was sent early, any other was held, and each
// method is the one or the other on every connection. A held request
// leaves in the Finished's datagram or after it, the same on every
// connection. On the h3-too-early page, whose server answers every 0-RTT
// request 425, each went again on its connection in 1-RTT, or none did.
func (s *edgeSet) earlyData(q *profile.QUIC, e *collector) *profile.H3EarlyData {
	if !q.ZeroRTT {
		return nil
	}
	early := map[string]bool{}
	var withFinished []bool
	var withFinishedAt []string
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
				return int(x.Stream) - int(y.Stream) //nolint:gosec // stream IDs are small
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
				case was != sent && !s.gap(p.at, "early_data.methods "+r.Method):
					edgeErr(e, p.at, "%s %s sent in 0-RTT %v, another %s %v", r.Method, nameOf(l), sent, r.Method, was)
				}
				if !sent {
					if !s.gap(p.at, "early_data.held_with_finished "+nameOf(l)) {
						withFinished = append(withFinished, pos.Datagram == finished)
						withFinishedAt = append(withFinishedAt, p.at+" "+nameOf(l))
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
