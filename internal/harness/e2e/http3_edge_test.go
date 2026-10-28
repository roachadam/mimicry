package e2e

import (
	"cmp"
	"context"
	"errors"
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
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// h3PageHost is the h3 pages' own host (the server's h3Host), where / is.
const h3PageHost = "www.h3-a.test"

// Every request of the h3-blackhole, h3-broken, h3-vn, h3-v2 (where python3
// has aioquic), h3-early and h3-too-early pages through mimicry on the
// browser's schedule, against every sample:
//
//   - per request: its connections in order, its delay, and on a 0-RTT
//     connection how each stream went (earlyStream);
//   - per QUIC attempt: resumption, versions, the server's answer, how it
//     ended, path moves (checkPathMoves), give-up datagrams, its trigger;
//   - per TCP connection: its trigger, its race against the QUIC attempt,
//     the GOAWAY sent, how many were declined.
//
// Times lie within the samples' range widened by edgeSlack, plus this
// process's stalls (compareTimes). Known differences are in h3EdgeGaps and
// h3EdgeSchedulingGaps. Each page runs twice: with
// Config.BrowserQUICFallback ("browser"), where that is all that differs,
// and with the fast default ("fast"), whose differences besides are
// exactly h3EdgeFastGaps: requests that stop waiting on a QUIC attempt
// the server has not answered, and the TCP connections they open. A
// healthy page has none. Subtests run in parallel (about 75 s).
func TestHTTP3EdgeMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		for _, sc := range []*server.Scenario{server.H3Blackhole, server.H3Broken, server.H3VN, server.H3V2, server.H3Early, server.H3TooEarly} {
			for _, fast := range []bool{false, true} {
				mode := "browser"
				if fast {
					mode = "fast"
				}

				t.Run(name+"/"+sc.Name+"/"+mode, func(t *testing.T) {
					t.Parallel()

					if sc.NeedsAioquic() {
						if err := server.AioquicAvailable(t.Context()); err != nil {
							t.Skipf("%s needs aioquic: %v", sc.Name, err)
						}
					}

					p, err := loadProfile(name)
					if err != nil {
						t.Fatal(err)
					}

					if p.HTTP3 == nil || p.QUIC == nil {
						t.Fatal("the profile has no http3 or quic section")
					}

					want := h3Captures(t, p, sc)
					skip, unused := edgeGapsFor(t, capture.Source(p, sc.Name)+"/"+sc.Name, fast)
					got := runH3Schedule(t, p, sc, want[0], skip, func(cfg *mimicry.Config) {
						cfg.BrowserQUICFallback = !fast
					})
					compareEdge(t, p, got, want, skip)
					unused()
				})
			}
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
		"firefox-158-macos/h3-too-early edge TCP www.h3-a.test declined":                    "beside 3–4 of its 4 retries after 425 Too Early per sample (which ones varies), Firefox also opened a TCP connection to the host and closed it before any request; the retry itself goes on the QUIC connection, as mimicry's does. No profile field holds when Firefox opens these (report h3-early-2026-10-26)",
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
			gaps[name+"/h3-broken edge request /h3/"+l+".so streams"] = "as its sending"
		}

		gaps[name+"/h3-broken edge QUIC www.h3-b.test #1 closed by"] = "Safari closes a QUIC connection itself when the server's GOAWAY arrives; no profile field describes it"
		// On the h3-early pages Safari resumes with 0-RTT after an idle
		// timeout too, by the method rule Chrome's profile holds (GET,
		// HEAD and OPTIONS early, the rest held for HANDSHAKE_DONE; a 425
		// sent again on its connection); mimicry's Safari connections are
		// fresh, so none of it shows.
		const (
			idleResumed = "Safari resumed this QUIC connection with 0-RTT after the last idled out (GET, HEAD and OPTIONS in 0-RTT, the other methods held until HANDSHAKE_DONE, a 425 sent again on the connection: report h3-early-2026-10-26); no profile section describes Safari's resumption, so mimicry's is fresh"
			noOptions   = "Safari's profile has no OPTIONS fetch (none in its kinds captures; kindsgen does not read the h3-early pages), so mimicry refuses it (ErrNotMeasured) and its form.so navigation goes on QUIC #9, not #10"
		)

		for _, sc := range []string{"h3-early", "h3-too-early"} {
			for i := 2; i <= 8; i++ {
				gaps[fmt.Sprintf("%s/%s edge QUIC www.h3-a.test #%d", name, sc, i)] = idleResumed
			}

			for _, l := range []string{"post", "put", "delete", "xhr-post", "pair-get", "pair-post", "get", "head", "form"} {
				gaps[name+"/"+sc+" edge request /h3/"+l+".so sent"] = idleResumed
				gaps[name+"/"+sc+" edge request /h3/"+l+".so streams"] = idleResumed
			}

			for _, k := range []string{
				"request /h3/options.so not measured",
				"request /h3/options.so sent",
				"request /h3/options.so streams",
				"request /h3/options.so delay ms",
				"QUIC www.h3-a.test #9",
				"QUIC www.h3-a.test #9 closed by",
				"QUIC www.h3-a.test #9 trigger",
				"QUIC www.h3-a.test #10",
				"QUIC www.h3-a.test #10 trigger",
				"QUIC www.h3-a.test #10 after its trigger ms",
			} {
				gaps[name+"/"+sc+" edge "+k] = noOptions
			}
		}
	}

	return gaps
}()

// h3EdgeSchedulingGaps are known differences that show or not by this
// process's scheduling, keyed as h3EdgeGaps: skipped as those are, but
// not required to appear.
var h3EdgeSchedulingGaps = func() map[string]string {
	gaps := map[string]string{}
	for _, name := range []string{"safari-26-macos", "safari-27-macos"} {
		for _, sc := range []string{"h3-early", "h3-too-early"} {
			gaps[name+"/"+sc+" edge QUIC www.h3-a.test #6 trigger"] = "Safari made the pair's GET and POST together (0–1 ms apart) 5–9 ms before its connection started, so the POST is the later trigger; mimicry's GET starts its connection at once, before or after its POST is made as this process schedules the two"
		}
	}

	return gaps
}()

// h3EdgeFastGaps are how the fast default differs from the browser on the
// edge pages, keyed as h3EdgeGaps: each must appear, in fast mode only,
// and only on a page where QUIC fails (decision 0036).
var h3EdgeFastGaps = func() map[string]string {
	const (
		chromeHold = "fast fallback: Chrome holds closed1.cs in 0-RTT on its resumed attempt until it gives up (4.0 s, zeroRttHolds), then sends it on TCP; mimicry's goes on TCP once the server has not answered for the race delay (305 ms)"
		chromeNext = "fast fallback: closed2.cs, made while Chrome's 0-RTT attempt is unanswered, waits for it with no TCP and triggers the fresh attempt and TCP after the give-up; mimicry's goes on the TCP connection closed1.cs opened, and the fresh attempt follows the next request after the give-up, closed3.cs"
		firefox    = "fast fallback: Firefox holds closed2.cs on its unanswered resumed attempt until it gives up (5 s, hold); mimicry's goes on TCP once the server has not answered for its first probe timeout (300 ms), opening the TCP connection closed3.cs would have"
		safari     = "fast fallback: Safari's request waits out its 100 ms race delay after Version Negotiation ended its attempt; mimicry's goes on TCP at once"
	)

	gaps := map[string]string{
		"chrome-155-macos/h3-broken edge request /h3/closed1.cs delay ms":            chromeHold,
		"chrome-155-macos/h3-broken edge TCP www.h3-b.test #2 trigger":               chromeHold,
		"chrome-155-macos/h3-broken edge TCP www.h3-b.test #2 after its trigger ms":  chromeHold,
		"chrome-155-macos/h3-broken edge TCP www.h3-b.test #2 after QUIC #2 ms":      chromeHold,
		"chrome-155-macos/h3-broken edge TCP www.h3-b.test #2 after QUIC #3 ms":      chromeHold,
		"chrome-155-macos/h3-broken edge request /h3/closed2.cs delay ms":            chromeNext,
		"chrome-155-macos/h3-broken edge QUIC www.h3-b.test #3 trigger":              chromeNext,
		"chrome-155-macos/h3-broken edge QUIC www.h3-b.test #3 after its trigger ms": chromeNext,
		"firefox-158-macos/h3-broken edge request /h3/closed2.cs delay ms":           firefox,
		"firefox-158-macos/h3-broken edge TCP www.h3-b.test #3 trigger":              firefox,
	}
	for _, name := range []string{"safari-26-macos", "safari-27-macos"} {
		for _, k := range []string{
			"edge request /h3/vn-3.so delay ms",
			"edge TCP www.h3-a.test #2 after its trigger ms",
			"edge TCP www.h3-a.test #2 after QUIC #1 ms",
		} {
			gaps[name+"/h3-vn "+k] = safari
		}
	}

	return gaps
}()

// edgeGapsFor is gapsFor for h3EdgeGaps and h3EdgeSchedulingGaps, and with
// fast for h3EdgeFastGaps.
func edgeGapsFor(t *testing.T, name string, fast bool) (skip func(key string) bool, unused func()) {
	t.Helper()

	var mu sync.Mutex

	seen := map[string]bool{}

	skip = func(key string) bool {
		k := name + " " + key
		why, ok := h3EdgeGaps[k]

		if !ok && fast {
			why, ok = h3EdgeFastGaps[k]
		}

		if !ok {
			why, ok = h3EdgeSchedulingGaps[k]
		}

		if !ok {
			return false
		}

		mu.Lock()

		seen[k] = true
		mu.Unlock()
		t.Logf("known gap: %s: %s", key, why)

		return true
	}
	unused = func() {
		required := []map[string]string{h3EdgeGaps}
		if fast {
			required = append(required, h3EdgeFastGaps)
		}

		for _, gaps := range required {
			for k := range gaps {
				if strings.HasPrefix(k, name+" ") && !seen[k] {
					t.Errorf("listed gap %q no longer appears", k)
				}
			}
		}
	}

	return skip, unused
}

// runH3Schedule walks sc's page through mimicry as p, its Config changed
// by config: the navigation to /, each step after its pause (NoWait steps
// on time beside the others), then fetch('/done') where browser did.
// Unanswered requests are cancelled; an ErrNotMeasured step fails unless
// skip pins it.
func runH3Schedule(t *testing.T, p *profile.Profile, sc *server.Scenario, browser *capture.Page, skip func(string) bool, config func(*mimicry.Config)) *capture.Page {
	t.Helper()
	// Its times from a request on are net of the process's stalls.
	stalls.watch()

	s := startPageWith(t, p, sc, config)
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

	// from is the page the step is made from, read when it is issued: a
	// navigation step replaces page while earlier steps are still sending.
	send := func(st server.H3Step, from *url.URL, target string) error {
		err := sendScheduled(ctx, s, st, from, target)
		if errors.Is(err, mimicry.ErrNotMeasured) && skip("edge request "+st.Path()+" not measured") {
			return nil
		}

		return err
	}

	var wg sync.WaitGroup

	for _, st := range steps {
		select {
		case <-time.After(time.Duration(st.PauseMillis) * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}

		target := s.url(st.Host, st.Path())
		if st.NoWait {
			target += "?t=" + strconv.FormatInt(time.Since(start).Milliseconds(), 10)
		}

		if st.Kind == "navigate" {
			// It ends the page's steps; the page it loads asks for /done.
			if err := send(st, page, target); err != nil {
				t.Fatalf("%s: %v", st.Label, err)
			}

			if page, err = url.Parse(target); err != nil {
				t.Fatal(err)
			}

			break
		}

		if !st.NoWait {
			if err := send(st, page, target); err != nil {
				t.Fatalf("%s: %v", st.Label, err)
			}

			continue
		}

		from := page
		wg.Go(func() {
			if err := send(st, from, target); err != nil && ctx.Err() == nil {
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
	// streams are its streams on QUIC connections that sent 0-RTT, in
	// order of arrival: the connection and how the stream went
	// (earlyStream.describe).
	streams []string
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
	// early is its stream, on a QUIC connection that sent 0-RTT; nil
	// otherwise.
	early *earlyStream
}

// earlyStream is a request stream on a QUIC connection that sent 0-RTT:
// its packet type (of its first STREAM frame), whether that went in the
// datagram with the client's Finished, when the Finished went (page
// clock, ms), and whether an earlier stream of the connection carried the
// same URL (its retry after 425 Too Early).
type earlyStream struct {
	packet       string
	withFinished bool
	finished     int64
	again        bool
}

// describe is how s went, for a request issued at issued (page clock):
// in 0-RTT; held for the handshake (in 1-RTT though made before the
// Finished went, within triggerSlack: the page's clock is in whole ms),
// in the Finished's datagram or after it; in 1-RTT, made later; or again
// after a 425, in its packet type.
func (s *earlyStream) describe(issued int64) string {
	switch {
	case s.again:
		return "again after 425, " + s.packet
	case s.packet == quictrace.Packet0RTT:
		return "0-RTT"
	case issued > s.finished+triggerSlack:
		return s.packet
	case s.withFinished:
		return "held, with the Finished"
	}

	return "held, after the Finished"
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

		finished, finishedAt := c.FinishedDatagram()
		starts := c.StreamStarts()
		reqs := slices.Clone(c.H3.Requests)
		slices.SortFunc(reqs, func(a, b quictrace.H3Request) int {
			return cmp.Compare(a.Stream, b.Stream)
		})

		seen := map[string]bool{}
		for _, r := range reqs {
			a := arrival{
				at:   r.Millis,
				path: r.Path,
				via:  via,
			}
			if s, ok := starts[r.Stream]; ok && c.EarlyData {
				a.early = &earlyStream{
					packet:       s.PacketType,
					withFinished: s.Datagram == finished,
					finished:     c.Millis + finishedAt/1000,
					again:        seen[r.Path],
				}
			}

			seen[r.Path] = true

			arrivals = append(arrivals, a)
		}
	}

	slices.SortStableFunc(arrivals, func(a, b arrival) int {
		return int(a.at - b.at)
	})

	byPath := map[string]*edgeRequest{}
	last := map[string]int64{}
	ts := map[string]int64{}
	early := map[string][]arrival{}

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
		if a.early != nil {
			early[a.path] = append(early[a.path], a)
		}
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

	for path, as := range early {
		r := byPath[path]
		for _, a := range as {
			r.streams = append(r.streams, a.via+": "+a.early.describe(r.issued))
		}
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
		if len(r.streams) > 0 {
			f.cat["request "+r.label+" streams"] = strings.Join(r.streams, "; ")
		}

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
// from the last request's first send: when, its probes on the old path and
// their packet-number steps (Chrome skips one), and its PTO as the probes
// show it (probePTO) and as RFC 9002 computes it from the trace (rttPTO).
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
	var (
		steps []string
		prev  uint64
	)

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

	var (
		prev  uint64
		steps []string
	)

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

// tracePTO is the least PTO (µs, no back-off) RFC 9002 allows c's client
// at before (sections 5.3, 6.2.1), from the trace's RTT samples. The trace
// only shows server packets to client ACKs, so the samples are the reverse
// round trip, without RFC 9002's guards (which only lengthen a sample). It is
// a floor: Chrome's probes show 31–34 ms against 27–28 here.
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

// checkPathMoves holds every connection of page that moved path to
// quic.loss.pathDegrading.afterPtos: the move comes that many of its own
// PTOs after its last request, within half a PTO (h3gen's rounding). The PTO
// is measured from its probes, floored by tracePTO, so the rule holds under
// load too.
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
// first flight's, tls.quic.versionNegotiation), and mimicry's and every
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
