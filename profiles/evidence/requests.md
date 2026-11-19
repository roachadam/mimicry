# Request headers

Each claim below gives its evidence and the test that checks it.

## Navigation headers over HTTP/2

- **`headers`: navigation header names, order, default values over HTTP/2**
  - Evidence: W+.
  - Safari 26: W+. It differs from 27 only in user agent and `accept-encoding`.
  - Checked by: `TestProfilesMatchCaptures`.
- **`referer`, `cookie` slots in `headers`**
  - Evidence: W.
    - Over HTTP/1.1: the `/nav` navigation.
    - Over HTTP/2: `cookies`, `c-nav-get.so`, a script navigation carrying both. 3 samples each, all alike.
  - Checked by:
    - Over HTTP/1.1: `TestHTTP1MatchesBrowserTrace`.
    - Over HTTP/2: `TestCookiesScenarioMatchesBrowser`. It compares HPACK-decoded lines against every sample.
    - Swapping the two slots in Chrome's or Firefox's `headers` fails it.
- **`content-length` slot in `headers`**
  - Evidence: **K**. Placed where each browser put it on `fetch()` uploads.
    - Chrome: first.
    - Firefox: after `referer`.
    - Safari: before `accept-language`.
  - A navigation POST takes `variants` instead (below).
  - Checked by: `TestKindsMatchBrowserTraces` (form POST).
- **`accept-language` value**
  - Evidence: W. But it is this machine's language setting, not a browser constant.
  - Checked by: `TestProfilesMatchCaptures`.

## Navigation variants

- **`variants`: the navigation's order for a form POST to its own origin**
  - Chrome adds `cache-control: max-age=0`.
  - Firefox moves `referer`, `cookie`.
  - Safari uses its CORS-checked order.
  - Evidence: W (`kinds`, `kinds-http1`, Safari 26 included).
  - Checked by: `TestKindsMatchBrowserTraces`.
- **`variants` `{POST, cross-site, corsChecked}`: a cross-site form POST navigation**
  - Evidence: W (`cookies`, `c-nav-post.so`). HTTP/2 only, so no `http1` list.
  - Checked by: `TestCookiesScenarioMatchesBrowser`.
  - A same-site form POST was not captured.
    - It is an error: the navigation's own lists have no Origin slot.
    - The rule: `TestRefusedRequests`.

## HTTP/1.1 header lists

- **`http1.headers`: HTTP/1.1 names as sent (casing), order, values, Host and Connection positions**
  - Evidence: W+ (`http1` scenario, every browser and Safari 26).
  - Checked by: `TestHTTP1MatchesBrowserTrace` (`/` and `/nav`, line for line).
- **`Content-Length` slot in HTTP/1.1 lists**
  - Covers `http1` of kinds and navigation `variants`. `internal/h1` places it by the list.
  - Evidence: W, every profile, from `kinds-http1`, `kinds-plain`:
    - `fetch-json.{so,ss,cs}`: 7 bytes.
    - `beacon.{so,ss,cs}`: 1 byte.
    - `form-post.so`: 3 bytes.
    - Safari's preflights' `Content-Length: 0`.
  - The navigation's own `http1.headers` slot is **K** (`fetch()` uploads).
    - No measured request reaches it: a navigation POST takes `variants`.
  - Checked by: `TestKindsMatchBrowserTraces` (kinds-http1, kinds-plain: line for line, `Content-Length` included).
  - Mutation, 2026-10-24: moving `Content-Length` last in `internal/h1` (`-overlay`) fails:
    - `TestKindsMatchBrowserTraces` for all four profiles, both scenarios (`fetch-json`, `beacon`, `form-post.so`, Safari's `fetch-custom` preflights).
    - `TestHeadersOrderMatchesBrowser` (Safari's HTTP/1.1 preflights).

## Cookies

The `cookies` section ([decision 0012](../../docs/decisions/0012-cookie-rules-in-profiles.md)).

- **Sources**: the `set-cookie`, `kinds`, `cookies`, `cookies-edge` and `cookies-edge2` captures.
  - [report](../../internal/harness/reports/cookies-2026-10-24.md)
  - [cookies-edge, cookies-edge2](../../internal/harness/reports/cookies-edge-2026-10-24.md)
- **Checked end to end by**:
  - `TestCookiesScenarioMatchesBrowser`: the cookies page walked through mimicry; every probe's cookies, in order.
  - `TestCookiesEdgeMatchesBrowser` and `TestCookiesEdge2MatchesBrowser`: every cookies-edge and cookies-edge2 step's Cookie header against each sample.
  - `TestKindsMatchBrowserTraces`: every kinds request's cookies.
  - `TestCookiesMatchBrowserTrace`: crumbs, HPACK representation.
  - The shared RFC 6265bis rules: `cookies` unit tests.
- **Safari 26**: captured on the second Mac, on macOS 26, 2026-10-24.
  - W from its own `set-cookie`, `cookies`, `cookies-edge` and `cookies-edge2` captures.
  - All as Safari 27 except:
    - `redirects`: **`chain`**. Safari 26 sends cookies on cross-site → same-site redirect hops; 27 blocks them.
    - `secureLoopback`: **`false`**.

### Cookie rules per browser

| Claim (`cookies.…`) | Chrome 155 | Firefox 158 | Safari 27 |
|---|---|---|---|
| `unsetSamesite` | W: `lax` [1] | W: `none` | W: `none` |
| `laxAllowsUnsafeSeconds` | 120 [2] | — | — |
| `noneRequiresSecure` | W | W | W: `false` (kept and sent) |
| `thirdParty` | W: `samesite` [3] | W: `partition_all` [4] | W: `partitioned_only` [5] |
| `redirects` | W: `ignore` | W: `chain` | W: `block_after_cross_site` [6] |
| `nameless` | W: `value` (`knoeq`) | W: `name` (`knoeq=`) | W: `drop` |
| `emptyName` | W: `keep` [7] | W: `drop` [8] | W: `drop` (all five) |
| `latin1Values` | W: no | W: no | W: yes (`cafÃ©`) |
| `order` | W+: `rfc` [9] | W+: `rfc` | W+: `domain_name` [10] |
| `newValueRenews` | W: `true` [11] | W: `false` (keeps its place) | — [12] |
| `samesiteScheme` | W: `schemeful` [13] | W: `schemeless` [14] | W: `schemeless` [15] |
| `hostPrefixPath` | W: `attribute` [16] | W: `attribute` | W: `stored` [17] |
| `httpPrefix` | W: `true` [18] | W: `true` (both halves) | W: `false` [19] |
| `secureLoopback` | W: `true` [20] | W: `true` | W: `true`; Safari 26 W: `false` |
| `maxPerSite`, `evictTo` | W: 169 sent [21] | W: same 169 | W: no limit [22] |
| `partitionedFirst` | — [23] | — (same) | W [24] |
| `websocketUnpartitioned` | W: `false` [25] | W: `false` [26] | W: `false` [27] |
| WebSocket handshake cookies [28] | W [29] | W | W; Safari 26 W [30] |
| Nameless pair with `=` in value refused [31] | W [32] | W | W |
| Nameless value with prefix refused [33] | W [34] | — [35] | — (same) |

Notes:

1. Third-party unset cookie not sent. Withheld on cross-site POST only past the window.
2. A seconds-old cookie sent on a cross-site form POST is W. The 120-second end is Chromium's documented value, not captured.
3. `Partitioned` keyed by top-level site (withheld under site C).
4. Nothing third-party crosses top-level sites.
5. Blocking at store or send is not observable; the jar does both.
6. For subresources: cross-site → same-site and → same-origin image hops alike.
   - A navigation's hops through another site carry their cookies (`nav-redirect-{page,form,click}-cookies`, decision 0048).
7. From `cookies-edge2`, `nameless-probe`.
   - `=x`, `=ctl` sent as `x; ctl`.
   - `=__Secure-x`, `= __Host-x`, `=__secure-x` dropped: the prefix rule in any case, after a space.
8. All five dropped, the bare `=x` and the `Secure` `=ctl` too.
9. Path length, then creation.
10. Path length, `Domain` cookies first, name. Ordering between `Domain` cookies of different levels not captured.
11. From `cookies-edge`, `rot-probe-2`. A new value moves the cookie last; the same value again does not.
12. `domain_name` order hides creation; validation forbids it.
13. From `sch-to-http`, `sch-to-https`: `Strict` withheld both ways. `cookies-edge2` again.
14. `Strict` sent both ways. `cookies-edge2` again.
15. From `cookies-edge2`, plain-only port: `sch-to-http` over plain HTTP/1.1 carries `Strict`, `sch-to-https` too.
16. From `prefix-probe`: `__Host-` without `Path` dropped.
17. Kept, default path `/`. A non-`/` default path not captured.
18. From `prefix-probe`: `__Http-`, `__Host-Http-` without `HttpOnly` dropped.
    - From `cookies-edge2`, `http-probe`: without `Secure` dropped too.
19. Kept. `__Http-` without `Secure` kept too. `__Host-Http-` without `Secure` dropped by `__Host-`'s rule.
20. From `lo-probe`: `true` for `localhost`. Other loopback hosts from the Secure Contexts specification.
21. From `many-probe`: 200 set, the newest 169 sent.
    - 180, then down to 150: the numbers are Chromium's source; the wire shows 169.
    - LRU among equals not separable from oldest-first.
22. All 200 sent, 4,998 bytes.
23. Not shown either way. `Partitioned` cookies were the newest in every capture, so `rfc` order puts them last too.
24. From `websocket`, `websocket-h2`.
    - Every request carrying `ws_a_part` with `ws_a_lax` and `ws_a_none` sends `ws_a_part` first, against `domain_name`'s name order. Navigations and sockets alike.
    - Safari 26 W: same.
    - Path length against partition is not captured (every cookie at `/`). The jar puts partition first.
25. Cross-site socket: `ws_b_none; ws_b_part3p`, the credentialed-subresource rule.
26. `ws_b_part3p` only.
27. `ws_b_part3p` only.
    - Safari 26 W: **`true`** (`ws_b_part3p; ws_b_lax; ws_b_none`, all 6 samples, HTTP/1.1 and HTTP/2).
    - No browser sent `ws_b_part` (the socket host's own partition).
    - A `Strict` cookie on a cross-site socket is not captured (the jar withholds it).
28. A WebSocket handshake's cookies (`cookies.Request.WebSocket`) are a credentialed subresource's of its page. `ws://` and `wss://` count as `http://` and `https://`.
29. From `websocket`, `websocket-h2`.
    - Same-origin sockets carry all three `ws_a_*`.
    - `plain-so` from the `http://` page carries only the non-`Secure` `ws_a_lax`.
30. Set-Cookie on a handshake response is not captured.
31. A nameless pair whose value holds `=` is refused (code, every browser).
32. From `nameless-probe`: `=e_nlctl=1` and the prefixed ones dropped by all.
33. A nameless value starting with `__Secure-` or `__Host-` (any case, after whitespace) is refused (code).
34. From `cookies-edge2`, `nameless-probe`: `=__Secure-x`, `= __Host-x`, `=__secure-x` dropped; `=x` and `=ctl` kept.
35. `emptyName: drop`: no nameless cookie kept to compare.

## Request kinds

- **Derived** from the `kinds`, `kinds-http1`, `kinds-plain`, `cookies`, `cors` and `bodiless` captures by `internal/harness/cmd/kindsgen`.
  - It refuses captures that disagree.
  - Covers every profile.
  - Safari 26's equal Safari 27's apart from its user agent and `accept-encoding`.
- **Committed profiles**: `TestRegenerateReproducesProfiles` keeps them equal to what kindsgen derives from the committed captures.
- **Rows from the kinds pages**: checked by `TestKindsMatchBrowserTraces`.
  - Every request of the kinds page, made through mimicry as its kind from the page, equals the browser's.
  - Compared: names, order, values, HTTP/2 weight; over HTTP/1.1 line for line; cookies included.
- **Browser variance**: where the browser's own samples differ, any form it sent passes and the variance is logged.
  - One Chrome HTTP/1.1 sample sends `Sec-Fetch-User` on cross-origin frames and the form POST; the others do not (activation state).
- **Cookie race**: `kb_part`, set by an image the page loads, races Safari's CSS font request (`font-css.cs`). Of three samples each:
  - Safari 27 sends it in all over HTTP/1.1 and two over HTTP/2.
  - Safari 26 sends it in two over HTTP/1.1 and none over HTTP/2.
  - For that label only (`raceCookieLabels`), the test also accepts mimicry's request carrying the cookie where no browser sample does, if nothing else differs, and logs it.
- **Rows from the `cookies` capture**: checked by `TestCookiesScenarioMatchesBrowser` (every probe's full header lines and weight).
- **Rows only the `cors` capture shows**: checked by their derivation only.
  - `TestCORSMatchesBrowser` compares those requests' methods, paths, cookies and `Access-Control-Request-*` headers, not their header order.

### Kind lists and variants

- **`kinds.*.headers`, `http1`: names, order, values per kind**
  - Evidence: W.
  - Notes: `font` is the CSS `@font-face` load. Firefox's sends `accept-encoding: identity`, unlike its `FontFace` loads.
- **`kinds.*.variants`: order by method, site relation, CORS check, page-set headers, redirect**
  - For a redirect's request also `crossOriginRedirect` and `originBeforeRedirect`.
  - Evidence: W.
  - Notes:
    - Safari has many. A CORS-checked request starts `sec-fetch-site, accept, origin`. A cross-site beacon differs from a same-site one.
    - Safari's fetches redirected to another origin drop `sec-fetch-*`, `accept-language` and `priority`. Same-origin redirects reorder every header.
    - Every browser orders a redirect's request its own way.
    - Firefox also for cross-origin fetches and preload fonts.
    - A combination not captured falls back to the kind's default order.
      - Unless that order has no place for an Origin the request sends: then it is an error (`TestRefusedRequests`).
    - A redirect's request of a kind whose redirects were captured (fetch, image, injected image) takes only its exact variant, else it is an error.
- **`pageSet`**
  - A variant for requests carrying a header the page set itself, not one the kind's list names. It implies `corsChecked`.
  - Evidence: W.
    - `kinds`: `fetch-custom`.
    - `cors`.
    - `headers-order`: 60 fetch/XHR steps, 3 samples each, every profile.
  - Checked by: `TestKindsMatchBrowserTraces`, `TestHeadersOrderMatchesBrowser`.
  - Notes:
    - Firefox: the `*` slot, in the fetch and XHR base lists and its cross-site variants.
    - Chrome, Safari: only where `context.headerTable` cannot place them (redirects: Safari's). Elsewhere the table does and kindsgen drops the variant.
    - Chrome's redirected `cors` fetch: its base, the cross-site fetch redirected within its origin, `acao-redirect.cs.hop1`, is now a variant of its own.
- **`context.headerTable`**
  - Chrome: `blink`, page's first, own `sec-ch-ua*`/`user-agent`, `content-type` as the page's.
  - Safari 26/27: `cf`, own `accept`, `content-type`, `origin`, `referer`, `sec-fetch-*`, `user-agent`, `content-length` late.
  - Firefox: none.
  - Evidence: W + S.
    - W: `headers-order`, all 3 samples identical. `kinds`, `kinds-http1`, `kinds-plain`, `cors` fetches re-checked.
    - S: Blink `hash_table.h`, `case_folding_hash.h`, rapidhash; CoreFoundation `CFBasicHash`, `__CFStrHashCharacters`.
  - Checked by: `TestHeadersOrderMatchesBrowser`, `TestRegenerateReproducesProfiles`.
  - Notes:
    - Derived by kindsgen (`deriveTable`). Every non-redirected fetch/XHR capture with page headers is reproduced from the list without them.
    - Chrome's `content-type` role is a tie. No capture has it with another page header; Blink source says as-page.
- **Page headers set twice; casing; forbidden headers**
  - Page headers set twice combine into one line `v1, v2` under the first spelling.
  - fetch() sends page names lowercase over HTTP/1.1; XHR as spelled.
  - Forbidden `sec-a` dropped (mimicry does not drop it).
  - Evidence: W (`headers-order`, `case` set and `around`, every profile).
  - Checked by: `TestHeadersOrderMatchesBrowser`.
- **Fetch `PUT` variants `{PUT, cross-site, corsChecked}` and the same with `pageSet`**
  - Evidence: W (`cors`: `put.cs`, and the `maxage.cs` PUT with the page's header). HTTP/2 only.
  - Notes:
    - Firefox, Safari 27 and 26. Safari places `content-length` differently in the two.
    - Chrome's PUTs merge into its base fetch order, so it has none.
    - Derivation only (see above).

### Empty bodies

- **`emptyBody` (navigation) and `kinds.fetch.emptyBody`**
  - A request without a body sends `content-length: 0` for the listed methods, in the list's place or `last` (before `te`).
  - Over HTTP/2 it ends with an empty DATA frame (`data`) or END_STREAM on HEADERS.
  - Other methods send none and end on HEADERS.
  - Over HTTP/3 the HEADERS frame ends the stream.
- **Evidence**: W (`bodiless`, 3 samples each, all identical).
  - Chrome 156 macOS (`-h3-local-roots`):
    - fetch POST and PUT and the form POST right after the pseudo-headers (the list's place).
    - PATCH none. END_STREAM on HEADERS. The same over HTTP/3.
  - Firefox 158 macOS (`-h3-local-roots`):
    - fetch POST and PUT `last` with `data`. PATCH none.
    - The form POST in its list's place with `data`.
    - Over HTTP/3 the same lists, HEADERS and FIN.
  - Safari 27 macOS:
    - fetch POST, PUT **and PATCH** and the form POST before `accept-language` (the list's place).
    - END_STREAM on HEADERS.
    - HTTP/2 only, as Safari stayed on TCP.
- **Checked by**: `TestBodilessMatchesBrowser` (header lines and frames, HTTP/2 and HTTP/3; Safari's fetch order pinned, see Not covered).
- **Notes**:
  - Derived by kindsgen (`deriveEmptyBodies`), which refuses samples, methods or protocols that disagree.
  - Profiles without a `bodiless` capture have no `emptyBody` and send none (see Not covered).

### Redirect, HTTP/2-only and insecure variants

- **`kinds.image`, `kinds.injected_image` variant `{same-site, redirected}`**
  - Evidence: W (`cookies`, `c-redirect.cs-ss.hop`). HTTP/2 only.
  - Checked by: `TestCookiesScenarioMatchesBrowser`.
- **Safari 27's `cookie` slot in the redirected same-origin image lists (HTTP/2)**
  - Evidence: W (`cookies`, `c-redirect.ss-so.hop`: Safari 27 sends cookies on that hop).
  - Checked by: `TestCookiesScenarioMatchesBrowser`.
- **Firefox's `sec-fetch-storage-access` slot in the base fetch list**
  - Evidence: W (`fetch-include.cs`: `none` on credentialed cross-site fetches).
  - Checked by: `TestKindsMatchBrowserTraces`.
- **Variants with no `http1` list** (those only the `cookies` or `cors` capture shows)
  - Evidence: W over HTTP/2 only.
  - Notes: such a request over HTTP/1.1 is refused with `ErrNotMeasured` rather than sent in a guessed order (`TestHTTP2OnlyVariantOverHTTP1`).
- **`insecure` variants (kinds and navigation)**: requests to a non-secure origin, HTTP/1.1 lists as sent.
  - Evidence: W (`kinds-plain`, every profile incl. Safari 26; [report](../../internal/harness/reports/plain-http-2026-10-24.md)).
  - Checked by: kindsgen `TestRegenerateReproducesProfiles`, `TestKindsMatchBrowserTraces` (`kinds-plain`).
  - Notes:
    - No `Sec-Fetch-*` (Chrome: no client hints either).
    - `Accept-Encoding: gzip, deflate`.
    - Chrome's `Origin` after `User-Agent`.
    - Safari's reorders and `Upgrade-Insecure-Requests` on frames.
    - A variant's values come from the kind's own plain requests, in its condition where captured, else in any:
      - Firefox: image `Priority: u=5, i`; font `Accept-Encoding: identity`; preload_font `gzip, deflate`.
      - Safari: async_script `u=3, i`; script, defer_script and preload_script `u=1, i`; image `u=5, i`.
    - A combination not captured takes the kind's non-secure default, never the secure lists.
    - Loopback hosts count as secure (Secure Contexts specification, not captured).

### Weights, WebSocket, priority and page-set slot

- **`kinds.*.weight`**
  - Evidence: W.
  - Notes: Chrome and Firefox. Safari sends no priority.
- **`kinds.websocket`**: handshake lists.
  - HTTP/2 extended CONNECT and HTTP/1.1 Upgrade.
  - The cross-site socket folded in.
  - An `insecure` variant for `ws://` from an `http://` page.
  - Evidence: W (`websocket` and `websocket-h2`, 3 samples each, every profile). Each of the 30 handshakes per browser has its list's order and defaults.
  - Notes:
    - Generated by `kindsgen`. Never CORS-checked: Origin is on every handshake.
    - Weight: Chrome 146 exclusive, Firefox 21, Safari none.
    - `:protocol` follows `http2.pseudoHeaderOrder` in every browser. It is a rule, not a field, and `kindsgen` refuses a capture that breaks it.
    - The `insecure` variant has no `Sec-WebSocket-Protocol` slot (`plain-so` asked for none). Protocols there are not measured.
- **`kinds.fetch.high`, `.low`; `kinds.injected_image.high`, `.low`**
  - Evidence: W.
  - Notes: other kinds' fetchpriority is not measured. Asking for it is an error.
- **`*` (where page-set headers go)**
  - Evidence: W (`fetch-custom`: X-Mimicry's position).
  - Notes: one position for every page-set header. The browsers place them by name (see "Not covered").

### Context rules

- **`context.storageAccess`**
  - Evidence: W. Chrome `active`, Firefox `none`, Safari none.
  - Notes: sent on credentialed cross-site requests.
- **`context.sameOriginElementOrigin`**
  - Evidence: W. Chrome, Safari.
  - Notes: Origin on same-origin CORS element requests.
- **`context.stylesheetReferrer`**
  - Evidence: W. Chrome, Firefox.
  - Notes: Safari refers CSS fonts to the page. Only same-origin fonts captured, at `/`.
- **`context.anonymousConnections`, `frameConnections`**
  - Evidence: W. Chrome.
  - Checked by: `TestKindsMatchBrowserTraces` (which requests share a connection, every sample).
  - Notes:
    - Requests without credentials, and a cross-site frame's navigation, go on connections of their own.
    - Safari: one connection per host, as mimicry's default.
    - Firefox: no rule (see "Not covered"). The test pins its known differences label by label (`partitionGaps`). It fails on any other or on one no longer seen.
- **`context.acceptLanguageQ`**: Brave's farbled Accept-Language.
  - The last language's q is drawn per top-level page origin and session. Preflights keep the lists' value.
  - Evidence: W. Brave, 0.5–0.9, all five in every platform's captures. Every other browser none.
  - Checked by: `TestAcceptLanguageFarbling`:
    - One q per page on `page` and `kinds`.
    - A redirect keeping its first URL's on `nav-redirect-cs`.
    - mimicry's draws over 200 origins.
  - Notes:
    - Derived by kindsgen from every request of every capture as sent. It refuses a preflight farbled.
    - The other tests compare Brave's captures unfarbled (`capture.Unfarble*`) with the farbling off.
- **`context.blocksCrossSiteBeacons`**
  - Evidence: W. Brave: the kinds pages' `beacon.cs` absent, 9 of 9 samples per platform. `beacon.so`, `beacon.ss` sent.
  - Checked by: `TestKindsMatchBrowserTraces` holds mimicry to refusing it and every sample to not sending it.
  - Notes: derived by kindsgen.

### Referer, Origin, credentials

- **Referer by strict-origin-when-cross-origin; Origin per the Fetch standard; credentials per mode**
  - Evidence: W where captured, otherwise the standard.
  - Notes: the page sits at `/`, so full URL and origin coincide except for the CSS font.
- **After a redirect**
  - sec-fetch-site is the chain's most distant relation (a navigation's: `redirects.navigationSite`).
  - `Origin: null` once the chain is tainted.
  - The page's Referer (not `http.Client`'s), with the policy applied to each hop in turn. Origin only once the chain left the page's origin.
  - Storage access by the URL's own relation.
  - Evidence: W (`fetch-redirect`, `image-redirect`, both directions; `nav-redirect-page` for the Referer back on the page's origin).

## Client hints

`clientHints` ([decision 0055](../../docs/decisions/0055-client-hints-opt-in.md)).

- **Source**: derived by kindsgen from the `client-hints` captures.
  - 2026-11-04, 3 samples each, all identical.
  - `.local-roots` for Chrome and Firefox.
- **Chrome 156: W**, on two machines:
  - macOS: 156.0.8078.80, macOS 27.0.1, arm.
  - Windows: 156.0.8078.81, Windows 10, x86.
- **Firefox 159 and Safari 27 (macOS): W**, no `clientHints`. No hint beyond their lists, no restart.
- **kindsgen refuses**:
  - captures whose samples disagree;
  - any captured request the model would send differently, every subresource's table run included.
- **Checked by**: `TestClientHintsMatchesBrowser` replays the page through mimicry and compares every request with the samples, both sends of a restarted navigation included.
- **`chrome-156-windows`** carries the full version its capture shows, .81, though its other captures were of .80. Chrome updates patch versions silently.

### Hint runs and opt-in

- **`clientHints.hints`**: the run a navigation or frame sends once its origin opted in, in order, where its list has the low-entropy hints.
  - Evidence: W.
    - `next.nav`, `meta.nav`, `optin.iframe`.
    - `h1-next.nav` over HTTP/1.1; `h3-next.nav` over HTTP/3.
    - The shrunk sets `after-shrink.nav`, `crit-again.nav`.
    - A redirect's hop `r-land.nav`, `r-xland.nav` (after `sec-fetch-dest`, as their variants keep `sec-ch-ua`).
    - The form POST `r-post.nav` (after `cache-control`).
  - Notes:
    - Lowercase over HTTP/1.1 too.
    - `sec-ch-ua-form-factor` (singular), `save-data`, `width` and `sec-ch-width` are never in the run.
- **`clientHints.tableOrder`**: other requests' hints enter Blink's header table (`context.headerTable`) in this order, then `user-agent`, after the page's own headers.
  - Evidence: W + S.
    - W: every captured fetch, image, script and `/done` (24, 7, 6 and 4 names); `r.fetch-header` with `x-mimicry`; `r.fetch-json` with `content-type`.
    - S: Blink `FrameFetchContext::AddClientHintsIfNecessary`.
  - Notes: the navigation order does not reproduce the 24-name runs.
- **Opt-in**
  - A navigation's `Accept-CH` replaces its origin's set, a redirect's and a form POST's included.
  - The set is kept across navigations without one.
  - A subresource's `Accept-CH` is ignored.
  - Evidence: W.
    - `base.accept-sub`, `base.after-sub`.
    - `optin.nav`, `next.nav`; `shrink.nav`.
    - `r-ach-hop.nav` (302) then `r-ach-land.nav`, `r-ach.fetch`.
    - `r-post.nav` then `r-xland.nav`.
  - Notes: hints Chrome cannot send on a navigation (`Sec-CH-Width`, `Save-Data`, unknown names) still replace the set.
- **Who gets them**
  - The page's own origin: all.
  - Another origin (a subdomain too): the low-entropy three.
  - A delegated origin: all.
  - An origin left out of an explicit allowlist: none.
  - Evidence: W (`optin.sub-fetch`, `optin.xo-*`, `sub.nav`, `b.nav`; `deleg.xo-*`, `meta.xo-*`; `deleg.sub-fetch`).
  - Notes: mimicry is told per request (`Request.ClientHints`).

### Restarts and clearing

- **`Critical-CH`**: one restart of a GET navigation, a redirect included.
  - Happens when it names a hint that the response's `Accept-CH` asks for, that navigations send, and that the request lacked.
  - On the same connection.
  - The run goes after `clientHints.restartAfter` (`accept`).
  - Evidence: W.
    - Restarted: `crit.nav`, `crit-add.nav`, `crit-blocked.nav`, `h1-optin.nav`, `h3-optin.nav`, the 302 `r-ach-hop.nav`. 36 of 36 on one connection.
    - None for `crit-again.nav`, `crit-width.nav`, `crit-save-data.nav`, `crit-unknown.nav`, or the form POST `r-post.nav`.
  - Notes: a response's own `Permissions-Policy` does not stop it (`crit-blocked.nav` sent Model).
- **`Clear-Site-Data: "clientHints"` and `"*"`** empty the origin's set.
  - On a navigation's response: from the next request on.
  - On a fetch's: from the next navigation. The page that made the fetch keeps its hints.
  - Evidence: W (`clear.fetch`, `after-clear.nav`; `after-star.nav`; `r-csd.fetch` then `r-after-csd.fetch` (hints kept) and `r-after-csd.nav` (none)).
  - Notes: mimicry keeps the last navigation's page's hints for its subresources. Any other page's subresources read the store.

### Hint values

- **`clientHints.fullVersion` and `clientHints.machine` (but `downlink`)**
  - Evidence: W (every value of every hint sent, all samples).
  - Values:
    - macOS: `"27.0.1"`, `"arm"`, DPR 2, 1200 × 953.
    - Windows: `"10.0.0"`, `"x86"`, DPR 1, 1249 × 1285.
    - Both: `"64"`, `""`, `?0`, `"Desktop"`, 32 GiB, `dark`, `no-preference`, `4g`.
  - Captured on one display. With a DPR-1 display beside the Mac's, Chrome's navigations said DPR 2 and its subresources 1.
- **`rtt` in steps of 50 ms; `downlink` in steps of 0.05 Mbps within 10% of `clientHints.machine.downlink`**
  - Evidence: W. rtt 0–100 and downlink 1.45–1.75 on both machines.
    - Fixed per host within a sample.
    - Different for a host's navigations and its subresources.
  - Notes: `downlink`'s centre is derived. The noise and rounding follow Chrome's (see "Not covered").

## URLs

`urls` ([decision 0057](../../docs/decisions/0057-url-parsing-measured.md)).

- **Source**: derived by kindsgen from the `urls` captures (2026-11-04, 3 samples each, all identical within a browser).
- **W**: Chrome 156 and Firefox 159 on macOS and Windows; Safari 27 and Safari 26 on macOS.
- **I** (inherited):
  - `safari-27-ios`, `chrome-157-ios` and `firefox-159-ios` inherit Safari 27's.
  - `safari-26-ios` inherits Safari 26's.
  - `chrome-157-macos` inherits Chrome 156's on macOS.
- **None**: `chrome-155-macos`, `firefox-158-macos` and `firefox-158-windows` (see "Not covered").
- **The page**: each browser fetches and navigates to (an iframe) 43 URLs. Windows matched macOS on all of them.
- **kindsgen refuses**:
  - samples that disagree;
  - a fetch and a navigation that differ;
  - a character every other browser encodes sent as written.
- **Checked by**: `TestURLsMatchBrowser` sends each URL through mimicry. It compares the request target, `Host` or `:authority` and SNI with every sample's fetch and navigation.

### Query and path

- **Query**
  - The URL standard's query set: space, `"`, `<`, `>`, controls, non-ASCII.
  - And `'` for http(s) and ws(s).
  - `` ` ``, `{`, `}`, `\|`, `\`, `^` and a lone `%` as written.
  - `%XX` kept.
  - Evidence: W (`q-*`, every browser).
  - Notes: `'` was "Not covered" before. Every browser sends `%27`.
- **Path**
  - The query set and `?`, `^`, `` ` ``, `{`, `}` encoded.
  - `'` as written.
  - `\` a separator.
  - `.` and `..` segments, as `%2e` too, resolved.
  - Evidence: W (`p-*`, `p-dots`, `p-pctdots`, every browser).
  - Notes: Go's own encoding sent `\` as `%5C` and kept dot segments.
- **`urls.pathEncodes`**: `\|` in a path encoded (`%7C`) by Chrome; sent as written by Firefox and Safari.
  - Evidence: W (`p-bar`).
  - Notes: the request target, Referer, WebSocket URL and cookie paths use mimicry's form, not Go's `EscapedPath` (`internal/weburl`).

### Hosts

- **Hosts**
  - Lowercase.
  - Non-ASCII through UTS #46 ToASCII.
  - An explicit default port, `:0443` too, dropped.
  - IPv4 in short, hex, octal, integer and trailing-dot forms as dotted decimal.
  - Evidence: W (`h-upper`, `h-idn`, `h-port443`, `h-port0`, `ip-*`, every browser).
  - Notes: `ip-*` from a plain `http://` page. Safari blocks even loopback `http://` from an `https://` page.
- **`urls.checkPunycode`**: an ASCII host's `xn--` label that is not valid punycode.
  - Refused by Firefox and Safari 26. Sent as written by Chrome and Safari 27.
  - Evidence: W (`h-badpuny`).
  - Notes: a valid `xn--` label goes out as written in every browser.
- **A host ending in a dot keeps it in `Host` and `:authority`**
  - Evidence: W (`h-dot`, `h-dotpool`; Safari's requests, the others' pooled ones).
- **A host ending in a dot, in SNI** (`tls.trailingDotSni`, hellogen)
  - Desktop Chrome, Edge, Firefox keep it in SNI.
  - Safari and every iPhone profile (WebKit) send none, and Safari gives it a connection of its own.
  - Evidence: W.
    - `h-dot`, every sample, fetched and navigated to; `h-dotpool` for Safari.
    - Firefox 158 from Mozilla's archive.
    - Chrome 155 from Chrome for Testing 155 (the network stack's, decision 0042).
  - Notes: over QUIC by inference. No capture has such a host on HTTP/3.

## CORS preflights

`kinds.preflight` and `context.preflightCache` ([decision 0017](../../docs/decisions/0017-cors-preflights.md)).

- **Sources**: the `kinds`, `kinds-http1`, `kinds-plain` and `cors` captures ([report](../../internal/harness/reports/cors-2026-10-24.md)).
- **Profiles**: every profile, including Safari 26.
- **Class**: all rows **W**.

| Claim | Checked by | Notes |
|---|---|---|
| `kinds.preflight` [1] | `TestKindsMatchBrowserTraces` [2] | [3] |
| When a request is preflighted [4] | [5] | [6] |
| `Access-Control-Request-Headers` [7] | `TestCORSMatchesBrowser` (`multi.cs`, `put.cs`) | |
| No cookies; Chrome's own connection [8] | `TestCORSMatchesBrowser` (`include.cs`) | [9] |
| Cache lifetime [10] | [11] | Max-Age caps not measured: honoured as sent |
| `context.preflightCache` [12] | `TestCORSMatchesBrowser` [13] | Derived by kindsgen from the `cors` capture |
| A refused preflight: no request [14] | [15] | [16] |
| A redirected request is preflighted again for its new URL | `TestCORSMatchesBrowser` (`redirect.cs`) | |

Notes:

1. The `OPTIONS` request's names, order, values, weight; HTTP/1.1 lines; non-secure variants.
2. Every preflight of the kinds page; none where the browser sent none.
3. Priority and weight are the browser's `fetch()` ones. Safari sends `content-length: 0`.
4. `fetch()` in CORS mode or XHR, to another origin, with a method other than GET/HEAD/POST or a header outside the Fetch safelist.
5. `TestKindsMatchBrowserTraces`, `TestCORSMatchesBrowser`. The safelist's edges by `TestPreflightWhenNeeded`.
6. The safelist's edges (long values, ranges, languages) follow the Fetch standard, not captures.
7. Lowercase, sorted, joined by `,`. Absent without unsafe headers.
8. The preflight carries no cookies. Chrome sends it on its credential-less connection.
9. Firefox and Safari: the requests' own connection.
10. `Access-Control-Max-Age`, else 5 s.
11. `TestCORSMatchesBrowser`: Max-Age 600, the default at once and after 6 s. `TestPreflightCacheLifetime`: the bounds, fake clock.
12. `replace` (Chrome, Safari 27, 26); `merge` (Firefox).
13. `maxage.cs`: the request after another header's preflight.
14. Status not 2xx, or a header not allowed.
15. `TestCORSMatchesBrowser` (`denied-*.cs`). The other refusal rules by `TestPreflightRefused`.
16. Origin, credentials and wildcard rules follow the Fetch standard. Only these two refusals were captured.

### Redirects

`redirects` and the transport's redirect rules.

- **Source**: the `cors-edge` capture ([report](../../internal/harness/reports/cors-edge-2026-10-24.md)).
- **Samples**: three per browser, every profile including Safari 26, all identical (ports aside).

#### CORS checks and methods

- **A CORS-mode request to another origin whose 3xx fails the CORS check**: the next URL is never requested.
  - Fails: no `Access-Control-Allow-Origin`; with credentials, no `Access-Control-Allow-Credentials: true`.
  - Evidence: W.
    - `cors-edge`: `noacao-redirect.cs`, `noacao-redirect-pf.cs`.
    - `redirects2`: `xhr-noacao.cs`, `img-noacao.cs` (`<img crossorigin>`), `script-noacao.cs` (`<script crossorigin>`), `fetch-noacao.ss`, `fetch-noacao.cs-cs`, `cred-noacac.cs-cs`.
    - Their `acao`/`acac` controls followed, the cs-cs hop with `Origin: null`.
  - Checked by: `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser` (a redirect's `*CORSError`).
  - Notes:
    - Wildcard rule follows the Fetch standard.
    - Other CORS-mode element kinds (styles, fonts, preloads) take the same check unmeasured (one Fetch-standard path in every engine).
- **Method changes**
  - PUT, DELETE and PATCH answered 301 or 302 keep method, body and `Content-Type`.
  - POST answered 301 or 302, and every method but HEAD answered 303, become a GET without body or `Content-Type`.
  - HEAD stays HEAD.
  - 307 and 308 keep everything (PUT, POST, PATCH; fetch() and form POSTs).
  - Evidence: W.
    - `cors-edge` `m-{put,delete,post}-{301,302,303}.so`.
    - `redirects2` `m-put-{307,308}.so`, `m-post-{307,308}.{so,cs}`, `m-head-{301,302,303}.so`, `m-patch-{301..308}.{so,cs}`, `form-{301..308}.{so,cs}`.
  - Checked by: `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser` (method, body length, lines); `TestRefusedRequests` (a body without `GetBody`).
- **`redirects.methodChangeOrigin`**
  - `keep`: Safari 27, 26. The fetch() GET carries the page's `Origin`.
  - `drop`: Chrome, Firefox. None.
  - Evidence: W (the GET hops of `cors-edge` `m-*.so` and `redirects2` `m-patch-303.so`).
  - Checked by: `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser`.
  - Notes:
    - Hand-edited.
    - A navigation's GET (form POST answered 301–303) carries none in every browser (`form-30{1,2,3}.{so,cs}.hop1`).
- **Redirect limits**
  - A fetch() chain stops after 20 redirects.
  - `redirects.navigationLimit`: a navigation after 19 (Chrome) or 20 (Firefox, Safari 27, 26).
  - Evidence: W.
    - `cors-edge` `chain.so`.
    - `redirects2` `nav-chain.so`, 25 redirects: Chrome requested `.hop19` last, the others `.hop20`.
  - Checked by: `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser`.
  - Notes:
    - Hand-edited.
    - Chrome's error page reloads `.hop19` a second later (`cache-control: max-age=0`) and follows the rest. Browser UI, not reproduced.
    - Firefox's and Safari's pages were finished by the watchdog worker.
    - Frames keep 20 (not measured).
- **`redirects.hopPreflight`**
  - `inherit`: Firefox, Safari 27, 26. The GET that a preflighted cross-site PATCH answered 303 became is preflighted (`access-control-request-method: GET`).
  - `own`: Chrome. Not preflighted.
  - Evidence: W (`redirects2` `m-patch-303.cs.hop1`).
  - Checked by: `TestRedirects2MatchesBrowser`.
  - Notes: hand-edited. Only measured for a hop to the same cross-site origin.

#### Navigation hop order

- **Navigation redirects are ordered by variants**
  - Chrome moves `sec-ch-ua*` after `sec-fetch-dest` on every hop. It keeps `cache-control: max-age=0` after a form POST.
  - Firefox swaps `referer`/`origin` on a 307/308 POST hop.
  - Safari rebuilds every hop:
    - a typed one (`site: none`) otherwise than a page's;
    - one from `http://` (`insecureBeforeRedirect`) with `upgrade-insecure-requests`;
    - a hop to another site than the URL before it (`crossSiteRedirect`, navigations only) with `referer` before `sec-fetch-dest`. After it on a hop within a site: apex→www, www→static, a form's within a cross-site origin.
  - Variant conditions `methodChangedByRedirect`, `insecureBeforeRedirect` and `crossSiteRedirect` are new.
    - `methodChangedByRedirect`: Chrome re-adds Origin after `sec-fetch-*` on a fetch() hop the redirect turned into a GET.
  - A user's navigation (a clicked link or submit button, a typed URL) is ordered as a script's, with `sec-fetch-user: ?1`, on its first leg and every hop:
    - Chrome: between `sec-fetch-mode` and `sec-fetch-dest`.
    - Firefox: after `sec-fetch-site`.
    - Safari sends none and orders a click as a script's.
  - Evidence: W, 3 samples each.
    - `redirects2`: form POSTs, `nav-chain.so`.
    - `nav-redirect-{so,ss,cs,http}`: typed navigations redirected within the origin, apex→www, to another site, http→https.
    - `nav-redirect-page`: a page's script and form navigations redirected to another origin.
    - `nav-redirect-form`: form POSTs by script answered 301–308 between every pair of relations.
    - `nav-redirect-click`: the same clicked, and clicked links answered 301/302 from and to every relation.
    - The capture tool's real clicks: DevTools `Input.dispatchMouseEvent`, BiDi `input.performActions`, Safari the system's mouse events.
    - Every clicked request's header names are a script's plus `sec-fetch-user`.
    - Safari's first sample of `nav-302.cs-so.hop1` left out, see below.
  - Checked by: `TestRedirects2MatchesBrowser`, `TestNavRedirectsMatchBrowser` (a click sent as a script's fails it); kindsgen `TestRegenerateReproducesProfiles`.
  - Notes:
    - Derived by kindsgen.
    - A user's navigation is `ErrNotMeasured` in a condition measured only on a script's.
    - Also over HTTP/1.1 where that list has no `Sec-Fetch-User` slot (Firefox's form POST: no clicked form was captured over HTTP/1.1).
    - Safari 26's clicks not captured (`missingCaptures`).
- **A redirect hop carrying cookies has the `cookie` slot of its variant**
  - Chrome 156 (macOS, Windows): between `accept-language` and `priority` on every hop (navigations, images, fetch()).
  - Firefox 158:
    - after `referer` (or `origin`) and before `upgrade-insecure-requests` on navigations;
    - before `sec-fetch-dest` on images and fetch();
    - after `accept-encoding` on a typed navigation's hop.
  - Safari 27, 26: last, after `accept-encoding`.
  - Evidence: W, the cookie twins of decision 0048, 3 samples each, all alike.
    - `nav-redirect-{so,ss,cs,http,page,form,click}-cookies`, `redirects2-cookies`, `cors-edge-cookies`.
    - Safari 26 without clicks.
    - Every twin request's other header names are its original's.
    - Chrome 156.0.8078.79 through powhttp on httpbin.org's `/cookies/set` → `/cookies` sent Chrome's order.
  - Checked by: `TestNavRedirectsMatchBrowser`, `TestRedirects2MatchesBrowser`, `TestCORSEdgeMatchesBrowser`; kindsgen `TestRegenerateReproducesProfiles`.
    - A variant without the slot sends the cookies after its list (after `priority` (Chrome), `te` (Firefox)) and fails them.
  - Notes:
    - Derived by kindsgen, each twin's hops merged into its original's conditions.
    - Chrome 155 lacks the slot on these hops (`missingCaptures`):
      - the twins came after it;
      - Chrome 155 is no longer installable as branded Chrome;
      - it cannot inherit Chrome 156's captures.
    - Safari 26's clicked hops not captured (`missingCaptures`).
    - Hops no twin reaches with cookies keep no slot ([Not covered](gaps.md#not-covered-by-any-claim)):
      - cross-site subresources with credentials;
      - hops from `http://`;
      - Safari 27's image hop to another origin of the page's site.

#### Navigation Referer, site and Origin

- **Navigation hop `Referer` and `redirects.navigationSite`**
  - A navigation hop's `Referer` applies the policy to the `Referer` the request before it sent.
    - Once a chain left the page's origin, only the origin, even back on it.
  - `redirects.navigationSite`: after a redirect to another site, `sec-fetch-site` counts:
    - the whole chain (`chain`: Chrome, Firefox, Safari 26); or
    - only the URLs from that redirect on (`restart`: Safari 27, `same-origin` on `nav-302.cs-so.hop1`).
  - Evidence: W (`nav-redirect-page`: `nav-302.cs-so.hop1`, `nav-301.ss-so.hop1`, every browser).
  - Checked by: `TestNavRedirectsMatchBrowser` (setting Safari 27 to `chain` fails it).
  - Notes:
    - `navigationSite` hand-edited.
    - Measured on one hop (a chain to another site and back). Longer chains follow the rule unmeasured.
    - Each Safari's first (cold) sample sent that hop on a new connection, ordered as a hop within a site (Safari 27: with `cross-site`).
      - Browser state: left out by kindsgen (`sampleGaps`) and accepted (logged) by the test.
- **`redirects.navigationOrigin`**: a form POST's `Origin` kept by a 307/308 becomes `null`:
  - `cross_origin` (Chrome, Safari 27, 26): once any redirect went to another origin than the URL before it.
  - `taint` (Firefox): once the chain is tainted as the Fetch standard has it, a redirect from another origin than the page's to a third.
    - Firefox keeps the page's origin from the page's origin to static.navr-a.test or another site.
  - Evidence: W (`nav-redirect-form`, `nav-redirect-click`, every browser, 3 samples).
    - `*-30{7,8}.{so-ss,so-cs,ss-so,cs-ss,cs-so}.hop1`: `null` or not.
    - `*-30{7,8}.{so,ss,cs}.hop1`: the page's origin.
  - Checked by: `TestNavRedirectsMatchBrowser` (setting Chrome to `taint` fails it).
  - Notes: hand-edited. Measured on one hop; longer chains follow the rule unmeasured.

#### Subresource hops

- **Safari's bodies and preflights after a preflight**
  - Safari sends no `content-length` on a body redirected after a preflight (cross-site PATCH, POST 307/308 hops).
  - Its preflight without `access-control-request-headers` starts `sec-fetch-site, origin`.
  - Evidence: W (`redirects2` `m-patch-*.cs`, `m-post-30{7,8}.cs`).
  - Checked by: `TestRedirects2MatchesBrowser`.
  - Notes: plan `NoContentLength` (HTTP/2). Preflight variant `pageSet` means "asks for headers".
- **A redirect's request is ordered by how it was made**
  - Variant conditions:
    - `crossOriginRedirect`: the redirect went to another origin than the request it answered.
    - `originBeforeRedirect`: that request carried `Origin`.
  - Chrome: an `Origin` the request before carried keeps its place (before `sec-fetch-site`). One the redirect adds goes after `sec-fetch-*`.
  - Firefox: hops order otherwise than first legs (`referer` on the PUT hop, `origin` on the cross-site one), but agree within each condition.
  - Safari 27, 26:
    - a fetch redirected to another origin drops `sec-fetch-*`, `accept-language` and `priority`;
    - a same-origin redirect reorders every header, differently with and without `Origin`.
  - Evidence: W (every hop of `kinds`, `kinds-http1`, `kinds-plain`, `cookies`, `cors`, `cors-edge`, `redirects2`, `nav-redirect-*`; all samples identical).
  - Checked by: `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser`, `TestNavRedirectsMatchBrowser` (every request's lines and weight), `TestKindsMatchBrowserTraces`, `TestCookiesScenarioMatchesBrowser`, `TestCORSMatchesBrowser`; kindsgen `TestRegenerateReproducesProfiles`.
  - Notes:
    - Derived by kindsgen from the capture of the request each hop follows.
    - A hop combination not captured is `ErrNotMeasured` (e.g. a no-cors image redirected within another site; [report](../../internal/harness/reports/redirects2-2026-10-24.md)).
