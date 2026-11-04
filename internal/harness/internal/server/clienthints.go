package server

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// The client-hints page's hosts: the site that opts in (chHost), another
// origin of the same site (chSub), a third party (chThird), a fresh site
// for Critical-CH (chCritical), one offered only HTTP/1.1 (chHTTP1) and one
// advertising HTTP/3 (chHTTP3). Only chHTTP3 sends Alt-Svc.
const (
	chHost     = "www.ch-a.test"
	chSub      = "sub.ch-a.test"
	chThird    = "www.ch-b.test"
	chCritical = "www.ch-k.test"
	chHTTP1    = "www.ch-h1.test"
	chHTTP3    = "www.ch-h3.test"
)

// ClientHints is the client-hints scenario: a page that opts in to every
// client hint with Accept-CH and then asks for them on its own origin,
// another origin of its site and a third party (with and without
// Permissions-Policy or a Delegate-CH meta tag delegating them), narrows
// and clears its opt-in (Clear-Site-Data), and answers navigations with
// Critical-CH on a fresh site, over HTTP/2, HTTP/1.1 and HTTP/3. See
// clientHintsSteps.
var ClientHints = &Scenario{
	Name: "client-hints",
	Doc:  "Accept-CH for every client hint on a navigation, then same-origin, same-site and third-party requests (subresources, fetch, iframes, navigations; Permissions-Policy and Delegate-CH delegation), Accept-CH on a subresource, a narrower Accept-CH, Clear-Site-Data \"clientHints\" and \"*\", and Critical-CH restarts (new hint, hint already sent, hints a navigation never sends) over HTTP/2, HTTP/1.1 and HTTP/3: which hints each browser sends, their values and places",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				chHost,
				chSub,
				chThird,
				chCritical,
				chHTTP1,
				chHTTP3,
			},
			http1: []string{
				chHTTP1,
			},
			runFor: 45 * time.Second,
		},
		steps: clientHintsSteps,
		plainHosts: []string{
			chHost,
			chSub,
			chThird,
			chCritical,
			chHTTP1,
		},
		headers: clientHintsHeaders,
		head:    clientHintsHead,
	},
	h3: &h3Server{},
}

// allHints are every client hint Chrome is known to accept, low-entropy
// ones included, plus Save-Data, which is no client hint but is listed
// beside them.
var allHints = []string{
	"Sec-CH-UA",
	"Sec-CH-UA-Mobile",
	"Sec-CH-UA-Platform",
	"Sec-CH-UA-Arch",
	"Sec-CH-UA-Bitness",
	"Sec-CH-UA-Full-Version",
	"Sec-CH-UA-Full-Version-List",
	"Sec-CH-UA-Model",
	"Sec-CH-UA-Platform-Version",
	"Sec-CH-UA-WoW64",
	"Sec-CH-UA-Form-Factor",
	"Sec-CH-UA-Form-Factors",
	"Sec-CH-Prefers-Color-Scheme",
	"Sec-CH-Prefers-Reduced-Motion",
	"Sec-CH-Prefers-Reduced-Transparency",
	"Sec-CH-DPR",
	"Sec-CH-Viewport-Width",
	"Sec-CH-Viewport-Height",
	"Sec-CH-Device-Memory",
	"Sec-CH-Width",
	"DPR",
	"Viewport-Width",
	"Device-Memory",
	"Width",
	"ECT", //nolint:misspell // the Effective Connection Type hint, not "etc"
	"RTT",
	"Downlink",
	"Save-Data",
}

// hintStep is a step of the client-hints page and what its response
// carries: Accept-CH, Critical-CH, Clear-Site-Data and Permissions-Policy
// (empty: none); delegate, on a navigation, is the content of a Delegate-CH
// meta tag in its document. In policy and delegate, %[1]s is the third
// party's origin.
type hintStep struct {
	H3Step
	acceptCH      string
	criticalCH    string
	clearSiteData string
	policy        string
	delegate      string
}

// hintSteps lists the client-hints page's requests in order (each on the
// document its Page names):
//
//   - on chHost's / (f, the baseline): a fetch, an image and a script, a
//     third-party fetch, and a fetch whose response carries Accept-CH (a
//     subresource's opt-in, which the next requests show kept or not);
//   - optin.nav answers Accept-CH for allHints (a, g); its document makes
//     the same requests, to its own origin, chSub and the third party,
//     iframes included; next.nav (no Accept-CH) makes them again with an
//     image with sizes (Sec-CH-Width), then navigations to chSub and to the
//     third party (c);
//   - deleg.nav delegates every hint to the third party with
//     Permissions-Policy, meta.nav with a Delegate-CH meta tag (d);
//   - shrink.nav narrows Accept-CH to one hint, clear.nav answers
//     Clear-Site-Data "clientHints" (e), each followed by a navigation and
//     a fetch;
//   - on chCritical (b): Critical-CH for a hint not yet sent (a restart),
//     the same again (already sent), a new hint beside sent ones, a hint
//     Permissions-Policy disables, hints a navigation never sends (Width,
//     Save-Data) and an unknown one, each answered alike every time, so a
//     restart that loops shows; then Clear-Site-Data "*";
//   - the opt-in with Critical-CH and its requests over HTTP/1.1 (chHTTP1),
//     and over HTTP/3 (chHTTP3, after a warm-up and a 2 s pause, as
//     BodilessSteps), whose last document asks for /done.
func hintSteps() []hintStep {
	all := strings.Join(allHints, ", ")

	req := func(label, kind, host, page string) hintStep {
		return hintStep{
			Label:       label,
			Kind:        kind,
			Host:        host,
			Page:        page,
			PauseMillis: 200,
		}
	}

	nav := func(label, host, page string) hintStep {
		return req(label, "navigate", host, page)
	}

	// kinds are a fetch, an image and a script of prefix on host.
	kinds := func(prefix, host, page string) []hintStep {
		return []hintStep{
			req(prefix+".fetch", "fetch", host, page),
			req(prefix+".img", "image", host, page),
			req(prefix+".script", "script", host, page),
		}
	}

	with := func(s hintStep, f func(*hintStep)) hintStep {
		f(&s)

		return s
	}

	// critical is a navigation on chCritical answering Accept-CH accept
	// and Critical-CH critical.
	critical := func(label, page, accept, critical string) hintStep {
		return with(nav(label, chCritical, page), func(s *hintStep) {
			s.acceptCH, s.criticalCH = accept, critical
		})
	}

	const (
		optin  = "/h3/optin.nav"
		next   = "/h3/next.nav"
		deleg  = "/h3/deleg.nav"
		meta   = "/h3/meta.nav"
		shrink = "/h3/shrink.nav"
		after  = "/h3/after-shrink.nav"
		clr    = "/h3/clear.nav"
		k      = "Sec-CH-UA-Full-Version-List, Sec-CH-UA-Platform-Version, Sec-CH-UA-Arch"
	)

	return slices.Concat(
		kinds("base", chHost, "/"),
		[]hintStep{
			req("base.xo-fetch", "fetch", chThird, "/"),
			with(req("base.accept-sub", "fetch", chHost, "/"), func(s *hintStep) {
				s.acceptCH = all
			}),
			req("base.after-sub", "fetch", chHost, "/"),
			with(nav("optin.nav", chHost, "/"), func(s *hintStep) {
				s.acceptCH = all
			}),
		},
		kinds("optin", chHost, optin),
		[]hintStep{
			req("optin.iframe", "iframe", chHost, optin),
			req("optin.sub-fetch", "fetch", chSub, optin),
			req("optin.xo-fetch", "fetch", chThird, optin),
			req("optin.xo-img", "image", chThird, optin),
			req("optin.xo-iframe", "iframe", chThird, optin),
			nav("next.nav", chHost, optin),
			with(req("next.width-img", "image", chHost, next), func(s *hintStep) {
				s.Tag = `<img srcset="%s 100w" sizes="50px" alt="">`
			}),
		},
		kinds("next", chHost, next),
		[]hintStep{
			nav("sub.nav", chSub, next),
			req("sub.fetch", "fetch", chSub, "/h3/sub.nav"),
			nav("b.nav", chThird, "/h3/sub.nav"),
			req("b.fetch", "fetch", chThird, "/h3/b.nav"),
			with(nav("deleg.nav", chHost, "/h3/b.nav"), func(s *hintStep) {
				s.policy = delegation()
			}),
			req("deleg.xo-fetch", "fetch", chThird, deleg),
			req("deleg.xo-img", "image", chThird, deleg),
			req("deleg.xo-script", "script", chThird, deleg),
			req("deleg.xo-iframe", "iframe", chThird, deleg),
			req("deleg.sub-fetch", "fetch", chSub, deleg),
			with(nav("meta.nav", chHost, deleg), func(s *hintStep) {
				s.delegate = delegateCH()
			}),
			req("meta.xo-fetch", "fetch", chThird, meta),
			req("meta.xo-img", "image", chThird, meta),
			req("meta.xo-script", "script", chThird, meta),
			with(nav("shrink.nav", chHost, meta), func(s *hintStep) {
				s.acceptCH = "Sec-CH-UA-Arch"
			}),
			req("shrink.fetch", "fetch", chHost, shrink),
			nav("after-shrink.nav", chHost, shrink),
			req("after-shrink.fetch", "fetch", chHost, after),
			with(nav("clear.nav", chHost, after), func(s *hintStep) {
				s.clearSiteData = `"clientHints"`
			}),
			req("clear.fetch", "fetch", chHost, clr),
			nav("after-clear.nav", chHost, clr),
			req("after-clear.fetch", "fetch", chHost, "/h3/after-clear.nav"),
			critical("crit.nav", "/h3/after-clear.nav", k, "Sec-CH-UA-Platform-Version"),
			req("crit.fetch", "fetch", chCritical, "/h3/crit.nav"),
			critical("crit-again.nav", "/h3/crit.nav", k, "Sec-CH-UA-Platform-Version"),
			critical("crit-add.nav", "/h3/crit-again.nav", k+", Sec-CH-UA-Bitness", "Sec-CH-UA-Bitness"),
			with(critical("crit-blocked.nav", "/h3/crit-add.nav", k+", Sec-CH-UA-Model", "Sec-CH-UA-Model"), func(s *hintStep) {
				s.policy = "ch-ua-model=()"
			}),
			critical("crit-width.nav", "/h3/crit-blocked.nav", "Sec-CH-Width", "Sec-CH-Width"),
			critical("crit-save-data.nav", "/h3/crit-width.nav", "Save-Data", "Save-Data"),
			critical("crit-unknown.nav", "/h3/crit-save-data.nav", "Sec-CH-Mimicry-Unknown", "Sec-CH-Mimicry-Unknown"),
			critical("star-optin.nav", "/h3/crit-unknown.nav", "Sec-CH-UA-Arch", ""),
			with(nav("clear-star.nav", chCritical, "/h3/star-optin.nav"), func(s *hintStep) {
				s.clearSiteData = `"*"`
			}),
			nav("after-star.nav", chCritical, "/h3/clear-star.nav"),
			with(nav("h1-optin.nav", chHTTP1, "/h3/after-star.nav"), func(s *hintStep) {
				s.acceptCH, s.criticalCH = all, "Sec-CH-UA-Platform-Version"
			}),
		},
		kinds("h1", chHTTP1, "/h3/h1-optin.nav"),
		[]hintStep{
			nav("h1-next.nav", chHTTP1, "/h3/h1-optin.nav"),
			req("h1-next.fetch", "fetch", chHTTP1, "/h3/h1-next.nav"),
			nav("h3-start.nav", chHTTP3, "/h3/h1-next.nav"),
			req("h3-warm", "fetch", chHTTP3, "/h3/h3-start.nav"),
			with(nav("h3-optin.nav", chHTTP3, "/h3/h3-start.nav"), func(s *hintStep) {
				s.acceptCH, s.criticalCH = all, "Sec-CH-UA-Platform-Version"
				s.PauseMillis = 2000
			}),
		},
		kinds("h3", chHTTP3, "/h3/h3-optin.nav"),
		[]hintStep{
			nav("h3-next.nav", chHTTP3, "/h3/h3-optin.nav"),
			req("h3-next.fetch", "fetch", chHTTP3, "/h3/h3-next.nav"),
		},
	)
}

// clientHintsSteps are hintSteps as the page runs them.
func clientHintsSteps() []H3Step {
	hs := hintSteps()
	out := make([]H3Step, len(hs))

	for i, s := range hs {
		out[i] = s.H3Step
	}

	return out
}

// hintStepAt finds the step answered at path on host.
func hintStepAt(host, path string) (hintStep, bool) {
	for _, s := range hintSteps() {
		if s.Host == host && s.Path() == path {
			return s, true
		}
	}

	return hintStep{}, false
}

// clientHintsHeaders are the client-hint headers of the response to path
// on host.
func clientHintsHeaders(host, port, path string) []hpack.HeaderField {
	s, ok := hintStepAt(host, path)
	if !ok {
		return nil
	}

	var out []hpack.HeaderField

	for _, h := range []struct {
		name, value string
	}{
		{"accept-ch", s.acceptCH},
		{"critical-ch", s.criticalCH},
		{"clear-site-data", s.clearSiteData},
		{"permissions-policy", thirdParty(s.policy, port)},
	} {
		if h.value != "" {
			out = append(out, hpack.HeaderField{
				Name:  h.name,
				Value: h.value,
			})
		}
	}

	return out
}

// clientHintsHead is the Delegate-CH meta tag of the document at path, if
// it has one.
func clientHintsHead(path, port string) string {
	for _, s := range hintSteps() {
		if s.Path() == path && s.delegate != "" {
			return `<meta http-equiv="Delegate-CH" content="` + thirdParty(s.delegate, port) + `">`
		}
	}

	return ""
}

// thirdParty is format with the third party's origin on port in place of
// %[1]s; "" stays "".
func thirdParty(format, port string) string {
	if format == "" || !strings.Contains(format, "%[1]s") {
		return format
	}

	return fmt.Sprintf(format, "https://"+net.JoinHostPort(chThird, port))
}

// hintFeatures are the Permissions-Policy features of allHints, in their
// order, once each (DPR and Sec-CH-DPR are both ch-dpr).
func hintFeatures() []string {
	var out []string

	for _, h := range allHints {
		f := strings.ToLower(h)
		if rest, ok := strings.CutPrefix(f, "sec-"); ok {
			f = rest
		} else {
			f = "ch-" + f
		}

		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}

	return out
}

// delegation is a Permissions-Policy delegating every hint to the third
// party.
func delegation() string {
	features := hintFeatures()
	parts := make([]string, len(features))

	for i, f := range features {
		parts[i] = f + `=(self "%[1]s")`
	}

	return strings.Join(parts, ", ")
}

// delegateCH is a Delegate-CH meta tag's content delegating every hint to
// the third party.
func delegateCH() string {
	parts := make([]string, 0, len(allHints))
	for _, h := range allHints {
		parts = append(parts, strings.ToLower(h)+" %[1]s")
	}

	return strings.Join(parts, "; ")
}
