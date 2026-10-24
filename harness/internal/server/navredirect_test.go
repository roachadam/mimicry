package server

import (
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// Each nav-redirect page starts at /p/<label> on its first host, answered
// with its redirect to https:// of the next host's /p/<label>.hop1, whose
// page asks for /done; the page hands over both requests with their
// headers (HTTP/2 traces, or HTTP/1.1 lines for the plain start).
func TestNavRedirects(t *testing.T) {
	for _, sc := range NavRedirects {
		t.Run(sc.Name, func(t *testing.T) {
			s, ok := sc.NavRedirectStep()
			if !ok {
				t.Fatal("no step")
			}
			if !slices.Contains(Scenarios, sc) {
				t.Error("not in Scenarios")
			}
			hosts, _, _ := sc.MultiHost()
			if hosts[0] != s.Host || !slices.Contains(hosts, s.Next) {
				t.Errorf("hosts %q for %s → %s", hosts, s.Host, s.Next)
			}
			if sc.StartPath() != "/p/"+s.Label || sc.StartsPlain() != s.Plain || sc.PlainPort() != s.Plain {
				t.Errorf("starts at %s, plain %v, plain port %v", sc.StartPath(), sc.StartsPlain(), sc.PlainPort())
			}
			var client *http.Client
			var ports portPair
			var captures <-chan *capture.Capture
			if s.Plain {
				client, ports, _, captures = plainPortServer(t, sc)
			} else {
				var port string
				client, port, captures = replayServer(t, sc)
				ports = portPair{tls: port}
			}
			scheme, port := "https://", ports.tls
			if s.Plain {
				scheme, port = "http://", ports.plain
			}
			start := scheme + net.JoinHostPort(s.Host, port) + s.StartPath()
			hop := "https://" + net.JoinHostPort(s.Next, ports.tls) + s.HopPath()

			resp, _ := fetch(t, client, "GET", start, nil)
			if resp.StatusCode != s.Status || resp.Header.Get("Location") != hop {
				t.Errorf("start: %s to %q, want %d to %q", resp.Status, resp.Header.Get("Location"), s.Status, hop)
			}
			if s.Plain != (resp.TLS == nil) {
				t.Errorf("start over TLS %v, want plain %v", resp.TLS != nil, s.Plain)
			}
			resp, body := fetch(t, client, "GET", hop, nil)
			if resp.StatusCode != 200 || !strings.Contains(body, `fetch("/done")`) {
				t.Errorf("hop: %s\n%s", resp.Status, body)
			}
			// Nothing else is this page's.
			for _, u := range []string{
				"https://" + net.JoinHostPort(s.Next, ports.tls) + s.StartPath() + "x",
				scheme + net.JoinHostPort(s.Host, port) + s.HopPath() + ".hop2",
				"https://" + net.JoinHostPort(s.Host, ports.tls) + "/",
			} {
				if resp, _ := fetch(t, client, "GET", u, nil); resp.StatusCode != 404 {
					t.Errorf("%s: %s", u, resp.Status)
				}
			}

			fetch(t, client, "GET", "https://"+net.JoinHostPort(s.Next, ports.tls)+"/done", nil)
			var page *capture.Page
			for page == nil {
				if got := receive(t, captures); got.Finished {
					page = got.Page
				}
			}
			var h2 []string
			for _, tr := range page.H2 {
				for _, r := range tr.Requests {
					if len(r.Instructions) == 0 {
						t.Errorf("%s on %s recorded without its headers", r.Path, tr.Host)
					}
					h2 = append(h2, tr.Host+" "+r.Path)
				}
			}
			h1 := make([]string, 0, len(page.H1))
			for _, r := range page.H1 {
				if len(r.Headers) == 0 {
					t.Errorf("%s recorded without its headers", r.Line)
				}
				h1 = append(h1, r.Host+" "+r.Line)
			}
			if s.Plain {
				if want := s.Host + " GET " + s.StartPath() + " HTTP/1.1"; !slices.Contains(h1, want) {
					t.Errorf("page lacks %q: %q", want, h1)
				}
			} else if want := s.Host + " " + s.StartPath(); !slices.Contains(h2, want) {
				t.Errorf("page lacks %q: %q", want, h2)
			}
			if want := s.Next + " " + s.HopPath(); !slices.Contains(h2, want) {
				t.Errorf("page lacks %q: %q", want, h2)
			}
		})
	}
	if DefaultScenario.StartPath() != "/" || Redirects2.StartPath() != "/" {
		t.Error("other pages start somewhere other than /")
	}
}
