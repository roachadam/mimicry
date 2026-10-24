package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// Scenario is one way of running the capture page: the SETTINGS the server
// advertises and how it answers each path. The first request is always the
// same navigation to /, so every scenario also re-checks the profile.
type Scenario struct {
	// Name identifies the scenario in capture file names.
	Name string
	// Doc says what the scenario finds out.
	Doc string
	// Settings are advertised in the server's SETTINGS frame.
	Settings []http2.Setting
	// HTTP1 offers only http/1.1 in ALPN, so browsers load the page over
	// HTTP/1.1.
	HTTP1 bool
	// respond answers the paths the scenario changes; the default page
	// answers the rest.
	respond func(path string, uploaded int) (response, bool)
	// multi, when set, serves a page across several hosts and answers every
	// request instead (Replay, the kinds page).
	multi multiHost
}

// request is what a scenario needs to know to answer a request.
type request struct {
	method string
	// host is the authority, with its port.
	host string
	path string
	// origin and preflightHeaders are the Origin and
	// Access-Control-Request-Headers headers.
	origin           string
	preflightHeaders string
	// uploaded is the request body's size.
	uploaded int
}

// DefaultScenario is the capture page every profile comes from.
var DefaultScenario = &Scenario{
	Name: capture.DefaultScenario,
	Doc:  "subresources, a 16 MiB download, a 200 KB upload, a Huffman probe header, a navigation with document.cookie cookies",
}

// Scenarios lists every scenario, DefaultScenario first.
var Scenarios = []*Scenario{
	DefaultScenario,
	{
		Name:    "set-cookie",
		Doc:     "cookies set by the server, real-world shaped, sent on subresources, fetch() and a navigation",
		respond: setCookieResponse,
	},
	{
		Name: "large-settings",
		Doc:  "the server allows a 64 KiB header table and 64 KiB frames: size updates and DATA frame caps",
		Settings: []http2.Setting{
			{
				ID:  http2.SettingHeaderTableSize,
				Val: 65536,
			},
			{
				ID:  http2.SettingMaxFrameSize,
				Val: 65536,
			},
		},
	},
	{
		Name: "small-limits",
		Doc:  "the server allows a 256-byte header table and 2 concurrent streams: evictions, queueing, priorities",
		Settings: []http2.Setting{
			{
				ID:  http2.SettingHeaderTableSize,
				Val: 256,
			},
			{
				ID:  http2.SettingMaxConcurrentStreams,
				Val: 2,
			},
		},
	},
	{
		Name: "huge-table",
		Doc:  "the server allows a 1 MiB header table: each browser's own table ceiling, and whether it announces the server's size or its own",
		Settings: []http2.Setting{
			{
				ID:  http2.SettingHeaderTableSize,
				Val: 1 << 20,
			},
		},
	},
	{
		Name: "explicit-table",
		Doc:  "the server sends the default 4096 header table size explicitly: whether a browser acknowledges an unchanged size",
		Settings: []http2.Setting{
			{
				ID:  http2.SettingHeaderTableSize,
				Val: 4096,
			},
		},
	},
	{
		Name:    "slow-idle",
		Doc:     "a 15 s trickle download, then 11 s idle before a request: window-update timers and pings",
		respond: slowIdleResponse,
	},
	{
		Name:  "http1",
		Doc:   "the server offers only HTTP/1.1: header casing and order, cookies and connections over HTTP/1.1",
		HTTP1: true,
	},
	Kinds,
	KindsHTTP1,
	Cookies,
	Resume,
}

// ScenarioByName finds a scenario.
func ScenarioByName(name string) (*Scenario, error) {
	for _, s := range Scenarios {
		if s.Name == name {
			return s, nil
		}
	}
	names := make([]string, len(Scenarios))
	for i, s := range Scenarios {
		names[i] = s.Name
	}
	return nil, fmt.Errorf("unknown scenario %q (have %s)", name, strings.Join(names, ", "))
}

func (sc *Scenario) answer(r request) response {
	if sc.multi != nil {
		return sc.multi.answer(r)
	}
	if sc.respond != nil {
		if resp, ok := sc.respond(r.path, r.uploaded); ok {
			return resp
		}
	}
	return defaultResponse(r.path, r.uploaded)
}

// tableSize is the header table size the scenario allows.
func (sc *Scenario) tableSize() uint32 {
	if v, ok := sc.Setting(http2.SettingHeaderTableSize); ok {
		return v
	}
	return 4096
}

// maxFrameSize is the largest frame the scenario accepts.
func (sc *Scenario) maxFrameSize() uint32 {
	if v, ok := sc.Setting(http2.SettingMaxFrameSize); ok {
		return v
	}
	return 16384
}

// Setting reports the value of a setting the scenario advertises, and
// whether it advertises it at all.
func (sc *Scenario) Setting(id http2.SettingID) (uint32, bool) {
	for _, s := range sc.Settings {
		if s.ID == id {
			return s.Val, true
		}
	}
	return 0, false
}

// response is what the server answers for a path.
type response struct {
	status      int
	contentType string
	body        []byte
	// headers are sent after the standard ones.
	headers []hpack.HeaderField
	// drip sends the body dripChunk bytes at a time, dripEvery apart.
	dripChunk int
	dripEvery time.Duration
	// closeAfter ends the connection after this response (HTTP/1.1:
	// Connection: close; HTTP/2: GOAWAY), so the next request needs a new
	// one.
	closeAfter bool
	// delay holds the response back, so a client's concurrent requests
	// stay in flight together.
	delay time.Duration
}

// page is what a browser loads: subresources on the same connection, then a
// script (app.js, which scenarios replace) that finally asks for /done,
// which completes the capture.
const page = `<!doctype html>
<html><head><meta charset="utf-8"><title>mimicry capture</title>
<link rel="icon" href="data:,">
<link rel="stylesheet" href="/style.css">
</head><body>
<p>mimicry capture ok</p>
<img src="/img.png" alt="">
<script src="/app.js"></script>
</body></html>
`

// script downloads, uploads, requests again with a header whose Huffman
// coding is longer than the raw string (which tells "Huffman when not
// longer" from "always"), and navigates to /nav with cookies. The cookies are
// scoped to /nav, so no other request (and no later sample in a persistent
// browser like Safari) carries them, and /nav deletes them.
const script = `(async () => {
  const big = await fetch('/big');
  await big.arrayBuffer();
  await fetch('/upload', {method: 'POST', body: new Uint8Array(` + uploadSize + `)});
  await (await fetch('/again', {headers: {'x-mimicry-huffman': '{^}~'}})).text();
  document.cookie = 'mimicry_a=1; path=/nav';
  document.cookie = 'mimicry_b=2; path=/nav';
  location.href = '/nav';
})();
`

// navPage is the page a cookie-carrying navigation lands on.
const navPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>mimicry capture</title>
<link rel="icon" href="data:,">
</head><body>
<p>mimicry capture ok</p>
<script>
document.cookie = 'mimicry_a=; path=/nav; max-age=0';
document.cookie = 'mimicry_b=; path=/nav; max-age=0';
fetch('/done');
</script>
</body></html>
`

const uploadSize = "200000"

// bigSize is larger than every captured browser's stream receive window
// (Chrome 6 MiB, Firefox ~12 MiB, Safari 2 MiB), so the download shows how
// each one returns flow-control credit.
const bigSize = 16 << 20

// png is a 1x1 transparent PNG.
var png = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func defaultResponse(path string, uploaded int) response {
	switch path {
	case "/":
		return response{status: 200, contentType: "text/html; charset=utf-8", body: []byte(page)}
	case "/style.css":
		return response{status: 200, contentType: "text/css", body: []byte("body { font: 14px sans-serif }\n")}
	case "/app.js":
		return response{status: 200, contentType: "text/javascript", body: []byte(script)}
	case "/img.png":
		return response{status: 200, contentType: "image/png", body: png}
	case "/big":
		return response{status: 200, contentType: "application/octet-stream", body: make([]byte, bigSize)}
	case "/upload":
		return response{status: 200, contentType: "text/plain", body: []byte(strconv.Itoa(uploaded) + "\n")}
	case "/nav":
		return response{status: 200, contentType: "text/html; charset=utf-8", body: []byte(navPage)}
	case "/again", "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}
	return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
}

// serverCookies is shaped like the first response of bot-protected sites
// (sampled 2026-10-23: Nike, Ticketmaster, Zillow, Walmart): a mix of
// attributes, lengths from 1 to 88 characters, one name set several times,
// plus an Akamai _abck-sized value. The p19, p20 and p21 crumbs are 19, 20
// and 21 bytes long, probing where Firefox and Safari stop never-indexing
// short cookies. Values are synthetic. Every cookie expires in 10 minutes and
// /done deletes them, so Safari, which cannot be isolated, keeps nothing.
var serverCookies = []struct {
	name, attrs string
	length      int
}{
	{"sid", "Path=/; Secure; HttpOnly; SameSite=Lax", 80},
	{"bid", "Path=/; Secure; HttpOnly; SameSite=None", 80},
	{"geoloc", "Path=/", 50},
	{"aka_a2", "Path=/; Secure; HttpOnly", 1},
	{"aka_a2", "Path=/; Secure; HttpOnly", 1},
	{"_abck", "Path=/; Secure", 480},
	{"ni_d", "Path=/; Secure", 36},
	{"p19", "Path=/", 15},
	{"p20", "Path=/", 16},
	{"p21", "Path=/", 17},
}

// cookieValue is a deterministic value of n characters drawn from the
// cookie-value alphabet real sites use.
func cookieValue(seed string, n int) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz~-_"
	var b strings.Builder
	h := uint32(2166136261)
	for i := range len(seed) {
		h = (h ^ uint32(seed[i])) * 16777619
	}
	for range n {
		h = h*1664525 + 1013904223
		b.WriteByte(alphabet[h>>26])
	}
	return b.String()
}

// cookieScript sends the server's cookies on a fetch() and a navigation.
const cookieScript = `(async () => {
  await (await fetch('/again')).text();
  location.href = '/nav';
})();
`

func setCookieResponse(path string, _ int) (response, bool) {
	switch path {
	case "/":
		r := defaultResponse(path, 0)
		for _, c := range serverCookies {
			r.headers = append(r.headers, hpack.HeaderField{
				Name:  "set-cookie",
				Value: c.name + "=" + cookieValue(c.name, c.length) + "; Max-Age=600; " + c.attrs,
			})
		}
		return r, true
	case "/app.js":
		return response{status: 200, contentType: "text/javascript", body: []byte(cookieScript)}, true
	case "/done":
		r := defaultResponse(path, 0)
		for _, c := range serverCookies {
			r.headers = append(r.headers, hpack.HeaderField{
				Name:  "set-cookie",
				Value: c.name + "=; Max-Age=0; " + c.attrs,
			})
		}
		return r, true
	}
	return response{}, false
}

// slowScript reads a trickled body, stays idle for longer than Chrome's
// 10 s connection check, then makes one more request.
const slowScript = `(async () => {
  await (await fetch('/slow')).arrayBuffer();
  await new Promise(resolve => setTimeout(resolve, 11000));
  await (await fetch('/again')).text();
  await fetch('/done');
})();
`

func slowIdleResponse(path string, _ int) (response, bool) {
	switch path {
	case "/app.js":
		return response{status: 200, contentType: "text/javascript", body: []byte(slowScript)}, true
	case "/slow":
		return response{
			status:      200,
			contentType: "application/octet-stream",
			body:        make([]byte, 60<<10),
			dripChunk:   1 << 10,
			dripEvery:   250 * time.Millisecond,
		}, true
	}
	return response{}, false
}
