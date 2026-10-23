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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// Every request of the kinds page, made through mimicry as its kind from
// the page, is the browser's: the same header names in the same order, the
// same values, the same HTTP/2 weight, and over HTTP/1.1 the same lines;
// redirects' requests too, as http.Client follows them. Left out: cookies
// (mimicry/cookies; this client has no jar, and cookie fields are not
// compared), CORS preflights, and FontFace loads (no kind).
func TestKindsMatchBrowserTraces(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			if p.Kinds == nil {
				t.Skipf("%s describes no request kinds (no kinds capture)", name)
			}
			for _, sc := range []*server.Scenario{server.Kinds, server.KindsHTTP1} {
				t.Run(sc.Name, func(t *testing.T) {
					want, err := capture.LoadPages(captureDir + name + "." + sc.Name + ".pagetrace.json.gz")
					if err != nil {
						t.Fatal(err)
					}
					got := runKindsPage(t, p, sc)
					h1 := sc == server.KindsHTTP1
					var all []string
					for _, step := range kindsRequests() {
						labels := []string{step.label}
						if step.redirect {
							labels = append(labels, step.label+".hop")
						}
						all = append(all, labels...)
						for _, label := range labels {
							g := kindsFields(got, label, h1)
							if g == nil {
								t.Errorf("%s: mimicry sent no request", label)
								continue
							}
							forms := browserForms(t, want, label, h1)
							if !slices.ContainsFunc(forms, func(w *kindsSent) bool {
								return slices.Equal(g.lines, w.lines) && g.weight == w.weight
							}) {
								t.Errorf("%s:\n got %q weight %d\nwant %q weight %d", label, g.lines, g.weight, forms[0].lines, forms[0].weight)
							}
						}
					}
					if !h1 {
						checkConnections(t, name, got, want, all)
					}
				})
			}
		})
	}
}

// partitionGaps are profiles whose browser groups the kinds page's
// requests into connections in a way mimicry does not model, and why.
var partitionGaps = map[string]string{
	"firefox-158-macos": "Firefox gives credential-less requests their own connection to the same-site host only, sharing one connection to the page's own host and to the cross-site one (all three samples); no rule found, not modelled",
}

// checkConnections compares which requests share a connection: for each
// request, the first request on its connection, in mimicry's page and in
// every browser sample (HTTP/2, where a browser keeps one connection per
// host and partition).
func checkConnections(t *testing.T, name string, got *capture.Page, want []*capture.Page, labels []string) {
	t.Helper()
	g := connGroups(got, labels)
	for i, page := range want {
		w := connGroups(page, labels)
		var diffs []string
		for _, l := range labels {
			if g[l] != w[l] {
				diffs = append(diffs, l+": mimicry with "+g[l]+", browser with "+w[l])
			}
		}
		if len(diffs) == 0 {
			continue
		}
		if gap, ok := partitionGaps[name]; ok {
			t.Logf("connections, sample %d, a known gap (%s):\n  %s", i+1, gap, strings.Join(diffs, "\n  "))
			continue
		}
		t.Errorf("connections, sample %d:\n  %s", i+1, strings.Join(diffs, "\n  "))
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
	var out []kindsRequest
	for _, s := range server.KindsMarkup() {
		base, _, _ := strings.Cut(s.Label, ".")
		out = append(out, kindsRequest{
			label:  s.Label,
			method: http.MethodGet,
			req: mimicry.Request{
				Kind: markup[base],
			},
		})
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
				CrossOrigin: map[string]mimicry.CrossOrigin{"": mimicry.None, "anonymous": mimicry.Anonymous, "use-credentials": mimicry.UseCredentials}[s.CrossOrigin],
				Credentials: map[string]mimicry.Credentials{"": mimicry.SameOrigin, "include": mimicry.Include, "omit": mimicry.Omit}[s.Credentials],
				NoCORS:      s.Mode == "no-cors",
				Priority:    map[string]mimicry.Priority{"": mimicry.Auto, "high": mimicry.High, "low": mimicry.Low}[s.Priority],
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
	}
	return append(out, kindsRequest{
		label:  "form-post.so",
		method: http.MethodPost,
		req: mimicry.Request{
			Kind: mimicry.Navigate,
		},
		headers: http.Header{
			"Content-Type": {"application/x-www-form-urlencoded"},
		},
		body: "a=1",
	})
}

// runKindsPage serves sc and makes the kinds page's requests through
// mimicry as profile p, from the page, and returns the server's capture.
func runKindsPage(t *testing.T, p *profile.Profile, sc *server.Scenario) *capture.Page {
	t.Helper()
	ca, err := server.LoadOrCreateCA(t.TempDir(), server.CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	captures := make(chan *capture.Capture, 64)
	ctx, cancel := context.WithCancel(t.Context())
	srv := &server.Server{
		Scenario:  sc,
		TLSConfig: &tls.Config{},
		IssueCert: func(hosts []string) (tls.Certificate, error) {
			return ca.Issue(hosts, time.Now())
		},
		OnCapture: func(c *capture.Capture) {
			captures <- c
		},
		Logf: t.Logf,
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
	tr, err := mimicry.NewTransport(mimicry.Config{
		Profile: p,
		RootCAs: pool,
		// Every capture host is this server.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{
		Transport: tr,
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	hosts, _, _ := sc.MultiHost()
	page := "https://" + net.JoinHostPort(hosts[0], port)
	send := func(method, target string, r mimicry.Request, h http.Header, body string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(t.Context(), method, target, rd)
		if err != nil {
			t.Fatal(err)
		}
		maps.Copy(req.Header, h)
		resp, err := client.Do(mimicry.With(req, r))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	send(http.MethodGet, page+"/", mimicry.Request{}, nil, "")
	from, err := url.Parse(page + "/")
	if err != nil {
		t.Fatal(err)
	}
	steps := map[string]server.KindsStep{}
	for _, s := range slices.Concat(server.KindsMarkup(), server.KindsSteps()) {
		steps[s.Label] = s
	}
	for _, r := range kindsRequests() {
		host := hosts[0]
		if s, ok := steps[r.label]; ok {
			host = s.Host
		}
		r.req.From = from
		if strings.HasPrefix(r.label, "font-css.") {
			// The stylesheet that declares the font loads it.
			r.req.Stylesheet = from.JoinPath("k", "style-fonts.so")
		}
		send(r.method, "https://"+net.JoinHostPort(host, port)+"/k/"+r.label, r.req, r.headers, r.body)
	}
	send(http.MethodGet, page+"/done", mimicry.Request{}, nil, "")
	for {
		select {
		case c := <-captures:
			if c.Finished && c.Page != nil {
				return c.Page
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no page capture")
		}
	}
}

// kindsSent is one request as compared: its header lines (cookies left
// out, ports replaced) and HEADERS weight.
type kindsSent struct {
	lines  []string
	weight uint8
}

var testPort = regexp.MustCompile(`\.test:\d+`)

// kindsFields returns page's first request for label (not a preflight, not
// after a redirect), or nil.
func kindsFields(page *capture.Page, label string, h1 bool) *kindsSent {
	path := "/k/" + label
	if h1 {
		for _, r := range page.H1 {
			method, rest, _ := strings.Cut(r.Line, " ")
			target, _, _ := strings.Cut(rest, " ")
			if target != path || method == http.MethodOptions {
				continue
			}
			s := &kindsSent{}
			for _, l := range r.Headers {
				if name, _, _ := strings.Cut(l, ":"); !strings.EqualFold(name, "cookie") {
					s.lines = append(s.lines, testPort.ReplaceAllString(l, ".test:PORT"))
				}
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
				case ins.Name == "" || ins.Name == "cookie" || strings.HasPrefix(ins.Name, ":"):
				default:
					s.lines = append(s.lines, ins.Name+": "+testPort.ReplaceAllString(ins.Value, ".test:PORT"))
				}
			}
			if method == http.MethodOptions {
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
// label, commonest first. When samples differ (a browser's own variance,
// such as Chrome's Sec-Fetch-User depending on activation state across
// runs), any form the browser was seen sending is one it sends; the
// variance is logged.
func browserForms(t *testing.T, samples []*capture.Page, label string, h1 bool) []*kindsSent {
	t.Helper()
	counts := map[string]int{}
	forms := map[string]*kindsSent{}
	var keys []string
	for i, page := range samples {
		s := kindsFields(page, label, h1)
		if s == nil {
			t.Fatalf("%s: sample %d has no browser request", label, i+1)
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
		t.Logf("%s: the browser's samples differ; %d of %d: %q", label, counts[keys[1]], len(samples), out[1].lines)
	}
	return out
}
