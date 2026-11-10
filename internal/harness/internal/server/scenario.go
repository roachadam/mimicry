package server

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
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
	// connectHosts are server names whose HTTP/2 connections also
	// advertise SETTINGS_ENABLE_CONNECT_PROTOCOL (see settingsFor), so
	// one page has hosts with extended CONNECT and hosts without.
	connectHosts []string
	// HTTP1 offers only http/1.1 in ALPN, so browsers load the page over
	// HTTP/1.1.
	HTTP1 bool
	// recordUploads keeps every TLS record a client sends (TLS 1.3): each
	// HTTP/1.1 request gets how they carried it (capture.H1Upload), an
	// HTTP/2 trace all its records (h2trace.Trace.Records); the traces
	// are written gzipped (capture.Capture.Gzip).
	recordUploads bool
	// Proxy, when set, has the browser reach the server through the
	// capture proxy: ProxyHTTP, ProxyHTTPS, ProxySOCKS5, ProxySOCKS4 or
	// ProxySOCKS4A.
	Proxy string
	// Plain serves the page over plain HTTP (HTTP/1.1, no TLS).
	Plain bool
	// ProxyAuth has the proxy demand ProxyUser and ProxyPassword: CONNECTs,
	// or with Plain each forwarded request, without them are answered 407.
	ProxyAuth bool
	// proxyHTTP1 has a ProxyHTTPS proxy offer only http/1.1 in ALPN (see
	// ProxyNextProtos).
	proxyHTTP1 bool
	// maxVersion caps the TLS version the server negotiates (zero: TLS
	// 1.3), to see what browsers do with a TLS 1.2-only server.
	maxVersion uint16
	// mixedScheme serves plain HTTP on the same port as TLS: a connection
	// whose first byte does not start a TLS handshake record is answered
	// as plain HTTP/1.1, so a page can move between http:// and https:// of
	// one host (and reach http://localhost).
	mixedScheme bool
	// plainPort serves plain HTTP/1.1, and nothing else, on a second port
	// (see PlainPort); ports are both ports, set on the copy Serve runs.
	// startPlain starts the page there (see StartsPlain).
	plainPort  bool
	startPlain bool
	ports      portPair
	// defaultPort has the capture tool also take the TLS port's traffic on
	// 443, https://'s default (see DefaultPort), so a URL naming the
	// default port reaches the page.
	defaultPort bool
	// respond answers the paths the scenario changes; the default page
	// answers the rest.
	respond func(path string, uploaded int) (response, bool)
	// multi, when set, serves a page across several hosts and answers every
	// request instead (Replay and the other pages of several hosts).
	multi multiHost
	// helloRetryHost, when set, answers every ClientHello for that server
	// name with a HelloRetryRequest carrying a cookie, records the second
	// ClientHello and closes the connection (HRR).
	helloRetryHost string
	// h3, when set, also serves HTTP/3 over QUIC on the TLS port's UDP
	// twin (see HTTP3), with these settings.
	h3 *h3Server
	// clientCert, when set, has the server ask every TLS connection for a
	// client certificate (Server.ClientCAs) and record each handshake
	// (capture.ClientAuth); it names the key of the identity the capture
	// tool gives the browser (ClientKeyEC, ClientKeyRSA).
	clientCert string
	// ech has the browser look the page's hosts up in DNS, where their
	// HTTPS records carry ECH configs (ECHRecord), and the server decrypt
	// ECH for the configs it holds keys of (ECH).
	ech bool
}

// ResolvesHosts reports a scenario whose hosts the browser looks up in DNS
// (the capture tool's DNS stub, or its DoH endpoint) instead of mapping
// them to the server's address itself, so it sees their HTTPS records.
func (sc *Scenario) ResolvesHosts() bool {
	return sc.ech
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
	// ports are a PlainPort scenario's two ports (zero otherwise).
	ports portPair
	// upgrade is the protocol a WebSocket handshake asks for: an
	// HTTP/1.1 Upgrade header, or an HTTP/2 extended CONNECT's :protocol.
	// wsKey, wsProtocols and wsExtensions are its Sec-WebSocket-Key,
	// -Protocol and -Extensions (repeated headers joined by ", ").
	upgrade      string
	wsKey        string
	wsProtocols  string
	wsExtensions string
}

// DefaultScenario is the capture page every profile comes from.
var DefaultScenario = &Scenario{
	Name: capture.DefaultScenario,
	Doc:  "subresources, a 16 MiB download, a 200 KB upload, a Huffman probe header, a navigation with document.cookie cookies",
}

// Values of Scenario.Proxy, also the proxy URL's scheme the browser is
// given. ProxyHTTPS is an HTTP proxy the browser reaches over TLS, at
// ProxyHost. With ProxySOCKS4 the browser resolves the host and sends its
// IPv4 address; with ProxySOCKS4A it sends the host name (SOCKS4a).
const (
	ProxyHTTP    = "http"
	ProxyHTTPS   = "https"
	ProxySOCKS5  = "socks5"
	ProxySOCKS4  = "socks4"
	ProxySOCKS4A = "socks4a"
)

// ProxyHost is the name of a ProxyHTTPS proxy, under .test so the capture
// CA issues its certificate and browsers send server_name.
const ProxyHost = "proxy.mimicry.test"

// ProxyNextProtos are the ALPN protocols a ProxyHTTPS scenario's proxy
// offers, in its order of preference: h2 and http/1.1, as proxies that
// speak HTTP/2 do, or only http/1.1.
func (sc *Scenario) ProxyNextProtos() []string {
	if sc.proxyHTTP1 {
		return []string{
			"http/1.1",
		}
	}

	return []string{
		"h2",
		"http/1.1",
	}
}

// The credentials a ProxyAuth scenario's proxy demands, which the capture
// tool gives the browser when challenged.
const (
	ProxyUser     = "mimicry"
	ProxyPassword = "capture"
)

// ProxyAuthorization is the Proxy-Authorization value for ProxyUser and
// ProxyPassword.
func ProxyAuthorization() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(ProxyUser+":"+ProxyPassword))
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
	HTTP1Upload,
	H2Upload,
	{
		Name:  "proxy-connect",
		Doc:   "the capture page through an HTTP proxy: each CONNECT request, and the tunnelled connection against the direct one",
		Proxy: ProxyHTTP,
	},
	{
		Name:  "proxy-socks5",
		Doc:   "the capture page through a SOCKS5 proxy: the handshake bytes, and the tunnelled connection against the direct one",
		Proxy: ProxySOCKS5,
	},
	{
		Name:  "proxy-socks4",
		Doc:   "the capture page through a SOCKS4 proxy, the browser resolving the host: the request bytes, and the tunnelled connection against the direct one",
		Proxy: ProxySOCKS4,
	},
	{
		Name:  "proxy-socks4a",
		Doc:   "the capture page through a SOCKS4a proxy, the host name sent for the proxy to resolve: the request bytes, and the tunnelled connection against the direct one",
		Proxy: ProxySOCKS4A,
	},
	{
		Name:  "proxy-plain",
		Doc:   "the capture page over plain HTTP through an HTTP proxy: absolute-form requests and their headers",
		Proxy: ProxyHTTP,
		Plain: true,
	},
	{
		Name:      "proxy-auth",
		Doc:       "the resume page (a new connection per request, three hosts) through an HTTP proxy demanding Basic credentials: the 407 challenge, the retry, and whether later CONNECTs send credentials unasked",
		Proxy:     ProxyHTTP,
		ProxyAuth: true,
		multi: resumePage{
			fixedHosts: resumeHosts,
		},
	},
	{
		Name:      "proxy-plain-auth",
		Doc:       "the capture page over plain HTTP through an HTTP proxy demanding Basic credentials: where Proxy-Authorization goes on forwarded requests",
		Proxy:     ProxyHTTP,
		Plain:     true,
		ProxyAuth: true,
	},
	{
		Name:  "proxy-https",
		Doc:   "the capture page through an HTTPS proxy offering h2 and http/1.1: the ClientHello to the proxy and the protocol it gets, each CONNECT (an HTTP/2 stream with every frame sent, or HTTP/1.1 lines), the ClientHello through the tunnel, and the tunnelled connection against the direct one",
		Proxy: ProxyHTTPS,
	},
	{
		Name:       "proxy-https-h1",
		Doc:        "the capture page through an HTTPS proxy offering only http/1.1: the ClientHello to the proxy, each CONNECT's lines, the ClientHello through the tunnel, and the tunnelled connection against the direct one",
		Proxy:      ProxyHTTPS,
		proxyHTTP1: true,
	},
	{
		Name:      "proxy-https-auth",
		Doc:       "the resume page (a new connection per request, three hosts) through an HTTPS proxy offering h2 and http/1.1 and demanding Basic credentials: the 407 challenge and the retry on the protocol the proxy got, whether later CONNECTs send credentials unasked, and how many connections go to the proxy",
		Proxy:     ProxyHTTPS,
		ProxyAuth: true,
		multi: resumePage{
			fixedHosts: resumeHosts,
		},
	},
	{
		Name:       "proxy-https-h1-auth",
		Doc:        "the resume page through an HTTPS proxy offering only http/1.1 and demanding Basic credentials: the challenge flow over HTTP/1.1 and, with each tunnel on a connection of its own, whether TLS to the proxy resumes",
		Proxy:      ProxyHTTPS,
		proxyHTTP1: true,
		ProxyAuth:  true,
		multi: resumePage{
			fixedHosts: resumeHosts,
		},
	},
	Kinds,
	KindsHTTP1,
	KindsPlain,
	CORS,
	Cookies,
	Resume,
	ResumeTLS12,
	CookiesEdge,
	CORSEdge,
	CORSEdgeCookies,
	Connections,
	HeadersOrder,
	CookiesEdge2,
	Redirects2,
	Redirects2Cookies,
	NavRedirectSO,
	NavRedirectSS,
	NavRedirectCS,
	NavRedirectHTTP,
	NavRedirectPage,
	NavRedirectForm,
	NavRedirectClick,
	NavRedirectSOCookies,
	NavRedirectSSCookies,
	NavRedirectCSCookies,
	NavRedirectHTTPCookies,
	NavRedirectPageCookies,
	NavRedirectFormCookies,
	NavRedirectClickCookies,
	WebSocket,
	WebSocketH2,
	ProxyWebSocket,
	ProxyWebSocketAuth,
	ProxyWebSocketSOCKS5,
	H3Discovery,
	H3Page,
	H3Blackhole,
	H3Broken,
	H3V2,
	H3VN,
	H3Download,
	H3Early,
	H3TooEarly,
	H3ResumeTable,
	H3StreamsBlocked,
	Bodiless,
	HRR,
	ECH,
	ECHH3,
	ClientHints,
	URLs,
	ClientCertEC,
	ClientCertRSA,
	ClientCertECTLS12,
	ClientCertRSATLS12,
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

// UnsupportedOnIOS says why Safari on an iPhone, served from a Mac over
// the network (cmd/capture -listen), cannot take sc; "" when it can.
func (sc *Scenario) UnsupportedOnIOS() string {
	switch {
	case sc.ProxyAuth:
		return iosNoProxyAuth
	case sc.Proxy == ProxySOCKS5:
		return "the iPhone's Wi-Fi proxy setting takes an HTTP proxy only"
	case sc.Proxy == ProxySOCKS4, sc.Proxy == ProxySOCKS4A:
		return iosNoSOCKS4
	case sc.Proxy == ProxyHTTPS:
		return "the iPhone's Wi-Fi proxy setting takes a plain HTTP proxy, and its proxy script is read by the same system networking as macOS's, which " + noSafariHTTPSProxy + " ([INFERENCE] not tried on the phone)"
	case sc == CookiesEdge:
		return "its lo-* steps navigate to http://localhost, which on the iPhone is the phone itself, so the page stops there"
	case sc.clientCert != "":
		return "nothing installs a client identity on the iPhone, and " + noSafariClientCert
	case sc == ECHH3:
		return "[INFERENCE] as macOS Safari: " + safariECHH3
	}

	return ""
}

// UnsupportedBy says why the browser the capture tool calls tool (on a
// computer; on an iPhone see UnsupportedOnIOS) cannot take sc, "" when it
// can.
func (sc *Scenario) UnsupportedBy(tool string) string {
	switch {
	case sc.clientCert != "" && tool == "safari":
		return noSafariClientCert
	case sc.Proxy == ProxyHTTPS && tool == "safari":
		return "macOS gives Safari no proxy it reaches over TLS: its secure web proxy setting is a plain HTTP proxy for https:// URLs, and the system " + noSafariHTTPSProxy
	case sc == ECHH3 && tool == "safari":
		return safariECHH3
	case sc.Proxy == ProxySOCKS4A && capture.Chromium(tool):
		return "Chromium has no SOCKS4a: given --proxy-server=socks4a://, Chrome 156 sent nothing to the proxy (2026-11-08), and with socks4:// it resolves the host itself (proxy-socks4)"
	}

	return ""
}

// safariECHH3 is why Safari does not take ech-h3.
const safariECHH3 = "Safari ignores ECH configs (tls.realEch.ignore), so the page shows nothing of ECH, and its HTTP/3 discovery is h3-discovery's; and on https://'s default port, unlike the h3 pages' fresh port per sample, its Alt-Svc cache outlives a sample: Safari 27's first sample used TCP and a later one HTTP/3 alone, so the run failed, and every sample of the next run used HTTP/3 alone (macOS 27, 2026-11-09)"

// noSafariHTTPSProxy is what macOS (27.0.1, Safari 27, 2026-11-09) did
// with a proxy script answering HTTPS host:port for *.test.
const noSafariHTTPSProxy = "ignores a proxy script's HTTPS host:port: Safari connected to the site directly, and nothing reached the proxy (macOS 27.0.1, 2026-11-09)"

// noSafariSOCKS4 is what macOS (27.0.1 with Safari 27.0.1, 26.2 with
// Safari 26.2; 2026-11-10) did given a SOCKS proxy that speaks SOCKS4 only.
const noSafariSOCKS4 = "Safari speaks SOCKS4 only through macOS's SOCKS proxy setting with kCFStreamPropertySOCKSVersion set to SOCKS4 (scutil --prefs): a proxy script's SOCKS host:port gets SOCKS5 (05 01 00) and no SOCKS4 retry when refused, and its SOCKS4 host:port is ignored (decision 0063)"

// iosNoSOCKS4 is why the SOCKS4 scenarios are never captured on the
// iPhone, and its profiles refuse SOCKS4 (the maintainer's decision,
// 2026-11-10; decision 0063).
const iosNoSOCKS4 = "iOS offers no SOCKS4 route: the Wi-Fi proxy's Manual setting and configuration profiles take an HTTP proxy only, and a proxy script's SOCKS is SOCKS5 on the networking stack iOS shares with macOS, where " + noSafariSOCKS4 + ", a key iOS gives no way to set ([INFERENCE] for iOS itself, not tried on a phone); a SOCKS4 form no iPhone sends would show to the proxy, so the iPhone profiles refuse SOCKS4"

// iosNoProxyAuth is why the proxy-credential scenarios are neither
// captured on the iPhone nor inherited from macOS Safari.
const iosNoProxyAuth = "Safari on iOS 27.2 never answers a proxy's Basic 407 with the Wi-Fi proxy's credentials: every CONNECT (Safari's and the system's) came without Proxy-Authorization, on a new connection each, and Safari failed with NSURLErrorDomain -1013, so macOS Safari's challenge flow cannot stand in"

// AutomationDiffersOnIOS says why safaridriver's automation window on an
// iPhone does not stand in for the phone's own Safari in sc, "" when it
// does (decision 0051). It orders headers otherwise: Origin before
// Sec-Fetch-Site in requests with a body, redirect hops and the
// service-worker script in another order than the phone's own Safari
// (by-hand captures); and a redirect page's watchdog service worker never
// ended a page there. Only the default page, plain or through the proxy,
// was seen to match.
func (sc *Scenario) AutomationDiffersOnIOS() string {
	if slices.Contains(iosAutomationMatches, sc.Name) {
		return ""
	}

	return "safaridriver's automation window orders headers otherwise than the phone's own Safari (Origin before Sec-Fetch-Site with a body, redirect hops, service-worker scripts) and never runs a redirect page's watchdog; use -ios-driver devicectl"
}

// iosAutomationMatches are the scenarios whose safaridriver captures
// match the phone's own Safari's: the default page (cmd/diff, by hand and
// through safaridriver) and its loads over HTTP/1.1 and through the proxy.
var iosAutomationMatches = []string{
	"http1",
	"page",
	"proxy-connect",
	"proxy-plain",
}

// NotInheritedOnIOS says why an iOS Safari profile does not inherit macOS
// Safari's captures of sc it does not capture itself, "" when it does
// (decision 0051): where the iPhone's own captures differ in that area.
func (sc *Scenario) NotInheritedOnIOS() string {
	switch {
	case sc.ProxyAuth:
		return iosNoProxyAuth
	case sc.Proxy == ProxySOCKS4, sc.Proxy == ProxySOCKS4A:
		return "macOS Safari speaks SOCKS4 only through the Mac's SOCKS proxy setting as SOCKS4, which the iPhone has no counterpart of ([INFERENCE]; decision 0063): the phone's SOCKS4, if any, is not macOS Safari's"
	case sc.HTTP3() && sc != H3Discovery && sc != H3Page && sc != H3Blackhole && sc != H3Broken:
		return "macOS Safari's QUIC hellos differ from the iPhone's in the transport parameter 0xff080808 (27: only the iPhone sends it; 26: only macOS), and its ACK delay, so its HTTP/3 edges are not the iPhone's; not captured on the iPhone yet"
	case strings.HasPrefix(sc.Name, "replay-") || slices.Contains(iosCORSOrder, sc.Name):
		return "its CORS-mode requests carry macOS Safari's header order (sec-fetch-site, accept, origin), where the iPhone's put accept first (kinds, Safari 26 and 27); not captured on the iPhone yet"
	case sc == Connections:
		return "the iPhone's connection reuse is not macOS Safari's (27: uncredentialed requests on connections of their own, kinds, cors); not captured on the iPhone yet"
	}

	return ""
}

// iosCORSOrder are the scenarios whose macOS Safari 27 captures hold
// CORS-mode requests (origin and sec-fetch-site), in macOS Safari's order;
// the replays hold them too.
var iosCORSOrder = []string{
	"cors-edge",
	"cors-edge-cookies",
	"explicit-table",
	"headers-order",
	"huge-table",
	"kinds-http1",
	"large-settings",
	"nav-redirect-click",
	"nav-redirect-click-cookies",
	"nav-redirect-form",
	"nav-redirect-form-cookies",
	"nav-redirect-page",
	"nav-redirect-page-cookies",
	"proxy-socks5",
	"proxy-websocket",
	"proxy-websocket-socks5",
	"redirects2",
	"redirects2-cookies",
	"resume-tls12",
	"small-limits",
}

// GoesAway reports whether the server sends GOAWAY on the HTTP/2
// connection that carried a GET of path (with its query) on host.
func (sc *Scenario) GoesAway(host, path string) bool {
	return sc.answer(request{
		method: "GET",
		host:   host,
		path:   path,
	}).closeAfter
}

// GoesAwayH3 reports whether the server sends GOAWAY (then, a second
// later, CONNECTION_CLOSE) on the HTTP/3 connection that carried a GET of
// path (with its query) on host.
func (sc *Scenario) GoesAwayH3(host, path string) bool {
	r := sc.answer(request{
		method: "GET",
		host:   host,
		path:   path,
	})

	return r.closeAfter && !r.tcpOnly
}

func (sc *Scenario) answer(r request) response {
	r.ports = sc.ports
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

// settingsFor are the SETTINGS the scenario's HTTP/2 server advertises on
// a connection to host: Settings, and SETTINGS_ENABLE_CONNECT_PROTOCOL
// for its connectHosts.
func (sc *Scenario) settingsFor(host string) []http2.Setting {
	if !slices.Contains(sc.connectHosts, host) {
		return sc.Settings
	}

	return append(slices.Clone(sc.Settings), http2.Setting{
		ID:  http2.SettingEnableConnectProtocol,
		Val: 1,
	})
}

// settingFor is Setting on a connection to host (settingsFor).
func (sc *Scenario) settingFor(host string, id http2.SettingID) (uint32, bool) {
	for _, s := range sc.settingsFor(host) {
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
	// Connection: close; HTTP/2: GOAWAY; HTTP/3: GOAWAY, then
	// CONNECTION_CLOSE), so the next request needs a new one; tcpOnly
	// limits it to HTTP/1.1 and HTTP/2, and blackhole has no QUIC
	// connection started after an HTTP/3 close answered.
	closeAfter bool
	tcpOnly    bool
	blackhole  bool
	// silence, the page's first time over HTTP/3, answers nothing: from
	// the request on, the server drops every datagram of its connection
	// both ways, as if the path had died. Over TCP, or again, the request
	// is answered.
	silence bool
	// delay holds the response back, so a client's concurrent requests
	// stay in flight together.
	delay time.Duration
	// websocket accepts a WebSocket handshake: HTTP/1.1 answers 101, an
	// HTTP/2 extended CONNECT 200, and the socket runs as it says
	// (status, contentType and body are unused).
	websocket *wsAccept
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
// (sampled 2026-10-23), with synthetic values. The p19–p21 crumbs probe where
// Firefox and Safari stop never-indexing short cookies. All expire in 10
// minutes and /done deletes them, since Safari cannot be isolated.
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
