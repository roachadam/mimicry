package server

import (
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// The redirects2 page's hosts: its own, one of its site, a cross-site one,
// and a third site for a cross-site redirect to another site.
const (
	redirHost      = "www.redir-a.test"
	redirSameSite  = "static.redir-a.test"
	redirCrossSite = "www.redir-b.test"
	redirThirdSite = "www.redir-c.test"
)

// Redirects2 is the redirects2 scenario: the redirects cors-edge left
// unmeasured (decision 0022's "Revisit when", and the hops mimicry refuses
// as unmeasured), from https://www.redir-a.test (see Redirects2Steps).
var Redirects2 = &Scenario{
	Name: "redirects2",
	Doc:  "redirects cors-edge left open: form POST navigations answered 301, 302, 303, 307 and 308, a 25-redirect navigation, images (markup, new Image(), crossorigin) redirected within their origin and site, XHR and <script crossorigin> redirected without Access-Control-Allow-Origin, same-site fetch() hops, cross-site fetch() chains to another site with and without Access-Control-Allow-Origin and -Credentials, PUT and POST on 307 and 308, HEAD on 301, 302 and 303, PATCH on all five, same-origin and preflighted cross-site",
	multi: redirects2Page{
		fixedHosts: fixedHosts{
			names: []string{
				redirHost,
				redirSameSite,
				redirCrossSite,
				redirThirdSite,
			},
			runFor: 45 * time.Second,
		},
		steps: Redirects2Steps(),
	},
}

// Values of Redirects2Step.Kind: how the page makes the step's first
// request.
const (
	// RedirectForm submits a form (the step's Method, GET or POST; a
	// POST's body the default application/x-www-form-urlencoded) from a
	// page of its own: a top-level navigation.
	RedirectForm = "form"
	// RedirectNavigate is a top-level navigation by script (location.href)
	// from a page of its own.
	RedirectNavigate = "navigate"
	// RedirectFetch is a fetch().
	RedirectFetch = "fetch"
	// RedirectXHR is an XMLHttpRequest GET, without withCredentials.
	RedirectXHR = "xhr"
	// RedirectImageMarkup is an <img> in the page's HTML, RedirectImageNew
	// a new Image(), neither with crossorigin.
	RedirectImageMarkup = "img-markup"
	RedirectImageNew    = "img-new"
	// RedirectImage and RedirectScript are <img crossorigin="anonymous">
	// and <script crossorigin="anonymous">, created by script.
	RedirectImage  = "img"
	RedirectScript = "script"
)

// Redirects2Step is one request of the redirects2 page (or of
// nav-redirect-page, which has navigations only): to /p/<Label> on Host,
// answered Status with a Location to /p/<Label>.hop1 on HopHost, which is
// answered the same way up to .hop<Hops>, which is answered 200.
type Redirects2Step struct {
	// Label ends in the step's sites, as the kinds page's do: "so" (the
	// page's origin), "ss" (another origin of its site), "cs" (another
	// site), "cs-cs" (another site, whose redirect goes to a third): the
	// first request's, then, for redirects to another host, the hops'
	// ("so-cs": from the page's origin to another site).
	Label string
	Kind  string
	Host  string
	// Next is the host the redirects go to; "" for Host.
	Next string
	// Method is the first request's method; "" for GET.
	Method string
	// Body is the first request's body ("" for none): fetch() sends a
	// string as text/plain;charset=UTF-8; a form's is its one field,
	// urlencoded (a GET form's goes in the query instead).
	Body string
	// Credentials is fetch()'s credentials mode; "" for the default
	// (same-origin).
	Credentials string
	// Status is the redirects' status and Hops how many there are.
	Status int
	Hops   int
	// NoACAO leaves Access-Control-Allow-Origin and -Credentials off the
	// redirects, NoACAC only -Credentials; the last hop has both. A
	// navigation's redirects carry neither.
	NoACAO bool
	NoACAC bool
	// Click has a user make the navigation (a navigation step's): the
	// capture tool clicks its link (RedirectNavigate) or its form's submit
	// button (RedirectForm) as a user does, so the navigation has user
	// activation (Sec-Fetch-User); otherwise the page's script makes it.
	Click bool
}

// ClickTitle is the title a page takes once it waits for a user's click
// (Redirects2Step.Click): the capture tool clicks the middle of a page
// with this title, once per URL, as a user does, and the page's link or
// submit button fills the window.
const ClickTitle = "mimicry-click"

// Clicks reports a scenario whose page waits for a user's clicks
// (ClickTitle).
func (sc *Scenario) Clicks() bool {
	steps, _ := sc.RedirectSteps()
	return slices.ContainsFunc(steps, func(s Redirects2Step) bool {
		return s.Click
	})
}

// RedirectSteps returns the steps of a page served as the redirects2 page
// is (redirects2, nav-redirect-page, nav-redirect-form,
// nav-redirect-click); ok is false for other scenarios.
func (sc *Scenario) RedirectSteps() (steps []Redirects2Step, ok bool) {
	p, ok := sc.multi.(redirects2Page)
	return p.steps, ok
}

// HopHost is the host the step's redirects go to.
func (s Redirects2Step) HopHost() string {
	if s.Next != "" {
		return s.Next
	}
	return s.Host
}

// Nav reports a top-level navigation step.
func (s Redirects2Step) Nav() bool {
	return s.Kind == RedirectForm || s.Kind == RedirectNavigate
}

// redirChain is how many redirects the navigation chain is answered
// with: more than any browser's fetch() follows (20).
const redirChain = 25

// navFollowed is the fewest redirects any browser's navigation follows
// (Chrome's 19, redirects2): a longer chain ends on an error page.
const navFollowed = 19

// redirWatch is how long the watchdog waits after a navigation step
// starts: its redirects, the page it lands on and the next step's page
// take well under a second.
const redirWatch = 10 * time.Second

// Redirects2Steps returns the redirects2 page's requests, in order. The
// page at / makes the steps that are not navigations, one after another,
// 5 s at most each (a refused one is expected and ignored; its markup
// images load with the page, and it waits for them first); then each
// navigation starts from a page of its own on the page's host,
// /s/<Label>, and the page its last hop answers goes to the next one's.
// Each question is one step, its label unique:
//
//   - img-markup.<so|ss>, img-new.<so|ss>: an <img> in the page's HTML
//     and a new Image() (no crossorigin) answered 302 within the page's
//     origin, and within static.redir-a.test (its site): .hop1's headers;
//   - xhr-acao.cs, xhr-noacao.cs: a cross-site XHR GET answered 302 with
//     and without Access-Control-Allow-Origin: is .hop1 requested?
//   - img-*.cs, script-*.cs: the same for <img crossorigin> and
//     <script crossorigin>;
//   - fetch-acao.ss, fetch-noacao.ss: a same-site fetch() answered 302
//     within static.redir-a.test, with and without
//     Access-Control-Allow-Origin;
//   - fetch-acao.cs-cs, fetch-noacao.cs-cs: a cross-site fetch() answered
//     302 to the third site, with and without Access-Control-Allow-Origin:
//     is .hop1 requested, with which Origin (a redirect to another origin
//     taints it: "null")?
//   - cred-acac.cs-cs, cred-noacac.cs-cs: the same with credentials
//     "include", Access-Control-Allow-Origin, and with or without
//     Access-Control-Allow-Credentials;
//   - m-put-<307|308>.so: same-origin PUT with a body: .hop1's method,
//     body and content-type; m-post-<307|308>.<so|cs>: the same for POST,
//     same-origin and cross-site;
//   - m-head-<301..303>.so: the same for HEAD;
//   - m-patch-<301..308>.<so|cs>: PATCH with a body answered 301, 302,
//     303, 307 and 308, same-origin and cross-site (preflighted: each
//     PATCH hop's preflight is answered too);
//   - form-<status>.<so|cs>: a form POST (field a=1) to the page's own
//     origin or another site answered 301, 302, 303, 307 and 308: .hop1's
//     method, body, Origin and Content-Type;
//   - nav-chain.so: a navigation answered 302 redirChain times: the last
//     .hop<n> requested is where the browser stops. A browser that stops
//     shows an error page, so the watchdog asks for /done; without one
//     (no service workers) the step is skipped.
func Redirects2Steps() []Redirects2Step {
	steps := []Redirects2Step{
		{Label: "img-markup.so", Kind: RedirectImageMarkup, Host: redirHost, Status: 302, Hops: 1},
		{Label: "img-markup.ss", Kind: RedirectImageMarkup, Host: redirSameSite, Status: 302, Hops: 1},
		{Label: "img-new.so", Kind: RedirectImageNew, Host: redirHost, Status: 302, Hops: 1},
		{Label: "img-new.ss", Kind: RedirectImageNew, Host: redirSameSite, Status: 302, Hops: 1},
		{Label: "xhr-acao.cs", Kind: RedirectXHR, Host: redirCrossSite, Status: 302, Hops: 1},
		{Label: "xhr-noacao.cs", Kind: RedirectXHR, Host: redirCrossSite, Status: 302, Hops: 1, NoACAO: true},
		{Label: "img-acao.cs", Kind: RedirectImage, Host: redirCrossSite, Status: 302, Hops: 1},
		{Label: "img-noacao.cs", Kind: RedirectImage, Host: redirCrossSite, Status: 302, Hops: 1, NoACAO: true},
		{Label: "script-acao.cs", Kind: RedirectScript, Host: redirCrossSite, Status: 302, Hops: 1},
		{Label: "script-noacao.cs", Kind: RedirectScript, Host: redirCrossSite, Status: 302, Hops: 1, NoACAO: true},
		{Label: "fetch-acao.ss", Kind: RedirectFetch, Host: redirSameSite, Status: 302, Hops: 1},
		{Label: "fetch-noacao.ss", Kind: RedirectFetch, Host: redirSameSite, Status: 302, Hops: 1, NoACAO: true},
		{Label: "fetch-acao.cs-cs", Kind: RedirectFetch, Host: redirCrossSite, Next: redirThirdSite, Status: 302, Hops: 1},
		{Label: "fetch-noacao.cs-cs", Kind: RedirectFetch, Host: redirCrossSite, Next: redirThirdSite, Status: 302, Hops: 1, NoACAO: true},
		{Label: "cred-acac.cs-cs", Kind: RedirectFetch, Host: redirCrossSite, Next: redirThirdSite, Credentials: "include", Status: 302, Hops: 1},
		{Label: "cred-noacac.cs-cs", Kind: RedirectFetch, Host: redirCrossSite, Next: redirThirdSite, Credentials: "include", Status: 302, Hops: 1, NoACAC: true},
	}
	sites := []struct {
		name, host string
	}{
		{"so", redirHost},
		{"cs", redirCrossSite},
	}
	withMethod := func(method, body, site, host string, status int) Redirects2Step {
		return Redirects2Step{
			Label:  fmt.Sprintf("m-%s-%d.%s", strings.ToLower(method), status, site),
			Kind:   RedirectFetch,
			Host:   host,
			Method: method,
			Body:   body,
			Status: status,
			Hops:   1,
		}
	}
	for _, status := range []int{307, 308} {
		steps = append(steps, withMethod("PUT", "x", "so", redirHost, status))
	}
	for _, site := range sites {
		for _, status := range []int{307, 308} {
			steps = append(steps, withMethod("POST", "x", site.name, site.host, status))
		}
	}
	for _, status := range []int{301, 302, 303} {
		steps = append(steps, withMethod("HEAD", "", "so", redirHost, status))
	}
	for _, site := range sites {
		for _, status := range []int{301, 302, 303, 307, 308} {
			steps = append(steps, withMethod("PATCH", "x", site.name, site.host, status))
		}
	}
	for _, site := range sites {
		for _, status := range []int{301, 302, 303, 307, 308} {
			steps = append(steps, Redirects2Step{
				Label:  fmt.Sprintf("form-%d.%s", status, site.name),
				Kind:   RedirectForm,
				Host:   site.host,
				Method: "POST",
				Body:   "a=1",
				Status: status,
				Hops:   1,
			})
		}
	}
	return append(steps, Redirects2Step{
		Label:  "nav-chain.so",
		Kind:   RedirectNavigate,
		Host:   redirHost,
		Status: 302,
		Hops:   redirChain,
	})
}

// redirects2Page serves steps (Redirects2Steps, or NavRedirectPageSteps)
// from its first host, the page's.
type redirects2Page struct {
	fixedHosts
	steps []Redirects2Step
}

func (p redirects2Page) answer(req request) response {
	host, port := splitAuthority(req.host)
	origin := originOn("https://", port)
	path, _, _ := strings.Cut(req.path, "?")
	notFound := response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
	steps, home := p.steps, p.names[0]
	switch {
	case path == "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	case host == home && path == watchdogPath:
		return watchdogResponse()
	case host == home && path == "/":
		return htmlResponse(redirRootPage(steps, origin))
	}
	if label, ok := strings.CutPrefix(path, "/s/"); ok {
		i := slices.IndexFunc(steps, func(s Redirects2Step) bool {
			return s.Label == label
		})
		if host != home || i < 0 || !steps[i].Nav() {
			return notFound
		}
		return htmlResponse(redirStartPage(steps[i], origin))
	}
	target, ok := strings.CutPrefix(path, "/p/")
	if !ok {
		return notFound
	}
	label, hop := target, 0
	if l, n, ok := strings.Cut(target, ".hop"); ok {
		var err error
		if hop, err = strconv.Atoi(n); err != nil || hop < 1 {
			return notFound
		}
		label = l
	}
	i := slices.IndexFunc(steps, func(s Redirects2Step) bool {
		return s.Label == label
	})
	if i < 0 {
		return notFound
	}
	s := steps[i]
	want := s.Host
	if hop > 0 {
		want = s.HopHost()
	}
	if host != want || hop > s.Hops {
		return notFound
	}
	var cors []hpack.HeaderField
	if req.origin != "" && !s.Nav() {
		cors = corsAllow(req.origin)
	}
	if req.method == "OPTIONS" {
		return response{status: 204, contentType: "text/plain", headers: preflightAllow(cors, "GET, HEAD, POST, PUT, PATCH", req.preflightHeaders)}
	}
	if hop == s.Hops {
		return redirLast(steps, i, home, req.method, cors, origin)
	}
	r := response{status: s.Status, contentType: "text/plain"}
	switch {
	case s.NoACAO:
	case s.NoACAC && len(cors) > 0:
		r.headers = cors[:1]
	default:
		r.headers = cors
	}
	r.headers = append(slices.Clone(r.headers), hpack.HeaderField{
		Name:  "location",
		Value: origin(s.HopHost()) + "/p/" + s.Label + ".hop" + strconv.Itoa(hop+1),
	})
	return r
}

func htmlResponse(body []byte) response {
	return response{status: 200, contentType: "text/html; charset=utf-8", body: body}
}

// redirLast answers step i's last hop: what its kind loads, readable by
// the page (cors); a navigation's page goes on to the next navigation's
// page on home, or, after the last, ends the watchdog (of its own origin:
// the last navigation lands on home) and asks for /done.
func redirLast(steps []Redirects2Step, i int, home, method string, cors []hpack.HeaderField, origin func(string) string) response {
	switch steps[i].Kind {
	case RedirectForm, RedirectNavigate:
		next := "await end();\nfetch(\"/done\");"
		for _, s := range steps[i+1:] {
			if s.Nav() {
				next = fmt.Sprintf("location.href = %s;", strconv.Quote(origin(home)+"/s/"+s.Label))
				break
			}
		}
		return htmlResponse(htmlPage("redirects2", "", pageScript([]string{next})))
	case RedirectImage, RedirectImageMarkup, RedirectImageNew:
		return response{status: 200, contentType: "image/png", body: png, headers: cors}
	case RedirectScript:
		return response{status: 200, contentType: "text/javascript", body: []byte(";\n"), headers: cors}
	}
	r := response{status: 200, contentType: "application/json", body: []byte("{}\n"), headers: cors}
	if method == "HEAD" {
		r.body = nil
	}
	return r
}

// redirURL is the URL of a step's first request.
func redirURL(s Redirects2Step, origin func(string) string) string {
	return origin(s.Host) + "/p/" + s.Label
}

// redirRootPage is the page at /: its markup images, then a script that
// installs the watchdog, waits for them, makes the other steps that are
// not navigations in turn, then goes to the first navigation's page.
func redirRootPage(steps []Redirects2Step, origin func(string) string) []byte {
	var markup strings.Builder
	for _, s := range steps {
		if s.Kind == RedirectImageMarkup {
			markup.WriteString(`<img id="` + html.EscapeString(s.Label) + `" src="` + html.EscapeString(redirURL(s, origin)) + `" alt="">` + "\n")
		}
	}
	return htmlPage("redirects2", "", markup.String()+pageScript(redirFirstPage(steps, origin)))
}

// redirFirstPage is the script of the page at /.
func redirFirstPage(steps []Redirects2Step, origin func(string) string) []string {
	out := []string{
		"await register();",
		`const f = (u, init) => within(fetch(u, init).then(r => r.text()), 5000);`,
		`const xhr = u => within(new Promise(d => { const x = new XMLHttpRequest(); x.onloadend = d; x.open("GET", u); x.send(); }), 5000);`,
		`const load = (tag, u) => within(new Promise(d => { const e = document.createElement(tag); e.crossOrigin = "anonymous"; e.onload = e.onerror = d; e.src = u; if (tag === "script") document.body.append(e); }), 5000);`,
		`const image = u => within(new Promise(d => { const e = new Image(); e.onload = e.onerror = d; e.src = u; }), 5000);`,
		`const shown = id => within(new Promise(d => { const e = document.getElementById(id); if (e.complete) { d(); } else { e.onload = e.onerror = d; } }), 5000);`,
	}
	for _, s := range steps {
		u := strconv.Quote(redirURL(s, origin))
		switch s.Kind {
		case RedirectImageMarkup:
			out = append(out, fmt.Sprintf("await shown(%s);", strconv.Quote(s.Label)))
		case RedirectImageNew:
			out = append(out, fmt.Sprintf("await image(%s);", u))
		case RedirectXHR:
			out = append(out, fmt.Sprintf("await xhr(%s);", u))
		case RedirectImage, RedirectScript:
			out = append(out, fmt.Sprintf("await load(%s, %s);", strconv.Quote(s.Kind), u))
		case RedirectFetch:
			var init []string
			if s.Method != "" {
				init = append(init, "method: "+strconv.Quote(s.Method))
			}
			if s.Body != "" {
				init = append(init, "body: "+strconv.Quote(s.Body))
			}
			if s.Credentials != "" {
				init = append(init, "credentials: "+strconv.Quote(s.Credentials))
			}
			out = append(out, fmt.Sprintf("await f(%s, {%s});", u, strings.Join(init, ", ")))
		default:
			return append(out, fmt.Sprintf("location.href = %s;", strconv.Quote("/s/"+s.Label)))
		}
	}
	return append(out, `fetch("/done");`)
}

// redirStartPage is a navigation step's page: it arms the watchdog, then
// submits its form or navigates; for a Click step it shows its link or
// submit button over the whole window and takes ClickTitle, and the
// capture tool clicks. Without the watchdog, a chain longer than a
// browser follows (which ends on an error page) is skipped.
func redirStartPage(s Redirects2Step, origin func(string) string) []byte {
	u := redirURL(s, origin)
	arm := fmt.Sprintf("await arm(%d)", redirWatch.Milliseconds())
	const fill = ` id="go" style="position: fixed; inset: 0; width: 100%; height: 100%"`
	click := fmt.Sprintf("document.title = %s;", strconv.Quote(ClickTitle))
	if s.Kind == RedirectNavigate {
		watch := arm + ";"
		if s.Hops > navFollowed {
			watch = "if (!(" + arm + ")) {\nfetch(\"/done\");\nreturn;\n}"
		}
		if s.Click {
			return htmlPage("redirects2", "", `<a`+fill+` href="`+html.EscapeString(u)+`">go</a>`+"\n"+pageScript([]string{
				watch,
				click,
			}))
		}
		return htmlPage("redirects2", "", pageScript([]string{
			watch,
			fmt.Sprintf("location.href = %s;", strconv.Quote(u)),
		}))
	}
	name, value, _ := strings.Cut(s.Body, "=")
	submit, send := "", "document.forms[0].submit();"
	if s.Click {
		submit, send = `<button`+fill+`>go</button>`, click
	}
	form := `<form method="` + strings.ToLower(s.Method) + `" action="` + html.EscapeString(u) + `"><input type="hidden" name="` + html.EscapeString(name) + `" value="` + html.EscapeString(value) + `">` + submit + `</form>` + "\n"
	return htmlPage("redirects2", "", form+pageScript([]string{
		arm + ";",
		send,
	}))
}
