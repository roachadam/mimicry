package server

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// The cookies-edge page answers each step with its cookies, chains its
// pages (each fetching its steps by path, then navigating to the next, over
// http:// where the step says), serves http:// and https:// on one port,
// deletes every cookie in the form it was set, and hands over the page,
// plain requests included, at /done.
func TestCookiesEdge(t *testing.T) {
	client, port, captures := replayServer(t, CookiesEdge)
	url := func(s CookiesEdgeStep) string {
		scheme := "https://"
		if s.Plain {
			scheme = "http://"
		}
		return scheme + s.Host + ":" + port + s.Path
	}
	steps := CookiesEdgeSteps()
	seen := map[string]bool{}
	for i, s := range steps {
		if key := s.Host + s.Path; seen[key] {
			t.Errorf("%s: %s%s is another step's too", s.Label, s.Host, s.Path)
		} else {
			seen[key] = true
		}
		resp, body := fetch(t, client, "GET", url(s), nil)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %s", s.Label, resp.Status)
			continue
		}
		if s.Plain != (resp.TLS == nil) {
			t.Errorf("%s: served over TLS %v, want plain %v", s.Label, resp.TLS != nil, s.Plain)
		}
		if got := resp.Header.Values("Set-Cookie"); !slices.Equal(got, s.Sets) {
			t.Errorf("%s: sets %q, want %q", s.Label, got, s.Sets)
		}
		if !s.Nav {
			continue
		}
		// The page fetches the steps up to the next navigation, in order,
		// then goes there; the last one asks for /done.
		rest := body
		next := `fetch("/done")`
		for _, f := range steps[i+1:] {
			if f.Nav {
				next = `location.href = "` + url(f) + `"`
				break
			}
			_, after, ok := strings.Cut(rest, `await get("`+f.Path+`")`)
			if !ok {
				t.Errorf("%s: page does not fetch %s next:\n%s", s.Label, f.Path, body)
				break
			}
			rest = after
		}
		if !strings.Contains(rest, next) {
			t.Errorf("%s: page does not end with %s:\n%s", s.Label, next, body)
		}
	}
	if last := steps[len(steps)-1]; last.Label != "cleanup" || !last.Nav {
		t.Errorf("last step %s, want the cleanup page", last.Label)
	}

	// 200 cookies in one response fit one HEADERS frame at the default
	// SETTINGS_MAX_FRAME_SIZE: the server sends no CONTINUATION.
	for _, label := range []string{"many-page", "many-cleanup"} {
		i := slices.IndexFunc(steps, func(s CookiesEdgeStep) bool {
			return s.Label == label
		})
		if n := headerBlockSize(steps[i].Sets); n > 16384 {
			t.Errorf("%s: header block of %d bytes exceeds one frame", label, n)
		}
	}

	byLabel := func(label string) []string {
		i := slices.IndexFunc(steps, func(s CookiesEdgeStep) bool {
			return s.Label == label
		})
		return steps[i].Sets
	}
	if got := byLabel("many-page"); len(got) != edgeManyCount || setCookieName(got[0]) != "e_m000" || setCookieName(got[199]) != "e_m199" {
		t.Errorf("many-page sets %d cookies, %q to %q", len(got), setCookieName(got[0]), setCookieName(got[len(got)-1]))
	}
	cleanup := byLabel("cleanup")
	for _, d := range []string{
		// Without Path, from /cleanup: the default path "/", as set.
		"__Host-e_nopath=; Secure; Max-Age=0",
		"__Host-e_pathx=; Path=/x; Secure; Max-Age=0",
		"__Host-Http-e_hh=; Path=/; Secure; HttpOnly; Max-Age=0",
		// Nameless: the whole pair, since "=" alone is ignored.
		"= __Host-e_nlhs=1; Path=/n/1/2/3/4; Secure; Max-Age=0",
		"e_sch_strict=; Path=/; Max-Age=0",
		"e_rot_a=; Path=/r; Secure; Max-Age=0",
	} {
		if !slices.Contains(cleanup, d) {
			t.Errorf("cleanup lacks %q: %q", d, cleanup)
		}
	}
	if want := len(edgePrefixCookies) + len(edgeNamelessCookies) + len(edgeSchemeCookies) + 3; len(cleanup) != want {
		t.Errorf("cleanup deletes %d cookies, want %d", len(cleanup), want)
	}
	if got := byLabel("lo-cleanup"); !slices.Equal(got, []string{"e_lo_secure=; Path=/; Secure; Max-Age=0", "e_lo_plain=; Path=/; Max-Age=0"}) {
		t.Errorf("loopback cleanup %q", got)
	}

	fetch(t, client, "GET", "https://"+edgeHost+":"+port+"/done", nil)
	var page *capture.Page
	for page == nil {
		if c := receive(t, captures); c.Finished {
			page = c.Page
		}
	}
	plain := make([]string, 0, len(page.H1))
	for _, r := range page.H1 {
		plain = append(plain, r.Host+" "+r.Line)
	}
	for _, s := range steps {
		if want := s.Host + " GET " + s.Path + " HTTP/1.1"; s.Plain && !slices.Contains(plain, want) {
			t.Errorf("page lacks plain request %q: %q", want, plain)
		}
	}
}

// headerBlockSize is the HPACK size of a response carrying sets, besides
// the standard fields.
func headerBlockSize(sets []string) int {
	var b strings.Builder
	enc := hpack.NewEncoder(&b)
	for _, f := range []hpack.HeaderField{
		{
			Name:  ":status",
			Value: "200",
		},
		{
			Name:  "content-type",
			Value: "text/html; charset=utf-8",
		},
		{
			Name:  "content-length",
			Value: "100000",
		},
		{
			Name:  "cache-control",
			Value: "no-store",
		},
	} {
		_ = enc.WriteField(f)
	}
	for _, c := range sets {
		_ = enc.WriteField(hpack.HeaderField{
			Name:  "set-cookie",
			Value: c,
		})
	}
	return b.Len()
}
