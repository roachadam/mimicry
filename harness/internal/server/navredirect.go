package server

import (
	"net"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// The nav-redirect pages' hosts: a site's apex, www and static hosts, and
// another site.
const (
	navrSite      = "navr-a.test"
	navrHost      = "www.navr-a.test"
	navrStatic    = "static.navr-a.test"
	navrCrossSite = "www.navr-b.test"
)

// The nav-redirect scenarios: the capture tool's start URL, a typed
// navigation to /p/<label>, is answered with a redirect to
// /p/<label>.hop1, whose page asks for /done. Each records how the browser
// sends a typed navigation and its redirect hop (HTTP/2, the plain start
// HTTP/1.1 without TLS), which redirects of script navigations do not
// show. Labels end in the hop's relation to the start, as redirects2's do.
// See NavRedirectSteps.
var (
	NavRedirectSO = navRedirect("nav-redirect-so", "a typed navigation answered 301 within its origin",
		NavRedirectStep{Label: "nav.so", Host: navrHost, Status: 301, Next: navrHost})
	NavRedirectSS = navRedirect("nav-redirect-ss", "a typed navigation to a site's apex answered 301 to its www host",
		NavRedirectStep{Label: "nav.ss", Host: navrSite, Status: 301, Next: navrHost})
	NavRedirectCS = navRedirect("nav-redirect-cs", "a typed navigation answered 302 to another site",
		NavRedirectStep{Label: "nav.cs", Host: navrHost, Status: 302, Next: navrCrossSite})
	NavRedirectHTTP = navRedirect("nav-redirect-http", "a typed http:// navigation (a port without TLS) answered 301 to https:// of its host",
		NavRedirectStep{Label: "nav.http", Host: navrHost, Plain: true, Status: 301, Next: navrHost})
)

// NavRedirectPage is the nav-redirect-page scenario: navigations a page
// makes that are redirected to another origin, from
// https://www.navr-a.test/ (see NavRedirectPageSteps). It is served as the
// redirects2 page is: each navigation from a page of its own,
// /s/<label>, the page its hop lands on going to the next one's, the
// watchdog armed before each.
var NavRedirectPage = &Scenario{
	Name: "nav-redirect-page",
	Doc:  "navigations by a page's script (location.href) and forms (GET, and POST answered 303) redirected to another origin: same-origin and same-site URLs answered 302 to another site, 301 from www to static and from the apex to www, a cross-site URL answered 302 back to the page's origin",
	multi: redirects2Page{
		fixedHosts: fixedHosts{
			names: []string{
				navrHost,
				navrSite,
				navrStatic,
				navrCrossSite,
			},
			runFor: 20 * time.Second,
		},
		steps: NavRedirectPageSteps(),
	},
}

// NavRedirectPageSteps returns the nav-redirect-page page's navigations,
// in order, each a top-level navigation from a page on www.navr-a.test
// whose one redirect goes to another origin (a hop that mimicry refuses as
// unmeasured without this capture). Each is made by script: none has user
// activation, so none sends Sec-Fetch-User (the capture tool sends no
// input events). Labels name the first request's site, then the hop's:
//
//   - nav-302.so-cs, nav-302.ss-cs: location.href to the page's origin and
//     to static.navr-a.test (its site), answered 302 to another site;
//   - nav-301.so-ss, nav-301.ss-so: location.href to the page's origin
//     answered 301 to static.navr-a.test, and to the apex answered 301 to
//     www.navr-a.test (the page's origin);
//   - form-get-302.so-cs: a form GET (field a=1, in the query) to the
//     page's origin answered 302 to another site;
//   - form-post-303.so-cs: a form POST (a=1) to the page's origin
//     answered 303 to another site: .hop1 is a GET;
//   - nav-302.cs-so: location.href to another site answered 302 back to
//     the page's origin. Last: the page it lands on, on www.navr-a.test,
//     ends the watchdog it armed.
func NavRedirectPageSteps() []Redirects2Step {
	return []Redirects2Step{
		{Label: "nav-302.so-cs", Kind: RedirectNavigate, Host: navrHost, Next: navrCrossSite, Status: 302, Hops: 1},
		{Label: "nav-302.ss-cs", Kind: RedirectNavigate, Host: navrStatic, Next: navrCrossSite, Status: 302, Hops: 1},
		{Label: "nav-301.so-ss", Kind: RedirectNavigate, Host: navrHost, Next: navrStatic, Status: 301, Hops: 1},
		{Label: "nav-301.ss-so", Kind: RedirectNavigate, Host: navrSite, Next: navrHost, Status: 301, Hops: 1},
		{Label: "form-get-302.so-cs", Kind: RedirectForm, Host: navrHost, Next: navrCrossSite, Method: "GET", Body: "a=1", Status: 302, Hops: 1},
		{Label: "form-post-303.so-cs", Kind: RedirectForm, Host: navrHost, Next: navrCrossSite, Method: "POST", Body: "a=1", Status: 303, Hops: 1},
		{Label: "nav-302.cs-so", Kind: RedirectNavigate, Host: navrCrossSite, Next: navrHost, Status: 302, Hops: 1},
	}
}

// NavRedirects are the nav-redirect scenarios: typed navigations
// (NavRedirectStep) and the page's (NavRedirectPage).
var NavRedirects = []*Scenario{
	NavRedirectSO,
	NavRedirectSS,
	NavRedirectCS,
	NavRedirectHTTP,
	NavRedirectPage,
}

// NavRedirectStep is a nav-redirect page: the typed navigation to
// /p/<Label> on Host (over http:// on the scenario's plain port if Plain,
// else https://), answered Status with a Location to https:// Next
// /p/<Label>.hop1, which is answered 200 with a page that asks for /done.
type NavRedirectStep struct {
	Label  string
	Host   string
	Plain  bool
	Status int
	Next   string
}

// StartPath is the start URL's path.
func (s NavRedirectStep) StartPath() string {
	return "/p/" + s.Label
}

// HopPath is the redirect target's path.
func (s NavRedirectStep) HopPath() string {
	return "/p/" + s.Label + ".hop1"
}

// NavRedirectStep returns a nav-redirect scenario's page; ok is false for
// other scenarios.
func (sc *Scenario) NavRedirectStep() (step NavRedirectStep, ok bool) {
	p, ok := sc.multi.(navRedirectPage)
	return p.step, ok
}

// StartPath is the path the browser starts at: the nav-redirect pages'
// /p/<label>, every other page's "/".
func (sc *Scenario) StartPath() string {
	if s, ok := sc.NavRedirectStep(); ok {
		return s.StartPath()
	}
	return "/"
}

// navRedirect is a nav-redirect scenario: on a plain port of its own,
// where it starts, if the start is Plain.
func navRedirect(name, doc string, step NavRedirectStep) *Scenario {
	hosts := []string{
		step.Host,
	}
	if step.Next != step.Host {
		hosts = append(hosts, step.Next)
	}
	return &Scenario{
		Name: name,
		Doc:  doc,
		multi: navRedirectPage{
			fixedHosts: fixedHosts{
				names:  hosts,
				runFor: 10 * time.Second,
			},
			step: step,
		},
		plainPort:  step.Plain,
		startPlain: step.Plain,
	}
}

type navRedirectPage struct {
	fixedHosts
	step NavRedirectStep
}

func (p navRedirectPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	path, _, _ := strings.Cut(req.path, "?")
	plain := req.ports.plain != "" && port == req.ports.plain
	s := p.step
	switch {
	case path == "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	case host == s.Host && path == s.StartPath() && plain == s.Plain:
		if req.ports.tls != "" {
			port = req.ports.tls
		}
		return response{
			status:      s.Status,
			contentType: "text/html; charset=utf-8",
			headers: []hpack.HeaderField{
				{
					Name:  "location",
					Value: "https://" + net.JoinHostPort(s.Next, port) + s.HopPath(),
				},
			},
		}
	case host == s.Next && path == s.HopPath() && !plain:
		return htmlResponse(htmlPage("nav-redirect", "", "<script>fetch(\"/done\");</script>\n"))
	}
	return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
}
