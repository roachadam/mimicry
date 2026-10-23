package pageshape

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// netLogBuilder writes a net log the way Chrome does: constants, then one
// event per line.
type netLogBuilder struct {
	types  map[string]int
	events []string
	time   int
}

func newNetLog() *netLogBuilder {
	return &netLogBuilder{
		types: map[string]int{},
	}
}

func (l *netLogBuilder) event(typ string, source int, params map[string]any) {
	if _, ok := l.types[typ]; !ok {
		l.types[typ] = len(l.types) + 1
	}
	l.time++
	e := map[string]any{
		"time":   fmt.Sprint(1000 + l.time),
		"type":   l.types[typ],
		"phase":  1,
		"source": map[string]any{"id": source, "type": 1},
	}
	if params != nil {
		e["params"] = params
	}
	data, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	l.events = append(l.events, string(data))
}

func (l *netLogBuilder) bytes() []byte {
	constants, err := json.Marshal(map[string]any{
		"logEventTypes": l.types,
	})
	if err != nil {
		panic(err)
	}
	return []byte(`{"constants": ` + string(constants) + ",\n\"events\": [\n" + strings.Join(l.events, ",\n") + "]}")
}

// request adds a page request on source id with its request and response
// headers; initiator "" means the page's own origin.
func (l *netLogBuilder) request(id int, top, initiator, kind, method, rawURL, sendEvent string, sent, received []string, bytesRead int) {
	if initiator == "" {
		initiator = "https://example.com"
	}
	l.event("URL_REQUEST_START_JOB", id, map[string]any{
		"url": rawURL, "method": method, "initiator": initiator, "request_type": kind,
		"network_isolation_key": top + " " + top,
	})
	l.event(sendEvent, id, map[string]any{"headers": sent})
	l.event("HTTP_TRANSACTION_READ_RESPONSE_HEADERS", id, map[string]any{"headers": received})
	l.event("URL_REQUEST_JOB_FILTERED_BYTES_READ", id, map[string]any{"byte_count": bytesRead})
}

func TestFromNetLog(t *testing.T) {
	l := newNetLog()
	const h2 = "HTTP_TRANSACTION_HTTP2_SEND_REQUEST_HEADERS"
	l.event("HTTP2_SESSION", 100, map[string]any{"host": "www.example.com:443"})
	l.event("HTTP2_SESSION_SEND_HEADERS", 100, map[string]any{"headers": []string{":method: GET", ":authority: www.example.com", ":path: /"}})
	l.request(1, "https://example.com", "not an origin", "main frame", "GET", "https://www.example.com/", h2,
		[]string{":authority: www.example.com", ":path: /", "sec-fetch-dest: document", "sec-fetch-mode: navigate", "user-agent: x"},
		[]string{"HTTP/1.1 200", "content-type: text/html; charset=utf-8",
			"set-cookie: sid=0123abcd; Domain=.example.com; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=3600",
			"set-cookie: gone=x; Expires=Thu, 01 Jan 1970 00:00:00 GMT"},
		500)
	// A second host's request coalesced onto www's connection.
	l.event("HTTP2_SESSION_SEND_HEADERS", 100, map[string]any{"headers": []string{":method: POST", ":authority: api.example.com", ":path: /v1/x?a=1"}})
	l.request(2, "https://example.com", "", "other", "POST", "https://api.example.com/v1/x?a=1", h2,
		[]string{":authority: api.example.com", ":path: /v1/x?a=1", "sec-fetch-dest: empty", "sec-fetch-mode: cors",
			"content-length: 12", "x-trace-id: deadbeef", "content-type: application/json", "accept: application/json",
			"cookie: sid=0123abcd; _ga=GA1.2.345"},
		[]string{"HTTP/1.1 204"}, 0)
	// Chrome's own request for the page, and another tab's: both left out.
	l.request(3, "https://example.com", "not an origin", "other", "GET", "https://content-autofill.googleapis.com/q", h2, nil, []string{"HTTP/1.1 200"}, 10)
	l.request(4, "https://other.org", "", "other", "GET", "https://other.org/", h2, nil, []string{"HTTP/1.1 200"}, 10)
	// An HTTP/1.1 beacon, and a request that was never sent (cached).
	l.request(5, "https://example.com", "", "other", "POST", "https://beacon.example.net/b", "HTTP_TRANSACTION_SEND_REQUEST_HEADERS",
		[]string{"Host: beacon.example.net", "sec-fetch-dest: empty"}, []string{"HTTP/1.1 200"}, 2)
	l.event("URL_REQUEST_START_JOB", 6, map[string]any{
		"url": "https://www.example.com/cached.js", "method": "GET", "initiator": "https://example.com",
		"request_type": "other", "network_isolation_key": "https://example.com https://example.com",
	})
	// A redirect: the job restarts on the same request source.
	l.request(7, "https://example.com", "", "other", "GET", "https://example.com/old", "HTTP_TRANSACTION_SEND_REQUEST_HEADERS",
		[]string{"Host: example.com"}, []string{"HTTP/1.1 301"}, 0)
	l.event("URL_REQUEST_REDIRECTED", 7, map[string]any{"location": "/new"})
	l.request(7, "https://example.com", "", "other", "GET", "https://example.com/new", "HTTP_TRANSACTION_SEND_REQUEST_HEADERS",
		[]string{"Host: example.com"}, []string{"HTTP/1.1 200"}, 3)
	data := l.bytes()
	// Cut off mid-event, as a killed browser leaves it.
	data = append(data[:len(data)-len("]}")], ",\n{\"time\": \"99"...)

	p, err := FromNetLog(data, "example.com", "chrome-155-macos", "2026-10-23")
	if err != nil {
		t.Fatal(err)
	}
	hosts := make([]string, 0, len(p.Hosts))
	for _, h := range p.Hosts {
		hosts = append(hosts, h.Name)
	}
	if got := len(p.Requests); got != 5 {
		t.Fatalf("got %d requests, want 5 (navigation, API call, beacon, redirect and its target); hosts %v", got, hosts)
	}
	nav, api, beacon, moved, target := p.Requests[0], p.Requests[1], p.Requests[2], p.Requests[3], p.Requests[4]
	if moved.Status != 301 || hosts[moved.RedirectTo] != "example.com" || target.RedirectFrom != 3 || moved.RedirectFrom != -1 {
		t.Errorf("redirect %+v -> %+v", moved, target)
	}
	if nav.Initiator != -1 || hosts[api.Initiator] != "example.com" {
		t.Errorf("initiators: navigation %d, API call %d", nav.Initiator, api.Initiator)
	}
	if nav.Dest != "document" || nav.Protocol != "h2" || nav.Conn != 1 || nav.Status != 200 ||
		nav.ContentType != "text/html" || nav.Size != 500 || nav.At != 0 {
		t.Errorf("navigation %+v", nav)
	}
	wantSID := Cookie{
		ID:         1,
		NameLen:    3,
		ValueLen:   8,
		Class:      "hex",
		SiteWide:   true,
		Path:       "/",
		Secure:     true,
		HTTPOnly:   true,
		SameSite:   "lax",
		Persistent: true,
	}
	if len(nav.SetCookies) < 2 || nav.SetCookies[0] != wantSID || !nav.SetCookies[1].Delete {
		t.Errorf("navigation cookies %+v", nav.SetCookies)
	}
	// _ga was sent without a Set-Cookie: set by script, placed on the
	// site's last response before it was sent.
	if last := nav.SetCookies[len(nav.SetCookies)-1]; !last.Script || last.NameLen != 3 || last.ValueLen != 9 {
		t.Errorf("script cookie not placed on the navigation: %+v", nav.SetCookies)
	}
	if hosts[api.Host] != "api.example.com" || api.Body != 12 || api.QueryLen != 3 || api.PathLen != 5 ||
		api.CookieCrumbs != 2 || api.Conn != 0 || api.Mode != "cors" {
		t.Errorf("API request %+v", api)
	}
	wantHeaders := []Field{
		{
			Name:  "x-trace-id",
			Len:   8,
			Class: "hex",
		},
		{
			Name:  "content-type",
			Len:   16,
			Class: Classify("application/json"),
			Value: "application/json",
		},
		{
			Name:  "accept",
			Len:   16,
			Class: Classify("application/json"),
			Value: "application/json",
		},
	}
	if !slices.Equal(api.Headers, wantHeaders) {
		t.Errorf("page-set headers %+v, want %+v", api.Headers, wantHeaders)
	}
	if got := p.Hosts[nav.Host].Shared; !slices.Equal(got, []int{api.Host}) {
		t.Errorf("www shares its connection with %v, want the API host", got)
	}
	if beacon.Protocol != "h1" || p.Hosts[beacon.Host].Site != "example.net" {
		t.Errorf("beacon %+v on %+v", beacon, p.Hosts[beacon.Host])
	}
}

func TestClassify(t *testing.T) {
	for value, want := range map[string]string{
		"":             "empty",
		"12345":        "digits",
		"deadBEEF01":   "hex",
		"GA1x2y":       "alnum",
		"YWJj+/==":     "base64",
		"YWJj-_x":      "base64url",
		"a%20b":        "urlencoded",
		"GA1.2.345":    "text",
		`{"a":"b c"}`:  "text",
		"en-US,en;q=1": "text",
	} {
		if got := Classify(value); got != want {
			t.Errorf("Classify(%q) = %q, want %q", value, got, want)
		}
	}
}
