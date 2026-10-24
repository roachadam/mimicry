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
  rules;
- refuses a 21st request;
- returns `*CORSError` (with `Redirect` set) for a refused CORS 3xx,
  unless `IgnoreCORS`.

`NewClient` sets a `CheckRedirect` that allows every redirect, so the
Transport's limit is the only one. The `Origin` rule on a hop that became
a GET is a profile field, `redirects.method_change_origin` (`keep` Safari,
`drop` Chrome and Firefox).

## Consequences

- No API change. It works for any `http.Client` wrapping a Transport.
- A caller's own `CheckRedirect` sees Go's view of each hop (a PUT
  answered 301 appears as a GET in `via`), and the Transport fixes it on
  the wire.
- A redirected body without `GetBody` is an error, not a dropped body.

## Revisit when

- A capture covers what these rules do not: 307/308, PATCH, HEAD; XHR and
  `<img crossorigin>` redirects; a navigation chain longer than 20; a form
  POST answered 302 in Safari.
- Header order on GET redirect hops gets its variant condition (listed as
  a gap).
