# 0022: Redirects follow the browser's rules, inside the Transport

- Status: Accepted; amended 2026-10-26 (`navigationOrigin`, and user navigations' `Sec-Fetch-User`, from the `nav-redirect-*` captures)
- Date: 2026-10-24
- Commits: `fd5e825`; amended in `0a4a442`

## Problem

`http.Client` follows redirects by Go's rules:
- it turns PUT and DELETE into GET on 301 and 302;
- it stops after 10 hops;
- it follows a cross-origin 3xx that a browser's CORS check would refuse.

The `cors-edge` capture ([report](../../internal/harness/reports/cors-edge-2026-10-24.md))
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
  navigations `redirects.navigationLimit` (19 in Chrome, 20 in Firefox
  and Safari);
- returns `*CORSError` (with `Redirect` set) for a refused CORS 3xx,
  unless `IgnoreCORS`. This covers fetch(), XHR, elements with
  `crossorigin` and fonts, and requires `Access-Control-Allow-Credentials`
  when the request carries credentials.

`NewClient` sets a `CheckRedirect` that allows every redirect, so the
Transport's limit is the only one. Profile `redirects` holds:
- `methodChangeOrigin`: the `Origin` rule on a hop that became a GET
  (`keep` Safari, `drop` Chrome and Firefox);
- `navigationLimit`;
- `hopPreflight`: whether a hop after a preflighted request is
  preflighted again even when simple (`inherit`, Firefox and Safari) or
  only by its own needs (`own`, Chrome);
- `navigationSite`: a navigation hop's `Sec-Fetch-Site` after a
  redirect to another site: the whole chain's (`chain`: Chrome, Firefox,
  Safari 26) or only from that redirect on (`restart`: Safari 27, whose
  page navigation sent to another site and back is `same-origin`).

Hop header order is chosen by redirect conditions on variants (0006);
a navigation's also by `crossSiteRedirect` (Safari puts `Referer`
before `Sec-Fetch-Dest` on a hop to another site than the URL before
it). Typed navigations take site `none`. Each hop's Referer applies the
policy to the Referer the request before sent, so a chain that left the
page's origin sends only the origin back on it (every browser). A hop
the captures do not cover is `ErrNotMeasured` (0008).

## Evidence

The `cors-edge`, `redirects2` and `nav-redirect-*` captures
([reports](../../internal/harness/reports/cors-edge-2026-10-24.md),
[redirects2](../../internal/harness/reports/redirects2-2026-10-24.md),
[nav-click](../../internal/harness/reports/nav-click-2026-10-26.md)), all four
browsers (Safari 26 without clicks), identical across samples but for
one hop: in `nav-redirect-page`, each Safari's first (cold) sample sent
the hop back to the page's site (`nav-302.cs-so.hop1`) on a new
connection, ordered as a same-site hop (Safari 27: with `cross-site`).
kindsgen leaves that one request out (`sampleGaps`); the e2e test
accepts it and logs it.

## Consequences

- No API change. It works for any `http.Client` wrapping a Transport.
- A caller's own `CheckRedirect` sees Go's view of each hop (a PUT
  answered 301 appears as a GET in `via`), and the Transport fixes it on
  the wire.
- A redirected body without `GetBody` is an error, not a dropped body.
- Chrome's error page reloads the last URL after its limit; that is
  browser UI and not reproduced.

## Revisit when

- A navigation chain changes origin more than once: `navigationSite`
  and `navigationOrigin` were measured on one redirect each.
- Styles, fonts and preloads redirected without
  `Access-Control-Allow-Origin`: the CORS check is assumed from the spec
  there, not captured.

## Amendment 2026-10-26: form POSTs and clicked links

Profile `redirects` gains:
- `navigationOrigin`: when a form POST's `Origin`,
  kept by a 307 or 308, becomes `null`: once any redirect went to another
  origin than the URL before it (`cross_origin`: Chrome, Safari), or only
  once the chain is tainted as the Fetch standard has it (`taint`:
  Firefox). mimicry sent the page's origin before the `nav-redirect-form`
  and `nav-redirect-click` captures showed this; the Fetch standard's
  rule alone (one code path) would have been wrong for three profiles.

A user's navigation (a clicked link or submit button, a typed URL) sends
`Sec-Fetch-User` on its first leg and every hop; a script's sends none,
and its caller says so with a nil `Sec-Fetch-User` header. The captures
(`nav-redirect-click`, the capture tool clicking as a user does) show a
click ordered as the script's navigation of the same condition with
that one header added (Chrome, Firefox) or nothing added (Safari), so
kindsgen merges both into one variant with a `sec-fetch-user` slot
rather than adding an activation condition. A user's navigation whose
variant has no slot (measured on a script's only) is `ErrNotMeasured`,
and over HTTP/1.1 where its HTTP/1.1 list has none.
