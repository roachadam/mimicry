package e2e

import (
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// Over HTTP/1.1, mimicry's two navigations (a typed URL, then /nav from the
// page with its cookies) are the browser's line for line: casing, order,
// values. Sec-Fetch-User is suppressed (script navigation); the authority is
// normalised.
func TestHTTP1MatchesBrowserTrace(t *testing.T) {
	sc, err := server.ScenarioByName("http1")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			samples := loadSamples[[]capture.H1Request](t, p, "http1", ".h1trace.json")
			jar := newJar(t, p)
			base, tr, captures := captureClientWith(t, p, sc, jar)

			page, err := url.Parse(base + "/")
			if err != nil {
				t.Fatal(err)
			}

			client := &http.Client{
				Transport: tr,
			}
			get := func(path string, r mimicry.Request, h http.Header) {
				t.Helper()

				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
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
			get("/", mimicry.Request{}, nil)
			// The page then sets these with document.cookie.
			jar.SetCookies(cookies.Request{
				URL:  page,
				Page: page,
			}, []string{
				"mimicry_a=1",
				"mimicry_b=2",
			})
			// A script navigation has no user activation.
			get("/nav", mimicry.Request{
				Kind: mimicry.Navigate,
				From: page,
			}, http.Header{
				"Sec-Fetch-User": nil,
			})
			get("/done", mimicry.Request{}, nil)

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
