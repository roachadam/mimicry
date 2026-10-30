package e2e

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/publicsuffix"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// The redirects2 page and its cookie twin through mimicry, step by step,
// send the browser's requests on each connection with its header lines and
// weight: CORS checks on redirects (*mimicry.CORSError), methods and bodies
// on 301 to 308 hops, the hop preflight, the navigation limit (Chrome 19,
// others 20); the twin, the cookie slot of each hop that carries cookies.
//
// Left out: the watchdog worker's script, /done's header lines, and Chrome's
// error-page reload after the limit (browser UI; redirectsReload).
func TestRedirects2MatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		for _, sc := range []*server.Scenario{server.Redirects2, server.Redirects2Cookies} {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				t.Parallel()

				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}

				want := loadSamples[*capture.Page](t, p, sc.Name, ".pagetrace.json.gz")

				steps, _ := sc.RedirectSteps()
				done := runRedirectsPage(t, p, sc, steps)
				apart := redirectsApart(sc, steps)
				// Gaps are pinned for the captures, so by the profile that
				// holds them; the twin's are its original's.
				src := capture.Source(p, sc.Name)
				got := mergeHosts(redirectsConnections(t, done, false, apart), redirectsMerged[src])
				wantRows := redirectsConnections(t, want[0], redirectsReload[src], apart)

				for i, w := range want {
					if d := rowsDiff(got, mergeHosts(redirectsConnections(t, w, redirectsReload[src], apart), redirectsMerged[src])); d != "" {
						t.Errorf("sample %d: connections differ: %s", i+1, d)
					}
				}

				for _, path := range redirectsPaths(wantRows) {
					if why, ok := redirectsLineGaps[src][path]; ok {
						t.Logf("%s: not compared: %s", path, why)

						continue
					}

					for _, preflight := range []bool{false, true} {
						comparePath(t, done, want, path, false, false, preflight)
					}
				}
			})
		}
	}
}

// redirectsReload are the browsers whose error page reloads a navigation
// its redirect limit stopped (nav-chain.so): their rows leave the reload
// out, and must show one.
var redirectsReload = map[string]bool{
	"chrome-155-macos":   true,
	"chrome-156-macos":   true, // its own capture: redirects2-cookies
	"chrome-156-windows": true,
}

// redirectsMerged are hosts whose connections are compared as one, per
// browser, with why: connection choices no rule explains yet (gaps, not
// redirect rules).
var redirectsMerged = map[string]map[string]string{
	"firefox-158-macos": {
		// Its credential-less fetch-acao hop and credentialed cred-acac hop
		// went on two connections, while www.redir-b.test's requests of
		// both sorts shared one (every sample).
		"www.redir-c.test": "credentialed and credential-less hops on connections of their own, on this host only",
	},
	"chrome-156-macos": {
		// redirects2-cookies: in one sample of three the error page's
		// reload of nav-chain.so (browser UI, redirectsReload) went on a
		// new connection, which /done then took.
		"www.redir-a.test": "after the error page's reload on a connection of its own (one sample), /done on that one",
	},
}

// redirectsLineGaps are requests whose header lines are not compared, per
// browser, with why: priorities that depend on layout, not on how the
// request was made or redirected.
var redirectsLineGaps = map[string]map[string]string{
	"firefox-158-macos": {
		"/p/img-markup.so":      "markup images here get priority u=4 (weight 13), the kinds page's u=5",
		"/p/img-markup.so.hop1": "markup images here get priority u=4 (weight 13), the kinds page's u=5",
		"/p/img-markup.ss":      "markup images here get priority u=4 (weight 13), the kinds page's u=5",
		"/p/img-markup.ss.hop1": "markup images here get priority u=4 (weight 13), the kinds page's u=5",
	},
	"chrome-155-macos": {
		"/p/img-markup.ss.hop1": "the hop of a markup image goes at u=1 (weight 219), raised once layout found it visible",
	},
	// Its own capture is redirects2-cookies's (redirects2's is
	// chrome-155-macos's): the same layout priority, on more of the
	// markup images, in some samples.
	"chrome-156-macos": {
		"/p/img-markup.so.hop1": "markup images and their hops go at u=1 (weight 219) in some samples, raised once layout found them visible",
		"/p/img-markup.ss":      "markup images and their hops go at u=1 (weight 219) in some samples, raised once layout found them visible",
		"/p/img-markup.ss.hop1": "markup images and their hops go at u=1 (weight 219) in some samples, raised once layout found them visible",
	},
}

// mergeHosts joins the connections of each host in hosts into one, in
// order.
func mergeHosts(rows [][]string, hosts map[string]string) [][]string {
	var out [][]string

	merged := map[string]int{}

	for _, conn := range rows {
		if _, ok := hosts[conn[0]]; !ok {
			out = append(out, conn)
			continue
		}

		if i, ok := merged[conn[0]]; ok {
			out[i] = append(out[i], conn[1:]...)
			continue
		}

		merged[conn[0]] = len(out)
		out = append(out, slices.Clone(conn))
	}

	return out
}

// runRedirectsPage makes the requests of page sc (redirects2, or
// nav-redirect-page) through mimicry as profile p, as the page does
// (steps), and returns the server's capture.
func runRedirectsPage(t *testing.T, p *profile.Profile, sc *server.Scenario, steps []server.Redirects2Step) *capture.Page {
	t.Helper()

	s := startPage(t, p, sc)
	s.planEvery()

	// As NewClient: the Transport stops chains where browsers do.
	s.client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return nil
	}

	hosts, _, _ := sc.MultiHost()
	pageURL := s.url(hosts[0], "/")
	s.send(http.MethodGet, pageURL, mimicry.Request{}, nil, "")

	root, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}

	// The page's markup images load with it, before its script.
	ordered := slices.Concat(
		slices.DeleteFunc(slices.Clone(steps), func(st server.Redirects2Step) bool {
			return st.Kind != server.RedirectImageMarkup
		}),
		slices.DeleteFunc(slices.Clone(steps), func(st server.Redirects2Step) bool {
			return st.Kind == server.RedirectImageMarkup
		}),
	)
	// last is the page the next navigation starts from: the one the
	// previous navigation landed on.
	last := root
	for _, st := range ordered {
		target := s.url(st.Host, "/p/"+st.Label)

		var err error

		if st.Nav() {
			start := s.url(hosts[0], "/s/"+st.Label)
			if _, err := redirectsSend(t, s, http.MethodGet, start, "", nil, mimicry.Request{Kind: mimicry.Navigate, From: last}, true); err != nil {
				t.Fatalf("%s: %v", start, err)
			}

			from, perr := url.Parse(start)
			if perr != nil {
				t.Fatal(perr)
			}

			var h http.Header

			body := st.Body
			method := st.Method

			if method == "" {
				method = http.MethodGet
			}

			switch {
			case method == http.MethodGet && body != "":
				// A GET form sends its field in the query.
				target += "?" + body

				body = ""

			case body != "":
				h = http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
			}

			var landed *url.URL
			// A clicked navigation has user activation; the page's
			// script's has none.
			landed, err = redirectsSend(t, s, method, target, body, h, mimicry.Request{Kind: mimicry.Navigate, From: from}, !st.Click)
			if landed != nil {
				last = landed
			}
		} else {
			err = redirects2Subresource(t, s, root, st, target)
		}

		var cerr *mimicry.CORSError
		switch {
		case st.NoACAO || st.NoACAC:
			if !errors.As(err, &cerr) || !cerr.Redirect {
				t.Errorf("%s: got %v, want a redirect's *mimicry.CORSError", st.Label, err)
			}

		case st.Hops > p.Redirects.NavigationLimit:
			if want := "stopped after " + strconv.Itoa(p.Redirects.NavigationLimit) + " redirects"; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: got %v, want %s", st.Label, err, want)
			}

		case err != nil:
			t.Errorf("%s: %v", st.Label, err)
		}
	}

	return s.finish(s.url(hosts[0], "/done"))
}

// redirects2Subresource makes step st, not a navigation, from the page at
// root, as the page's script (or markup) does.
func redirects2Subresource(t *testing.T, s *pageSession, root *url.URL, st server.Redirects2Step, target string) error {
	t.Helper()

	r := mimicry.Request{
		From: root,
	}
	switch st.Kind {
	case server.RedirectImageMarkup:
		r.Kind = mimicry.Image
	case server.RedirectImageNew:
		r.Kind = mimicry.InjectedImage
	case server.RedirectImage:
		r.Kind, r.CrossOrigin = mimicry.InjectedImage, mimicry.CrossOriginAnonymous
	case server.RedirectScript:
		r.Kind, r.CrossOrigin = mimicry.InjectedScript, mimicry.CrossOriginAnonymous
	case server.RedirectXHR:
		r.Kind = mimicry.XHR
	case server.RedirectFetch:
		r.Kind = mimicry.Fetch
		if st.Credentials == "include" {
			r.Credentials = mimicry.CredentialsInclude
		}

	default:
		t.Fatalf("%s: kind %q", st.Label, st.Kind)
	}

	method := st.Method
	if method == "" {
		method = http.MethodGet
	}

	var h http.Header
	if st.Body != "" {
		// fetch() sends a string body so.
		h = http.Header{"Content-Type": {"text/plain;charset=UTF-8"}}
	}

	_, err := redirectsSend(t, s, method, target, st.Body, h, r, false)

	return err
}

// redirectsSend makes one request as r describes, with the page's headers
// h and body, following its redirects, and returns the URL it ended at. A
// script's navigation (script) carries no Sec-Fetch-User.
func redirectsSend(t *testing.T, s *pageSession, method, target, body string, h http.Header, r mimicry.Request, script bool) (*url.URL, error) {
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

	if script {
		req.Header["Sec-Fetch-User"] = nil
	}

	resp, err := s.client.Do(mimicry.With(req, r))
	if err != nil {
		return nil, err
	}

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.Request.URL, resp.Body.Close()
}

// The nav-redirect pages through mimicry: typed navigations whose start URL
// redirects (Navigate, no From), and the page's script and click navigations
// (From the page; Sec-Fetch-User only on a click). Each sends the browser's
// requests per connection with its header lines and weight; navigations to
// another site are compared per host, as mimicry's pool is not partitioned.
func TestNavRedirectsMatchBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		for _, sc := range server.NavRedirects {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				t.Parallel()

				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}

				want := loadSamples[*capture.Page](t, p, sc.Name, ".pagetrace.json.gz")
				if steps, ok := sc.RedirectSteps(); ok {
					done := runRedirectsPage(t, p, sc, steps)
					apart := redirectsApart(sc, steps)
					got := redirectsConnections(t, done, false, apart)

					for i, w := range want {
						if d := rowsDiff(got, redirectsConnections(t, w, false, apart)); d != "" {
							t.Errorf("sample %d: connections differ: %s", i+1, d)
						}
					}

					for _, path := range redirectsPaths(redirectsConnections(t, want[0], false, apart)) {
						comparePath(t, done, want, path, false, false, false)
					}

					return
				}

				step, ok := sc.NavRedirectStep()
				if !ok {
					t.Fatalf("%s is no nav-redirect scenario", sc.Name)
				}

				s := startPage(t, p, sc)
				s.planEvery()

				start := s.url(step.Host, step.StartPath())
				if step.Plain {
					start = "http://" + net.JoinHostPort(step.Host, s.plainPort) + step.StartPath()
				}

				landed, err := redirectsSend(t, s, http.MethodGet, start, "", nil, mimicry.Request{}, false)
				if err != nil {
					t.Fatal(err)
				}

				hops := step.HopPaths()
				if landed.Path != hops[len(hops)-1] {
					t.Errorf("landed on %s, want %s", landed, hops[len(hops)-1])
				}

				done := s.finish(s.url(step.LastHost(), "/done"))
				// /done is the landing page's own fetch, not the
				// navigation's: Safari sends it on a connection of its own.
				noDone := func(rows [][]string) [][]string {
					var out [][]string
					for _, conn := range rows {
						conn = slices.DeleteFunc(slices.Clone(conn), func(row string) bool {
							return strings.HasPrefix(row, "GET /done ")
						})
						if len(conn) > 1 {
							out = append(out, conn)
						}
					}

					return out
				}
				got := noDone(redirectsConnections(t, done, false, nil))

				for i, w := range want {
					if d := rowsDiff(got, noDone(redirectsConnections(t, w, false, nil))); d != "" {
						t.Errorf("sample %d: connections differ: %s", i+1, d)
					}
				}

				comparePath(t, done, want, step.StartPath(), false, step.Plain, false)

				for _, hop := range hops {
					comparePath(t, done, want, hop, false, false, false)
				}
			})
		}
	}
}

// redirectsPaths are the paths of the rows' requests (redirectsConnections)
// to compare, each once, sorted: all but /done (Firefox's and Safari's
// comes from the watchdog worker).
func redirectsPaths(rows [][]string) []string {
	var out []string
	for _, conn := range rows {
		for _, row := range conn[1:] {
			path := strings.Fields(row)[1]
			if path != "/done" && !slices.Contains(out, path) {
				out = append(out, path)
			}
		}
	}

	slices.Sort(out)

	return out
}

// redirectsConnections lists page's HTTP/2 connections (host and requests in
// stream order, as corsEdgeConnections), then its HTTP/1.1 ones, without the
// watchdog script. With reload, the error page's reload chain is dropped and
// must exist; it may go on a connection of its own (Chrome 156). Steps in
// apart are listed per host, not per connection: the browser partitions by
// top-level site and mimicry does not (a gap).
func redirectsConnections(t *testing.T, page *capture.Page, reload bool, apart map[string]bool) [][]string {
	t.Helper()

	reloaded := false

	var out [][]string

	moved := map[string][]string{}

	var movedHosts []string

	// Paths seen on any connection: a reload repeats one, on its first
	// request's connection or a new one.
	seen := map[string]bool{}
	chain := ""

	for _, rows := range corsEdgeConnections(page) {
		kept := []string{rows[0]}

		for _, row := range rows[1:] {
			fields := strings.Fields(row)
			method, path := fields[0], fields[1]
			label, _, _ := strings.Cut(path, "?")

			label, _, _ = strings.Cut(label, ".hop")

			switch {
			case path == "/sw/watchdog.js":
				continue
			case reload && method != http.MethodOptions && seen[path]:
				reloaded, chain = true, label
				continue

			case chain != "" && label == chain:
				continue
			}

			seen[path] = seen[path] || method != http.MethodOptions

			if apart[label] {
				if moved[rows[0]] == nil {
					movedHosts = append(movedHosts, rows[0])
				}

				moved[rows[0]] = append(moved[rows[0]], row)

				continue
			}

			kept = append(kept, row)
		}

		if len(kept) > 1 {
			out = append(out, kept)
		}
	}

	for _, host := range movedHosts {
		out = append(out, append([]string{host + " (navigations to another site)"}, moved[host]...))
	}

	slices.SortStableFunc(out, func(a, b []string) int {
		if c := strings.Compare(a[0], b[0]); c != 0 {
			return c
		}

		return len(b) - len(a)
	})

	h1 := map[int][]string{}

	var conns []int

	for _, r := range page.H1 {
		if h1[r.Conn] == nil {
			conns = append(conns, r.Conn)

			host, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = r.Host
			}

			h1[r.Conn] = []string{"h1 " + host}
		}

		method, rest, _ := strings.Cut(r.Line, " ")
		target, _, _ := strings.Cut(rest, " ")

		h1[r.Conn] = append(h1[r.Conn], method+" "+target)
	}

	for _, c := range conns {
		out = append(out, h1[c])
	}

	if reload && !reloaded {
		t.Errorf("no reload of a stopped navigation in the browser's capture: drop it from redirectsReload")
	}

	return out
}

// redirectsApart are the steps of page sc that redirectsConnections lists
// apart: the navigations with a request to another site than the page's
// (first leg or hop).
func redirectsApart(sc *server.Scenario, steps []server.Redirects2Step) map[string]bool {
	hosts, _, _ := sc.MultiHost()
	site := func(host string) string {
		s, err := publicsuffix.EffectiveTLDPlusOne(host)
		if err != nil {
			return host
		}

		return s
	}
	out := map[string]bool{}

	for _, st := range steps {
		if st.Nav() && (site(st.Host) != site(hosts[0]) || site(st.HopHost()) != site(hosts[0])) {
			out["/p/"+st.Label] = true
		}
	}

	return out
}

// rowsDiff describes where connection rows got and want first differ, or
// returns "".
func rowsDiff(got, want [][]string) string {
	for i := range max(len(got), len(want)) {
		switch {
		case i >= len(got):
			return fmt.Sprintf("mimicry has no connection %d, the browser %q", i+1, want[i])
		case i >= len(want):
			return fmt.Sprintf("the browser has no connection %d, mimicry %q", i+1, got[i])
		}

		for j := range max(len(got[i]), len(want[i])) {
			g, w := "(none)", "(none)"
			if j < len(got[i]) {
				g = got[i][j]
			}

			if j < len(want[i]) {
				w = want[i][j]
			}

			if g != w {
				return fmt.Sprintf("connection %d (%s), row %d: got %q, want %q", i+1, want[i][0], j, g, w)
			}
		}
	}

	return ""
}
