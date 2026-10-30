package main

import (
	"fmt"
	"math"
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// backgroundMillis: a request that needed a new connection while a QUIC
// attempt ran and reached the server within this long did not wait for
// it.
const backgroundMillis = 50

// raceEvent is a request that needed a new connection, and how long it
// waited for QUIC before it went on TCP.
type raceEvent struct {
	at    string
	label string
	// wait is from when it started waiting (the request, or the QUIC
	// attempt it started) to its TCP connection's ClientHello, or to its
	// arrival on an open TCP connection.
	wait float64
	// attempt: a QUIC attempt was running or started with it.
	attempt bool
	// zeroRTT: the attempt it started sends 0-RTT.
	zeroRTT bool
	// tcpFirst: TCP was dialled before the QUIC attempt it started
	// (Discovery.RaceAfterGoAway).
	tcpFirst bool
	// started: the request started its attempt.
	started bool
}

// sendsZeroRTT reports whether a's first flight carries a 0-RTT packet.
func sendsZeroRTT(a *attempt) bool {
	for _, d := range a.flights[0] {
		if slices.ContainsFunc(d.Packets, func(p quictrace.Packet) bool {
			return p.Type == quictrace.Packet0RTT
		}) {
			return true
		}
	}

	return false
}

// race measures how r, which needed a new connection, waited.
func race(p *edgePage, r *edgeReq) raceEvent {
	ev := raceEvent{
		at:    p.at,
		label: r.name(),
	}
	made := float64(r.made)
	started := p.attemptsTo(r.host, made-5, made+50)

	if len(started) == 0 {
		for _, a := range p.attemptsTo(r.host, math.Inf(-1), made) {
			ev.attempt = ev.attempt || a.end() >= made && !a.answered
		}

		ev.wait = float64(r.delay())

		return ev
	}

	a := started[0]

	ev.attempt, ev.started, ev.zeroRTT = true, true, sendsZeroRTT(a)

	if before := p.tcpHellos(r.host, made-5, float64(a.start)); len(before) > 0 {
		ev.tcpFirst = true
		return ev
	}

	if tcp := p.tcpHellos(r.host, float64(a.start), float64(a.start)+10000); len(tcp) > 0 && float64(tcp[0]) <= float64(r.arrival) {
		ev.wait = float64(tcp[0] - a.start)
	} else {
		ev.wait = float64(r.arrival - a.start)
	}

	return ev
}

// raceEvents are the edge pages' requests that needed a new connection
// with QUIC not up: h3-blackhole's first requests after each GOAWAY,
// h3-vn's, and h3-broken's first two after the server's close where they
// started an attempt of their own (not held by one: afterClose).
func (s *edgeSet) raceEvents(e *collector) []raceEvent {
	var out []raceEvent
	for _, p := range s.pages {
		var labels []string
		switch p.scenario {
		case scenarioBlackhole:
			labels = []string{"after-goaway1.so", "after-goaway2.so"}
		case scenarioVN:
			labels = []string{"vn-3.so"}
		case scenarioBroken:
			labels = []string{"closed1.cs", "closed2.cs"}
		}

		for _, l := range labels {
			r := p.request(l)
			if r == nil {
				edgeErr(e, p.at, "no request %s", l)

				continue
			}

			ev := race(p, r)
			if ev.attempt && (p.scenario != scenarioBroken || ev.started) {
				out = append(out, ev)
			}
		}
	}

	return out
}

// failure is one failed QUIC attempt of an edge page, or a Version
// Negotiation and its retry together.
type failure struct {
	resumed bool
	// end is when it failed: its close, its last datagram if dropped
	// before a probe, or its idle timeout.
	end float64
}

// failures are p's failed attempts to host from after, in order, and
// whether an attempt started after the last of them (an unfinished one
// included).
func (s *edgeSet) failures(q *profile.QUIC, p *edgePage, host string, after float64) (fs []failure, more bool) {
	idle := math.Inf(1)
	for _, tp := range q.TransportParameters {
		if tp.ID == profile.TPMaxIdleTimeout && tp.Value != nil {
			idle = float64(*tp.Value)
		}
	}

	pageEnd := 0.0
	for _, r := range p.reqs {
		pageEnd = max(pageEnd, float64(r.arrival))
	}

	for _, a := range p.attemptsTo(host, after, math.Inf(1)) {
		if a.answered && a.c.H3 != nil {
			more = true
			continue
		}

		u := unanswered{
			p: p,
			a: a,
		}
		end := float64(a.start) + idle

		switch f := a.closeFlight(); {
		case f != nil:
			end = a.millis(f[0].Micros)
		case a.vn || u.abandoned():
			end = a.end()
		}

		if n := len(fs); n > 0 && fs[n-1].end >= float64(a.start)-10 && float64(a.start)-fs[n-1].end <= 10 && !a.resumed && s.vnRetry(p, a) {
			// A Version Negotiation's retry is the same attempt.
			fs[n-1].end = end
			continue
		}

		if end > pageEnd {
			more = true
			continue
		}

		fs = append(fs, failure{
			resumed: a.resumed,
			end:     end,
		})
		more = false
	}

	return fs, more
}

// vnRetry reports whether a followed another attempt's Version
// Negotiation.
func (s *edgeSet) vnRetry(p *edgePage, a *attempt) bool {
	return a.index > 0 && p.attempts[a.index-1].vn && a.c.Version != "00000001"
}

// brokenSeq is one sample's failed attempts to a host, and whether the
// browser tried QUIC again when it could have.
type brokenSeq struct {
	at string
	fs []failure
	// stopped: a request that could have started an attempt came after
	// the last failure, and none did.
	stopped bool
	// more: an attempt started after the last failure.
	more bool
}

// counted is how many of fs count with countsResumed.
func counted(fs []failure, countsResumed bool) int {
	n := 0
	for _, f := range fs {
		if countsResumed || !f.resumed {
			n++
		}
	}

	return n
}

// fallback derives http3.discovery.fallback.
func (s *edgeSet) fallback(q *profile.QUIC, d *profile.Discovery, e *collector) profile.Fallback {
	var out profile.Fallback

	events := s.raceEvents(e)

	var (
		waits  []int
		pinned []raceEvent
	)

	for _, ev := range events {
		if ev.tcpFirst || ev.zeroRTT && ev.wait > 1000 {
			// Discovery.RaceAfterGoAway's, or a 0-RTT attempt's
			// hold (afterClose).
			continue
		}

		if ev.wait < backgroundMillis {
			if out.Mode == profile.FallbackRace {
				edgeErr(e, ev.at, "%s waited %.0f ms for QUIC, other requests longer", ev.label, ev.wait)
			}

			out.Mode = profile.FallbackBackground

			continue
		}

		if out.Mode == profile.FallbackBackground {
			edgeErr(e, ev.at, "%s waited %.0f ms for QUIC, other requests not at all", ev.label, ev.wait)
		}

		out.Mode = profile.FallbackRace

		if _, ok := sampleGaps[ev.at+": tcpDelayMs "+ev.label]; ok {
			pinned = append(pinned, ev)
			continue
		}

		waits = append(waits, int(ev.wait))
	}

	if out.Mode == profile.FallbackRace && len(waits) > 0 {
		out.TCPDelayMillis = span(waits)
		// A pin holds only a wait the others' range leaves out.
		for _, ev := range pinned {
			if r := out.TCPDelayMillis; int(ev.wait) < r.Min || int(ev.wait) > r.Max {
				s.gap(ev.at, "tcpDelayMs "+ev.label)
			}
		}
	}

	fresh, resumed := s.unansweredAttempts()
	abandoned := 0

	for _, u := range append(fresh, resumed...) {
		if u.abandoned() {
			abandoned++
		}
	}

	out.AbandonOnTCP = abandoned > 0
	s.afterClose(d, &out, e)
	s.broken(q, d, &out, e)
	s.pending(&out, e)

	return out
}

// closedHost is a h3-broken sample's host whose QUIC connection the
// server closed (close.cs), and when.
func closedHost(p *edgePage) (string, float64, error) {
	r := p.request("close.cs")
	if r == nil || r.quic < 0 {
		return "", 0, fmt.Errorf("close.cs did not go over QUIC")
	}

	c := p.attempts[r.quic].c
	if c.Close == nil {
		return "", 0, fmt.Errorf("close.cs's connection did not close")
	}

	return c.Host, float64(c.Close.Millis), nil
}

// afterClose derives what h3-broken's requests after the server's close
// show: whether a 0-RTT attempt holds them (ZeroRTTHolds, its fresh retry
// and TCP together) or a re-validation does (Hold).
func (s *edgeSet) afterClose(d *profile.Discovery, out *profile.Fallback, e *collector) {
	for i, p := range s.scenario(scenarioBroken) {
		host, closed, err := closedHost(p)
		if err != nil {
			edgeErr(e, p.at, "%v", err)

			continue
		}

		r := p.request("closed1.cs")
		as := p.attemptsTo(host, closed, math.Inf(1))

		if r == nil || len(as) == 0 {
			edgeErr(e, p.at, "no QUIC attempt after the server's close")

			continue
		}

		a := as[0]
		end := a.end()

		if f := a.closeFlight(); f != nil {
			end = a.millis(f[0].Micros)
		}

		holds, hold := false, false
		switch ev := race(p, r); {
		case ev.zeroRTT && !ev.tcpFirst && ev.wait > 1000:
			holds = true

			if tcp := p.tcpHellos(host, float64(a.start), end-5); len(tcp) > 0 {
				edgeErr(e, p.at, "TCP at %d while the 0-RTT attempt held closed1.cs", tcp[0])
			}

			if len(as) < 2 || math.Abs(float64(as[1].start)-end) > 5 || as[1].resumed {
				edgeErr(e, p.at, "no fresh attempt when the 0-RTT one gave up at %.0f", end)
			} else if tcp := p.tcpHellos(host, end-5, end+5); len(tcp) == 0 {
				edgeErr(e, p.at, "no TCP with the fresh attempt at %.0f", end)
			} else {
				s.checkArrivals(p, host, float64(as[1].start), as[1].end(), 0, 20, "made while the fresh attempt ran", e)
			}

		case ev.tcpFirst:
			hold = d.RaceAfterGoAway
		}

		if holds || hold {
			s.checkArrivals(p, host, float64(a.start), end, end, 20, "held by the attempt after the close", e)
		}

		if i > 0 && (holds != out.ZeroRTTHolds || hold != out.Hold) {
			edgeErr(e, p.at, "zeroRttHolds %v hold %v, others %v %v", holds, hold, out.ZeroRTTHolds, out.Hold)
		}

		out.ZeroRTTHolds, out.Hold = holds, hold
	}
}

// checkArrivals checks that the requests to host made in [from, to)
// arrived on TCP within [base, base+within] ms, base 0 meaning their own
// making.
func (s *edgeSet) checkArrivals(p *edgePage, host string, from, to, base, within float64, what string, e *collector) {
	for _, r := range p.reqs {
		made := float64(r.made)
		if r.host != host || r.made < 0 || made < from || made >= to {
			continue
		}

		b := base
		if b == 0 {
			b = made
		}

		if r.quic >= 0 || float64(r.arrival) < b-1 || float64(r.arrival) > b+within {
			edgeErr(e, p.at, "%s, %s arrived at %d (QUIC %v), want TCP within %.0f ms of %.0f", what, r.label, r.arrival, r.quic >= 0, within, b)
		}
	}
}

// broken derives how many failed attempts in a row mark an origin broken
// and whether resumed ones count: the one rule every sample's sequence of
// failures fits, stopping exactly where the browser stopped trying.
func (s *edgeSet) broken(q *profile.QUIC, d *profile.Discovery, out *profile.Fallback, e *collector) {
	var seqs []brokenSeq
	for _, p := range s.pages {
		var host string

		after := math.Inf(-1)
		used := false

		switch p.scenario {
		case scenarioBlackhole, scenarioVN:
			host = "www.h3-a.test"
		case scenarioBroken:
			h, closed, err := closedHost(p)
			if err != nil {
				continue
			}

			host, after, used = h, closed, true

		default:
			continue
		}

		fs, more := s.failures(q, p, host, after)
		if len(fs) == 0 {
			continue
		}

		seq := brokenSeq{
			at:   p.at,
			fs:   fs,
			more: more,
		}
		last := fs[len(fs)-1].end

		if !more {
			for _, r := range p.reqs {
				// A request that could have started an attempt: any
				// once the origin used HTTP/3, or one needing a new
				// connection, or any where Alt-Svc alone moves the
				// origin.
				if r.host == host && r.made >= 0 && float64(r.made) > last+1000 && (used || r.first || d.Switch != profile.SwitchNewConnection) {
					seq.stopped = true
				}
			}
		}

		seqs = append(seqs, seq)
	}

	type rule struct {
		n             int
		countsResumed bool
	}

	var fits []rule

	for _, cr := range []bool{false, true} {
		for n := 1; n <= 5; n++ {
			ok := true
			for _, sq := range seqs {
				for k := 1; k <= len(sq.fs); k++ {
					c := counted(sq.fs[:k], cr)
					last := k == len(sq.fs)
					// Broken after the k-th failure: no later
					// attempt, and stopped if the page asked.
					switch {
					case c >= n && (!last || sq.more):
						ok = false
					case last && sq.stopped && c != n:
						ok = false
					case last && !sq.stopped && c > n:
						ok = false
					}
				}
			}

			if ok {
				fits = append(fits, rule{
					n:             n,
					countsResumed: cr,
				})

				break
			}
		}
	}

	if len(fits) != 1 {
		edgeErr(e, s.set.name, "brokenAfter: %d rules fit the samples' failures (%+v), want 1", len(fits), fits)

		if len(fits) == 0 {
			return
		}
	}

	out.BrokenAfter, out.BrokenCountsResumed = fits[0].n, fits[0].countsResumed
	if out.Mode == profile.FallbackBackground {
		out.RevalidateMillis = s.revalidate(e)
	}
}

// revalidate bounds how long after a resumed attempt failed or a live
// connection died a request starts a fresh attempt: above every request
// that did not, at most the first that did.
func (s *edgeSet) revalidate(e *collector) *profile.Range {
	lower, upper := 0, math.MaxInt
	for _, p := range s.scenario(scenarioBroken) {
		type event struct {
			host string
			at   float64
		}

		var events []event

		if host, closed, err := closedHost(p); err == nil {
			for _, a := range p.attemptsTo(host, closed, math.Inf(1)) {
				if f := a.closeFlight(); f != nil && a.resumed {
					events = append(events, event{
						host: host,
						at:   a.millis(f[0].Micros),
					})
				}

				break
			}
		}

		for _, sl := range s.silenced {
			if sl.p == p && sl.a.c.Close != nil && sl.a.c.Close.By == "idle" {
				events = append(events, event{
					host: sl.a.c.Host,
					at:   float64(sl.a.c.Close.Millis),
				})
			}
		}

		for _, ev := range events {
			for _, r := range p.reqs {
				made := float64(r.made)
				if r.host != ev.host || r.made < 0 || made <= ev.at {
					continue
				}

				if as := p.attemptsTo(ev.host, made-5, made+50); len(as) > 0 && !as[0].resumed {
					upper = min(upper, int(made-ev.at))
					break
				}

				lower = max(lower, int(made-ev.at)+1)
			}
		}
	}

	if upper == math.MaxInt || lower > upper {
		edgeErr(e, s.set.name, "revalidateMs: no request after a failure started a fresh attempt, or bounds %d > %d", lower, upper)

		return nil
	}

	return &profile.Range{
		Min: lower,
		Max: upper,
	}
}

// pending derives where the requests waiting on the silenced connection
// went when it died: TCP (a connection per request, within 20 ms) or a
// new QUIC connection.
func (s *edgeSet) pending(out *profile.Fallback, e *collector) {
	var reconnects []int
	for i, sl := range s.silenced {
		p := sl.p
		host := sl.a.c.Host
		cl, died := sl.clientClose()

		if cl == nil {
			if sl.a.c.Close == nil || sl.a.c.Close.By != "idle" {
				edgeErr(e, p.at, "the silenced connection did not die")

				continue
			}

			died = float64(sl.a.c.Close.Millis)
		}

		var waiting []edgeReq
		for _, r := range p.reqs {
			if r.host == host && r.made >= 0 && float64(r.made) < died && float64(r.arrival) >= died-1 {
				waiting = append(waiting, r)
			}
		}

		if len(waiting) == 0 {
			edgeErr(e, p.at, "no request waiting when the silenced connection died at %.0f", died)

			continue
		}

		got := profile.PendingTCP
		if as := p.attemptsTo(host, died, died+50); len(as) > 0 && waiting[0].quic == as[0].index {
			got = profile.PendingQUIC

			reconnects = append(reconnects, int(float64(as[0].start)-died))
		}

		for _, r := range waiting {
			switch {
			case got == profile.PendingQUIC && (r.quic < 0 || p.attempts[r.quic] != p.attemptsTo(host, died, died+50)[0]):
				edgeErr(e, p.at, "%s did not go on the new QUIC connection", r.label)
			case got == profile.PendingTCP && (r.quic >= 0 || float64(r.arrival) > died+50):
				edgeErr(e, p.at, "%s arrived at %d, not on TCP within 50 ms of %.0f", r.label, r.arrival, died)
			}
		}

		if n := len(p.tcpHellos(host, died-1, died+20)); got == profile.PendingTCP && n != len(waiting) {
			edgeErr(e, p.at, "%d TCP connections for %d waiting requests", n, len(waiting))
		}

		if i > 0 && got != out.Pending {
			edgeErr(e, p.at, "pending requests went %q, others %q", got, out.Pending)
		}

		out.Pending = got
	}

	if out.Pending == profile.PendingQUIC && len(reconnects) > 0 {
		out.ReconnectMillis = span(reconnects)
	}
}
