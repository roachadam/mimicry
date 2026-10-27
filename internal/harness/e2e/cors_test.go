package e2e

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// The cors page's fetches through mimicry send the browser's preflights and
// requests, in order on each connection: preflight caching (Max-Age, the 5 s
// default), no request after a refused preflight (*CORSError), a redirect's
// second preflight. The profiles run in parallel: the page waits 6 s.
func TestCORSMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			want, err := loadPages(t, p, "cors", ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}

			s := startPage(t, p, server.CORS)
			s.planEvery()

			hosts, _, _ := server.CORS.MultiHost()
			page := s.url(hosts[0], "/")
			s.send(http.MethodGet, page, mimicry.Request{}, nil, "")

			from, err := url.Parse(page)
			if err != nil {
				t.Fatal(err)
			}

			for _, st := range server.CORSSteps() {
				time.Sleep(st.Wait)

				err := corsFetch(t, s, from, s.url(hosts[2], "/p/"+st.Label), st)

				var cerr *mimicry.CORSError

				switch {
				case st.Refused && !errors.As(err, &cerr):
					t.Errorf("%s: got %v, want a *mimicry.CORSError", st.Label, err)
				case !st.Refused && err != nil:
					t.Errorf("%s: %v", st.Label, err)
				}
			}

			got := corsConnections(s.finish(s.url(hosts[0], "/done")))
			for i, w := range want {
				if wc := corsConnections(w); !slices.EqualFunc(got, wc, slices.Equal) {
					t.Errorf("sample %d: connections to the cross-site host differ:\n got %q\nwant %q", i+1, got, wc)
				}
			}
		})
	}
}

// corsFetch makes step st as the page's fetch() from page from.
func corsFetch(t *testing.T, s *pageSession, from *url.URL, target string, st server.CORSStep) error {
	t.Helper()

	method := st.Method
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(t.Context(), method, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, h := range st.Headers {
		// As the page spelled it: browsers keep a page's spelling.
		req.Header[h[0]] = []string{h[1]}
	}

	r := mimicry.Request{
		Kind: mimicry.Fetch,
		From: from,
	}
	if st.Include {
		r.Credentials = mimicry.CredentialsInclude
	}

	resp, err := s.client.Do(mimicry.With(req, r))
	if err != nil {
		return err
	}

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.Body.Close()
}

// corsConnections lists, for each HTTP/2 connection to the cors page's
// cross-site host, its requests in stream order: method, path, whether it
// carried cookies, Access-Control-Request-Method and -Headers. Longest
// first.
func corsConnections(page *capture.Page) [][]string {
	var out [][]string
	for _, tr := range page.H2 {
		if !strings.HasPrefix(tr.Host, "cdn.") || len(tr.Requests) == 0 {
			continue
		}

		reqs := slices.Clone(tr.Requests)
		slices.SortFunc(reqs, func(a, b h2trace.Request) int {
			return int(a.Stream) - int(b.Stream)
		})

		var rows []string
		for _, r := range reqs {
			fields := map[string]string{}
			for _, ins := range r.Instructions {
				fields[ins.Name] = ins.Value
			}

			cookie := "-"
			if _, ok := fields["cookie"]; ok {
				cookie = "cookie"
			}

			rows = append(rows, strings.Join([]string{
				fields[":method"], r.Path, cookie,
				fields["access-control-request-method"], fields["access-control-request-headers"],
			}, " "))
		}

		out = append(out, rows)
	}

	slices.SortStableFunc(out, func(a, b []string) int {
		return len(b) - len(a)
	})

	return out
}
