# 0034: Request plans: RoundTrip's path, without sending

- Status: Accepted
- Date: 2026-10-26
- Commits: `33fb830`

**Summary.** `Transport.Plan(req)` returns what a request would send
without sending it or changing any state: per-protocol request lines and
headers (each with its own error), the protocol it would take now, the
kind, profile variant, preflight, overrides and dropped headers. It is
built from the same functions `RoundTrip` sends with, so the preview cannot
drift from the wire, which makes porting and debugging header problems
possible before anything goes out. A plan is a snapshot: cookies,
connections or a cached preflight that change before `RoundTrip` change
what is sent.

## Problem

What a request sends exists only on the wire. tls-client users print their
own header literal; in mimicry the final lines are computed (0005, 0006,
0021, 0030): the kind's list or a variant, values the page decides, the
jar's cookies, `Config.Header`, placements, then each connection's writer
(HTTP/1.1 casing, Host and framing; HTTP/2 and HTTP/3 pseudo-headers,
split cookies, `alt-used`). Porting is guesswork against a capture proxy,
and the porting traps (a copied `sec-fetch-site` or `Referer` silently
replacing the computed one) report nothing
([coming from tls-client](../concepts/coming-from-tls-client.md),
weakness 3). Whether a request is `ErrNotMeasured` can depend on the
protocol the server picks (weakness 5).

## Options

How the preview is computed:

- **A second resolver that describes the request.** Cheap to write, and
  wrong the first time either side changes: the preview's value is that it
  is what goes out.
- **Run RoundTrip against a recording fake engine.** Shares the root's
  path but not the connections' writers, which make half the lines; and
  RoundTrip changes state as it goes (the jar's last-access times, expired
  cookies dropped, the preflight cache, the last navigation, a redirect
  hop's body opened from `GetBody`).
- **Refactor so both call the same functions, and let them differ only
  where sending changes state.** Chosen. In the root: `outgoing` (the
  redirect hop, placements, `resolve`, the preflight decision) and `ready`
  (context, headers, cookies), with a `use` that is `toSend` or `toPlan`
  at the two places they must differ: `browserHop` opens the previous
  request's body only to send (a plan carries an unread stand-in with the
  same length), and the jar is read with `Cookies` or `Peek`. The
  preflight is decided by `preflightFor` (need, cache, resolve) and sent
  by `sendPreflight`. In the engine: `begin` (URL checks, route, unasked
  proxy credentials) and `tcpRefusal`, `h3Refusal` and `pickWay` (quicFor's
  decision, now a pure function it switches on). In the connections:
  `h1.Writer.Head`, `h2.Head`, `h3.Head`, the same builders
  `Conn.RoundTrip` writes with.

What it returns:

- **One header list.** The request's protocol is often unknown before
  connecting (ALPN, Alt-Svc, a QUIC race), and the lists differ by
  protocol: HTTP/1.1 casing, Host, Connection and framing; HTTP/3's
  dropped headers and `alt-used`; a variant measured over HTTP/2 only has
  no HTTP/1.1 list at all.
- **A list per protocol, each with its own error, plus the protocol the
  request would take now.** Chosen: `Plan.HTTP2`, `HTTP1`, `HTTP3` are
  `Lines{RequestLine, Headers, Err}`; `Protocol` names one as
  `resp.Proto` does (`"HTTP/2.0"`, `"HTTP/1.1"`, `"HTTP/3.0"`), so a
  caller compares it with the response, or is empty when it is decided
  when connecting. A protocol that cannot carry the request shows why in
  its `Err`; `Plan` itself fails only as `RoundTrip` would before sending:
  when every protocol the request may take now fails (that protocol's
  error, the preflight's first), or earlier (`ErrNotMeasured`,
  `ErrPlacement`, a combination no browser makes).

What else a plan says: the kind; `Variant`, a JSON path into the profile
(`kinds.fetch.variants[4]`, `kinds.fetch.headers`, `headers`) with
`Condition`, the request's condition in `profile.Variant`'s JSON names,
so a fallback to the kind's own list shows; `Preflight`, a plan of its
own, nil when none is needed or the cache allows the request;
`Overrides` (a header the caller or `Config.Header` set in place of a
profile default, a computed value or the jar's cookies:
`headerorder.Replaced`, Build's rules reported) and `Dropped` (profile
headers `HeaderOrderKey` leaves out); `String` for logs.

Names: `Plan` because the code already calls the resolved request a plan
(`headerorder.Plan`) and the tls-client page proposed `Transport.Plan`;
`Lines` and `Header` rather than `[]struct{Name, Value}` because each
protocol needs its own error and HTTP/1.1 its request line; protocol names
as `resp.Proto` writes them.

A `*CORSError` cannot be known in advance: it is the server's answer to the
preflight or to a redirect. `Plan` never returns one; `Preflight` shows
what would ask.

## Decision

`func (t *Transport) Plan(req *http.Request) (*Plan, error)` as above,
side-effect free: no dial, no DNS lookup (coalescing by lookup is left
undecided), no change to the jar (`cookies.Jar.Peek`), the last
navigation, the preflight cache, the pools (`pool.Peek`), tickets
(`Tickets.Has`) or HTTP/3 state (origins are read, never created). The
engine reports the protocols a request may take now (`protocolsNow`:
HTTP/3 as `quicFor` would decide it, then the pool's connection, plain
HTTP's HTTP/1.1, or HTTP/2 and HTTP/1.1 by ALPN). Before an origin's
Alt-Svc is known, the HTTP/3 lines name the origin's own port in
`alt-used` (Firefox).

## Evidence

`internal/harness/e2e`'s page replays plan every request just before sending it
(`planEvery`, `checkPlans`): the kinds, headers-order, cors, cors-edge,
redirects2 and nav-redirect, cookies, cookies-edge and h3 pages, every
profile. For each request the plan's lines for the protocol the server
saw equal what it received (HTTP/1.1 lines verbatim, HTTP/2 HPACK and
HTTP/3 QPACK fields decoded), its preflight's too, the planned protocol
when decided is the one used, `Plan` fails exactly when `RoundTrip` fails
before sending, the jar (cookies and last-access times) is unchanged by
`Plan`, and every request the server received was planned.

Mutation checks (2026-10-26), each reverted: a header value changed only
in `h2.Conn.head`, only in `h1.Conn.writeRequest`, or one cookie dropped
only in the send branch of `cookieLine` fails the plan check (hundreds of
"planned … sent …" errors); `Plan` reading the jar with `Cookies` fails
the jar check; the same change inside `h2.buildHead`, which both paths
call, leaves the plan check passing and fails the browser comparisons:
the plan cannot drift from the send path because there is no second path.

## Consequences

Porting and debugging read the lines, variant and overrides before
anything is sent; `ErrNotMeasured` that depends on the protocol shows per
protocol up front. The shared path costs RoundTrip nothing: the `use`
switch is two comparisons, and the per-protocol head builders are the
connections' own. A plan is a snapshot: cookies a response sets, a
connection opened or closed, a navigation, or a preflight answer cached
between `Plan` and `RoundTrip` change what goes out.

## Revisit when

- Callers want the plan of every request as it is sent (a
  `Config.OnRequest` hook), not only on demand.
- WebSocket handshakes need a plan (`DialWebSocket` resolves its own way).
