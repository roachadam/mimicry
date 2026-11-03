package e2e

import (
	"net/http"
	"slices"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/diff"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// hrrConnections is how many connections mimicry opens to the hrr page's
// retrying host: each places the cookie afresh.
const hrrConnections = 20

// mimicry's ClientHello sent again after a HelloRetryRequest with a cookie
// is the browser's on the hrr page, on every connection: the browser's
// second hello whose first was a full one, cookie and key share included.
// With a fixed extension order that pins the cookie's slot; with a shuffled
// one, where the comparison takes the extensions as a set, the cookie must
// also stay out of the pinned extensions at either end.
func TestHelloRetryMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			want, err := loadPages(t, p, server.HRR.Name, ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}

			browser := browserRetryHello(p, want)
			if browser == nil {
				t.Fatal("no browser connection retried a full ClientHello")
			}

			got := runHRRPage(t, p)

			n := 0
			for i, c := range got.Connections {
				if c.Retry == nil {
					continue
				}

				n++

				w := profile.Spec{
					TLS: *browser,
				}
				h := profile.Spec{
					TLS: *c.Retry.Hello,
				}

				w.TLS.ExtensionOrder, h.TLS.ExtensionOrder = p.TLS.ExtensionOrder, p.TLS.ExtensionOrder

				for _, d := range diff.Compare(&w, &h) {
					t.Errorf("connection %d's second ClientHello: %s", i+1, d)
				}

				if at := cookieAt(c.Retry.Hello); pinnedEnd(c.Retry.Hello.Extensions, at) {
					t.Errorf("connection %d's second ClientHello has the cookie at %d, among the pinned extensions", i+1, at)
				}
			}

			if n != hrrConnections {
				t.Errorf("%d connections sent a second ClientHello, want %d", n, hrrConnections)
			}
		})
	}
}

// browserRetryHello is the browser's second ClientHello on the hrr page
// after a first one with every extension of p's full hello, with the GREASE
// ECH choices of all of them; nil for none.
func browserRetryHello(p *profile.Spec, pages []*capture.Page) *profile.TLS {
	var hellos []*profile.TLS

	for _, page := range pages {
		for _, c := range page.Connections {
			if c.Retry == nil || c.Hello == nil || len(c.Hello.Extensions) != len(p.TLS.Extensions) {
				continue
			}

			hellos = append(hellos, c.Retry.Hello)
		}
	}

	return mergeHellos(hellos)
}

// cookieAt is the index of t's cookie extension, -1 without one.
func cookieAt(t *profile.TLS) int {
	return slices.IndexFunc(t.Extensions, func(e profile.Extension) bool {
		return e.Type == profile.ExtCookie
	})
}

// pinnedEnd reports whether index i of exts lies in the leading or trailing
// run of pinned extensions.
func pinnedEnd(exts []profile.Extension, i int) bool {
	lead := 0
	for lead < len(exts) && exts[lead].Pinned() {
		lead++
	}

	trail := len(exts)
	for trail > lead && exts[trail-1].Pinned() {
		trail--
	}

	return i < lead || i >= trail
}

// runHRRPage walks the hrr page through mimicry as profile p: the page,
// hrrConnections requests to the retrying host, each failing at its
// handshake, then /done.
func runHRRPage(t *testing.T, p *profile.Spec) *capture.Page {
	t.Helper()

	s := startPage(t, p, server.HRR)
	hosts, _, _ := server.HRR.MultiHost()
	s.send(http.MethodGet, s.url(hosts[0], "/"), mimicry.Request{}, nil, "")

	for range hrrConnections {
		err := s.do(http.MethodGet, s.url(hosts[1], "/hrr/0"), mimicry.Request{}, nil, "")
		if err == nil {
			t.Fatal("a request to the retrying host succeeded")
		}
	}

	return s.finish(s.url(hosts[0], "/done"))
}
