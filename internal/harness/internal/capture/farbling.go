package capture

import (
	"regexp"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

// Brave's Shields farble Accept-Language (decision 0066): its last
// language takes a q value drawn per top-level page origin and session,
// so every sample of Brave sends another. The captures keep what was sent.
// The generators and the e2e comparisons read them unfarbled (Unfarble*):
// every q value replaced by UnfarbledQ, the browser's own, which its CORS
// preflights send (Shields leave them alone). The profile's lists carry
// it, mimicry under test sends it, and the farbling itself is derived
// (profile.Context.AcceptLanguageQ, kindsgen) and checked on its own
// (e2e TestAcceptLanguageFarbling).

// UnfarbledQ is the q value Brave's Accept-Language carries where Shields
// leave it alone: on its CORS preflights, en-US,en;q=0.9 in every capture.
const UnfarbledQ = "0.9"

// farbledLanguage matches an Accept-Language value whose last language
// carries a one-digit q value: a farbled one, in a farbling browser's
// capture.
var farbledLanguage = regexp.MustCompile(`^(.+;q=)(0\.[0-9])$`)

// Farbles reports whether browser (ParseUserAgent's name, or Identify's
// named) farbles its Accept-Language per site and session: Brave.
func Farbles(browser string) bool {
	return browser == "brave"
}

// FarbledQ is the q value of v, an Accept-Language value, and the value
// unfarbled (with UnfarbledQ), when v carries one; ok false otherwise.
func FarbledQ(v string) (q, unfarbled string, ok bool) {
	m := farbledLanguage.FindStringSubmatch(v)
	if m == nil {
		return "", "", false
	}

	return m[2], m[1] + UnfarbledQ, true
}

// unfarble returns v unfarbled when name is Accept-Language and v carries
// a q value, v otherwise.
func unfarble(name, v string) string {
	if !strings.EqualFold(name, "accept-language") {
		return v
	}

	if _, u, ok := FarbledQ(v); ok {
		return u
	}

	return v
}

// UnfarbleHeaders unfarbles the Accept-Language of hs, a profile's list as
// a capture recorded it, when browser farbles.
func UnfarbleHeaders(browser string, hs []profile.Header) {
	if !Farbles(browser) {
		return
	}

	for i := range hs {
		hs[i].Value = unfarble(hs[i].Name, hs[i].Value)
	}
}

// UnfarblePages unfarbles every request of pages, a capture of browser,
// when browser farbles.
func UnfarblePages(browser string, pages []*Page) {
	if !Farbles(browser) {
		return
	}

	for _, p := range pages {
		unfarbleTraces(p.H2)
		unfarbleH1(p.H1)

		for _, q := range p.QUIC {
			if q.H3 == nil {
				continue
			}

			for i := range q.H3.Requests {
				fs := q.H3.Requests[i].Section.Fields
				for j := range fs {
					fs[j].Value = unfarble(fs[j].Name, fs[j].Value)
				}
			}
		}
	}
}

// UnfarbleTraces unfarbles every request of traces, an HTTP/2 capture of
// browser, when browser farbles. HPACK reproduction reads them as sent.
func UnfarbleTraces(browser string, traces []*h2trace.Trace) {
	if Farbles(browser) {
		unfarbleTraces(traces)
	}
}

// UnfarbleH1 unfarbles every request of reqs, an HTTP/1.1 capture of
// browser, when browser farbles.
func UnfarbleH1(browser string, reqs []H1Request) {
	if Farbles(browser) {
		unfarbleH1(reqs)
	}
}

func unfarbleTraces(traces []*h2trace.Trace) {
	for _, tr := range traces {
		for i := range tr.Requests {
			ins := tr.Requests[i].Instructions
			for j := range ins {
				ins[j].Value = unfarble(ins[j].Name, ins[j].Value)
			}
		}
	}
}

func unfarbleH1(reqs []H1Request) {
	for i := range reqs {
		for j, l := range reqs[i].Headers {
			name, value, ok := strings.Cut(l, ": ")
			if !ok {
				continue
			}

			if u := unfarble(name, value); u != value {
				reqs[i].Headers[j] = name + ": " + u
			}
		}
	}
}

// alignFarbling unfarbles every sample's Accept-Language where browser
// farbles, so that Combine compares the rest; the samples' traces keep
// what was sent.
func alignFarbling(samples []*Capture) {
	browser := samples[0].Profile.Browser
	for _, s := range samples {
		UnfarbleHeaders(browser, s.Profile.Headers)
	}
}
