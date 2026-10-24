package server

import (
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// resumeH1 is the resume page's HTTP/1.1-only host.
const resumeH1 = "h1.kinds-a.test"

// ResumeSteps are the resume page's requests in order, by label (path
// /k/<label>); the suffix names the host: so the page's own, ss its
// same-site sibling, h1 the HTTP/1.1-only host. Each sequential step's
// response closes its connection, so the next starts a new one while the
// browser holds a ticket from the last; the p steps go out together.
var ResumeSteps = []string{
	"r1.so", "r2.so", "r3.so",
	"r1.ss", "r2.ss",
	"r1.h1", "r2.h1",
	"p1.h1|p2.h1|p3.h1",
}

// Resume is the resume scenario: TLS session resumption, as each browser
// does it on new connections to a host it holds a ticket for, over HTTP/2
// and HTTP/1.1, sequential and parallel.
var Resume = &Scenario{
	Name:  "resume",
	Doc:   "TLS session resumption: new connections to hosts the browser holds tickets for, sequential and parallel, HTTP/2 and HTTP/1.1",
	multi: resumePage{},
}

// ResumeTLS12 is the resume page from a server that speaks TLS 1.2 at
// most: resumption by session ticket instead of pre_shared_key.
var ResumeTLS12 = &Scenario{
	Name:       "resume-tls12",
	Doc:        "TLS 1.2 session resumption: the resume page from a TLS 1.2-only server (session tickets)",
	multi:      resumePage{},
	maxVersion: tls.VersionTLS12,
}

type resumePage struct{}

func (resumePage) hosts() []string {
	return []string{
		kindsHost,
		kindsSameSite,
		resumeH1,
	}
}

func (resumePage) certNames(host string) ([]string, bool) {
	switch host {
	case kindsHost, kindsSameSite, resumeH1:
		return []string{
			host,
		}, true
	}
	return nil, false
}

func (resumePage) h1Only(host string) bool {
	return host == resumeH1
}

func (resumePage) length() time.Duration {
	return 20 * time.Second
}

func (p resumePage) answer(req request) response {
	host, port, err := net.SplitHostPort(req.host)
	if err != nil {
		host, port = req.host, "443"
	}
	origin := func(h string) string {
		return "https://" + net.JoinHostPort(h, port)
	}
	var cors []hpack.HeaderField
	if req.origin != "" {
		cors = []hpack.HeaderField{
			{Name: "access-control-allow-origin", Value: req.origin},
			{Name: "access-control-allow-credentials", Value: "true"},
		}
	}
	path, _, _ := strings.Cut(req.path, "?")
	switch {
	case host == kindsHost && path == "/":
		return response{status: 200, contentType: "text/html; charset=utf-8", body: p.document(origin), closeAfter: true}
	case path == "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}
	label, ok := strings.CutPrefix(path, "/k/")
	if !ok {
		return response{status: 404, contentType: "text/plain", body: []byte("not found\n"), headers: cors}
	}
	r := response{status: 200, contentType: "text/plain", body: []byte("ok\n"), headers: cors}
	if strings.HasPrefix(label, "p") {
		r.delay = 500 * time.Millisecond
	} else {
		r.closeAfter = true
	}
	return r
}

// document runs ResumeSteps one after another, 300 ms apart, so each
// connection's tickets have arrived before the next starts.
func (resumePage) document(origin func(string) string) []byte {
	hostOf := map[string]string{
		"so": kindsHost,
		"ss": kindsSameSite,
		"h1": resumeH1,
	}
	steps := make([]string, 0, len(ResumeSteps))
	for _, step := range ResumeSteps {
		var urls []string
		for label := range strings.SplitSeq(step, "|") {
			_, suffix, _ := strings.Cut(label, ".")
			urls = append(urls, strconv.Quote(origin(hostOf[suffix])+"/k/"+label))
		}
		steps = append(steps, "["+strings.Join(urls, ", ")+"]")
	}
	return []byte("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>mimicry resume</title><link rel=\"icon\" href=\"data:,\"></head><body>\n<script>(async () => {\n" +
		"const get = u => fetch(u, {credentials: \"include\"}).then(r => r.arrayBuffer()).catch(() => {});\n" +
		"const pause = () => new Promise(d => setTimeout(d, 300));\n" +
		"for (const step of [" + strings.Join(steps, ", ") + "]) { await pause(); await Promise.all(step.map(get)); }\n" +
		"fetch(\"/done\");\n})();</script>\n</body></html>\n")
}
