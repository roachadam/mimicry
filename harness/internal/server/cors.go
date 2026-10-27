package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"
)

// CORS is the cors scenario: CORS preflight behaviour the kinds page does
// not show (header form, methods, credentials, the preflight cache, refused
// preflights, redirects). See CORSSteps and
// harness/reports/cors-2026-10-24.md.
var CORS = &Scenario{
	Name: "cors",
	Doc:  "CORS preflights: Access-Control-Request-Headers' form, methods, credentials, the preflight cache, refused preflights, redirects",
	multi: corsPage{
		fixedHosts: fixedHosts{
			names: []string{
				kindsHost,
				kindsSameSite,
				kindsCrossSite,
			},
			runFor: 30 * time.Second,
		},
	},
}

type corsPage struct {
	fixedHosts
}

// CORSStep is one fetch() of the cors page, to the cross-site host, in
// CORS mode.
type CORSStep struct {
	// Label is the path's last element: /p/<label>.
	Label string
	// Method is the fetch's method; "" for GET.
	Method string
	// Headers are the page's own, in the page's order: name, value.
	Headers [][2]string
	// Include is credentials: "include".
	Include bool
	// Wait is how long the page waits before the step.
	Wait time.Duration
	// Refused: the preflight's answer does not allow the request.
	Refused bool
}

// CORSSteps returns the cors page's fetches, in order (see CORS).
func CORSSteps() []CORSStep {
	custom := [][2]string{
		{"X-Mimicry", "1"},
	}
	return []CORSStep{
		{Label: "multi.cs", Headers: [][2]string{{"X-B", "1"}, {"X-A", "1"}}},
		{Label: "put.cs", Method: "PUT"},
		{Label: "setcookie.cs", Include: true},
		{Label: "include.cs", Include: true, Headers: custom},
		{Label: "maxage.cs", Headers: custom},
		{Label: "maxage.cs", Headers: custom},
		{Label: "maxage.cs", Headers: [][2]string{{"X-Other", "1"}}},
		{Label: "maxage.cs", Headers: custom},
		{Label: "maxage.cs", Method: "PUT", Headers: custom},
		{Label: "default.cs", Headers: custom},
		{Label: "default.cs", Headers: custom},
		{Label: "default.cs", Headers: custom, Wait: 6 * time.Second},
		{Label: "denied-headers.cs", Headers: custom, Refused: true},
		{Label: "denied-status.cs", Headers: custom, Refused: true},
		{Label: "redirect.cs", Headers: custom},
		{Label: "cleanup.cs", Include: true},
	}
}

// corsScript runs the steps against origin cs; refused preflights' failures
// are ignored.
func corsScript(cs string) string {
	var b strings.Builder
	b.WriteString("(async () => {\nconst f = (path, init) => fetch(")
	b.WriteString(strconv.Quote(cs))
	b.WriteString(" + path, init).then(r => r.text()).catch(() => {});\n")
	for _, st := range CORSSteps() {
		if st.Wait > 0 {
			fmt.Fprintf(&b, "await new Promise(r => setTimeout(r, %d));\n", st.Wait.Milliseconds())
		}
		var init []string
		if st.Method != "" {
			init = append(init, "method: "+strconv.Quote(st.Method))
		}
		if st.Include {
			init = append(init, `credentials: "include"`)
		}
		if len(st.Headers) > 0 {
			hs := make([]string, 0, len(st.Headers))
			for _, h := range st.Headers {
				hs = append(hs, strconv.Quote(h[0])+": "+strconv.Quote(h[1]))
			}
			init = append(init, "headers: {"+strings.Join(hs, ", ")+"}")
		}
		fmt.Fprintf(&b, "await f(%q, {%s});\n", "/p/"+st.Label, strings.Join(init, ", "))
	}
	b.WriteString("fetch(\"/done\");\n})();")
	return b.String()
}

func (p corsPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	origin := originOn("https://", port)
	path, _, _ := strings.Cut(req.path, "?")
	if host == kindsHost && path == "/" {
		script := corsScript(origin(kindsCrossSite))
		return response{status: 200, contentType: "text/html; charset=utf-8", body: htmlPage("cors", "", "<script>"+script+"</script>\n")}
	}
	if path == "/done" {
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}
	label, ok := strings.CutPrefix(path, "/p/")
	if !ok {
		return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
	}
	allow := corsAllow(req.origin)
	if req.method == "OPTIONS" {
		requested := req.preflightHeaders
		if label == "denied-headers.cs" {
			requested = ""
		}
		allow := preflightAllow(allow, "GET, POST, PUT", requested)
		status := 204
		switch label {
		case "maxage.cs":
			allow = append(allow, hpack.HeaderField{
				Name:  "access-control-max-age",
				Value: "600",
			})
		case "denied-status.cs":
			status = 403
		}
		return response{status: status, contentType: "text/plain", headers: allow}
	}
	r := response{status: 200, contentType: "application/json", body: []byte("{}\n"), headers: allow}
	switch label {
	case "setcookie.cs":
		r.headers = append(r.headers, hpack.HeaderField{
			Name:  "set-cookie",
			Value: "pc=1; Path=/; Secure; SameSite=None; Partitioned",
		})
	case "cleanup.cs":
		r.headers = append(r.headers, hpack.HeaderField{
			Name:  "set-cookie",
			Value: "pc=; Path=/; Secure; SameSite=None; Partitioned; Max-Age=0",
		})
	case "redirect.cs":
		r = response{status: 307, contentType: "text/plain", headers: append(allow, hpack.HeaderField{
			Name:  "location",
			Value: origin(kindsCrossSite) + "/p/redirect-target.cs",
		})}
	}
	return r
}
