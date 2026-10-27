package e2e

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/server"
)

// The cors-edge page's fetches through mimicry follow redirects as the
// browser did, in order on each connection: the CORS check of a cross-site
// redirect (*CORSError), method and body on 301/302/303, the 20-redirect
// limit; every request with the browser's header lines and weight.
func TestCORSEdgeMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			want, err := loadPages(t, p, "cors-edge", ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}
			s := startPage(t, p, server.CORSEdge)
			s.planEvery()
			// As NewClient: the Transport stops chains where browsers do,
			// not http.Client's default 10.
			s.client.CheckRedirect = func(*http.Request, []*http.Request) error {
				return nil
			}
			hosts, _, _ := server.CORSEdge.MultiHost()
			page := s.url(hosts[0], "/")
			s.send(http.MethodGet, page, mimicry.Request{}, nil, "")
			from, err := url.Parse(page)
			if err != nil {
				t.Fatal(err)
			}
			for _, st := range server.CORSEdgeSteps() {
				err := corsEdgeFetch(t, s, from, st)
				var cerr *mimicry.CORSError
				switch {
				case st.NoACAO:
					if !errors.As(err, &cerr) || !cerr.Redirect {
						t.Errorf("%s: got %v, want a redirect's *mimicry.CORSError", st.Label, err)
					}
				case st.Hops > 20:
					if err == nil || !strings.Contains(err.Error(), "stopped after 20 redirects") {
						t.Errorf("%s: got %v, want stopped after 20 redirects", st.Label, err)
					}
				case err != nil:
					t.Errorf("%s: %v", st.Label, err)
				}
			}
			done := s.finish(s.url(hosts[0], "/done"))
			got := corsEdgeConnections(done)
			for i, w := range want {
				if wc := corsEdgeConnections(w); !slices.EqualFunc(got, wc, slices.Equal) {
					t.Errorf("sample %d: connections differ:\n got %q\nwant %q", i+1, got, wc)
				}
			}
			for _, path := range corsEdgePaths(want[0]) {
				for _, preflight := range []bool{false, true} {
					comparePath(t, done, want, path, false, false, preflight)
				}
			}
		})
	}
}

// corsEdgePaths are the paths of page's fetches (/p/), first legs and
// hops, each once, sorted.
func corsEdgePaths(page *capture.Page) []string {
	var out []string
	for _, tr := range page.H2 {
		for _, r := range tr.Requests {
			if strings.HasPrefix(r.Path, "/p/") && !slices.Contains(out, r.Path) {
				out = append(out, r.Path)
			}
		}
	}
	slices.Sort(out)
	return out
}

// corsEdgeFetch makes step st as the page's fetch() from page from: a
// string body goes as text/plain;charset=UTF-8.
func corsEdgeFetch(t *testing.T, s *pageSession, from *url.URL, st server.CORSEdgeStep) error {
	t.Helper()
	method := st.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if st.Body != "" {
		body = strings.NewReader(st.Body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, s.url(st.Host, "/p/"+st.Label), body)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range st.Headers {
		// As the page spelled it: browsers keep a page's spelling.
		req.Header[h[0]] = []string{h[1]}
	}
	if st.Body != "" {
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	}
	resp, err := s.client.Do(mimicry.With(req, mimicry.Request{
		Kind: mimicry.Fetch,
		From: from,
	}))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// corsEdgeConnections lists, for each HTTP/2 connection of page, its host
// and requests in stream order: method, path, body length (its DATA
// frames) and Origin (port dropped). Sorted by host, then longest first.
func corsEdgeConnections(page *capture.Page) [][]string {
	var out [][]string
	for _, tr := range page.H2 {
		if len(tr.Requests) == 0 {
			continue
		}
		data := map[uint32]uint32{}
		for _, f := range tr.Frames {
			if f.Type == "DATA" {
				data[f.Stream] += f.Length
			}
		}
		reqs := slices.Clone(tr.Requests)
		slices.SortFunc(reqs, func(a, b h2trace.Request) int {
			return int(a.Stream) - int(b.Stream)
		})
		rows := []string{tr.Host}
		for _, r := range reqs {
			method, origin := "", "-"
			for _, ins := range r.Instructions {
				switch ins.Name {
				case ":method":
					method = ins.Value
				case "origin":
					origin = testPort.ReplaceAllString(ins.Value, ".test")
				}
			}
			rows = append(rows, strings.Join([]string{
				method, r.Path, strconv.Itoa(int(data[r.Stream])), origin,
			}, " "))
		}
		out = append(out, rows)
	}
	slices.SortStableFunc(out, func(a, b []string) int {
		if c := strings.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return len(b) - len(a)
	})
	return out
}
