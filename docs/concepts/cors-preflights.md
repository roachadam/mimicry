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

## The answer

The request is sent only if the answer allows it:
- the status is 2xx;
- `Access-Control-Allow-Origin` is the page's origin, or `*` for requests
  without credentials;
- with credentials, `Access-Control-Allow-Credentials: true`;
- the method is listed in `Access-Control-Allow-Methods`, or is GET, HEAD or
  POST;
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

## The cache

A successful answer is remembered per page origin, URL and credentials
mode. It lasts for `Access-Control-Max-Age` seconds, or 5 s without one
(`Max-Age: 0`: not at all). A later request that the remembered answer
allows goes without a preflight. Browsers differ in what they remember
(`context.preflight_cache`):

- `replace` (Chrome, Safari): the URL's latest answer only. After a
  preflight for `X-Other`, a request with `X-Mimicry` is preflighted again.
- `merge` (Firefox): everything the URL's answers allowed, each until its
  own expiry.

Each `Transport` has its own cache, like a browser profile. Redirects come
back through `RoundTrip`, so a redirected request is preflighted for its
new URL too.

## What checks it

- `TestKindsMatchBrowserTraces` compares every preflight of the kinds page
  with the browser's: lists, values and weight over HTTP/2, HTTP/1.1 and
  plain HTTP, and that mimicry sends none where the browser did not.
- `TestCORSMatchesBrowser` replays the `cors` page. On each connection to
  the cross-site host, the same requests and preflights must arrive in the
  same order as the browser's. The refused steps must return a
  `*CORSError`.
- Unit tests in `preflight_test.go` cover the safelist, the expiry with a
  fake clock, and the refusal rules.
