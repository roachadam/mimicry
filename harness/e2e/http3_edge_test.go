package e2e

import (
	"context"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// h3PageHost is the h3 pages' own host (the server's h3Host), where / is.
const h3PageHost = "www.h3-a.test"

// Every request of the h3-blackhole, h3-broken, h3-vn and h3-v2 pages
// (server.H3BlackholeSteps and the rest; h3-v2 where python3 has
// aioquic), made through mimicry on the browser's schedule (each step
// starts at its time whatever the earlier ones take, its URL with ?t= as
// the page's script adds it), against every sample of the browser's
// capture (Chrome's and Firefox's -h3-local-roots captures where they
// exist, as TestHTTP3MatchesBrowser):
//
//   - per request: every connection it went on, in order (protocol, host,
//     which of the host's connections, 0-RTT), and its delay (its last
//     arrival less ?t=, less the sample's smallest such difference: how
//     long the client held it, its connection's handshake included);
//   - per QUIC attempt of each host, in order: whether it resumed, its
//     versions, what the server answered, how it ended, whether it moved
//     to a new path and how many probes it sent on the old one first
//     (the move's timing is checkPathMoves's rule, in the connection's
//     own probe timeouts); for one the server never completed, its
//     datagrams and when its last went (the give-up); when it started
//     after the request before it (its trigger);
//   - per TCP connection of each host that carried requests: when it
//     started after the request before it and after the QUIC attempt the
//     same request started (the race; negative where TCP came first), and
//     the GOAWAY the client sent on it; how many the client opened and
//     declined.
//
// Times must lie within the browser's samples' range widened by
// edgeSlack; one of mimicry's from a request on may lie above it by no
// more than this process was stalled within it (compareTimes). Categories
// and counts are held as TestHTTP3MatchesBrowser holds its features. Known
// differences are pinned in h3EdgeGaps; a listed gap that no longer
// appears fails. The profiles and scenarios run in parallel; the test
// takes as long as the longest page (h3-broken, about 75 s).
func TestHTTP3EdgeMatchesBrowser(t *testing.T) {
	for _, name := range profiles.Names() {
		for _, sc := range []*server.Scenario{server.H3Blackhole, server.H3Broken, server.H3VN, server.H3V2} {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				t.Parallel()
				if sc.NeedsAioquic() {
					if err := server.AioquicAvailable(t.Context()); err != nil {
						t.Skipf("%s needs aioquic: %v", sc.Name, err)
					}
				}
				p, err := profiles.ByName(name)
				if err != nil {
					t.Fatal(err)
				}
				if p.HTTP3 == nil || p.QUIC == nil {
					t.Fatal("the profile has no http3 or quic section")
				}
				want := h3Captures(t, name, sc)
				got := runH3Schedule(t, p, sc, want[0])
				skip, unused := edgeGapsFor(t, name+"/"+sc.Name)
				compareEdge(t, p, got, want, skip)
				unused()
			})
		}
	}
}

// h3EdgeGaps are known differences, by profile, scenario and feature (as
// compareEdge labels it): the feature is not compared. A listed gap that
// no longer appears fails.
var h3EdgeGaps = func() map[string]string {
	gaps := map[string]string{
		"chrome-155-macos/h3-broken edge request /h3/silence.so sent":                       "the server never answers the silenced request (the scenario drops it); Chrome sent it again over a new TCP connection 54–61 s later, when it closed its migrated connection with '85:Network blackhole detected' after keep-alive PINGs there went unanswered (report http3-edge-2026-10-25, corrections), a rule no profile field holds; mimicry's waits on its QUIC connection",
		"chrome-155-macos/h3-broken edge request /h3/silence.so delay ms":                   "as its sending",
		"chrome-155-macos/h3-broken edge TCP www.h3-a.test #2 goaway":                       "the TCP connection that carried Chrome's late resend of silence.so",
		"chrome-155-macos/h3-broken edge TCP www.h3-a.test #2 trigger":                      "as its goaway",
		"chrome-155-macos/h3-broken edge TCP www.h3-a.test #2 after its trigger ms":         "as its goaway",
		"chrome-155-macos/h3-broken edge TCP www.h3-b.test #3 goaway":                       "Chrome moved h3-b's requests to a new TCP connection after 6–10 s idle on its last one (closed4–closed6, in every sample once the origin was broken), not captured why; mimicry keeps its HTTP/2 connection",
		"chrome-155-macos/h3-broken edge TCP www.h3-b.test #3 after its trigger ms":         "as its goaway",
		"chrome-155-macos/h3-broken edge QUIC www.h3-b.test #2 datagrams":                   "Chrome's resumed attempt probes first with 4 Initials (its whole resumed hello, cut afresh) and sends lone 0-RTT datagrams later (+0.4 s, +2.0/3.0 s); quic.loss's one_packet probe sends one Initial per PTO and says nothing of the 0-RTT ones (13 datagrams, Chrome 16; timing and give-up match)",
		"chrome-155-macos/h3-broken edge QUIC www.h3-b.test #2 Initial packet number steps": "as its datagrams: the browser's first resumed probe is 4 Initials (steps 4 1 1 1, mimicry's 4 alone); every other step, the skip before each probe and none before the close, matches",
		"firefox-158-macos/h3-blackhole edge TCP www.h3-a.test #1 goaway":                   "Firefox answers the server's HTTP/2 GOAWAY with one of its own (NO_ERROR) at once, on every page; no profile field models it (an HTTP/2 rule, outside HTTP/3's)",
		"firefox-158-macos/h3-blackhole edge TCP www.h3-a.test #2 goaway":                   "as TCP #1's",
		"firefox-158-macos/h3-vn edge TCP www.h3-a.test #1 goaway":                          "as h3-blackhole's",
		"firefox-158-macos/h3-broken edge QUIC www.h3-b.test #2 datagrams":                  "Firefox's resumed probes vary between samples (pairs every PTO from a ~3 ms cached RTT, the 3-packet resumed first flight not repeated, lone 40-B 0-RTT datagrams); quic.loss's flight probe replays all 3 first-flight packets per PTO, and the profile has no resumed-probe rule; give-up time and close match",
	}
	// Safari resumed QUIC with 0-RTT after closing a connection itself
	// (its path's close, its own close on the server's GOAWAY), which no
	// profile section describes (Safari has no tls.resumption); mimicry's
	// reconnections are fresh, so the replayed requests go in 1-RTT and
	// the attempt after the close sends no 0-RTT close when dropped.
	// Safari also closes a QUIC connection itself on the server's GOAWAY,
	// which no profile field describes.
	for _, name := range []string{"safari-26-macos", "safari-27-macos"} {
		const resumed = "Safari resumed this QUIC connection with 0-RTT after closing the last itself; no profile section describes Safari's resumption, so mimicry's is fresh"
		for _, k := range []string{
			"edge QUIC www.h3-a.test #2",
			"edge QUIC www.h3-b.test #2",
			"edge QUIC www.h3-b.test #2 datagrams",
			"edge QUIC www.h3-b.test #2 last datagram ms",
		} {
			gaps[name+"/h3-broken "+k] = resumed
		}
		for _, l := range []string{"silence", "silent1", "silent2", "silent3", "silent4", "silent5", "silent6"} {
			gaps[name+"/h3-broken edge request /h3/"+l+".so sent"] = "replayed in 0-RTT on Safari's resumed connection; " + resumed
		}
		gaps[name+"/h3-broken edge QUIC www.h3-b.test #1 closed by"] = "Safari closes a QUIC connection itself when the server's GOAWAY arrives; no profile field describes it"
	}
	return gaps
}()

// edgeGapsFor is gapsFor for h3EdgeGaps.
func edgeGapsFor(t *testing.T, name string) (skip func(key string) bool, unused func()) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]bool{}
	skip = func(key string) bool {
		k := name + " " + key
		if _, ok := h3EdgeGaps[k]; !ok {
			return false
		}
		mu.Lock()
		seen[k] = true
		mu.Unlock()
		t.Logf("known gap: %s: %s", key, h3EdgeGaps[k])
		return true
	}
	unused = func() {
		for k := range h3EdgeGaps {
			if strings.HasPrefix(k, name+" ") && !seen[k] {
				t.Errorf("listed gap %q no longer appears", k)
			}
		}
	}
	return skip, unused
}

// runH3Schedule walks sc's page through mimicry as profile p: the typed
// navigation to /, then each step after its pause, those the page does
// not wait for (NoWait) started on time beside the others, then
// fetch('/done') where browser asked for it. A request still unanswered
// when the page is taken is cancelled.
func runH3Schedule(t *testing.T, p *profile.Profile, sc *server.Scenario, browser *capture.Page) *capture.Page {
	t.Helper()
	// Its times from a request on are net of the process's stalls.
	stalls.watch()
	s := startPage(t, p, sc)
	if s.quicReady != nil {
		// The browser's page loads long after aioquic is up; a datagram
		// sent earlier would be recorded when aioquic starts reading.
		select {
		case <-s.quicReady:
		case <-time.After(30 * time.Second):
			t.Fatal("aioquic did not start")
		}
	}
	page, err := url.Parse(s.url(h3PageHost, "/"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s.send(http.MethodGet, page.String(), mimicry.Request{}, nil, "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	steps := h3Steps(sc)
	select {
	case <-time.After(scriptStart(browser, steps[0])):
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	var wg sync.WaitGroup
	for _, st := range steps {
		select {
		case <-time.After(time.Duration(st.PauseMillis) * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
		target := s.url(st.Host, st.Path())
		if !st.NoWait {
			if err := sendScheduled(ctx, s, st, page, target); err != nil {
				t.Fatalf("%s: %v", st.Label, err)
			}
			continue
		}
		target += "?t=" + strconv.FormatInt(time.Since(start).Milliseconds(), 10)
		wg.Go(func() {
			if err := sendScheduled(ctx, s, st, page, target); err != nil && ctx.Err() == nil {
				t.Errorf("%s: %v", st.Label, err)
			}
		})
	}
	time.Sleep(h3ScriptTurn)
	done := doneHost(browser)
	s.send(http.MethodGet, s.url(done, "/done"), mimicry.Request{
		Kind:   mimicry.Fetch,
		From:   page,
		NoCORS: done != h3PageHost,
	}, nil, "")
	for {
		select {
		case c := <-s.captures:
			if c.Finished && c.Page != nil {
				cancel()
				wg.Wait()
				return c.Page
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no page capture")
		}
	}
}

// scriptStart is how long after its request for / arrived the browser's
// page started its script (parse and script start, which mimicry has
// none of): when first, a NoWait step, was made, less its pause. A QUIC
// attempt / started (Firefox's, on its Alt-Svc) then starts before the
// page's first request, as the browser's did.
func scriptStart(browser *capture.Page, first server.H3Step) time.Duration {
	v := viewEdge(browser)
	var nav, step *edgeRequest
	for _, r := range v.requests {
		switch r.label {
		case "/":
			nav = r
		case first.Path():
			step = r
		}
	}
	if nav == nil || step == nil || step.delay < 0 {
		return 0
	}
	return max(0, time.Duration(step.issued-nav.issued-int64(first.PauseMillis))*time.Millisecond)
}

// sendScheduled makes st's request from page to target, on ctx, and reads
// its response.
func sendScheduled(ctx context.Context, s *pageSession, st server.H3Step, page *url.URL, target string) error {
	method, r, h := h3StepRequest(st, page)
	var body io.Reader
	if st.Body != "" {
		body = strings.NewReader(st.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	maps.Copy(req.Header, h)
	resp, err := s.client.Do(mimicry.With(req, r))
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return err
}

// doneHost is the host the browser's page asked for /done (another than
// the page's where QUIC is not the capture server's own).
func doneHost(browser *capture.Page) string {
	for _, tr := range browser.H2 {
		for _, r := range tr.Requests {
			if r.Path == "/done" {
				return tr.Host
			}
		}
	}
	return h3PageHost
}

// edgeSlack is how far outside the browser's samples' range a time may
// lie: loopback handshakes, timers and a loaded machine's scheduling.
// hi may be negative (a race's signed gap).
func edgeSlack(hi int) int {
	return 50 + max(hi, -hi)/20
}

// raceWindow is how close a host's TCP connection and QUIC attempt must
// start to be compared as a race.
const raceWindow = 500

// triggerSlack is how far into a request's millisecond an event may come
// before it and still have been caused by it (its issue time is ?t= plus
// the sample's clock offset, which rounds).
const triggerSlack = 3

// edgeRequest is one request of a page, as the server saw it.
type edgeRequest struct {
	label string
	// issued is when the page made it, on the page's clock: its ?t= plus
	// the sample's clock offset, or without one its first arrival.
	issued int64
	// delay is its last arrival less issued; -1 without ?t=.
	delay int64
	// sent are the connections it went on, in order of arrival.
	sent []string
}

// edgeView is what compareEdge compares of a page.
type edgeView struct {
	requests []*edgeRequest
	// tcp are each host's HTTP/2 connections that carried requests, in
	// order; declined counts the others.
	tcp      map[string][]*h2trace.Trace
	declined map[string]int
	quic     map[string][]*quictrace.Conn
}

// arrival is one request's arrival on a connection.
type arrival struct {
	at   int64
	path string
	via  string
}

// viewEdge reads page's requests and connections.
func viewEdge(page *capture.Page) edgeView {
	v := edgeView{
		tcp:      map[string][]*h2trace.Trace{},
		declined: map[string]int{},
		quic:     map[string][]*quictrace.Conn{},
	}
	traces := slices.Clone(page.H2)
	slices.SortStableFunc(traces, func(a, b *h2trace.Trace) int {
		return int(a.Millis - b.Millis)
	})
	var arrivals []arrival
	for _, tr := range traces {
		if len(tr.Requests) == 0 {
			v.declined[tr.Host]++
			continue
		}
		v.tcp[tr.Host] = append(v.tcp[tr.Host], tr)
		via := fmt.Sprintf("h2 %s #%d", tr.Host, len(v.tcp[tr.Host]))
		for _, r := range tr.Requests {
			at := tr.Millis
			for _, f := range tr.Frames {
				if f.Type == "HEADERS" && f.Stream == r.Stream {
					at += f.Millis
					break
				}
			}
			arrivals = append(arrivals, arrival{
				at:   at,
				path: r.Path,
				via:  via,
			})
		}
	}
	for _, c := range page.QUIC {
		v.quic[c.Host] = append(v.quic[c.Host], c)
		via := fmt.Sprintf("h3 %s #%d", c.Host, len(v.quic[c.Host]))
		if c.EarlyData {
			via += " 0-RTT"
		}
		if c.H3 == nil {
			continue
		}
		for _, r := range c.H3.Requests {
			arrivals = append(arrivals, arrival{
				at:   r.Millis,
				path: r.Path,
				via:  via,
			})
		}
	}
	slices.SortStableFunc(arrivals, func(a, b arrival) int {
		return int(a.at - b.at)
	})
	byPath := map[string]*edgeRequest{}
	last := map[string]int64{}
	ts := map[string]int64{}
	for _, a := range arrivals {
		r := byPath[a.path]
		if r == nil {
			label, t := splitT(a.path)
			r = &edgeRequest{
				label:  label,
				issued: a.at,
				delay:  -1,
			}
			if t >= 0 {
				ts[a.path] = t
			}
			byPath[a.path] = r
			v.requests = append(v.requests, r)
		}
		if len(r.sent) == 0 || r.sent[len(r.sent)-1] != a.via {
			r.sent = append(r.sent, a.via)
		}
		last[a.path] = a.at
	}
	offset, found := int64(0), false
	for path, t := range ts {
		if d := last[path] - t; !found || d < offset {
			offset, found = d, true
		}
	}
	for path, t := range ts {
		r := byPath[path]
		r.issued = t + offset
		r.delay = last[path] - r.issued
	}
	slices.SortStableFunc(v.requests, func(a, b *edgeRequest) int {
		return int(a.issued - b.issued)
	})
	return v
}

// splitT is path without its query, and its ?t= (-1 without).
func splitT(path string) (string, int64) {
	p, query, _ := strings.Cut(path, "?")
	q, err := url.ParseQuery(query)
	if err != nil || q.Get("t") == "" {
		return p, -1
	}
	t, err := strconv.ParseInt(q.Get("t"), 10, 64)
	if err != nil {
		return p, -1
	}
	return p, t
}

// trigger is the last request the page made at or before at: of those
// with ?t=, whose issue time is known, or else of the others (the
// navigation), whose arrival stands for it and so comes after the
// requests its response let the page make.
func (v edgeView) trigger(at int64) *edgeRequest {
	var timed, other *edgeRequest
	for _, r := range v.requests {
		switch {
		case r.issued > at+triggerSlack:
		case r.delay >= 0:
			timed = r
		default:
			other = r
		}
	}
	if timed != nil {
		return timed
	}
	return other
}

// outcome is what the server sent a QUIC attempt: "version negotiation",
// "handshake" (it got as far as the server's Handshake packets),
// "initial" or "nothing".
func outcome(c *quictrace.Conn) string {
	out := "nothing"
	for _, d := range c.Server {
		for _, p := range d.Packets {
			switch p.Type {
			case quictrace.PacketVN:
				return "version negotiation"
			case quictrace.PacketHandshake, quictrace.Packet1RTT:
				out = "handshake"
			case quictrace.PacketInitial:
				if out == "nothing" {
					out = "initial"
				}
			}
		}
	}
	return out
}

// edgeFeatures are what compareEdge compares: categories and counts
// (features), times in ms (ms) and, for a time from when the page made a
// request (a request's delay, a connection's start after its trigger),
// how long this process was stalled within it (stalled: stalledIn).
func edgeFeatures(page *capture.Page) (f features, ms, stalled map[string]int) {
	v := viewEdge(page)
	f = newFeatures()
	ms, stalled = map[string]int{}, map[string]int{}
	for _, r := range v.requests {
		if r.label == "/" || r.label == "/done" {
			continue
		}
		f.cat["request "+r.label+" sent"] = strings.Join(r.sent, ", ")
		if r.delay >= 0 {
			k := "request " + r.label + " delay"
			ms[k], stalled[k] = int(r.delay), stalledIn(page, r.issued, r.delay)
		}
	}
	// The TCP connection that carried the page itself has no trigger: it
	// is the page's start.
	tcpTrigger := map[*h2trace.Trace]*edgeRequest{}
	for _, traces := range v.tcp {
		for _, tr := range traces {
			if !slices.ContainsFunc(tr.Requests, func(r h2trace.Request) bool {
				return r.Path == "/"
			}) {
				tcpTrigger[tr] = v.trigger(tr.Millis)
			}
		}
	}
	quicTrigger := map[*quictrace.Conn]*edgeRequest{}
	for host, conns := range v.quic {
		for i, c := range conns {
			k := fmt.Sprintf("QUIC %s #%d", host, i+1)
			version := c.Version
			if c.NegotiatedVersion != "" {
				version += " to " + c.NegotiatedVersion
			}
			f.cat[k] = fmt.Sprintf("resumed %v, version %s, server sent %s", c.Resumed, version, outcome(c))
			if c.Close != nil {
				f.cat[k+" closed by"] = c.Close.By
			}
			if tr := v.trigger(c.Millis); tr != nil {
				quicTrigger[c] = tr
				f.cat[k+" trigger"] = tr.label
				ms[k+" after its trigger"], stalled[k+" after its trigger"] = int(c.Millis-tr.issued), stalledIn(page, tr.issued, c.Millis-tr.issued)
			}
			if o := outcome(c); o != "handshake" && len(c.Datagrams) > 0 {
				f.num[k+" datagrams"] = len(c.Datagrams)
				f.cat[k+" Initial packet number steps"] = initialSteps(c)
				ms[k+" last datagram"] = int(c.Datagrams[len(c.Datagrams)-1].Micros / 1000)
			}
			if m, ok := movePath(c); ok {
				f.cat[k+" new path"] = "yes"
				f.num[k+" probes before new path"] = m.probes
				f.cat[k+" probe packet number steps"] = m.steps
			}
		}
	}
	for host, traces := range v.tcp {
		for j, tr := range traces {
			k := fmt.Sprintf("TCP %s #%d", host, j+1)
			if r := tcpTrigger[tr]; r != nil {
				f.cat[k+" trigger"] = r.label
				ms[k+" after its trigger"], stalled[k+" after its trigger"] = int(tr.Millis-r.issued), stalledIn(page, r.issued, tr.Millis-r.issued)
			}
			// A race: the request that started a QUIC attempt started
			// this connection too. The gap is signed (negative where the
			// TCP connection came first): the two start side by side, so
			// which arrives first by a few milliseconds is the client's
			// scheduling (Chrome's attempt leads its TCP connection by 2–4
			// ms; mimicry's hello is built while its TCP connects, and a
			// loaded machine can put either first), not a rule; the
			// order's weight is in the gap, held to the samples' range.
			for i, c := range v.quic[host] {
				if r := tcpTrigger[tr]; r == nil || quicTrigger[c] != r {
					continue
				}
				if d := tr.Millis - c.Millis; d >= -raceWindow && d <= raceWindow {
					ms[fmt.Sprintf("%s after QUIC #%d", k, i+1)] = int(d)
				}
			}
			goaway := "no"
			for _, fr := range tr.Frames {
				if fr.Type == "GOAWAY" {
					goaway = fr.ErrCode
				}
			}
			f.cat[k+" goaway"] = goaway
		}
	}
	for host, n := range v.declined {
		f.num["TCP "+host+" declined"] = n
	}
	return f, ms, stalled
}

// stalledIn is how long this process was stalled (stalls) within the d ms
// of page's clock from from on; 0 for a capture read from a file (the
// browser's), which has no Start.
func stalledIn(page *capture.Page, from, d int64) int {
	if page.Start.IsZero() || d <= 0 {
		return 0
	}
	at := page.Start.Add(time.Duration(from) * time.Millisecond)
	return int(stalls.stalled(at, at.Add(time.Duration(d)*time.Millisecond)).Milliseconds())
}

// pathMove is how a QUIC connection moved to a new client address, in µs
// from the first send of the last request before the move: when it
// moved; the ack-eliciting datagrams it sent on the old path in between
// (its probes) and how far each one's packet number is from the client's
// packet before (steps: 2 where it skipped one, as Chrome does); its
// probe timeout as those probes show it (probePTO) and as RFC 9002
// computes it from the RTT samples the trace has (rttPTO).
type pathMove struct {
	after    int64
	probes   int
	steps    string
	probePTO float64
	rttPTO   float64
}

// initialSteps is how far each of c's Initial packets' numbers is from
// the one before: Chrome skips one before each probe (1 2 2 2 1, the last
// its close), the others none.
func initialSteps(c *quictrace.Conn) string {
	var steps []string
	var prev uint64
	first := true
	for _, d := range c.Datagrams {
		for _, p := range d.Packets {
			if p.Type != quictrace.PacketInitial {
				continue
			}
			if !first {
				steps = append(steps, strconv.FormatUint(p.PN-prev, 10))
			}
			prev, first = p.PN, false
		}
	}
	return strings.Join(steps, " ")
}

// movePath is c's move to a new path; false if it never moved, or moved
// with no request before.
func movePath(c *quictrace.Conn) (pathMove, bool) {
	moved := slices.IndexFunc(c.Datagrams, func(d quictrace.Datagram) bool {
		return d.Path != 0
	})
	if moved < 0 {
		return pathMove{}, false
	}
	// The last request's first send: the last client-initiated
	// bidirectional stream's first datagram.
	sent, seen := -1, map[uint64]bool{}
	for i, d := range c.Datagrams[:moved] {
		for _, p := range d.Packets {
			for _, f := range p.Frames {
				if f.Type == quictrace.FrameStream && f.Stream != nil && *f.Stream%4 == 0 && !seen[*f.Stream] {
					seen[*f.Stream] = true
					sent = i
				}
			}
		}
	}
	if sent < 0 {
		return pathMove{}, false
	}
	start := c.Datagrams[sent].Micros
	m := pathMove{
		after:  c.Datagrams[moved].Micros - start,
		rttPTO: tracePTO(c, start),
	}
	last := start
	var prev uint64
	var steps []string
	for i, d := range c.Datagrams[sent:moved] {
		probe := i > 0 && slices.ContainsFunc(d.Packets, func(p quictrace.Packet) bool {
			return slices.ContainsFunc(p.Frames, func(f quictrace.Frame) bool {
				return f.Type != quictrace.FrameAck && f.Type != quictrace.FramePadding && f.Type != quictrace.FrameConnectionClose
			})
		})
		for _, p := range d.Packets {
			if p.Type != quictrace.Packet1RTT {
				continue
			}
			if probe {
				steps = append(steps, strconv.FormatUint(p.PN-prev, 10))
				probe = false
				m.probes++
				last = d.Micros
			}
			prev = p.PN
		}
	}
	m.steps = strings.Join(steps, " ")
	// The k-th probe goes 1 + 2 + … + 2^(k-1) = 2^k - 1 PTOs after the
	// request (RFC 9002, section 6.2.1: the timer doubles on each); each
	// timer's lateness is spread over all of them.
	if m.probes > 0 {
		m.probePTO = float64(last-start) / float64(int(1)<<m.probes-1)
	}
	return m, true
}

// tracePTO is the least probe timeout (µs, without back-off) RFC 9002
// allows c's client at before (sections 5.3 and 6.2.1: smoothed RTT plus
// four RTT variations, at least 1 ms, plus the server's max_ack_delay) on
// the RTT samples its trace has. The trace opens the server's 1-RTT
// headers only, not its ACK frames, so the samples are the round trips
// the other way: from the server's packet to the client's ACK of it (its
// largest newly acknowledged), less the delay that ACK reports, without
// RFC 9002's guards on a peer's report (the cap at max_ack_delay, the
// min_rtt check), which only lengthen a sample. The client's own samples
// are on the server's ACKs, which the trace cannot show, and its PTO runs
// above this one: Chrome's probes show 31–34 ms against 27–28 here,
// mimicry's 70 ms under -race against 34. So this is a floor, not the
// PTO.
func tracePTO(c *quictrace.Conn, before int64) float64 {
	exponent := transportParameter(c.TransportParameters, tpAckDelayExponent, 3)
	sent := map[string]int64{}
	for _, d := range c.Server {
		for _, p := range d.Packets {
			sent[pnSpace(p.Type)+" "+strconv.FormatUint(p.PN, 10)] = d.Micros
		}
	}
	largest := map[string]uint64{}
	var smoothed, variation float64
	sampled := false
	for _, d := range c.Datagrams {
		if d.Micros >= before {
			break
		}
		for _, p := range d.Packets {
			space := pnSpace(p.Type)
			for _, f := range p.Frames {
				if l, ok := largest[space]; f.Type != quictrace.FrameAck || ok && f.Largest <= l {
					continue
				}
				largest[space] = f.Largest
				at, ok := sent[space+" "+strconv.FormatUint(f.Largest, 10)]
				if !ok {
					continue
				}
				latest := float64(d.Micros - at)
				if !sampled {
					smoothed, variation, sampled = latest, latest/2, true
					continue
				}
				adjusted := latest
				if delay := float64(f.Delay << exponent); delay < latest {
					adjusted -= delay
				}
				variation = 0.75*variation + 0.25*math.Abs(smoothed-adjusted)
				smoothed = 0.875*smoothed + 0.125*adjusted
			}
		}
	}
	if !sampled {
		// RFC 9002's initial RTT, 333 ms.
		smoothed, variation = 333000, 333000/2
	}
	maxAckDelay := float64(transportParameter(c.ServerTransportParameters, tpMaxAckDelay, 25) * 1000)
	return smoothed + max(4*variation, 1000) + maxAckDelay
}

// pnSpace is the packet number space of a packet type.
func pnSpace(typ string) string {
	if typ == quictrace.PacketInitial || typ == quictrace.PacketHandshake {
		return typ
	}
	return "application"
}

// checkPathMoves holds every QUIC connection of page that moved to a new
// path to the profile's rule (quic.loss.path_degrading.after_ptos): it
// moved that many of its own probe timeouts after its last request's
// first send, within half a PTO (h3gen's rounding when it derives the
// count), its PTO measured from its probes on the old path, which come no
// sooner than RFC 9002 allows on the trace's RTT samples (tracePTO). In
// the connection's own PTOs the rule holds on a loaded machine too, where
// the client's RTT, and so its PTO, grows.
func checkPathMoves(t *testing.T, p *profile.Profile, who string, page *capture.Page) {
	t.Helper()
	for i, c := range page.QUIC {
		m, ok := movePath(c)
		if !ok {
			continue
		}
		conn := fmt.Sprintf("%s: QUIC %s (connection %d)", who, c.Host, i+1)
		d := p.QUIC.Loss.PathDegrading
		switch {
		case d == nil || d.Action != profile.PathMigrate:
			t.Errorf("%s moved to a new path; the profile never migrates", conn)
			continue
		case m.probes == 0:
			t.Errorf("%s moved to a new path %.1f ms after its last request without a probe first", conn, float64(m.after)/1000)
			continue
		}
		ratio := float64(m.after) / m.probePTO
		report := fmt.Sprintf("%s moved to a new path %.1f ms after its last request: %.2f of its %.1f ms PTOs (%d probes; RFC 9002 on its trace's RTT samples: at least %.1f ms)", conn, float64(m.after)/1000, ratio, m.probePTO/1000, m.probes, m.rttPTO/1000)
		switch {
		case math.Abs(ratio-float64(d.AfterPTOs)) > 0.5:
			t.Errorf("%s; want %d", report, d.AfterPTOs)
		case m.probePTO < m.rttPTO-1000:
			t.Errorf("%s; its probes came sooner than its RTT allows", report)
		default:
			t.Log(report)
		}
	}
}

// compareEdge holds mimicry's page to every browser sample, the ClientHello
// and transport parameters of a connection retried after Version
// Negotiation to the browser's retries (Firefox's: another hello than its
// first flight's, tls.quic.version_negotiation), and mimicry's and every
// sample's moves to a new path to the profile's rule (checkPathMoves).
func compareEdge(t *testing.T, p *profile.Profile, got *capture.Page, want []*capture.Page, skip func(string) bool) {
	t.Helper()
	gf, gms, stalled := edgeFeatures(got)
	refs := make([]features, 0, len(want))
	refMS := make([]map[string]int, 0, len(want))
	for _, w := range want {
		f, ms, _ := edgeFeatures(w)
		refs = append(refs, f)
		refMS = append(refMS, ms)
	}
	compareFeatures(t, "edge", gf, refs, skip)
	compareTimes(t, gms, stalled, refMS, skip)
	checkPathMoves(t, p, "mimicry", got)
	for i, w := range want {
		checkPathMoves(t, p, fmt.Sprintf("browser sample %d", i+1), w)
	}
	retries := make([]*quictrace.Conn, 0, len(want))
	for _, w := range want {
		retries = append(retries, vnRetries(w)...)
	}
	for _, c := range vnRetries(got) {
		t.Run("retry after Version Negotiation", func(t *testing.T) {
			if len(retries) == 0 {
				t.Fatal("the browser retried no connection after Version Negotiation")
			}
			compareHello(t, p, c, retries)
			compareFeatures(t, "transport parameters", paramFeatures(c), mapConns(retries, paramFeatures), skip)
		})
	}
	if len(retries) > 0 && len(vnRetries(got)) == 0 {
		t.Error("no connection retried after Version Negotiation; the browser retried")
	}
}

// vnRetries are page's QUIC connections that follow one the server
// answered with Version Negotiation, to the same host.
func vnRetries(page *capture.Page) []*quictrace.Conn {
	var out []*quictrace.Conn
	for i, c := range page.QUIC {
		if i > 0 && page.QUIC[i-1].Host == c.Host && outcome(page.QUIC[i-1]) == "version negotiation" && c.Version != page.QUIC[i-1].Version {
			out = append(out, c)
		}
	}
	return out
}

// compareTimes holds each of got's times to the refs' range widened by
// edgeSlack: present where every ref has it, absent where none does. A
// stall only lengthens a time, so one above the range passes if it is in
// range less the time this process was stalled within it (stalled), and
// one below never does.
func compareTimes(t *testing.T, got, stalled map[string]int, refs []map[string]int, skip func(string) bool) {
	t.Helper()
	keys := map[string]bool{}
	for k := range got {
		keys[k] = true
	}
	for _, r := range refs {
		for k := range r {
			keys[k] = true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		lo, hi, in := 0, 0, 0
		for _, r := range refs {
			v, ok := r[k]
			if !ok {
				continue
			}
			if in == 0 || v < lo {
				lo = v
			}
			if in == 0 || v > hi {
				hi = v
			}
			in++
		}
		g, ok := got[k]
		slack := edgeSlack(hi)
		label := "edge " + k + " ms"
		under := ok && g < lo-slack
		over := ok && g > hi+slack && g-stalled[k] > hi+slack
		switch {
		case (in == 0 || !ok && in == len(refs) || under || over) && skip(label):
		case in == 0:
			t.Errorf("%s: %d, the browser has none", label, g)
		case !ok && in == len(refs):
			t.Errorf("%s: none, the browser's %d..%d", label, lo, hi)
		case under || over:
			t.Errorf("%s: %d (%d of it with the process stalled), the browser's %d..%d (± %d)", label, g, stalled[k], lo, hi, slack)
		case ok && g > hi+slack:
			t.Logf("%s: %d, %d of it with the process stalled (browser %d..%d)", label, g, stalled[k], lo, hi)
		case ok:
			t.Logf("%s: %d (browser %d..%d)", label, g, lo, hi)
		}
	}
}
