package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// CORSEdge is the cors-edge scenario: what each browser does with
// redirects of fetch(), from the kinds page's origin (see CORSEdgeSteps).
var CORSEdge = &Scenario{
	Name: "cors-edge",
	Doc:  "fetch() redirects: a CORS redirect without Access-Control-Allow-Origin, PUT, DELETE and POST answered 301, 302 and 303, and the redirect limit",
	multi: corsEdgePage{
		fixedHosts: fixedHosts{
			names: []string{
				kindsHost,
				kindsCrossSite,
			},
			runFor: 20 * time.Second,
		},
	},
}

// CORSEdgeStep is one fetch() of the cors-edge page to /p/<label> on Host,
// redirected (Status) through .hop1 … .hop<Hops>, the last answered 200.
type CORSEdgeStep struct {
	Label string
	Host  string
	// Method is the fetch's method; "" for GET.
	Method string
	// Headers are the page's own, in the page's order: name, value.
	Headers [][2]string
	// Body is the request body ("" for none); fetch() sends a string body
	// as text/plain;charset=UTF-8.
	Body string
	// Status is the redirects' status and Hops how many there are.
	Status int
	Hops   int
	// NoACAO leaves Access-Control-Allow-Origin (and -Credentials) off the
	// redirects; the preflight and the last hop still have them.
	NoACAO bool
}

// corsEdgeChain is how many redirects the chain step is answered with:
// more than any browser follows (the Fetch standard: 20).
const corsEdgeChain = 25

// CORSEdgeSteps returns the cors-edge page's fetches, in order: cross-site
// redirects with and without Access-Control-Allow-Origin, same-origin
// PUT/DELETE/POST redirected 301/302/303, and a corsEdgeChain-long chain.
// See harness/reports/cors-edge-2026-10-24.md.
func CORSEdgeSteps() []CORSEdgeStep {
	steps := []CORSEdgeStep{
		{Label: "acao-redirect.cs", Host: kindsCrossSite, Status: 302, Hops: 1},
		{Label: "noacao-redirect.cs", Host: kindsCrossSite, Status: 302, Hops: 1, NoACAO: true},
		{Label: "noacao-redirect-pf.cs", Host: kindsCrossSite, Headers: [][2]string{{"X-Mimicry", "1"}}, Status: 302, Hops: 1, NoACAO: true},
	}
	for _, method := range []string{"PUT", "DELETE", "POST"} {
		for _, status := range []int{301, 302, 303} {
			steps = append(steps, CORSEdgeStep{
				Label:  fmt.Sprintf("m-%s-%d.so", strings.ToLower(method), status),
				Host:   kindsHost,
				Method: method,
				Body:   "x",
				Status: status,
				Hops:   1,
			})
		}
	}

	return append(steps, CORSEdgeStep{
		Label:  "chain.so",
		Host:   kindsHost,
		Status: 302,
		Hops:   corsEdgeChain,
	})
}

type corsEdgePage struct {
	fixedHosts
}

func (p corsEdgePage) answer(req request) response {
	host, port := splitAuthority(req.host)
	origin := originOn("https://", port)
	path, _, _ := strings.Cut(req.path, "?")
	if host == kindsHost && path == "/" {
		return response{status: 200, contentType: "text/html; charset=utf-8", body: htmlPage("cors-edge", "", "<script>"+corsEdgeScript(origin)+"</script>\n")}
	}
	if path == "/done" {
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}
	notFound := response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
	target, ok := strings.CutPrefix(path, "/p/")
	if !ok {
		return notFound
	}
	label, hop := target, 0
	if l, n, ok := strings.Cut(target, ".hop"); ok {
		var err error
		if hop, err = strconv.Atoi(n); err != nil || hop < 1 {
			return notFound
		}
		label = l
	}
	var cors []hpack.HeaderField
	if req.origin != "" {
		cors = corsAllow(req.origin)
	}
	for _, s := range CORSEdgeSteps() {
		if s.Label != label || s.Host != host || hop > s.Hops {
			continue
		}
		if req.method == "OPTIONS" {
			return response{status: 204, contentType: "text/plain", headers: preflightAllow(cors, "GET, POST, PUT, DELETE", req.preflightHeaders)}
		}
		if hop == s.Hops {
			return response{status: 200, contentType: "application/json", body: []byte("{}\n"), headers: cors}
		}
		r := response{status: s.Status, contentType: "text/plain"}
		if !s.NoACAO {
			r.headers = cors
		}
		r.headers = append(r.headers, hpack.HeaderField{
			Name:  "location",
			Value: origin(host) + "/p/" + label + ".hop" + strconv.Itoa(hop+1),
		})

		return r
	}

	return notFound
}

// corsEdgeScript makes each step's fetch in turn, 5 s at most each;
// refused ones are ignored.
func corsEdgeScript(origin func(string) string) string {
	var b strings.Builder
	b.WriteString("(async () => {\nconst f = (u, init) => Promise.race([fetch(u, init).then(r => r.text()).catch(() => {}), new Promise(d => setTimeout(d, 5000))]);\n")
	for _, s := range CORSEdgeSteps() {
		var init []string
		if s.Method != "" {
			init = append(init, "method: "+strconv.Quote(s.Method))
		}
		if len(s.Headers) > 0 {
			hs := make([]string, 0, len(s.Headers))
			for _, h := range s.Headers {
				hs = append(hs, strconv.Quote(h[0])+": "+strconv.Quote(h[1]))
			}
			init = append(init, "headers: {"+strings.Join(hs, ", ")+"}")
		}
		if s.Body != "" {
			init = append(init, "body: "+strconv.Quote(s.Body))
		}
		fmt.Fprintf(&b, "await f(%s, {%s});\n", strconv.Quote(origin(s.Host)+"/p/"+s.Label), strings.Join(init, ", "))
	}
	b.WriteString("fetch(\"/done\");\n})();")

	return b.String()
}
