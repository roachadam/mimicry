# How a request is built

What happens between `client.Do(req)` and bytes on the wire. Users mostly
need §1 (how to describe a request) and §5 (how to see what will be sent).
The rest is for contributors; the code is the reference (`mimicry.go`,
`request.go`, `resolve.go`, `redirect.go`, `plan.go` and `internal/`).

Two words recur on this page:

- A **kind** is what made the request in the browser: a navigation, a
  `fetch()`, an image, a stylesheet, a font, a frame, a beacon, and so on.
  Each kind sends its own headers in its own order.
- A **variant** is another header list the browser uses for the same kind
  in a particular situation, such as a POST, a cross-site URL or a
  redirect.

## 1. The request says how it is made

A plain request is a top-level navigation, as if typed into the address
bar. To make it something else, use `mimicry.As(req, mimicry.Fetch)` or
`mimicry.With(req, mimicry.Request{…})`. These set the kind and, where it
applies:

- the page the request comes from (`From`);
- fetchpriority and the crossorigin attribute;
- the credentials mode, or no-cors;
- the stylesheet that loads a font or image.

A Transport is one browsing entity, like one browser tab. A request that
is not a navigation and has no `From` comes from the transport's last
navigation, whichever goroutine made it. Before any navigation, it comes
from the request's own origin. Tasks that browse in parallel should use
one client each, or set `From`.

A navigation without `From` is typed into the address bar, so it must be
a GET or HEAD. A POST navigation comes from a form and needs `From`. An
API call is a `Fetch`.

Combinations no browser makes are errors before anything is sent:

- a no-cors fetch with a method other than GET, HEAD or POST;
- a beacon that is not a POST;
- a stylesheet set on anything but a font or an image;
- a crossorigin attribute on a kind that has none;
- a credentials mode on anything but Fetch and XHR.

## 2. The transport resolves it against the profile

`Transport.RoundTrip` turns the request into a plan (`resolve.go`).

First the URL takes the form a browser's URL parser (the WHATWG URL
standard, as the `urls` capture measures it) gives it, as do
`Request.From`, `Request.Stylesheet`, every redirect's `Location` and a
WebSocket's URL and page (`url.go`):

- the host is lowercased, and a host with non-ASCII characters goes
  through UTS #46 ToASCII (`bücher.de` is `xn--bcher-kva.de`); one that
  fails is an error before anything is sent. An IPv4 address in any form
  the standard accepts (`127.1`, `0x7f.1`, `2130706433`) becomes dotted
  decimal;
- a port equal to the scheme's default is dropped (`:443` on `https`);
- the path's `\` is a separator, its `.` and `..` segments (as `%2e` too)
  are resolved, and it is percent-encoded with the path set (the query
  set, `?`, `^`, `` ` ``, `{` and `}`), keeping any `%XX` already there;
- the query is percent-encoded with the query set (control characters,
  space, `"`, `#`, `<`, `>` and every byte above `~`), and `'` on http(s)
  and ws(s), keeping any `%XX` already there.

Where browsers differ, the profile's `urls` says what its browser does:
Chrome encodes `|` in a path (`pathEncodes`); Firefox and Safari 26
refuse a host whose `xn--` label is not valid punycode (`checkPunycode`).
A profile without `urls` returns `ErrNotMeasured` for those two inputs
([decision 0057](../decisions/0057-url-parsing-measured.md)).

To send a request target exactly as written, as a security test may
(`/../`, `\`, an invalid escape, a raw `'`), set `req.URL.Opaque`, as
with `net/http`: `"/raw/path"`, or `"//host/raw/path"`, whose host part
is dropped. Its path and `RawQuery` go out untouched over HTTP/1.1, HTTP/2
and HTTP/3, `Exact` or not; only the host is still made canonical, since
it picks the dial, SNI and connection pool. A browser never sends such a
target, so it is a deviation the caller asks for.

Everything after this sees that form: the dial, SNI, the connection pool
and its tickets, `Host` and `:authority`, cookies, `Origin` and
`Referer`. Your `req` is left as it is; `resp.Request.URL` is the URL
sent, as a browser's `response.url` is. A host ending in a dot keeps it
everywhere but SNI, which follows the profile's `tls.trailingDotSni`:
desktop Chrome, Edge and Firefox send the name with its dot; Safari and
every iPhone profile (WebKit) send none (and give such a host a
connection of its own).

1. **Add `Config.Header`.** Each one the request does not set itself is
   added. From here on it counts as a header the page set. Once a redirect
   chain has left an origin, `Authorization` and `Cookie` from
   `Config.Header` are no longer added, and every request but a
   navigation loses its own `Authorization` too (the Fetch standard's
   HTTP-redirect fetch), before the preflight decision.
2. **Pick the header list.** A *list* is the browser's header names in
   order, with blanks (*slots*) for values that depend on the request.
   Each kind has a default list and variants. The variant depends on the
   method, how the URL relates to the page, CORS, page-set headers,
   redirects, or a non-secure URL (plain `http://` to anything but
   loopback). If no variant matches, the default list is used. Two cases
   fail with `ErrNotMeasured` instead:
   - a redirected fetch or image request that matches no variant;
   - a user's navigation (typed URL, clicked link or button) whose list
     has no `Sec-Fetch-User` slot, because the browser was only seen
     sending that list from scripts.
3. **Compute what the page decides** and fill the slots:
   - `Sec-Fetch-Site`: same-origin, same-site or cross-site. After a
     redirect, the most distant relation along the chain.
   - `Sec-Fetch-Mode`: the kind's mode, `cors` with a crossorigin
     attribute, `no-cors` for a no-cors fetch.
   - `Cookie`: sent or removed by the browser's credentials rules
     ([cookies](cookies.md)), in the list's `cookie` slot; a redirect hop
     the captures never saw carrying cookies has none, and the jar's
     cookies go after its list
     ([EVIDENCE.md, Not covered](../../profiles/EVIDENCE.md#not-covered-by-any-claim)).
   - `Origin`: on non-GET/HEAD requests and on CORS requests to another
     origin; `null` where the browser sends it after redirects.
   - `Referer`: strict-origin-when-cross-origin, applied hop by hop.
     `http.Client`'s redirect Referer is discarded.
   - `Priority` and the HTTP/2 weight: the kind's, or its fetchpriority's.
   - A connection partition: Chrome keeps credential-less requests and
     cross-site frames on separate connections.
4. **Layer the request's own headers.** They win over the profile's
   defaults and `Config.Header`, except framing: `Content-Length`,
   `Connection` and `Transfer-Encoding` in a header map are ignored, and
   the connection builds them from the request (`req.ContentLength`, the
   body, the protocol), as `net/http` does. A POST or PUT with no body
   sends `Content-Length: 0` over HTTP/2 and HTTP/3 where the profile
   measured it (`emptyBody`: Chrome and Safari in the list's place, Safari
   on a PATCH too; Firefox's fetch() puts it last, before `te`, and ends
   the HTTP/2 stream with an empty DATA frame); a profile without that
   measurement sends none. Over HTTP/1.1,
   `Connection: close` (or `req.Close`) is honoured: it goes in the
   profile's `Connection` slot and the connection closes after the
   response. A header set to `nil`
   suppresses a default. `HeaderOrderKey` replaces the order, and a
   profile default it does not name is not sent. `Config.HeaderPlacement` and
   `Request.HeaderPlacement` move one header directly before or after
   another; the connection applies them last. A request with
   `HeaderOrderKey` ignores `Config` placements, and combining it with its
   own placements is an error (`ErrPlacement`).
5. **Preflight it if the browser would.** See
   [CORS preflights](cors-preflights.md).

`Exact(req)` skips steps 2, 3 and 5. Only the request's own headers and
`Config.Header` are sent, in `HeaderOrderKey`'s order or sorted by name.

What the browser was never seen doing is an error wrapping
`mimicry.ErrNotMeasured`, not a guess. Examples: a kind the profile lacks,
a fetchpriority it has no data for, or a variant seen only over HTTP/2 but
sent over HTTP/1.1.

### The order of the page's own headers (XHR)

`http.Header` keeps no order, so mimicry takes the headers a page sets
as set in name order, as `fetch()` does. An XHR keeps the order its
script called `setRequestHeader` in, and the browsers differ in how much
that shows:

- **Firefox** sends the page's headers as one run, in that order. Set
  the order with a placement:

  ```go
  req.Header.Set("X-Token", token)
  req.Header.Set("X-App", "shop")
  // Firefox's script set X-Token first; in name order X-App would lead.
  req = mimicry.With(req, mimicry.Request{
  	Kind:            mimicry.XHR,
  	HeaderPlacement: []mimicry.Place{{Header: "X-Token", Before: "X-App"}},
  })
  ```

- **Chrome and Safari** place each header by its name in a hash table,
  so the order the page set them changes the result only when names
  collide, which takes many page headers. Leave these unplaced: moving
  one makes an order the browser never sends.

Don't use `HeaderOrderKey` for this: it replaces the whole order, and a
profile header it doesn't name, such as `sec-ch-ua`, is not sent.

## 3. The connection writes it

The engine (`internal/engine/native`) rejects an invalid host, picks the
proxy, and asks the pool for a connection. Connection reuse, the per-host
limit, coalescing and idle timeouts are described in
[what mimicry does](what-mimicry-does.md); proxies in
[proxies](proxies.md).

A new connection is dialled, through the proxy if there is one, and
handshaked with the profile's ClientHello. Its certificate is checked
against `Config.RootCAs`, then `Config.Pins` and `Config.VerifyConnection`.
A request of which nothing was sent is retried on another connection. The
request is then written:

- **HTTP/2** (`internal/h2`): pseudo-headers in the browser's order, the
  plan's headers, cookies split as the browser splits them, HPACK encoded
  as the browser encodes, and the stream priority.
- **HTTP/1.1** (`internal/h1`): the plan's HTTP/1.1 list, names cased as
  the browser sends them. Headers the profile does not name keep the
  caller's spelling.
- **HTTP/3** (`internal/h3`): as HTTP/2, with the browser's HTTP/3 header
  changes and QPACK.

Each writer applies header placements last, to every line it is about to
send. Pseudo-headers are never placed. A placement naming a header the
request does not send fails with `ErrPlacement` before anything is
written.

## 4. The response comes back

With a cookie jar, the response's `Set-Cookie` lines go to the jar
([cookies](cookies.md)). A body in gzip, deflate, Brotli or zstd is
decoded, as a browser would, unless `Config.DisableDecompression` is set.
A navigation that succeeded becomes the transport's last navigation.

## 5. Seeing it before it is sent

`Transport.Plan(req)` builds the request exactly as `RoundTrip` would and
stops before anything goes out. Use it to check a ported request, or to
log what will be sent.

```go
tr := client.Transport.(*mimicry.Transport)

plan, err := tr.Plan(mimicry.As(req, mimicry.Fetch))
if err != nil {
	log.Fatal(err) // the error RoundTrip would return
}
fmt.Println(plan) // every field, ready for a log
```

A `Plan` holds:

- `Kind`, and `Variant`: the list it used, as a path in the profile's
  JSON, such as `kinds.fetch.variants[4]` or `kinds.fetch.headers`.
- `Condition`: why that variant matched. Its names are `pageSet` (a header
  the page set), `corsChecked`, `crossOriginRedirect`,
  `crossSiteRedirect`, `originBeforeRedirect` and `insecure`, plus the
  method and site.
- `HTTP2`, `HTTP1` and `HTTP3`: the header lines each protocol would
  write, including Host, framing, casing, cookies and placements. Where a
  protocol would fail, its `Err` says why.
- `Protocol`: the protocol the request would take now, named as
  `resp.Proto` names it. It is empty when only the server's answer can
  decide.
- `Preflight`: the CORS preflight's own plan, its `Kind` `mimicry.Preflight`,
  or nil when none is sent.
- `Overrides`: headers you set in place of a profile default, a computed
  value or the jar's cookies. `Dropped`: profile headers your
  `HeaderOrderKey` leaves out.

Plan changes nothing. It dials nothing, looks nothing up, and leaves the
cookie jar, last navigation, preflight cache and connections as they
were. It returns the error `RoundTrip` would return before sending, but
never a `*CORSError`, since that is the server's answer. A plan is a
snapshot: a response that sets cookies, or a navigation in between,
changes what goes out. See `ExampleTransport_Plan` for a full example.

## 6. Seeing what was sent

A plan shows the request; a packet capture shows everything else on the
connection (the TLS handshake, frames, QUIC packets). It needs the TLS
secrets to be readable: `Config.KeyLogWriter` writes them in the NSS key
log format that Chrome and Firefox write to `SSLKEYLOGFILE`, for every
connection, TCP and QUIC.

```go
keys, err := os.Create("keys.log")
if err != nil {
	log.Fatal(err)
}
defer keys.Close()

client, err := mimicry.NewClient(mimicry.Config{
	Profile:      profiles.ChromeMacOS(),
	KeyLogWriter: keys,
})
```

Capture the traffic (`sudo tcpdump -i any -w trace.pcap port 443`), then
open the capture in Wireshark with Preferences ▸ Protocols ▸ TLS ▸
(Pre)-Master-Secret log filename set to `keys.log`, or
`tshark -r trace.pcap -o tls.keylog_file:keys.log`. HTTP/2 and HTTP/3 then
show as frames and headers.

Nothing on the wire changes. Anyone with the log can read the traffic it
covers, so keep it like a password and leave it unset outside debugging.
mimicry never reads `SSLKEYLOGFILE` itself
([0061](../decisions/0061-tls-key-log.md)): a library that wrote secrets
wherever its environment said could leak them from a process that never
asked.

## Limits

- **Headers you set are sent as set**, including names a page may not set
  (`Cookie`, `Origin`, `Sec-Fetch-*`, `Proxy-Authorization`), as in other
  HTTP clients (the maintainer's decision): an override is deliberate.
  `Host` is replaced by the URL's host, and `Content-Length`,
  `Connection` and `Transfer-Encoding` are built from the request (step
  4); an override can make a request look less like the browser's.
- **`Accept-Language` is the capture machine's** setting, not a browser
  constant. Vendors compare it with the exit IP's location; set your own
  with `Config.Header` (preflights carry it too). A Brave profile varies
  its q value per page origin (`Context.AcceptLanguageQ`); one you set is
  sent as set. A request from a cross-site frame takes the frame's own
  draw (`Request.From`), where Brave takes its tab's.
- **No HTTP cache.** Nothing adds `If-None-Match` or `If-Modified-Since`;
  set them yourself to look like a returning visitor.
- **`ErrNotMeasured`** also comes from: a same-site form POST navigation; a
  variant measured over HTTP/2 only (the cross-site form POST navigation,
  PUT fetches, redirected-image variants, the XHR POST) sent over
  HTTP/1.1; Firefox's clicked form POST over HTTP/1.1; `Request.Priority`
  on a kind other than `Fetch` and `InjectedImage`; a priority measured
  only over HTTP/3 sent over TCP (custom profiles); an `http://` request
  or a redirect hop in a condition no capture showed; Safari's `fetch()`
  with method OPTIONS; on a profile without `urls` (`chrome-155-macos`,
  `firefox-158-*`), a `|` in a path or an invalid `xn--` host label.
- **A lone `%` in a path** cannot be written in a URL: Go's `url.Parse`
  refuses it inside `http.NewRequest`, where browsers send it as written.
  Send it with `req.URL.Opaque` (above).
- **A host ending in a dot** goes on the connection a browser would pick
  only for Safari: Chrome and Firefox reuse another host's connection for
  it in one of fetch() and navigation, which mimicry does not model
  ([0057](../decisions/0057-url-parsing-measured.md#amendment-2026-11-06-trailing-dot-sni)).
  A profile without `urls` refuses such a host over TLS.
- **No kind** for `FontFace().load()` or `crossorigin` stylesheets.
- **`Config.Header`'s `Authorization` and `Cookie`** stop once a redirect
  chain has left their origin, as the Fetch standard says; so does a
  request's own `Authorization`, except on a navigation (whose page-set
  headers after a cross-origin redirect are not measured).
- **`CheckRedirect` and `via`** show the hops as `http.Client` built them (a
  PUT answered 301 shows as a GET); the requests sent are the browser's.

Background: decisions [0005](../decisions/0005-request-kinds-api.md),
[0006](../decisions/0006-header-order-variants.md),
[0008](../decisions/0008-unmeasured-is-an-error.md),
[0011](../decisions/0011-connection-partitioning.md),
[0015](../decisions/0015-non-secure-origins.md),
[0017](../decisions/0017-cors-preflights.md),
[0030](../decisions/0030-header-placement.md),
[0032](../decisions/0032-certificate-pinning.md),
[0034](../decisions/0034-request-plans.md),
[0046](../decisions/0046-response-decompression.md),
[0057](../decisions/0057-url-parsing-measured.md),
[0061](../decisions/0061-tls-key-log.md).
