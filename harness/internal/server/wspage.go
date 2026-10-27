package server

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// The websocket pages' hosts: the page's own, and a cross-site one.
const (
	wsHost      = "www.ws-a.test"
	wsCrossSite = "www.ws-b.test"
)

// WebSocket is the websocket scenario: its HTTP/2 server does not
// advertise SETTINGS_ENABLE_CONNECT_PROTOCOL, so a browser opens each
// wss:// socket over HTTP/1.1 (on a connection of its own: see
// capture.WebSocket.Connection). See WebSocketSteps.
var WebSocket = &Scenario{
	Name:      "websocket",
	Doc:       "WebSockets over HTTP/1.1 (the HTTP/2 server has no extended CONNECT): handshakes (same-origin, cross-site with subprotocols, with Lax, None and Partitioned cookies on both hosts), the permessage-deflate offer and compressed frames (varied text of every size, empty and incompressible messages, client_no_context_takeover, client_max_window_bits 8 to 15), frame headers (masking, 7/16/64-bit lengths, fragmentation), ping/pong, client and server close and who closes TCP first, ws:// from an https:// page and from an http:// page",
	multi:     wsPageOf(),
	plainPort: true,
}

// WebSocketH2 is the websocket-h2 scenario: the websocket page with
// SETTINGS_ENABLE_CONNECT_PROTOCOL advertised, so a browser that
// supports RFC 8441 opens its wss:// sockets as extended CONNECT streams
// on its HTTP/2 connections (and one that does not still uses HTTP/1.1).
var WebSocketH2 = &Scenario{
	Name: "websocket-h2",
	Doc:  "the websocket page with SETTINGS_ENABLE_CONNECT_PROTOCOL advertised: which browsers carry wss:// over HTTP/2 extended CONNECT (RFC 8441), its HEADERS (pseudo-header order, priority, flags), DATA-carried frames, and how the stream ends",
	Settings: []http2.Setting{
		{
			ID:  http2.SettingEnableConnectProtocol,
			Val: 1,
		},
	},
	multi:     wsPageOf(),
	plainPort: true,
}

func wsPageOf() wsPage {
	return wsPage{
		names: []string{
			wsHost,
			wsCrossSite,
		},
		runFor:  120 * time.Second,
		sockets: WebSocketSteps(),
		steps:   WebSocketPageSteps(),
	}
}

// WebSocketPage returns a websocket page's sockets and page steps (see
// WebSocketSteps and WebSocketPageSteps), and whether sc serves one.
func (sc *Scenario) WebSocketPage() (sockets []WebSocketStep, steps []WebSocketPageStep, ok bool) {
	p, ok := sc.multi.(wsPage)
	if !ok {
		return nil, nil, false
	}

	return p.sockets, p.steps, true
}

// wsPattern is what text messages are made of: WSMessage.Data.
const wsPattern = "mimicry-websocket-"

// WSMessage is a message a websocket step sends: Size bytes of wsPattern
// repeated (text), of the bytes 0 to 255 repeated (Binary), of wsProse
// from At (Prose, text), or pseudo-random bytes seeded by At (Noise, with
// Binary).
type WSMessage struct {
	Binary bool
	Size   int
	Prose  bool
	Noise  bool
	At     int
}

// Data is the message's payload.
func (m WSMessage) Data() []byte {
	switch {
	case m.Prose:
		return []byte((wsProse + wsProse)[m.At : m.At+m.Size])
	case m.Noise:
		return wsNoise(m.At, m.Size)
	}

	b := make([]byte, m.Size)
	for i := range b {
		if m.Binary {
			b[i] = byte(i)
		} else {
			b[i] = wsPattern[i%len(wsPattern)]
		}
	}

	return b
}

// js is the page's expression for the message.
func (m WSMessage) js() string {
	switch {
	case m.Prose:
		return "prose(" + strconv.Itoa(m.At) + ", " + strconv.Itoa(m.Size) + ")"
	case m.Noise:
		return "noise(" + strconv.Itoa(m.At) + ", " + strconv.Itoa(m.Size) + ")"
	case m.Binary:
		return "binary(" + strconv.Itoa(m.Size) + ")"
	}

	return "text(" + strconv.Itoa(m.Size) + ")"
}

// WebSocketStep is one socket of the websocket pages: page Page opens it at
// Host's /ws/<Label>, sends Sends, awaits the echoes and closes with 1000
// "bye" if Close. Each step is time-bounded and reported (capture.WSResult).
type WebSocketStep struct {
	Label string
	Page  string
	Host  string
	// Plain is a ws:// socket on the plain port; otherwise wss:// on the
	// TLS port.
	Plain bool
	// Protocols are the subprotocols the page asks for; Pick is the one the
	// server answers with.
	Protocols []string
	Pick      string
	// Deflate accepts permessage-deflate, with DeflateParams added to the
	// answer; otherwise no extension is answered.
	Deflate       bool
	DeflateParams []string
	// Ping has the server ping right after the handshake, ServerClose
	// send a close (1001 "going away") after its first echo.
	Ping        bool
	ServerClose bool
	Sends       []WSMessage
	Close       bool
	// Hangs is a user-agent token ("Firefox/") of a browser this socket
	// hangs; its page skips the step and reports it skipped.
	Hangs string
}

// Path is the socket's path.
func (s WebSocketStep) Path() string {
	return "/ws/" + s.Label
}

// wsStepTime bounds each socket step on the page.
const wsStepTime = 10 * time.Second

// WebSocketSteps lists the websocket pages' sockets in the order the pages
// open them:
//
//   - so-basic: same-origin, server ping, 7/16/64-bit lengths, text and binary;
//   - cs-protocols: cross-site, server picks chat of chat and superchat;
//   - so-deflate: permessage-deflate, repeated 4 KiB shows context takeover;
//   - so-deflate-sizes: the size sweep (wsSizeSweep);
//   - so-deflate-nct: client_no_context_takeover;
//   - so-deflate-w8 to w15: client_max_window_bits (Firefox skips w8: it hangs);
//   - so-server-close: the server closes first (1001);
//   - mixed: ws:// from the https:// page to the plain port;
//   - plain-so: ws:// from the http:// page (which cookies go).
func WebSocketSteps() []WebSocketStep {
	short := []WSMessage{
		{
			Size: 5,
		},
	}
	steps := []WebSocketStep{
		{
			Label: "so-basic",
			Page:  "sockets",
			Host:  wsHost,
			Ping:  true,
			Sends: []WSMessage{
				{Size: 5},
				{Size: 125},
				{Size: 126},
				{Size: 200},
				{Size: 65535},
				{Size: 65536},
				{Size: 70 << 10},
				{Binary: true, Size: 256},
				{Binary: true, Size: 65536},
			},
			Close: true,
		},
		{
			Label: "cs-protocols",
			Page:  "sockets",
			Host:  wsCrossSite,
			Protocols: []string{
				"chat",
				"superchat",
			},
			Pick:  "chat",
			Sends: short,
			Close: true,
		},
		{
			Label:   "so-deflate",
			Page:    "sockets",
			Host:    wsHost,
			Deflate: true,
			Sends: []WSMessage{
				{Size: 5},
				{Size: 4096},
				{Size: 4096},
				{Size: 70 << 10},
			},
			Close: true,
		},
		{
			Label:   "so-deflate-sizes",
			Page:    "sockets",
			Host:    wsHost,
			Deflate: true,
			Sends:   wsSizeSweep(),
			Close:   true,
		},
		{
			Label:         "so-deflate-nct",
			Page:          "sockets",
			Host:          wsHost,
			Deflate:       true,
			DeflateParams: []string{"client_no_context_takeover"},
			Sends: []WSMessage{
				{Prose: true, At: 1000, Size: 4096},
				{Prose: true, At: 1000, Size: 4096},
				{Prose: true, At: 9000, Size: 200},
				{Prose: true, At: 9000, Size: 6},
				{Binary: true, Noise: true, At: 7, Size: 300},
				{Size: 0},
				{Size: 5},
				// Each compresses to exactly its length on its own (zlib;
				// zlib-rs too for the first): sent compressed or not?
				{Prose: true, At: 98, Size: 10},
				{Prose: true, At: 28, Size: 24},
				{Prose: true, At: 20000, Size: 1000},
			},
			Close: true,
		},
	}

	for bits := 8; bits <= 15; bits++ {
		st := WebSocketStep{
			Label:         "so-deflate-w" + strconv.Itoa(bits),
			Page:          "sockets",
			Host:          wsHost,
			Deflate:       true,
			DeflateParams: []string{"client_max_window_bits=" + strconv.Itoa(bits)},
			Sends: []WSMessage{
				{Prose: true, At: 3000, Size: 3000},
				{Prose: true, At: 3000, Size: 3000},
				{Prose: true, At: 12000, Size: 20000},
				{Prose: true, At: 3500, Size: 1000},
			},
			Close: true,
		}
		if bits == 8 {
			// zlib-rs refuses a raw 8-bit window, and Firefox's
			// WebSocketChannel::HandleExtensions calls AbortSession
			// holding mCompressorMutex, which DoStopSession takes again:
			// its networking deadlocks and no page loads any more.
			st.Hangs = "Firefox/"
		}

		steps = append(steps, st)
	}

	return append(steps, []WebSocketStep{
		{
			Label:       "so-server-close",
			Page:        "sockets",
			Host:        wsHost,
			ServerClose: true,
			Sends:       short,
		},
		{
			Label: "mixed",
			Page:  "sockets",
			Host:  wsHost,
			Plain: true,
			Sends: short,
			Close: true,
		},
		{
			Label: "plain-so",
			Page:  "plain",
			Host:  wsHost,
			Plain: true,
			Sends: []WSMessage{
				{Size: 5},
				{Size: 200},
			},
			Close: true,
		},
	}...)
}

// wsSizeSweep is so-deflate-sizes's messages. See
// harness/reports/websocket-deflate-2026-10-26.md.
func wsSizeSweep() []WSMessage {
	out := []WSMessage{
		{Prose: true, At: 0, Size: 4096},
		{Size: 0},
		{Binary: true, Size: 0},
		{Prose: true, At: 100, Size: 200},
	}
	sizes := []int{}

	for n := 6; n <= 40; n++ {
		sizes = append(sizes, n)
	}

	sizes = append(sizes, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 448, 512,
		640, 768, 896, 1024, 1280, 1536, 1792, 2048, 2560, 3072, 3584, 4000, 4095)
	for _, n := range sizes {
		out = append(out, WSMessage{
			Prose: true,
			At:    n * 7919 % len(wsProse),
			Size:  n,
		})
	}

	// The last sweep message's text, close enough to be in the window.
	last := out[len(out)-1].At

	return append(out,
		WSMessage{Prose: true, At: last + 100, Size: 12},
		WSMessage{Prose: true, At: last + 100, Size: 30},
		WSMessage{Prose: true, At: last + 100, Size: 200},
		WSMessage{Binary: true, Noise: true, At: 1, Size: 6},
		WSMessage{Binary: true, Noise: true, At: 2, Size: 64},
		WSMessage{Binary: true, Noise: true, At: 3, Size: 1024},
		WSMessage{Binary: true, Noise: true, At: 4, Size: 4096},
		WSMessage{Prose: true, At: 0, Size: 4096},
	)
}

// WebSocketPageStep is one of the websocket pages' requests around their
// sockets: a scripted navigation (Nav) or a credentialed fetch() (CORS when
// cross-origin). Plain steps go to the plain port over http://.
type WebSocketPageStep struct {
	Label string
	Host  string
	Plain bool
	Path  string
	Nav   bool
	// Sets are the Set-Cookie values its response carries, in order.
	Sets []string
}

// The cookies set before the sockets: Lax, None and Partitioned on each host
// first-party, plus a cross-site Partitioned one in the page's partition.
var (
	wsHostCookies = []string{
		"ws_a_lax=1; Path=/; SameSite=Lax; Max-Age=600",
		"ws_a_none=1; Path=/; Secure; SameSite=None; Max-Age=600",
		"ws_a_part=1; Path=/; Secure; SameSite=None; Partitioned; Max-Age=600",
	}
	wsCrossCookies = []string{
		"ws_b_lax=1; Path=/; SameSite=Lax; Max-Age=600",
		"ws_b_none=1; Path=/; Secure; SameSite=None; Max-Age=600",
		"ws_b_part=1; Path=/; Secure; SameSite=None; Partitioned; Max-Age=600",
	}
	wsCrossPartitioned = []string{
		"ws_b_part3p=1; Path=/; Secure; SameSite=None; Partitioned; Max-Age=600",
	}
)

// wsDeletions deletes each cookie with the attributes it was set with, which
// partitioned and cross-site cookies need.
func wsDeletions(set []string) []string {
	out := make([]string, 0, len(set))
	for _, c := range set {
		parts := strings.Split(c, "; ")
		name, _, _ := strings.Cut(parts[0], "=")

		var d strings.Builder
		d.WriteString(name)
		d.WriteString("=")

		for _, a := range parts[1:] {
			if !strings.HasPrefix(a, "Max-Age=") {
				d.WriteString("; ")
				d.WriteString(a)
			}
		}

		out = append(out, d.String()+"; Max-Age=0")
	}

	return out
}

// WebSocketPageSteps lists the websocket pages' requests other than sockets
// and reports: set cookies, open sockets, delete cookies (Safari keeps them).
func WebSocketPageSteps() []WebSocketPageStep {
	return []WebSocketPageStep{
		{Label: "doc", Host: wsHost, Path: "/", Nav: true, Sets: wsHostCookies},
		{Label: "b-set", Host: wsCrossSite, Path: "/b/set", Nav: true, Sets: wsCrossCookies},
		{Label: "sockets", Host: wsHost, Path: "/w/sockets", Nav: true},
		{Label: "b-set-3p", Host: wsCrossSite, Path: "/b/set-3p", Sets: wsCrossPartitioned},
		{Label: "plain", Host: wsHost, Plain: true, Path: "/w/plain", Nav: true},
		{Label: "b-cleanup", Host: wsCrossSite, Path: "/b/cleanup", Nav: true, Sets: wsDeletions(wsCrossCookies)},
		{Label: "end", Host: wsHost, Path: "/w/end", Nav: true, Sets: wsDeletions(wsHostCookies)},
		{Label: "b-cleanup-3p", Host: wsCrossSite, Path: "/b/cleanup-3p", Sets: wsDeletions(wsCrossPartitioned)},
	}
}

// wsAway is how long the watchdog waits after the page leaves its https://
// origin: for the cross-site host's page and back, or for the plain page
// (a socket step) and the two navigations back.
const wsAway = 30 * time.Second

// wsPage is a websocket page: its hosts, sockets and page steps.
type wsPage struct {
	fixedHosts
	sockets []WebSocketStep
	steps   []WebSocketPageStep
}

func (p wsPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	path, _, _ := strings.Cut(req.path, "?")
	plain := req.ports.plain != "" && port == req.ports.plain
	ok := response{status: 200, contentType: "text/plain", body: []byte("ok\n")}

	switch {
	case path == "/done", path == wsResultPath:
		return ok
	case path == watchdogPath:
		return watchdogResponse()
	case strings.HasPrefix(path, "/ws/"):
		for _, s := range p.sockets {
			if s.Host == host && s.Path() == path && s.Plain == plain {
				return wsHandshake(s, req)
			}
		}
	}

	steps := p.steps
	for i, s := range steps {
		if s.Host != host || s.Path != path || s.Plain != plain {
			continue
		}

		r := ok
		if s.Nav {
			r = htmlResponse(htmlPage("websocket", "", pageScript(p.statements(i, req.ports))))
		} else if req.origin != "" {
			r.headers = corsAllow(req.origin)
		}

		for _, c := range s.Sets {
			r.headers = append(r.headers, hpack.HeaderField{
				Name:  "set-cookie",
				Value: c,
			})
		}

		return r
	}

	return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
}

// wsHandshake answers a request for a socket's path: a handshake is
// accepted as the step says, anything else answered 400.
func wsHandshake(s WebSocketStep, req request) response {
	handshake := strings.EqualFold(req.upgrade, "websocket") &&
		(req.method == "GET" && req.wsKey != "" || req.method == "CONNECT")
	if !handshake {
		return response{status: 400, contentType: "text/plain", body: []byte("not a websocket handshake\n")}
	}

	a := &wsAccept{
		ping:        s.Ping,
		serverClose: s.ServerClose,
	}
	if s.Pick != "" && slices.Contains(headerList(req.wsProtocols), s.Pick) {
		a.subprotocol = s.Pick
	}

	if s.Deflate {
		for _, ext := range headerList(req.wsExtensions) {
			if name, _, _ := strings.Cut(ext, ";"); strings.TrimSpace(name) == "permessage-deflate" {
				a.extensions = strings.Join(append([]string{"permessage-deflate"}, s.DeflateParams...), "; ")
				a.clientNoContext = slices.Contains(s.DeflateParams, "client_no_context_takeover")
			}
		}
	}

	return response{
		status:    101,
		websocket: a,
	}
}

// headerList splits a list-valued header into its trimmed elements.
func headerList(v string) []string {
	var out []string
	for e := range strings.SplitSeq(v, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}

	return out
}

// wsPageURL is a page step's URL.
func wsPageURL(s WebSocketPageStep, ports portPair) string {
	if s.Plain {
		return "http://" + net.JoinHostPort(s.Host, ports.plain) + s.Path
	}

	return "https://" + net.JoinHostPort(s.Host, ports.tls) + s.Path
}

// wsSocketURL is a socket's URL.
func wsSocketURL(s WebSocketStep, ports portPair) string {
	if s.Plain {
		return "ws://" + net.JoinHostPort(s.Host, ports.plain) + s.Path()
	}

	return "wss://" + net.JoinHostPort(s.Host, ports.tls) + s.Path()
}

// wsHelpers are the page's helpers: get(u) fetches with credentials (5 s
// at most); text(n), binary(n), prose(at, n) and noise(at, n) are
// WSMessage payloads; socket(url, protocols, sends, close) runs one
// socket step and resolves to what the script saw of it
// (capture.WSResult), within wsStepTime; report sends that to the server.
var wsHelpers = `const get = u => within(fetch(u, {credentials: "include"}).then(r => r.text()), 5000);
const pattern = ` + strconv.Quote(wsPattern) + `;
const text = n => pattern.repeat(Math.ceil(n / pattern.length)).slice(0, n);
const binary = n => Uint8Array.from({length: n}, (_, i) => i % 256);
const corpus = ` + strconv.Quote(wsProse) + `;
const prose = (at, n) => (corpus + corpus).slice(at, at + n);
const noise = (seed, n) => {
  let x = seed >>> 0;
  return Uint8Array.from({length: n}, () => (x = (Math.imul(x, 1664525) + 1013904223) >>> 0) >>> 24);
};
const report = (label, res) => get("` + wsResultPath + `?label=" + encodeURIComponent(label) + "&r=" + encodeURIComponent(JSON.stringify(res)));
const socket = (url, protocols, sends, close) => {
  const res = {open: false, sent: 0, echoes: 0, closed: false};
  const run = new Promise(done => {
    let ws;
    try {
      ws = protocols.length ? new WebSocket(url, protocols) : new WebSocket(url);
    } catch (e) {
      res.error = e.name + ": " + e.message;
      return done();
    }
    ws.binaryType = "arraybuffer";
    ws.onopen = () => {
      res.open = true;
      res.protocol = ws.protocol;
      res.extensions = ws.extensions;
      for (const m of sends) {
        ws.send(m);
        res.sent++;
      }
    };
    ws.onmessage = () => {
      res.echoes++;
      if (close && res.echoes === sends.length) ws.close(1000, "bye");
    };
    ws.onerror = () => {
      res.error = res.error || "error";
    };
    ws.onclose = e => {
      res.closed = true;
      res.code = e.code;
      res.reason = e.reason;
      res.clean = e.wasClean;
      done();
    };
  });
  return within(run.then(() => true), ` + strconv.FormatInt(wsStepTime.Milliseconds(), 10) + `).then(ok => {
    if (!ok) res.timedOut = true;
    return res;
  });
};
`

// statements are page step i's script: it fetches the steps up to the
// next navigation, runs its sockets one after another and reports each,
// then goes there, or asks for /done. The watchdog lives on https:// of
// wsHost: the first page registers it, the others disarm it on load, arm
// it before leaving the origin and end it before /done.
func (p wsPage) statements(i int, ports portPair) []string {
	steps := p.steps
	page := steps[i]
	watched := page.Host == wsHost && !page.Plain
	out := []string{
		wsHelpers,
	}

	switch {
	case i == 0:
		out = append(out, "await register();")
	case watched:
		out = append(out, "await arm(0);")
	}

	next := -1
	for j := i + 1; j < len(steps); j++ {
		if steps[j].Nav {
			next = j
			break
		}

		out = append(out, fmt.Sprintf("await get(%s);", strconv.Quote(wsPageURL(steps[j], ports))))
	}

	for _, s := range p.sockets {
		if s.Page != page.Label {
			continue
		}

		protocols, _ := json.Marshal(append([]string{}, s.Protocols...))
		sends := make([]string, len(s.Sends))

		for k, m := range s.Sends {
			sends[k] = m.js()
		}

		run := fmt.Sprintf("await socket(%s, %s, [%s], %t)", strconv.Quote(wsSocketURL(s, ports)), protocols, strings.Join(sends, ", "), s.Close)
		if s.Hangs != "" {
			run = fmt.Sprintf("(navigator.userAgent.includes(%s) ? {skipped: true} : %s)", strconv.Quote(s.Hangs), run)
		}

		out = append(out, fmt.Sprintf("await report(%s, %s);", strconv.Quote(s.Label), run))
	}

	if next < 0 {
		if watched {
			out = append(out, "await end();")
		}

		return append(out, `fetch("/done");`)
	}

	n := steps[next]
	if watched && (n.Host != wsHost || n.Plain) {
		out = append(out, fmt.Sprintf("await arm(%d);", wsAway.Milliseconds()))
	}

	return append(out, fmt.Sprintf("location.href = %s;", strconv.Quote(wsPageURL(n, ports))))
}
