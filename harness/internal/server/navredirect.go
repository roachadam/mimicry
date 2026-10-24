package server

import (
	"net"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// The nav-redirect pages' hosts: a site's apex and www host, and another
// site.
const (
	navrSite      = "navr-a.test"
	navrHost      = "www.navr-a.test"
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

// NavRedirects are the nav-redirect scenarios.
var NavRedirects = []*Scenario{
	NavRedirectSO,
	NavRedirectSS,
	NavRedirectCS,
	NavRedirectHTTP,
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
