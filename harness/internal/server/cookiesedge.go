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

// The cookies-edge page's hosts: its own, served over https:// and http://
// on one port; a site of its own for the 200 cookies, so their eviction
// cannot touch the other probes; and loopback over http://.
const (
	edgeHost     = "www.cookies-e.test"
	edgeManyHost = "www.cookies-m.test"
	edgeLoopback = "localhost"
)

// CookiesEdge is the cookies-edge scenario: cookie rules the cookies and
// kinds pages do not reach. Every cookie is set by Set-Cookie, lives 10
// minutes at most and is deleted before /done, since Safari keeps cookies
// between samples. See harness/reports/cookies-edge-2026-10-24.md.
var CookiesEdge = &Scenario{
	Name: "cookies-edge",
	Doc:  "cookie edge rules: __Host- paths, nameless prefixed cookies, __Http- prefixes, order after a value changes, 200 cookies on one site, schemeful SameSite across http:// and https:// of one host, Secure cookies on http://localhost",
	multi: cookiesEdgePage{
		fixedHosts: fixedHosts{
			names: []string{
				edgeHost,
				edgeManyHost,
				edgeLoopback,
			},
			runFor: 30 * time.Second,
		},
	},
	mixedScheme: true,
}

// edgePrefixCookies are set by the page's document, at "/" so that a cookie
// without Path gets "/" as its default path. Probe: prefix-probe.
var edgePrefixCookies = []string{
	// The control: a valid __Host- cookie.
	"__Host-e_ok=1; Path=/; Secure; Max-Age=600",
	// __Host- without a Path attribute (the default path is "/").
	"__Host-e_nopath=1; Secure; Max-Age=600",
	// __Host- with a Path other than "/".
	"__Host-e_pathx=1; Path=/x; Secure; Max-Age=600",
	// __Http- and __Host-Http- with HttpOnly, as they require, and without.
	"__Http-e_http=1; Path=/; Secure; HttpOnly; Max-Age=600",
	"__Http-e_nohttp=1; Path=/; Secure; Max-Age=600",
	"__Host-Http-e_hh=1; Path=/; Secure; HttpOnly; Max-Age=600",
	"__Host-Http-e_nohh=1; Path=/; Secure; Max-Age=600",
}

// edgeNamelessCookies are nameless cookies whose value starts with a prefix,
// plus a control. A browser keeps one nameless cookie per path, so each has
// its own nested path and nameless-probe carries every one kept.
var edgeNamelessCookies = []string{
	"=e_nlctl=1; Path=/n; Secure; Max-Age=600",
	"=__Secure-e_nls=1; Path=/n/1; Secure; Max-Age=600",
	"= __Secure-e_nlss=1; Path=/n/1/2; Secure; Max-Age=600",
	"=__Host-e_nlh=1; Path=/n/1/2/3; Secure; Max-Age=600",
	"= __Host-e_nlhs=1; Path=/n/1/2/3/4; Secure; Max-Age=600",
}

// edgeSchemeCookies are not Secure (but the last), so they may go to
// http:// of the page's host; the sch-* steps probe schemeful SameSite.
var edgeSchemeCookies = []string{
	"e_sch_strict=1; Path=/; SameSite=Strict; Max-Age=600",
	"e_sch_lax=1; Path=/; SameSite=Lax; Max-Age=600",
	"e_sch_unset=1; Path=/; Max-Age=600",
	"e_sch_secure=1; Path=/; Secure; SameSite=Lax; Max-Age=600",
}

// edgeRotate sets a rotation cookie, under /r so only the rotation probes
// carry them.
func edgeRotate(name, value string) string {
	return name + "=" + value + "; Path=/r; Secure; Max-Age=600"
}

// edgeLoopbackCookies are set by http://localhost: a Secure one, which
// only a browser treating loopback as secure keeps, and a control.
var edgeLoopbackCookies = []string{
	"e_lo_secure=1; Path=/; Secure; Max-Age=600",
	"e_lo_plain=1; Path=/; Max-Age=600",
}

// edgeManyCount is how many cookies the many-page sets on its site, more
// than any browser's per-site limit (Chrome and Firefox: 180).
const edgeManyCount = 200

// edgeManyCookies are the many-page's cookies, e_m000 to e_m199, set in
// that order in one response, each with a 16-character value.
func edgeManyCookies() []string {
	out := make([]string, 0, edgeManyCount)
	for i := range edgeManyCount {
		name := fmt.Sprintf("e_m%03d", i)
		out = append(out, name+"="+cookieValue(name, 16)+"; Path=/; Max-Age=600")
	}
	return out
}

// CookiesEdgeStep is one request of the cookies-edge page.
type CookiesEdgeStep struct {
	// Label names the step.
	Label string
	// Host is the step's host; Plain sends it over http://, otherwise
	// https:// (HTTP/2).
	Host  string
	Plain bool
	// Path is the request's path: on each host, every step has its own.
	Path string
	// Nav is a top-level navigation by script from the previous step's page.
	// Other steps are same-origin credentialed fetch()es from the last Nav's page.
	Nav bool
	// Sets are the Set-Cookie values its response carries, in order.
	Sets []string
}

// CookiesEdgeSteps lists the cookies-edge page's requests in the order it
// makes them. What each probe's Cookie header answers is in
// harness/reports/cookies-edge-2026-10-24.md; the cleanup requests delete
// every cookie in the form it was set.
func CookiesEdgeSteps() []CookiesEdgeStep {
	doc := slices.Concat(edgePrefixCookies, edgeNamelessCookies, edgeSchemeCookies)
	rotation := []string{
		edgeRotate("e_rot_a", "1"),
		edgeRotate("e_rot_b", "1"),
		edgeRotate("e_rot_c", "1"),
	}
	many := edgeManyCookies()
	return []CookiesEdgeStep{
		{Label: "doc", Host: edgeHost, Path: "/", Nav: true, Sets: doc},
		{Label: "prefix-probe", Host: edgeHost, Path: "/x/prefix-probe"},
		{Label: "nameless-probe", Host: edgeHost, Path: "/n/1/2/3/4/nameless-probe"},
		{Label: "rot-set-a", Host: edgeHost, Path: "/r/set-a", Sets: rotation[:1]},
		{Label: "rot-set-b", Host: edgeHost, Path: "/r/set-b", Sets: rotation[1:2]},
		{Label: "rot-set-c", Host: edgeHost, Path: "/r/set-c", Sets: rotation[2:]},
		{Label: "rot-probe-1", Host: edgeHost, Path: "/r/probe-1"},
		{Label: "rot-change-a", Host: edgeHost, Path: "/r/change-a", Sets: []string{edgeRotate("e_rot_a", "2")}},
		{Label: "rot-same-b", Host: edgeHost, Path: "/r/same-b", Sets: []string{edgeRotate("e_rot_b", "1")}},
		{Label: "rot-probe-2", Host: edgeHost, Path: "/r/probe-2"},
		{Label: "sch-to-http", Host: edgeHost, Plain: true, Path: "/e/sch-to-http", Nav: true},
		{Label: "sch-http-probe", Host: edgeHost, Plain: true, Path: "/e/sch-http-probe"},
		{Label: "sch-to-https", Host: edgeHost, Path: "/e/sch-to-https", Nav: true},
		{Label: "sch-https-probe", Host: edgeHost, Path: "/e/sch-https-probe"},
		{Label: "lo-page", Host: edgeLoopback, Plain: true, Path: "/e/lo-page", Nav: true, Sets: edgeLoopbackCookies},
		{Label: "lo-probe", Host: edgeLoopback, Plain: true, Path: "/e/lo-probe"},
		{Label: "lo-cleanup", Host: edgeLoopback, Plain: true, Path: "/cleanup", Sets: cookieDeletions(edgeLoopbackCookies)},
		{Label: "many-page", Host: edgeManyHost, Path: "/e/many-page", Nav: true, Sets: many},
		{Label: "many-probe", Host: edgeManyHost, Path: "/e/many-probe"},
		{Label: "many-cleanup", Host: edgeManyHost, Path: "/cleanup", Sets: cookieDeletions(many)},
		// At "/cleanup", so deleting __Host-e_nopath without a Path has
		// the default path "/", as its setting did.
		{Label: "cleanup", Host: edgeHost, Path: "/cleanup", Nav: true, Sets: cookieDeletions(slices.Concat(doc, rotation))},
	}
}

type cookiesEdgePage struct {
	fixedHosts
}

func (p cookiesEdgePage) answer(req request) response {
	host, port := splitAuthority(req.host)
	path, _, _ := strings.Cut(req.path, "?")
	if path == "/done" {
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}
	steps := CookiesEdgeSteps()
	for i, s := range steps {
		if s.Host != host || s.Path != path {
			continue
		}
		r := response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
		if s.Nav {
			r = response{status: 200, contentType: "text/html; charset=utf-8", body: htmlPage("cookies-edge", "", "<script>"+edgeScript(steps[i+1:], port)+"</script>\n")}
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

// edgeScript is a navigation's page: it fetches the steps up to the next
// navigation one after another (by path, so on whatever origin the page
// loaded from; 5 s at most each), then goes there, or asks for /done.
func edgeScript(rest []CookiesEdgeStep, port string) string {
	var b strings.Builder
	b.WriteString("(async () => {\nconst get = u => Promise.race([fetch(u, {credentials: \"include\"}).then(r => r.text()).catch(() => {}), new Promise(d => setTimeout(d, 5000))]);\n")
	for _, s := range rest {
		if s.Nav {
			scheme := "https://"
			if s.Plain {
				scheme = "http://"
			}
			fmt.Fprintf(&b, "location.href = %s;\n})();", strconv.Quote(scheme+net.JoinHostPort(s.Host, port)+s.Path))
			return b.String()
		}
		fmt.Fprintf(&b, "await get(%s);\n", strconv.Quote(s.Path))
	}
	b.WriteString("fetch(\"/done\");\n})();")
	return b.String()
}
