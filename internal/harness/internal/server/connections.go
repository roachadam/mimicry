package server

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The connections page's hosts. The page's own and its same-site sibling
// each have a certificate naming both; the pair, two other sites, likewise;
// solo and the HTTP/1.1-only host have certificates naming only themselves.
// All resolve to 127.0.0.1 and are served on one port.
const (
	connHost     = "www.conns-a.test"
	connSameSite = "static.conns-a.test"
	connPairX    = "x.conns-b.test"
	connPairY    = "y.conns-c.test"
	connSolo     = "solo.conns-a.test"
	connH1       = "h1.conns-a.test"
)

// connCerts are the names on each host's certificate, the host first.
var connCerts = map[string][]string{
	connHost: {
		connHost,
		connSameSite,
	},
	connSameSite: {
		connSameSite,
		connHost,
	},
	connPairX: {
		connPairX,
		connPairY,
	},
	connPairY: {
		connPairY,
		connPairX,
	},
	connSolo: {
		connSolo,
	},
	connH1: {
		connH1,
	},
}

// connParallel is how many requests the page makes to the HTTP/1.1 host at
// once: more than any browser's per-host connection limit (Chrome 6).
const connParallel = 10

// connParallelDelay holds each HTTP/1.1 response back, so the parallel
// requests are all in flight together and the ones over the limit wait
// for a connection.
const connParallelDelay = time.Second

// Connections is the connections scenario: which requests share a
// connection, from one page (see ConnectionsSteps).
var Connections = &Scenario{
	Name: "connections",
	Doc:  "connections: HTTP/2 coalescing of hosts sharing a certificate and address, a host whose certificate names only itself, and 10 parallel requests to an HTTP/1.1-only host",
	multi: connectionsPage{
		fixedHosts: fixedHosts{
			names: []string{
				connHost,
				connSameSite,
				connPairX,
				connPairY,
				connSolo,
				connH1,
			},
			http1: []string{
				connH1,
			},
			runFor: 15 * time.Second,
		},
	},
}

// ConnectionsStep is one step of the connections page: requests to Host,
// one per label (path /c/<label>), made together as no-cors credentialed
// fetches; the page waits for them and 300 ms more before the next.
type ConnectionsStep struct {
	Host   string
	Labels []string
}

// ConnectionsSteps are the connections page's steps in order. What each
// answers is in internal/harness/reports/connections-2026-10-24.md.
func ConnectionsSteps() []ConnectionsStep {
	parallel := make([]string, 0, connParallel)
	for i := 1; i <= connParallel; i++ {
		parallel = append(parallel, fmt.Sprintf("par%02d", i))
	}

	return []ConnectionsStep{
		{Host: connSameSite, Labels: []string{"coalesce.ss"}},
		{Host: connPairX, Labels: []string{"pair.x"}},
		{Host: connPairY, Labels: []string{"pair.y"}},
		{Host: connSolo, Labels: []string{"solo"}},
		{Host: connH1, Labels: parallel},
	}
}

type connectionsPage struct {
	fixedHosts
}

// certNames gives each host its certificate from connCerts.
func (p connectionsPage) certNames(host string) ([]string, bool) {
	names, ok := connCerts[host]
	return slices.Clone(names), ok
}

func (p connectionsPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	origin := originOn("https://", port)
	path, _, _ := strings.Cut(req.path, "?")

	switch {
	case host == connHost && path == "/":
		return response{status: 200, contentType: "text/html; charset=utf-8", body: connectionsDocument(origin)}
	case path == "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}

	label, ok := strings.CutPrefix(path, "/c/")
	if !ok {
		return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
	}

	for _, s := range ConnectionsSteps() {
		if s.Host != host || !slices.Contains(s.Labels, label) {
			continue
		}

		r := response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
		if host == connH1 {
			r.delay = connParallelDelay
		}

		return r
	}

	return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
}

// connectionsDocument runs ConnectionsSteps one after another, each
// request given 5 s at most, then asks for /done.
func connectionsDocument(origin func(string) string) []byte {
	steps := make([]string, 0, len(ConnectionsSteps()))
	for _, s := range ConnectionsSteps() {
		urls := make([]string, 0, len(s.Labels))
		for _, l := range s.Labels {
			urls = append(urls, strconv.Quote(origin(s.Host)+"/c/"+l))
		}

		steps = append(steps, "["+strings.Join(urls, ", ")+"]")
	}

	return htmlPage("connections", "", "<script>(async () => {\n"+
		"const get = u => Promise.race([fetch(u, {mode: \"no-cors\", credentials: \"include\"}).then(r => r.arrayBuffer()).catch(() => {}), new Promise(d => setTimeout(d, 5000))]);\n"+
		"const pause = () => new Promise(d => setTimeout(d, 300));\n"+
		"for (const step of ["+strings.Join(steps, ", ")+"]) { await pause(); await Promise.all(step.map(get)); }\n"+
		"fetch(\"/done\");\n})();</script>\n")
}
