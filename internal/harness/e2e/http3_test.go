package e2e

import (
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/h3"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/diff"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/internal/qpack"
	"github.com/roachadam/mimicry/profile"
)

// Every request of the h3-discovery and h3-page pages through mimicry, with
// the browser's pauses (the 35 s idle included), against every sample
// (Chrome's and Firefox's -h3-local-roots captures, Safari's unforced):
//
//   - transport: HTTP/2 or HTTP/3, which connection, 0-RTT, the TCP/QUIC
//     start on Alt-Svc, GOAWAYs;
//   - per QUIC connection: first flight, ClientHello, transport parameters,
//     0-RTT packets, NEW_CONNECTION_IDs, credit frames, close;
//   - HTTP/3: streams, SETTINGS, control frames, QPACK instructions, and per
//     request field lines, frames, PRIORITY_UPDATE and end.
//
// Features the samples agree on must match; others are logged; numbers lie
// within the samples' range. Per-connection draws are held to the profile's
// rule instead (quicRuleViolations), and compareDraws catches a repeated draw.
func TestHTTP3MatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name+"/rules", func(t *testing.T) {
			t.Parallel()

			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			if p.QUIC == nil {
				t.Fatal("the profile has no quic section")
			}

			for _, c := range capturedConns(t, p) {
				for _, v := range quicRuleViolations(p, c.c, t.Logf) {
					t.Errorf("%s: the profile's rule does not describe the browser: %s", c.at, v)
				}
			}
		})

		for _, sc := range []*server.Scenario{server.H3Discovery, server.H3Page} {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				t.Parallel()

				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}

				if p.HTTP3 == nil || p.QUIC == nil {
					t.Fatal("the profile has no http3 or quic section")
				}

				want := h3Captures(t, p, sc)
				got := runH3Page(t, p, sc, want[0])
				skip, unused := gapsFor(t, capture.Source(p, sc.Name)+"/"+sc.Name)
				compareH3Page(t, p, got, want, flightRefs(t, p), skip)
				unused()
			})
		}
	}
}

// h3Captures loads the browser's capture of sc p is checked against: the
// -h3-local-roots one where it exists (Chrome, Firefox), else the unforced
// one (Safari), rewritten to p's version where inherited.
func h3Captures(t *testing.T, p *profile.Profile, sc *server.Scenario) []*capture.Page {
	t.Helper()

	rest := ".local-roots.pagetrace.json.gz"
	if path, _, _ := capturePath(t, p, sc.Name, rest); !exists(path) {
		rest = ".pagetrace.json.gz"
	}

	pages, err := loadPages(t, p, sc.Name, rest)
	if err != nil {
		t.Fatal(err)
	}

	return pages
}

// exists reports whether the file at path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// h3Steps are sc's steps.
func h3Steps(sc *server.Scenario) []server.H3Step {
	switch sc {
	case server.H3Page:
		return server.H3PageSteps()
	case server.H3Blackhole:
		return server.H3BlackholeSteps()
	case server.H3Broken:
		return server.H3BrokenSteps()
	case server.H3VN:
		return server.H3VNSteps()
	case server.H3V2:
		return server.H3V2Steps()
	case server.H3Download:
		return server.H3DownloadSteps()
	case server.H3Early, server.H3TooEarly:
		return server.H3EarlySteps()
	}

	return server.H3DiscoverySteps()
}

// firstPageDelay is how long after the first QUIC connection to host
// started the browser's page sent its first request over it: the page's
// parse and script start, which mimicry lacks. Waiting as long gives the
// Alt-Svc connection the browser's head start. Zero where it went on TCP.
func firstPageDelay(browser *capture.Page, host string, steps []server.H3Step) time.Duration {
	offset := map[string]int64{}

	var pauses int64

	for _, st := range steps {
		if st.Page != "/" {
			continue
		}

		if st.Tag == "" {
			pauses += int64(st.PauseMillis)
		}

		offset[st.Path()] = pauses
	}

	for _, c := range browser.QUIC {
		if c.Host != host {
			continue
		}

		least, found := int64(0), false
		if c.H3 != nil {
			for _, r := range c.H3.Requests {
				o, ok := offset[r.Path]
				if d := r.Millis - c.Millis - o; ok && (!found || d < least) {
					least, found = d, true
				}
			}
		}

		return time.Duration(max(least, 0)) * time.Millisecond
	}

	return 0
}

// h3ScriptTurn stands for the browser's script turn before fetch('/done'),
// with margin.
const h3ScriptTurn = 10 * time.Millisecond

// runH3Page walks sc's pages through mimicry as profile p: the typed
// navigation to /, then each page's markup requests in the order browser
// sent them, then its script's steps after their pauses, a navigation
// ending a page, and fetch('/done') from the last page.
func runH3Page(t *testing.T, p *profile.Profile, sc *server.Scenario, browser *capture.Page) *capture.Page {
	t.Helper()
	// Its connections' ACK delays are held net of the process's stalls.
	stalls.watch()

	s := startPage(t, p, sc)
	s.planEvery()

	steps := h3Steps(sc)
	order := sentOrder(browser)
	host := steps[0].Host

	page, err := url.Parse(s.url(host, "/"))
	if err != nil {
		t.Fatal(err)
	}

	s.send(http.MethodGet, page.String(), mimicry.Request{}, nil, "")

	for path := "/"; path != ""; {
		var markup, scripted []server.H3Step
		for _, st := range steps {
			switch {
			case st.Page != path:
			case st.Tag != "":
				markup = append(markup, st)
			default:
				scripted = append(scripted, st)
			}
		}

		slices.SortStableFunc(markup, func(a, b server.H3Step) int {
			return order[a.Path()] - order[b.Path()]
		})

		if path == "/" {
			select {
			case <-time.After(firstPageDelay(browser, host, steps)):
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}
		}

		for _, st := range markup {
			sendH3Step(s, st, page)
		}

		next := ""
		for _, st := range scripted {
			select {
			case <-time.After(time.Duration(st.PauseMillis) * time.Millisecond):
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}

			if st.Kind == "navigate" {
				target := s.url(st.Host, st.Path())
				// The page navigates by script, without user activation.
				s.send(http.MethodGet, target, mimicry.Request{
					From: page,
				}, http.Header{
					"Sec-Fetch-User": nil,
				}, "")

				if page, err = url.Parse(target); err != nil {
					t.Fatal(err)
				}

				next = st.Path()

				break
			}

			sendH3Step(s, st, page)
		}

		path = next
	}

	// The page's script asks for /done a few milliseconds after the last
	// response (2–3 ms in the captures); mimicry has no script turn, and
	// the server snapshots every connection when /done arrives, so a
	// connection answered in 0.5-RTT would lose its last packets
	// (Firefox's fourth NEW_CONNECTION_ID, sent once the server retires
	// the first connection ID after the handshake).
	time.Sleep(h3ScriptTurn)
	s.send(http.MethodGet, s.url(page.Hostname(), "/done"), mimicry.Request{
		Kind: mimicry.Fetch,
		From: page,
	}, nil, "")

	for {
		select {
		case c := <-s.captures:
			if c.Finished && c.Page != nil {
				s.checkPlans(c.Page)

				return c.Page
			}

		case <-time.After(10 * time.Second):
			t.Fatal("no page capture")
		}
	}
}

// sentOrder numbers each path by when browser sent it: by connection
// (TCP ones first, in order), then within it.
func sentOrder(browser *capture.Page) map[string]int {
	out := map[string]int{}
	n := 0

	for _, tr := range browser.H2 {
		for _, r := range tr.Requests {
			if _, ok := out[r.Path]; !ok {
				out[r.Path] = n
			}

			n++
		}
	}

	for _, c := range browser.QUIC {
		if c.H3 == nil {
			continue
		}

		reqs := slices.Clone(c.H3.Requests)
		slices.SortFunc(reqs, func(a, b quictrace.H3Request) int {
			return int(a.Stream) - int(b.Stream)
		})

		for _, r := range reqs {
			if _, ok := out[r.Path]; !ok {
				out[r.Path] = n
			}

			n++
		}
	}

	return out
}

// sendH3Step makes st's request through mimicry from page, as the page's
// markup or script makes it.
func sendH3Step(s *pageSession, st server.H3Step, page *url.URL) {
	s.t.Helper()

	method, r, h := h3StepRequest(st, page)
	s.send(method, s.url(st.Host, st.Path()), r, h, st.Body)
}

// h3StepRequest is how the page at page makes st's request: its method,
// how mimicry sends it (kind, page, credentials, mode, priority), and the
// page's own headers.
func h3StepRequest(st server.H3Step, page *url.URL) (string, mimicry.Request, http.Header) {
	kinds := map[string]mimicry.Kind{
		"script": mimicry.InjectedScript,
		"style":  mimicry.Style,
		"image":  mimicry.InjectedImage,
		"iframe": mimicry.Frame,
		"font":   mimicry.Font,
		"fetch":  mimicry.Fetch,
		"xhr":    mimicry.XHR,
		"beacon": mimicry.Beacon,
	}
	kind := kinds[st.Kind]

	if st.Tag != "" {
		kind = map[string]mimicry.Kind{
			"script": mimicry.Script,
			"style":  mimicry.Style,
			"image":  mimicry.Image,
		}[st.Kind]
		if strings.Contains(st.Tag, " async ") {
			kind = mimicry.AsyncScript
		}
	}

	method := http.MethodGet
	if st.Method != "" {
		method = st.Method
	}

	h := http.Header{}
	for k, v := range st.Headers {
		h[k] = []string{v}
	}

	switch {
	case st.Kind == "navigate":
		// The page's script navigates, without user activation; a POST
		// submits a form.
		kind = mimicry.Navigate
		h["Sec-Fetch-User"] = nil

		if st.Body != "" {
			h.Set("Content-Type", "application/x-www-form-urlencoded")
		}

	case st.Kind == "beacon":
		// sendBeacon's string body: the browser sets the type.
		method = http.MethodPost

		h.Set("Content-Type", "text/plain;charset=UTF-8")

	case st.Body != "" && !slices.ContainsFunc(slices.Collect(maps.Keys(h)), func(k string) bool {
		return strings.EqualFold(k, "Content-Type")
	}):
		// fetch() and XHR send a string body as text (the Fetch
		// standard's extract a body).
		h.Set("Content-Type", "text/plain;charset=UTF-8")
	}

	return method, mimicry.Request{
		Kind:        kind,
		From:        page,
		Credentials: map[string]mimicry.Credentials{"": mimicry.CredentialsSameOrigin, "include": mimicry.CredentialsInclude, "omit": mimicry.CredentialsOmit}[st.Credentials],
		NoCORS:      st.Mode == "no-cors",
		Priority:    map[string]mimicry.Priority{"": mimicry.PriorityAuto, "high": mimicry.PriorityHigh, "low": mimicry.PriorityLow}[st.Priority],
	}, h
}

// h3Transport is how one request went: protocol, host, the connection of
// that host's (TCP and QUIC counted apart, in order) and whether in 0-RTT.
type h3Transport struct {
	proto string
	host  string
	conn  int
	early bool
}

func (x h3Transport) String() string {
	s := fmt.Sprintf("%s %s #%d", x.proto, x.host, x.conn+1)
	if x.early {
		s += " 0-RTT"
	}

	return s
}

// transports maps each path (at its first sending) to how it went.
func transports(page *capture.Page) map[string]h3Transport {
	out := map[string]h3Transport{}
	seen := map[string]int{}

	for _, tr := range page.H2 {
		i := seen[tr.Host]
		seen[tr.Host]++

		for _, r := range tr.Requests {
			if _, ok := out[r.Path]; !ok {
				out[r.Path] = h3Transport{
					proto: "h2",
					host:  tr.Host,
					conn:  i,
				}
			}
		}
	}

	seen = map[string]int{}
	for _, c := range page.QUIC {
		i := seen[c.Host]
		seen[c.Host]++

		if c.H3 == nil {
			continue
		}

		for _, r := range c.H3.Requests {
			if _, ok := out[r.Path]; !ok {
				out[r.Path] = h3Transport{
					proto: "h3",
					host:  c.Host,
					conn:  i,
					early: c.EarlyData,
				}
			}
		}
	}

	return out
}

// quicConn is a page's QUIC connection by host and its place among that
// host's, or nil.
func quicConn(page *capture.Page, host string, n int) *quictrace.Conn {
	for _, c := range page.QUIC {
		if c.Host != host {
			continue
		}

		if n == 0 {
			return c
		}

		n--
	}

	return nil
}

// compareH3Page compares mimicry's page with every browser sample.
func compareH3Page(t *testing.T, p *profile.Profile, got *capture.Page, want []*capture.Page, flights map[bool][]*quictrace.Conn, skip func(string) bool) {
	t.Helper()

	gotT := transports(got)
	for path, w := range transports(want[0]) {
		var forms []string
		for _, sample := range want {
			if x, ok := transports(sample)[path]; ok && !slices.Contains(forms, x.String()) {
				forms = append(forms, x.String())
			}
		}

		g, ok := gotT[path]
		switch {
		case (!ok || !slices.Contains(forms, g.String())) && skip("transport "+path):
		case !ok:
			t.Errorf("%s: not sent (browser: %s)", path, w)
		case !slices.Contains(forms, g.String()):
			t.Errorf("%s: went %s, browser %s", path, g, strings.Join(forms, " or "))
		}
	}

	for path := range gotT {
		if _, ok := transports(want[0])[path]; !ok {
			t.Errorf("%s: sent, the browser did not", path)
		}
	}

	compareFeatures(t, "TCP", tcpFeatures(got), mapEach(want, tcpFeatures), skip)
	compareFeatures(t, "QUIC", quicHosts(got), mapEach(want, quicHosts), skip)

	seen := map[string]int{}
	for _, c := range got.QUIC {
		n := seen[c.Host]
		seen[c.Host]++

		label := fmt.Sprintf("QUIC %s #%d", c.Host, n+1)

		var refs []*quictrace.Conn

		for _, sample := range want {
			if w := quicConn(sample, c.Host, n); w != nil {
				refs = append(refs, w)
			}
		}

		if len(refs) == 0 {
			t.Errorf("%s: the browser opened no such connection", label)

			continue
		}

		t.Run(fmt.Sprintf("%s#%d", c.Host, n+1), func(t *testing.T) {
			compareQUICConn(t, p, c, refs, flights[c.Resumed], skip)
		})
	}

	compareDraws(t, p, got)
}

// compareDraws: what the profile draws per connection from so many values
// that two connections alike by chance is rarer than once in 10^9 (Chrome's
// shuffled transport parameters, its CRYPTO layout) differs between
// mimicry's connections of a page. The rules hold each draw to the
// profile's range; this catches the same draw on every connection.
func compareDraws(t *testing.T, p *profile.Profile, page *capture.Page) {
	t.Helper()

	draws := map[string][]string{}
	for _, c := range page.QUIC {
		// A resumed hello and its parameters differ from a full one's.
		group := fmt.Sprintf(" (resumed %v)", c.Resumed)
		if p.QUIC.TransportParameterOrder == profile.TransportParameterOrderShuffled {
			order := make([]string, 0, len(c.TransportParameters))
			for _, tp := range c.TransportParameters {
				order = append(order, tpName(tp.ID, tp.Grease))
			}

			k := "transport parameter order" + group

			draws[k] = append(draws[k], strings.Join(order, " "))
		}

		if p.QUIC.Crypto.Split == profile.CryptoSplitShuffle {
			var layout []string
			for _, d := range firstFlight(c) {
				for _, pk := range d.Packets {
					for _, f := range pk.Frames {
						if pk.Type == quictrace.PacketInitial {
							layout = append(layout, fmt.Sprintf("%s %d+%d", f.Type, f.Offset, f.Length))
						}
					}
				}
			}

			k := "CRYPTO layout" + group

			draws[k] = append(draws[k], strings.Join(layout, ", "))
		}
	}

	for what, vs := range draws {
		if len(vs) >= 2 && !slices.ContainsFunc(vs, func(v string) bool {
			return v != vs[0]
		}) {
			t.Errorf("%s: the same on all %d QUIC connections, which the profile draws per connection", what, len(vs))
		}
	}
}

func mapEach[T any](pages []*capture.Page, f func(*capture.Page) T) []T {
	out := make([]T, len(pages))
	for i, p := range pages {
		out[i] = f(p)
	}

	return out
}

// h3Gaps are known differences, by profile and feature (as compareFeatures
// labels it): the feature is not compared. A listed gap that no longer
// appears fails.
var h3Gaps = map[string]string{
	"firefox-158-macos/h3-page HTTP/3 /h3/font.so header list":               "FontFace loads are no request kind of mimicry's (the kinds test skips them too): its Font kind is a stylesheet's @font-face load, which Firefox sends with accept-encoding identity",
	"firefox-158-macos/h3-page HTTP/3 /h3/font.so field lines":               "as its header list",
	"firefox-158-macos/h3-discovery HTTP/3 /h3/markup-image2.so header list": "Firefox gave the second page's only image u=4, i (its kind's u=5, i): a layout-driven image priority, pinned in kindsgen's h3PriorityGaps too",
	"firefox-158-macos/h3-discovery HTTP/3 /h3/markup-image2.so field lines": "as its header list",
	"safari-27-macos/h3-page TCP www.h3-a.test #2 goaway":                    "not the page's traffic: a second TCP connection repeating GET /h3/kinds.so with another user agent (Mac OS X 10_11_1), no referer and sec-fetch-site none, another system client (report http3-2026-10-25, section 4)",
}

// gapsFor returns the check compareFeatures skips known gaps with, and a
// function that fails the test for each of the profile's gaps not seen.
func gapsFor(t *testing.T, name string) (skip func(key string) bool, unused func()) {
	t.Helper()

	var mu sync.Mutex

	seen := map[string]bool{}

	skip = func(key string) bool {
		k := name + " " + key
		if _, ok := h3Gaps[k]; !ok {
			return false
		}

		mu.Lock()

		seen[k] = true
		mu.Unlock()
		t.Logf("known gap: %s: %s", key, h3Gaps[k])

		return true
	}
	unused = func() {
		for k := range h3Gaps {
			if strings.HasPrefix(k, name+" ") && !seen[k] {
				t.Errorf("listed gap %q no longer appears", k)
			}
		}
	}

	return skip, unused
}

// features are categorical features (compared exactly where the
// browser's samples agree) and numbers (within the samples' range).
type features struct {
	cat map[string]string
	num map[string]int
}

func newFeatures() features {
	return features{
		cat: map[string]string{},
		num: map[string]int{},
	}
}

// compareFeatures holds got to refs: a categorical feature every ref has
// alike must be that (absent included); one they differ on is logged
// when got's is none of theirs; a number must lie within the refs'
// range, and be absent where every ref lacks it.
func compareFeatures(t *testing.T, label string, got features, refs []features, skip func(string) bool) {
	t.Helper()

	keys := map[string]bool{}
	for k := range got.cat {
		keys[k] = true
	}

	for _, r := range refs {
		for k := range r.cat {
			keys[k] = true
		}
	}

	for _, k := range slices.Sorted(maps.Keys(keys)) {
		var values []string
		for _, r := range refs {
			v, ok := r.cat[k]
			if !ok {
				v = "<none>"
			}

			if !slices.Contains(values, v) {
				values = append(values, v)
			}
		}

		g, ok := got.cat[k]
		if !ok {
			g = "<none>"
		}

		switch {
		case len(values) == 1 && g != values[0] && !skip(label+" "+k):
			t.Errorf("%s %s:\n%s", label, k, firstDifference(g, values[0]))
		case len(values) > 1 && !slices.Contains(values, g):
			t.Logf("%s %s varies in the browser's samples (%d forms); mimicry's is none of them", label, k, len(values))
		}
	}

	nums := map[string]bool{}
	for k := range got.num {
		nums[k] = true
	}

	for _, r := range refs {
		for k := range r.num {
			nums[k] = true
		}
	}

	for _, k := range slices.Sorted(maps.Keys(nums)) {
		lo, hi, in := 0, 0, 0
		for _, r := range refs {
			v, ok := r.num[k]
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

		g, ok := got.num[k]
		switch {
		case (in == 0 || !ok && in == len(refs) || ok && (g < lo || g > hi)) && skip(label+" "+k):
		case in == 0:
			t.Errorf("%s %s: %d, the browser has none", label, k, g)
		case !ok && in == len(refs):
			t.Errorf("%s %s: none, the browser's %d..%d", label, k, lo, hi)
		case ok && (g < lo || g > hi):
			t.Errorf("%s %s: %d, the browser's %d..%d", label, k, g, lo, hi)
		}
	}
}

// firstDifference shows got and want, or for values of several lines the
// first line that differs.
func firstDifference(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	if len(g) == 1 && len(w) == 1 {
		return " got " + got + "\nwant " + want
	}

	for i := range max(len(g), len(w)) {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}

		if i < len(w) {
			wl = w[i]
		}

		if gl != wl {
			return fmt.Sprintf(" line %d of %d (want %d):\n got %s\nwant %s", i+1, len(g), len(w), gl, wl)
		}
	}

	return ""
}

// tcpFeatures: per host's TCP connection that carried requests, whether
// the client sent GOAWAY on it.
func tcpFeatures(page *capture.Page) features {
	f := newFeatures()
	seen := map[string]int{}

	for _, tr := range page.H2 {
		if len(tr.Requests) == 0 {
			continue
		}

		n := seen[tr.Host]
		seen[tr.Host]++

		goaway := "no"

		for _, fr := range tr.Frames {
			if fr.Type == "GOAWAY" {
				goaway = fr.ErrCode
			}
		}

		f.cat[fmt.Sprintf("%s #%d goaway", tr.Host, n+1)] = goaway
	}

	return f
}

// quicWithTCP is how soon after a host's first TCP ClientHello its first
// QUIC connection must start to count as started with it: Firefox's (on
// the response carrying Alt-Svc, 3–85 ms) and Chrome's to the page's host
// (beside the first request after it, 28–45 ms) do; one started by a
// request after a script's pause (1 s or more) does not.
const quicWithTCP = 500 * time.Millisecond

// quicHosts: how many QUIC connections each host had, and whether the
// first started with the host's first TCP connection (quicWithTCP).
func quicHosts(page *capture.Page) features {
	f := newFeatures()
	n := map[string]int{}

	for _, c := range page.QUIC {
		if n[c.Host] == 0 {
			tcp := int64(-1)
			for _, tc := range page.Connections {
				if tc.Host == c.Host && (tcp < 0 || tc.Millis < tcp) {
					tcp = tc.Millis
				}
			}

			f.cat[c.Host+" QUIC #1 starts with its TCP connection"] = strconv.FormatBool(tcp >= 0 && time.Duration(c.Millis-tcp)*time.Millisecond <= quicWithTCP)
		}

		n[c.Host]++
	}

	for h, k := range n {
		f.cat[h+" connections"] = strconv.Itoa(k)
	}

	return f
}

// capturedConn is a QUIC connection of a browser's capture, with where it
// is.
type capturedConn struct {
	at string
	c  *quictrace.Conn
}

// capturedConns are every QUIC connection of the h3 captures p is checked
// against, unforced and -h3-local-roots, failed handshakes included,
// rewritten to p's version where inherited.
func capturedConns(t *testing.T, p *profile.Profile) []capturedConn {
	t.Helper()

	var out []capturedConn
	for _, sc := range []string{"h3-discovery", "h3-page"} {
		for _, cond := range []string{"", ".local-roots"} {
			if path, _, _ := capturePath(t, p, sc, cond+".pagetrace.json.gz"); !exists(path) {
				continue
			}

			pages, err := loadPages(t, p, sc, cond+".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}

			for i, p := range pages {
				for j, c := range p.QUIC {
					out = append(out, capturedConn{
						at: fmt.Sprintf("%s%s sample %d connection %d (%s)", sc, cond, i+1, j+1, c.Host),
						c:  c,
					})
				}
			}
		}
	}

	return out
}

// flightRefs are every QUIC connection of the profile's h3 captures, by
// whether it resumed: what the first flight is held to.
func flightRefs(t *testing.T, p *profile.Profile) map[bool][]*quictrace.Conn {
	t.Helper()

	out := map[bool][]*quictrace.Conn{}
	for _, c := range capturedConns(t, p) {
		out[c.c.Resumed] = append(out[c.c.Resumed], c.c)
	}

	return out
}

// compareQUICConn compares one of mimicry's QUIC connections with the
// browser's at the same place (refs), its first flight with flights, and
// holds it to the profile's rules.
func compareQUICConn(t *testing.T, p *profile.Profile, got *quictrace.Conn, refs, flights []*quictrace.Conn, skip func(string) bool) {
	t.Helper()

	for _, v := range quicRuleViolations(p, got, t.Logf) {
		t.Errorf("rule: %s", v)
	}

	if len(flights) == 0 {
		t.Errorf("the browser has no connection resumed %v", got.Resumed)
	} else {
		compareFeatures(t, "first flight", flightFeatures(got), mapConns(flights, flightFeatures), skip)
		compareFeatures(t, "transport parameters", paramFeatures(got), mapConns(flights, paramFeatures), skip)
		compareFeatures(t, "packets", packetFeatures(got), mapConns(flights, packetFeatures), skip)
		compareHello(t, p, got, flights)
	}

	compareFeatures(t, "connection", connFeatures(got), mapConns(refs, connFeatures), skip)

	if got.H3 == nil {
		if refs[0].H3 != nil {
			t.Error("no HTTP/3: the handshake did not complete")
		}

		return
	}

	var h3refs []*quictrace.Conn
	for _, r := range refs {
		if r.H3 != nil {
			h3refs = append(h3refs, r)
		}
	}

	compareFeatures(t, "HTTP/3", h3Features(got), mapConns(h3refs, h3Features), skip)
}

func mapConns(conns []*quictrace.Conn, f func(*quictrace.Conn) features) []features {
	out := make([]features, len(conns))
	for i, c := range conns {
		out[i] = f(c)
	}

	return out
}

// firstFlight is the client's datagrams before it heard from the server or
// its first PTO expired. A trace has no timers, so the PTO shows as the first
// probe, which brings nothing new (RFC 9002, 6.2.4): the flight ends at the
// first datagram with an ACK, with data already sent (sentData), or with only
// PINGs and PADDING. A loaded machine's late server leaves mimicry's probes
// out, as it would the browser's.
func firstFlight(c *quictrace.Conn) []quictrace.Datagram {
	var sent sentData
	for i := range c.Datagrams {
		d := &c.Datagrams[i]
		pings, fresh := false, false

		for pi := range d.Packets {
			pk := &d.Packets[pi]
			for fi := range pk.Frames {
				f := &pk.Frames[fi]
				switch {
				case f.Type == quictrace.FrameAck, sent.repeats(pk, f):
					return c.Datagrams[:i]
				case f.Type == quictrace.FramePing:
					pings = true
				case f.Type != quictrace.FramePadding:
					fresh = true
				}
			}
		}

		if pings && !fresh {
			return c.Datagrams[:i]
		}

		for pi := range d.Packets {
			sent.addPacket(&d.Packets[pi])
		}
	}

	return c.Datagrams
}

// sentData is what a client's frames carried so far, to tell data sent
// again (a probe's, or loss recovery's) from new: CRYPTO and STREAM bytes,
// by packet number space and stream, and NEW_CONNECTION_ID sequence
// numbers.
type sentData struct {
	spans []sentSpan
	ncids map[uint64]bool
}

// sentSpan is the bytes [start, end) of a CRYPTO frame's packet number
// space or of a STREAM frame's stream.
type sentSpan struct {
	space      string
	stream     uint64
	start, end uint64
}

// frameSpan is the bytes f carries in pk; false for a frame without.
func frameSpan(pk *quictrace.Packet, f *quictrace.Frame) (sentSpan, bool) {
	switch {
	case f.Type == quictrace.FrameCrypto:
		return sentSpan{space: pnSpace(pk.Type), start: f.Offset, end: f.Offset + f.Length}, true
	case f.Type == quictrace.FrameStream && f.Stream != nil:
		return sentSpan{stream: *f.Stream, start: f.Offset, end: f.Offset + f.Length}, true
	}

	return sentSpan{}, false
}

// repeats reports whether f, in pk, carries something already sent.
func (s *sentData) repeats(pk *quictrace.Packet, f *quictrace.Frame) bool {
	if f.Type == quictrace.FrameNewConnectionID {
		return s.ncids[f.Sequence]
	}

	x, ok := frameSpan(pk, f)

	return ok && slices.ContainsFunc(s.spans, func(y sentSpan) bool {
		return y.space == x.space && y.stream == x.stream && y.start < x.end && x.start < y.end
	})
}

// addPacket records what pk's frames carry.
func (s *sentData) addPacket(pk *quictrace.Packet) {
	for i := range pk.Frames {
		f := &pk.Frames[i]
		if f.Type == quictrace.FrameNewConnectionID {
			if s.ncids == nil {
				s.ncids = map[uint64]bool{}
			}

			s.ncids[f.Sequence] = true
		} else if x, ok := frameSpan(pk, f); ok {
			s.spans = append(s.spans, x)
		}
	}
}

// flightFeatures describe the datagrams of the first flight that carry
// Initial packets, and those packets' headers. Their packet numbers, DCID
// lengths and how the ClientHello is cut into them (with the byte a cut
// can leave unused, cryptoLeftover) are random per connection in some
// browsers: quicRuleViolations checks those.
func flightFeatures(c *quictrace.Conn) features {
	f := newFeatures()
	initials := 0
	dgrams := 0

	for _, d := range firstFlight(c) {
		types := make([]string, 0, len(d.Packets))
		hasInitial := false

		for _, pk := range d.Packets {
			types = append(types, pk.Type)
			hasInitial = hasInitial || pk.Type == quictrace.PacketInitial
		}

		if !hasInitial {
			continue
		}

		dp := fmt.Sprintf("datagram %d", dgrams+1)
		dgrams++

		f.num[dp+" size"] = d.Size
		f.cat[dp+" packets"] = strings.Join(types, "+")
		f.cat[dp+" trailing padding"] = strconv.FormatBool(d.Trailing > 0 && !cryptoLeftover(&d))

		for _, pk := range d.Packets {
			if pk.Type != quictrace.PacketInitial {
				continue
			}

			ip := fmt.Sprintf("Initial %d", initials+1)
			initials++
			// The low bits are the packet number's length.
			f.cat[ip+" first byte"] = fmt.Sprintf("0x%02x", pk.FirstByte&^0x03)
			if pk.SCIDLength != nil {
				f.cat[ip+" scid length"] = strconv.Itoa(*pk.SCIDLength)
			}

			if pk.TokenLength != nil {
				f.num[ip+" token length"] = *pk.TokenLength
			}
		}
	}

	f.num["datagrams with Initials"] = dgrams

	return f
}

// paramFeatures describe the transport parameters: which, with what
// values, how many GREASE ones. Their order, GREASE lengths,
// version_information's order and initial_rtt's value are random per
// connection in some browsers: quicRuleViolations checks those.
func paramFeatures(c *quictrace.Conn) features {
	f := newFeatures()

	var ids []string

	grease := 0

	for _, tp := range c.TransportParameters {
		if tp.Grease {
			grease++
			continue
		}

		id := fmt.Sprintf("0x%x", tp.ID)

		ids = append(ids, id)

		v := tp.Hex

		switch {
		case tp.ID == uint64(profile.TPInitialRTT):
			v = "the cached RTT"
		case tp.Value != nil:
			v = strconv.FormatUint(*tp.Value, 10)
		case tp.Versions != nil:
			rest := slices.Clone(tp.Versions[1:])
			for i, ver := range rest {
				if n, err := strconv.ParseUint(ver, 16, 32); err == nil && profile.IsGREASEVersion(uint32(n)) {
					rest[i] = "G"
				}
			}

			slices.Sort(rest)

			v = tp.Versions[0] + " " + strings.Join(rest, ",")

		case tp.Name == "initial_source_connection_id":
			v = "length " + strconv.Itoa(tp.Length)
		}

		f.cat[id] = v
	}

	slices.Sort(ids)

	f.cat["set"] = strings.Join(ids, " ")
	f.num["GREASE parameters"] = grease

	return f
}

// packetFeatures describe the packets after the first flight: frame
// types sent at all and credit values. ACK delays and the spin bit are
// random per connection or timing, and so is whether PADDING is sent at
// all (where a datagram's padding goes): quicRuleViolations checks those.
func packetFeatures(c *quictrace.Conn) features {
	f := newFeatures()
	types := map[string]bool{}

	for _, d := range c.Datagrams {
		for _, pk := range d.Packets {
			for _, fr := range pk.Frames {
				types[fr.Type] = true
				switch fr.Type {
				case quictrace.FrameMaxData, quictrace.FrameMaxStreamData, quictrace.FrameMaxStreams:
					// Credit: only values the browser sent.
					f.cat[fmt.Sprintf("sends %s %d", fr.Type, fr.Max)] = "yes"
				}
			}
		}
	}

	delete(types, quictrace.FramePadding)

	for typ := range types {
		f.cat["sends "+typ] = "yes"
	}

	return f
}

// connFeatures describe a connection against the browser's at its place:
// 0-RTT, its 0-RTT packets' frames, NEW_CONNECTION_IDs and how it ended.
// Data sent again counts once (sentData), and a 0-RTT probe with nothing new
// not at all.
func connFeatures(c *quictrace.Conn) features {
	f := newFeatures()

	f.cat["resumed"] = strconv.FormatBool(c.Resumed)
	f.cat["0-RTT"] = strconv.FormatBool(c.EarlyData)

	var early []string

	ncids := 0
	closes := "none"

	var sent sentData

	for di := range c.Datagrams {
		for pi := range c.Datagrams[di].Packets {
			pk := &c.Datagrams[di].Packets[pi]

			var frames []string

			fresh := len(pk.Frames) == 0

			for fi := range pk.Frames {
				fr := &pk.Frames[fi]
				repeat := sent.repeats(pk, fr)

				switch fr.Type {
				case quictrace.FrameNewConnectionID:
					if !repeat {
						ncids++
					}

				case quictrace.FrameConnectionClose:
					closes = fmt.Sprintf("0x%x frame 0x%x", derefCode(fr.ErrorCode), fr.FrameType)
				}

				fresh = fresh || !repeat && fr.Type != quictrace.FramePadding && fr.Type != quictrace.FramePing
				if pk.Type == quictrace.Packet0RTT && fr.Type != quictrace.FramePadding {
					s := fr.Type
					if fr.Stream != nil {
						s += fmt.Sprintf("(%d)", *fr.Stream)
					}

					frames = append(frames, s)
				}
			}

			sent.addPacket(pk)

			if pk.Type == quictrace.Packet0RTT && fresh {
				early = append(early, strings.Join(frames, " "))
			}
		}
	}

	f.cat["0-RTT packets"] = strings.Join(early, " | ")
	f.num["NEW_CONNECTION_IDs"] = ncids
	f.cat["client CONNECTION_CLOSE"] = closes

	if c.Close != nil {
		f.cat["ended by"] = c.Close.By
	}

	return f
}

func derefCode(c *uint64) uint64 {
	if c == nil {
		return 0
	}

	return *c
}

// compareHello compares the connection's ClientHello with the browser's
// (diff.Compare as the profile's QUIC hello orders extensions; the
// transport parameters are compared apart): it must have no differences
// from at least one of them, and reuse its X25519 key across key shares
// as theirs with both shares do (read from the hellos' bytes).
func compareHello(t *testing.T, p *profile.Profile, got *quictrace.Conn, flights []*quictrace.Conn) {
	t.Helper()

	if got.Hello == nil {
		t.Errorf("ClientHello not parsed: %s", got.HelloError)

		return
	}

	if reused, both := x25519Reuse(got.ClientHelloBytes()); both {
		for _, w := range flights {
			if r, wb := x25519Reuse(w.ClientHelloBytes()); wb && r != reused {
				t.Errorf("ClientHello reuses the X25519 key across key shares: %v, the browser's %v", reused, r)

				break
			}
		}
	}

	g := profile.Profile{
		TLS: helloWithoutParams(got.Hello, p),
	}

	var first []diff.Difference

	for _, w := range flights {
		if w.Hello == nil {
			continue
		}

		want := profile.Profile{
			TLS: helloWithoutParams(w.Hello, p),
		}
		d := diff.Compare(&want, &g)

		if len(d) == 0 {
			return
		}

		if first == nil {
			first = d
		}
	}

	for _, d := range first {
		t.Errorf("ClientHello: %s", d)
	}
}

// helloWithoutParams is h with quic_transport_parameters' body and GREASE
// ECH's cipher suite and payload length left out (picked per connection;
// quicRuleViolations holds them to the profile's), and the extension
// order the profile's QUIC hello has.
func helloWithoutParams(h *profile.TLS, p *profile.Profile) profile.TLS {
	out := *h

	out.Extensions = slices.Clone(h.Extensions)

	for i := range out.Extensions {
		e := &out.Extensions[i]
		switch {
		case e.Type == profile.ExtQUICTransportParameters:
			*e = profile.Extension{
				Type: profile.ExtQUICTransportParameters,
			}

		case e.ECH != nil:
			ech := *e.ECH

			ech.CipherSuites, ech.PayloadLengths = nil, nil
			e.ECH = &ech
		}
	}

	out.ExtensionOrder = p.TLS.QUIC.ExtensionOrder

	return out
}

// testPorts normalises the capture ports in values.
func normalisePort(s string) string {
	return testPort.ReplaceAllString(s, ".test:PORT")
}

// h3Features describe the HTTP/3 layer: unidirectional streams,
// SETTINGS, control stream frames, QPACK instructions, and per request
// its field lines, frames, PRIORITY_UPDATE and end.
func h3Features(c *quictrace.Conn) features {
	f := newFeatures()
	uni := make([]string, 0, len(c.H3.Streams))

	var control []string

	priority := map[uint64]string{}

	for _, s := range c.H3.Streams {
		uni = append(uni, fmt.Sprintf("%d:%s", s.ID, s.Name))
		switch s.Type {
		case quictrace.StreamControl:
			for _, fr := range s.Frames {
				name := fr.Name
				switch {
				case fr.Grease:
					name = "GREASE"
				case fr.Type == quictrace.FrameH3PriorityUpdateRequest:
					if fr.ID != nil {
						priority[*fr.ID] = fr.Priority
					}

					continue
				}

				control = append(control, name)

				var settings []string

				greaseSettings := 0

				for _, st := range fr.Settings {
					if st.Grease {
						greaseSettings++
						continue
					}

					settings = append(settings, fmt.Sprintf("0x%x=%d", st.ID, st.Value))
				}

				if fr.Type == quictrace.FrameH3Settings {
					f.cat["SETTINGS"] = strings.Join(settings, " ")
					f.cat["GREASE settings"] = strconv.Itoa(greaseSettings)
				}
			}

		case quictrace.StreamQPACKEncoder:
			var ins []string
			for _, in := range s.Encoder {
				ins = append(ins, normalisePort(fmt.Sprintf("%+v", in)))
			}

			f.cat["encoder stream"] = strings.Join(ins, "\n")

		case quictrace.StreamQPACKDecoder:
			var ins []string
			for _, in := range s.Decoder {
				ins = append(ins, fmt.Sprintf("%+v", in))
			}

			f.cat["decoder stream"] = strings.Join(ins, " ")
		}
	}

	f.cat["unidirectional streams"] = strings.Join(uni, " ")
	f.cat["control stream"] = strings.Join(control, " ")

	paths := make([]string, 0, len(c.H3.Requests))

	for _, r := range c.H3.Requests {
		paths = append(paths, r.Path)

		rp := r.Path + " "

		var lines, reps []string

		for _, fl := range r.Section.Fields {
			lines = append(lines, normalisePort(fl.Name+": "+fl.Value))
			reps = append(reps, normalisePort(fmt.Sprintf("%s static=%v index=%d %s: %s", fl.Rep, fl.Static, fl.Index, fl.Name, fl.Value)))
		}

		f.cat[rp+"header list"] = strings.Join(lines, "\n")
		f.cat[rp+"field lines"] = strings.Join(reps, "\n")
		f.cat[rp+"section prefix"] = fmt.Sprintf("ric=%d sign=%v delta=%d", r.Section.RequiredInsertCount, r.Section.Sign, r.Section.DeltaBase)

		var frames []string

		for _, fr := range r.Frames {
			s := fr.Name
			if fr.Type == quictrace.FrameH3Data {
				s += fmt.Sprintf("(%d)", fr.Length)
			}

			frames = append(frames, s)
		}

		f.cat[rp+"frames"] = strings.Join(frames, " ")
		f.cat[rp+"PRIORITY_UPDATE"] = priority[r.Stream]
		f.cat[rp+"end"] = r.End
	}

	f.cat["requests"] = strings.Join(paths, " ")

	return f
}

// Replaying a browser's HTTP/3 requests through the profile's http3.qpack
// encoder reproduces every field section and encoder instruction byte for
// byte, on every connection of every h3 capture.
func TestQPACKReproducesBrowserSections(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			if p.HTTP3 == nil {
				t.Fatal("the profile has no http3 section")
			}

			for _, sc := range []*server.Scenario{server.H3Discovery, server.H3Page} {
				for _, cond := range []string{"", ".local-roots"} {
					// As captured: the encoded bytes are compared.
					path, _, _ := capturePath(t, p, sc.Name, cond+".pagetrace.json.gz")
					if !exists(path) {
						continue
					}

					pages, err := capture.LoadPages(path)
					if err != nil {
						t.Fatal(err)
					}

					for i, page := range pages {
						for j, c := range page.QUIC {
							if c.H3 == nil || len(c.H3.Requests) == 0 {
								continue
							}

							t.Run(fmt.Sprintf("%s%s sample %d connection %d", sc.Name, cond, i+1, j+1), func(t *testing.T) {
								reencode(t, p.HTTP3, sc, c)
							})
						}
					}
				}
			}
		})
	}
}

// reencode encodes c's requests' field lists in stream order and checks
// each section and the encoder stream against the browser's.
func reencode(t *testing.T, h *profile.HTTP3, sc *server.Scenario, c *quictrace.Conn) {
	t.Helper()

	enc := qpack.NewEncoder(h3.QPACKConfig(h))
	capacity, blocked := uint64(0), uint64(0)

	for _, st := range sc.H3Settings() {
		switch st[0] {
		case quictrace.SettingQPACKMaxTableCapacity:
			capacity = st[1]
		case quictrace.SettingQPACKBlockedStreams:
			blocked = st[1]
		}
	}

	enc.SetPeerSettings(capacity, blocked)

	var want []quictrace.EncoderInstruction
	for _, s := range c.H3.Streams {
		want = append(want, s.Encoder...)
	}

	decoded := quictrace.NewTable(capacity)

	var got []quictrace.EncoderInstruction

	reqs := slices.Clone(c.H3.Requests)
	slices.SortFunc(reqs, func(a, b quictrace.H3Request) int {
		return int(a.Stream) - int(b.Stream)
	})

	for _, r := range reqs {
		fields := make([]qpack.Field, 0, len(r.Section.Fields))
		for _, fl := range r.Section.Fields {
			fields = append(fields, qpack.Field{
				Name:  fl.Name,
				Value: fl.Value,
			})
		}

		ins, block := enc.Encode(r.Stream, fields)
		read, n, err := decoded.ReadEncoderStream(ins)

		if err != nil || n != len(ins) {
			t.Fatalf("%s: encoder stream unreadable (%d of %d bytes): %v", r.Path, n, len(ins), err)
		}

		got = append(got, read...)

		if hex.EncodeToString(block) != r.Block {
			sec, err := decoded.DecodeSection(block)
			t.Fatalf("%s: section differs (%v)\n got %+v\nwant %+v", r.Path, err, sec, r.Section)
		}

		if r.Section.RequiredInsertCount > 0 {
			if err := enc.OnDecoderStream(qpack.AppendSectionAck(nil, r.Stream)); err != nil {
				t.Fatal(err)
			}
		}
	}

	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = fmt.Sprintf("%+v", got[i])
		}

		if i < len(want) {
			w = fmt.Sprintf("%+v", want[i])
		}

		if g != w {
			t.Fatalf("encoder instruction %d:\n got %s\nwant %s", i+1, g, w)
		}
	}
}
