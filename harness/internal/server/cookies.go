package server

import (
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// cookiesSiteC is a second top-level site: the page navigates to it and
// back, to show cookies on cross-site navigations and partitioned cookies
// under another top-level site.
const cookiesSiteC = "www.kinds-c.test"

// CookiesSet are what page A's document sets: each SameSite value, None
// without Secure, and cookies of mixed paths and domains to show the order
// they are sent in. Every name starts with "c" so cleanup can find them.
var CookiesSet = []string{
	"c_strict=1; Path=/; Secure; SameSite=Strict",
	"c_lax=1; Path=/; Secure; SameSite=Lax",
	"c_none=1; Path=/; Secure; SameSite=None",
	"c_unset=1; Path=/; Secure",
	"c_noneinsecure=1; Path=/; SameSite=None",
	"co_k=1; Path=/k; Secure",
	"co_dom=1; Domain=" + kindsSiteA + "; Path=/; Secure",
	"co_dk=1; Domain=" + kindsSiteA + "; Path=/k; Secure",
	"co_alpha=1; Path=/; Secure",
}

// CookiesThirdParty are set by the cross-site host on an image under
// top-level site A; requests from site C show whether they are kept apart.
var CookiesThirdParty = []string{
	"cb_none=1; Path=/; Secure; SameSite=None",
	"cb_part=1; Path=/; Secure; SameSite=None; Partitioned",
	"cb_unset=1; Path=/; Secure",
	"cb_lax=1; Path=/; Secure; SameSite=Lax",
}

// CookiesRedirects are the image redirects page A follows, by label
// (".<from>-<to>"): cross-site to same-site, same-site to the page's
// origin, cross-site to the page's origin.
var CookiesRedirects = map[string]string{
	"c-redirect.cs-ss": kindsSameSite,
	"c-redirect.ss-so": kindsHost,
	"c-redirect.cs-so": kindsHost,
}

// Cookies is the cookies scenario: page A sets cookies and third-party
// cookies, follows redirects across sites, navigates to site C (which
// loads A's and the cross-site host's images, then navigates back to A by
// a link and by a form POST), and cleans up.
var Cookies = &Scenario{
	Name: "cookies",
	Doc:  "cookies on cross-site navigations (GET, form POST), across top-level sites (partitioned), after cross-site redirects; SameSite=None without Secure; order with mixed paths and domains",
	multi: cookiesPage{
		fixedHosts: fixedHosts{
			names: []string{
				kindsHost,
				kindsSameSite,
				kindsCrossSite,
				cookiesSiteC,
			},
			runFor: 40 * time.Second,
		},
	},
}

type cookiesPage struct {
	fixedHosts
}

func (p cookiesPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	origin := originOn("https://", port)
	html := func(script string) response {
		return response{status: 200, contentType: "text/html; charset=utf-8", body: htmlPage("cookies", "", "<script>(async () => {\n"+
			cookiesLoad+script+"\n})();</script>\n")}
	}
	image := response{status: 200, contentType: "image/png", body: png}
	path, _, _ := strings.Cut(req.path, "?")
	switch {
	case host == kindsHost && path == "/":
		r := html(`await load("` + origin(kindsCrossSite) + `/k/c-setcookie.cs");
await load("/k/c-order.so");
await load("` + origin(kindsCrossSite) + `/k/c-redirect.cs-ss");
await load("` + origin(kindsSameSite) + `/k/c-redirect.ss-so");
await load("` + origin(kindsCrossSite) + `/k/c-redirect.cs-so");
location.href = "` + origin(cookiesSiteC) + `/c";`)
		for _, c := range CookiesSet {
			r.headers = append(r.headers, hpack.HeaderField{Name: "set-cookie", Value: c})
		}

		return r
	case host == cookiesSiteC && path == "/c":
		return html(`await load("` + origin(kindsCrossSite) + `/k/c-part.cs");
await load("` + origin(kindsHost) + `/k/c-third.so");
location.href = "` + origin(kindsHost) + `/k/c-nav-get.so";`)
	case host == kindsHost && path == "/k/c-nav-get.so":
		return html(`location.href = "` + origin(cookiesSiteC) + `/c-post";`)
	case host == cookiesSiteC && path == "/c-post":
		return html(`const f = document.createElement("form"); f.method = "POST"; f.action = "` + origin(kindsHost) + `/k/c-nav-post.so";
const i = document.createElement("input"); i.name = "a"; i.value = "1"; f.appendChild(i);
document.body.appendChild(f); f.submit();`)
	case host == kindsHost && path == "/k/c-nav-post.so":
		var cleanup []string
		for _, h := range p.hosts() {
			cleanup = append(cleanup, strconv.Quote(origin(h)+"/cleanup"))
		}

		return html(`await Promise.all([` + strings.Join(cleanup, ", ") + `].map(u => fetch(u, {credentials: "include", mode: "no-cors"}).catch(() => {})));
fetch("/done");`)
	case path == "/cleanup" || path == "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n"), headers: cookiesDeletions(host)}
	case host == kindsCrossSite && path == "/k/c-setcookie.cs":
		for _, c := range CookiesThirdParty {
			image.headers = append(image.headers, hpack.HeaderField{Name: "set-cookie", Value: c})
		}

		return image
	}
	label, ok := strings.CutPrefix(path, "/k/")
	if !ok {
		return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
	}
	if to, ok := CookiesRedirects[label]; ok {
		return response{status: 302, contentType: "text/plain", headers: []hpack.HeaderField{
			{Name: "location", Value: origin(to) + "/k/" + label + ".hop"},
		}}
	}

	return image
}

// cookiesLoad is the pages' helper: load an image, give up after 5 s.
const cookiesLoad = `const load = u => new Promise(d => { const i = new Image(); i.onload = i.onerror = () => d(); setTimeout(d, 5000); i.src = u; });
`

// cookiesDeletions deletes every cookie the scenario sets on host, with
// the attributes it was set with.
func cookiesDeletions(host string) []hpack.HeaderField {
	var set []string
	switch host {
	case kindsHost:
		set = CookiesSet
	case kindsCrossSite:
		set = CookiesThirdParty
	}
	out := make([]hpack.HeaderField, 0, len(set))
	for _, c := range set {
		name := setCookieName(c)
		path, domain, partitioned := "/", "", ""
		for _, a := range strings.Split(c, "; ")[1:] {
			switch {
			case strings.HasPrefix(a, "Path="):
				path = strings.TrimPrefix(a, "Path=")
			case strings.HasPrefix(a, "Domain="):
				domain = "; " + a
			case a == "Partitioned":
				partitioned = "; SameSite=None; Partitioned"
			}
		}
		out = append(out, hpack.HeaderField{
			Name:  "set-cookie",
			Value: name + "=; Path=" + path + domain + partitioned + "; Secure; Max-Age=0",
		})
	}

	return out
}
