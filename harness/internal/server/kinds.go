package server

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// The kinds page's hosts: the page itself, another origin of its site, and
// another site.
const (
	kindsHost      = "www.kinds-a.test"
	kindsSameSite  = "static.kinds-a.test"
	kindsCrossSite = "cdn.kinds-b.test"
	kindsSiteA     = "kinds-a.test"
)

// kindsTargets are the hosts every request kind is tried against, with the
// suffix their steps' labels carry.
var kindsTargets = []struct {
	host, suffix string
}{
	{kindsHost, "so"},
	{kindsSameSite, "ss"},
	{kindsCrossSite, "cs"},
}

// KindsStep is one request the kinds page makes: its label (also its path,
// /k/<label>), the API it uses and how.
type KindsStep struct {
	Label string `json:"label"`
	// Kind is the API: script, style, image, font, iframe, fetch, xhr,
	// beacon.
	Kind string `json:"kind"`
	Host string `json:"-"`
	// CrossOrigin is the element's crossorigin attribute (script, style,
	// image): "", "anonymous" or "use-credentials".
	CrossOrigin string `json:"xo,omitempty"`
	// Mode, Credentials, Method, Headers, Body and Priority are fetch()
	// (and XHR) options; Priority is also an image's fetchpriority.
	Mode        string            `json:"mode,omitempty"`
	Credentials string            `json:"cred,omitempty"`
	Method      string            `json:"method,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
	Priority    string            `json:"priority,omitempty"`
	// RedirectTo, when set, makes the response a 302 to the same label on
	// that host, with ".hop" appended.
	RedirectTo string `json:"-"`
	// Tag, for a markup step, is the HTML element making the request, with
	// %s for the URL.
	Tag string `json:"-"`
	// URL is filled in when the page is rendered.
	URL string `json:"url"`
}

// KindsSteps lists the kinds page's requests in the order it makes them.
func KindsSteps() []KindsStep {
	steps := []KindsStep{
		// Third-party cookies first, so every later cross-site request
		// shows whether they are sent.
		{Label: "image-setcookie.cs", Kind: "image", Host: kindsCrossSite},
	}
	for _, t := range kindsTargets {
		for _, s := range []KindsStep{
			{Label: "script", Kind: "script"},
			{Label: "script-cors", Kind: "script", CrossOrigin: "anonymous"},
			{Label: "script-cors-cred", Kind: "script", CrossOrigin: "use-credentials"},
			{Label: "style", Kind: "style"},
			{Label: "image", Kind: "image"},
			{Label: "image-cors", Kind: "image", CrossOrigin: "anonymous"},
			{Label: "image-high", Kind: "image", Priority: "high"},
			{Label: "font", Kind: "font"},
			{Label: "iframe", Kind: "iframe"},
			{Label: "fetch", Kind: "fetch"},
			{Label: "fetch-include", Kind: "fetch", Credentials: "include"},
			{Label: "fetch-omit", Kind: "fetch", Credentials: "omit"},
			{Label: "fetch-nocors", Kind: "fetch", Mode: "no-cors"},
			{Label: "fetch-json", Kind: "fetch", Method: "POST", Headers: map[string]string{"content-type": "application/json"}, Body: `{"a":1}`},
			{Label: "fetch-custom", Kind: "fetch", Headers: map[string]string{"x-mimicry": "1"}},
			{Label: "fetch-high", Kind: "fetch", Priority: "high"},
			{Label: "fetch-low", Kind: "fetch", Priority: "low"},
			{Label: "xhr", Kind: "xhr"},
			{Label: "beacon", Kind: "beacon", Body: "x"},
		} {
			s.Label += "." + t.suffix
			s.Host = t.host
			steps = append(steps, s)
		}
	}
	// Redirects across sites: sec-fetch-site after a hop.
	return append(steps,
		KindsStep{Label: "fetch-redirect.so-cs", Kind: "fetch", Credentials: "include", Host: kindsHost, RedirectTo: kindsCrossSite},
		KindsStep{Label: "fetch-redirect.cs-so", Kind: "fetch", Credentials: "include", Host: kindsCrossSite, RedirectTo: kindsHost},
		KindsStep{Label: "image-redirect.so-cs", Kind: "image", Host: kindsHost, RedirectTo: kindsCrossSite},
		KindsStep{Label: "image-redirect.cs-so", Kind: "image", Host: kindsCrossSite, RedirectTo: kindsHost},
	)
}

// kindsFormLabel is the form-submit POST that ends the page.
const kindsFormLabel = "form-post.so"

// KindsCookies are what the page's own document sets: one per SameSite
// value, a site-wide one, and cookies breaking the rules in the ways real
// sites do (to see which each browser keeps and how it sends them back).
// Every name starts with "k" so /cleanup can find them.
var KindsCookies = []string{
	"k_strict=1; Path=/; Secure; SameSite=Strict",
	"k_lax=1; Path=/; Secure; SameSite=Lax",
	"k_none=1; Path=/; Secure; SameSite=None",
	"k_unset=1; Path=/; Secure",
	"k_site=1; Domain=" + kindsSiteA + "; Path=/; Secure; SameSite=None",
	`k_quoted="Ab+/cd=="; Path=/; Secure`,
	"k_space=a b c; Path=/; Secure",
	"k_comma=a,b; Path=/; Secure",
	`k_bslash=back\slash; Path=/; Secure`,
	"k_utf=café; Path=/; Secure",
	`k_json={"a":1}; Path=/; Secure`,
	"k_big=" + strings.Repeat("b", 4100) + "; Path=/; Secure",
	"k_dommis=1; Domain=other-site.test; Path=/; Secure",
	"__Host-k_bad=1; Domain=" + kindsSiteA + "; Path=/; Secure",
	"__Secure-k_ok=1; Path=/; Secure",
	"k_expired=1; Path=/; Secure; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
	"k_maxage0=1; Path=/; Secure; Max-Age=0",
	"k_empty=; Path=/; Secure",
	"knoeq; Path=/; Secure",
}

// KindsThirdPartyCookies are set by the cross-site host on an image the page
// loads: whether later cross-site requests carry them shows each browser's
// third-party cookie policy.
var KindsThirdPartyCookies = []string{
	"kb_none=1; Path=/; Secure; SameSite=None",
	"kb_lax=1; Path=/; Secure; SameSite=Lax",
	"kb_unset=1; Path=/; Secure",
	"kb_part=1; Path=/; Secure; SameSite=None; Partitioned",
}

// KindsMarkup are the requests the kinds page's HTML makes while it is
// parsed, before its script runs: browsers prioritise these by where they
// sit (a blocking script in the head, a deferred one, a preload), not only
// by kind. Tag is the element, with %s for the URL; the image comes last,
// in the body. Steps without a Tag are requested by the page's CSS: the
// fonts its @font-face rules declare, fetched because text uses them.
func KindsMarkup() []KindsStep {
	return []KindsStep{
		{Label: "style-head.so", Kind: "style", Host: kindsHost, Tag: `<link rel="stylesheet" href="%s">`},
		{Label: "style-fonts.so", Kind: "style", Host: kindsHost, Tag: `<link rel="stylesheet" href="%s">`},
		{Label: "preload-script.so", Kind: "script", Host: kindsHost, Tag: `<link rel="preload" as="script" href="%s">`},
		{Label: "preload-font.so", Kind: "font", Host: kindsHost, Tag: `<link rel="preload" as="font" type="font/woff2" crossorigin href="%s">`},
		{Label: "script-blocking.so", Kind: "script", Host: kindsHost, Tag: `<script src="%s"></script>`},
		{Label: "script-defer.so", Kind: "script", Host: kindsHost, Tag: `<script defer src="%s"></script>`},
		{Label: "script-async.so", Kind: "script", Host: kindsHost, Tag: `<script async src="%s"></script>`},
		{Label: "script-async.cs", Kind: "script", Host: kindsCrossSite, Tag: `<script async src="%s"></script>`},
		{Label: "image-markup.so", Kind: "image", Host: kindsHost, Tag: `<img src="%s" alt="">`},
		{Label: "font-css.so", Kind: "font", Host: kindsHost},
		{Label: "font-css.cs", Kind: "font", Host: kindsCrossSite},
	}
}

// kindsFontCSS declares a same-origin and a cross-site web font, the way a
// site and Google Fonts do; the page's text uses both.
func kindsFontCSS(origin func(string) string) []byte {
	return []byte(`@font-face { font-family: "k-so"; src: url("/k/font-css.so") format("woff2"); }
@font-face { font-family: "k-cs"; src: url("` + origin(kindsCrossSite) + `/k/font-css.cs") format("woff2"); }
`)
}

// Kinds is the kinds scenario: every request kind a page makes, against its
// own origin, another origin of its site and another site, with each
// credentials mode, a preflight, priorities, redirects across sites, and
// cookies of every SameSite value plus non-standard ones; it ends with a
// form-submit POST navigation.
var Kinds = &Scenario{
	Name:  "kinds",
	Doc:   "request kinds (script, style, image, font, iframe, fetch, XHR, beacon, form POST) same-origin, same-site and cross-site; SameSite, third-party and non-standard cookies",
	multi: kindsPage{},
}

// KindsHTTP1 is the kinds page with every host offering only HTTP/1.1:
// each kind's HTTP/1.1 header casing and order.
var KindsHTTP1 = &Scenario{
	Name: "kinds-http1",
	Doc:  "the kinds page over HTTP/1.1 only",
	multi: kindsPage{
		http1: true,
	},
}

type kindsPage struct {
	http1 bool
}

func (kindsPage) hosts() []string {
	return []string{
		kindsHost,
		kindsSameSite,
		kindsCrossSite,
	}
}

func (kindsPage) certNames(host string) ([]string, bool) {
	switch host {
	case kindsHost, kindsSameSite, kindsCrossSite:
		return []string{
			host,
		}, true
	}
	return nil, false
}

func (p kindsPage) h1Only(string) bool {
	return p.http1
}

func (kindsPage) length() time.Duration {
	// Sequential steps of a few milliseconds each, and up to 5 s for one
	// that never completes.
	return 20 * time.Second
}

func (p kindsPage) answer(req request) response {
	host, port, err := net.SplitHostPort(req.host)
	if err != nil {
		host, port = req.host, "443"
	}
	origin := func(h string) string {
		return "https://" + net.JoinHostPort(h, port)
	}
	var cors []hpack.HeaderField
	if req.origin != "" {
		cors = []hpack.HeaderField{
			{
				Name:  "access-control-allow-origin",
				Value: req.origin,
			},
			{
				Name:  "access-control-allow-credentials",
				Value: "true",
			},
		}
	}
	if req.method == "OPTIONS" {
		allow := slices.Concat(cors, []hpack.HeaderField{
			{
				Name:  "access-control-allow-methods",
				Value: "GET, POST",
			},
		})
		if req.preflightHeaders != "" {
			allow = append(allow, hpack.HeaderField{
				Name:  "access-control-allow-headers",
				Value: req.preflightHeaders,
			})
		}
		return response{status: 204, contentType: "text/plain", headers: allow}
	}
	path, _, _ := strings.Cut(req.path, "?")
	switch path {
	case "/":
		if host != kindsHost {
			break
		}
		r := response{status: 200, contentType: "text/html; charset=utf-8", body: p.document(port)}
		for _, c := range KindsCookies {
			r.headers = append(r.headers, hpack.HeaderField{
				Name:  "set-cookie",
				Value: c,
			})
		}
		return r
	case "/cleanup", "/done":
		r := response{status: 200, contentType: "text/plain", body: []byte("ok\n"), headers: cors}
		r.headers = append(r.headers, kindsDeletions(host)...)
		return r
	}
	label, ok := strings.CutPrefix(path, "/k/")
	if !ok {
		return response{status: 404, contentType: "text/plain", body: []byte("not found\n"), headers: cors}
	}
	switch label {
	case kindsFormLabel:
		return response{status: 200, contentType: "text/html; charset=utf-8", body: kindsLanding(origin)}
	case "style-fonts.so":
		return response{status: 200, contentType: "text/css", body: kindsFontCSS(origin)}
	}
	hop := strings.HasSuffix(label, ".hop")
	for _, s := range append(KindsMarkup(), KindsSteps()...) {
		if s.Label != strings.TrimSuffix(label, ".hop") {
			continue
		}
		if s.RedirectTo != "" && !hop {
			r := response{status: 302, contentType: "text/plain", headers: cors}
			r.headers = append(r.headers, hpack.HeaderField{
				Name:  "location",
				Value: origin(s.RedirectTo) + "/k/" + s.Label + ".hop",
			})
			return r
		}
		r := kindsBody(s.Kind)
		r.headers = append(r.headers, cors...)
		if s.Label == "image-setcookie.cs" {
			for _, c := range KindsThirdPartyCookies {
				r.headers = append(r.headers, hpack.HeaderField{
					Name:  "set-cookie",
					Value: c,
				})
			}
		}
		return r
	}
	return response{status: 404, contentType: "text/plain", body: []byte("not found\n"), headers: cors}
}

// kindsBody is a response each API accepts as loaded.
func kindsBody(kind string) response {
	switch kind {
	case "script":
		return response{status: 200, contentType: "text/javascript", body: []byte("/* ok */\n")}
	case "style":
		return response{status: 200, contentType: "text/css", body: []byte("/* ok */\n")}
	case "image":
		return response{status: 200, contentType: "image/png", body: png}
	case "iframe":
		return response{status: 200, contentType: "text/html; charset=utf-8", body: []byte("<!doctype html><title>frame</title>\n")}
	case "font":
		// Not a font: the request is what matters, the load may fail.
		return response{status: 200, contentType: "font/woff2", body: []byte("wOF2")}
	}
	return response{status: 200, contentType: "application/json", body: []byte("{}\n")}
}

// kindsDeletions deletes every cookie the page may have set on host, in
// each form it was set.
func kindsDeletions(host string) []hpack.HeaderField {
	type deletion struct {
		name, attrs string
	}
	var dels []deletion
	switch host {
	case kindsHost:
		for _, c := range KindsCookies {
			switch {
			case strings.HasPrefix(c, "k_site="):
				dels = append(dels, deletion{"k_site", "; Domain=" + kindsSiteA})
			case !strings.Contains(c, "Domain="):
				dels = append(dels, deletion{setCookieName(c), ""})
			}
		}
	case kindsCrossSite:
		for _, c := range KindsThirdPartyCookies {
			dels = append(dels, deletion{setCookieName(c), ""})
		}
		dels = append(dels, deletion{"kb_part", "; SameSite=None; Partitioned"})
	}
	out := make([]hpack.HeaderField, 0, len(dels))
	for _, d := range dels {
		out = append(out, hpack.HeaderField{
			Name:  "set-cookie",
			Value: d.name + "=; Path=/; Secure; Max-Age=0" + d.attrs,
		})
	}
	return out
}

// setCookieName is the name a Set-Cookie value sets; a pair without "="
// sets a cookie with an empty name (RFC 6265bis).
func setCookieName(setCookie string) string {
	pair, _, _ := strings.Cut(setCookie, ";")
	name, _, ok := strings.Cut(pair, "=")
	if !ok {
		return ""
	}
	return name
}

// document is the kinds page: it runs the steps one after another (5 s at
// most each), then submits a form, a POST navigation to the landing page.
func (kindsPage) document(port string) []byte {
	url := func(s KindsStep) string {
		return "https://" + net.JoinHostPort(s.Host, port) + "/k/" + s.Label
	}
	var markup strings.Builder
	for _, s := range KindsMarkup() {
		if s.Tag != "" {
			fmt.Fprintf(&markup, s.Tag+"\n", url(s))
		}
	}
	steps := KindsSteps()
	for i := range steps {
		steps[i].URL = url(steps[i])
	}
	stepsJSON, _ := json.Marshal(steps) //nolint:gosec // Credentials is fetch()'s credentials mode, not a secret
	script := strings.NewReplacer(
		"__STEPS__", string(stepsJSON),
		"__FORM__", strconv.Quote("https://"+net.JoinHostPort(kindsHost, port)+"/k/"+kindsFormLabel),
	).Replace(kindsScript)
	head, body, _ := strings.Cut(markup.String(), "<img")
	return []byte("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>mimicry kinds</title><link rel=\"icon\" href=\"data:,\">\n" +
		head + "</head><body>\n<img" + body +
		"<p style=\"font-family: k-so\">same-origin font</p>\n<p style=\"font-family: k-cs\">cross-site font</p>\n" +
		"<script>" + script + "</script>\n</body></html>\n")
}

const kindsScript = `(async () => {
const steps = __STEPS__;
function run(s) {
  return new Promise(done => {
    const fin = () => done();
    setTimeout(fin, 5000);
    let el;
    switch (s.kind) {
    case 'script':
      el = document.createElement('script');
      if (s.xo) el.crossOrigin = s.xo;
      el.onload = el.onerror = fin; el.src = s.url; document.head.appendChild(el); return;
    case 'style':
      el = document.createElement('link'); el.rel = 'stylesheet';
      if (s.xo) el.crossOrigin = s.xo;
      el.onload = el.onerror = fin; el.href = s.url; document.head.appendChild(el); return;
    case 'image':
      el = new Image();
      if (s.xo) el.crossOrigin = s.xo;
      if (s.priority) el.fetchPriority = s.priority;
      el.onload = el.onerror = fin; el.src = s.url; return;
    case 'font':
      new FontFace('f' + Math.random().toString(36).slice(2), 'url(' + s.url + ')').load().then(fin, fin); return;
    case 'iframe':
      el = document.createElement('iframe'); el.onload = fin; el.src = s.url; document.body.appendChild(el); return;
    case 'xhr': {
      const x = new XMLHttpRequest(); x.open(s.method || 'GET', s.url);
      x.onloadend = fin; x.send(s.body || null); return;
    }
    case 'beacon':
      navigator.sendBeacon(s.url, s.body || ''); setTimeout(fin, 500); return;
    }
    const init = {method: s.method || 'GET'};
    if (s.mode) init.mode = s.mode;
    if (s.cred) init.credentials = s.cred;
    if (s.headers) init.headers = s.headers;
    if (s.body) init.body = s.body;
    if (s.priority) init.priority = s.priority;
    fetch(s.url, init).then(r => r.arrayBuffer()).then(fin, fin);
  });
}
for (const s of steps) await run(s);
const f = document.createElement('form'); f.method = 'POST'; f.action = __FORM__;
const i = document.createElement('input'); i.name = 'a'; i.value = '1'; f.appendChild(i);
document.body.appendChild(f); f.submit();
})();`

// kindsLanding is where the form POST lands: it deletes every host's
// cookies and asks for /done.
func kindsLanding(origin func(string) string) []byte {
	cleanup, _ := json.Marshal([]string{
		origin(kindsHost) + "/cleanup",
		origin(kindsSameSite) + "/cleanup",
		origin(kindsCrossSite) + "/cleanup",
	})
	return []byte("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>mimicry kinds</title><link rel=\"icon\" href=\"data:,\"></head><body>\n<script>(async () => {\n" +
		"await Promise.all(" + string(cleanup) + ".map(u => fetch(u, {credentials: 'include', mode: 'no-cors'}).catch(() => {})));\n" +
		"fetch('/done');\n})();</script>\n</body></html>\n")
}
