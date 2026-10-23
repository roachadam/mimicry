package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/publicsuffix"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profiles"
)

// Over HTTP/1.1, mimicry's navigations are the browser's line for line:
// header names in the browser's casing and order, the same values, and one
// Cookie line in the browser's cookie order. The first navigation uses the
// profile's defaults; for the second (/nav, with a referer and the page's
// cookies) the test sets what a caller would for that navigation: headers
// whose values the browser changed, and suppression of ones it dropped.
// Host and URLs are compared with the authority replaced, since the browser
// was served on another name and port.
func TestHTTP1MatchesBrowserTrace(t *testing.T) {
	sc, err := server.ScenarioByName("http1")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			samples := loadH1Traces(t, name)
			want := navigations(t, samples[0])
			base, tr, captures := captureClient(t, p, sc)
			jar, err := cookiejar.New(&cookiejar.Options{
				PublicSuffixList: publicsuffix.List,
			})
			if err != nil {
				t.Fatal(err)
			}
			baseURL, err := url.Parse(base)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{
				Transport: tr,
				Jar:       jar,
			}
			get := func(path string, h http.Header) {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				for k, v := range h {
					req.Header[k] = v
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			get("/", nil)
			// The page then sets these with document.cookie.
			jar.SetCookies(baseURL, []*http.Cookie{
				{ //nolint:gosec // as document.cookie sets them: no HttpOnly, no SameSite
					Name:  "mimicry_a",
					Value: "1",
				},
				{ //nolint:gosec // as document.cookie sets them: no HttpOnly, no SameSite
					Name:  "mimicry_b",
					Value: "2",
				},
			})
			get("/nav", callerHeaders(want[0], want[1], baseURL.Host))
			get("/done", nil)
			c := <-captures
			if c.Protocol != "http/1.1" {
				t.Fatalf("negotiated %q, want http/1.1", c.Protocol)
			}
			got := [][]string{
				normalize(c.H1[0].Headers),
				normalize(c.H1[1].Headers),
			}
			for i, s := range samples {
				for j, w := range navigations(t, s) {
					if !slices.Equal(got[j], w) {
						t.Errorf("sample %d %s:\n got %q\nwant %q", i+1, []string{"/", "/nav"}[j], got[j], w)
					}
				}
			}
		})
	}
}

func loadH1Traces(t *testing.T, name string) [][]capture.H1Request {
	t.Helper()
	data, err := os.ReadFile(captureDir + name + ".http1.h1trace.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples [][]capture.H1Request
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatal(err)
	}
	return samples
}

// navigations returns a sample's first navigation and its /nav, normalized.
func navigations(t *testing.T, sample []capture.H1Request) [][]string {
	t.Helper()
	out := make([][]string, 0, 2)
	for _, path := range []string{"/", "/nav"} {
		i := slices.IndexFunc(sample, func(r capture.H1Request) bool {
			return strings.HasPrefix(r.Line, "GET "+path+" ")
		})
		if i < 0 {
			t.Fatalf("sample has no GET %s", path)
		}
		out = append(out, normalize(sample[i].Headers))
	}
	return out
}

// normalize replaces the request's authority, from its Host line, with
// {host} in every line.
func normalize(lines []string) []string {
	var host string
	for _, l := range lines {
		if name, value, _ := strings.Cut(l, ":"); strings.EqualFold(name, "host") {
			host = strings.TrimSpace(value)
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.ReplaceAll(l, host, "{host}")
	}
	return out
}

// callerHeaders returns what a caller sets to turn the first navigation
// (first) into the second (nav): headers the browser added or changed,
// except Host and Cookie, which the client and jar supply, with the authority
// put back as host; and nil for headers it dropped.
func callerHeaders(first, nav []string, host string) http.Header {
	values := func(lines []string) map[string]string {
		m := make(map[string]string, len(lines))
		for _, l := range lines {
			name, value, _ := strings.Cut(l, ":")
			m[http.CanonicalHeaderKey(name)] = strings.TrimSpace(value)
		}
		return m
	}
	before, after := values(first), values(nav)
	h := http.Header{}
	for name, v := range after {
		if name == "Host" || name == "Cookie" || before[name] == v {
			continue
		}
		h[name] = []string{
			strings.ReplaceAll(v, "{host}", host),
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			h[name] = nil
		}
	}
	return h
}
