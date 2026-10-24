package e2e

import (
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profiles"
)

// Cookies a server sets come back from mimicry, through its cookie jar, as
// the browser sent them on its first request after: the same crumbs in the
// same order, each with the same representation and Huffman use (Chrome
// indexes every crumb; Firefox and Safari never index those under 20 bytes;
// Safari orders them by name). Table indices are left out: the browser's
// earlier subresource requests filled its table differently.
func TestCookiesMatchBrowserTrace(t *testing.T) {
	sc, err := server.ScenarioByName("set-cookie")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			base, tr, captures := captureClientWith(t, p, sc, newJar(t, p))
			client := &http.Client{
				Transport: tr,
			}
			for _, path := range []string{"/", "/again"} {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			tr.CloseIdleConnections()
			got := cookieShapes((<-captures).Trace.Requests[1])

			traces, err := h2trace.Load(captureDir + name + ".set-cookie.h2trace.json")
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range traces {
				var first h2trace.Request
				for _, r := range want.Requests {
					if len(cookieShapes(r)) > 0 {
						first = r
						break
					}
				}
				if w := cookieShapes(first); !slices.Equal(got, w) {
					t.Errorf("sample %d (%s):\n got %v\nwant %v", i+1, first.Path, got, w)
				}
			}
		})
	}
}

// cookieShapes lists a request's cookie crumbs as name, representation and
// Huffman use.
func cookieShapes(r h2trace.Request) []string {
	var out []string
	for _, ins := range r.Instructions {
		if ins.Name == "cookie" {
			name, _, _ := strings.Cut(ins.Value, "=")
			out = append(out, name+" "+string(ins.Rep)+" huffman="+strconv.FormatBool(ins.HuffmanValue))
		}
	}
	return out
}
