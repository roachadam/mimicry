package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/roachadam/mimicry/harness/internal/quictrace"
)

// The h3 pages' hosts: the page's own site and another site. Both
// advertise HTTP/3 on every response, over TCP and QUIC alike.
const (
	h3Host      = "www.h3-a.test"
	h3CrossSite = "www.h3-b.test"
)

// H3Discovery is the h3-discovery scenario: a page over HTTPS whose every
// response advertises HTTP/3 (Alt-Svc: h3=":<port>"; ma=86400), then
// requests spaced in time, a second page, another site, a GOAWAY and an
// idle spell: when each browser moves to HTTP/3, whether it races TCP and
// QUIC, and what it sends first. See H3DiscoverySteps. Its server allows
// no QPACK dynamic table (empty SETTINGS, as quic-go's HTTP/3 server).
var H3Discovery = &Scenario{
	Name: "h3-discovery",
	Doc:  "HTTP/3 discovery: Alt-Svc on every response, then subresources, fetches and a POST spaced in time, a second page, a cross-site host, a GOAWAY and a 35 s idle: when each browser switches to QUIC, whether it races TCP, its QUIC Initial, transport parameters, HTTP/3 SETTINGS and requests",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
				h3CrossSite,
			},
			runFor: 65 * time.Second,
		},
		steps: H3DiscoverySteps,
	},
	h3: &h3Server{},
}

// H3Page is the h3-page scenario: after a warm-up that learns both hosts'
// Alt-Svc, a navigation to a page making every request kind (see
// H3PageSteps), over HTTP/3 where the browser takes it: each kind's
// header list, priority (the priority header, PRIORITY_UPDATE) and QPACK
// encoding. Its server allows a 64 KiB QPACK dynamic table and 100
// blocked streams, the defaults of Google's QUIC server (quiche), so the
// browser's QPACK encoder may use it.
var H3Page = &Scenario{
	Name: "h3-page",
	Doc:  "request kinds over HTTP/3 (navigation, scripts, styles, images by priority, fetch GET/POST, XHR, beacon, iframe, font, cross-site): header lists, priorities and PRIORITY_UPDATE, QPACK with a 64 KiB dynamic table allowed",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
				h3CrossSite,
			},
			runFor: 30 * time.Second,
		},
		steps: H3PageSteps,
	},
	h3: &h3Server{
		settings: [][2]uint64{
			{quictrace.SettingQPACKMaxTableCapacity, 64 << 10},
			{quictrace.SettingQPACKBlockedStreams, 100},
		},
	},
}

// H3ProfileCaptures returns the paths of name's h3-discovery and h3-page
// captures in dir, unforced and with -h3-local-roots, those that exist,
// sorted: the captures a profile's QUIC and HTTP/3 sections are
// generated from, but for what only the edge scenarios show
// (H3EdgeCaptures).
func H3ProfileCaptures(dir, name string) ([]string, error) {
	return existingCaptures(dir, name, H3Discovery, H3Page)
}

// H3EdgeCaptures returns the paths of name's h3-blackhole, h3-broken,
// h3-vn and h3-download captures in dir, as H3ProfileCaptures does: the
// captures h3gen derives loss recovery, fallback, version negotiation,
// acknowledgement, credit and key updates from. h3-v2 is left out: its
// server is aioquic, whose upgrade only internal/quic's v2 support and
// harness/e2e check.
func H3EdgeCaptures(dir, name string) ([]string, error) {
	return existingCaptures(dir, name, H3Blackhole, H3Broken, H3VN, H3Download)
}

func existingCaptures(dir, name string, scenarios ...*Scenario) ([]string, error) {
	var out []string
	for _, sc := range scenarios {
		for _, cond := range []string{"", ".local-roots"} {
			path := filepath.Join(dir, name+"."+sc.Name+cond+".pagetrace.json.gz")
			_, err := os.Stat(path)
			switch {
			case err == nil:
				out = append(out, path)
			case !errors.Is(err, fs.ErrNotExist):
				return nil, err
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// H3Step is one request of the h3 pages: a KindsStep (Host's
// /h3/<Label>) made by the document at Page (a path), PauseMillis after
// its previous step completed (or, after a NoWait step, started). A step
// with a Tag is in the document's markup instead; a "navigate" step loads
// the document at its own URL (its steps are those whose Page is that
// path) and ends its page's steps; a page whose steps end otherwise asks
// for /done.
type H3Step struct {
	KindsStep
	Page        string `json:"-"`
	PauseMillis int    `json:"pause"`
	// NoWait starts the next step's pause without waiting for this one to
	// complete, so steps keep their schedule however long the browser
	// takes; its URL gets ?t=<performance.now() in ms>, when the script
	// made the request.
	NoWait bool `json:"nowait,omitempty"`
	// Delay holds the response back, so the browser can change the
	// request's priority while it is in flight; CloseAfter ends the
	// connection that carried it after answering (HTTP/3: GOAWAY, then
	// CONNECTION_CLOSE H3_NO_ERROR; HTTP/2: GOAWAY), GoAwayTCP only an
	// HTTP/2 one (over HTTP/3 it is answered as any other).
	Delay      time.Duration `json:"-"`
	CloseAfter bool          `json:"-"`
	GoAwayTCP  bool          `json:"-"`
	// Silence, the first time it arrives over HTTP/3, leaves the request
	// unanswered and drops every later datagram of its connection both
	// ways (repeated on another connection, it is answered); Blackhole,
	// with CloseAfter over HTTP/3, leaves every QUIC connection started
	// after the close unanswered.
	Silence   bool `json:"-"`
	Blackhole bool `json:"-"`
	// Download, when set, answers with that many incompressible bytes.
	Download int `json:"-"`
}

// Path is the step's path.
func (s H3Step) Path() string {
	return "/h3/" + s.Label
}

// h3Idle is the h3-discovery page's idle spell: longer than the idle
// timeout browsers announce (30 s), shorter than the server's.
const h3Idle = 35 * time.Second

// H3DiscoverySteps lists the h3-discovery page's requests in order. The
// page at / (over TCP: nothing has advertised HTTP/3 yet) loads a
// stylesheet, a script and an image from its markup, then with pauses: a
// fetch, an image, a script, a POST of 8 KiB, a fetch, then navigates to
// /h3/second.so, which loads an image from its markup, then: a fetch, a
// fetch and an image from the other site (its first request learns its
// Alt-Svc), a second fetch there, a fetch answered with a GOAWAY that
// ends its connection, a fetch after it, and after h3Idle a fetch to each
// site (the idle timeout has ended their QUIC connections).
func H3DiscoverySteps() []H3Step {
	const second = "/h3/second.so"
	upload := strings.Repeat("h3-discovery-", 8192/13+1)[:8192]
	return []H3Step{
		{KindsStep: KindsStep{Label: "markup-style.so", Kind: "style", Host: h3Host, Tag: `<link rel="stylesheet" href="%s">`}, Page: "/"},
		{KindsStep: KindsStep{Label: "markup-script.so", Kind: "script", Host: h3Host, Tag: `<script src="%s"></script>`}, Page: "/"},
		{KindsStep: KindsStep{Label: "markup-image.so", Kind: "image", Host: h3Host, Tag: `<img src="%s" alt="">`}, Page: "/"},
		{KindsStep: KindsStep{Label: "fetch1.so", Kind: "fetch", Host: h3Host}, Page: "/", PauseMillis: 1000},
		{KindsStep: KindsStep{Label: "image1.so", Kind: "image", Host: h3Host}, Page: "/", PauseMillis: 1000},
		{KindsStep: KindsStep{Label: "script1.so", Kind: "script", Host: h3Host}, Page: "/", PauseMillis: 1000},
		{KindsStep: KindsStep{Label: "post1.so", Kind: "fetch", Host: h3Host, Method: "POST", Body: upload}, Page: "/", PauseMillis: 1000},
		{KindsStep: KindsStep{Label: "fetch2.so", Kind: "fetch", Host: h3Host}, Page: "/", PauseMillis: 2000},
		{KindsStep: KindsStep{Label: "second.so", Kind: "navigate", Host: h3Host}, Page: "/", PauseMillis: 1000},
		{KindsStep: KindsStep{Label: "markup-image2.so", Kind: "image", Host: h3Host, Tag: `<img src="%s" alt="">`}, Page: second},
		{KindsStep: KindsStep{Label: "fetch3.so", Kind: "fetch", Host: h3Host}, Page: second, PauseMillis: 1000},
		{KindsStep: KindsStep{Label: "xfetch1.cs", Kind: "fetch", Host: h3CrossSite, Mode: "no-cors", Credentials: "include"}, Page: second, PauseMillis: 1000},
		{KindsStep: KindsStep{Label: "ximage1.cs", Kind: "image", Host: h3CrossSite}, Page: second, PauseMillis: 1000},
		{KindsStep: KindsStep{Label: "xfetch2.cs", Kind: "fetch", Host: h3CrossSite, Mode: "no-cors", Credentials: "include"}, Page: second, PauseMillis: 2000},
		{KindsStep: KindsStep{Label: "goaway.so", Kind: "fetch", Host: h3Host}, Page: second, PauseMillis: 1000, CloseAfter: true},
		{KindsStep: KindsStep{Label: "after-goaway.so", Kind: "fetch", Host: h3Host}, Page: second, PauseMillis: 2000},
		{KindsStep: KindsStep{Label: "after-idle.so", Kind: "fetch", Host: h3Host}, Page: second, PauseMillis: int(h3Idle / time.Millisecond)},
		{KindsStep: KindsStep{Label: "xafter-idle.cs", Kind: "fetch", Host: h3CrossSite, Mode: "no-cors", Credentials: "include"}, Page: second},
	}
}

// h3ImageDelay holds the h3-page's images back, so a browser that
// reprioritises an image once laid out (in or out of the viewport) does so
// while it is in flight.
const h3ImageDelay = time.Second

// H3PageSteps lists the h3-page's requests in order. The page at / makes a
// fetch to each host, so both hosts' Alt-Svc is known, then navigates to
// /h3/kinds.so. That page's markup loads a stylesheet, a blocking and an
// async script, an image in the viewport and one far below it; its script
// then makes every kind one after another: script, style, images at
// default, high and low priority, fetch at default, high and low priority,
// a JSON POST and a 64 KiB upload, XHR GET and POST, a beacon, an iframe, a
// font, and a fetch, an image and a script from the other site.
func H3PageSteps() []H3Step {
	const kinds = "/h3/kinds.so"
	upload := strings.Repeat("h3-page-upload-", 65536/15+1)[:65536]
	// scripted are the kinds page's script steps to host, 300 ms apart.
	scripted := func(host string, kindsSteps []KindsStep) []H3Step {
		out := make([]H3Step, len(kindsSteps))
		for i, s := range kindsSteps {
			s.Host = host
			out[i] = H3Step{
				KindsStep:   s,
				Page:        kinds,
				PauseMillis: 300,
			}
		}
		return out
	}
	// first are the warm-up page's steps and the kinds page's markup.
	first := []H3Step{
		{KindsStep: KindsStep{Label: "warm.so", Kind: "fetch", Host: h3Host}, Page: "/"},
		{KindsStep: KindsStep{Label: "warm.cs", Kind: "fetch", Host: h3CrossSite, Mode: "no-cors", Credentials: "include"}, Page: "/"},
		{KindsStep: KindsStep{Label: "kinds.so", Kind: "navigate", Host: h3Host}, Page: "/", PauseMillis: 2000},
		{KindsStep: KindsStep{Label: "style-head.so", Kind: "style", Host: h3Host, Tag: `<link rel="stylesheet" href="%s">`}, Page: kinds},
		{KindsStep: KindsStep{Label: "script-blocking.so", Kind: "script", Host: h3Host, Tag: `<script src="%s"></script>`}, Page: kinds},
		{KindsStep: KindsStep{Label: "script-async.so", Kind: "script", Host: h3Host, Tag: `<script async src="%s"></script>`}, Page: kinds},
		{KindsStep: KindsStep{Label: "image-markup.so", Kind: "image", Host: h3Host, Tag: `<img src="%s" alt="">`}, Page: kinds, Delay: h3ImageDelay},
		{KindsStep: KindsStep{Label: "image-offscreen.so", Kind: "image", Host: h3Host, Tag: `<div style="height: 5000px"></div><img src="%s" alt="">`}, Page: kinds, Delay: h3ImageDelay},
	}
	return slices.Concat(first, scripted(h3Host, []KindsStep{
		{Label: "script.so", Kind: "script"},
		{Label: "style.so", Kind: "style"},
		{Label: "image.so", Kind: "image"},
		{Label: "image-high.so", Kind: "image", Priority: "high"},
		{Label: "image-low.so", Kind: "image", Priority: "low"},
		{Label: "fetch.so", Kind: "fetch"},
		{Label: "fetch-high.so", Kind: "fetch", Priority: "high"},
		{Label: "fetch-low.so", Kind: "fetch", Priority: "low"},
		{Label: "fetch-json.so", Kind: "fetch", Method: "POST", Headers: map[string]string{"content-type": "application/json"}, Body: `{"a":1}`},
		{Label: "fetch-upload.so", Kind: "fetch", Method: "POST", Body: upload},
		{Label: "xhr.so", Kind: "xhr"},
		{Label: "xhr-post.so", Kind: "xhr", Method: "POST", Body: "a=1"},
		{Label: "beacon.so", Kind: "beacon", Body: "x"},
		{Label: "iframe.so", Kind: "iframe"},
		{Label: "font.so", Kind: "font"},
	}), scripted(h3CrossSite, []KindsStep{
		{Label: "fetch.cs", Kind: "fetch", Mode: "no-cors", Credentials: "include"},
		{Label: "image.cs", Kind: "image"},
		{Label: "script.cs", Kind: "script"},
	}))
}

// h3Page serves an h3 page: the documents and requests of its steps,
// each response advertising HTTP/3 on the port it came on, except from
// plainHosts. With doneHost the page asks for /done there (a host whose
// requests stay on TCP, when HTTP/3 is not this server's).
type h3Page struct {
	fixedHosts
	steps      func() []H3Step
	plainHosts []string
	doneHost   string
}

func (p h3Page) answer(req request) response {
	host, port := splitAuthority(req.host)
	path, _, _ := strings.Cut(req.path, "?")
	r := p.respond(host, port, path)
	if req.origin != "" {
		r.headers = append(r.headers, corsAllow(req.origin)...)
	}
	if !slices.Contains(p.plainHosts, host) {
		r.headers = append(r.headers, altSvc(port))
	}
	return r
}

func (p h3Page) respond(host, port, path string) response {
	switch {
	case path == "/" && host == h3Host:
		return response{status: 200, contentType: "text/html; charset=utf-8", body: p.document("/", port)}
	case path == "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}
	for _, s := range p.steps() {
		if s.Host != host || s.Path() != path {
			continue
		}
		if s.Kind == "navigate" {
			return response{status: 200, contentType: "text/html; charset=utf-8", body: p.document(path, port)}
		}
		r := kindsBody(s.Kind)
		if s.Download > 0 {
			r = response{
				status:      200,
				contentType: "application/octet-stream",
				body:        incompressible()[:s.Download],
			}
		}
		r.delay = s.Delay
		r.closeAfter, r.tcpOnly, r.blackhole = s.CloseAfter || s.GoAwayTCP, s.GoAwayTCP, s.Blackhole
		r.silence = s.Silence
		return r
	}
	return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
}

// document is the page at path: its steps' markup, then a script running
// the others one after another.
func (p h3Page) document(path, port string) []byte {
	var head, body strings.Builder
	var steps []H3Step
	for _, s := range p.steps() {
		if s.Page != path {
			continue
		}
		s.URL = "https://" + net.JoinHostPort(s.Host, port) + s.Path()
		switch {
		case s.Tag == "":
			steps = append(steps, s)
		case s.Kind == "image":
			fmt.Fprintf(&body, s.Tag+"\n", s.URL)
		default:
			fmt.Fprintf(&head, s.Tag+"\n", s.URL)
		}
	}
	stepsJSON, _ := json.Marshal(steps) //nolint:gosec // Credentials is fetch()'s credentials mode, not a secret
	done := "fetch('/done');"
	if p.doneHost != "" {
		done = "fetch('https://" + net.JoinHostPort(p.doneHost, port) + "/done', {mode: 'no-cors'});"
	}
	script := "(async () => {\nconst steps = " + string(stepsJSON) + ";\n" + kindsRun + `
for (const s of steps) {
  await new Promise(d => setTimeout(d, s.pause));
  if (s.kind === 'navigate') { location.href = s.url; return; }
  if (s.nowait) { run({...s, url: s.url + '?t=' + Math.round(performance.now())}); continue; }
  await run(s);
}
` + done + `
})();`
	return htmlPage("h3 "+path, "\n"+head.String(), body.String()+"<script>"+script+"</script>\n")
}
