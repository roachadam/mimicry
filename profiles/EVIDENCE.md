# Evidence ledger

This file lists every claim the built-in profiles make, where it comes
from, and what keeps it true. A claim is one profile field, such as the
TLS cipher list or a cookie rule.

**Covered:** `brave-157-macos`, `brave-157-windows`, `brave-157-linux`,
`chrome-155-macos`, `chrome-156-macos`, `chrome-156-windows`,
`chrome-157-macos`, `chrome-157-linux`, `chrome-157-ios`,
`edge-157-macos`, `edge-157-windows`, `firefox-158-macos`,
`firefox-158-windows`, `firefox-159-macos`, `firefox-159-windows`,
`firefox-159-linux`, `firefox-159-ios`, `safari-27-macos`,
`safari-26-macos`, `safari-27-ios` and `safari-26-ios`. Safari 27 was
captured on macOS 27; Safari 26 on a second Mac kept on macOS 26, as were
Chrome 157, Firefox 159, Edge 157 and Brave 157 for macOS (that Mac on
26.2 by Edge's run); Chrome 156, Firefox 158 and
159, Edge 157 and Brave 157 for Windows on Windows 10; Chrome
157.0.8103.38, Firefox 159.0.1 and Brave 1.99.55 for Linux, Google's,
Mozilla's and Brave's own builds, in WSL2 (Ubuntu 24.04) on that Windows
PC; Safari 27.2, Chrome 157.0.8103.24 and Firefox 159.1 for iOS on an
iPhone on iOS 27.2, Safari 26.6.2 on a second iPhone on iOS 26.7.1.

**Headline:** about 85% of the classed cells in the tables below are **W** or
**W+** (seen on the wire). The rest are fitted, copied, measured on another
kind of request, or not captured. No claim rests on browser source code alone.

**Classes**, one line each:

- **W**: seen on the wire, three samples, identical unless noted.
- **W+**: **W**, and stable across the other server scenarios.
- **F**: fitted rule, verified by re-encoding every captured header block.
- **K**: measured on a different kind of request.
- **C**: copied from another profile, not measured for this one.
- **I**: inherited from an earlier version's captures.
- **—**: does not apply to this browser.

**Reading a row:** the first column names the profile field. Each browser
column gives the class, then the measured value and sample counts. The last
column names the test that fails if the profile and the captures drift
apart. "Known gap" marks a difference mimicry does not imitate.

Last fully reviewed 2026-10-24. Rows were updated with the 2026-10-28
captures, `chrome-156-windows` added with its 2026-10-29 captures, the
redirect rows with the 2026-10-30 cookie twins (0048), `safari-27-ios`
and `safari-26-ios` with their 2026-11-01 captures (0051),
`firefox-158-windows` with its 2026-11-01 captures, `chrome-157-ios`
with its 2026-11-01 captures (0053), `firefox-159-ios` with its
2026-11-03 captures (0053, amended), `firefox-159-macos` and
`firefox-159-windows` with their 2026-11-03 quick refreshes, the client
hints rows with the 2026-11-04 `client-hints` captures (0055), the URL
rows with the 2026-11-04 `urls` captures (0057), `chrome-157-linux` and
`firefox-159-linux` with their 2026-11-05 captures (0058),
`edge-157-macos` with its 2026-11-06 captures (0059), the
`tls.clientAuth` rows with the 2026-11-08 `client-cert-*` captures
(0064), the Brave profiles with their 2026-11-09 captures (0067), and
`chrome-157-macos` with its 2026-11-09 quick refresh. The "Not covered"
list was rechecked against the code on 2026-10-29.

## How rows are classed

| Code | Meaning |
|---|---|
| **W** | Seen on the wire: the browser against mimicry's capture server, three samples (three browser launches for Chrome and Firefox), identical across samples unless the row says otherwise. Captures in `internal/harness/testdata/captures/`. |
| **W+** | **W**, and also stable across the server scenarios (`set-cookie`, `large-settings`, `small-limits`, `huge-table`, `explicit-table`, `slow-idle`, `http1`) and, where noted, the six replayed real pages. |
| **F** | Fitted: not visible as such on the wire. A rule chosen to explain the traces, then verified by re-encoding every captured header block byte for byte (`TestHPACKReproducesBrowserBlocks`). |
| **K** | Measured on a different kind of request than the profile describes (e.g. on `fetch()` uploads, used for navigations). |
| **C** | Copied from another profile, not measured for this one. |
| **I** | Inherited: checked against an earlier version's captures (the profile's `capturesFrom`, decision 0035), with only version tokens and the navigation's `accept-encoding` in header values rewritten (`capture.Rewriter`). The test passes for this profile, but this version's own traffic was not captured. Written with the source version's class: `I: from chrome-155 (W+)`. |

A new version's column starts as **I** for every row whose evidence is a
scenario in its `capturesFrom`. Rows from the scenarios a quick refresh
recaptures (the fingerprint layer, `server.QuickScenarios`) get their own
class. A full refresh replaces every **I** with the class its new captures
earn. Deprecated profiles (`"deprecated": true`, older than the newest five
versions) keep their columns as last reviewed.

No claim rests on browser source code or on another client's presets alone:
those were leads only. Where another client disagreed with the wire, the wire
won (httpcloak's single joined Safari cookie field, for one: every Safari 27
sample splits cookies).

## Behaviour in code, not in profiles

| Behaviour | Evidence | Checked by |
|---|---|---|
| A fully read response is not reset (no RST_STREAM; waits for END_STREAM up to 10 s) | W: no browser resets streams it read | `TestClosingFullyReadBodyDoesNotReset` |
| Uploads read in 65535-byte chunks | W for the first 64 KiB of Chrome and Safari uploads; later chunks and all of Firefox's are not modelled | `TestConnectionMatchesBrowserTrace` (largest frame only) |
| HTTP/1.1-only servers get parallel connections at once; HTTP/2 candidates wait for the first | W (`resume`: three parallel requests, three connections opened together) | `TestResumptionMatchesBrowser` |
| END_STREAM on an upload's last DATA frame (body of known length), no empty frame after | W (the capture page's upload, every profile, every sample) | `TestDataFrameCap` |
| `Config.Header`' Authorization and Cookie are not sent once a redirect chain has left an origin | The Fetch standard (Authorization on a cross-origin redirect); not captured | `TestConfigCredentialsStayBehindCrossOriginRedirects` |
| A CORS preflight carries only the `Config.Header` the preflight kind's lists name (a User-Agent or Accept-Language override), never others such as an API key | Follows the request it precedes; not captured (browsers have no `Config.Header`) | no test |
| The HelloRetryRequest cookie placed at a random non-pinned position (uTLS fork), where the profile has no `tls.helloRetry`, and in every QUIC hello | not captured | known gap |
| WebSocket messages go as one frame each | W for Firefox and Safari (every message); Chrome sends the 71680-byte text whole in 3 of 6 samples, split 65464 + 6216 in the others: mimicry always sends the whole form | `TestWebSocketsMatchBrowser` |
| `ws://` from an `https://` page is refused before connecting | W: Chrome throws `SecurityError`; Firefox and Safari fail and no handshake reaches the server | `TestWebSocketsMatchBrowser` (`mixed`, every profile, both scenarios: the dial errors and no handshake reached the server) |
| Firefox's socket transport: left to the server, ended by mimicry 10 s after the close handshake (HTTP/1.1: TCP close; HTTP/2: RST_STREAM NO_ERROR); a stream the server ended is forgotten without a frame | Firefox's own end is not captured: an unmeasured fallback so a socket cannot leak | no test (known gap) |
| Chrome over HTTP/2: `Close` returns once the server has ended the socket's stream (mimicry's END_STREAM went with its close frame), so a later request's chained priority never depends on it; a stream the server does not end is reset after 10 s (Chromium waits 2 s, then drops the stream) | Chromium's `WebSocketChannel` fires its close event only after the stream closed ([INFERENCE], from its source); every captured socket handshake, each opened after the one before had closed, depends on stream 0 (`websocket-h2`, `proxy-websocket*`); the 10 s fallback is not captured | `TestWebSocketsMatchBrowser` (each socket's handshake priority); the fallback no test (known gap) |
| A close handshake the server never answers ends the transport after 10 s | not captured | no test (known gap) |
| Firefox's HTTP/2 segmenting that counts a frame header into its 16000 bytes (14 + 15986) | W, not modelled: only the cap and header-alone are | known gap |
| `ErrNotMeasured`: an empty message on a compressing socket; a server answering `client_max_window_bits` under 15 or `client_no_context_takeover` (on sending); WebSockets through a proxy without `proxy.websocket`, with proxy credentials without `proxy.websocket.credentials`, ws:// through SOCKS5 | not captured | `TestWebSocketsMatchBrowser` (Firefox deflate); `TestUnmeasuredProxyIsAnError` (the proxy cases); the rest no test |
| A cross-site socket over HTTP/2 may share the cross-site host's own first-party connection (no top-level-site partition) | W: browsers use only the page's partition's connection (the one that carried `b-set-3p`) | known gap |
| Read limits: 64 MiB per message, 64 queued messages | safety bounds, not captured | `TestReadingServerFrames` (server frames, hostile input) |

## Keeping it true

Change a profile, change its row. A new browser version starts as a copy of
the previous one ([decision 0004](../docs/decisions/0004-complete-profile-files.md);
`cmd/refresh` does the copy): mark what it copied, everything a capture
cannot show (`http2.hpack`, header slots, `cookies`, `http1` slots), **C**
until its own captures pass the tests above.
