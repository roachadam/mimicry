# Evidence ledger

This ledger lists every claim the built-in profiles make, where it comes from, and what keeps it true.

- A claim is one profile field, such as the TLS cipher list or a cookie rule.
- About 85% of the classed cells are **W** or **W+**: seen on the wire.
- The rest are fitted, copied, measured on another kind of request, or not captured.
- No claim rests on browser source code alone.

## Where to look

| File | Covers |
|---|---|
| [Profile notes](evidence/profiles.md) | What each profile was captured on, what it inherits, where it differs |
| [TLS ClientHello](evidence/tls.md) | The ClientHello, client certificates, ECH |
| [HTTP/2 and HTTP/1.1](evidence/http2-http1.md) | The HTTP/2 connection, HTTP/1.1 uploads and TLS records, HPACK |
| [Requests](evidence/requests.md) | Request headers, cookies, request kinds, client hints, URLs, CORS preflights, redirects |
| [Proxies](evidence/proxies.md) | Proxies |
| [Connections and WebSockets](evidence/connections.md) | Connection reuse and limits, WebSockets |
| [QUIC and HTTP/3](evidence/http3.md) | QUIC and HTTP/3: failure, version negotiation, long transfers, early data, stream limits |
| [Not covered by any claim](evidence/gaps.md) | Known differences from the browsers, and what no capture measured |
| [What checks each behaviour](evidence/tests.md) | The tests and captures behind each concept page |

Below in this file: [how rows are classed](#how-rows-are-classed), [behaviour in code, not in profiles](#behaviour-in-code-not-in-profiles), [keeping it true](#keeping-it-true) and the [review history](#review-history).

## Profiles covered

| Browser | Profiles |
|---|---|
| Chrome | `chrome-155-macos`, `chrome-156-macos`, `chrome-156-windows`, `chrome-157-macos`, `chrome-157-linux`, `chrome-157-ios` |
| Firefox | `firefox-158-macos`, `firefox-158-windows`, `firefox-159-macos`, `firefox-159-windows`, `firefox-159-linux`, `firefox-159-ios` |
| Safari | `safari-27-macos`, `safari-26-macos`, `safari-27-ios`, `safari-26-ios` |
| Edge | `edge-157-macos`, `edge-157-windows` |
| Brave | `brave-157-macos`, `brave-157-windows`, `brave-157-linux` |

Where each was captured:

- **macOS:**
  - Safari 27 on macOS 27.
  - Safari 26 on a second Mac kept on macOS 26.
  - Chrome 157, Firefox 159, Edge 157 and Brave 157 on that second Mac (on 26.2 by Edge's run).
- **Windows:** Chrome 156, Firefox 158 and 159, Edge 157 and Brave 157, on Windows 10.
- **Linux:** Chrome 157.0.8103.38, Firefox 159.0.1 and Brave 1.99.55.
  - Google's, Mozilla's and Brave's own builds.
  - In WSL2 (Ubuntu 24.04) on that Windows PC.
- **iOS:**
  - Safari 27.2, Chrome 157.0.8103.24 and Firefox 159.1 on an iPhone on iOS 27.2.
  - Safari 26.6.2 on a second iPhone on iOS 26.7.1.

## How rows are classed

Each table row is one profile field. Reading a row:

- The first column names the profile field.
- Each browser column gives the class, then the measured value and sample counts.
- The last column names the test that fails if the profile and the captures drift apart.
- "Known gap" marks a difference mimicry does not imitate.

The classes:

| Class | Meaning |
|---|---|
| **W** | Seen on the wire [1] |
| **W+** | **W**, and stable across the other server scenarios [2] |
| **F** | Fitted rule, verified by re-encoding every captured header block [3] |
| **K** | Measured on a different kind of request [4] |
| **C** | Copied from another profile, not measured for this one |
| **I** | Inherited from an earlier version's captures [5] |
| **—** | Does not apply to this browser |

Notes:

1. The browser against mimicry's capture server, three samples (three browser launches for Chrome and Firefox), identical across samples unless the row says otherwise. Captures are in `internal/harness/testdata/captures/`.
2. The server scenarios are `set-cookie`, `large-settings`, `small-limits`, `huge-table`, `explicit-table`, `slow-idle` and `http1`; where noted, also the six replayed real pages.
3. Not visible as such on the wire. A rule chosen to explain the traces, then verified by re-encoding every captured header block byte for byte (`TestHPACKReproducesBrowserBlocks`).
4. For example, measured on `fetch()` uploads and used for navigations.
5. Checked against an earlier version's captures (the profile's `capturesFrom`, decision 0035).
   - Only version tokens and the navigation's `accept-encoding` in header values are rewritten (`capture.Rewriter`).
   - The test passes for this profile, but this version's own traffic was not captured.
   - Written with the source version's class: `I: from chrome-155 (W+)`.

How classes change over a profile's life:

- A new version's column starts as **I** for every row whose evidence is a scenario in its `capturesFrom`.
- Rows from the scenarios a quick refresh recaptures (the fingerprint layer, `server.QuickScenarios`) get their own class.
- A full refresh replaces every **I** with the class its new captures earn.
- Deprecated profiles (`"deprecated": true`, older than the newest five versions) keep their columns as last reviewed.

Browser source code and other clients' presets:

- No claim rests on them alone: they were leads only.
- Where another client disagreed with the wire, the wire won.
- For one: httpcloak's single joined Safari cookie field. Every Safari 27 sample splits cookies.

## Behaviour in code, not in profiles

Behaviour mimicry's code fixes for every profile. Each item: what it does, the evidence, and what checks it.

- **A fully read response is not reset:** no RST_STREAM; it waits for END_STREAM up to 10 s.
  - Evidence: W. No browser resets streams it read.
  - Checked by `TestClosingFullyReadBodyDoesNotReset`.
- **Uploads read in 65535-byte chunks.**
  - Evidence: W for the first 64 KiB of Chrome and Safari uploads. Later chunks, and all of Firefox's, are not modelled.
  - Checked by `TestConnectionMatchesBrowserTrace` (largest frame only).
- **HTTP/1.1-only servers get parallel connections at once;** HTTP/2 candidates wait for the first.
  - Evidence: W (`resume`: three parallel requests, three connections opened together).
  - Checked by `TestResumptionMatchesBrowser`.
- **END_STREAM on an upload's last DATA frame** (body of known length), with no empty frame after.
  - Evidence: W (the capture page's upload, every profile, every sample).
  - Checked by `TestDataFrameCap`.
- **`Config.Header`' Authorization and Cookie are not sent once a redirect chain has left an origin.**
  - Evidence: the Fetch standard (Authorization on a cross-origin redirect). Not captured.
  - Checked by `TestConfigCredentialsStayBehindCrossOriginRedirects`.
- **A CORS preflight carries only the `Config.Header` the preflight kind's lists name** (a User-Agent or Accept-Language override), never others such as an API key.
  - Evidence: follows the request it precedes. Not captured (browsers have no `Config.Header`).
  - No test.
- **The HelloRetryRequest cookie is placed at a random non-pinned position** (uTLS fork), where the profile has no `tls.helloRetry`, and in every QUIC hello.
  - Evidence: not captured.
  - Known gap.
- **WebSocket messages go as one frame each.**
  - Evidence: W for Firefox and Safari (every message).
  - Chrome sends the 71680-byte text whole in 3 of 6 samples, split 65464 + 6216 in the others. mimicry always sends the whole form.
  - Checked by `TestWebSocketsMatchBrowser`.
- **`ws://` from an `https://` page is refused before connecting.**
  - Evidence: W. Chrome throws `SecurityError`; Firefox and Safari fail, and no handshake reaches the server.
  - Checked by `TestWebSocketsMatchBrowser` (`mixed`, every profile, both scenarios: the dial errors and no handshake reached the server).
- **Firefox's socket transport** is left to the server, and ended by mimicry 10 s after the close handshake.
  - HTTP/1.1: TCP close. HTTP/2: RST_STREAM NO_ERROR.
  - A stream the server ended is forgotten without a frame.
  - Evidence: Firefox's own end is not captured. It is an unmeasured fallback, so a socket cannot leak.
  - No test (known gap).
- **Chrome over HTTP/2: `Close` returns once the server has ended the socket's stream.**
  - mimicry's END_STREAM went with its close frame, so a later request's chained priority never depends on it.
  - A stream the server does not end is reset after 10 s. Chromium waits 2 s, then drops the stream.
  - Evidence: Chromium's `WebSocketChannel` fires its close event only after the stream closed ([INFERENCE], from its source).
  - Evidence: every captured socket handshake, each opened after the one before had closed, depends on stream 0 (`websocket-h2`, `proxy-websocket*`).
  - The 10 s fallback is not captured.
  - Checked by `TestWebSocketsMatchBrowser` (each socket's handshake priority). The fallback: no test (known gap).
- **A close handshake the server never answers ends the transport after 10 s.**
  - Evidence: not captured.
  - No test (known gap).
- **Firefox's HTTP/2 segmenting**, which counts a frame header into its 16000 bytes (14 + 15986).
  - Evidence: W, not modelled. Only the cap and header-alone are.
  - Known gap.
- **`ErrNotMeasured`** for:
  - an empty message on a compressing socket;
  - a server answering `client_max_window_bits` under 15, or `client_no_context_takeover` (on sending);
  - WebSockets through a proxy without `proxy.websocket`, with proxy credentials without `proxy.websocket.credentials`, and ws:// through SOCKS5.
  - Evidence: not captured.
  - Checked by `TestWebSocketsMatchBrowser` (Firefox deflate) and `TestUnmeasuredProxyIsAnError` (the proxy cases). The rest: no test.
- **A cross-site socket over HTTP/2 may share the cross-site host's own first-party connection** (no top-level-site partition).
  - Evidence: W. Browsers use only the page's partition's connection (the one that carried `b-set-3p`).
  - Known gap.
- **Read limits:** 64 MiB per message, 64 queued messages.
  - Evidence: safety bounds, not captured.
  - Checked by `TestReadingServerFrames` (server frames, hostile input).

## Keeping it true

- Change a profile, change its row.
- A new browser version starts as a copy of the previous one ([decision 0004](../docs/decisions/0004-complete-profile-files.md); `cmd/refresh` does the copy).
- Mark what it copied as **C** until its own captures pass the tests. That includes everything a capture cannot show: `http2.hpack`, header slots, `cookies`, `http1` slots.

## Review history

- 2026-10-24: last full review.
- 2026-10-28: rows updated with that day's captures.
- 2026-10-29:
  - `chrome-156-windows` added with its captures.
  - The "Not covered" list rechecked against the code.
- 2026-10-30: the redirect rows, with the cookie twins (0048).
- 2026-11-01:
  - `safari-27-ios` and `safari-26-ios` (0051).
  - `firefox-158-windows`.
  - `chrome-157-ios` (0053).
- 2026-11-03:
  - `firefox-159-ios` (0053, amended).
  - `firefox-159-macos` and `firefox-159-windows`, quick refreshes.
- 2026-11-04: the client hints rows, with the `client-hints` captures (0055); the URL rows, with the `urls` captures (0057).
- 2026-11-05: `chrome-157-linux` and `firefox-159-linux` (0058).
- 2026-11-06: `edge-157-macos` (0059).
- 2026-11-08: the `tls.clientAuth` rows, with the `client-cert-*` captures (0064).
- 2026-11-09:
  - the Brave profiles (0067);
  - `chrome-157-macos`, quick refresh.
