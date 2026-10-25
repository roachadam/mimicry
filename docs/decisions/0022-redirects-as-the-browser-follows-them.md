# 0022: Redirects follow the browser's rules, inside the Transport

- Status: Accepted
- Date: 2026-10-24
- Commits: (the wave-3 commit)

## Problem

`http.Client` follows redirects by Go's rules:
- it turns PUT and DELETE into GET on 301 and 302;
- it stops after 10 hops;
- it follows a cross-origin 3xx that a browser's CORS check would refuse.

The `cors-edge` capture ([report](../../harness/reports/cors-edge-2026-10-24.md))
shows all four browsers:
- keeping PUT/DELETE and their body on 301/302;
- turning only 303, and POST on 301/302, into GET;
- stopping after 20 redirects (21 requests);
- never requesting the next hop after a CORS-mode 3xx without
  `Access-Control-Allow-Origin`.

On a hop that became a GET, Safari keeps `Origin` and Chrome and Firefox
drop it.

## Options

- **Document Go's behaviour.** The wire differs on every PUT redirect and
  on long chains.
- **`NewClient` returns a client that follows redirects itself.** A new
  code path. `NewTransport` users still get Go's rules, and
  `CheckRedirect`, `Jar` and `Timeout` semantics would need copying.
- **The Transport corrects each hop `http.Client` builds** (it sees the
  previous request through `req.Response`).

## Decision

The Transport corrects each hop. It:
- restores the method, body (`GetBody`) and body headers by the Fetch
  rules: 301/302 keep every method but POST, 303 makes a GET (HEAD stays
  HEAD), 307/308 keep everything;
- refuses a request beyond the limit: 20 redirects for fetches, and for
  navigations `redirects.navigation_limit` (19 in Chrome, 20 in Firefox
  and Safari);
- returns `*CORSError` (with `Redirect` set) for a refused CORS 3xx,
  unless `IgnoreCORS`. This covers fetch(), XHR, elements with
  `crossorigin` and fonts, and requires `Access-Control-Allow-Credentials`
  when the request carries credentials.

`NewClient` sets a `CheckRedirect` that allows every redirect, so the
Transport's limit is the only one. Profile `redirects` holds:
- `method_change_origin`: the `Origin` rule on a hop that became a GET
  (`keep` Safari, `drop` Chrome and Firefox);
- `navigation_limit`;
- `hop_preflight`: whether a hop after a preflighted request is
  preflighted again even when simple (`inherit`, Firefox and Safari) or
  only by its own needs (`own`, Chrome);
- `navigation_site`: a navigation hop's `Sec-Fetch-Site` after a
  redirect to another site: the whole chain's (`chain`: Chrome, Firefox,
  Safari 26) or only from that redirect on (`restart`: Safari 27, whose
  page navigation sent to another site and back is `same-origin`).

Hop header order is chosen by redirect conditions on variants (0006);
a navigation's also by `cross_site_redirect` (Safari puts `Referer`
before `Sec-Fetch-Dest` on a hop to another site than the URL before
it). Typed navigations take site `none`. Each hop's Referer applies the
policy to the Referer the request before sent, so a chain that left the
page's origin sends only the origin back on it (every browser). A hop
the captures do not cover is `ErrNotMeasured` (0008).

## Evidence

The `cors-edge`, `redirects2` and `nav-redirect-*` captures
([reports](../../harness/reports/cors-edge-2026-10-24.md),
[redirects2](../../harness/reports/redirects2-2026-10-24.md)), all four
browsers, identical across samples but for one hop: in
`nav-redirect-page`, each Safari's first (cold) sample sent the hop back
to the page's site (`nav-302.cs-so.hop1`) on a new connection, ordered as
a same-site hop (Safari 27: with `cross-site`). kindsgen leaves that one
request out (`sampleGaps`); the e2e test accepts it and logs it.

## Consequences

- No API change. It works for any `http.Client` wrapping a Transport.
- A caller's own `CheckRedirect` sees Go's view of each hop (a PUT
  answered 301 appears as a GET in `via`), and the Transport fixes it on
  the wire.
- A redirected body without `GetBody` is an error, not a dropped body.
- Chrome's error page reloads the last URL after its limit; that is
  browser UI and not reproduced.

## Revisit when

- A link's navigation (with user activation, so `Sec-Fetch-User`) is
  redirected to another origin: the captures have script and form
  navigations only, so those hops are `ErrNotMeasured` in Chrome and
  Firefox.
- Styles, fonts and preloads redirected without
  `Access-Control-Allow-Origin`: the CORS check is assumed from the spec
  there, not captured.
