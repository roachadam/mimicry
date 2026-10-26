# Cookies

Rough draft. Reference: `cookies/` (the package), [decision 0009](../decisions/0009-own-cookie-jar.md)
(why our own jar), [decision 0012](../decisions/0012-cookie-rules-in-profiles.md)
(the profile's rules), [the measurements](../../harness/reports/cookies-2026-10-24.md).

## Using it

`mimicry.NewClient` gives the transport a jar with the profile's rules;
nothing else is needed, and `Transport.Cookies()` returns it
(`client.Transport.(*mimicry.Transport).Cookies()`). To share one jar
between Transports, or set a hook, make your own and pass it in.
`cookies.New` checks the rules it is given (`profile.Cookies.Validate`);
nil rules are RFC 6265bis's (`profile.DefaultCookies`), for a jar used
without a profile.

```go
jar, err := cookies.New(cookies.Config{
	Rules: profiles.Chrome155MacOS().Cookies,
	OnSetCookie: func(e cookies.Event) {
		if e.Rejected != "" {
			log.Printf("%s dropped %q: %s", e.URL, e.Line, e.Rejected)
		}
	},
})
if err != nil {
	return err
}
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome155MacOS(),
	Cookies: jar,
})
```

- `jar.Cookies(cookies.Request{…})` is exactly what a request would send,
  in the browser's order, and updates those cookies' last-access times
  (which the per-site limit evicts by); `jar.Peek` returns the same and
  changes nothing (`Transport.Plan` reads the jar with it);
  `cookies.Header` formats either.
- `All`, `Set` (no URL needed), `Delete`, `Clear`, `ClearExpired`,
  `ClearSession`, `Clone`.
- `SetString(domain, "a=1; b=2")` seeds cookies from a `Cookie` header
  string, `FromString` makes a jar from one, and `String(domain)` gives
  them back as one ([decision 0031](../decisions/0031-cookies-for-any-site.md)).
  `domain` is a host name or a URL (its host), read as a browser reads a
  cookie's `Domain` attribute and never widened to the site: each pair is
  a cookie for that name and every name under it. `amazon.com` reaches
  `www.`, `api.` and `smile.amazon.com`; `www.amazon.com` reaches
  `www.amazon.com` (and names under it), not `api.amazon.com`. Path `/`,
  session, not `Secure`, no `SameSite` (the profile's default). A public
  suffix (`com`, `co.uk`, `localhost`) or an IP address is an error, as no
  `Domain` may name one.
- `SetHostString(host, "a=1; b=2")` stores host-only cookies: that one
  host (a name, an IP address or a URL's host), not the names under it.
- Prefixes get what browsers require, automatically. A `__Host-` cookie is
  one a site pins to the single host that set it, HTTPS only, every path:
  it is stored host-only on exactly the name given (`SetString("amazon.com",
  …)` puts it on the bare `amazon.com` host), `Secure`, path `/`.
  `__Secure-` cookies are stored `Secure`; `__Http-` and `__Host-Http-`
  ones `Secure` and `HttpOnly` where the profile enforces them.
- `String(domain)` lists every unpartitioned cookie the jar holds for that
  name and the names under it, host-only and `Domain` alike, of every path,
  in creation order: `String("www.amazon.com")` leaves out `amazon.com`'s
  `Domain` cookies, which `www.amazon.com` is sent but which are not its
  own. Hosts, paths and attributes are dropped (`WriteJSON` keeps them).
- `cookies.AnySite` as the domain stores cookies every site receives: no
  browser has them, so this is opt-in and visible in your code. Every
  host gets them where its own cookie would go (SameSite and third-party
  rules still apply), trackers and CDNs included, and a site's cookie of
  the same name goes as well. They count towards no site's limit and are
  left out of `WriteNetscape`. A `__Host-` cookie cannot be one (an
  error): it belongs to one host.
- `SetHTTPCookies(u, []*http.Cookie)` stores cookies from a solver or
  another library as `Set-Cookie` lines of a navigation to `u`, through
  the browser's rules; the error names each cookie refused and why, and
  the rest are stored.
- `WriteJSON` / `ReadJSON`: lossless (every attribute, partitions, the raw
  `Set-Cookie` line). `WriteNetscape` / `ReadNetscape`: the cookies.txt
  format curl and wget use; it cannot hold SameSite, partitions or creation
  times and separates fields with tabs, so partitioned cookies and cookies
  with a tab in a field are left out.
- A request that sets its own `Cookie` header (in any case) sends that, as
  written; one set with no values sends none.
- `jar.CookieJar()` is an `http.CookieJar` for other code. It is lossy:
  `net/http` hands it only the cookies it can parse (values with
  backslashes, UTF-8 or inner quotes are dropped before the jar sees them;
  it keeps the raw lines of the rest), re-quotes values with spaces or
  commas when it sends them, and asks with only a URL, so no SameSite or
  third-party rule can apply.

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
default paths and path matching, on the URL's escaped path; `Secure` only
from and to HTTPS (and loopback, where the profile says), and an insecure
URL cannot shadow a `Secure` cookie; `__Secure-` and `__Host-` prefixes; a
nameless cookie whose value starts with `__Secure-` or `__Host-` (any
case, after spaces and tabs), or holds `=`, is refused, by `SetCookies`
and `Set` alike (where a browser keeps nameless cookies at all, below);
name plus value at most 4096 bytes; `Max-Age` over `Expires`, either
capped at 400 days from creation;
of repeated attributes the last wins (a last invalid `Path` gives the
default path, a last empty `Domain` a host-only cookie); `Lax` and
`Strict` cookies refused from cross-site responses; `Partitioned` needs
`Secure`. Cookies are kept by registrable domain, so a request looks only
at its own host's.

Where browsers differ, from the profile (`cookies` in the JSON):

| | Chrome | Firefox | Safari |
|---|---|---|---|
| No SameSite | Lax, but sent on a cross-site form POST for 2 minutes | None | None |
| `SameSite=None` without `Secure` | rejected | rejected | kept |
| Third-party (a request whose site is not its page's) | `SameSite=None` cookies sent; `Partitioned` ones kept per top-level site | every cookie kept per top-level site | only `Partitioned` cookies |
| After a redirect | judged by the URL alone | judged by the whole chain | nothing after a cross-site hop |
| `knoeq` (no `=`) | value: sent `knoeq` | name: sent `knoeq=` | dropped |
| `=x` (empty name) | value: sent `x`; `=__Secure-x`, `= __Host-x`, `=__secure-x` rejected | dropped | dropped |
| UTF-8 values | as received | as received | re-encoded as Latin-1 |
| Order | longer path, then older | same | longer path, `Domain` cookies first, then name |
| A cookie replaced with a new value | becomes the newest (moves in the order) | keeps its creation time | (order is by name) |
| SameSite across `http://` and `https://` of one host | cross-site (schemeful) | same-site | same-site |
| `__Host-` without `Path` (default path `/`) | rejected: needs the attribute | rejected | kept |
| `__Http-`, `__Host-Http-` | need `Secure` and `HttpOnly` | same | ordinary names (`__Host-Http-` still needs `__Host-`'s `Secure`) |
| `Secure` over `http://localhost` | set and sent | same | Safari 27: same; Safari 26: rejected |
| Cookies per site | 180; the 181st evicts the least recently used down to 150 | same | no limit (200 kept) |

A top-level navigation's site is its own; any other request's is its
page's. A request is cross-site, for SameSite, when its site differs from
that (for Firefox, when any URL of its redirect chain does; for Firefox
and Safari, without the scheme). Lax cookies go cross-site only on
top-level navigations with a safe method. The per-site limit counts each
partition apart, and evicts expired cookies first.

## Not modelled

Total cookie limits, and Chrome's priorities and `Secure`-last eviction
within the per-site limit; adjusting `Expires` for the server's `Date`
header; whether Chrome refuses a nameless value starting with `__Http-`
(not captured; the jar checks `__Secure-` and `__Host-`, which covers
`__Host-Http-`); loopback hosts other than `localhost` (from the Secure
Contexts specification); the order of
`Domain` cookies of different levels in Safari; whether Safari refuses
unpartitioned third-party cookies at store or send (the jar does both);
the end of Chrome's two-minute window is Chromium's documented value, not
captured. The ledger lists the gaps in
[not covered](../../profiles/EVIDENCE.md#not-covered-by-any-claim), and
the [backlog](../backlog.md#cookies) what closing each would take.
