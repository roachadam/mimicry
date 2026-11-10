package e2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// urlsKnown are the urls cases mimicry does not send as the browser
// measured does, fetched or navigated to, and why: keyed by case for
// every profile, or "<profile>/<case>" for one. TestURLsMatchBrowser
// fails once one matches every sample, so the entry goes.
var urlsKnown = func() map[string]string {
	known := map[string]string{
		"p-lonepct": "Go's url.Parse refuses a path with a lone %, so http.NewRequest fails before mimicry sees the URL (URL.Opaque sends it); browsers send it as written",
	}

	const chromePool = "Chrome sent the fetch() of www.urls.test. on its www.urls.test connection and opened a new one for the navigation (its hello, keeping the dot, refused); mimicry runs the case once and coalesces it as the fetch. A navigation's own connection would be top-level-site partitioning, measured but not modelled (decisions 0011, 0029)"
	for _, name := range []string{"chrome-156-macos", "chrome-156-windows", "chrome-157-linux", "edge-157-macos", "edge-157-windows", "brave-157-macos", "brave-157-windows", "brave-157-linux"} {
		known[name+"/h-dotpool"] = chromePool
	}

	const firefoxPool = "Firefox sent the navigation to www.urls.test. on its www.urls.test connection and opened a new one for the credential-less fetch() (its hello, keeping the dot, refused); mimicry runs the case once and opens a new connection as the fetch. Which requests Firefox groups on a connection is a known gap (decision 0011)"
	for _, name := range []string{"firefox-158-macos", "firefox-158-windows", "firefox-159-macos", "firefox-159-windows", "firefox-159-linux"} {
		known[name+"/h-dotpool"] = firefoxPool
	}

	return known
}()

// The urls page's URLs through mimicry, each given to http.NewRequest as
// the page writes it (server.URLCases), against every sample: for each, the
// request target after the case's prefix, Host or :authority (ports
// replaced) and SNI must be what the browser sent for it fetched and
// navigated to, or nothing when the browser sent nothing (a URL it
// refuses), but for the cases in urlsKnown.
func TestURLsMatchBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			want, err := loadPages(t, p, server.URLs.Name, ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}

			got := runURLs(t, p)
			differs := map[string]bool{}

			for i, page := range want {
				tlsPort, plainPort := urlsReportPorts(t, page)
				sent := urlsSent(page, tlsPort, plainPort)

				for _, c := range server.URLCases() {
					for _, kind := range []string{"f", "n"} {
						b, m := sent[kind+"-"+c.ID], got["m-"+c.ID]
						if b == m {
							continue
						}

						differs[c.ID] = true

						if !urlKnown(name, c.ID) {
							t.Errorf("sample %d, %s-%s %s:\n  browser %s\n  mimicry %s", i, kind, c.ID, c.URL, b, m)
						}
					}
				}
			}

			for _, c := range server.URLCases() {
				if urlKnown(name, c.ID) && !differs[c.ID] {
					t.Errorf("%s matches every sample now: drop it from urlsKnown", c.ID)
				}
			}
		})
	}
}

// urlKnown reports whether urlsKnown lists case id for every profile or
// for name.
func urlKnown(name, id string) bool {
	_, all := urlsKnown[id]
	_, one := urlsKnown[name+"/"+id]

	return all || one
}

// runURLs has mimicry, as p, send every urls case (kind "m") and returns
// what reached the server (urlsSent).
func runURLs(t *testing.T, p *profile.Spec) map[string]string {
	t.Helper()

	s := startPage(t, p, server.URLs)

	for _, c := range server.URLCases() {
		if err := s.do(http.MethodGet, c.On("m", s.port, s.plainPort), mimicry.Request{}, nil, ""); err != nil {
			t.Logf("%s: %v", c.ID, err)
		}
	}

	return urlsSent(s.finish(s.url("www.urls.test", "/done")), s.port, s.plainPort)
}

// urlsReportPorts returns the ports a urls page reported it ran on.
func urlsReportPorts(t *testing.T, page *capture.Page) (tlsPort, plainPort string) {
	t.Helper()

	for _, r := range wireRequests(page) {
		path, query, _ := strings.Cut(r.path, "?")
		if path != "/report" {
			continue
		}

		v, err := url.ParseQuery(query)
		if err != nil {
			t.Fatalf("report %q: %v", r.path, err)
		}

		if v.Get("failed") != "" {
			t.Logf("the page reports failing: %s", v.Get("failed"))
		}

		return v.Get("tls"), v.Get("plain")
	}

	t.Fatal("no /report request")

	return "", ""
}

// urlsSent returns what page's requests for the urls cases sent, by kind
// and case ("f-q-squote"): the method, the target after the case's prefix,
// Host or :authority with the ports as {tls} and {plain}, and SNI. A case
// of the page's kinds ("f", "n" or "m") with no request is "nothing
// sent", or, when the server refused a ClientHello naming its host (a
// trailing dot, capture.Connection.Refused), what that hello named.
func urlsSent(page *capture.Page, tlsPort, plainPort string) map[string]string {
	ports := strings.NewReplacer(":"+tlsPort, ":{tls}", ":"+plainPort, ":{plain}")
	sent := map[string]string{}
	kinds := map[string]bool{}

	for _, r := range wireRequests(page) {
		rest, ok := strings.CutPrefix(r.path, "/c/")
		if !ok {
			continue
		}

		key, target, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}

		kind, _, _ := strings.Cut(key, "-")

		kinds[kind] = true

		if _, dup := sent[key]; !dup {
			sent[key] = r.method + " /" + target + " host=" + ports.Replace(r.host) + " sni=" + r.via.host
		}
	}

	refused := map[string]bool{}

	for _, c := range page.Connections {
		if c.Refused != "" {
			refused[c.Host] = true
		}
	}

	for kind := range kinds {
		for _, c := range server.URLCases() {
			if _, ok := sent[kind+"-"+c.ID]; ok {
				continue
			}

			sent[kind+"-"+c.ID] = "nothing sent"

			if u, err := url.Parse(c.On(kind, "1", "2")); err == nil && refused[strings.ToLower(u.Hostname())] {
				sent[kind+"-"+c.ID] = "nothing sent, hello refused: sni=" + strings.ToLower(u.Hostname())
			}
		}
	}

	return sent
}
