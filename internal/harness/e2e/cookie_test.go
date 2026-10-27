package e2e

import (
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// Cookies a server sets come back through mimicry's jar as the browser sent
// them: same crumbs, order, HPACK representation and Huffman use. Table
// indices are ignored: the browser's earlier requests filled its table.
func TestCookiesMatchBrowserTrace(t *testing.T) {
	sc, err := server.ScenarioByName("set-cookie")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
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

			traces, err := loadH2Traces(t, p, "set-cookie")
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
