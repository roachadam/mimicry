# Evidence ledger

Every claim the built-in profiles make, where it comes from, and what keeps
it true. Profiles: `chrome-155-macos`, `firefox-158-macos`, `safari-27-macos`
(captured on this Mac), `safari-26-macos` (captured on a second Mac, macOS
26.2). Last reviewed 2026-10-23.

## Evidence classes

| Code | Meaning |
|---|---|
| **W** | Seen on the wire: the browser against our capture server, three samples (three browser launches for Chrome and Firefox), identical across samples unless the row says otherwise. Captures in `harness/testdata/captures/`. |
| **W+** | **W**, and also stable across the server scenarios (`set-cookie`, `large-settings`, `small-limits`, `huge-table`, `explicit-table`, `slow-idle`, `http1`) and, where noted, the six replayed real pages. |
| **F** | Fitted: not visible as such on the wire. A rule chosen to explain the traces, then verified by re-encoding every captured header block byte for byte (`TestHPACKReproducesBrowserBlocks`). |
| **K** | Measured on a different kind of request than the profile describes (e.g. on `fetch()` uploads, used for navigations). |
| **C** | Copied from another profile, not measured for this one. |

No claim rests on browser source code or on another client's presets alone:
those were leads only. Where another client disagreed with the wire, the wire
won (httpcloak's single joined Safari cookie field, for one: every Safari 27
sample splits cookies).

## TLS ClientHello

All rows checked by `TestProfilesMatchCaptures` (mimicry's ClientHello
diffed field by field against the capture, extension order as the capture
says) and `TestClientSendsThePlannedHello` (the planned bytes are the bytes
sent).

| Claim (`tls.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 | Notes |
|---|---|---|---|---|---|
| `cipher_suites`, `compression_methods` | W+ | W+ | W+ | W+ | |
| `extension_order` | W: shuffled (8 connections, 8 orders, GREASE pinned first and last) | W: fixed | W: fixed | W: fixed, and identical to Safari 27's | Two installs on two macOS versions agree, so Safari's order is not per install. Shuffle uniformity: `TestShuffleIsUniform`, `TestShufflePinsGREASE`. |
| `extensions` (types, contents: groups, key shares, signature algorithms, versions, ALPN, ALPS, point formats, PSK modes, certificate compression, record size limit, raw data such as Chrome's trust anchors 0xca34) | W+ | W+ | W+ | W+ | Chrome's trust anchor list was identical in all samples. |
| GREASE ECH (0xfe0d) payload | presence and position W; payload is BoringSSL's, matches | presence and position W; **payload differs**: Firefox sends two cipher suites and a 223-byte payload | — | — | The schema records only the type. Known gap. |
| key share material, random, session ID | generated per connection | | | | Values, not claims. |
| `resumption` (TLS 1.3: full hello minus `drop`, `pre_shared_key` last, single-use tickets, no 0-RTT; `tls12`: ticket in `session_ticket`, used as the policy says) | W: drops nothing; `tls12: reuse` | W: drops `session_ticket`; `tls12: single_use` | W: none (never resumes, TLS 1.3 or 1.2) | W: none, as Safari 27 | `resume`, `resume-tls12` scenarios ([report](../harness/reports/resume-2026-10-24.md)); `TestResumptionMatchesBrowser` (used connections and resumptions per host, every resumed hello diffed) |

Cross-checked outside our server: Firefox 158's JA3, and Safari 27's JA3, JA4
and Akamai fingerprints, match what tls.peet.ws reports for the same
browsers.

## HTTP/2 connection

| Claim (`http2.…`) | Chrome 155 | Firefox 158 | Safari 27 / 26 | Checked by |
|---|---|---|---|---|
| `settings` (ids, values, order) | W+ | W+ | W+ | `TestProfilesMatchCaptures` |
| `window_update` (connection credit after the preface) | W+ | W+ | W+ | `TestProfilesMatchCaptures` |
| `priorities` (PRIORITY frames at connection start) | W: none | W: none | W: none | `TestProfilesMatchCaptures` |
| `headers_priority` | W: exclusive, weight 255 | W: weight 41 | W: none | `TestProfilesMatchCaptures` |
| `chain_priorities` (each request depends exclusively on the previous) | W (capture page traces) | — | — | `TestChainedPriorities` (unit); not compared against the trace end to end |
| `pseudo_header_order` | W+ | W+ | W+ | `TestProfilesMatchCaptures` |
| `first_stream_id`, `stream_window_update` | — (1, none) | W+: stream 3, 12451840 per stream | — | `TestProfilesMatchCaptures`, `TestFirstStreamAndStreamWindowUpdate` |
| `max_data_frame` (upload DATA cap) | W+: 16375 | W+: 16000 | W+: 16374 | `TestConnectionMatchesBrowserTrace` (largest frame equal), `TestDataFrameCap`; holds whatever frame size the server allows (`large-settings`) |
| `stream_update_threshold`, `connection_update_threshold` | W | W | W | `TestConnectionMatchesBrowserTrace`: every increment within the browser's own observed range ± one frame. Firefox's stream credit and Safari's connection overshoot are timing-driven; only the range is matched. |
| `window_update_interval_ms` (credit returned after 5 s however little) | W (`slow-idle`) | — | — | `TestWindowUpdateInterval` (unit, fake clock) |
| `idle_ping_ms` (PING after 10 s idle, close if unanswered) | W (`slow-idle`) | — (never pings) | — (never pings) | `TestIdlePing` (unit, fake clock) |
| `split_cookie` (one HPACK field per cookie) | W+ | W+ | W+ | `TestCookiesMatchBrowserTrace`, `TestCookieSplitAndOrder` |

## HPACK

All rows **F**, checked by `TestHPACKReproducesBrowserBlocks`: every header
block of every sample of every scenario, and of every connection of the six
replayed pages (3516 checks), re-encoded byte for byte.

| Claim (`http2.hpack.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `without_indexing` | `:path`; `:method` (seen only on `OPTIONS` preflights in replays: GET and POST are exact static-table matches, so the rule cannot show for them) | `:path` | `:path`, `content-length` | as Safari 27, verified on its own scenarios |
| `never_indexed` | — | `authorization` (replays) | `authorization` (replays) | `authorization` **C** from Safari 27: no Safari 26 replay captured |
| `never_index_below` | — (indexes every cookie) | `cookie` under 20 bytes (19 never, 20 indexed) | same | same |
| `literal_on_exact_match` | — | yes | — | — |
| `name_match` | static first | highest index | static first | static first |
| `huffman` | when shorter | always | when shorter | when shorter |
| `max_table_size` | server's | server's | 4096 (`huge-table`) | 4096 |
| `size_updates` | on change (`huge-table`, `explicit-table`) | always | always | always |

## Request headers

| Claim | Evidence | Checked by |
|---|---|---|
| `headers`: navigation header names, order, default values over HTTP/2 | W+ (Safari 26: W+, differs from 27 only in user agent and `accept-encoding`) | `TestProfilesMatchCaptures` |
| `referer`, `cookie` slots in `headers` | W (the `/nav` navigation) | over HTTP/1.1 by `TestHTTP1MatchesBrowserTrace`; over HTTP/2 not compared end to end |
| `content-length` slot in `headers` | **K**: placed where each browser put it on `fetch()` uploads (Chrome first, Firefox after `referer`, Safari before `accept-language`). A navigation POST takes `variants` instead (below) | `TestKindsMatchBrowserTraces` (form POST) |
| `variants`: the navigation's order for a form POST (Chrome adds `cache-control: max-age=0`; Firefox moves `referer`, `cookie`; Safari uses its CORS-checked order) | W (`kinds`, `kinds-http1`, Safari 26 included) | `TestKindsMatchBrowserTraces` |
| `accept-language` value | W, but it is this machine's language setting, not a browser constant | `TestProfilesMatchCaptures` |
| `http1.headers`: HTTP/1.1 names as sent (casing), order, values, Host and Connection positions | W+ (`http1` scenario, every browser and Safari 26) | `TestHTTP1MatchesBrowserTrace` (`/` and `/nav`, line for line), `TestRequestHeadFollowsHTTP1Section` |
| `Content-Length` slot in `http1.headers` | **K** (`fetch()` uploads) | `TestRequestHeadFollowsHTTP1Section` (Chrome, unit) |

## Cookies

`cookies` section ([decision 0012](../docs/decisions/0012-cookie-rules-in-profiles.md)),
from the `set-cookie`, `kinds` and `cookies` captures
([report](../harness/reports/cookies-2026-10-24.md)). Checked end to end by
`TestCookiesScenarioMatchesBrowser` (the cookies page walked through
mimicry: every probe's cookies, in order), `TestKindsMatchBrowserTraces`
(every kinds request's cookies) and `TestCookiesMatchBrowserTrace` (crumbs,
HPACK representation); the shared RFC 6265bis rules by `cookies` unit tests.
Safari 26 (captured on the second Mac, 2026-10-24): W from its own `set-cookie`
and `cookies` captures, all as Safari 27 except `redirects`: **`chain`**
(Safari 26 sends cookies on cross-site → same-site redirect hops; 27 blocks
them).

| Claim (`cookies.…`) | Chrome 155 | Firefox 158 | Safari 27 |
|---|---|---|---|
| `unset_samesite` | W: `lax` (third-party unset cookie not sent; withheld on cross-site POST only past the window) | W: `none` | W: `none` |
| `lax_allows_unsafe_seconds` | 120: a seconds-old cookie sent on a cross-site form POST is W; the 120-second end is Chromium's documented value, not captured | — | — |
| `none_requires_secure` | W | W | W: `false` (kept and sent) |
| `third_party` | W: `samesite`; `Partitioned` keyed by top-level site (withheld under site C) | W: `partition_all` (nothing third-party crosses top-level sites) | W: `partitioned_only` (blocking at store or send is not observable; the jar does both) |
| `redirects` | W: `ignore` | W: `chain` | W: `block_after_cross_site` (cross-site → same-site and → same-origin hops alike) |
| `nameless` | W: `value` (`knoeq`) | W: `name` (`knoeq=`) | W: `drop` |
| `latin1_values` | W: no | W: no | W: yes (`cafÃ©`) |
| `order` | W+: `rfc` (path length, then creation) | W+: `rfc` | W+: `domain_name` (path length, `Domain` cookies first, name); ordering between `Domain` cookies of different levels not captured |

## Request kinds

Derived from the `kinds` and `kinds-http1` captures by `harness/cmd/kindsgen`
(it refuses captures that disagree), for every profile; Safari 26's equal
Safari 27's apart from its user agent and `accept-encoding`.
All rows checked by `TestKindsMatchBrowserTraces`: every request of the kinds
page, made through mimicry as its kind from the page, equals the browser's
(names, order, values, HTTP/2 weight; over HTTP/1.1 line for line), cookies
included. Where the browser's own samples differ, any form it sent passes and
the variance is logged: one Chrome HTTP/1.1 sample sends `Sec-Fetch-User` on
cross-origin frames and the form POST, the others do not (activation state).
`kb_part`, set by an image the page loads, races Safari's CSS font request
(`font-css.cs`): Safari 27 and Safari 26 over HTTP/1.1 send it in some
samples, Safari 26 over HTTP/2 in none of six. The test accepts a request
that differs only by that cookie, and logs it.

| Claim | Evidence | Notes |
|---|---|---|
| `kinds.*.headers`, `http1`: names, order, values per kind | W | `font` is the CSS `@font-face` load: Firefox's sends `accept-encoding: identity`, unlike its `FontFace` loads |
| `kinds.*.variants`: order by method, site relation, CORS check, redirect | W | Safari has many (a CORS-checked request starts `sec-fetch-site, accept, origin`; a cross-site beacon differs from a same-site one; its redirected fetches drop `sec-fetch-*`, `accept-language` and `priority`). Every browser orders a redirect's request its own way. Firefox also for cross-origin fetches and preload fonts. A combination not captured falls back to the kind's default order |
| `kinds.*.weight` | W | Chrome and Firefox; Safari sends no priority |
| `kinds.fetch.high`, `.low`; `kinds.injected_image.high` | W | Other kinds' fetchpriority is not measured: asking for it is an error |
| `*` (where page-set headers go) | W (`fetch-custom`) | |
| `context.storage_access` | W: Chrome `active`, Firefox `none`, Safari none | Sent on credentialed cross-site requests |
| `context.same_origin_element_origin` | W: Chrome, Safari | Origin on same-origin CORS element requests |
| `context.stylesheet_referrer` | W: Chrome, Firefox | Safari refers CSS fonts to the page; only same-origin fonts captured, at `/` |
| `context.anonymous_connections`, `frame_connections` | W: Chrome | Requests without credentials, and a cross-site frame's navigation, go on connections of their own; checked by `TestKindsMatchBrowserTraces` (which requests share a connection, every sample). Safari: one connection per host, as mimicry's default. Firefox: see below |
| Referer by strict-origin-when-cross-origin; Origin per the Fetch standard; credentials per mode | W where captured, otherwise the standard | The page sits at `/`, so full URL and origin coincide except for the CSS font |
| After a redirect: sec-fetch-site the chain's most distant relation, `Origin: null` once the chain is tainted, the page's Referer (not `http.Client`'s), storage access by the URL's own relation | W (`fetch-redirect`, `image-redirect`, both directions) | |

## Behaviour in code, not in profiles

| Behaviour | Evidence | Checked by |
|---|---|---|
| A fully read response is not reset (no RST_STREAM; waits for END_STREAM up to 10 s) | W: no browser resets streams it read | `TestClosingFullyReadBodyDoesNotReset` |
| Uploads read in 65535-byte chunks | W for the first 64 KiB of Chrome and Safari uploads; later chunks and all of Firefox's are not modelled | `TestConnectionMatchesBrowserTrace` (largest frame only) |
| HTTP/1.1-only servers get parallel connections at once; HTTP/2 candidates wait for the first | W (`resume`: three parallel requests, three connections opened together) | `TestResumptionMatchesBrowser` |
| HelloRetryRequest cookie placed at a random non-pinned position (uTLS fork patch 2) | **not Chrome's mechanism**: Chrome places it by its per-handshake permutation | known gap |

## Not covered by any claim

What a profile does not describe, so mimicry does not imitate it: CORS
preflights; Firefox's connections for credential-less requests (their own
to the same-site host, shared to the page's own and the cross-site host, in
every sample; no rule found); connections for what a cross-site frame
itself loads; how many parallel connections a page opens; Chrome's
mid-load re-prioritisation of a script and chained dependencies between
concurrent subresources (not compared); `FontFace` loads; `crossorigin`
stylesheets; ECH payloads; HTTP/3; the TCP/IP fingerprint.

## Keeping it true

Change a profile, change its row. A new browser version starts as a copy of
the previous one (see `AGENTS.md`): mark what it copied **C** until its own
captures pass the tests above.
