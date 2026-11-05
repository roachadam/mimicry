package server

import (
	"strings"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// The urls page's hosts: the page's own, its uppercase, trailing-dot and
// default-port forms included; one only ever named with a trailing dot,
// so its connections are its own (a browser may pool www.urls.test.
// with www.urls.test, whose certificate names it but for the dot); an
// internationalized one (Bücher), and a malformed punycode label, served
// in case a browser sends it. IP-literal hosts are on the plain port: a
// browser sends no SNI for them, so TLS would show nothing more.
const (
	urlsHost    = "www.urls.test"
	urlsDotHost = "dot.urls.test"
	urlsIDNHost = "xn--bcher-kva.urls.test"
	urlsBadHost = "xn--a.urls.test"
)

// URLs is the urls scenario: the page has the browser parse awkward URLs,
// each by fetch() and by a navigation (an iframe), and the server records
// the request line or :path, Host or :authority and SNI as they arrive.
// Issue #71.
var URLs = &Scenario{
	Name: "urls",
	Doc:  "awkward URLs parsed by the browser, fetched and navigated to: query and path characters, dot segments, hosts in other forms (uppercase, internationalized, trailing dot, explicit default port, short IPv4 forms, malformed punycode)",
	multi: urlsPage{
		fixedHosts: fixedHosts{
			names: []string{
				urlsHost,
				urlsDotHost,
				urlsIDNHost,
				urlsBadHost,
				// Only so the browser resolves them (Chrome's MAP rules,
				// Firefox's localDomains). A server name ending in a dot
				// is recorded, then refused: Go's TLS cannot serve it.
				urlsHost + ".",
				urlsDotHost + ".",
			},
			runFor: 90 * time.Second,
		},
	},
	plainPort:   true,
	defaultPort: true,
}

// URLCase is a URL the urls page has the browser parse. URL is as the page
// writes it, with {tls} and {plain} for the server's ports and {c} for the
// case's path prefix (URLCasePrefix), which every request it sends starts
// with.
type URLCase struct {
	ID  string
	URL string
}

// URLCasePrefix is case id's path prefix for kind: the page's "f"
// (fetch()) or "n" (a navigation), or a kind of the caller's.
func URLCasePrefix(kind, id string) string {
	return "/c/" + kind + "-" + id + "/"
}

// On is c's URL with the ports and kind's prefix filled in.
func (c URLCase) On(kind, tlsPort, plainPort string) string {
	return strings.NewReplacer(
		"{tls}", tlsPort,
		"{plain}", plainPort,
		"{c}", URLCasePrefix(kind, c.ID),
	).Replace(c.URL)
}

// URLsCaptures returns p's existing urls captures in dir (own or
// inherited): what kindsgen derives the profile's urls from.
func URLsCaptures(dir string, p *profile.Spec) ([]capture.File, error) {
	return existingCaptures(dir, p, URLs)
}

// URLChar is a character the urls page puts in a query (case "q-"+ID)
// and in a path (case "p-"+ID), between "a" and "b".
type URLChar struct {
	ID, Char string
}

// URLChars lists the urls page's characters in the order it uses them.
func URLChars() []URLChar {
	return []URLChar{
		{"space", " "},
		{"dquote", `"`},
		{"squote", "'"},
		{"lt", "<"},
		{"gt", ">"},
		{"backtick", "`"},
		{"lbrace", "{"},
		{"rbrace", "}"},
		{"bar", "|"},
		{"backslash", `\`},
		{"caret", "^"},
		{"nonascii", "é"},
		{"lonepct", "%"},
		{"pctxx", "%41"},
	}
}

// URLCases lists the urls page's URLs in the order it requests them.
func URLCases() []URLCase {
	const page = "https://" + urlsHost + ":{tls}{c}"

	var out []URLCase

	for _, ch := range URLChars() {
		out = append(out, URLCase{
			ID:  "q-" + ch.ID,
			URL: page + "?a" + ch.Char + "b",
		})
	}

	for _, ch := range URLChars() {
		out = append(out, URLCase{
			ID:  "p-" + ch.ID,
			URL: page + "a" + ch.Char + "b",
		})
	}

	return append(out,
		URLCase{
			ID:  "p-dots",
			URL: page + "a/./b/../c",
		},
		URLCase{
			ID:  "p-pctdots",
			URL: page + "a/%2e/b/%2E%2e/c",
		},
		URLCase{
			ID:  "h-upper",
			URL: "https://WWW.URLS.TEST:{tls}{c}",
		},
		URLCase{
			ID:  "h-idn",
			URL: "https://Bücher.urls.test:{tls}{c}",
		},
		URLCase{
			ID:  "h-dot",
			URL: "https://" + urlsDotHost + ".:{tls}{c}",
		},
		URLCase{
			ID:  "h-dotpool",
			URL: "https://" + urlsHost + ".:{tls}{c}",
		},
		URLCase{
			ID:  "h-port443",
			URL: "https://" + urlsHost + ":443{c}",
		},
		URLCase{
			ID:  "h-port0",
			URL: "https://" + urlsHost + ":0{tls}{c}",
		},
		URLCase{
			ID:  "h-badpuny",
			URL: "https://" + urlsBadHost + ":{tls}{c}",
		},
		URLCase{
			ID:  "ip-dotted",
			URL: "http://127.0.0.1:{plain}{c}",
		},
		URLCase{
			ID:  "ip-short",
			URL: "http://127.1:{plain}{c}",
		},
		URLCase{
			ID:  "ip-hex",
			URL: "http://0x7f.1:{plain}{c}",
		},
		URLCase{
			ID:  "ip-octal",
			URL: "http://0177.0.0.1:{plain}{c}",
		},
		URLCase{
			ID:  "ip-int",
			URL: "http://2130706433:{plain}{c}",
		},
		URLCase{
			ID:  "ip-dot",
			URL: "http://127.0.0.1.:{plain}{c}",
		},
	)
}

// urlsPlainPath is the page's second half, at http:// on the plain port:
// the plain cases, so a page at https:// would not make them mixed
// content (Safari blocks even loopback).
const urlsPlainPath = "/plain"

// urlsReportPath is where each half of the page reports the cases whose
// fetch() failed or navigation did not load (a hint: the wire is the
// evidence) and the ports it ran on.
const urlsReportPath = "/report"

type urlsPage struct {
	fixedHosts
}

// certNames answers a ClientHello without a server name, which Safari
// sends for a host ending in a dot, with a certificate naming the hosts
// the page names that way, so its request arrives.
func (p urlsPage) certNames(host string) ([]string, bool) {
	if host == "" {
		return []string{
			urlsHost,
			urlsDotHost,
		}, true
	}

	return p.fixedHosts.certNames(host)
}

func (p urlsPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	path, _, _ := strings.Cut(req.path, "?")

	if host == urlsHost && (path == "/" && port == req.ports.tls || path == urlsPlainPath && port == req.ports.plain) {
		return response{
			status:      200,
			contentType: "text/html; charset=utf-8",
			body:        htmlPage("urls", "", urlsScript(req.ports, path == urlsPlainPath)),
		}
	}

	// Every case, /report and /done: a page, so a navigation loads.
	return response{
		status:      200,
		contentType: "text/html; charset=utf-8",
		body:        []byte("<!doctype html>ok\n"),
	}
}

// urlsCase is a case as the page's script gets it.
type urlsCase struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	Nav bool   `json:"nav"`
}

// urlsScript fetches each case of its half (plain: the http:// ones),
// then loads each in an iframe, 5 s at most each, and reports. The first
// half then goes to the second, which asks for /done.
func urlsScript(ports portPair, plain bool) string {
	var cases []urlsCase

	for _, nav := range []bool{false, true} {
		kind := "f"
		if nav {
			kind = "n"
		}

		for _, c := range URLCases() {
			if strings.HasPrefix(c.URL, "http://") != plain {
				continue
			}

			cases = append(cases, urlsCase{
				ID:  kind + "-" + c.ID,
				URL: c.On(kind, ports.tls, ports.plain),
				Nav: nav,
			})
		}
	}

	next := `location.href = "http://` + urlsHost + ":" + ports.plain + urlsPlainPath + `";`
	if plain {
		next = `fetch("/done");`
	}

	return pageScript([]string{
		"const cases = " + string(mustJSON(cases)) + ";",
		`const failed = [];
for (const c of cases) {
  let ok;
  if (c.nav) {
    ok = await within(new Promise((d, e) => {
      const f = document.createElement("iframe");
      f.onload = () => { f.remove(); d(true); };
      f.onerror = e;
      try { f.src = c.url; } catch (err) { e(err); return; }
      document.body.append(f);
    }), 5000);
  } else {
    ok = await within(fetch(c.url, {mode: "no-cors", credentials: "omit", cache: "no-store"}).then(() => true), 5000);
  }
  if (!ok) failed.push(c.id);
}`,
		`await within(fetch("` + urlsReportPath + `?" + new URLSearchParams({tls: "` + ports.tls + `", plain: "` + ports.plain + `", failed: failed.join(",")})), 5000);`,
		next,
	})
}
