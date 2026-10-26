package main

import (
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// Edge scenario names.
const (
	scenarioBlackhole = "h3-blackhole"
	scenarioBroken    = "h3-broken"
	scenarioVN        = "h3-vn"
	scenarioDownload  = "h3-download"
)

// flightGapMicros separates a connection's client datagrams into
// flights: datagrams less than this apart were sent together.
const flightGapMicros = 2000

// edgePage is one sample of an edge capture.
type edgePage struct {
	// at names it: "<name>.<scenario>[.local-roots]: sample N".
	at       string
	scenario string
	page     *capture.Page
	reqs     []edgeReq
	attempts []*attempt
}

// edgeReq is one request of an edge page.
type edgeReq struct {
	label string
	host  string
	// made is when the page's script made it, on the page's clock: its
	// ?t= plus the sample's offset (the smallest arrival - t, the
	// request that waited least); -1 without ?t=.
	made int64
	// arrival is when it reached the server.
	arrival int64
	// quic is the QUIC connection that carried it (an index of
	// page.QUIC), -1 over TCP.
	quic int
	// first: the first request on its TCP connection.
	first bool
}

// delay is how long the browser held r.
func (r edgeReq) delay() int64 {
	return r.arrival - r.made
}

// name is r's label without its query.
func (r edgeReq) name() string {
	name, _, _ := strings.Cut(r.label, "?")
	return name
}

// attempt is one QUIC connection of an edge page, as the client tried
// it.
type attempt struct {
	index int
	c     *quictrace.Conn
	// start is its first datagram's time on the page's clock.
	start   int64
	resumed bool
	// answered: the server sent it something other than Version
	// Negotiation.
	answered bool
	vn       bool
	// flights are its client datagrams, grouped by flightGapMicros.
	flights [][]*quictrace.Datagram
}

// millis is the page time of a datagram's microseconds.
func (a *attempt) millis(us int64) float64 {
	return float64(a.start) + float64(us)/1000
}

// closing reports whether a flight carries a CONNECTION_CLOSE.
func closing(f []*quictrace.Datagram) bool {
	for _, d := range f {
		for i := range d.Packets {
			if hasFrame(&d.Packets[i], quictrace.FrameConnectionClose) {
				return true
			}
		}
	}
	return false
}

// closeFrame is the first CONNECTION_CLOSE in f, with its packet.
func closeFrame(f []*quictrace.Datagram) (*quictrace.Packet, *quictrace.Frame) {
	for _, d := range f {
		for i := range d.Packets {
			p := &d.Packets[i]
			for j := range p.Frames {
				if p.Frames[j].Type == quictrace.FrameConnectionClose {
					return p, &p.Frames[j]
				}
			}
		}
	}
	return nil, nil
}

// probes are a's flights after the first, its close left out.
func (a *attempt) probes() [][]*quictrace.Datagram {
	var out [][]*quictrace.Datagram
	for _, f := range a.flights[1:] {
		if !closing(f) {
			out = append(out, f)
		}
	}
	return out
}

// closeFlight is a's flight carrying its CONNECTION_CLOSE, nil for none.
func (a *attempt) closeFlight() []*quictrace.Datagram {
	for _, f := range a.flights {
		if closing(f) {
			return f
		}
	}
	return nil
}

// end is when a sent its last datagram.
func (a *attempt) end() float64 {
	last := a.flights[len(a.flights)-1]
	return a.millis(last[len(last)-1].Micros)
}

// edgeSet is a profile's edge captures.
type edgeSet struct {
	pages []*edgePage
	set   *captureSet
}

// loadEdge reads name's edge captures (server.H3EdgeCaptures).
func loadEdge(dir string, set *captureSet) (*edgeSet, error) {
	paths, err := server.H3EdgeCaptures(dir, set.name)
	if err != nil {
		return nil, err
	}
	out := &edgeSet{
		set: set,
	}
	for _, path := range paths {
		base := strings.TrimSuffix(filepath.Base(path), ".pagetrace.json.gz")
		scenario, _, _ := strings.Cut(strings.TrimPrefix(base, set.name+"."), ".")
		pages, err := capture.LoadPages(path)
		if err != nil {
			return nil, err
		}
		for si, page := range pages {
			ep := &edgePage{
				at:       fmt.Sprintf("%s: sample %d", base, si+1),
				scenario: scenario,
				page:     page,
			}
			ep.reqs = edgeRequests(page)
			for i, c := range page.QUIC {
				ep.attempts = append(ep.attempts, newAttempt(i, c))
			}
			out.pages = append(out.pages, ep)
		}
	}
	return out, nil
}

// scenario are the samples of one edge scenario.
func (s *edgeSet) scenario(name string) []*edgePage {
	var out []*edgePage
	for _, p := range s.pages {
		if p.scenario == name {
			out = append(out, p)
		}
	}
	return out
}

// gap reports whether at's difference in feature is pinned in
// sampleGaps, and marks the pin used.
func (s *edgeSet) gap(at, feature string) bool {
	key := at + ": " + feature
	if _, ok := sampleGaps[key]; !ok {
		return false
	}
	s.set.usedGaps[key] = true
	return true
}

func newAttempt(i int, c *quictrace.Conn) *attempt {
	a := &attempt{
		index:   i,
		c:       c,
		start:   c.Millis,
		resumed: c.Resumed,
	}
	for _, d := range c.Server {
		if slices.ContainsFunc(d.Packets, func(p quictrace.Packet) bool {
			return p.Type == quictrace.PacketVN
		}) {
			a.vn = true
		} else {
			a.answered = true
		}
	}
	var last int64 = math.MinInt64
	for di := range c.Datagrams {
		d := &c.Datagrams[di]
		if len(a.flights) == 0 || d.Micros-last >= flightGapMicros {
			a.flights = append(a.flights, nil)
		}
		a.flights[len(a.flights)-1] = append(a.flights[len(a.flights)-1], d)
		last = d.Micros
	}
	return a
}

// edgeRequests lists page's requests, TCP and QUIC, in the order they
// arrived, each with when it was made.
func edgeRequests(page *capture.Page) []edgeReq {
	var out []edgeReq
	conns := tracedConns(page)
	for i, tr := range page.H2 {
		base := tr.Millis
		if base == 0 && i < len(conns) {
			base = conns[i].Millis
		}
		at := map[uint32]int64{}
		for _, f := range tr.Frames {
			if _, ok := at[f.Stream]; f.Type == "HEADERS" && !ok {
				at[f.Stream] = f.Millis
			}
		}
		for ri, r := range tr.Requests {
			out = append(out, edgeReq{
				label:   label(r.Path),
				host:    tr.Host,
				made:    madeAt(r.Path),
				arrival: base + at[r.Stream],
				quic:    -1,
				first:   ri == 0,
			})
		}
	}
	for qi, q := range page.QUIC {
		if q.H3 == nil {
			continue
		}
		for _, r := range q.H3.Requests {
			out = append(out, edgeReq{
				label:   label(r.Path),
				host:    q.Host,
				made:    madeAt(r.Path),
				arrival: r.Millis,
				quic:    qi,
			})
		}
	}
	offset := int64(math.MaxInt64)
	for _, r := range out {
		if r.made >= 0 {
			offset = min(offset, r.arrival-r.made)
		}
	}
	for i := range out {
		if out[i].made >= 0 {
			out[i].made += offset
		}
	}
	slices.SortStableFunc(out, func(a, b edgeReq) int {
		return int(a.arrival - b.arrival)
	})
	return out
}

// madeAt is a path's ?t=, -1 without one.
func madeAt(path string) int64 {
	_, query, ok := strings.Cut(path, "?")
	if !ok {
		return -1
	}
	v, err := url.ParseQuery(query)
	if err != nil {
		return -1
	}
	t, err := strconv.ParseFloat(v.Get("t"), 64)
	if err != nil {
		return -1
	}
	return int64(t)
}

// request is the request labelled l (its label without ?t=), nil for
// none.
func (p *edgePage) request(l string) *edgeReq {
	for i := range p.reqs {
		if p.reqs[i].name() == l {
			return &p.reqs[i]
		}
	}
	return nil
}

// attemptsTo are the page's QUIC attempts to host that started in
// [from, to).
func (p *edgePage) attemptsTo(host string, from, to float64) []*attempt {
	var out []*attempt
	for _, a := range p.attempts {
		if a.c.Host == host && float64(a.start) >= from && float64(a.start) < to {
			out = append(out, a)
		}
	}
	return out
}

// tcpHellos are the times of the page's TCP connections to host whose
// ClientHello arrived in [from, to).
func (p *edgePage) tcpHellos(host string, from, to float64) []int64 {
	var out []int64
	for _, c := range p.page.Connections {
		if c.Host == host && float64(c.Millis) >= from && float64(c.Millis) < to {
			out = append(out, c.Millis)
		}
	}
	slices.Sort(out)
	return out
}

// roundTo rounds v to the nearest multiple of step: the browsers' timers
// are round numbers, and the captures measure them a few ms late.
func roundTo(v float64, step int) int {
	return int(math.Round(v/float64(step))) * step
}

// edgeErr adds an error about a sample.
func edgeErr(e *collector, at, format string, args ...any) {
	e.errs = append(e.errs, fmt.Errorf("%s: %s", at, fmt.Sprintf(format, args...)))
}

// deriveEdge fills in what only the edge captures show: q's loss
// recovery, keep-alive, acknowledgement, credit, key update and version
// negotiation rules, and h's fallback.
func deriveEdge(s *edgeSet, q *profile.QUIC, h *profile.HTTP3) error {
	e := &collector{}
	for _, sc := range []string{scenarioBlackhole, scenarioBroken, scenarioVN, scenarioDownload} {
		if len(s.scenario(sc)) == 0 {
			edgeErr(e, s.set.name, "no %s capture", sc)
		}
	}
	if err := e.err(); err != nil {
		return err
	}
	q.Loss = s.loss(q, e)
	q.KeepAlive = s.keepAlive(e)
	q.Ack = s.ack(e)
	q.Credit = s.credit(q, e)
	q.KeyUpdate = s.keyUpdate(e)
	q.VersionNegotiation = s.versionNegotiation(e)
	h.Discovery.Fallback = s.fallback(q, &h.Discovery, e)
	return e.err()
}
