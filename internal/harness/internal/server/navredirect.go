package server

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// The nav-redirect pages' hosts: a site's apex, www and static hosts, and
// another site's www host and apex.
const (
	navrSite      = "navr-a.test"
	navrHost      = "www.navr-a.test"
	navrStatic    = "static.navr-a.test"
	navrCrossSite = "www.navr-b.test"
	navrCrossApex = "navr-b.test"
)

// The nav-redirect scenarios: a typed navigation to /p/<label> redirected
// once, which script navigations' redirects do not show. See
// NavRedirectStep.
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

// The typed navigations' cookie twins: each is its original (named without
// "-cookies") but for its cookie handling, so its hop's header list differs
// from the original's only by the cookie slot. The first leg's redirect sets
// the cookies the hop carries; the landing page's /done deletes them, as
// Safari keeps its cookies from one sample to the next.
var (
	// NavRedirectSOCookies: three cookies of the hop's host, as httpbin's
	// /cookies/set answers (no SameSite: Lax, sent on a top-level GET).
	NavRedirectSOCookies = navRedirect("nav-redirect-so-cookies", "nav-redirect-so, its 301 setting three cookies of its host the hop carries",
		NavRedirectStep{
			Label:  "nav.so",
			Host:   navrHost,
			Status: 301,
			Next:   navrHost,
			SetCookies: []string{
				"zeta=1; Path=/",
				"alpha=2; Path=/",
				"mid=3; Path=/",
			},
		})
	// NavRedirectSSCookies: a cookie of the site, set by its apex for the
	// www host the hop goes to.
	NavRedirectSSCookies = navRedirect("nav-redirect-ss-cookies", "nav-redirect-ss, its 301 setting a cookie of the site the hop carries",
		NavRedirectStep{
			Label:  "nav.ss",
			Host:   navrSite,
			Status: 301,
			Next:   navrHost,
			SetCookies: []string{
				siteCookie(navrSite),
			},
		})
	// NavRedirectHTTPCookies: a cookie set over http://, so neither Secure
	// nor SameSite=None (Lax by default, sent on a top-level GET): the hop
	// to https:// of the host carries it unless the browser binds cookies
	// to their scheme.
	NavRedirectHTTPCookies = navRedirect("nav-redirect-http-cookies", "nav-redirect-http, its 301 setting a cookie of its host",
		NavRedirectStep{
			Label:  "nav.http",
			Host:   navrHost,
			Plain:  true,
			Status: 301,
			Next:   navrHost,
			SetCookies: []string{
				"rc_http=1; Path=/",
			},
		})
	// NavRedirectCSCookies is nav-redirect-cs's hop with a cookie: one of
	// the other site needs the browser to have met that site, so the typed
	// navigation starts there, and its 302 sets the cookie, goes to the
	// page's site and back. Both hops go to another site (the conditions of
	// nav-redirect-cs's hop); the second carries the cookie.
	NavRedirectCSCookies = navRedirect("nav-redirect-cs-cookies", "a typed navigation answered 302 to another site and 302 back, the first 302 setting a cookie of its site the second hop carries",
		NavRedirectStep{
			Label:  "nav.cs",
			Host:   navrCrossSite,
			Status: 302,
			Next:   navrHost,
			Then:   navrCrossSite,
			SetCookies: []string{
				siteCookie(navrCrossApex),
			},
		})
)

// NavRedirectPage is the nav-redirect-page scenario: script navigations
// and forms redirected to another origin (see NavRedirectPageSteps).
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
// in order, each made by script (no user activation) from a page of its
// own on www.navr-a.test and redirected once to another origin. Labels
// name the first request's site, then the hop's.
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

// NavRedirectForm is the nav-redirect-form scenario: form POSTs submitted
// by script and redirected (see NavRedirectFormSteps).
var NavRedirectForm = &Scenario{
	Name: "nav-redirect-form",
	Doc: "form POSTs a page's script submits answered " + navrFormStatus + ": " + navrFormDoc +
		"; the page's form without user activation, as nav-redirect-click's with it",
	multi: redirects2Page{
		fixedHosts: fixedHosts{
			names: []string{
				navrHost,
				navrSite,
				navrStatic,
				navrCrossSite,
			},
			runFor: 60 * time.Second,
		},
		steps: NavRedirectFormSteps(),
	},
}

// navrForms are the form POSTs' first hosts and redirect targets ("" for
// the first host), labelled by their sites.
var navrForms = []struct {
	sites, host, next string
}{
	{"so", navrHost, ""},
	{"so-ss", navrHost, navrStatic},
	{"so-cs", navrHost, navrCrossSite},
	{"ss", navrStatic, ""},
	{"ss-so", navrSite, navrHost},
	{"cs", navrCrossSite, ""},
	{"cs-ss", navrCrossSite, navrStatic},
	{"cs-so", navrCrossSite, navrHost},
}

// navrFormDoc describes navrForms in the scenarios' Doc.
const navrFormDoc = "to the page's origin within it, to static.navr-a.test (its site) and to another site; to static.navr-a.test within it; " +
	"to the apex to www.navr-a.test (the page's origin); to another site within it, to static.navr-a.test and back to the page's origin"

// navrFormStatuses: 301 to 303 turn a POST into a GET, 307 and 308 keep it.
var navrFormStatuses = []int{301, 302, 303, 307, 308}

// navrFormStatus names navrFormStatuses in the scenarios' Doc.
const navrFormStatus = "301, 302, 303, 307 and 308"

// navrFormSteps returns a form POST step for each of navrForms and
// navrFormStatuses, labelled <prefix>-<status>.<sites>.
func navrFormSteps(prefix string, click bool) []Redirects2Step {
	steps := make([]Redirects2Step, 0, len(navrForms)*len(navrFormStatuses))
	for _, to := range navrForms {
		for _, status := range navrFormStatuses {
			steps = append(steps, Redirects2Step{
				Label:  fmt.Sprintf("%s-%d.%s", prefix, status, to.sites),
				Kind:   RedirectForm,
				Host:   to.host,
				Next:   to.next,
				Method: "POST",
				Body:   "a=1",
				Status: status,
				Hops:   1,
				Click:  click,
			})
		}
	}

	return steps
}

// NavRedirectFormSteps returns the nav-redirect-form page's form POSTs,
// in order, each submitted by script: form-<status>.<sites>.
func NavRedirectFormSteps() []Redirects2Step {
	return navrFormSteps("form", false)
}

// NavRedirectClick is the nav-redirect-click scenario: clicked links and
// form submit buttons, redirected (see NavRedirectClickSteps). How each
// browser is clicked is in docs/capturing.md.
var NavRedirectClick = &Scenario{
	Name: "nav-redirect-click",
	Doc: "navigations with user activation, clicked links and form submit buttons, redirected: links answered 301 or 302 within an origin, " +
		"to another origin of its site and to another site, from the page's site and another site; form POSTs answered " + navrFormStatus +
		": " + navrFormDoc,
	multi: redirects2Page{
		fixedHosts: fixedHosts{
			names: []string{
				navrHost,
				navrSite,
				navrStatic,
				navrCrossSite,
				navrCrossApex,
			},
			runFor: 120 * time.Second,
		},
		steps: NavRedirectClickSteps(),
	},
}

// The page navigations' cookie twins: each is its original (named without
// "-cookies") but for its cookie handling (redirects2Page.cookies), so its
// requests' header lists differ from the original's only by the cookie
// slot.
var (
	NavRedirectPageCookies  = cookieTwin(NavRedirectPage)
	NavRedirectFormCookies  = cookieTwin(NavRedirectForm)
	NavRedirectClickCookies = cookieTwin(NavRedirectClick)
)

// NavRedirectClickSteps returns the nav-redirect-click page's
// navigations, in order, each clicked (so with user activation) on a page
// of its own on www.navr-a.test: GETs redirected between each pair of
// relations, then submit-<status>.<sites> as nav-redirect-form's.
func NavRedirectClickSteps() []Redirects2Step {
	link := func(label, host, next string, status int) Redirects2Step {
		return Redirects2Step{
			Label:  label,
			Kind:   RedirectNavigate,
			Host:   host,
			Next:   next,
			Status: status,
			Hops:   1,
			Click:  true,
		}
	}
	steps := []Redirects2Step{
		link("link-302.so", navrHost, "", 302),
		link("link-301.so-ss", navrHost, navrStatic, 301),
		link("link-302.so-cs", navrHost, navrCrossSite, 302),
		link("link-302.ss", navrStatic, "", 302),
		link("link-301.ss-so", navrSite, navrHost, 301),
		link("link-302.cs", navrCrossSite, "", 302),
		link("link-301.cs-cs", navrCrossApex, navrCrossSite, 301),
		link("link-302.cs-ss", navrCrossSite, navrStatic, 302),
	}

	steps = append(steps, navrFormSteps("submit", true)...)

	return append(steps, link("link-302.cs-so", navrCrossSite, navrHost, 302))
}

// NavRedirects are the nav-redirect scenarios: the originals, then their
// cookie twins.
var NavRedirects = []*Scenario{
	NavRedirectSO,
	NavRedirectSS,
	NavRedirectCS,
	NavRedirectHTTP,
	NavRedirectPage,
	NavRedirectForm,
	NavRedirectClick,
	NavRedirectSOCookies,
	NavRedirectSSCookies,
	NavRedirectCSCookies,
	NavRedirectHTTPCookies,
	NavRedirectPageCookies,
	NavRedirectFormCookies,
	NavRedirectClickCookies,
}

// NavRedirectStep is a nav-redirect page: /p/<Label> on Host (plain HTTP
// if Plain) answered Status to https:// Next /p/<Label>.hop1, with a
// Set-Cookie for each of SetCookies; with Then, .hop1 is answered Status
// again to https:// Then /p/<Label>.hop2. The last hop's page asks for
// /done, which deletes SetCookies.
type NavRedirectStep struct {
	Label      string
	Host       string
	Plain      bool
	Status     int
	Next       string
	Then       string
	SetCookies []string
}

// StartPath is the start URL's path.
func (s NavRedirectStep) StartPath() string {
	return "/p/" + s.Label
}

// HopPaths are the redirect targets' paths, in order.
func (s NavRedirectStep) HopPaths() []string {
	out := []string{
		s.hopPath(1),
	}
	if s.Then != "" {
		out = append(out, s.hopPath(2))
	}

	return out
}

// LastHost is the host the navigation lands on.
func (s NavRedirectStep) LastHost() string {
	if s.Then != "" {
		return s.Then
	}

	return s.Next
}

func (s NavRedirectStep) hopPath(n int) string {
	return "/p/" + s.Label + ".hop" + strconv.Itoa(n)
}

// NavRedirectStep returns a nav-redirect scenario's page; ok is false for
// other scenarios.
func (sc *Scenario) NavRedirectStep() (step NavRedirectStep, ok bool) {
	p, ok := sc.multi.(navRedirectPage)
	return p.step, ok
}

// StartPath is the path the browser starts at: "/" except for the
// nav-redirect pages.
func (sc *Scenario) StartPath() string {
	if s, ok := sc.NavRedirectStep(); ok {
		return s.StartPath()
	}

	return "/"
}

// navRedirect starts on its own plain port if the step is Plain.
func navRedirect(name, doc string, step NavRedirectStep) *Scenario {
	hosts := []string{
		step.Host,
	}
	for _, h := range []string{step.Next, step.Then} {
		if h != "" && !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
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

	if req.ports.tls != "" {
		port = req.ports.tls
	}

	redirect := func(to string, n int, cookies []string) response {
		headers := append(make([]hpack.HeaderField, 0, 1+len(cookies)), hpack.HeaderField{
			Name:  "location",
			Value: "https://" + net.JoinHostPort(to, port) + s.hopPath(n),
		})

		return response{
			status:      s.Status,
			contentType: "text/html; charset=utf-8",
			headers:     append(headers, setCookies(cookies)...),
		}
	}
	hops := s.HopPaths()

	switch {
	case path == "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n"), headers: setCookies(cookieDeletions(s.SetCookies))}
	case host == s.Host && path == s.StartPath() && plain == s.Plain:
		return redirect(s.Next, 1, s.SetCookies)
	case s.Then != "" && host == s.Next && path == hops[0] && !plain:
		return redirect(s.Then, 2, nil)
	case host == s.LastHost() && path == hops[len(hops)-1] && !plain:
		return htmlResponse(htmlPage("nav-redirect", "", "<script>fetch(\"/done\");</script>\n"))
	}

	return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
}

// setCookies are a Set-Cookie field for each of cookies.
func setCookies(cookies []string) []hpack.HeaderField {
	out := make([]hpack.HeaderField, 0, len(cookies))
	for _, c := range cookies {
		out = append(out, hpack.HeaderField{
			Name:  "set-cookie",
			Value: c,
		})
	}

	return out
}

// siteCookie is the cookie a cookie twin sets for a site (its registrable
// domain): sent on any request to the site's hosts that carries cookies
// (Secure, SameSite=None: cross-site POST navigations too).
func siteCookie(site string) string {
	name, _, _ := strings.Cut(site, ".")

	return "rc_" + name + "=1; Domain=" + site + "; Path=/; Secure; SameSite=None"
}
