package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// The cors-edge page makes every step's fetch with its method, headers and
// body; each step is redirected Hops times with its status (with or
// without Access-Control-Allow-Origin as it says) on its own host, then
// answered 200, and anything past the last hop is not found.
func TestCORSEdge(t *testing.T) {
	client, port, _ := replayServer(t, CORSEdge)
	url := func(host, path string) string {
		return "https://" + host + ":" + port + path
	}
	page := url(kindsHost, "")
	_, html := fetch(t, client, "GET", url(kindsHost, "/"), nil)
	rest := html
	for _, s := range CORSEdgeSteps() {
		call := `await f("` + url(s.Host, "/p/"+s.Label) + `", {`
		_, after, ok := strings.Cut(rest, call)
		if !ok {
			t.Fatalf("page does not fetch %s next:\n%s", s.Label, html)
		}
		rest = after
		init, _, _ := strings.Cut(after, "});")
		if s.Method != "" && !strings.Contains(init, `method: "`+s.Method+`"`) {
			t.Errorf("%s: init %s lacks its method", s.Label, init)
		}
		if s.Body != "" && !strings.Contains(init, `body: "`+s.Body+`"`) {
			t.Errorf("%s: init %s lacks its body", s.Label, init)
		}
		for _, h := range s.Headers {
			if !strings.Contains(init, `"`+h[0]+`": "`+h[1]+`"`) {
				t.Errorf("%s: init %s lacks %s", s.Label, init, h[0])
			}
		}

		method := s.Method
		if method == "" {
			method = "GET"
		}
		origin := http.Header{
			"Origin": {page},
		}
		for hop := 0; hop <= s.Hops; hop++ {
			path := "/p/" + s.Label
			if hop > 0 {
				path += ".hop" + strconv.Itoa(hop)
			}
			resp, _ := fetch(t, client, method, url(s.Host, path), origin)
			acao := resp.Header.Get("Access-Control-Allow-Origin")
			if hop == s.Hops {
				if resp.StatusCode != 200 || acao != page {
					t.Errorf("%s: %s, Access-Control-Allow-Origin %q; want 200 allowing %s", path, resp.Status, acao, page)
				}
				break
			}
			next := url(s.Host, "/p/"+s.Label+".hop"+strconv.Itoa(hop+1))
			if resp.StatusCode != s.Status || resp.Header.Get("Location") != next {
				t.Errorf("%s: %s to %q, want %d to %s", path, resp.Status, resp.Header.Get("Location"), s.Status, next)
			}
			if want := !s.NoACAO; (acao == page) != want {
				t.Errorf("%s: Access-Control-Allow-Origin %q, want it %v", path, acao, want)
			}
		}
		resp, _ := fetch(t, client, method, url(s.Host, "/p/"+s.Label+".hop"+strconv.Itoa(s.Hops+1)), origin)
		if resp.StatusCode != 404 {
			t.Errorf("%s: past the last hop: %s", s.Label, resp.Status)
		}
		resp, _ = fetch(t, client, "OPTIONS", url(s.Host, "/p/"+s.Label), http.Header{
			"Origin":                         {page},
			"Access-Control-Request-Method":  {method},
			"Access-Control-Request-Headers": {"x-mimicry"},
		})
		if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != page ||
			!strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), method) ||
			resp.Header.Get("Access-Control-Allow-Headers") != "x-mimicry" {
			t.Errorf("%s: preflight %s %v", s.Label, resp.Status, resp.Header)
		}
	}
	if !strings.Contains(rest, `fetch("/done")`) {
		t.Error("page does not end with /done")
	}

	// The chain outlasts the Fetch standard's 20 redirects.
	if s := CORSEdgeSteps()[len(CORSEdgeSteps())-1]; s.Label != "chain.so" || s.Hops <= 21 {
		t.Errorf("last step %s with %d hops, want chain.so with more than 21", s.Label, s.Hops)
	}
	// A label on another host is not this page's.
	if resp, _ := fetch(t, client, "GET", url(kindsHost, "/p/noacao-redirect.cs"), nil); resp.StatusCode != 404 {
		t.Errorf("cross-site label on the page's host: %s", resp.Status)
	}
}
