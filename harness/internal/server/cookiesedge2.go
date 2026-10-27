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

// The cookies-edge2 page's hosts: one for schemeful SameSite, served over
// https:// on the page's port and over http:// on its own port (cookies are
// not bound to a port), and one for the nameless and __Http- cookies, so
// each group stays off the other's probes.
const (
	edge2Host         = "www.cookies-s.test"
	edge2NamelessHost = "www.cookies-n.test"
)

// CookiesEdge2 is the cookies-edge2 scenario: what cookies-edge left
// unmeasured. Cookies follow the same rules as CookiesEdge's. See
// harness/reports/cookies-edge-2026-10-24.md.
var CookiesEdge2 = &Scenario{
	Name: "cookies-edge2",
	Doc:  "cookie rules cookies-edge left open: schemeful SameSite across https:// and http:// of one host with http:// on a port without TLS (Safari's HTTPS upgrade fails and falls back), nameless cookies without a second \"=\", __Http- and __Host-Http- without Secure",
	multi: cookiesEdge2Page{
		fixedHosts: fixedHosts{
			names: []string{
				edge2Host,
				edge2NamelessHost,
			},
			runFor: 30 * time.Second,
		},
	},
	plainPort: true,
}

// edge2SchemeCookies are set from https:// and are not Secure (but the
// last), so they may go to http:// of the same host.
var edge2SchemeCookies = []string{
	"e2_sch_strict=1; Path=/; SameSite=Strict; Max-Age=600",
	"e2_sch_lax=1; Path=/; SameSite=Lax; Max-Age=600",
	"e2_sch_unset=1; Path=/; Max-Age=600",
	"e2_sch_secure=1; Path=/; Secure; SameSite=Lax; Max-Age=600",
}

// edge2NamelessCookies are nameless cookies without a second "=" (every
// browser refused cookies-edge's, which had one). Each has its own nested
// path, as in edgeNamelessCookies, and is sent as its value alone, so each
// value names its cookie.
var edge2NamelessCookies = []string{
	"=ctl; Path=/n; Secure; Max-Age=600",
	"=__Secure-x; Path=/n/1; Secure; Max-Age=600",
	"= __Host-x; Path=/n/1/2; Secure; Max-Age=600",
	"=__secure-x; Path=/n/1/2/3; Secure; Max-Age=600",
	"=x; Path=/n/1/2/3/4; Max-Age=600",
}

// edge2HTTPCookies are __Http- and __Host-Http- cookies with HttpOnly,
// with Secure (the controls, which cookies-edge saw kept) and without.
var edge2HTTPCookies = []string{
	"__Http-e2_ok=1; Path=/; Secure; HttpOnly; Max-Age=600",
	"__Http-e2_nosec=1; Path=/; HttpOnly; Max-Age=600",
	"__Host-Http-e2_ok=1; Path=/; Secure; HttpOnly; Max-Age=600",
	"__Host-Http-e2_nosec=1; Path=/; HttpOnly; Max-Age=600",
}

// edge2PlainWatch is how long the watchdog waits after a page leaves for
// the plain port: the http:// page loads, makes its fetch (5 s at most)
// and goes back to https://, whose page disarms it.
const edge2PlainWatch = 15 * time.Second

// CookiesEdge2Steps lists the cookies-edge2 page's requests in the order
// it makes them. A Plain step goes to the plain port, which answers no TLS:
// a browser trying https:// there is refused and falls back to http://.
// What each probe answers is in harness/reports/cookies-edge-2026-10-24.md.
func CookiesEdge2Steps() []CookiesEdgeStep {
	return []CookiesEdgeStep{
		{Label: "doc", Host: edge2Host, Path: "/", Nav: true, Sets: edge2SchemeCookies},
		{Label: "sch-start-probe", Host: edge2Host, Path: "/e/sch-start-probe"},
		{Label: "sch-to-http", Host: edge2Host, Plain: true, Path: "/e/sch-to-http", Nav: true},
		{Label: "sch-http-probe", Host: edge2Host, Plain: true, Path: "/e/sch-http-probe"},
		{Label: "sch-to-https", Host: edge2Host, Path: "/e/sch-to-https", Nav: true},
		{Label: "sch-https-probe", Host: edge2Host, Path: "/e/sch-https-probe"},
		{Label: "sch-cleanup", Host: edge2Host, Path: "/cleanup", Sets: cookieDeletions(edge2SchemeCookies)},
		{Label: "n-doc", Host: edge2NamelessHost, Path: "/d/n-doc", Nav: true, Sets: edge2NamelessCookies},
		{Label: "nameless-probe", Host: edge2NamelessHost, Path: "/n/1/2/3/4/nameless-probe"},
		{Label: "http-set", Host: edge2NamelessHost, Path: "/h/set", Sets: edge2HTTPCookies},
		{Label: "http-probe", Host: edge2NamelessHost, Path: "/h/http-probe"},
		{Label: "n-cleanup", Host: edge2NamelessHost, Path: "/cleanup", Sets: cookieDeletions(slices.Concat(edge2NamelessCookies, edge2HTTPCookies))},
	}
}

type cookiesEdge2Page struct {
	fixedHosts
}

func (p cookiesEdge2Page) answer(req request) response {
	host, port := splitAuthority(req.host)
	path, _, _ := strings.Cut(req.path, "?")

	switch path {
	case "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	case watchdogPath:
		return watchdogResponse()
	}

	plain := port == req.ports.plain
	steps := CookiesEdge2Steps()

	for i, s := range steps {
		if s.Host != host || s.Path != path || s.Plain != plain {
			continue
		}

		r := response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
		if s.Nav {
			r = response{status: 200, contentType: "text/html; charset=utf-8", body: htmlPage("cookies-edge2", "", pageScript(edge2Statements(steps, i, req.ports)))}
		}

		for _, c := range s.Sets {
			r.headers = append(r.headers, hpack.HeaderField{
				Name:  "set-cookie",
				Value: c,
			})
		}

		return r
	}

	return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
}

// edge2URL is a step's URL: Plain steps on the plain port.
func edge2URL(s CookiesEdgeStep, ports portPair) string {
	if s.Plain {
		return "http://" + net.JoinHostPort(s.Host, ports.plain) + s.Path
	}

	return "https://" + net.JoinHostPort(s.Host, ports.tls) + s.Path
}

// edge2Statements are step i's page: it fetches the steps up to the next
// navigation (5 s at most each), then goes there, or asks for /done. The
// watchdog lives on https:// of edge2Host: its first page registers it,
// others disarm it on load, arm it before leaving for the plain port and
// end it before leaving the host.
func edge2Statements(steps []CookiesEdgeStep, i int, ports portPair) []string {
	page := steps[i]
	watched := page.Host == edge2Host && !page.Plain
	out := []string{
		`const get = u => within(fetch(u, {credentials: "include"}).then(r => r.text()), 5000);`,
	}

	switch {
	case i == 0:
		out = append(out, "await register();")
	case watched:
		out = append(out, "await arm(0);")
	}

	for _, s := range steps[i+1:] {
		if !s.Nav {
			out = append(out, fmt.Sprintf("await get(%s);", strconv.Quote(s.Path)))
			continue
		}

		switch {
		case watched && s.Plain:
			out = append(out, fmt.Sprintf("await arm(%d);", edge2PlainWatch.Milliseconds()))
		case watched && s.Host != page.Host:
			out = append(out, "await end();")
		}

		return append(out, fmt.Sprintf("location.href = %s;", strconv.Quote(edge2URL(s, ports))))
	}

	return append(out, `fetch("/done");`)
}
