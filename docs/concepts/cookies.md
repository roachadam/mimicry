# Cookies

Rough draft. Reference: `cookies/` (the package), [decision 0009](../decisions/0009-own-cookie-jar.md)
(why our own jar), [decision 0012](../decisions/0012-cookie-rules-in-profiles.md)
(the profile's rules), [the measurements](../../harness/reports/cookies-2026-10-24.md).

## Using it

`mimicry.NewClient` gives the transport a jar with the profile's rules;
nothing else is needed. To share, inspect or persist it, make your own and
pass it in:

```go
jar := cookies.New(cookies.Config{
	Rules: profiles.Chrome155MacOS().Cookies,
	OnSetCookie: func(e cookies.Event) {
		if e.Rejected != "" {
			log.Printf("%s dropped %q: %s", e.URL, e.Line, e.Rejected)
		}
	},
})
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome155MacOS(),
	Cookies: jar,
})
```

- `jar.Cookies(cookies.Request{…})` is exactly what a request would send,
  in the browser's order; `cookies.Header` formats it.
- `All`, `Set` (no URL needed), `Delete`, `Clear`, `ClearExpired`,
  `ClearSession`, `Clone`.
- `WriteJSON` / `ReadJSON`: lossless (every attribute, partitions, the raw
  `Set-Cookie` line). `WriteNetscape` / `ReadNetscape`: the cookies.txt
  format curl and wget use; it cannot hold SameSite or partitions, so
  partitioned cookies are left out.
- A request that sets its own `Cookie` header sends that, as written.
- `jar.CookieJar()` is an `http.CookieJar` for other code. It is lossy:
  `net/http` alters values before a jar sees them, and asks with only a
  URL, so no SameSite or third-party rule can apply.

## How the transport uses it

For each request, the resolver (see [how a request is
built](how-a-request-is-built.md)) decides whether its credentials mode
carries cookies at all. If it does, the jar gets the request's URL,
method, whether it is a top-level navigation, its page (`From`), and its
redirect chain, and answers with the cookies the browser would send. The
response's raw `Set-Cookie` lines go back to the jar with the same context.

## The rules

Shared by every browser, in code (RFC 6265bis): `Domain` must match the
host and not be a public suffix; host-only cookies go to their host only;
path matching and default paths; `Secure` only from and to HTTPS, and an
insecure URL cannot shadow a `Secure` cookie; `__Secure-` and `__Host-`
prefixes; name plus value at most 4096 bytes; `Max-Age` over `Expires`,
expiry capped at 400 days; `Lax` and `Strict` cookies refused from
cross-site responses; `Partitioned` needs `Secure`.

Where browsers differ, from the profile (`cookies` in the JSON):

| | Chrome | Firefox | Safari |
|---|---|---|---|
| No SameSite | Lax, but sent on a cross-site form POST for 2 minutes | None | None |
| `SameSite=None` without `Secure` | rejected | rejected | kept |
| Third-party (a request whose site is not its page's) | `SameSite=None` cookies sent; `Partitioned` ones kept per top-level site | every cookie kept per top-level site | only `Partitioned` cookies |
| After a redirect | judged by the URL alone | judged by the whole chain | nothing after a cross-site hop |
| `knoeq` (no `=`) | value: sent `knoeq` | name: sent `knoeq=` | dropped |
| UTF-8 values | as received | as received | re-encoded as Latin-1 |
| Order | longer path, then older | same | longer path, `Domain` cookies first, then name |

A top-level navigation's site is its own; any other request's is its
page's. A request is cross-site, for SameSite, when its site differs from
that (for Firefox, when any URL of its redirect chain does). Lax cookies
go cross-site only on top-level navigations with a safe method.

## Not modelled

Per-domain and total cookie limits (browsers evict past roughly 180 per
domain); the order of `Domain` cookies of different levels in Safari;
whether Safari refuses unpartitioned third-party cookies at store or send
(the jar does both); the end of Chrome's two-minute window is Chromium's
documented value, not captured.
