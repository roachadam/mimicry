package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/dnsstub"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/server"
)

// connectionsPreconnects are the connections a browser opened on the
// connections page and never used, which mimicry does not open: host,
// one entry per connection. Chrome preconnects to the page's host once
// more (a handshake, no HTTP/2 preface, in every sample).
var connectionsPreconnects = map[string][]string{
	"chrome-155-macos": {
		"www.conns-a.test",
	},
}

// The connections page through mimicry opens the browser's connections:
// same hosts, protocols and requests per connection, the same coalescing
// (Firefox's declined connections included), and the 10 parallel HTTP/1.1
// requests spread over 6 connections.
func TestConnectionsMatchBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			want, err := loadPages(t, p, "connections", ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}

			s := startPage(t, p, server.Connections)
			hosts, _, _ := server.Connections.MultiHost()
			page := s.url(hosts[0], "/")
			s.send(http.MethodGet, page, mimicry.Request{}, nil, "")

			from, err := url.Parse(page)
			if err != nil {
				t.Fatal(err)
			}

			for _, st := range server.ConnectionsSteps() {
				time.Sleep(300 * time.Millisecond)
				connectionsStep(t, s, from, st)
			}

			got := s.finish(s.url(hosts[0], "/done"))
			gotConns, gotGroups := connectionsSeen(got, nil), connectionsGrouping(got)

			for i, w := range want {
				if wc := connectionsSeen(w, connectionsPreconnects[capture.Source(p, "connections")]); !slices.Equal(gotConns, wc) {
					t.Errorf("sample %d: connections differ:\n got %q\nwant %q", i+1, gotConns, wc)
				}

				if wg := connectionsGrouping(w); !slices.Equal(gotGroups, wg) {
					t.Errorf("sample %d: requests per connection differ:\n got %q\nwant %q", i+1, gotGroups, wg)
				}
			}
		})
	}
}

// connectionsStep makes st's requests as the page does: no-cors fetches
// with credentials, all at once. They start in the page's order, a few
// milliseconds apart, so they reach the connection pool in that order
// (the page makes them in one synchronous loop).
func connectionsStep(t *testing.T, s *pageSession, from *url.URL, st server.ConnectionsStep) {
	t.Helper()

	var wg sync.WaitGroup
	for _, label := range st.Labels {
		target := s.url(st.Host, "/c/"+label)
		wg.Go(func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			if err != nil {
				t.Error(err)

				return
			}

			resp, err := s.client.Do(mimicry.With(req, mimicry.Request{
				Kind:        mimicry.Fetch,
				From:        from,
				NoCORS:      true,
				Credentials: mimicry.CredentialsInclude,
			}))
			if err != nil {
				t.Errorf("%s: %v", label, err)

				return
			}

			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		})
		time.Sleep(20 * time.Millisecond)
	}

	wg.Wait()
}

// connectionsSeen lists page's connections as "host protocol requests",
// sorted, leaving out one unused connection to each host in skip.
func connectionsSeen(page *capture.Page, skip []string) []string {
	skip = slices.Clone(skip)

	var out []string

	for _, c := range page.Connections {
		if i := slices.Index(skip, c.Host); i >= 0 && c.Requests == 0 {
			skip = slices.Delete(skip, i, i+1)
			continue
		}

		out = append(out, fmt.Sprintf("%s %s %d", c.Host, c.Protocol, c.Requests))
	}

	slices.Sort(out)

	return out
}

// connectionsGrouping lists, sorted, each connection's requests: on an
// HTTP/2 connection, each request's host and path in stream order, or
// the frames of one that carried none; on an HTTP/1.1 one, whether each
// request, in order, was among the first six the page made ("early") or
// the four after ("late"), which go out as connections free up.
func connectionsGrouping(page *capture.Page) []string {
	labels := map[int][]string{}

	var conns []int

	for _, r := range page.H1 {
		_, rest, _ := strings.Cut(r.Line, " ")
		target, _, _ := strings.Cut(rest, " ")
		label := strings.TrimPrefix(target, "/c/")

		if !slices.Contains(conns, r.Conn) {
			conns = append(conns, r.Conn)
		}

		labels[r.Conn] = append(labels[r.Conn], label)
	}

	out := make([]string, 0, len(page.H2)+len(conns))
	for _, tr := range page.H2 {
		out = append(out, tr.Host+": "+strings.Join(h2Requests(tr), ", "))
	}

	parallel := server.ConnectionsSteps()[len(server.ConnectionsSteps())-1].Labels
	for _, c := range conns {
		var row []string
		for _, l := range labels[c] {
			switch i := slices.Index(parallel, l); {
			case i < 0:
				row = append(row, l)
			case i < 6:
				row = append(row, "early")
			default:
				row = append(row, "late")
			}
		}

		out = append(out, "http/1.1: "+strings.Join(row, " "))
	}

	slices.Sort(out)

	return out
}

// h2Requests are tr's requests in stream order as "host path", or, if it
// carried none, its frame types.
func h2Requests(tr *h2trace.Trace) []string {
	if len(tr.Requests) == 0 {
		frames := make([]string, 0, len(tr.Frames))
		for _, f := range tr.Frames {
			frames = append(frames, f.Type)
		}

		return []string{
			"no requests (" + strings.Join(frames, " ") + ")",
		}
	}

	reqs := slices.Clone(tr.Requests)
	slices.SortFunc(reqs, func(a, b h2trace.Request) int {
		return int(a.Stream) - int(b.Stream)
	})

	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		authority := ""
		for _, ins := range r.Instructions {
			if ins.Name == ":authority" {
				authority = ins.Value
			}
		}

		host, _, err := net.SplitHostPort(authority)
		if err != nil {
			host = authority
		}

		out = append(out, host+" "+r.Path)
	}

	return out
}

// stubResolver is a resolver answering as the capture machines' DNS does
// (dnsstub): every name under .test is 127.0.0.1, where the test servers
// listen.
func stubResolver(t *testing.T) *net.Resolver {
	t.Helper()

	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() {
		// Serve stops as the test ends, before its cleanups run.
		done <- dnsstub.Serve(t.Context(), pc)
	}()

	t.Cleanup(func() {
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	addr := pc.LocalAddr().String()

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", addr)
		},
	}
}
