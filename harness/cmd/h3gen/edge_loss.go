package main

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"

	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// Transport error codes of a CONNECTION_CLOSE (0x1c).
const (
	errNoError          = 0x00
	errApplicationError = 0x0c
)

// ptoSlack is how much later than doubling a probe may come: Firefox's
// probe timeout follows its RTT estimate, which grows a little with each
// unanswered probe (2117 to 4816 ms after a 1212 ms interval, 12% late).
const ptoSlack = 1.15

// unanswered is a QUIC attempt the server never answered, with its page.
type unanswered struct {
	p *edgePage
	a *attempt
}

func (u unanswered) at() string {
	return fmt.Sprintf("%s, QUIC connection %d", u.p.at, u.a.index+1)
}

// abandoned: no flight after the first carries an Initial packet: the
// client dropped the attempt before its first probe (Safari, when TCP
// won).
func (u unanswered) abandoned() bool {
	for _, f := range u.a.flights[1:] {
		for _, d := range f {
			if slices.ContainsFunc(d.Packets, func(p quictrace.Packet) bool {
				return p.Type == quictrace.PacketInitial
			}) {
				return false
			}
		}
	}

	return true
}

// unansweredAttempts are the attempts of the h3-blackhole and h3-broken
// samples that the server never answered, fresh and resumed.
func (s *edgeSet) unansweredAttempts() (fresh, resumed []unanswered) {
	for _, p := range s.pages {
		if p.scenario != scenarioBlackhole && p.scenario != scenarioBroken {
			continue
		}
		for _, a := range p.attempts {
			if a.answered || a.vn {
				continue
			}
			u := unanswered{
				p: p,
				a: a,
			}
			if a.resumed {
				resumed = append(resumed, u)
			} else {
				fresh = append(fresh, u)
			}
		}
	}

	return fresh, resumed
}

// loss derives quic.loss: the handshake's probes and give-ups from every
// unanswered attempt, and path degrading from h3-broken's silenced
// connection.
func (s *edgeSet) loss(q *profile.QUIC, e *collector) profile.QUICLoss {
	var out profile.QUICLoss
	fresh, resumed := s.unansweredAttempts()
	if len(fresh) == 0 {
		edgeErr(e, s.set.name, "no unanswered fresh QUIC attempt")
		return out
	}
	all := append(slices.Clone(fresh), resumed...)
	abandoned := 0
	for _, u := range all {
		if u.abandoned() {
			abandoned++
		}
	}
	switch abandoned {
	case len(all):
		out.AbandonClose = abandonClose(fresh, resumed, e)
	case 0:
		s.probes(&out, fresh, resumed, e)
		out.Fresh = giveUp(q, fresh, "loss.fresh", s, e)
		out.Resumed = giveUp(q, resumed, "loss.resumed", s, e)
	default:
		edgeErr(e, s.set.name, "%d of %d unanswered attempts dropped before their first probe, the others not", abandoned, len(all))
	}
	out.PathDegrading = s.pathDegrading(q, e)

	return out
}

// abandonClose is what attempts dropped before their first probe sent
// after their first flight: nothing (fresh), or one 0-RTT datagram
// (resumed with 0-RTT: CloseZeroRTT).
func abandonClose(fresh, resumed []unanswered, e *collector) string {
	for _, u := range fresh {
		if len(u.a.flights) > 1 {
			edgeErr(e, u.at(), "a fresh attempt dropped before its first probe sent %d more flights", len(u.a.flights)-1)
		}
	}
	out := ""
	for i, u := range resumed {
		got := ""
		if len(u.a.flights) == 2 && len(u.a.flights[1]) == 1 && !slices.ContainsFunc(u.a.flights[1][0].Packets, func(p quictrace.Packet) bool {
			return p.Type != quictrace.Packet0RTT
		}) {
			got = profile.CloseZeroRTT
		} else if len(u.a.flights) > 1 {
			edgeErr(e, u.at(), "a dropped resumed attempt sent %d flights after its first, not one 0-RTT datagram", len(u.a.flights)-1)
		}
		if i > 0 && got != out {
			edgeErr(e, u.at(), "dropped with close %q, others with %q", got, out)
		}
		out = got
	}

	return out
}

// probes derives the probe timeout, the probes' contents and packet
// number skips from the fresh attempts, and the cached RTT from the
// resumed ones.
func (s *edgeSet) probes(out *profile.QUICLoss, fresh, resumed []unanswered, e *collector) {
	first := math.Inf(1)
	for _, u := range fresh {
		if p := u.a.probes(); len(p) > 0 {
			first = min(first, u.a.millis(p[0][0].Micros)-float64(u.a.start))
		}
	}
	if math.IsInf(first, 1) {
		edgeErr(e, s.set.name, "no fresh attempt sent a probe")
		return
	}
	out.InitialPTOMillis = roundTo(first, 100)
	out.Probe = probeKind(fresh[0].a)
	out.SkipPacketNumber = skips(fresh[0].a)
	for _, u := range fresh {
		prev := float64(u.a.start)
		for k, f := range u.a.probes() {
			t := u.a.millis(f[0].Micros)
			want := float64(out.InitialPTOMillis) * math.Pow(2, float64(k))
			if got := t - prev; got < want-2 || got > want*ptoSlack+10 {
				edgeErr(e, u.at(), "probe %d %.1f ms after the last, want %.0f (up to %.0f%% later)", k+1, got, want, (ptoSlack-1)*100)
			}
			prev = t
		}
		if got := probeKind(u.a); got != out.Probe {
			edgeErr(e, u.at(), "probes are %q, others' %q", got, out.Probe)
		}
		if got := skips(u.a); got != out.SkipPacketNumber {
			edgeErr(e, u.at(), "skipPacketNumber %v, others' %v", got, out.SkipPacketNumber)
		}
	}
	type resumedProbe struct {
		u       unanswered
		t       float64
		cached  bool
		ackWait bool
	}
	var firsts []resumedProbe
	votes := map[[2]bool]int{}
	for _, u := range resumed {
		p := u.a.probes()
		if len(p) == 0 {
			edgeErr(e, u.at(), "a resumed attempt sent no probe")
			continue
		}
		t := u.a.millis(p[0][0].Micros) - float64(u.a.start)
		f := resumedProbe{
			u:      u,
			t:      t,
			cached: t < float64(out.InitialPTOMillis)/4,
			// A probe timeout of max_ack_delay (25 ms) and more
			// on a loopback RTT counts it before the handshake
			// completes.
			ackWait: t >= 15,
		}
		firsts = append(firsts, f)
		votes[[2]bool{f.cached, f.ackWait}]++
	}
	best := -1
	for v, n := range votes {
		if n > best {
			best = n
			out.CachedRTT, out.PTOAckDelay = v[0], v[1]
		}
	}
	for _, f := range firsts {
		if (f.cached != out.CachedRTT || f.ackWait != out.PTOAckDelay) && !s.gap(f.u.at(), "loss.cachedRtt") {
			edgeErr(e, f.u.at(), "first probe after %.1f ms, unlike the other resumed attempts", f.t)
		}
	}
	if !out.CachedRTT {
		out.PTOAckDelay = false
	}
}

// probeKind is how a's probes repeat its first flight: ProbeFlight (every
// Initial as sent), ProbeOnePacket (one packet cycling the data), or "".
func probeKind(a *attempt) string {
	first := initials(a.flights[0])
	flight, one := true, true
	for k, f := range a.probes() {
		got := initials(f)
		if len(got) != len(first) {
			flight = false
		} else {
			for i := range got {
				if !slices.Equal(cryptoFrames(got[i]), cryptoFrames(first[i])) {
					flight = false
				}
			}
		}
		if len(got) != 1 || len(f) != 1 || !slices.Equal(covered(got[0]), covered(first[k%len(first)])) {
			one = false
		}
	}
	switch {
	case flight:
		return profile.ProbeFlight
	case one:
		return profile.ProbeOnePacket
	}

	return ""
}

// skips reports whether a skips a packet number before each probe, and
// fails to say so consistently by returning the first probe's answer.
func skips(a *attempt) bool {
	var last uint64
	skip := false
	for k, f := range a.flights {
		ps := initials(f)
		if len(ps) == 0 {
			continue
		}
		if k > 0 && !closing(f) {
			skip = ps[0].PN == last+2
		}
		last = ps[len(ps)-1].PN
	}

	return skip
}

// initials are a flight's Initial packets.
func initials(f []*quictrace.Datagram) []*quictrace.Packet {
	var out []*quictrace.Packet
	for _, d := range f {
		for i := range d.Packets {
			if d.Packets[i].Type == quictrace.PacketInitial {
				out = append(out, &d.Packets[i])
			}
		}
	}

	return out
}

// covered is the ClientHello bytes p's CRYPTO frames carry, as merged
// ranges.
func covered(p *quictrace.Packet) []cryptoRange {
	rs := cryptoFrames(p)
	slices.SortFunc(rs, func(a, b cryptoRange) int {
		return a.start - b.start
	})
	var out []cryptoRange
	for _, r := range rs {
		if n := len(out); n > 0 && r.start <= out[n-1].end {
			out[n-1].end = max(out[n-1].end, r.end)
			continue
		}
		out = append(out, r)
	}

	return out
}

// elapsedRE finds quiche's elapsed time in a close reason.
var elapsedRE = regexp.MustCompile(`after (\d+)us`)

// giveUp derives when attempts give up and what they send then, each
// within 20 ms after the round time it implies.
func giveUp(q *profile.QUIC, us []unanswered, feature string, s *edgeSet, e *collector) profile.QUICGiveUp {
	var out profile.QUICGiveUp
	idle := math.Inf(1)
	for _, tp := range q.TransportParameters {
		if tp.ID == profile.TPMaxIdleTimeout && tp.Value != nil {
			idle = float64(*tp.Value)
		}
	}
	type closed struct {
		u    unanswered
		at   float64
		give profile.QUICGiveUp
	}
	var closes []closed
	silent := 0
	for _, u := range us {
		f := u.a.closeFlight()
		if f == nil {
			silent++
			if end := u.a.end() - float64(u.a.start); end >= idle {
				edgeErr(e, u.at(), "sent its last datagram %.0f ms in, past max_idle_timeout", end)
			}

			continue
		}
		p, fr := closeFrame(f)
		t := u.a.millis(f[0].Micros) - float64(u.a.start)
		g := profile.QUICGiveUp{
			DatagramSize: f[0].Size,
		}
		switch {
		case p.Type != quictrace.PacketInitial || fr.Code != 0x1c || fr.ErrorCode == nil:
			edgeErr(e, u.at(), "gave up with a %s close (frame %#x), not an Initial transport one", p.Type, fr.Code)
		case *fr.ErrorCode == errNoError && fr.Reason != "":
			g.Close = profile.CloseTransport
			m := elapsedRE.FindStringSubmatchIndex(fr.Reason)
			if m == nil {
				g.Reason = fr.Reason
				break
			}
			g.Reason = fr.Reason[:m[2]] + "{elapsed_us}" + fr.Reason[m[3]:]
			if us, _ := strconv.ParseFloat(fr.Reason[m[2]:m[3]], 64); math.Abs(us/1000-t) > 5 {
				edgeErr(e, u.at(), "close reason says %.0f us, sent at %.1f ms", us, t)
			}
		case *fr.ErrorCode == errApplicationError && fr.Reason == "":
			g.Close = profile.CloseApplication
		default:
			edgeErr(e, u.at(), "gave up with error %#x reason %q: neither a transport nor an application close", *fr.ErrorCode, fr.Reason)
		}
		closes = append(closes, closed{
			u:    u,
			at:   t,
			give: g,
		})
	}
	switch {
	case len(closes) == 0:
		return out
	case silent > 0:
		edgeErr(e, s.set.name, "%s: %d attempts closed, %d ended silently", feature, len(closes), silent)
		return out
	}
	out = closes[0].give
	first := math.Inf(1)
	for _, c := range closes {
		first = min(first, c.at)
	}
	out.AfterMillis = roundTo(first, 100)
	for _, c := range closes {
		if g := c.give; g.Close != out.Close || g.Reason != out.Reason || g.DatagramSize != out.DatagramSize {
			edgeErr(e, c.u.at(), "%s: closed %+v, others %+v", feature, g, out)
		}
		if (c.at < float64(out.AfterMillis)-1 || c.at > float64(out.AfterMillis)+20) && !s.gap(c.u.at(), feature) {
			edgeErr(e, c.u.at(), "%s: gave up %.1f ms in, not within 20 ms of %d", feature, c.at, out.AfterMillis)
		}
	}

	return out
}

// silenced is a h3-broken sample's silenced connection: the one that
// carried silence.so, with when the request arrived and the last
// datagram the server sent before it went dark.
type silenced struct {
	p *edgePage
	a *attempt
	// sent is when silence.so arrived; stream its stream.
	sent   float64
	stream uint64
}

func (s *edgeSet) silencedConns(e *collector) []silenced {
	var out []silenced
	for _, p := range s.scenario(scenarioBroken) {
		r := p.request("silence.so")
		if r == nil || r.quic < 0 {
			edgeErr(e, p.at, "silence.so did not go over QUIC")
			continue
		}
		a := p.attempts[r.quic]
		sl := silenced{
			p:    p,
			a:    a,
			sent: float64(r.arrival),
		}
		for _, hr := range a.c.H3.Requests {
			if hr.Millis == r.arrival && label(hr.Path) == r.label {
				sl.stream = hr.Stream
			}
		}
		out = append(out, sl)
	}

	return out
}

// lastReceived is when the server sent sl's connection its last datagram
// before t.
func (sl silenced) lastReceived(t float64) float64 {
	last := math.Inf(-1)
	for _, d := range sl.a.c.Server {
		if at := sl.a.millis(d.Micros); at < t {
			last = max(last, at)
		}
	}

	return last
}

// clientClose is sl's connection's first client CONNECTION_CLOSE after
// the silence, with when it was sent.
func (sl silenced) clientClose() (*quictrace.Frame, float64) {
	for di := range sl.a.c.Datagrams {
		d := &sl.a.c.Datagrams[di]
		if t := sl.a.millis(d.Micros); t > sl.sent {
			if _, f := closeFrame([]*quictrace.Datagram{d}); f != nil {
				return f, t
			}
		}
	}

	return nil, 0
}

// pathDegrading derives when the browser gives up on the silenced path
// and whether it migrates, closes, or waits for the idle timeout.
func (s *edgeSet) pathDegrading(q *profile.QUIC, e *collector) *profile.QUICPathDegrading {
	var out *profile.QUICPathDegrading
	for i, sl := range s.silencedConns(e) {
		var got *profile.QUICPathDegrading
		at := sl.p.at
		migrated := slices.IndexFunc(sl.a.c.Datagrams, func(d quictrace.Datagram) bool {
			return d.Path != 0
		})
		cl, closedAt := sl.clientClose()
		switch {
		case migrated >= 0:
			got = migration(q, sl, migrated, at, e)
		case cl != nil && cl.Reason != "":
			last := sl.lastReceived(closedAt)
			got = &profile.QUICPathDegrading{
				AfterMillis: roundTo(closedAt-last, 1000),
				Action:      profile.PathClose,
				CloseReason: cl.Reason,
			}
			if d := closedAt - last; d < float64(got.AfterMillis) || d > float64(got.AfterMillis)+50 {
				edgeErr(e, at, "closed %.1f ms after the last datagram received, not within 50 ms of %d", d, got.AfterMillis)
			}
		case sl.a.c.Close == nil || sl.a.c.Close.By != "idle":
			edgeErr(e, at, "the silenced connection neither migrated, closed with a reason nor idled out")
			continue
		}
		if i == 0 {
			out = got
			continue
		}
		switch {
		case (got == nil) != (out == nil):
			edgeErr(e, at, "path degrading %+v, others %+v", got, out)
		case got != nil && (got.Action != out.Action || got.CloseReason != out.CloseReason || got.AfterMillis != out.AfterMillis):
			edgeErr(e, at, "path degrading %+v, others %+v", *got, *out)
		}
	}

	return out
}

// migration checks sl's move to a new port (padded PATH_CHALLENGE, then
// RETIRE_CONNECTION_ID, nothing more on the old path) and derives after
// how many probe timeouts it came.
func migration(q *profile.QUIC, sl silenced, first int, at string, e *collector) *profile.QUICPathDegrading {
	ds := sl.a.c.Datagrams
	d := &ds[first]
	if d.Size != q.DatagramSize || len(d.Packets) == 0 || !hasFrame(&d.Packets[0], quictrace.FramePathChallenge) {
		edgeErr(e, at, "first datagram on the new path (%d B) is not a PATH_CHALLENGE padded to %d", d.Size, q.DatagramSize)
	}
	retired := false
	for i := first; i < min(first+4, len(ds)); i++ {
		for j := range ds[i].Packets {
			retired = retired || hasFrame(&ds[i].Packets[j], quictrace.FrameRetireConnectionID)
		}
	}
	if !retired {
		edgeErr(e, at, "no RETIRE_CONNECTION_ID in the first datagrams on the new path")
	}
	for _, later := range ds[first:] {
		if later.Path == 0 {
			edgeErr(e, at, "a datagram on the old path after the migration")
			break
		}
	}
	var sends []float64
	for _, dg := range ds[:first] {
		for _, p := range dg.Packets {
			if slices.ContainsFunc(p.Frames, func(f quictrace.Frame) bool {
				return f.Type == quictrace.FrameStream && f.Stream != nil && *f.Stream == sl.stream && f.Offset == 0
			}) {
				sends = append(sends, sl.a.millis(dg.Micros))
			}
		}
	}
	if len(sends) < 2 {
		edgeErr(e, at, "silence.so's stream sent %d times before the migration, want a retransmission", len(sends))
		return nil
	}
	pto := sends[1] - sends[0]
	ratio := (sl.a.millis(d.Micros) - sends[0]) / pto
	out := &profile.QUICPathDegrading{
		AfterPTOs: int(math.Round(ratio)),
		Action:    profile.PathMigrate,
	}
	if math.Abs(ratio-float64(out.AfterPTOs)) > 0.5 {
		edgeErr(e, at, "migrated %.2f probe timeouts after the request", ratio)
	}

	return out
}

// keepAlive derives the keep-alive PING period and its start moment: the
// candidate every sample puts within 50 ms of one period. PINGs within
// 5 ms of another datagram are probes, not keep-alives.
func (s *edgeSet) keepAlive(e *collector) profile.QUICKeepAlive {
	var out profile.QUICKeepAlive
	froms := []string{profile.KeepAliveFromSent, profile.KeepAliveFromIdle, profile.KeepAliveFromReceived}
	deltas := map[string][]float64{}
	var ats []string
	for _, sl := range s.silencedConns(e) {
		ds := sl.a.c.Datagrams
		found := false
		for i := range ds {
			t := sl.a.millis(ds[i].Micros)
			if t <= sl.sent || !pingOnly(&ds[i]) || i > 0 && ds[i].Micros-ds[i-1].Micros < 5000 || i+1 < len(ds) && ds[i+1].Micros-ds[i].Micros < 5000 {
				continue
			}
			received := sl.lastReceived(t)
			sent, idle := math.Inf(-1), math.Inf(1)
			for j := range ds[:i] {
				if at := sl.a.millis(ds[j].Micros); ackEliciting(&ds[j]) {
					sent = at
					if at > received {
						idle = min(idle, at)
					}
				}
			}
			if math.IsInf(idle, 1) {
				idle = received
			}
			deltas[profile.KeepAliveFromSent] = append(deltas[profile.KeepAliveFromSent], t-sent)
			deltas[profile.KeepAliveFromIdle] = append(deltas[profile.KeepAliveFromIdle], t-idle)
			deltas[profile.KeepAliveFromReceived] = append(deltas[profile.KeepAliveFromReceived], t-received)
			ats = append(ats, sl.p.at)
			found = true

			break
		}
		if !found {
			edgeErr(e, sl.p.at, "no keep-alive PING on the silenced connection")
		}
	}
	if len(ats) == 0 {
		return out
	}
	late := func(d float64, period int) bool {
		return d < float64(period)-1 || d > float64(period)+50
	}
	for _, from := range froms {
		ds := deltas[from]
		period := roundTo(slices.Min(ds), 100)
		fits := true
		for i, d := range ds {
			if _, pinned := sampleGaps[ats[i]+": keepAlive"]; late(d, period) && !pinned {
				fits = false
			}
		}
		if !fits {
			continue
		}
		if out.From != "" {
			edgeErr(e, s.set.name, "keep-alive PINGs fit counting from %q and from %q", out.From, from)
		}
		out.PeriodMillis, out.From = period, from
	}
	if out.From == "" {
		edgeErr(e, s.set.name, "keep-alive PINGs fit no moment they count from: %v", deltas)
		return out
	}
	for i, d := range deltas[out.From] {
		if late(d, out.PeriodMillis) {
			s.gap(ats[i], "keepAlive")
		}
	}

	return out
}

// ackEliciting reports a datagram carrying a frame other than ACK,
// PADDING and CONNECTION_CLOSE.
func ackEliciting(d *quictrace.Datagram) bool {
	for _, p := range d.Packets {
		for _, f := range p.Frames {
			switch f.Type {
			case quictrace.FrameAck, quictrace.FramePadding, quictrace.FrameConnectionClose:
			default:
				return true
			}
		}
	}

	return false
}

// pingOnly reports a datagram of 1-RTT packets carrying only PING (and
// PADDING) frames.
func pingOnly(d *quictrace.Datagram) bool {
	ping := false
	for _, p := range d.Packets {
		if p.Type != quictrace.Packet1RTT {
			return false
		}
		for _, f := range p.Frames {
			switch f.Type {
			case quictrace.FramePing:
				ping = true
			case quictrace.FramePadding:
			default:
				return false
			}
		}
	}

	return ping
}

// versionNegotiation derives the response to Version Negotiation from
// h3-vn: a retry in another version within 10 ms (and whether it closes
// on missing version_information), or no other attempt.
func (s *edgeSet) versionNegotiation(e *collector) profile.QUICVersionNegotiation {
	var out profile.QUICVersionNegotiation
	for i, p := range s.scenario(scenarioVN) {
		var got profile.QUICVersionNegotiation
		vn := slices.IndexFunc(p.attempts, func(a *attempt) bool {
			return a.vn
		})
		if vn < 0 {
			edgeErr(e, p.at, "no attempt got Version Negotiation")
			continue
		}
		a := p.attempts[vn]
		var vnAt float64
		for _, d := range a.c.Server {
			vnAt = a.millis(d.Micros)
			break
		}
		later := p.attemptsTo(a.c.Host, float64(a.start)+1, math.Inf(1))
		if len(later) > 0 {
			r := later[0]
			got.Retry = true
			switch {
			case float64(r.start)-vnAt > 10:
				edgeErr(e, p.at, "retried %.1f ms after Version Negotiation", float64(r.start)-vnAt)
			case r.c.Version == "00000001":
				edgeErr(e, p.at, "retried in version 1")
			case slices.ContainsFunc(r.c.ServerTransportParameters, func(tp quictrace.TransportParameter) bool {
				return tp.ID == uint64(profile.TPVersionInformation)
			}):
				edgeErr(e, p.at, "the server's transport parameters carry version_information: no downgrade check shows")
			}
			if f := r.closeFlight(); f != nil {
				if _, fr := closeFrame(f); fr.ErrorCode != nil {
					got.DowngradeError = profile.QUICErrorCode(*fr.ErrorCode)
				}
			}
			if len(later) > 1 {
				edgeErr(e, p.at, "%d attempts after the retry", len(later)-1)
			}
		}
		if i > 0 && got != out {
			edgeErr(e, p.at, "version negotiation %+v, others %+v", got, out)
		}
		out = got
	}

	return out
}
