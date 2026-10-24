package server

import (
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// send makes one request with a body and headers and returns the response
// with its body read.
func send(t *testing.T, client *http.Client, method, url, body string, header http.Header) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(got)
}

// The redirects2 page makes every step that is not a navigation from /,
// then each navigation from a page of its own; each step is answered with
// its redirects, to its hop host, with the CORS headers it says, then with
// what its kind loads; the last navigation's page asks for /done.
func TestRedirects2(t *testing.T) {
	client, port, captures := replayServer(t, Redirects2)
	origin := originOn("https://", port)
	page := origin(redirHost)
	steps := Redirects2Steps()
	if hosts, _, _ := Redirects2.MultiHost(); hosts[0] != redirHost {
		t.Fatalf("the page starts on %s", hosts[0])
	}

	labels := map[string]bool{}
	firstNav := slices.IndexFunc(steps, Redirects2Step.Nav)
	for i, s := range steps {
		if labels[s.Label] {
			t.Errorf("%s: label used twice", s.Label)
		}
		labels[s.Label] = true
		if s.Nav() != (i >= firstNav) {
			t.Errorf("%s: navigation %v at %d, the first navigation at %d", s.Label, s.Nav(), i, firstNav)
		}
	}
	// Every step's label is its own question: no two steps share kind,
	// method, status, hosts and headers.
	type question struct {
		kind, method, host, next, credentials string
		status, hops                          int
		noACAO, noACAC                        bool
	}
	asked := map[question]string{}
	for _, s := range steps {
		q := question{s.Kind, s.Method, s.Host, s.HopHost(), s.Credentials, s.Status, s.Hops, s.NoACAO, s.NoACAC}
		if other, ok := asked[q]; ok {
			t.Errorf("%s asks what %s does", s.Label, other)
		}
		asked[q] = s.Label
	}

	// The page at / has its markup images, installs the watchdog, waits
	// for them and makes the other steps in order, then goes to the first
	// navigation's page.
	_, root := fetch(t, client, "GET", page+"/", nil)
	markup, _, _ := strings.Cut(root, "<script>")
	_, rest, ok := strings.Cut(root, "await register();")
	if !ok {
		t.Fatalf("/ does not install the watchdog:\n%s", root)
	}
	for _, s := range steps[:firstNav] {
		u := strconv.Quote(origin(s.Host) + "/p/" + s.Label)
		call := map[string]string{
			RedirectImageMarkup: `await shown("` + s.Label + `")`,
			RedirectImageNew:    "await image(" + u + ")",
			RedirectXHR:         "await xhr(" + u + ")",
			RedirectImage:       `await load("img", ` + u + ")",
			RedirectScript:      `await load("script", ` + u + ")",
			RedirectFetch:       "await f(" + u + ", {",
		}[s.Kind]
		if tag := `<img id="` + s.Label + `" src="` + origin(s.Host) + "/p/" + s.Label + `" alt="">`; s.Kind == RedirectImageMarkup && !strings.Contains(markup, tag) {
			t.Errorf("%s: / lacks %s before its script:\n%s", s.Label, tag, root)
		}
		_, after, ok := strings.Cut(rest, call)
		if !ok {
			t.Errorf("%s: / does not make %s next:\n%s", s.Label, call, root)
			break
		}
		rest = after
	}
	if want := `location.href = "/s/` + steps[firstNav].Label + `"`; !strings.Contains(rest, want) {
		t.Errorf("/ does not end with %s:\n%s", want, root)
	}
	for _, want := range []string{
		`await f("` + page + `/p/m-patch-302.so", {method: "PATCH", body: "x"})`,
		`await f("` + origin(redirCrossSite) + `/p/m-patch-307.cs", {method: "PATCH", body: "x"})`,
		`await f("` + origin(redirCrossSite) + `/p/m-post-308.cs", {method: "POST", body: "x"})`,
		`await f("` + page + `/p/m-head-301.so", {method: "HEAD"})`,
		`await f("` + origin(redirSameSite) + `/p/fetch-noacao.ss", {})`,
		`await f("` + origin(redirCrossSite) + `/p/fetch-acao.cs-cs", {})`,
		`await f("` + origin(redirCrossSite) + `/p/cred-noacac.cs-cs", {credentials: "include"})`,
	} {
		if !strings.Contains(root, want) {
			t.Errorf("/ lacks %s", want)
		}
	}

	for i, s := range steps {
		if s.Nav() {
			// Its page arms the watchdog, then submits the form or goes.
			resp, body := fetch(t, client, "GET", page+"/s/"+s.Label, nil)
			if resp.StatusCode != 200 {
				t.Errorf("%s: start page %s", s.Label, resp.Status)
			}
			first := origin(s.Host) + "/p/" + s.Label
			var want []string
			switch s.Kind {
			case RedirectForm:
				want = []string{`<form method="post" action="` + first + `"><input type="hidden" name="a" value="1"></form>`, "await arm(10000);\ndocument.forms[0].submit();"}
			case RedirectNavigate:
				want = []string{"if (!(await arm(10000))) {\nfetch(\"/done\");\nreturn;\n}\nlocation.href = \"" + first + `";`}
			}
			for _, w := range want {
				if !strings.Contains(body, w) {
					t.Errorf("%s: start page lacks %q:\n%s", s.Label, w, body)
				}
			}
		} else if resp, _ := fetch(t, client, "GET", page+"/s/"+s.Label, nil); resp.StatusCode != 404 {
			t.Errorf("%s: a start page for a step that is not a navigation: %s", s.Label, resp.Status)
		}

		// The chain: each request answered Status to the next hop, with
		// CORS headers for a request with Origin, as the step says.
		header := http.Header{}
		if !s.Nav() && s.Host != redirHost {
			header.Set("Origin", page)
		}
		method := s.Method
		if method == "" {
			method = "GET"
		}
		if s.Kind == RedirectForm {
			header.Set("Content-Type", "application/x-www-form-urlencoded")
			header.Set("Origin", page)
		}
		u := origin(s.Host) + "/p/" + s.Label
		for hop := range s.Hops {
			resp, _ := send(t, client, method, u, s.Body, header)
			if resp.StatusCode != s.Status {
				t.Errorf("%s: hop %d answered %s, want %d", s.Label, hop, resp.Status, s.Status)
				break
			}
			next := origin(s.HopHost()) + "/p/" + s.Label + ".hop" + strconv.Itoa(hop+1)
			if got := resp.Header.Get("Location"); got != next {
				t.Errorf("%s: hop %d to %q, want %q", s.Label, hop, got, next)
			}
			acao, acac := resp.Header.Get("Access-Control-Allow-Origin"), resp.Header.Get("Access-Control-Allow-Credentials")
			wantACAO, wantACAC := header.Get("Origin"), "true"
			switch {
			case s.Nav() || s.NoACAO:
				wantACAO, wantACAC = "", ""
			case s.NoACAC:
				wantACAC = ""
			}
			if wantACAO == "" {
				wantACAC = ""
			}
			if acao != wantACAO || acac != wantACAC {
				t.Errorf("%s: hop %d: Access-Control-Allow-Origin %q, -Credentials %q; want %q, %q", s.Label, hop, acao, acac, wantACAO, wantACAC)
			}
			u = next
			if s.HopHost() != s.Host && header.Get("Origin") != "" {
				// A redirect to another origin taints the request's.
				header.Set("Origin", "null")
			}
		}
		// The step's first request is not answered on its hop host, nor a
		// hop past the last.
		if s.HopHost() != s.Host {
			if resp, _ := fetch(t, client, "GET", origin(s.HopHost())+"/p/"+s.Label, nil); resp.StatusCode != 404 {
				t.Errorf("%s: first request on the hop host: %s", s.Label, resp.Status)
			}
		}
		if resp, _ := fetch(t, client, "GET", origin(s.HopHost())+"/p/"+s.Label+".hop"+strconv.Itoa(s.Hops+1), nil); resp.StatusCode != 404 {
			t.Errorf("%s: hop past the last: %s", s.Label, resp.Status)
		}

		resp, body := send(t, client, method, u, s.Body, header)
		if resp.StatusCode != 200 {
			t.Errorf("%s: last hop %s", s.Label, resp.Status)
			continue
		}
		wantType := map[string]string{
			RedirectForm:        "text/html; charset=utf-8",
			RedirectNavigate:    "text/html; charset=utf-8",
			RedirectFetch:       "application/json",
			RedirectXHR:         "application/json",
			RedirectImage:       "image/png",
			RedirectImageMarkup: "image/png",
			RedirectImageNew:    "image/png",
			RedirectScript:      "text/javascript",
		}[s.Kind]
		if got := resp.Header.Get("Content-Type"); got != wantType {
			t.Errorf("%s: last hop is %q, want %q", s.Label, got, wantType)
		}
		if o := header.Get("Origin"); !s.Nav() && o != "" && (resp.Header.Get("Access-Control-Allow-Origin") != o || resp.Header.Get("Access-Control-Allow-Credentials") != "true") {
			t.Errorf("%s: last hop does not let %q read it: %v", s.Label, o, resp.Header)
		}
		if method == "HEAD" && body != "" {
			t.Errorf("%s: HEAD answered with a body", s.Label)
		}
		if !s.Nav() {
			continue
		}
		// The page it lands on goes to the next navigation's page; the
		// last ends the watchdog and asks for /done.
		want := "await end();\nfetch(\"/done\");"
		if i+1 < len(steps) {
			want = `location.href = "` + page + "/s/" + steps[i+1].Label + `";`
		}
		if !strings.Contains(body, want) {
			t.Errorf("%s: landing page lacks %q:\n%s", s.Label, want, body)
		}
	}

	// The chain outlasts the 20 redirects every browser's fetch() follows.
	if s := steps[len(steps)-1]; s.Label != "nav-chain.so" || s.Hops <= 21 || s.Kind != RedirectNavigate {
		t.Errorf("last step %s (%s) with %d hops, want the navigation chain with more than 21", s.Label, s.Kind, s.Hops)
	}
	// A preflight is answered for the step's origin, on a hop too.
	h := http.Header{}
	h.Set("Origin", page)
	h.Set("Access-Control-Request-Headers", "x-mimicry")
	if resp, _ := send(t, client, "OPTIONS", origin(redirCrossSite)+"/p/xhr-acao.cs", "", h); resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != page || resp.Header.Get("Access-Control-Allow-Headers") != "x-mimicry" {
		t.Errorf("preflight: %s %v", resp.Status, resp.Header)
	}
	h.Set("Origin", "null")
	h.Set("Access-Control-Request-Method", "PATCH")
	if resp, _ := send(t, client, "OPTIONS", origin(redirCrossSite)+"/p/m-patch-307.cs.hop1", "", h); resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "null" || !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "PATCH") {
		t.Errorf("PATCH hop preflight: %s %v", resp.Status, resp.Header)
	}
	if resp, _ := fetch(t, client, "GET", origin(redirCrossSite)+"/p/nope.cs", nil); resp.StatusCode != 404 {
		t.Errorf("unknown label: %s", resp.Status)
	}
	if resp, body := fetch(t, client, "GET", page+watchdogPath, nil); resp.StatusCode != 200 || body != watchdogWorker {
		t.Errorf("watchdog script: %s", resp.Status)
	}

	fetch(t, client, "GET", origin(redirCrossSite)+"/done", nil)
	var got *capture.Page
	for got == nil {
		if c := receive(t, captures); c.Finished {
			got = c.Page
		}
	}
	if len(got.H2) == 0 {
		t.Error("the page has no HTTP/2 connections")
	}
}
