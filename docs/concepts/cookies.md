# Cookies

mimicry keeps cookies in its own jar (the `cookies` package). The jar
follows the rules of the browser you chose: which cookies it stores, which
it sends, and in what order.

## Using it

`mimicry.NewClient` gives the transport a jar with the browser's rules. You
need nothing else. To reach it, call `Transport.Cookies()`:

```go
jar := client.Transport.(*mimicry.Transport).Cookies()
```

To share one jar between Transports, or to watch every `Set-Cookie`, make
your own jar and pass it in:

```go
jar, err := cookies.New(cookies.Config{
	Rules: profiles.Chrome().Cookies,
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
	Profile: profiles.Chrome(),
	Cookies: jar,
})
```

`cookies.New` checks the rules it is given. With nil rules the jar follows
the cookie standard (RFC 6265bis), for use without a browser.

### Read

- `jar.Cookies(cookies.Request{…})` returns exactly what a request would
  send, in the browser's order. It also marks those cookies as just used,
  which matters when the per-site limit evicts old ones.
- `jar.Peek` returns the same and changes nothing.
- `cookies.Header` formats either result as a `Cookie` header.
- `All` lists every cookie.

### Seed

- `SetString(domain, "a=1; b=2")` stores cookies from a `Cookie` header
  string. `FromString` makes a new jar from one.
- `domain` is a host name or a URL. Each cookie goes to that name and every
  name under it, as if the site had set `Domain=` to it. `shop.example`
  reaches `www.shop.example` and `api.shop.example`. `www.shop.example`
  does not reach `api.shop.example`.
- Seeded cookies are session cookies with path `/`, not `Secure`, and no
  `SameSite`.
- A public suffix (`com`, `co.uk`, `localhost`) or an IP address is an
  error.
- `SetHostString(host, "a=1; b=2")` stores cookies for that one host only,
  not the names under it. Here the host may be an IP address.
- `SetHTTPCookies(u, []*http.Cookie)` stores cookies from a solver or
  another library. They pass the browser's rules as if a page at `u` had
  set them. The error names each cookie refused and why; the rest are
  stored.
- `Set` stores one cookie directly, without a URL.

Cookie name prefixes get what browsers require, automatically:

- A `__Host-` cookie belongs to the exact host given, is `Secure`, and has
  path `/`. `SetString("shop.example", …)` puts it on `shop.example` only.
- A `__Secure-` cookie is stored `Secure`.
- `__Http-` and `__Host-Http-` cookies are stored `Secure` and `HttpOnly`
  for browsers that require it.

### Share across every site

Pass `cookies.AnySite` as the domain to store cookies every site receives.
No browser does this, so it is opt-in and visible in your code. Every host
gets these cookies, trackers and CDNs included, still subject to SameSite
and third-party rules. A site's own cookie of the same name is sent as
well. They count toward no site's limit. A `__Host-` cookie cannot be
shared this way.

### Save and load

- `String(domain)` returns the cookies for that name and the names under
  it as one `Cookie`-style string. Hosts, paths and attributes are
  dropped. `String("www.shop.example")` leaves out cookies set for
  `shop.example`, even though `www.shop.example` receives them.
- `WriteJSON` / `ReadJSON` keep everything: every attribute, partitions,
  and the raw `Set-Cookie` line.
- `WriteNetscape` / `ReadNetscape` use the cookies.txt format of curl and
  wget. That format cannot hold SameSite, partitions or creation times.
  Partitioned cookies, shared (`AnySite`) cookies, and cookies with a tab
  in a field are left out.
- `Delete`, `Clear`, `ClearExpired`, `ClearSession` and `Clone` do what
  their names say.

### Other code and manual headers

- A request that sets its own `Cookie` header sends that header as
  written. An empty `Cookie` header sends no cookies.
- `jar.CookieJar()` is an `http.CookieJar` for other code. It loses detail:
  `net/http` drops cookies it cannot parse, re-quotes some values, and
  passes only a URL, so SameSite and third-party rules cannot apply.

## How the transport uses it

For each request, mimicry first decides whether the request carries
cookies at all (see [how a request is built](how-a-request-is-built.md)).
If it does, the jar gets the URL, method, page, redirect chain, and
whether this is a top-level navigation. It answers with the cookies the
browser would send. The response's `Set-Cookie` lines go back to the jar
with the same context.

## The rules

Every browser follows the cookie standard:

- `Domain` must match the host and cannot be a public suffix.
- `Secure` cookies are set and sent only over HTTPS. An `http://` page
  cannot overwrite a `Secure` cookie.
- Name plus value is at most 4096 bytes.
- `Max-Age` wins over `Expires`. Either is capped at 400 days.
- `Lax` and `Strict` cookies from cross-site responses are refused.
- `Partitioned` needs `Secure`.

Where browsers differ, the jar follows the profile:

| | Chrome | Firefox | Safari |
|---|---|---|---|
| No SameSite | Lax, but sent on a cross-site form POST for 2 minutes | None | None |
| `SameSite=None` without `Secure` | rejected | rejected | kept |
| Third-party requests | `SameSite=None` cookies sent; `Partitioned` ones kept per top-level site | every cookie kept per top-level site | only `Partitioned` cookies |
| After a redirect | judged by the URL alone | judged by the whole chain | nothing after a cross-site hop |
| UTF-8 values | as received | as received | re-encoded as Latin-1 |
| Order | longer path first, then older | same | `Partitioned` first, then longer path, then name |
| `http://` and `https://` of one host | different sites | same site | same site |
| Cookies per site | 180; the 181st evicts the least recently used down to 150 | same | no limit |

A third-party request is one whose site differs from its page's. A
top-level navigation's site is its own. Lax cookies go cross-site only on
top-level navigations with a safe method such as GET.

Some cases are not modelled yet; the [backlog](../backlog.md#cookies)
lists them.

Background: [decision 0009](../decisions/0009-own-cookie-jar.md) (why an
own jar), [decision 0012](../decisions/0012-cookie-rules-in-profiles.md)
(the rules, including edge cases), [decision
0031](../decisions/0031-cookies-for-any-site.md) (seeding and `AnySite`).
