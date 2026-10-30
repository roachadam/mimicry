package main

import (
	"fmt"
	"math"
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// download is a h3-download sample's connection that carried the 50 MiB
// response: the one the server sent the most datagrams on.
type download struct {
	p *edgePage
	a *attempt
}

func (s *edgeSet) downloads(e *collector) []download {
	var out []download
	for _, p := range s.scenario(scenarioDownload) {
		var best *attempt
		for _, a := range p.attempts {
			if best == nil || len(a.c.Server) > len(best.c.Server) {
				best = a
			}
		}

		if best == nil || len(best.c.Server) < 10000 {
			edgeErr(e, p.at, "no connection carried the download")

			continue
		}

		out = append(out, download{
			p: p,
			a: best,
		})
	}

	return out
}

// frames1RTT calls f for every frame of the client's 1-RTT packets, in
// wire order.
func (d download) frames1RTT(f func(p *quictrace.Packet, fr *quictrace.Frame)) {
	for di := range d.a.c.Datagrams {
		for pi := range d.a.c.Datagrams[di].Packets {
			p := &d.a.c.Datagrams[di].Packets[pi]
			if p.Type != quictrace.Packet1RTT {
				continue
			}

			for fi := range p.Frames {
				f(p, &p.Frames[fi])
			}
		}
	}
}

// advances are how far each ACK's largest acknowledged moved past the
// last one's, in order.
func (d download) advances() []int {
	var (
		out  []int
		last uint64
	)
	d.frames1RTT(func(_ *quictrace.Packet, fr *quictrace.Frame) {
		if fr.Type != quictrace.FrameAck {
			return
		}

		if last > 0 && fr.Largest > last {
			out = append(out, int(fr.Largest-last))
		}

		last = max(last, fr.Largest)
	})

	return out
}

// mode is the most frequent value of vs, the smallest among equals.
func mode(vs []int) (value, count int) {
	n := map[int]int{}
	for _, v := range vs {
		n[v]++
	}

	for v, c := range n {
		if c > count || c == count && v < value {
			value, count = v, c
		}
	}

	return value, count
}

// ack derives the bulk acknowledgement rule from every download: exact if
// one advance makes up 90% of ACKs after Chrome's warm-up run, else
// batched at the smallest advance covering 5% with under 1% shorter.
func (s *edgeSet) ack(e *collector) profile.QUICAck {
	var out profile.QUICAck
	for i, d := range s.downloaded {
		adv := d.advances()
		if len(adv) < 1000 {
			edgeErr(e, d.p.at, "only %d ACKs during the download", len(adv))

			continue
		}

		var got profile.QUICAck

		m, c := mode(adv)
		warm := slices.Index(adv, m)

		if rest := adv[warm:]; c*10 >= len(rest)*9 {
			got.Every = m
			if warm > 0 {
				got.WarmupEvery, _ = mode(adv[:warm])
				// The ACKs before the first decimated one cover the
				// warm-up's packets, to the nearest ten.
				sum := 0
				for _, a := range adv[:warm] {
					sum += a
				}

				got.WarmupPackets = roundTo(float64(sum), 50)
			}
		} else {
			got.Batched = true

			n := map[int]int{}

			for _, a := range adv {
				n[a]++
			}

			for v := 1; v <= m; v++ {
				if n[v]*20 >= len(adv) {
					got.Every = v
					break
				}
			}

			short := 0
			for _, a := range adv {
				if a < got.Every {
					short++
				}
			}

			if short*100 >= len(adv) {
				edgeErr(e, d.p.at, "%d of %d ACKs cover fewer than %d packets", short, len(adv), got.Every)
			}
		}

		if i > 0 && got != out {
			edgeErr(e, d.p.at, "ACKs every %+v, others %+v", got, out)
		}

		out = got
	}

	return out
}

// creditSlack is how far past window / divisor an update may come: the
// packets that arrived since the threshold was crossed (2% of Chrome's 3
// MiB and Firefox's 3 MiB stream steps at most).
const creditSlack = 0.02

// packetOverhead estimates the non-stream bytes of a server 1-RTT packet.
// The recorder can't decrypt server 1-RTT payloads, so received bytes are
// packet sizes less this.
const packetOverhead = 30

// credit derives MAX_DATA and MAX_STREAM_DATA raise rules from every
// download: a window/divisor step (median over samples), or with
// ConnectionStreamWindow, MAX_DATA as received bytes plus stream window.
func (s *edgeSet) credit(q *profile.QUIC, e *collector) profile.QUICCredit {
	var out profile.QUICCredit

	window := func(id profile.TransportParameterID) float64 {
		for _, tp := range q.TransportParameters {
			if tp.ID == id && tp.Value != nil {
				return float64(*tp.Value)
			}
		}

		return 0
	}
	streamWin, connWin := window(profile.TPInitialMaxStreamDataBidiLocal), window(profile.TPInitialMaxData)

	type limits struct {
		at      string
		msd, md []float64
		// ahead is, per MAX_DATA raise, its limit less the bytes
		// received when it was sent.
		ahead []float64
	}

	var (
		all                      []limits
		msdSteps, mdSteps, ahead []float64
	)

	for _, d := range s.downloaded {
		l := limits{
			at: d.p.at,
		}
		stream := downloadStream(d)
		received := receivedBy(d)

		for _, dg := range d.a.c.Datagrams {
			for _, p := range dg.Packets {
				if p.Type != quictrace.Packet1RTT {
					continue
				}

				for _, fr := range p.Frames {
					switch {
					case fr.Type == quictrace.FrameMaxStreamData && fr.Stream != nil && *fr.Stream == stream:
						l.msd = append(l.msd, float64(fr.Max))
					case fr.Type == quictrace.FrameMaxData:
						// Chrome repeats its limit after 19
						// ACK-only packets (ackOnlyLimit):
						// only raises count.
						if n := len(l.md); float64(fr.Max) > connWin && (n == 0 || float64(fr.Max) > l.md[n-1]) {
							l.md = append(l.md, float64(fr.Max))
							l.ahead = append(l.ahead, float64(fr.Max)-received(dg.Micros))
						}
					}
				}
			}
		}

		if len(l.msd) < 2 || len(l.md) < 1 {
			edgeErr(e, d.p.at, "%d MAX_STREAM_DATA and %d MAX_DATA raises during the download", len(l.msd), len(l.md))

			continue
		}

		msdSteps = append(msdSteps, steps(l.msd, streamWin)...)
		mdSteps = append(mdSteps, steps(l.md, connWin)...)
		ahead = append(ahead, l.ahead...)
		all = append(all, l)
	}

	if len(all) == 0 {
		return out
	}

	out.StreamDivisor = divisor(msdSteps, streamWin)

	near := func(v, w float64) bool {
		return math.Abs(v-w) <= w/10
	}

	switch m := median(ahead); {
	case near(m, streamWin) && !near(m, connWin):
		out.ConnectionStreamWindow = true
	case near(m, connWin):
		out.ConnectionDivisor = divisor(mdSteps, connWin)
	default:
		edgeErr(e, s.set.name, "MAX_DATA runs %.0f ahead of the bytes received: neither the stream window (%.0f) nor the connection's (%.0f)", m, streamWin, connWin)
	}

	for _, l := range all {
		bad := badStep("MAX_STREAM_DATA", l.msd, streamWin, out.StreamDivisor)
		if out.ConnectionDivisor > 0 && bad == "" {
			bad = badStep("MAX_DATA", l.md, connWin, out.ConnectionDivisor)
		}

		if bad != "" && !s.gap(l.at, "credit") {
			edgeErr(e, l.at, "%s", bad)
		}
	}

	return out
}

// receivedBy returns how many stream bytes d's client had received by a
// time (packetOverhead).
func receivedBy(d download) func(us int64) float64 {
	at := make([]int64, 0, len(d.a.c.Server))
	sum := make([]float64, 0, len(d.a.c.Server))
	total := 0.0

	for _, sd := range d.a.c.Server {
		for _, p := range sd.Packets {
			if p.Type == quictrace.Packet1RTT {
				total += float64(max(p.Size-packetOverhead, 0))
			}
		}

		at = append(at, sd.Micros)
		sum = append(sum, total)
	}

	return func(us int64) float64 {
		i, found := slices.BinarySearch(at, us)
		if found {
			i++
		}

		if i == 0 {
			return 0
		}

		return sum[i-1]
	}
}

// median is the middle of vs.
func median(vs []float64) float64 {
	vs = slices.Clone(vs)
	slices.Sort(vs)

	return vs[len(vs)/2]
}

// steps are how far each limit raised the last, the first from window.
func steps(limits []float64, window float64) []float64 {
	out := []float64{limits[0] - window}
	for i := 1; i < len(limits); i++ {
		out = append(out, limits[i]-limits[i-1])
	}

	return out
}

// downloadStream is the stream that carried d's download: the client
// stream whose MAX_STREAM_DATA frames are most.
func downloadStream(d download) uint64 {
	n := map[uint64]int{}
	d.frames1RTT(func(_ *quictrace.Packet, fr *quictrace.Frame) {
		if fr.Type == quictrace.FrameMaxStreamData && fr.Stream != nil {
			n[*fr.Stream]++
		}
	})

	var best uint64
	for s, c := range n {
		if c > n[best] {
			best = s
		}
	}

	return best
}

// divisor is window over the median of steps.
func divisor(steps []float64, window float64) int {
	steps = slices.Clone(steps)
	slices.Sort(steps)

	return int(math.Round(window / steps[len(steps)/2]))
}

// badStep describes the first limit that did not come window / div (and
// at most creditSlack more) after the last, "" for none.
func badStep(frame string, limits []float64, window float64, div int) string {
	want := window / float64(div)
	prev := window

	for i, l := range limits {
		if step := l - prev; step < want || step > want*(1+creditSlack) {
			return fmt.Sprintf("%s %d raised the limit by %.0f, want %.0f (%.0f%% more at most)", frame, i+1, step, want, creditSlack*100)
		}

		prev = l
	}

	return ""
}

// keyUpdate derives whether the client starts key updates: on every
// download it may change its key phase only after the server changed its
// own, as many times, and in its next packet.
func (s *edgeSet) keyUpdate(e *collector) profile.QUICKeyUpdate {
	var out profile.QUICKeyUpdate
	for _, d := range s.downloaded {
		client := phaseChanges(d.a.c.Datagrams, d.a)
		server := phaseChanges(d.a.c.Server, d.a)

		switch {
		case len(client) != len(server):
			edgeErr(e, d.p.at, "%d client key updates, %d server ones", len(client), len(server))

			out.Initiate = len(client) > len(server)

		case len(server) == 0:
			edgeErr(e, d.p.at, "no key update during the download")
		}

		for i := range min(len(client), len(server)) {
			if client[i].first < server[i].first {
				edgeErr(e, d.p.at, "client key update %d at %.1f ms, before the server's (%.1f ms)", i+1, client[i].first, server[i].first)

				out.Initiate = true
			}

			if client[i].lastOld > server[i].first+5 {
				edgeErr(e, d.p.at, "client still sent the old key phase %.1f ms after the server's update", client[i].lastOld-server[i].first)
			}
		}
	}

	return out
}

// phaseChange is a key phase change in a direction: when its first
// packet in the new phase was sent, and its last in the old one.
type phaseChange struct {
	first, lastOld float64
}

func phaseChanges(ds []quictrace.Datagram, a *attempt) []phaseChange {
	var out []phaseChange

	phase := -1

	var last float64

	for _, d := range ds {
		for _, p := range d.Packets {
			if p.Type != quictrace.Packet1RTT {
				continue
			}

			t := a.millis(d.Micros)
			if kp := int(p.FirstByte>>2) & 1; phase >= 0 && kp != phase {
				out = append(out, phaseChange{
					first:   t,
					lastOld: last,
				})
				phase = kp
			} else if phase < 0 {
				phase = kp
			}

			last = t
		}
	}

	return out
}
