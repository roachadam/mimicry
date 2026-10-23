package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/pageshape"
)

// replayPage is a small page with each case replay handles: a redirect
// before the document, a cross-site iframe whose own requests (a preflighted
// API call setting a host-only cookie) run in the frame, a script from
// another host of the site, an HTTP/1.1-only beacon host, and a service
// worker replay skips.
func replayPage() *pageshape.Page {
	return &pageshape.Page{
		Site: "ex.com",
		Hosts: []pageshape.Host{
			{
				Name: "www.ex.com",
				Site: "ex.com",
			},
			{
				Name: "cdn.ex.com",
				Site: "ex.com",
			},
			{
				Name: "frame.other.org",
				Site: "other.org",
			},
			{
				Name: "beacon.track.net",
				Site: "track.net",
			},
			{
				Name: "ex.com",
				Site: "ex.com",
			},
		},
		Requests: []pageshape.Request{
			{Host: 4, Method: "GET", Dest: "document", Mode: "navigate", Protocol: "h2", PathLen: 1, Status: 301, RedirectTo: 0, RedirectFrom: -1, Initiator: -1},
			{Host: 0, At: 10, Method: "GET", Dest: "document", Mode: "navigate", Protocol: "h2", PathLen: 1, Status: 200, RedirectTo: -1, RedirectFrom: 0, Initiator: -1,
				ContentType: "text/html", Size: 5000, SetCookies: []pageshape.Cookie{
					{ID: 1, NameLen: 3, ValueLen: 32, Class: "hex", SiteWide: true, HTTPOnly: true, Persistent: true},
				}},
			{Host: 1, At: 50, Method: "GET", Dest: "script", Mode: "no-cors", Protocol: "h2", PathLen: 20, Status: 200, RedirectTo: -1, RedirectFrom: -1, Initiator: 0,
				ContentType: "text/javascript", Size: 300},
			{Host: 2, At: 60, Method: "GET", Dest: "iframe", Mode: "navigate", Protocol: "h2", PathLen: 6, Status: 200, RedirectTo: -1, RedirectFrom: -1, Initiator: 0,
				ContentType: "text/html", Size: 2000},
			{Host: 2, At: 99, Method: "OPTIONS", Dest: "empty", Mode: "cors", Protocol: "h2", PathLen: 7, Status: 204, RedirectTo: -1, RedirectFrom: -1, Initiator: 2},
			{Host: 2, At: 100, Method: "POST", Dest: "empty", Mode: "cors", Protocol: "h2", PathLen: 7, QueryLen: 5, Body: 10, Status: 200, RedirectTo: -1, RedirectFrom: -1, Initiator: 2,
				Headers: []pageshape.Field{
					{Name: "x-trace-id", Len: 8, Class: "hex"},
					{Name: "content-type", Len: 16, Class: "base64", Value: "application/json"},
				},
				ContentType: "application/json", Size: 40, SetCookies: []pageshape.Cookie{
					{ID: 2, NameLen: 5, ValueLen: 10, Class: "alnum", SameSite: "none"},
				}},
			{Host: 3, At: 120, Method: "POST", Dest: "empty", Mode: "no-cors", Protocol: "h1", PathLen: 2, Body: 4, Status: 204, RedirectTo: -1, RedirectFrom: -1, Initiator: 0},
			{Host: 0, At: 130, Method: "GET", Dest: "serviceworker", Mode: "same-origin", Protocol: "h2", PathLen: 6, Status: 200, RedirectTo: -1, RedirectFrom: -1, Initiator: 0},
			// A frame the cross-site frame opens on its own host: a
			// document of that host too, but scheduled by its parent.
			{Host: 2, At: 140, Method: "GET", Dest: "iframe", Mode: "navigate", Protocol: "h2", PathLen: 3, Status: 200, RedirectTo: -1, RedirectFrom: -1, Initiator: 2,
				ContentType: "text/html", Size: 500},
		},
	}
}

// replayServer serves sc and returns a client that reaches every
// *.test name on it, the server's port, and the capture channel.
func replayServer(t *testing.T, sc *Scenario) (*http.Client, string, <-chan *capture.Capture) {
	t.Helper()
	ca, err := LoadOrCreateCA(t.TempDir(), CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	captures := make(chan *capture.Capture, 32)
	ctx, cancel := context.WithCancel(t.Context())
	srv := &Server{
		Scenario:  sc,
		TLSConfig: &tls.Config{},
		IssueCert: func(hosts []string) (tls.Certificate, error) {
			return ca.Issue(hosts, time.Now())
		},
		OnCapture: func(c *capture.Capture) {
			captures <- c
		},
		Logf: t.Logf,
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: pool,
		},
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
		},
	}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return client, port, captures
}

func fetch(t *testing.T, client *http.Client, method, url string, header http.Header) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(body)
}

// jobsOf returns the jobs a generated document schedules.
func jobsOf(t *testing.T, html string) []job {
	t.Helper()
	_, rest, ok := strings.Cut(html, "const jobs = ")
	list, _, ok2 := strings.Cut(rest, ", top = ")
	if !ok || !ok2 {
		t.Fatalf("no jobs in document:\n%.300s", html)
	}
	var jobs []job
	if err := json.Unmarshal([]byte(list), &jobs); err != nil {
		t.Fatal(err)
	}
	return jobs
}

func jobURLs(jobs []job) []string {
	urls := make([]string, 0, len(jobs))
	for _, j := range jobs {
		urls = append(urls, j.Dest+" "+j.URL)
	}
	return urls
}

func TestReplay(t *testing.T) {
	sc, err := Replay(replayPage())
	if err != nil {
		t.Fatal(err)
	}
	if hosts, _, ok := sc.MultiHost(); !ok || hosts[0] != "ex-com.test" || len(hosts) != 5 {
		t.Fatalf("hosts %v, want ex-com.test first of 5", hosts)
	}
	client, port, captures := replayServer(t, sc)
	base := func(host string) string {
		return "https://" + host + ".test:" + port
	}

	// The navigation redirects to the document, which schedules the
	// top frame's requests at their times; the frame's own requests are
	// the frame's, preflights and the service worker are not scheduled.
	resp, _ := fetch(t, client, "GET", base("ex-com")+"/", nil)
	doc := base("www.ex-com") + "/r1"
	if resp.StatusCode != 301 || resp.Header.Get("Location") != doc {
		t.Fatalf("navigation: %s to %q, want 301 to %s", resp.Status, resp.Header.Get("Location"), doc)
	}
	resp, html := fetch(t, client, "GET", doc, nil)
	if resp.ProtoMajor != 2 || len(html) != 5000 {
		t.Errorf("document over %s, %d bytes; want HTTP/2, the recorded 5000", resp.Proto, len(html))
	}
	if got := resp.Header.Values("Set-Cookie"); len(got) != 1 || !strings.HasPrefix(got[0], "c1_=") ||
		!strings.HasSuffix(got[0], "; Path=/; Secure; Domain=ex-com.test; HttpOnly; Max-Age=600") || len(strings.SplitN(got[0], ";", 2)[0]) != 3+1+32 {
		t.Errorf("document cookies %q", got)
	}
	top := jobsOf(t, html)
	wantTop := []string{
		"script " + base("cdn.ex-com") + "/r2/" + synthetic("alnum", 20-4, "path 2"),
		"iframe " + base("frame.other-org") + "/r3/" + synthetic("alnum", 2, "path 3"),
		"empty " + base("beacon.track-net") + "/r6",
	}
	if got := jobURLs(top); !slices.Equal(got, wantTop) {
		t.Errorf("top document jobs\n got %v\nwant %v", got, wantTop)
	}
	if !strings.Contains(html, `"https://frame.other-org.test:`+port+`/cleanup","https://www.ex-com.test:`+port+`/cleanup"`) {
		t.Errorf("top document does not clean up both cookie hosts")
	}

	_, frame := fetch(t, client, "GET", wantTop[1][len("iframe "):], nil)
	api := jobsOf(t, frame)
	if len(api) != 2 || api[0].Method != "POST" || api[0].Mode != "cors" || api[0].Body != 10 || api[0].At != 40 ||
		api[0].Headers["content-type"] != "application/json" || len(api[0].Headers["x-trace-id"]) != 8 {
		t.Fatalf("frame jobs %+v", api)
	}
	if nested := api[1]; nested.Dest != "iframe" || nested.URL != base("frame.other-org")+"/r8" || nested.At != 80 {
		t.Errorf("nested frame not scheduled by its parent: %+v", nested)
	}

	// CORS: the preflight allows what it was asked for, the request's
	// response its origin with credentials.
	origin := http.Header{
		"Origin":                         {base("frame.other-org")},
		"Access-Control-Request-Headers": {"content-type,x-trace-id"},
	}
	resp, _ = fetch(t, client, "OPTIONS", api[0].URL, origin)
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Headers") != "content-type,x-trace-id" ||
		resp.Header.Get("Access-Control-Allow-Origin") != base("frame.other-org") {
		t.Errorf("preflight: %s %v", resp.Status, resp.Header)
	}
	resp, _ = fetch(t, client, "POST", api[0].URL, origin)
	if resp.Header.Get("Access-Control-Allow-Credentials") != "true" || !strings.HasSuffix(resp.Header.Get("Set-Cookie"), "; Path=/; Secure; SameSite=none") {
		t.Errorf("API call: %v", resp.Header)
	}

	// The HTTP/1.1-only host is offered nothing else.
	resp, _ = fetch(t, client, "POST", wantTop[2][len("empty "):], nil)
	if resp.ProtoMajor != 1 || resp.StatusCode != 204 {
		t.Errorf("beacon: %s over %s, want 204 over HTTP/1.1", resp.Status, resp.Proto)
	}

	// Cleanup deletes what the host set, host-only cookies without Domain.
	resp, _ = fetch(t, client, "GET", base("frame.other-org")+"/cleanup", nil)
	want2 := cookieName(pageshape.Cookie{
		ID:      2,
		NameLen: 5,
	}) + "=; Path=/; Secure; Max-Age=0"
	if got := resp.Header.Values("Set-Cookie"); !slices.Equal(got, []string{want2}) || len(want2) != len("c2_xx=; Path=/; Secure; Max-Age=0") {
		t.Errorf("cleanup %q", got)
	}

	// /done hands over the whole page: every connection, every host.
	fetch(t, client, "GET", base("www.ex-com")+"/done", nil)
	var page *capture.Page
	for page == nil {
		c := receive(t, captures)
		if c.Finished {
			page = c.Page
		}
	}
	hosts := make([]string, 0, len(page.H2))
	for _, tr := range page.H2 {
		hosts = append(hosts, tr.Host)
	}
	slices.Sort(hosts)
	want := []string{"ex-com.test", "frame.other-org.test", "www.ex-com.test"}
	if !slices.Equal(hosts, want) {
		t.Errorf("page traces from %v, want %v", hosts, want)
	}
	if len(page.H1) != 1 || page.H1[0].Host != "beacon.track-net.test" {
		t.Errorf("page HTTP/1.1 requests %+v", page.H1)
	}
}
