# Coming from tls-client

This page is for users of [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client)
and its forks. It shows how the usual patterns map onto mimicry, what gets
easier, and what is missing. Runnable versions of the mimicry snippets are
in `example_test.go` and `cookies/example_test.go`.

The short version: with tls-client you describe the browser yourself on
every request. You write the header values, `HeaderOrderKey` and the
`sec-fetch-*` headers; the library supplies the TLS and HTTP/2
fingerprint. With mimicry you say what kind of request it is and which
page makes it. The profile, a recorded description of one browser
version, supplies everything else as the browser does it. Most of the
porting work is deleting header code.

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
	Profile: profiles.Chrome(),
	Proxy:   http.ProxyURL(proxyURL),
})
client.CheckRedirect = func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse // only if you really want the 3xx itself
}
```

| tls-client | mimicry |
|---|---|
| `WithClientProfile(profiles.Chrome_133)` | `Config.Profile`: `profiles.Chrome()` for the newest, `profiles.ByName("chrome-155-macos")` for a fixed version, `profile.Parse` for your own JSON |
| `WithRandomTLSExtensionOrder()` | nothing: the profile shuffles only where its browser does |
| `WithTimeoutSeconds(n)` | `Config.Timeout` (30 s by default; negative for none) or the request's context; `Config.DialTimeout` for connecting |
| `WithNotFollowRedirects()`, `SetFollowRedirect` | `CheckRedirect` returning `http.ErrUseLastResponse` |
| `WithCustomRedirectFunc(f)` | `http.Client.CheckRedirect` |
| `WithCookieJar(jar)`, `SetCookieJar` | `Config.Cookies` (a `*cookies.Jar`), not the client's `Jar` |
| `WithProxyUrl`, `SetProxy` | `Config.Proxy`, a function of the request |
| `WithDisableHttp3()`, `WithProtocolRacing()` | `Config.DisableHTTP3`; racing follows the profile |
| `WithDialer`, `WithDialContext`, `WithLocalAddr` | `Config.DialContext` (TCP only) |
| `WithDefaultHeaders(h)` | `Config.Headers` |
| `WithInsecureSkipVerify()`, `WithCharlesProxy` | `Config.RootCAs` with the intercepting proxy's CA, plus `Config.Proxy` |
| `WithCertificatePinning`, `WithCertificatePinner` | `Config.Pins` (same `sha256/<base64>` format); `Config.VerifyConnection` for your own check |
| `WithForceHttp1()` | none |
| `WithServerNameOverwrite`, `WithDisableIPV4/6`, `WithConnectHeaders` | none (`DialContext` can pick the address family) |
| `WithPreHook`, `WithPostHook`, `WithDebug`, `WithBandwidthTracker` | `Transport.Plan(req)` to preview a request; otherwise wrap the `http.RoundTripper` |
| `CloseIdleConnections()` | `client.CloseIdleConnections()`; `Config.IdleConnTimeout` |
| a new client per proxy | `Config.MaxIdleProxies` |
| `TransportOptions{DisableCompression: true}` | `Config.DisableDecompression` |

To list a site's pins, run `go run github.com/roachadam/mimicry/cmd/pins URL`
or call `FetchPins`.

`NewClient` returns a plain `*http.Client`. Anything that takes one, such
as an SDK or a login library, works unchanged. With tls-client such code
has to accept tls-client's own interface.

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
browser's (`Transport.Plan` lists the first two: [below](#seeing-what-will-be-sent)):

- **Do not copy the old header literals.** A header the request sets wins
  over the profile's computed one, so a ported `sec-fetch-site`, `Origin`,
  `Referer`, `Accept` or `sec-ch-ua` replaces the right value with a
  hand-written one; `Plan.Overrides` names each.
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
  (see `ExampleConfig_headerPlacement`).
- **Overriding `User-Agent`** (in `Config.Headers` or on the request)
  leaves `sec-ch-ua`, the TLS hello and HTTP/2 settings the profile's.
  Pick the profile whose browser you want to be instead.
- **A POST navigation needs its page.** A form POST is
  `mimicry.With(req, mimicry.Request{From: formPage})`; without `From` a
  navigation is a typed URL, which is GET or HEAD only.

## Seeing what will be sent

`Transport.Plan(req)` shows everything a request would send, without
sending it. It also names the headers you set over the browser's values
(`Plan.Overrides`) and the profile headers a `HeaderOrderKey` drops. See
[how a request is built](how-a-request-is-built.md) for the details.

```go
tr := client.Transport.(*mimicry.Transport)
plan, err := tr.Plan(mimicry.As(api, mimicry.Fetch))
if err != nil {
	return err // RoundTrip would fail the same way
}
for _, o := range plan.Overrides {
	log.Printf("%s: the browser sends %q, this request %q", o.Name, o.Mimicry, o.Caller)
}
```

## Navigations, fetches and the page

tls-client has no notion of a page. mimicry's Transport remembers its last
navigation, and every other request comes from that page unless
`Request.From` says otherwise. So the order of calls matters as it does
in a browser: load the product page, then call its API. Calling the API
cold sends it as a fetch from its own origin.

That state is per Transport, so a Transport is one tab. Parallel tasks
each use their own client, or set `From` on every request.

## Redirects

tls-client codebases usually turn redirects off and follow `Location`
themselves. mimicry follows redirects as the browser does, so you can
leave them on. See [redirects](what-mimicry-does.md#redirects). If you
turn following off, your next request for the `Location` is a new
navigation, not the browser's redirect hop.

## Cookies

| tls-client | mimicry |
|---|---|
| `client.GetCookies(u)` | `jar.String(domain)` as a header string; `jar.Cookies(cookies.Request{...})` for exactly what the browser would send to a URL; `jar.All()` |
| `client.SetCookies(u, cs)` | `jar.SetHTTPCookies(u, cs)`, applying the browser's rules; `jar.Set(cookies.Cookie{...})` to store one as given |
| a `Cookie` header string kept in a database | `jar.SetString(domain, header)` in; `jar.String(domain)` out |
| a new jar to clear | `jar.Clear()`, `jar.ClearSession()`, `jar.Delete(f)` |
| `SetCookieJar(shared)` | the same `*cookies.Jar` in each `Config.Cookies` |
| your own serialisation | `jar.WriteJSON`/`ReadJSON`, `WriteNetscape`/`ReadNetscape` |

The jar sends cookies as the profile's browser does, on redirect hops too.
It is safe for concurrent use. To visit several sites with one identity,
use one Transport per site and share one jar. The jar `NewClient` made is
`client.Transport.(*mimicry.Transport).Cookies()`.

A header string carries no attributes. `SetString` makes each pair a
cookie for exactly the domain you pass: `shop.example` reaches every host
under it, `www.shop.example` only that host and below. Cookies whose path
or other attributes matter go in with `Set`, `SetHTTPCookies` or
`ReadJSON`. See [cookies](cookies.md#using-it).

## Proxies and rotation

A tls-client codebase rotates with `SetProxy` or by building a new client
and carrying the cookies over. In mimicry `Config.Proxy` is called for
every request, so rotating means changing what it returns:

```go
var current atomic.Pointer[url.URL]
current.Store(proxies.Next())
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome(),
	Proxy: func(*http.Request) (*url.URL, error) {
		return current.Load(), nil
	},
})
// on a ban:
current.Store(proxies.Next())
```

The jar and the last page stay across proxies. A proxy failure returns a
`*mimicry.ProxyError`, which is your signal to rotate:

```go
resp, err := client.Do(req)
var pe *mimicry.ProxyError
if errors.As(err, &pe) {
	current.Store(proxies.Next())
}
```

Supported proxy types, credentials, idle connections and HTTP/3 through
SOCKS5 are covered in [proxies](proxies.md).

## Bodies, errors and retries

The response is a standard `*http.Response`: read and close the body.
Compressed bodies are decoded for you, even if a ported header literal
set `Accept-Encoding`. tls-client decodes in fewer cases. See
[what mimicry does](what-mimicry-does.md) for the rules and
`Config.DisableDecompression`.

Errors are returned, not encoded as a status code. Worth checking for:

- timeouts: a `net.Error` with `Timeout()` true, or the context's error;
- `mimicry.ErrNotMeasured`: the profile cannot describe the request, so
  nothing was sent. This is a programming error; do not retry;
- `*mimicry.CORSError`: the browser would not have sent the request;
- `*mimicry.ProxyError`: the proxy failed, not the site;
- `*mimicry.PinError`: the certificate matched none of `Config.Pins`.

mimicry retries on another connection only where the browser would.
Application-level retries, such as on 403, 429 or challenge pages, stay
yours.

## WebSockets

tls-client codebases use a separate WebSocket library with its own TLS
stack. mimicry opens sockets through the same Transport, with the
browser's handshake, cookies and TLS:

```go
tr := client.Transport.(*mimicry.Transport)
ws, _, err := tr.DialWebSocket(ctx, "wss://www.shop.example/live", mimicry.WebSocketOptions{})
```

See [WebSockets](websockets.md).

## Performance

Speed and memory compared with tls-client are in
[performance](../performance.md).

## Where mimicry is weaker

- **Few profiles.** Six desktop profiles: Chrome, Firefox and Safari on
  macOS, and Chrome on Windows. No iOS, Android or Linux, and no Firefox
  or Edge on Windows. Making a new one needs a real browser capture; see
  [refreshing profiles](../refreshing-profiles.md).
- **Proxy limits.** No `https://` proxies; see [proxies](proxies.md).
- **Cookie strings need a domain.** `SetString` needs the domain the
  cookies were for, and stores session cookies without `Secure`.
- **Some errors depend on the server.** A request the profile describes
  only over HTTP/2 fails with `ErrNotMeasured` on an HTTP/1.1-only
  server. `Plan` shows this in advance.
- **More typing.** `As` or `With` on every call, and a type assertion to
  reach the Transport.
- **Missing switches** browsers lack too: forcing HTTP/1.1, skipping
  verification, overriding the SNI.

Background: [0030](../decisions/0030-header-placement.md),
[0034](../decisions/0034-request-plans.md),
[0039](../decisions/0039-proxy-credentials-up-front.md),
[0046](../decisions/0046-response-decompression.md).
