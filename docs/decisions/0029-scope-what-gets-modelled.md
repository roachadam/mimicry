# 0029: Model what anti-bot systems check or users notice

- Status: Accepted; amended 2026-10-28 by [0036](0036-fast-quic-fallback.md) (fast failure-path timing by default) and [0037](0037-coalescing-opt-out.md) (faster defaults for costly, low-risk behaviours)
- Date: 2026-10-25
- Commits: `0e76f62`

## Problem

Captures keep turning up differences between mimicry and the browsers.
Some matter: a ClientHello extension in the wrong place changes the JA4
hash on every connection. Most do not. Examples are the order of probes
in a QUIC attempt that never gets an answer, how long Firefox waits
before closing a WebSocket, or how Safari spaces its flow-control credit
during a 50 MiB download. Each one costs a capture scenario, a generator
rule, a profile field and an e2e check, and the profiles and the engine
get harder to read with every rule added.

The library has one purpose: automating ordinary user actions on
websites (searching, adding to a cart, checking out) without being
flagged by anti-bot systems such as Cloudflare, Akamai, DataDome, HUMAN
and Kasada. We needed a rule for deciding which differences are worth
closing.

## Options

- **Model every difference the captures show.** This is where the
  evidence rule ([0003](0003-evidence-rule.md)) leads if nothing limits
  it. It never ends: each new scenario finds more. It also spends most of
  the work on failure paths and timing that no vendor looks at, and it
  adds behaviour that callers cannot see or predict.
- **Model only the first request of a connection** (the ClientHello, the
  HTTP/2 preface, the first header block). These are the classic
  fingerprints, but cookies, redirects, preflights and connection reuse
  are checked too. A client that is correct only up to its first request
  fails the request-flow checks.
- **Model what a vendor plausibly checks or a user would notice.** Each
  item has to give a reason before it is worked on.

## Decision

Model a behaviour only if one of these holds:

- **An anti-bot vendor plausibly checks it.** The layer these systems
  check is:
  - the TLS ClientHello (JA3/JA4, extension order, GREASE, ALPN,
    resumption);
  - the HTTP/2 fingerprint (SETTINGS, WINDOW_UPDATE, PRIORITY,
    pseudo-header order: the Akamai fingerprint), and its HTTP/3
    equivalent;
  - header names, order and values per request type (`sec-fetch-*`,
    `sec-ch-ua`, `Origin`, `Referer`, `priority`);
  - cookies: which are sent, in what order, and whether the ones the
    site set come back;
  - request-flow consistency: whether redirects, preflights and
    connection reuse look like a browser loading pages.
- **A user would notice it.** A stall, a failed request, an error where a
  browser would succeed, or a wrong result.

Everything else is deferred: failure-path timing, probe layouts, credit
timing on long transfers, behaviour in states that only a broken network
or an unusual server produces. A deferred item is not forgotten. It is
written down in [the backlog](../backlog.md) with what differs, when it
shows on the wire, how relevant it is to detection, and what closing it
would take, so later hardening can pick it up.

(The maintainer's decision, 2026-10-25.)

## Evidence

Nothing new was captured for this decision. It follows from the captures
taken so far and the gaps they left. The backlog lists more than a
hundred such gaps; most are rated low or none for detection. The
existing work already leans this way: decision
[0011](0011-connection-partitioning.md) models Firefox's connection
grouping only where a rule is measured, and decision
[0022](0022-redirects-as-the-browser-follows-them.md) leaves Chrome's
error-page reload alone as browser UI.

## Consequences

- A new capture or model cycle has to state its reason: the check a
  vendor plausibly runs, or the stall or failure a user would hit. "The
  browser does it" is not enough on its own.
- The evidence rule ([0003](0003-evidence-rule.md)) and the unmeasured
  error ([0008](0008-unmeasured-is-an-error.md)) still hold for whatever
  is modelled. This decision limits what gets modelled. It does not
  lower the bar for how it is modelled.
- The e2e tests keep their pinned gap tables (`sampleGaps`,
  `partitionGaps`, `h3EdgeGaps` and the others). A pinned gap is now
  expected to be in the backlog too.
- Deferring makes some failure paths behave unlike the browser (a QUIC
  attempt that never gets an answer, a WebSocket whose server never
  closes it). Those paths still have to end: every one is bounded by a
  timeout or a fallback, even where the timing is not the browser's.
- [`docs/concepts/what-mimicry-does.md`](../concepts/what-mimicry-does.md)
  lists everything mimicry does on the wire besides the caller's own
  requests. Any new automatic behaviour has to be added there, together
  with the option that turns it off or the backlog entry saying there is
  none.

## Revisit when

A vendor is seen flagging something this rule deferred (a deferred item
moves up the backlog), or the library gets a use beyond automating
ordinary user actions, such as research that needs the browsers' failure
paths reproduced.

## Amendment 2026-10-28: faster failure-path timing

By [0036](0036-fast-quic-fallback.md): failure-path
timing may be faster than the browser's by default when it is a stall a
user notices and is visible only where QUIC fails; the browser's timing
stays available as an option (`Config.BrowserQUICFallback`). The test that
it is visible only there is an e2e replay in both modes, with every
difference listed (`h3EdgeFastGaps`).

## Amendment 2026-10-28: faster defaults for costly, low-risk behaviours

The rule, recorded in [0037](0037-coalescing-opt-out.md)
(2026-10-28): when a browser behaviour costs significant performance and
is a low-ish detection risk, the default is the faster setting, documented
plainly, with a `Config` option for the browser's. HTTP/2 coalescing was
weighed against it and kept: it is faster on clean networks and slower
only under loss, so the browser's stays the default and
`Config.DisableCoalescing` turns it off.
