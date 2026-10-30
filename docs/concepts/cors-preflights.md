# CORS preflights

Before some cross-origin `fetch()` and XHR requests, a browser sends an
`OPTIONS` request, the preflight. It asks the server whether the real
request is allowed. mimicry does the same by default, and refuses to send
the request when the browser would.

## When a request is preflighted

All of these must hold:

- the request is a `Fetch` or `XHR` request (not `NoCORS`);
- its URL has another origin than its page (`From`);
- its method is not GET, HEAD or POST, **or** it carries a header that is
  not on the safelist below.

After a redirect from one origin to a third, the request's origin becomes
`null`. From then on, every URL counts as another origin.

Only headers the page could set count: the request's own and
`Config.Headers`. Headers only a browser sets (`Cookie`, `Referer`,
`Sec-*`, `User-Agent`, …) never count. The safelist is narrow:

- `Accept`, `Accept-Language` and `Content-Language` with plain values;
- `Content-Type` of `text/plain`, `multipart/form-data` or
  `application/x-www-form-urlencoded`;
- `Range: bytes=N-` or `bytes=N-M`;
- each at most 128 bytes.

So `Content-Type: application/json`, `Authorization`, or any `X-…` header
triggers a preflight.

```go
req, _ := http.NewRequest("PUT", "https://api.shop.example/items/1", body)
req.Header.Set("Content-Type", "application/json")
resp, err := client.Do(mimicry.With(req, mimicry.Request{
	Kind: mimicry.Fetch,
	From: page, // https://www.shop.example/
}))
// Sent: OPTIONS /items/1 (Access-Control-Request-Method: PUT,
// Access-Control-Request-Headers: content-type), then the PUT.
```

## What is sent

The preflight's headers, their order and values are the browser's own. The
headers that depend on the request are:

- `Origin`, `Referer` and `Sec-Fetch-Site`, as for the request;
- `Access-Control-Request-Method`: the request's method;
- `Access-Control-Request-Headers`: the names of the unsafelisted headers,
  lowercase, sorted, joined by `,`. It is absent when there are none.

A preflight never carries cookies, even for `CredentialsInclude`.

Of `Config.Headers`, a preflight carries only the ones the browser puts on
a preflight. An override of `User-Agent` or `Accept-Language` goes on it;
an API key never does. The request's own headers are never on it; they are
only named in `Access-Control-Request-Headers`.

## The answer

The request is sent only if the answer allows it:

- the status is 2xx;
- `Access-Control-Allow-Origin` is the page's origin, or `*` for requests
  without credentials;
- with credentials, `Access-Control-Allow-Credentials: true`;
- the method is GET, HEAD or POST, or is listed in
  `Access-Control-Allow-Methods`. A `*` there covers any method for
  requests without credentials;
- every unsafelisted header is listed in `Access-Control-Allow-Headers`. A
  `*` there covers headers only for requests without credentials, and
  never `Authorization`.

Otherwise `client.Do` returns an error wrapping `*mimicry.CORSError`. It
holds the reason and the preflight's status and headers. Nothing more is
sent. Set `Request.IgnoreCORS` to send the request anyway. The preflight
still goes first, but no browser would send what follows.

The real response's `Access-Control-*` headers are not checked. A browser
has already sent the request by then; it only hides the response from the
page's script.

## Redirects

A cross-origin `Fetch` or `XHR` request can follow a redirect only if the
redirect passes the CORS check. Every browser does this:

- `Access-Control-Allow-Origin` is the request's origin (`null` after a
  hop to a third origin), or `*` without credentials;
- with credentials, `Access-Control-Allow-Credentials: true` too.

Otherwise `client.Do` returns an error wrapping a `*mimicry.CORSError`
with `Redirect` set, and the next URL is never requested.
`Request.IgnoreCORS` follows it anyway. Same-origin and `NoCORS` requests,
navigations and page elements are not checked.

A redirected request is preflighted again for its new URL when needed. How
redirects change the method and body, and how many are followed, is in
[what mimicry does](what-mimicry-does.md#redirects).

## The cache

mimicry remembers a successful answer per page origin, URL and credentials
mode. A later request that the answer allows goes without a preflight. The
answer lasts for `Access-Control-Max-Age` seconds, or 5 seconds without
that header. `Max-Age: 0` means it is not remembered.

Browsers differ in what they remember:

- Chrome and Safari keep only the URL's latest answer. After a preflight
  for `X-Other`, a request with `X-Mimicry` is preflighted again.
- Firefox keeps everything the URL's answers allowed, each until it
  expires.

Each `Transport` has its own cache, like a browser profile.

Background: [decision 0017](../decisions/0017-cors-preflights.md).
