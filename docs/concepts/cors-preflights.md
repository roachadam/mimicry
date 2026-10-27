# CORS preflights

Before some cross-origin `fetch()` and XHR requests, a browser sends an
`OPTIONS` request (the preflight) asking whether the real one is allowed.
mimicry does the same, by default. See
[decision 0017](../decisions/0017-cors-preflights.md) for why it refuses
when the browser would.

## When a request is preflighted

All of these must hold:

- its kind is `Fetch` (not `NoCORS`) or `XHR`;
- its URL is another origin than the page's (`From`). After a redirect chain
  that went from another origin to a third, the request's origin is `null`,
  and every URL counts as another origin;
- its method is not GET, HEAD or POST, **or** it carries a header the page
  set that the Fetch standard does not safelist.

"Headers the page set" are the request's own and `Config.Headers`. Headers
only a browser may set (`Cookie`, `Referer`, `Sec-*`, `User-Agent`, …) never
count. The safelist is narrow:
- `Accept`, `Accept-Language` and `Content-Language` with plain values;
- `Content-Type` of `text/plain`, `multipart/form-data` or
  `application/x-www-form-urlencoded`;
- `Range: bytes=N-` or `bytes=N-M`;
- all of them at most 128 bytes.

So `Content-Type: application/json`, `Authorization`, or any `X-…` header
triggers a preflight.

```go
req, _ := http.NewRequest("PUT", "https://api.example.com/items/1", body)
req.Header.Set("Content-Type", "application/json")
resp, err := client.Do(mimicry.With(req, mimicry.Request{
	Kind: mimicry.Fetch,
	From: page, // https://www.example.com/
}))
// The wire: OPTIONS /items/1 (Access-Control-Request-Method: PUT,
// Access-Control-Request-Headers: content-type), then the PUT.
```

## What is sent

The profile's `preflight` kind gives the header list, its values and the
HTTP/2 weight, measured per browser. Priority and weight are each browser's
`fetch()` ones. Safari puts `Origin` first and sends `Content-Length: 0`.
The request-dependent headers are:
- `Origin`, `Referer` and `Sec-Fetch-Site`, as for the request;
- `Access-Control-Request-Method`: the request's method;
- `Access-Control-Request-Headers`: the unsafe header names, lowercase,
  sorted, joined by `,` (`x-a,x-b`), or absent when there are none.

A preflight never carries cookies, even for `Credentials: CredentialsInclude`. Chrome
sends it on its connection for credential-less requests.

Of `Config.Headers`, a preflight carries only those the preflight kind's
lists name, so an override of `User-Agent` or `Accept-Language` goes on it
as on the request; others, such as an API key, never do. The request's own
headers are never on it: the unsafe ones are only named in
`Access-Control-Request-Headers`.

## The answer

The request is sent only if the answer allows it:
- the status is 2xx;
- `Access-Control-Allow-Origin` is the page's origin, or `*` for requests
  without credentials;
- with credentials, `Access-Control-Allow-Credentials: true`;
- the method is listed in `Access-Control-Allow-Methods`, or is GET, HEAD or
  POST; a `*` in that list covers any method for requests without
  credentials;
- every unsafe header is listed in `Access-Control-Allow-Headers`. A `*` in
  that list covers headers only for requests without credentials, and
  never `Authorization`.

Otherwise `client.Do` returns an error wrapping `*mimicry.CORSError`, with
the reason and the preflight's status and headers, and nothing more is
sent. Set `Request.IgnoreCORS` to send the request anyway: the preflight
still goes first, but no browser would send what follows.

The real response's own `Access-Control-*` headers are not checked: a
browser has sent the request by then, and only hides the response from the
page's script.

## Redirects

A redirect is different: the browser decides from it whether to send
anything more. For a `fetch()` or XHR in CORS mode to another origin, a
3xx with a `Location` must pass the CORS check, as the Fetch standard
says and every captured browser did (Chrome 155, Firefox 158, Safari 27
and 26, with and without a preflight before):
`Access-Control-Allow-Origin` is the request's origin (`null` once
tainted), or `*` without credentials; with credentials,
`Access-Control-Allow-Credentials: true` too. Otherwise `client.Do` returns
an error wrapping a `*mimicry.CORSError` with `Redirect` set and the
redirect's status and headers, and the next URL is never requested.
`Request.IgnoreCORS` follows it anyway. Same-origin and `no-cors` requests,
navigations and elements are not checked.

How the next request goes out, every browser alike:

- a POST answered 301 or 302, and anything but GET and HEAD answered 303,
  becomes a GET without its body and `Content-Type`;
- every other request keeps its method, body and `Content-Type`: a PUT or
  DELETE answered 301 or 302 too, which `http.Client` would turn into a
  GET. mimicry sends it again from the request's `GetBody` (set by
  `http.NewRequest` for in-memory bodies); without one, following such a
  redirect is an error;
- a chain stops after 20 redirects: the 21st URL is never requested, and
  `client.Do` returns an error. `NewClient`'s client lifts `http.Client`'s
  default limit of 10; a client of your own keeps its `CheckRedirect`.

Where browsers differ, the profile says (`redirects.methodChangeOrigin`):
the GET a redirect makes of a same-origin POST, PUT or DELETE `fetch()`
carries the `Origin` the request sent in Safari (`keep`), none in Chrome
and Firefox (`drop`). A profile without the rule cannot send that GET
(`ErrNotMeasured`).

`http.Client` still follows the redirects: its `CheckRedirect`, `Jar` and
`Timeout` apply. Its `via` list shows the hops as it built them (a PUT
answered 301 appears as a GET); the requests sent, `resp.Request`
included, are the browser's.

## The cache

A successful answer is remembered per page origin, URL and credentials
mode. It lasts for `Access-Control-Max-Age` seconds as sent (browsers cap
it, but their caps are not measured), or 5 s without one (`Max-Age: 0`:
not at all). A later request that the remembered answer
allows goes without a preflight. Browsers differ in what they remember
(`context.preflightCache`):

- `replace` (Chrome, Safari): the URL's latest answer only. After a
  preflight for `X-Other`, a request with `X-Mimicry` is preflighted again.
- `merge` (Firefox): everything the URL's answers allowed, each until its
  own expiry.

Each `Transport` has its own cache, like a browser profile. Redirects come
back through `RoundTrip`, so a redirected request is preflighted for its
new URL too (after its redirect passed the CORS check).

## What checks it

- `TestKindsMatchBrowserTraces` compares every preflight of the kinds page
  with the browser's: lists, values and weight over HTTP/2, HTTP/1.1 and
  plain HTTP, and that mimicry sends none where the browser did not.
- `TestCORSMatchesBrowser` replays the `cors` page. On each connection to
  the cross-site host, the same requests and preflights must arrive in the
  same order as the browser's. The refused steps must return a
  `*CORSError`.
- `TestCORSEdgeMatchesBrowser` replays the `cors-edge` page the same way,
  on every connection: a cross-site redirect without
  `Access-Control-Allow-Origin` (no next request), PUT, DELETE and POST
  answered 301, 302 and 303 (method, body length, `Origin`), and a chain
  of 25 redirects (the 21st URL never requested).
- Unit tests in `preflight_test.go` cover the safelist, the expiry with a
  fake clock, the refusal rules, the cache's pruning, and which
  `Config.Headers` a preflight carries; `redirect_test.go` the redirect
  methods and bodies, the limit, the CORS check's rules and the `Origin`
  rule.
