package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// The headers-order page sends every header set by fetch() and XHR to each
// target, in the page's order; cross-origin requests are allowed with
// whatever headers their preflight asks for; the HTTP/1.1 target offers
// only HTTP/1.1.
func TestHeadersOrder(t *testing.T) {
	client, port, _ := replayServer(t, HeadersOrder)
	url := func(host, path string) string {
		return "https://" + host + ":" + port + path
	}
	_, html := fetch(t, client, "GET", url(kindsHost, "/"), nil)
	_, rest, _ := strings.Cut(html, "const steps = ")
	list, _, _ := strings.Cut(rest, ";\n")
	var steps []HeadersStep
	if err := json.Unmarshal([]byte(list), &steps); err != nil {
		t.Fatalf("steps in document: %v", err)
	}
	want := HeadersSteps()
	if len(want) != len(headersTargets)*len(headersSets())*2 {
		t.Errorf("%d steps, want every set by fetch and XHR to every target", len(want))
	}
	if len(steps) != len(want) {
		t.Fatalf("document schedules %d steps, want %d", len(steps), len(want))
	}
	labels := map[string]bool{}
	for i, s := range steps {
		w := want[i]
		if s.Label != w.Label || s.API != w.API || !slices.Equal(s.Headers, w.Headers) || s.URL != url(w.Host, "/h/"+w.Label) {
			t.Errorf("step %d: %+v, want %+v at %s", i, s, w, url(w.Host, "/h/"+w.Label))
		}
		if labels[s.Label] {
			t.Errorf("label %s twice", s.Label)
		}
		labels[s.Label] = true
	}
	if !strings.Contains(html, "fetch('/done')") {
		t.Error("document does not end with /done")
	}
	for _, set := range headersSets() {
		if set.name == "letters-rev" && (set.headers[0][0] != "z" || set.headers[25][0] != "a") {
			t.Errorf("letters-rev %q", set.headers)
		}
	}

	page := url(kindsHost, "")
	resp, _ := fetch(t, client, "OPTIONS", url(kindsCrossSite, "/h/case.fetch.cs"), http.Header{
		"Origin":                         {page},
		"Access-Control-Request-Method":  {"GET"},
		"Access-Control-Request-Headers": {"x-dup,x-lower,x-mixed,x-upper"},
	})
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != page || resp.Header.Get("Access-Control-Allow-Headers") != "x-dup,x-lower,x-mixed,x-upper" {
		t.Errorf("preflight: %s %v", resp.Status, resp.Header)
	}
	resp, _ = fetch(t, client, "GET", url(resumeH1, "/h/around.xhr.h1"), http.Header{
		"Origin": {page},
	})
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != page || resp.TLS.NegotiatedProtocol != "http/1.1" {
		t.Errorf("HTTP/1.1 target: %s over %s, %v", resp.Status, resp.TLS.NegotiatedProtocol, resp.Header)
	}
	resp, _ = fetch(t, client, "GET", url(kindsHost, "/h/single-a.fetch.so"), nil)
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("same-origin: %s %v", resp.Status, resp.Header)
	}
	if resp, _ := fetch(t, client, "GET", url(kindsHost, "/h/single-a.fetch.cs"), nil); resp.StatusCode != 404 {
		t.Errorf("another target's label: %s", resp.Status)
	}
}
