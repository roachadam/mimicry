# Coming from tls-client

For users of [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client)
(and its forks) with `bogdanfinn/fhttp`: how the usual patterns of an
automation codebase map onto mimicry, what gets easier, and what is
missing or awkward. The runnable versions of the mimicry snippets are the
package examples (`example_test.go`, `cookies/example_test.go`).

The short version: with tls-client you describe the browser yourself on
every request (header literals, `HeaderOrderKey`, `sec-fetch-*`), and the
library supplies the TLS and HTTP/2 fingerprint. With mimicry you say
what kind of request it is and which page makes it, and the profile
supplies the headers, their order and values, the cookies, redirects,
preflights, connection reuse and HTTP/3, all as the browser does them.
Most of the porting work is deleting header code.

## Building a client

tls-client:

```go
jar := tls_client.NewCookieJar()
client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
	tls_client.WithClientProfile(profiles.Chrome_133),
	tls_client.WithRandomTLSExtensionOrder(),
	tls_client.WithTimeoutSeconds(30),
	tls_client.WithNotFollowRedirects(),
	tls_client.WithProxyUrl("http://user:pass@proxy.example:8080"),
	tls_client.WithCookieJar(jar),
)
```

mimicry:

```go
proxyURL, _ := url.Parse("http://user:pass@proxy.example:8080")
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome155MacOS(),
	Proxy:   http.ProxyURL(proxyURL),
})
client.Timeout = 30 * time.Second
client.CheckRedirect = func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse // only if you really want the 3xx itself
}
```

| tls-client | mimicry |
|---|---|
| `WithClientProfile(profiles.Chrome_133)` | `Config.Profile` (`profiles.Chrome155MacOS()`, `profiles.ByName("chrome-155-macos")`, or your own JSON through `profile.Parse`) |
| `WithRandomTLSExtensionOrder()` | nothing: the profile shuffles where its browser does (Chrome) and keeps the order where it does not (Safari, Firefox over TCP) |
| `WithTimeoutSeconds(n)` | `http.Client.Timeout`, or the request's context; `Config.DialTimeout` for connecting |
| `WithNotFollowRedirects()`, `SetFollowRedirect` | `http.Client.CheckRedirect` returning `http.ErrUseLastResponse` |
| `WithCustomRedirectFunc(f)` | `http.Client.CheckRedirect` |
| `WithCookieJar(jar)`, `SetCookieJar` | `Config.Cookies` (a `*cookies.Jar`); not the client's `Jar`. `Transport.Cookies()` returns `NewClient`'s own |
| `WithProxyUrl`, `SetProxy` | `Config.Proxy`, a function of the request |
| `WithDisableHttp3()`, `WithProtocolRacing()` | `Config.DisableHTTP3`; racing is the profile's |
| `WithDialer`, `WithDialContext`, `WithLocalAddr` | `Config.DialContext` (TCP only: QUIC does not use it) |
| `WithDefaultHeaders(h)` | `Config.Headers` |
| `WithInsecureSkipVerify()`, `WithCharlesProxy` | `Config.RootCAs` holding the intercepting proxy's CA, plus `Config.Proxy` |
| `WithCertificatePinning`, `WithCertificatePinner` | `Config.Pins` (`sha256/<base64>` pins as tls-client takes them, hex, or certificate fingerprints; `FetchPins` or `go run github.com/roachadam/mimicry/cmd/pins URL` lists a site's), `Config.VerifyConnection` for your own check; `resp.TLS` on every response |
| `WithForceHttp1()` | none |
| `WithServerNameOverwrite`, `WithDisableIPV4/6`, `WithConnectHeaders` | none (`DialContext` can choose the address family) |
| `WithPreHook`, `WithPostHook`, `WithDebug`, `WithBandwidthTracker` | none in mimicry; wrap the `http.RoundTripper`, or `cookies.Config.OnSetCookie` for cookies |
| `CloseIdleConnections()` | `Transport.CloseIdleConnections()` (`client.CloseIdleConnections()`) |

`NewClient` returns a plain `*http.Client`, so anything that takes one
(an SDK, a login library) works unchanged; with tls-client such code has
to accept tls-client's own interface.

## Headers: the biggest change

A tls-client codebase spells out each request's browser headers and their
order, often in a per-site helper:

```go
req, _ := http.NewRequest(http.MethodGet, "https://www.shop.example/p/42", nil)
req.Header = http.Header{
	"sec-ch-ua":                 {`"Chromium";v="133", ...`},
	"sec-ch-ua-mobile":          {"?0"},
	"sec-ch-ua-platform":        {`"macOS"`},
	"upgrade-insecure-requests": {"1"},
	"user-agent":                {"Mozilla/5.0 ... Chrome/133.0.0.0 Safari/537.36"},
	"accept":                    {"text/html,application/xhtml+xml,..."},
	"sec-fetch-site":            {"none"},
	"sec-fetch-mode":            {"navigate"},
	"sec-fetch-user":            {"?1"},
	"sec-fetch-dest":            {"document"},
	"accept-encoding":           {"gzip, deflate, br, zstd"},
	"accept-language":           {"en-US,en;q=0.9"},
	http.HeaderOrderKey: {"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
		"upgrade-insecure-requests", "user-agent", "accept", "sec-fetch-site",
		"sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "accept-encoding",
		"accept-language", "cookie"},
}
```

and a second literal for API calls (`accept: application/json`,
`sec-fetch-mode: cors`, `sec-fetch-dest: empty`, `origin`, `referer`),
kept in step with the first by hand. Values drift: a user agent from one
Chrome version over another's TLS, `sec-fetch-user` on an XHR, an Accept
from a navigation on a `cors` request.

In mimicry the same navigation is:

```go
req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.example/p/42", nil)
resp, err := client.Do(req)
```

and an API call from that page:

```go
api, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.example/api/stock/42", nil)
api.Header.Set("X-CSRF-Token", token) // only what the page's own script sets
resp, err := client.Do(mimicry.As(api, mimicry.Fetch))
```

The profile picks the header list for the kind (`Navigate`, `Fetch`,
`XHR`, `Script`, `Image`, `Beacon` and the rest), the variant for the
method and context (a form POST, a redirect hop, a cross-site request),
and fills in what the page decides: `sec-fetch-site`, `Origin`, `Referer`
(strict-origin-when-cross-origin, hop by hop), the credentials and the
cookies. Headers the page sets go where the browser puts them (in the
browser's own order for page-set headers). A cross-origin fetch that needs a
CORS preflight gets one. See [how a request is built](how-a-request-is-built.md).

Porting traps, each of which silently makes the request less like the
browser's:

- **Do not copy the old header literals.** A header the request sets wins
  over the profile's computed one, so a ported `sec-fetch-site`, `Origin`,
  `Referer`, `Accept` or `sec-ch-ua` replaces the right value with a
  hand-written one, and nothing reports it.
- **Do not copy `HeaderOrderKey` lists.** mimicry's `HeaderOrderKey`
  replaces the order outright: profile headers it does not name are not
  sent, and request headers it does not name go last (over HTTP/1.1 that
  includes `Host` and `Connection`). tls-client users set it on every
  request; in mimicry it is an escape hatch. To move one header (an API
  key a server expects after `User-Agent`), use a `mimicry.Place` in
  `Config.HeaderPlacement` (every request) or `Request.HeaderPlacement`
  (one): the rest stays the browser's order, and a header or anchor the
  request does not send is an error (`ErrPlacement`), not a silent
  change. A request with `HeaderOrderKey` takes no `Config` placements
  (`ExampleConfig_headerPlacement`, [0030](../decisions/0030-header-placement.md)).
- **Do not set `Accept-Encoding`.** Setting it yourself turns off
  decompression (you asked for the raw body); tls-client codebases set it
  on every request.
- **Overriding `User-Agent`** (in `Config.Headers` or on the request)
  leaves `sec-ch-ua`, the TLS hello and HTTP/2 settings the profile's.
  Pick the profile whose browser you want to be instead.
- **A POST navigation needs its page.** A form POST is
  `mimicry.With(req, mimicry.Request{From: formPage})`; without `From` a
  navigation is a typed URL, which is GET or HEAD only.

## Navigations, fetches and the page

tls-client has no notion of a page; mimicry's Transport remembers its last
navigation, and every other kind comes from it unless `Request.From` says
otherwise. So the order of calls matters the way it does in a browser:
load the product page, then call its API. Calling the API cold sends it as
a fetch from its own origin.

That state is per Transport, so a Transport is one tab: parallel tasks
each use their own (`NewClient` per task), or set `From` on every request.

## Redirects

tls-client codebases usually turn redirects off and read `Location`
themselves, because following them with tls-client's defaults changes the
headers. In mimicry redirects are followed as the browser follows them:
each hop with its own `sec-fetch-site`, `Origin` and `Referer`, cookies
stored and sent per hop, 303 and POST-301/302 to GET, PUT/DELETE/PATCH
kept on 301/302, the browser's redirect limit, CORS checks on fetch hops.
The chain is on the response:

```go
resp, err := client.Do(mimicry.With(checkout, mimicry.Request{From: cartURL}))
for r := resp.Request; r.Response != nil; r = r.Response.Request {
	fmt.Println(r.Response.Request.Method, r.Response.Request.URL, "→", r.Response.Status)
}
```

Turning following off (`CheckRedirect` returning `http.ErrUseLastResponse`)
still works, but then the next request you make for the `Location` is
your own navigation, not the browser's redirect hop.

## Cookies

| tls-client | mimicry |
|---|---|
| `client.GetCookies(u)` | `jar.String(site)`: the site's cookies as a header string; `jar.Cookies(cookies.Request{URL: u, Method: "GET", Navigation: true})`: exactly what the browser would send to `u`; `jar.All()` for everything |
| `client.SetCookies(u, []*http.Cookie{...})` | `jar.SetHTTPCookies(u, cs)`, through the browser's rules as a response from `u`, with an error naming each cookie refused; `jar.Set(cookies.Cookie{...})` stores one as given; `jar.SetCookies(req, rawSetCookieLines)` for lines a response carried |
| a `Cookie` header string kept in a database | `jar.SetString(site, header)` (or `cookies.FromString`) in, `jar.String(site)` out (see `ExampleJar_SetString`) |
| a new jar to clear | `jar.Clear()`, `jar.ClearSession()`, `jar.Delete(func(*cookies.Cookie) bool)` |
| `SetCookieJar(shared)` on another client | the same `*cookies.Jar` in each `Config.Cookies` |
| persistence: your own serialisation | `jar.WriteJSON`/`ReadJSON` (every attribute), `WriteNetscape`/`ReadNetscape` (curl's cookies.txt) |

What mimicry adds: the cookies go as the profile's browser sends them
(SameSite with its defaults, third-party and partitioned cookies,
prefixes, order, the per-site limit, values byte for byte), on redirect
hops too; the jar is safe for concurrent use and can be shared across
Transports, which is the way to visit several sites with one identity
(one Transport per site, one jar).

The jar `NewClient` made is `client.Transport.(*mimicry.Transport).Cookies()`.
`SetString` makes each pair a cookie of the whole site (every host, path
`/`, not `Secure`, the profile's SameSite default), since a header string
carries no scope; cookies whose host, path or attributes matter go in
with `Set`, `SetHTTPCookies` or `ReadJSON`. One cookie for every site is
not something a browser has, so it takes `cookies.AnySite` as the site,
written out in your code ([decision 0031](../decisions/0031-cookies-for-any-site.md)):
every host the Transport talks to receives it, trackers included.
`jar.CookieJar()` exists for code that wants an `http.CookieJar`, but is
lossy and treats every request as a typed navigation.

## Proxies and rotation

A tls-client codebase rotates either with `SetProxy` (which rebuilds the
transport) or, more often, by building a new client with the next proxy
and carrying the cookies over. In mimicry `Config.Proxy` is consulted for
every request, so rotation is changing what it returns:

```go
var current atomic.Pointer[url.URL]
current.Store(proxies.Next())
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome155MacOS(),
	Proxy: func(*http.Request) (*url.URL, error) {
		return current.Load(), nil
	},
})
// on a ban:
current.Store(proxies.Next())
```

Each proxy gets its own connections and TLS sessions; the jar and the last
page stay. A per-request choice is the same function reading the request
(its context, its host). Credentials go as the browser sends them (Chrome
and Firefox wait for the 407, then send them unasked; Safari waits every
time), and the CONNECT lines and SOCKS5 bytes are the browser's.

Limits: `http`, `socks5` and `socks5h` proxies only (no `https://`
proxies), Basic authentication only, no WebSocket through a proxy
(`ErrNotMeasured`), and never HTTP/3 through a proxy.

A proxy that fails a request returns a `*mimicry.ProxyError`: unreachable,
CONNECT refused (`StatusCode`, `Header`), credentials rejected after the
browser's flow (407), a SOCKS5 refusal, or too slow (`errors.Is(err,
context.DeadlineExceeded)` still holds). Its `Proxy` has the password
removed, and so has its text. That is the rotation signal, as against the
site refusing the request (see `ExampleConfig_proxy`):

```go
resp, err := client.Do(req)
var pe *mimicry.ProxyError
if errors.As(err, &pe) {
	current.Store(proxies.Next())
}
```

## Bodies, errors and retries

The response is a standard `*http.Response`: read and close the body (a
fork that pre-reads the body into a string needs a small helper). It is
decompressed as the browser's `Accept-Encoding` asks (gzip, deflate,
Brotli, zstd), unless you set `Accept-Encoding` yourself.

Errors are returned, not encoded as a status code. Worth testing for:

- timeouts: a `net.Error` whose `Timeout()` is true for
  `http.Client.Timeout`, the context's error for a context deadline;
- `errors.Is(err, mimicry.ErrNotMeasured)`: the profile cannot describe
  the request (nothing was sent); a programming or profile-choice error,
  not something to retry;
- `errors.As(err, &corsErr)` with a `*mimicry.CORSError`: the browser
  would not have sent it (a refused preflight or redirect);
- `errors.As(err, &proxyErr)` with a `*mimicry.ProxyError`: the proxy
  failed, not the site (see above);
- `errors.As(err, &pinErr)` with a `*mimicry.PinError`: the host's
  certificate chain matched none of `Config.Pins` (nothing was sent).

mimicry retries on another connection only where the browser would (a
request nothing of which was sent, an idempotent one on a connection the
server closed); application-level retries (403, 429, challenge pages) stay
yours.

## WebSockets

tls-client codebases use a separate WebSocket library with its own TLS
stack (or tls-client's dialer). mimicry opens sockets through the same
Transport, from the page, with the browser's handshake, cookies, hello and
framing:

```go
tr := client.Transport.(*mimicry.Transport)
ws, _, err := tr.DialWebSocket(ctx, "wss://www.shop.example/live", mimicry.WebSocketOptions{})
```

## Where mimicry is weaker, ranked for retail automation

1. **The profile catalogue.** Four desktop profiles, all macOS (Chrome
   155, Firefox 158, Safari 26 and 27): no iOS Safari, no Android Chrome,
   no Windows, no older or newer versions. A site whose mobile endpoints
   or app traffic you imitate, or a session tied to a user agent minted
   elsewhere (a solver, a harvested browser), needs a matching profile,
   and making one takes the harness and a real browser capture.
   `profile.Parse` loads your own JSON, but nothing helps you write it.
2. **Proxy limits.** `https://` proxies are unsupported, and a WebSocket
   through a proxy is `ErrNotMeasured`.
3. **No way to see what will be sent.** tls-client users can print their
   own header literal; in mimicry the request's final header lines, its
   variant and whether it will be preflighted exist only on the wire. Add
   the porting traps above (a copied header silently replaces a computed
   one) and the first weeks of porting are guesswork against a capture
   proxy.
4. **Cookie scope from a string is the whole site.** `SetString` seeds a
   site-wide, non-`Secure` session cookie per pair, so a source browser's
   host-only, path-scoped or `Secure` cookies need `Set` or JSON to keep
   their scope, and asking what goes to one URL still takes a
   `cookies.Request`.
5. **Runtime `ErrNotMeasured` that depends on the server.** A variant
   measured over HTTP/2 only fails on a server that speaks only
   HTTP/1.1; you find out from the request, not up front.
6. **Verbosity in small places.** `NewRequestWithContext` plus `As`/`With`
   on every call, and the type assertion to reach `NewClient`'s Transport
   (`DialWebSocket`, `Cookies`). (Moving one header is a `Place` in
   `HeaderPlacement`, not a full `HeaderOrderKey` list.)
7. **Missing switches** that browsers do not have either: forcing
   HTTP/1.1, skipping verification, overriding the SNI, binding a local
   address for QUIC.

## Proposed adjustments

Candidates, not decisions; each would need a decision record.

1. **A request plan you can inspect.** `Transport.Plan(req) (*Plan, error)`
   returning what `RoundTrip` would send without sending it: the ordered
   header lines for HTTP/2 and HTTP/1.1, the kind's variant, the
   preflight if any, and `Overrides`, the computed headers the request's
   own replaced. It answers "why does this differ from my capture", flags
   ported header literals, and finds `ErrNotMeasured` up front. A
   `Config.OnRequest` hook with the same plan would cover logging.
2. **More profiles, and a way to make them.** iOS Safari and Android
   Chrome first, then Windows Chrome; the planned `profilegen derive` to
   start a new version from the last, and documentation of what a user
   capture needs to become a profile.
