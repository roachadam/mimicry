# mimicry

[![Go Reference](https://pkg.go.dev/badge/github.com/roachadam/mimicry.svg)](https://pkg.go.dev/github.com/roachadam/mimicry)

mimicry is a Go HTTP client that sends requests as Chrome, Firefox or Safari
on macOS send them, matched byte for byte against captures of the real
browsers: TLS, HTTP/2, HTTP/3 over QUIC, the headers and their order for
each kind of request, cookies, redirects, CORS preflights, WebSockets and
proxies. It is an `http.RoundTripper` behind a plain `*http.Client`: you say
what kind of request it is and which page makes it, and the browser profile
supplies the rest.

## Install

```sh
go get github.com/roachadam/mimicry
```

Requires Go 1.27.3 or later (`go.mod`).

## Quickstart

Load a product page, call the shop's API from it with `fetch()`, then add
to cart with a JSON POST:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profiles"
)

func main() {
	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome(), // the newest built-in Chrome
	})
	if err != nil {
		log.Fatal(err)
	}
	client.Timeout = 30 * time.Second
	ctx := context.Background()

	// A request with no kind is a top-level navigation: a URL typed into
	// the address bar. Bodies come back decompressed.
	page, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.example/p/42", nil)
	resp, err := client.Do(page)
	if err != nil {
		log.Fatal(err)
	}
	resp.Body.Close()

	// fetch() from that page: Referer, Origin, sec-fetch-* and cookies
	// follow from the page the client last navigated to.
	api, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.example/api/stock/42", nil)
	resp, err = client.Do(mimicry.As(api, mimicry.Fetch))
	if err != nil {
		log.Fatal(err)
	}
	var stock struct{ InStock bool }
	err = json.NewDecoder(resp.Body).Decode(&stock)
	resp.Body.Close()
	if err != nil {
		log.Fatal(err)
	}

	// A JSON POST from the same page. Set only what the page's script
	// sets; the browser's own headers go around them, in its order.
	add, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.shop.example/api/cart",
		strings.NewReader(`{"sku":42,"qty":1}`))
	add.Header.Set("Content-Type", "application/json")
	add.Header.Set("X-CSRF-Token", "token-from-the-page")
	resp, err = client.Do(mimicry.As(add, mimicry.Fetch))
	if err != nil {
		log.Fatal(err)
	}
	resp.Body.Close()
	fmt.Println(stock.InStock, resp.Status)
}
```

Other kinds (`Script`, `Style`, `Image`, `Font`, `XHR`, `Beacon` and more)
work the same way; `mimicry.With` takes a `mimicry.Request` for the rest:
the page making the request (`From`, say a form POST from the cart page),
credentials mode, `crossorigin`, `fetchpriority`. Runnable versions against
a local stand-in shop: `ExampleNewClient`, `ExampleAs`, `ExampleAs_addToCart`
and `ExampleWith` (checkout and its 303) in [`example_test.go`](example_test.go).

## Common tasks

Snippets assume a `client` from `NewClient` and a `ctx`; examples named are
in [`example_test.go`](example_test.go) unless stated.

**Cookies.** `NewClient` gives the Transport a jar that follows the
profile's cookie rules (SameSite, partitions, prefixes, order). Seed and
save sessions as Cookie header strings:

```go
jar := client.Transport.(*mimicry.Transport).Cookies()
// For shop.example and every name under it; then for one host only.
if err := jar.SetString("shop.example", "session=s1; cart=c9"); err != nil {
	log.Fatal(err)
}
if err := jar.SetHostString("checkout.shop.example", "step=2"); err != nil {
	log.Fatal(err)
}
saved := jar.String("shop.example") // later: save the session
```

`String` loses paths, expiry and attributes; `WriteJSON`/`ReadJSON` keep
them. `jar.SetString(cookies.AnySite, "debug=1")` sends a cookie to every
site, which no browser does: every host the jar talks to receives it.
Transports can share a jar through `Config.Cookies`. Examples:
`ExampleConfig_cookies`, `ExampleConfig_sharedJar`; `ExampleJar_SetString`,
`ExampleJar_SetHostString`, `ExampleJar_SetString_anySite` in
[`cookies/example_test.go`](cookies/example_test.go). See
[docs/concepts/cookies.md](docs/concepts/cookies.md).

**Proxies and rotation.** `Config.Proxy` is a function of the request, as
in `http.Transport`: `http`, `socks5` and `socks5h` proxies, credentials in
the URL sent as the browser sends them. A failure that is the proxy's, not
the site's, is a `*mimicry.ProxyError`:

```go
var current atomic.Int64 // index into proxies, your []*url.URL
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome(),
	Proxy: func(*http.Request) (*url.URL, error) {
		return proxies[current.Load()%int64(len(proxies))], nil
	},
})
// ...
resp, err := client.Do(req)
var pe *mimicry.ProxyError
if errors.As(err, &pe) {
	current.Add(1) // unreachable, refused the tunnel or the credentials: next proxy
}
```

Cookies and the last page stay the Transport's; each proxy has its own
connections. Requests through a proxy stay on TCP, as in the browser;
`Config.HTTP3OverSOCKS5` lets SOCKS5 proxies carry HTTP/3 (a UDP
association per QUIC connection), and no datagram ever goes around the
proxy. Example: `ExampleConfig_proxy`; see
[docs/concepts/proxies.md](docs/concepts/proxies.md).

**Certificate pins.** `go run github.com/roachadam/mimicry/cmd/pins
https://www.shop.example/` (or `mimicry.FetchPins`) prints a site's chain
with its pins and marks the intermediate to pin; add a backup key:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome(),
	Pins: []mimicry.Pin{
		{
			Host:              "shop.example",
			IncludeSubdomains: true,
			SPKI:              []string{"sha256/<intermediate>", "sha256/<backup>"},
		},
	},
})
```

A connection matching no pin fails with a `*mimicry.PinError` before
anything is sent; `Config.VerifyConnection` adds your own check on every
handshake, resumed ones included. Example: `ExampleConfig_pins`.

**Your own headers and where they go.** A request's own headers go where
the browser puts headers a page sets; `Config.Headers` go on every request.
To move one header and keep the browser's order for the rest, use a `Place`,
for every request or for one:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile:         profiles.Chrome(),
	Headers:         http.Header{"X-Api-Key": {"k3y"}},
	HeaderPlacement: []mimicry.Place{{Header: "X-Api-Key", After: "User-Agent"}},
})
// ...
resp, err := client.Do(mimicry.With(req, mimicry.Request{
	Kind:            mimicry.Fetch,
	HeaderPlacement: []mimicry.Place{{Header: "X-CSRF-Token", Before: "Accept"}},
}))
```

A `Place` whose header or anchor the request does not send fails with
`mimicry.ErrPlacement`, and nothing is sent. `req.Header[mimicry.HeaderOrderKey]`
(as in fhttp) replaces the whole order instead, and profile headers it does
not name are not sent. A header set to nil (`req.Header["Referer"] = nil`)
suppresses the profile's; `mimicry.Exact(req)` sends only your own.
Examples: `ExampleConfig_headerPlacement`, `ExampleHeaderOrderKey`; see
[docs/concepts/how-a-request-is-built.md](docs/concepts/how-a-request-is-built.md).

**Seeing what will be sent.** `Transport.Plan` works out a request's header
lines for each protocol, the protocol it would take, its CORS preflight and
the headers you set in place of the browser's, without sending anything or
changing any state:

```go
plan, err := client.Transport.(*mimicry.Transport).Plan(mimicry.As(api, mimicry.Fetch))
if err != nil {
	log.Fatal(err) // what RoundTrip would fail with: ErrNotMeasured, ErrPlacement, ...
}
fmt.Println(plan) // kind, variant, protocol, overrides, preflight, lines
for _, o := range plan.Overrides {
	fmt.Printf("%s: you replaced %q with %q\n", o.Name, o.Mimicry, o.Caller)
}
```

`Overrides` is where header literals copied from older code show up.
Example: `ExampleTransport_Plan`.

**WebSockets.** `DialWebSocket` opens a socket from the Transport's last
page as the browser does: handshake, cookies, compression and framing, as a
stream on the host's HTTP/2 connection where the server allows it.

```go
tr := client.Transport.(*mimicry.Transport)
ws, _, err := tr.DialWebSocket(ctx, "wss://www.shop.example/live", mimicry.WebSocketOptions{})
if err != nil {
	log.Fatal(err)
}
defer ws.Close(1000, "")
if err := ws.WriteMessage(ctx, mimicry.TextMessage, []byte(`{"subscribe":"queue"}`)); err != nil {
	log.Fatal(err)
}
_, msg, err := ws.ReadMessage(ctx)
```

Example: `ExampleTransport_DialWebSocket`; see
[docs/concepts/websockets.md](docs/concepts/websockets.md).

**HTTP/3.** On by default: once a site advertises it (`Alt-Svc`), requests
move to HTTP/3 when and how the profile's browser moves them. Share one
`mimicry.NewAltSvcCache()` between Transports through `Config.AltSvc`, and
save it with `WriteJSON`, so a returning Transport starts a known site on
HTTP/3 as a restarted browser does (`ExampleAltSvcCache`). Where QUIC
fails, requests fall back to TCP sooner than the browser's 4–5 s holds;
`Config.BrowserQUICFallback` keeps the browser's timing. On networks
that block UDP, `mimicry.Config{Profile: profiles.Firefox(), DisableHTTP3: true}`
keeps everything on TCP. Example: `ExampleConfig_disableHTTP3`; see
[docs/concepts/http3.md](docs/concepts/http3.md).

**`ErrNotMeasured`.** A profile describes only what its browser was captured
doing. Ask for anything else (an `<img fetchpriority="high">`, a request
kind or proxy setup the profile lacks) and the request fails with an error
wrapping `mimicry.ErrNotMeasured` that names what is missing; nothing is
sent. mimicry refuses rather than guesses because a guessed header order or
value is a request no real browser sends, which is what detection looks
for. An error tells you; a guess would not.

```go
hero := mimicry.Request{Kind: mimicry.Image, Priority: mimicry.PriorityHigh}
resp, err := client.Do(mimicry.With(req, hero))
if errors.Is(err, mimicry.ErrNotMeasured) {
	hero.Priority = mimicry.PriorityAuto // drop what was not measured
	resp, err = client.Do(mimicry.With(req, hero))
}
```

Example: `ExampleErrNotMeasured`.

## Supported browsers and versions

| Profile (`profiles.Names()`) | Accessor | Status |
|---|---|---|
| `chrome-156-macos` | `profiles.Chrome()`, `profiles.Chrome156MacOS()` | supported |
| `chrome-155-macos` | `profiles.Chrome155MacOS()` | supported |
| `firefox-158-macos` | `profiles.Firefox()`, `profiles.Firefox158MacOS()` | supported |
| `safari-27-macos` | `profiles.Safari()`, `profiles.Safari27MacOS()` | supported |
| `safari-26-macos` | `profiles.Safari26MacOS()` | supported |

`profiles.Chrome()`, `Firefox()` and `Safari()` return the newest built-in
version and move to each new one as it is added; a versioned accessor or
`profiles.ByName("chrome-155-macos")` stays put. The newest five versions of
each browser on each platform are supported (`profiles.Supported()`); older
ones stay in the module, deprecated and no longer refreshed
([decision 0035](docs/decisions/0035-version-policy-and-capture-inheritance.md)).
None is deprecated yet. Chrome 156 is a quick refresh: its fingerprint layer
(TLS, HTTP/2, HTTP/3, every request kind's headers, WebSockets) is captured
from Chrome 156, the rest checked against Chrome 155's captures
([EVIDENCE.md](profiles/EVIDENCE.md)).

## What mimicry does beyond your requests

As a browser does, mimicry adds traffic around your requests: redirect hops
(followed by `http.Client`, sent as the browser sends them, up to its
limit), CORS preflights before the cross-origin `fetch()` and XHR calls
that need one (a refused one is a `*mimicry.CORSError`, and the request is
not sent), and connection-level traffic: reuse and HTTP/2 coalescing,
retries on another connection, QUIC attempts and their TCP races, TLS
resumption and 0-RTT, PING, WINDOW_UPDATE and ACK frames, proxy CONNECTs
and credential retries.

It never fetches a page's subresources or parses HTML, runs scripts (so
analytics, beacons and sensor data go only if you send them), preconnects
or prefetches, upgrades `http://` to `https://` or applies HSTS, caches
responses, looks up DNS HTTPS records, or opens a WebSocket you did not ask
for. [docs/concepts/what-mimicry-does.md](docs/concepts/what-mimicry-does.md)
lists each behaviour, why the browser does it, and the option that changes it.

## Deliberate deviations from the browser, and how to turn them off

With no options, mimicry sends what the browser sends, with one
exception, where the browser's behaviour costs time a user notices and
the difference is unlikely to be checked by anti-bot systems; a `Config`
field gives the browser's behaviour back:

| Deviation | What a server can see | The browser's behaviour |
|---|---|---|
| **Shorter waits when QUIC fails.** Requests go on TCP after the race delay instead of waiting 4–5 s on a QUIC connection the server never answers ([decision 0036](docs/decisions/0036-fast-quic-fallback.md)). | only on networks or servers where QUIC fails: an earlier TCP connection | `Config.BrowserQUICFallback` |

Three options go the other way, off by default because they do what no
browser does; each trades fidelity for speed or reach:

| Deviation, when you turn it on | What a server can see | Option |
|---|---|---|
| **No HTTP/2 coalescing**: each host gets its own connection, where browsers share one between hosts whose certificate and address match. Set it on lossy networks: one connection for a site's hosts shares one congestion window, and warm page loads at 1% loss took half as long without it. On clean networks coalescing is faster (it saves the other hosts' handshakes: 0.36 s of a cold load at 80 ms), so leave it off there. Requests through a proxy never coalesce anyway ([decision 0037](docs/decisions/0037-coalescing-opt-out.md)). | a server hosting several of a site's hosts at one address under one certificate sees a connection per host where Chrome, Firefox and Safari end with one (low to medium risk) | `Config.DisableCoalescing` |
| **More than 6 HTTP/1.1 connections per host**, for faster bulk requests to an HTTP/1.1-only server (at 64 concurrent requests, 16 connections gave 1.5–2.1× the requests/s, 64 gave 1.7–2.6×; decision 0037). | more than 6 concurrent HTTP/1.1 connections from one client to one host | `Config.MaxConnsPerHost` |
| **HTTP/3 through SOCKS5 proxies**, over a UDP association per QUIC connection (RFC 1928), for sites that serve HTTP/3 reached from the proxy's address. Many SOCKS5 proxies relay no UDP: one that refuses is asked once, then everything goes on TCP. No datagram ever goes around the proxy ([decision 0038](docs/decisions/0038-http3-over-socks5.md)). | HTTP/3 from a proxy's address, which browsers never send through a proxy; the QUIC itself is the profile's, unchanged | `Config.HTTP3OverSOCKS5` |

## Evidence and limits

Each profile is one complete JSON file in `profiles/data/`, derived from
captures of the real browser: the `internal/harness/` module drives Chrome, Firefox
and Safari on macOS against its own capture server, usually three samples
per scenario. Its tests replay the captures through mimicry and fail on any
difference not pinned as a known gap: the ClientHello, HTTP/2 and HTTP/3
frames, every request kind's header lines, HPACK and QPACK blocks re-encoded
byte for byte, cookies, CORS, redirects, connections, proxies, WebSockets,
QUIC. [`profiles/EVIDENCE.md`](profiles/EVIDENCE.md) lists every claim a
profile makes, its evidence and the test that keeps it true;
[`docs/backlog.md`](docs/backlog.md) lists every known difference from the
browsers and how likely it is to be detected.

Limits that matter in practice:

- **macOS profiles only.** No Windows, Linux, iOS or Android profiles.
- **The TCP/IP fingerprint is your host's.** The SYN (TTL, window size, TCP
  options) comes from the operating system mimicry runs on: a macOS profile
  sent from Linux has a Linux SYN, which p0f-style checks compare with the
  User-Agent. Through a proxy the server sees the proxy's.
- **One Transport per browsing session, and per site.** A Transport is one
  browser tab: requests of other kinds come from its last navigation unless
  they set `Request.From`, so parallel tasks need a Transport (or
  `NewClient`) each. Browsers also partition connections by top-level site
  and mimicry does not, so give each site its own Transport; they can share
  a jar (`ExampleConfig_sharedJar`).
- **HTTP/3 ignores `Config.DialContext`.** QUIC sends UDP to the address
  `Config.Resolver` gives; `DialContext` dials TCP only.
- **HTTP/3 never goes through a proxy**, as in the browsers: proxied
  requests use HTTP/2 or HTTP/1.1. `https://` proxies (TLS to the proxy) are
  not supported.

## Coming from tls-client

With tls-client you write each request's browser headers and their order
yourself; with mimicry you say what kind of request it is and which page
makes it, and most of a port is deleting header code.
[docs/concepts/coming-from-tls-client.md](docs/concepts/coming-from-tls-client.md)
maps tls-client's options, headers, cookies, redirects and proxy rotation
onto mimicry, and says where mimicry is weaker.

## Development

[`AGENTS.md`](AGENTS.md) has the conventions and commands
(`moon run :test :vet :lint`). `internal/harness/` is a separate module that captures
the browsers and checks the profiles against them
([docs/capturing.md](docs/capturing.md)); its `go run ./cmd/refresh`
brings a profile up to the installed browser version
([docs/concepts/refreshing-profiles.md](docs/concepts/refreshing-profiles.md)).
[`docs/README.md`](docs/README.md) indexes the decisions and concept docs.

## Licence

MIT, see [`LICENSE`](LICENSE). Code bundled from other projects and the Go
dependencies are listed, with their licences, in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).
