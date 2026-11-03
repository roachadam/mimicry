package e2e

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// The bodiless page through mimicry (runH3Page): fetch() POST, PUT and
// PATCH and a form POST with no body, over HTTP/2 and then HTTP/3, against
// every sample. Each goes over the protocol the browser's did, with the
// browser's header list (content-length: 0 and its place, profile
// EmptyBody) and the browser's end of stream: END_STREAM on HEADERS or an
// empty DATA frame over HTTP/2, the HEADERS frame and FIN over HTTP/3.
// A request in bodilessGaps must differ only in its header order, its
// content-length: 0 in the browser's place, and must still differ.
func TestBodilessMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			want := h3Captures(t, p, server.Bodiless)
			got := runH3Page(t, p, server.Bodiless, want[0])
			gotT := transports(got)

			for _, st := range server.BodilessSteps() {
				if st.Method == "" {
					continue
				}

				path := st.Path()
				compareTransport(t, path, gotT, want, func(string) bool {
					return false
				})

				var forms []string

				for _, w := range want {
					if f := bodilessSent(w, path); !slices.Contains(forms, f) {
						forms = append(forms, f)
					}
				}

				g := bodilessSent(got, path)
				gap, pinned := bodilessGaps[capture.Source(p, server.Bodiless.Name)+" "+path]

				switch {
				case pinned && slices.Contains(forms, g):
					t.Errorf("%s: matches the browser, but bodilessGaps pins a difference: %s", path, gap)
				case pinned && slices.ContainsFunc(forms, func(f string) bool {
					return sameButOrder(g, f)
				}):
					t.Logf("%s: header order differs (bodilessGaps): %s", path, gap)
				case !slices.Contains(forms, g):
					t.Errorf("%s:\n got %s\nwant %s", path, g, strings.Join(forms, "\n  or "))
				}
			}
		})
	}
}

// safariBodilessOrder is Safari 27's bodiless fetch() gap (bodilessGaps).
const safariBodilessOrder = "Safari orders its own headers by its header table (context.headerTable), and its bodiless fetch() POST, PUT and PATCH start sec-fetch-site, accept, origin (no content-type in the table), where the lists mimicry takes for them, measured with a body and content-type, start accept, origin, sec-fetch-site; mimicry applies the table only to page-set headers (decision 0021). content-length: 0 is in Safari's place and the stream ends on HEADERS, as captured"

// bodilessGaps are known differences, by "<capture's profile> <path>": the
// header order alone (sameButOrder).
var bodilessGaps = map[string]string{
	"safari-27-macos /h3/fetch-post.h2":  safariBodilessOrder,
	"safari-27-macos /h3/fetch-put.h2":   safariBodilessOrder,
	"safari-27-macos /h3/fetch-patch.h2": safariBodilessOrder,
	"safari-27-macos /h3/fetch-post.h3":  safariBodilessOrder,
	"safari-27-macos /h3/fetch-put.h3":   safariBodilessOrder,
	"safari-27-macos /h3/fetch-patch.h3": safariBodilessOrder,
}

// sameButOrder reports whether bodilessSent forms a and b have the same
// protocol, frames and header lines, content-length at the same index,
// whatever the order of the others.
func sameButOrder(a, b string) bool {
	aHead, aLines, _ := strings.Cut(a, "] ")
	bHead, bLines, _ := strings.Cut(b, "] ")
	al, bl := strings.Split(aLines, "; "), strings.Split(bLines, "; ")
	at := slices.IndexFunc(al, func(l string) bool {
		return strings.HasPrefix(l, "content-length: ")
	})
	bt := slices.IndexFunc(bl, func(l string) bool {
		return strings.HasPrefix(l, "content-length: ")
	})

	slices.Sort(al)
	slices.Sort(bl)

	return aHead == bHead && at == bt && slices.Equal(al, bl)
}

// bodilessSent is page's first request for path as compared: its protocol,
// header lines (ports replaced) and frames, with how its stream ended.
func bodilessSent(page *capture.Page, path string) string {
	for _, tr := range page.H2 {
		for _, r := range tr.Requests {
			if r.Path != path {
				continue
			}

			var frames []string

			for _, f := range tr.Frames {
				if f.Stream != r.Stream || (f.Type != "HEADERS" && f.Type != "DATA" && f.Type != "CONTINUATION") {
					continue
				}

				s := f.Type
				if f.Type == "DATA" {
					s += fmt.Sprintf("(%d)", f.Length)
				}

				if f.Flags&0x1 != 0 {
					s += " END_STREAM"
				}

				frames = append(frames, s)
			}

			return "HTTP/2 [" + strings.Join(frames, ", ") + "] " + strings.Join(requestFields(page, path, false, false).lines, "; ")
		}
	}

	for _, c := range page.QUIC {
		if c.H3 == nil {
			continue
		}

		for _, r := range c.H3.Requests {
			if r.Path != path {
				continue
			}

			var frames, lines []string

			for _, f := range r.Frames {
				s := f.Name
				if f.Type == quictrace.FrameH3Data {
					s += fmt.Sprintf("(%d)", f.Length)
				}

				frames = append(frames, s)
			}

			for _, f := range r.Section.Fields {
				if !strings.HasPrefix(f.Name, ":") {
					lines = append(lines, normalisePort(f.Name+": "+f.Value))
				}
			}

			return "HTTP/3 [" + strings.Join(frames, ", ") + " " + r.End + "] " + strings.Join(lines, "; ")
		}
	}

	return "not sent"
}
