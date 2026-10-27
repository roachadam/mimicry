package server

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// HeadersOrder is the headers-order scenario: where each browser puts the
// headers a page sets on fetch() and XHR (see HeadersSteps).
var HeadersOrder = &Scenario{
	Name: "headers-order",
	Doc:  "headers a page sets on fetch() and XHR: where each name goes among the browser's own, by name, case and the page's order; same-origin, cross-site, and over HTTP/1.1",
	multi: headersPage{
		fixedHosts: fixedHosts{
			names: []string{
				kindsHost,
				kindsCrossSite,
				resumeH1,
			},
			http1: []string{
				resumeH1,
			},
			runFor: 25 * time.Second,
		},
	},
}

// headersTargets are the hosts every set of headers is sent to, with the
// suffix their labels carry: the page's origin and another site over
// HTTP/2, and an HTTP/1.1-only origin of the page's site, the only place
// a header name's case shows.
var headersTargets = []struct {
	host, suffix string
}{
	{kindsHost, "so"},
	{kindsCrossSite, "cs"},
	{resumeH1, "h1"},
}

// headersAround are sorted names falling just before, between and after
// the headers browsers send themselves. sec-a is forbidden (sec- prefix),
// to show the browser dropping it.
var headersAround = []string{
	"0",
	"a",
	"accept-a",
	"accept-f",
	"accept-m",
	"b",
	"cache-controlz",
	"co",
	"connectionz",
	"content-m",
	"content-z",
	"cookiez",
	"h",
	"hostz",
	"o",
	"originz",
	"p",
	"pragmaz",
	"priorityz",
	"r",
	"refererz",
	"s",
	"sec",
	"sec-a",
	"secz",
	"t",
	"tez",
	"u",
	"user-agentz",
	"v",
	"x",
	"zzz",
}

// headersSet is a set of headers a page sets, by name.
type headersSet struct {
	name    string
	headers [][2]string
}

// headersSets are the header sets, each sent by fetch() and XHR to each
// target in this order, values "1" unless given.
func headersSets() []headersSet {
	ones := func(names []string) [][2]string {
		out := make([][2]string, 0, len(names))
		for _, n := range names {
			out = append(out, [2]string{n, "1"})
		}

		return out
	}
	var letters []string
	for c := 'a'; c <= 'z'; c++ {
		letters = append(letters, string(c))
	}

	return []headersSet{
		// Alone: where one name goes with nothing else set.
		{"single-a", ones([]string{"a"})},
		{"single-m", ones([]string{"m"})},
		{"single-zzz", ones([]string{"zzz"})},
		// The cors page's X-B, X-A, and the other way round: does the
		// page's order matter?
		{"pair-ab", ones([]string{"X-A", "X-B"})},
		{"pair-ba", ones([]string{"X-B", "X-A"})},
		// Every initial letter, in order and reversed.
		{"letters", ones(letters)},
		{"letters-rev", ones(reversed(letters))},
		// Names around each browser header, in order and reversed.
		{"around", ones(headersAround)},
		{"around-rev", ones(reversed(headersAround))},
		// Case spellings, and one name set twice in two spellings.
		{"case", [][2]string{
			{"X-Upper", "1"},
			{"x-lower", "1"},
			{"X-MiXeD", "1"},
			{"X-Dup", "1"},
			{"x-dup", "2"},
		}},
	}
}

// reversed is a reversed copy of s.
func reversed(s []string) []string {
	out := slices.Clone(s)
	slices.Reverse(out)

	return out
}

// HeadersStep is one request of the headers-order page: to /h/<label> on
// Host by API ("fetch" or "xhr"), with Headers in the page's order. fetch()
// uses default credentials, XHR none, so cross-site requests are preflighted.
type HeadersStep struct {
	Label   string      `json:"label"`
	API     string      `json:"api"`
	Host    string      `json:"-"`
	Headers [][2]string `json:"headers"`
	// URL is filled in when the page is rendered.
	URL string `json:"url"`
}

// HeadersSteps lists the headers-order page's requests: per target, each
// headersSets entry by fetch() then XHR, labelled <set>.<api>.<target>. See
// harness/reports/headers-order-2026-10-24.md.
func HeadersSteps() []HeadersStep {
	steps := make([]HeadersStep, 0, len(headersTargets)*len(headersSets())*2)
	for _, t := range headersTargets {
		for _, set := range headersSets() {
			for _, api := range []string{"fetch", "xhr"} {
				steps = append(steps, HeadersStep{
					Label:   set.name + "." + api + "." + t.suffix,
					API:     api,
					Host:    t.host,
					Headers: set.headers,
				})
			}
		}
	}

	return steps
}

type headersPage struct {
	fixedHosts
}

func (p headersPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	origin := originOn("https://", port)
	path, _, _ := strings.Cut(req.path, "?")
	if host == kindsHost && path == "/" {
		return response{status: 200, contentType: "text/html; charset=utf-8", body: headersDocument(origin)}
	}
	if path == "/done" {
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}
	label, ok := strings.CutPrefix(path, "/h/")
	if !ok || !slices.ContainsFunc(HeadersSteps(), func(s HeadersStep) bool {
		return s.Label == label && s.Host == host
	}) {
		return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
	}
	r := response{status: 200, contentType: "application/json", body: []byte("{}\n")}
	if req.origin != "" {
		r.headers = corsAllow(req.origin)
	}
	if req.method == "OPTIONS" {
		return response{status: 204, contentType: "text/plain", headers: preflightAllow(r.headers, "GET", req.preflightHeaders)}
	}

	return r
}

// headersDocument runs HeadersSteps one after another, 5 s at most each,
// then asks for /done.
func headersDocument(origin func(string) string) []byte {
	steps := HeadersSteps()
	for i := range steps {
		steps[i].URL = origin(steps[i].Host) + "/h/" + steps[i].Label
	}
	list, _ := json.Marshal(steps)

	return htmlPage("headers-order", "", "<script>(async () => {\nconst steps = "+string(list)+";\n"+headersScript+"\n})();</script>\n")
}

// headersScript sends each step: fetch() with a Headers object filled in
// the page's order, or XHR with setRequestHeader in that order.
const headersScript = `function run(s) {
  return new Promise(done => {
    const fin = () => done();
    setTimeout(fin, 5000);
    if (s.api === 'xhr') {
      const x = new XMLHttpRequest(); x.open('GET', s.url);
      for (const [n, v] of s.headers) { try { x.setRequestHeader(n, v); } catch (e) {} }
      x.onloadend = fin; x.send(); return;
    }
    const h = new Headers();
    for (const [n, v] of s.headers) { try { h.append(n, v); } catch (e) {} }
    fetch(s.url, {headers: h}).then(r => r.arrayBuffer()).then(fin, fin);
  });
}
for (const s of steps) await run(s);
fetch('/done');`
