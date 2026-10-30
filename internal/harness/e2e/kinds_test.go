package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// Every request of the kinds page, made through mimicry as its kind, is the
// browser's: header lines, HTTP/2 weight, redirects, cookies, and CORS
// preflights exactly where the browser sent them. FontFace loads are left out.
func TestKindsMatchBrowserTraces(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			if p.Kinds == nil {
				t.Fatalf("%s describes no request kinds", name)
			}

			for _, sc := range []*server.Scenario{server.Kinds, server.KindsHTTP1, server.KindsPlain} {
				t.Run(sc.Name, func(t *testing.T) {
					want, err := loadPages(t, p, sc.Name, ".pagetrace.json.gz")
					if err != nil {
						t.Fatal(err)
					}

					got := runKindsPage(t, p, sc)
					h1 := sc != server.Kinds

					var all []string

					for _, step := range kindsRequests() {
						labels := []string{step.label}
						if step.redirect {
							labels = append(labels, step.label+".hop")
						}

						all = append(all, labels...)
						for _, label := range labels {
							for _, preflight := range []bool{false, true} {
								compareKind(t, got, want, label, h1, preflight)
							}
						}
					}

					if !h1 {
						checkConnections(t, capture.Source(p, sc.Name), got, want, all)
					}

					if sc.Plain {
						// The page itself: a typed navigation to a non-secure
						// origin, line for line.
						g := pageNavigation(got)
						if !slices.ContainsFunc(want, func(w *capture.Page) bool {
							return slices.Equal(pageNavigation(w), g)
						}) {
							t.Errorf("navigation:\n got %q\nwant %q", g, pageNavigation(want[0]))
						}
					}
				})
			}
		})
	}
}

// compareKind compares mimicry's request for label (or its preflight)
// with the browser's forms of it.
func compareKind(t *testing.T, got *capture.Page, want []*capture.Page, label string, h1, preflight bool) {
	t.Helper()
	comparePath(t, got, want, "/k/"+label, raceCookieLabels[label], h1, preflight)
}

// comparePath compares mimicry's first request for path (or its
// preflight) with the browser's forms of it: header lines and HTTP/2
// weight. race: the browser's samples were captured without raceCookie.
func comparePath(t *testing.T, got *capture.Page, want []*capture.Page, path string, race, h1, preflight bool) {
	t.Helper()

	g := requestFields(got, path, h1, preflight)
	name := path

	if preflight {
		name += " preflight"
		if browser := requestFields(want[0], path, h1, true) != nil; browser != (g != nil) {
			t.Errorf("%s: mimicry sent one %v, the browser %v", name, g != nil, browser)

			return
		}

		if g == nil {
			return
		}
	}

	if g == nil {
		t.Errorf("%s: mimicry sent no request", name)

		return
	}

	forms := browserForms(t, want, path, h1, preflight)
	same := func(w *kindsSent) bool {
		return slices.Equal(g.lines, w.lines) && g.weight == w.weight
	}

	if slices.ContainsFunc(forms, same) {
		return
	}

	// kb_part is set by a response during the page, so a browser request
	// racing it may go without; mimicry's, made in order, never does. Only
	// where the race was captured, and only in that direction.
	if race && hasRaceCookie(g.lines) && slices.ContainsFunc(forms, func(w *kindsSent) bool {
		return !hasRaceCookie(w.lines) && slices.Equal(withoutRaceCookie(g.lines), w.lines) && g.weight == w.weight
	}) {
		t.Logf("%s: matches but for %s, which the browser had not yet received", name, raceCookie)

		return
	}

	t.Errorf("%s:\n got %q weight %d\nwant %q weight %d", name, g.lines, g.weight, forms[0].lines, forms[0].weight)
}

// pageNavigation is the first HTTP/1.1 GET of a page's own document, its
// lines with ports replaced.
func pageNavigation(page *capture.Page) []string {
	for _, r := range page.H1 {
		if strings.HasPrefix(r.Line, "GET / ") {
			return portless(r.Headers)
		}
	}

	return nil
}

// raceCookie is the kinds page's cookie set during the page itself.
const raceCookie = "kb_part=1"

// raceCookieLabels are the labels whose browser samples were captured
// without raceCookie (Safari 26's font-css.cs over HTTP/2, requested before
// the response setting it had been processed).
var raceCookieLabels = map[string]bool{
	"font-css.cs": true,
}

// hasRaceCookie reports whether a cookie line carries raceCookie.
func hasRaceCookie(lines []string) bool {
	return slices.ContainsFunc(lines, func(l string) bool {
		name, value, ok := strings.Cut(l, ": ")
		return ok && strings.EqualFold(name, "cookie") && slices.Contains(strings.Split(value, "; "), raceCookie)
	})
}

// withoutRaceCookie drops raceCookie from cookie lines (one per line over
// HTTP/2, joined by "; " over HTTP/1.1), and lines left empty.
func withoutRaceCookie(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		name, value, ok := strings.Cut(l, ": ")
		if !ok || !strings.EqualFold(name, "cookie") {
			out = append(out, l)
			continue
		}

		crumbs := slices.DeleteFunc(strings.Split(value, "; "), func(c string) bool {
			return c == raceCookie
		})
		if len(crumbs) > 0 {
			out = append(out, name+": "+strings.Join(crumbs, "; "))
		}
	}

	return out
}

// partitionGaps are the known, unmodelled connection differences on the
// kinds page, label to "mimicry with X, browser with Y". Firefox gives
// credential-less requests their own connection to the same-site host only;
// no rule found.
var partitionGaps = map[string]map[string]string{
	"firefox-158-macos": {
		"script-cors.ss":  "mimicry with script.ss, browser with script-cors.ss",
		"image-cors.ss":   "mimicry with script.ss, browser with script-cors.ss",
		"fetch.ss":        "mimicry with script.ss, browser with script-cors.ss",
		"fetch-omit.ss":   "mimicry with script.ss, browser with script-cors.ss",
		"fetch-nocors.ss": "mimicry with script.ss, browser with script-cors.ss",
		"fetch-json.ss":   "mimicry with script.ss, browser with script-cors.ss",
		"fetch-custom.ss": "mimicry with script.ss, browser with script-cors.ss",
		"fetch-high.ss":   "mimicry with script.ss, browser with script-cors.ss",
		"fetch-low.ss":    "mimicry with script.ss, browser with script-cors.ss",
		"xhr.ss":          "mimicry with script.ss, browser with script-cors.ss",
	},
}

// checkConnections compares which requests share a connection: for each
// request, the first request on its connection, in mimicry's page and in
// every browser sample (HTTP/2, where a browser keeps one connection per
// host and partition). source names the profile that took want, whose
// partitionGaps apply.
func checkConnections(t *testing.T, source string, got *capture.Page, want []*capture.Page, labels []string) {
	t.Helper()

	g := connGroups(got, labels)
	known := partitionGaps[source]

	for i, page := range want {
		w := connGroups(page, labels)
		diffs := map[string]string{}

		for _, l := range labels {
			if g[l] != w[l] {
				diffs[l] = "mimicry with " + g[l] + ", browser with " + w[l]
			}
		}

		for _, l := range labels {
			d, isDiff := diffs[l]
			k, isKnown := known[l]

			switch {
			case isDiff && d != k:
				t.Errorf("connections, sample %d: %s: %s", i+1, l, d)
			case !isDiff && isKnown:
				t.Errorf("connections, sample %d: %s: known gap %q no longer seen", i+1, l, k)
			case isDiff:
				t.Logf("connections, sample %d: %s: known gap: %s", i+1, l, d)
			}
		}
	}
}

// connGroups maps each label to the first of labels sent on the same
// HTTP/2 connection.
func connGroups(page *capture.Page, labels []string) map[string]string {
	conn := map[string]int{}
	for ci, tr := range page.H2 {
		for _, r := range tr.Requests {
			label := strings.TrimPrefix(r.Path, "/k/")
			if _, seen := conn[label]; !seen {
				conn[label] = ci
			}
		}
	}

	first := map[int]string{}
	out := map[string]string{}

	for _, l := range labels {
		c, ok := conn[l]
		if !ok {
			continue
		}

		if _, ok := first[c]; !ok {
			first[c] = l
		}

		out[l] = first[c]
	}

	return out
}

// kindsRequest is one request of the kinds page as a mimicry caller makes
// it: the kind, and what the page's own code set.
type kindsRequest struct {
	label   string
	method  string
	req     mimicry.Request
	headers http.Header
	body    string
	// redirect: the server redirects it, and the client follows.
	redirect bool
}

// kindsRequests maps the kinds page's markup and steps to mimicry
// requests, in the page's order, ending with its form POST.
func kindsRequests() []kindsRequest {
	markup := map[string]mimicry.Kind{
		"style-head":      mimicry.Style,
		"style-fonts":     mimicry.Style,
		"preload-script":  mimicry.PreloadScript,
		"preload-font":    mimicry.PreloadFont,
		"script-blocking": mimicry.Script,
		"script-defer":    mimicry.DeferScript,
		"script-async":    mimicry.AsyncScript,
		"image-markup":    mimicry.Image,
		"font-css":        mimicry.Font,
	}
	scripted := map[string]mimicry.Kind{
		"script": mimicry.InjectedScript,
		"style":  mimicry.Style,
		"image":  mimicry.InjectedImage,
		"iframe": mimicry.Frame,
		"fetch":  mimicry.Fetch,
		"xhr":    mimicry.XHR,
		"beacon": mimicry.Beacon,
	}

	var out, fonts []kindsRequest

	for _, s := range server.KindsMarkup() {
		base, _, _ := strings.Cut(s.Label, ".")
		r := kindsRequest{
			label:  s.Label,
			method: http.MethodGet,
			req: mimicry.Request{
				Kind: markup[base],
			},
		}

		if s.Tag == "" {
			fonts = append(fonts, r)
			continue
		}

		out = append(out, r)
	}

	for _, s := range server.KindsSteps() {
		kind, ok := scripted[s.Kind]
		if !ok {
			continue // FontFace loads
		}

		r := kindsRequest{
			label:  s.Label,
			method: http.MethodGet,
			req: mimicry.Request{
				Kind:        kind,
				CrossOrigin: map[string]mimicry.CrossOrigin{"": mimicry.CrossOriginNone, "anonymous": mimicry.CrossOriginAnonymous, "use-credentials": mimicry.CrossOriginUseCredentials}[s.CrossOrigin],
				Credentials: map[string]mimicry.Credentials{"": mimicry.CredentialsSameOrigin, "include": mimicry.CredentialsInclude, "omit": mimicry.CredentialsOmit}[s.Credentials],
				NoCORS:      s.Mode == "no-cors",
				Priority:    map[string]mimicry.Priority{"": mimicry.PriorityAuto, "high": mimicry.PriorityHigh, "low": mimicry.PriorityLow}[s.Priority],
			},
			headers:  http.Header{},
			body:     s.Body,
			redirect: s.RedirectTo != "",
		}
		if s.Method != "" {
			r.method = s.Method
		}

		for k, v := range s.Headers {
			// As the page spelled it: browsers keep a page's spelling.
			r.headers[k] = []string{v}
		}

		if s.Kind == "beacon" {
			// sendBeacon's string body: the browser sets the type.
			r.method = http.MethodPost
			r.headers.Set("Content-Type", "text/plain;charset=UTF-8")
		}

		out = append(out, r)
		if len(fonts) > 0 {
			// The CSS fonts load when their text renders: in most browser
			// samples just after the page's first step, which sets the
			// third-party cookies the cross-site font then carries.
			out = append(out, fonts...)
			fonts = nil
		}
	}

	return append(out, kindsRequest{
		label:  "form-post.so",
		method: http.MethodPost,
		req: mimicry.Request{
			Kind: mimicry.Navigate,
		},
		// The page's script submits it: no user activation, so no
		// Sec-Fetch-User (the variant's slot is for a clicked form).
		headers: http.Header{
			"Content-Type":   {"application/x-www-form-urlencoded"},
			"Sec-Fetch-User": nil,
		},
		body: "a=1",
	})
}

// runKindsPage serves sc and makes the kinds page's requests through
// mimicry as profile p, from the page, and returns the server's capture.
func runKindsPage(t *testing.T, p *profile.Profile, sc *server.Scenario) *capture.Page {
	t.Helper()

	s := startPage(t, p, sc)
	s.planEvery()

	hosts, _, _ := sc.MultiHost()
	page := s.url(hosts[0], "/")
	s.send(http.MethodGet, page, mimicry.Request{}, nil, "")

	from, err := url.Parse(page)
	if err != nil {
		t.Fatal(err)
	}

	steps := map[string]server.KindsStep{}
	for _, st := range slices.Concat(server.KindsMarkup(), server.KindsSteps()) {
		steps[st.Label] = st
	}

	for _, r := range kindsRequests() {
		host := hosts[0]
		if st, ok := steps[r.label]; ok {
			host = st.Host
		}

		r.req.From = from
		if strings.HasPrefix(r.label, "font-css.") {
			// The stylesheet that declares the font loads it.
			r.req.Stylesheet = from.JoinPath("k", "style-fonts.so")
		}

		s.send(r.method, s.url(host, "/k/"+r.label), r.req, r.headers, r.body)
	}

	return s.finish(s.url(hosts[0], "/done"))
}

// pageSession is a multi-host scenario's server and a mimicry client for a
// profile reaching every host on it, with the profile's cookie jar.
// plainPort is the scenario's plain-HTTP port, if it has one
// (Scenario.PlainPort), which the client reaches by URL port.
type pageSession struct {
	t         *testing.T
	client    *http.Client
	port      string
	plainPort string
	scheme    string
	captures  chan *capture.Capture
	// quicReady is closed once an aioquic scenario's server serves QUIC
	// (server.Server.OnQUICReady); nil for other scenarios.
	quicReady chan struct{}
	// planning plans every request sent, once planEvery is called.
	planning *planning
}

func startPage(t *testing.T, p *profile.Profile, sc *server.Scenario) *pageSession {
	t.Helper()

	return startPageWith(t, p, sc, nil)
}

// startPageWith is startPage with the client's Config changed by config,
// when not nil.
func startPageWith(t *testing.T, p *profile.Profile, sc *server.Scenario, config func(*mimicry.Config)) *pageSession {
	t.Helper()

	ca, err := server.LoadOrCreateCA(t.TempDir(), server.CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	ln, pc := listenTwin(t, sc.HTTP3())
	s := &pageSession{
		t:        t,
		scheme:   "https",
		captures: make(chan *capture.Capture, 64),
	}

	if sc.Plain {
		s.scheme = "http"
	}

	ctx, cancel := context.WithCancel(t.Context())
	srv := &server.Server{
		Scenario:  sc,
		TLSConfig: &tls.Config{},
		IssueCert: func(hosts []string) (tls.Certificate, error) {
			return ca.Issue(hosts, time.Now())
		},
		OnCapture: func(c *capture.Capture) {
			s.captures <- c
		},
		Logf: t.Logf,
	}

	if sc.PlainPort() {
		if srv.PlainListener, err = (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}

		_, s.plainPort, _ = net.SplitHostPort(srv.PlainListener.Addr().String())
	}

	if pc != nil {
		// QUIC on the TLS port's UDP twin, which the Alt-Svc names.
		srv.PacketConn = pc
	}

	if sc.NeedsAioquic() {
		ready := make(chan struct{})

		s.quicReady = ready
		srv.OnQUICReady = func() {
			close(ready)
		}
	}

	done := make(chan error, 1)

	go func() {
		done <- srv.Serve(ctx, ln)
	}()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)

	cfg := mimicry.Config{
		Profile: p,
		RootCAs: pool,
		Cookies: newJar(t, p),
		// Every capture host is this server, and resolves to its address,
		// as on the capture machines; the plain port is its second
		// listener.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			to := ln.Addr().String()
			if _, port, err := net.SplitHostPort(addr); err == nil && s.plainPort != "" && port == s.plainPort {
				to = srv.PlainListener.Addr().String()
			}

			return (&net.Dialer{}).DialContext(ctx, network, to)
		},
		Resolver: stubResolver(t),
	}
	if config != nil {
		config(&cfg)
	}

	tr, err := mimicry.NewTransport(cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(tr.CloseIdleConnections)

	s.client = &http.Client{
		Transport: tr,
	}
	_, s.port, _ = net.SplitHostPort(ln.Addr().String())

	return s
}

// listenTwin listens on a free loopback TCP port and, with udp, on its
// UDP twin too, trying another port while the twin is taken (by a QUIC
// client socket of a test running beside).
func listenTwin(t *testing.T, udp bool) (net.Listener, net.PacketConn) {
	t.Helper()

	for range 20 {
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		if !udp {
			return ln, nil
		}

		pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", ln.Addr().String())
		if err == nil {
			return ln, pc
		}

		_ = ln.Close()
	}

	t.Fatal("no loopback port free for both TCP and UDP")

	return nil, nil
}

func (s *pageSession) url(host, path string) string {
	return s.scheme + "://" + net.JoinHostPort(host, s.port) + path
}

// send makes one request as r describes, with the page's own headers h and
// body, and reads the response.
func (s *pageSession) send(method, target string, r mimicry.Request, h http.Header, body string) {
	s.t.Helper()

	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(s.t.Context(), method, target, rd)
	if err != nil {
		s.t.Fatal(err)
	}

	maps.Copy(req.Header, h)

	resp, err := s.client.Do(mimicry.With(req, r))
	if err != nil {
		s.t.Fatal(err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// finish asks for done and returns the page capture, having checked the
// plans of its requests where s plans them (planEvery).
func (s *pageSession) finish(done string) *capture.Page {
	s.t.Helper()
	s.send(http.MethodGet, done, mimicry.Request{}, nil, "")

	for {
		select {
		case c := <-s.captures:
			if c.Finished && c.Page != nil {
				if s.planning != nil {
					s.checkPlans(c.Page)
				}

				return c.Page
			}

		case <-time.After(10 * time.Second):
			s.t.Fatal("no page capture")
		}
	}
}

// kindsSent is one request as compared: its header lines (ports replaced;
// cookies included, one line per HTTP/2 crumb) and HEADERS weight.
type kindsSent struct {
	lines  []string
	weight uint8
}

// requestFields returns page's first request for path (its preflight, when
// preflight; never one after a redirect), or nil.
func requestFields(page *capture.Page, path string, h1, preflight bool) *kindsSent {
	if h1 {
		for _, r := range page.H1 {
			method, rest, _ := strings.Cut(r.Line, " ")
			target, _, _ := strings.Cut(rest, " ")

			if target != path || (method == http.MethodOptions) != preflight {
				continue
			}

			s := &kindsSent{}
			for _, l := range r.Headers {
				s.lines = append(s.lines, normalisePort(l))
			}

			return s
		}

		return nil
	}

	for _, tr := range page.H2 {
		for _, r := range tr.Requests {
			if r.Path != path {
				continue
			}

			s := &kindsSent{}
			method := ""

			for _, ins := range r.Instructions {
				switch {
				case ins.Name == ":method":
					method = ins.Value
				case ins.Name == "" || strings.HasPrefix(ins.Name, ":"):
				default:
					s.lines = append(s.lines, ins.Name+": "+normalisePort(ins.Value))
				}
			}

			if (method == http.MethodOptions) != preflight {
				continue
			}

			for _, f := range tr.Frames {
				if f.Type == "HEADERS" && f.Stream == r.Stream && f.Priority != nil {
					s.weight = f.Priority.Weight
				}
			}

			return s
		}
	}

	return nil
}

// browserForms returns the distinct forms the browser's samples sent for
// path, commonest first. When samples differ (a browser's own variance,
// such as Chrome's Sec-Fetch-User depending on activation state across
// runs), any form the browser was seen sending is one it sends; the
// variance is logged.
func browserForms(t *testing.T, samples []*capture.Page, path string, h1, preflight bool) []*kindsSent {
	t.Helper()

	counts := map[string]int{}
	forms := map[string]*kindsSent{}

	var keys []string

	for i, page := range samples {
		s := requestFields(page, path, h1, preflight)
		if s == nil {
			t.Fatalf("%s: sample %d has no browser request", path, i+1)
		}

		key := strings.Join(s.lines, "\n") + "\n" + strconv.Itoa(int(s.weight))
		if counts[key] == 0 {
			keys = append(keys, key)
		}

		counts[key]++

		forms[key] = s
	}

	slices.SortStableFunc(keys, func(a, b string) int {
		return counts[b] - counts[a]
	})

	out := make([]*kindsSent, len(keys))
	for i, k := range keys {
		out[i] = forms[k]
	}

	if len(out) > 1 {
		t.Logf("%s: the browser's samples differ; %d of %d: %q", path, counts[keys[1]], len(samples), out[1].lines)
	}

	return out
}
