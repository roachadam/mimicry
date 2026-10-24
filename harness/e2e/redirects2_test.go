package e2e

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// The redirects2 page, made through mimicry step by step as the browser's
// page makes it, goes out as the browser's did: on each connection the
// same requests in the same order (method, path, body length, Origin),
// and every request (first legs, hops, preflights, the pages the
// navigations start from) with the browser's header lines and HTTP/2
// weight. That covers the CORS check of XHR, <img crossorigin> and
// <script crossorigin> redirects and of credentialed fetch() chains
// (*mimicry.CORSError, no next hop), the methods and bodies of PUT, POST,
// HEAD and PATCH hops and of form POSTs answered 301 to 308, Firefox's and
// Safari's preflight of a preflighted PATCH's GET hop, and the
// navigation's redirect limit (Chrome 19, Firefox and Safari 20).
//
// Left out: the watchdog service worker's script (no request kind), and
// /done's header lines (Firefox's and Safari's come from the worker).
// Chrome's error page reloads the navigation its limit stopped a second
// later (cache-control: max-age=0, from the last URL requested) and
// follows the rest of the chain: browser UI, not the request mimicry
// fails, so those requests are left out of the browser's rows
// (redirectsReload).
func TestRedirects2MatchesBrowser(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			want, err := capture.LoadPages(captureDir + name + ".redirects2.pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}
			done := runRedirects2Page(t, p)
			apart := redirectsApart()
			got := mergeHosts(redirectsConnections(t, done, false, apart), redirectsMerged[name])
			wantRows := redirectsConnections(t, want[0], redirectsReload[name], apart)
			for i, w := range want {
				if d := rowsDiff(got, mergeHosts(redirectsConnections(t, w, redirectsReload[name], apart), redirectsMerged[name])); d != "" {
					t.Errorf("sample %d: connections differ: %s", i+1, d)
				}
			}
			for _, path := range redirectsPaths(wantRows) {
				if why, ok := redirectsLineGaps[name][path]; ok {
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

// redirectsReload are the browsers whose error page reloads a navigation
// its redirect limit stopped (nav-chain.so): their rows leave the reload
// out, and must show one.
var redirectsReload = map[string]bool{
	"chrome-155-macos": true,
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

// runRedirects2Page makes the redirects2 page's requests through mimicry
// as profile p, as the page does (server.Redirects2Steps), and returns
// the server's capture.
func runRedirects2Page(t *testing.T, p *profile.Profile) *capture.Page {
	t.Helper()
	s := startPage(t, p, server.Redirects2)
	// As NewClient: the Transport stops chains where browsers do.
	s.client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return nil
	}
	hosts, _, _ := server.Redirects2.MultiHost()
	pageURL := s.url(hosts[0], "/")
	s.send(http.MethodGet, pageURL, mimicry.Request{}, nil, "")
	root, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	steps := server.Redirects2Steps()
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
			if st.Body != "" {
				h = http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
			}
			method := st.Method
			if method == "" {
				method = http.MethodGet
			}
			var landed *url.URL
			landed, err = redirectsSend(t, s, method, target, st.Body, h, mimicry.Request{Kind: mimicry.Navigate, From: from}, true)
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
	for k, v := range h {
		req.Header[k] = v
	}
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

// The nav-redirect pages, typed navigations whose start URL redirects
// (within its origin, apex to www, to another site, http:// to https://
// of its host), made through mimicry as a typed URL (Navigate, no From):
// the same requests on each connection, and every one with the browser's
// header lines (and HTTP/2 weight).
func TestNavRedirectsMatchBrowser(t *testing.T) {
	for _, name := range profiles.Names() {
		for _, sc := range server.NavRedirects {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				t.Parallel()
				p, err := profiles.ByName(name)
				if err != nil {
					t.Fatal(err)
				}
				want, err := capture.LoadPages(captureDir + name + "." + sc.Name + ".pagetrace.json.gz")
				if err != nil {
					t.Fatal(err)
				}
				step, ok := sc.NavRedirectStep()
				if !ok {
					t.Fatalf("%s is no nav-redirect scenario", sc.Name)
				}
				s := startPage(t, p, sc)
				start := s.url(step.Host, step.StartPath())
				if step.Plain {
					start = "http://" + net.JoinHostPort(step.Host, s.plainPort) + step.StartPath()
				}
				landed, err := redirectsSend(t, s, http.MethodGet, start, "", nil, mimicry.Request{}, false)
				if err != nil {
					t.Fatal(err)
				}
				if landed.Path != step.HopPath() {
					t.Errorf("landed on %s, want %s", landed, step.HopPath())
				}
				done := s.finish(s.url(step.Next, "/done"))
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
				comparePath(t, done, want, step.HopPath(), false, false, false)
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

// redirectsConnections lists, for each HTTP/2 connection of page, its host
// and requests in stream order (corsEdgeConnections), and then for each
// HTTP/1.1 connection its host and requests (method, path); the watchdog
// worker's script is left out. With reload, a navigation's request for a
// path already requested and the requests of that chain after it are left
// out (an error page's reload), and there must be one. The requests of the
// steps in apart (/p/<label>, and their hops) are listed per host, in
// order, apart from their connections: the navigations to another site,
// which every browser sends on a connection of their own (its pool is
// partitioned by the top-level site) and mimicry's pool does not
// (redirects2 report: a gap).
func redirectsConnections(t *testing.T, page *capture.Page, reload bool, apart map[string]bool) [][]string {
	t.Helper()
	reloaded := false
	var out [][]string
	moved := map[string][]string{}
	var movedHosts []string
	for _, rows := range corsEdgeConnections(page) {
		kept := []string{rows[0]}
		seen := map[string]bool{}
		chain := ""
		for _, row := range rows[1:] {
			fields := strings.Fields(row)
			method, path := fields[0], fields[1]
			label, _, _ := strings.Cut(path, ".hop")
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

// redirectsApart are the redirects2 steps redirectsConnections lists
// apart: the navigations to another site than the page's.
func redirectsApart() map[string]bool {
	hosts, _, _ := server.Redirects2.MultiHost()
	out := map[string]bool{}
	for _, st := range server.Redirects2Steps() {
		if st.Nav() && st.Host != hosts[0] {
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
