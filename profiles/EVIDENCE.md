# Evidence ledger

This file lists every claim the built-in profiles make, where it comes
from, and what keeps it true. A claim is one profile field, such as the
TLS cipher list or a cookie rule.

**Covered:** `chrome-155-macos`, `chrome-156-macos`, `chrome-156-windows`,
`firefox-158-macos`, `safari-27-macos` and `safari-26-macos`. Safari 27 was
captured on macOS 27; Safari 26 on a second Mac kept on macOS 26; Chrome
156 for Windows on Windows 10.

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
captures, `chrome-156-windows` added with its 2026-10-29 captures, and the
redirect rows with the 2026-10-30 cookie twins (0048). The
"Not covered" list was rechecked against the code on 2026-10-29.

## How rows are classed

| Code | Meaning |
|---|---|
| **W** | Seen on the wire: the browser against mimicry's capture server, three samples (three browser launches for Chrome and Firefox), identical across samples unless the row says otherwise. Captures in `internal/harness/testdata/captures/`. |
| **W+** | **W**, and also stable across the server scenarios (`set-cookie`, `large-settings`, `small-limits`, `huge-table`, `explicit-table`, `slow-idle`, `http1`) and, where noted, the six replayed real pages. |
| **F** | Fitted: not visible as such on the wire. A rule chosen to explain the traces, then verified by re-encoding every captured header block byte for byte (`TestHPACKReproducesBrowserBlocks`). |
| **K** | Measured on a different kind of request than the profile describes (e.g. on `fetch()` uploads, used for navigations). |
| **C** | Copied from another profile, not measured for this one. |
| **I** | Inherited: checked against an earlier version's captures (the profile's `capturesFrom`, decision 0035), with only version tokens in header values rewritten (`capture.Rewriter`). The test passes for this profile, but this version's own traffic was not captured. Written with the source version's class: `I: from chrome-155 (W+)`. |

A new version's column starts as **I** for every row whose evidence is a
scenario in its `capturesFrom`. Rows from the scenarios a quick refresh
recaptures (the fingerprint layer, `server.QuickScenarios`) get their own
class. A full refresh replaces every **I** with the class its new captures
earn. Deprecated profiles (`"deprecated": true`, older than the newest five
versions) keep their columns as last reviewed.

**`chrome-156-macos`** (quick refresh, 2026-10-26) has no column of its own.
Read its class from `chrome-155-macos`'s column. Rows whose evidence is a
quick-refresh scenario (`page`, `http1`, `kinds`, `kinds-http1`,
`kinds-plain`, `websocket`, `websocket-h2`, `h3-discovery`, `h3-page`) are
its own **W**: three samples for `page`, `h3-discovery` and `h3-page` (the
TLS and QUIC hellos, where Chrome shuffles), one for the others. `resume` is
its own too (two samples, 2026-10-28), so its resumption rows are its own
**W**. Every other row is `I: from chrome-155`, except the redirect
`cookie`-slot row, its own **W** from the cookie twins (2026-10-30, decision
0048), which Chrome 155 lacks. The refresh found no
difference beyond the user agent and `sec-ch-ua`
([report](../internal/harness/reports/refresh-chrome-156-macos-2026-10-26.md)),
and `realcheck` showed none against the real Chrome 156.

**`chrome-156-windows`** (2026-10-29, Chrome 156.0.8078.80 on Windows 10,
[report](../internal/harness/reports/refresh-chrome-156-windows-2026-10-29.md);
[decision 0047](../docs/decisions/0047-windows-profiles.md)) has no column
of its own either. It inherits nothing: every scenario and replay was
captured on Windows, three samples each, so each row is its own, with the
class and value of `chrome-155-macos`'s column, and the same tests keep it
true. Where Windows differs:

- **The platform:** `user-agent` says `Windows NT 10.0; Win64; x64` and
  `sec-ch-ua-platform` `"Windows"`, in every header list (the navigation,
  HTTP/1.1, every kind and variant).
- **`http3.discovery.fallback.tcpDelayMs`:** 307–314 ms (macOS 305–306).
- **The GREASE transport parameter's `maxLength`:** 14 (macOS 15): the
  longest seen; Chrome draws the length per connection.
- **One gap fewer than on macOS:** Chrome on macOS moved a broken
  origin's requests to a new TCP connection after 6–10 s idle (pinned
  there, `h3EdgeGaps`); Chrome on Windows kept its connection in all three
  `h3-broken` samples, as mimicry does.
- **Pinned as on macOS:** NEW_TOKEN presented on 3 of 9 `h3-discovery`
  reconnections (`h3gen`'s `sampleGaps`; `quic.tokens` true, as macOS),
  one PRIORITY_UPDATE read 1 ms after its HEADERS (1 of 3 samples), the
  preconnect (`connectionsPreconnects`), the XHR header order
  (`headersOrderGaps`), the error-page reload (`redirectsReload`), the
  blackhole resend and resumed probes (`h3EdgeGaps`; the first resumed
  probe 2 Initials in 1 of 3 samples, 4 in the others) and the fast
  fallback's differences (`h3EdgeFastGaps`).
- **Timing only:** the pair's POST in `h3-early` and `h3-too-early` came
  4–6 ms after its GET, after the resumed handshake, and went in 1-RTT (5
  of 6; `h3EdgeSchedulingGaps`); one migration's PATH_RESPONSE went
  before its RETIRE_CONNECTION_ID, the server's challenge having arrived
  first (`h3gen` counts the client's own datagrams); QUIC give-ups 1–15
  ms after 4.0 s (macOS 0–4).
- **The recorder on Windows:** its clock moves in steps of about 0.5 ms,
  so packets of both sides often share a timestamp (the download test
  orders such ties server first), and quic-go's NEW_TOKEN tokens were 89
  or 90 bytes by the loopback RTT they encode (the tests compare whether a
  token is sent, not its length).

`realcheck` on the Windows PC found no difference between Chrome and
`chrome-156-windows` on the echo endpoints.

No claim rests on browser source code or on another client's presets alone:
those were leads only. Where another client disagreed with the wire, the wire
won (httpcloak's single joined Safari cookie field, for one: every Safari 27
sample splits cookies).

## TLS ClientHello

All rows checked by `TestProfilesMatchCaptures` (mimicry's ClientHello
diffed field by field against the capture, extension order as the capture
says). The same e2e diff of mimicry's hello, `TestProfilesMatchCaptures`,
checks every built-in profile's record-layer version,
legacy_version, session ID length, key share lengths and GREASE ECH shape.
`profile.Validate` refuses a profile whose GREASE,
extended_master_secret, signed_certificate_timestamp, status_request or
renegotiation_info bodies differ from the ones uTLS sends, and a shape uTLS
cannot send (legacy_version other than 0x0303, a session ID other than 32
bytes, a key length not its group's, an ECH key other than X25519's), so a
profile cannot claim bytes the handshake would not send.

The ClientHello's shape (`recordVersion`, `legacyVersion`,
`sessionIdLength`, `keyShareLengths`, `x25519Reuse`, `ech`, `resumption.echPayloadLengths`)
is generated by `hellogen` from `<name>.clienthello` and, for the GREASE ECH
sets, from every connection of every page capture that recorded its hello's
shape (and checks `x25519Reuse` against every page and QUIC hello that kept
its bytes), never hand-edited, into the profile and the capture
(`TestRegenerateReproducesProfiles` in `internal/harness/cmd/hellogen`). It
also generates `http2.openingRecords` (below). `<name>.clienthello`
holds one ClientHello per line. Lines 1–3 are the 2026-10-24 retakes (3
browser launches, TLS records starting 0x16; Safari 26 from the second Mac, on macOS 26).
The older lines are bare handshake messages (starting 0x01): the previous
committed capture (`3b1c36f`; Safari 26: `f403006`) and, for Chrome 155,
Firefox 158 and Safari 27, earlier captures restored from git history
(`053c80c`, `7958315`, `8f56566`, then `2efb66b` or `965674c`). So the
full-hello shape rests on 8 hellos per browser (4 for Safari 26), plus
every page-capture connection with a recorded shape (Chrome 118 full and 21
TLS 1.3 resumed hellos, Firefox 111 and 21, each Safari 120 full);
the default scenario kept no other raw hellos, so none is **W+**.
`TestParseClientHelloSeesEveryShapeChange` changes
each recorded value in every browser's real hello and requires a difference
or a refusal.

| Claim (`tls.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 | Notes |
|---|---|---|---|---|---|
| `cipherSuites`, `compressionMethods` | W+ | W+ | W+ | W+ | |
| `extensionOrder` | W: shuffled (8 connections, 8 orders, GREASE pinned first and last) | W: fixed | W: fixed | W: fixed, and identical to Safari 27's | Two installs on two macOS versions agree, so Safari's order is not per install. Shuffle uniformity: `TestShuffleIsUniform`; GREASE pinned: `TestProfilesMatchCaptures`. |
| `extensions` (types, contents: groups, key shares, signature algorithms, versions, ALPN, ALPS, point formats, PSK modes, certificate compression, record size limit, raw data such as Chrome's trust anchors 0xca34) | W+ | W+ | W+ | W+ | Chrome's trust anchor list was identical in all samples. |
| `legacyVersion`, `sessionIdLength` | W (8 hellos + 139 page connections): 0x0303, 32 | W (8 + 132): 0x0303, 32 | W (8 + 120): 0x0303, 32 | W (4 + 120): 0x0303, 32 | The session ID's bytes are random per connection. Resumed hellos (TLS 1.3 and 1.2) alike. |
| `extensions[key_share].keyShareLengths` | W (8 + 139): GREASE 1, X25519MLKEM768 1216, x25519 32 | W (8 + 132): X25519MLKEM768 1216, x25519 32, secp256r1 65 | W (8 + 120): as Chrome | W (4 + 120): as Chrome | A group fixes its key's length; only GREASE's is a choice. Resumed hellos alike. |
| `x25519Reuse` (one X25519 key in both key shares: X25519MLKEM768's last 32 bytes are the x25519 share's, in every hello carrying both) | W (8 `.clienthello`): no, a separate key in all 8 (Chrome 156: 3 of 3; its `resume` 2026-10-28: 12 full and 13 resumed TCP hellos, none; its QUIC hellos: 24 full and 9 resumed, none) | W (8): yes, the same key in all 8; `resume` (2026-10-28, 2 samples): 10 full and 14 resumed TCP hellos, all 24; QUIC (the h3 captures, which keep each hello's bytes): 54 full and 69 resumed, all 123 (the 3 `h3-vn` retries send x25519 alone) | W (8): no; `resume` (2026-10-28, 2 samples): 24 full TCP hellos, none; QUIC 33 full and 60 resumed, none | W (4): no; `resume` (2026-10-28): 24 full TCP hellos, none; QUIC 33 and 60, none | Every hello: full and resumed, TCP and QUIC. Page captures keep each TCP hello's bytes since 2026-10-28 (`clientHello`, hex, beside the parsed `hello`); QUIC captures always did. No key repeats across connections in any capture (150 Firefox hellos, 117 Safari 26, 117 Safari 27, 58 Chrome 156): keys are fresh per connection. Derived by `hellogen` from the full hellos (`capture.Combine` refuses samples that disagree), which then refuses any page or QUIC hello with both shares that disagrees. Checked by `TestProfilesMatchCaptures` (full hellos), `TestResumptionMatchesBrowser` (resumed TCP) and `TestHTTP3MatchesBrowser` (QUIC), each from the hellos' bytes (`diff.Compare` leaves it out). uTLS sends the hybrid's X25519 key as the x25519 share (`ReuseHybridAndClassicalKeyShares`): one X25519 keygen fewer per hello (about 29 µs and 5 allocations on an Apple M3 Pro). |
| GREASE ECH (0xfe0d) `ech` | W (8 `.clienthello` + 118 full page connections): one suite, KDF 0x0001 (HKDF-SHA256) and AEAD 0x0001 (AES-128-GCM), in all; `encLength` 32; `payloadLengths` {144, 176, 208, 240}: one per connection (page connections: 32, 34, 27, 25 times), BoringSSL's four lengths | W (8 + 111 full): `cipherSuites` {0x0001/0x0001, 0x0001/0x0003 (ChaCha20Poly1305)}: one per connection (56 and 55 times on page connections); `encLength` 32; `payloadLengths` 240 in every full hello, TLS 1.2 resumptions included | — | — | Type outer in every hello; config_id a random byte per connection (not stored). The sets used to come from the `.clienthello` samples only (Chrome {176, 208}); every page connection now feeds them (`hellogen`). mimicry picks the suite and payload length per connection from the profile's sets (uTLS `GREASEEncryptedClientHelloExtension`); `diff.Compare` requires each connection's to be among the browser's. |
| padding (0x0015), total ClientHello length | none: no padding extension in any hello | none | none | none | Totals: Chrome 1987 or 1955 bytes (the ECH payload choice), Firefox 1893 (both with a 25-byte server_name body), Safari 1527 (a 14-byte one: the capture host names differ): implied by the fields above, not stored. The capture parser refuses padding that is not zeros or not BoringSSL's length, the only rule a profile can describe. |
| `recordVersion` | W (3 `.clienthello` records + 139 page connections, all 3 samples of every page capture): 0x0301 | W (3 + 132): 0x0301 | W (3 + 120): 0x0301 | W (3 + 120): 0x0301 | The 2026-10-24 retakes keep the records; the older `.clienthello` lines (bare messages) carry none and are left out of the check (`capture.Combine`). mimicry sends 0x0301 (uTLS writes TLS 1.0 records before a version is negotiated; `handshake.Spec` refuses anything else; the e2e diff of mimicry's hello, `TestProfilesMatchCaptures`, checks it); `diff.Compare` compares it where both sides kept it. |
| key share material, random, session ID bytes, ECH config_id, key and payload bytes | generated per connection | | | | Values, not claims. |
| `resumption` (TLS 1.3: full hello minus `drop`, `pre_shared_key` last, single-use tickets, no 0-RTT; `tls12`: ticket in `session_ticket`, used as the policy says) | W: drops nothing; `tls12: reuse` | W: drops `session_ticket`; `tls12: single_use` | W: none (never resumes, TLS 1.3 or 1.2) | W: none, as Safari 27 | `resume`, `resume-tls12` scenarios ([report](../internal/harness/reports/resume-2026-10-24.md)); `TestResumptionMatchesBrowser` (used connections and resumptions per host, every resumed hello diffed). A TLS 1.3 ticket past its lifetime or its certificate's expiry is not offered, so the hello is a full one (`TestTicketPastItsLifetimeIsNotOffered` (certificate expiry: no test)); a TLS 1.2 ticket lasts until its certificate expires (its lifetime is not measured) |
| a resumed ClientHello's shape (`legacyVersion`, session ID and key share lengths, ECH: `resumption.echPayloadLengths`) | W (21 TLS 1.3 resumed hellos: `resume` 3 samples, `cookies-edge` 2 of 3): as the full hello; ECH suite AES only, `payloadLengths` {144, 176, 208, 240} (5, 2, 7, 7 times), the full hello's set: BoringSSL's GREASE length does not depend on resumption. Chrome 156's own `resume` (2026-10-28, 2 samples, 13 resumed): AES only, {144, 176, 208, 240} (2, 4, 3, 4) | W (14, `resume` 2026-10-28, 2 samples, 7 each; the 2026-10-24 capture showed the same in 21): as the full hello; ECH suites both (8 AES, 6 ChaCha); `payloadLengths` 400 in all 14 (240 when full): NSS pads for the inner hello, which carries the PSK | — | — | `TestResumptionMatchesBrowser` diffs each mimicry resumed hello against the browser's, ECH sets taken from every resumed connection of every sample (the profile-shape fallback is gone). Firefox's 400 was measured with the capture server's tickets only; that it scales with ticket size is [INFERENCE], unmeasured. TLS 1.2 resumptions (session_ticket) keep the full hello's ECH lengths (Chrome {144..240}, Firefox 240). |
| `websocketH1` (the hello of a WebSocket's own HTTP/1.1 connection: `alpn`, `drop`) | W (`websocket`: 12 socket connections, 4 per sample, all TLS 1.3 resumed): `alpn` `http/1.1`, `drop` ALPS (0x44cd); otherwise the resumed hello (`pre_shared_key`) | W (12, all resumed): `alpn` `http/1.1`, `drop` `compress_certificate` (0x001b), which Firefox's other resumed hellos keep; `session_ticket` dropped as `resumption.drop` | W (12, full): `alpn` `http/1.1`, nothing dropped | W (12, full): as Safari 27 | Generated by `hellogen`, which diffs every socket connection's hello against the profile's with these changes and refuses any other difference. A full-handshake socket hello for Chrome and Firefox (no ticket for the host) is not captured: the same changes are assumed ([websocket report](../internal/harness/reports/websocket-2026-10-24.md)). |
| `quic` (the hello sent over QUIC: `cipherSuites`, `sessionIdLength`, `extensionOrder`, `last`, `drop`, `replace`, `transportParametersBefore`, `resumedEchPayloadLengths`) | W (`h3-discovery`, `h3-page`, with and without `-h3-local-roots`: 33 QUIC connections, 9 resumed): TLS 1.3 suites without GREASE; session ID 0; shuffled, no GREASE extension; drops GREASE, `ec_point_formats`, SCT, EMS, `renegotiation_info`, `session_ticket`, `status_request`; replaces ALPS and ALPN (`h3`), key shares and groups (no GREASE), signature algorithms (no ML-DSA, `0201` added), `supported_versions` `0304`; ECH from the TLS sets (9 resumed showed 144 and 240 of the four) | W (33, 9 resumed): suites `1301 1303 1302`; shuffled with `quic_transport_parameters`, then `encrypted_client_hello` last; drops `ec_point_formats`, `session_ticket`, SCT; replaces ALPN, `supported_versions`, signature algorithms (`0203` 4th), `compress_certificate` `0001 0003 0002`; resumed ECH payload 432 (TCP 400) | W (3): GREASE + TLS 1.3 suites; fixed: TCP's order without EMS, `renegotiation_info`, `ec_point_formats`, `quic_transport_parameters` before `compress_certificate`; ALPN `h3`, `supported_versions` GREASE `0304` | W (3): as Safari 27 | Generated by `hellogen` from every QUIC connection's ClientHello, which it diffs against the TLS hello with these changes (order exactly when fixed; `last` and `pre_shared_key` at the end when shuffled; a resumed hello also without `resumption.drop`, with `earlyData` 0x002a) and refuses any other difference. Safari never resumed over QUIC. The transport parameters' body is `quic.transportParameters` (below) |
| `quic.versionNegotiation` (the hello of a connection retried after Version Negotiation: `drop`, `replace`, `padding`) | — (no retry) | W (`h3-vn`, 3 connections retried in v2): the full QUIC hello without `compress_certificate` and GREASE ECH; groups and key shares without X25519MLKEM768 (key share X25519 only, 32 B); padding (0x0015) after `quic_transport_parameters`, last, the hello 512 bytes (NSS's 256–511 → 512); shuffled as the full one | — (no retry) | — | Generated by `hellogen` from every h3-vn connection that followed a Version Negotiation in another version, diffed against the full QUIC hello; any other difference is refused. Required iff `quic.versionNegotiation.retry` |

Cross-checked outside mimicry's capture server: Firefox 158's JA3, and Safari 27's JA3, JA4
and Akamai fingerprints, match what tls.peet.ws reports for the same
browsers.

## HTTP/2 connection

| Claim (`http2.…`) | Chrome 155 | Firefox 158 | Safari 27 / 26 | Checked by |
|---|---|---|---|---|
| `settings` (ids, values, order) | W+ | W+ | W+ | `TestProfilesMatchCaptures` |
| `windowUpdate` (connection credit after the preface) | W+ | W+ | W+ | `TestProfilesMatchCaptures` |
| `priorities` (PRIORITY frames at connection start) | W: none | W: none | W: none | `TestProfilesMatchCaptures` |
| `openingRecords` (how the preface, SETTINGS, WINDOW_UPDATE and the first request's frames are cut into TLS records, when the request is waiting as the connection opens) | Chrome 155: W (chrome-155: Chrome for Testing 155.0.8059.84, same network stack as branded; other fields from branded captures. `resume` 2026-10-28, 2 samples, all 14 connections: `chrome-155-macos.resume.chrome-for-testing.pagetrace.json.gz`, which feeds this field alone): `[preface SETTINGS WINDOW_UPDATE] [HEADERS]`. Chrome 156: W (`resume` 2026-10-28, 2 samples, all 14 HTTP/2 connections, fresh and resumed, several opened while the page's was live): the same | W (`resume`, 2 samples: 7 connections; 2 more end mid-record at the page's `/done`, which stops the trace): `[preface SETTINGS WINDOW_UPDATE] [HEADERS WINDOW_UPDATE]`; later requests' HEADERS and stream WINDOW_UPDATE share a record too | W, both (`resume` 2026-10-28, 2 samples each, 12 connections each; Safari 27.0.1 on macOS 27): `[preface SETTINGS WINDOW_UPDATE HEADERS]`, one record | Generated by `hellogen` (`capture.OpeningRecords`) from every HTTP/2 trace that kept its records (the capture server reads the record stream under TLS since 2026-10-28: `records`, TLS 1.3 only), skipping connections whose SETTINGS ACK went before the first HEADERS: the browser opened them ahead of the request, which mimicry never does (Firefox: 5 of 14, the same cut with the ACK alone between; Safari 27 and 26: the tab's first connection, 2 of 14 each, the opening alone, HEADERS after the ACK). It refuses connections that disagree. Testing-build captures (`<name>.<scenario>.chrome-for-testing.pagetrace.json.gz`, taken with `cmd/capture -chrome-app`) are no scenario's capture: `capture.Files` and `ScenarioOf` skip them, so only this field reads them. `TestConnectionMatchesBrowserTrace` compares mimicry's cut with it. A request's frames go out in one write (`internal/h2`): Firefox's HEADERS and stream WINDOW_UPDATE share a record on every request captured; a header block long enough for CONTINUATION is not captured. Decision 0042 |
| `headersPriority` | W: exclusive, weight 255 | W: weight 41 | W: none | `TestProfilesMatchCaptures` |
| `chainPriorities` (each request depends exclusively on the newest stream still open at the nearest weight at or above its own, Chromium's lists; a stream leaves them at the peer's END_STREAM or a reset, whether or not its body was read) | W (every Chrome HTTP/2 trace, replays included: 3006 of 11795 HEADERS of Chrome 155 and 156 depend on a stream, never one of lower weight, and every connection fits the rule; the newest open stream regardless of weight cannot fit 9 of Chrome 155's 1622 connections. A stream is still a parent after 3 MB of its response arrived, `replay-nike.com`, so not at its response headers, and none is once the page has the whole response; that the moment is its END_STREAM, not the body's reading, is Chromium's code, `SpdyStream::OnDataReceived`: [INFERENCE]) | W: never (stream 0, not exclusive) | W: never (no priority) | `TestChainedPrioritiesMatchBrowserTraces` (every trace's requests sent again, each stream ended after the last that depends on it, half with their bodies unread); `TestEndedStreamLeavesChainBeforeDelivery` (unit: a stream leaves the chain before its end reaches the caller) |
| `pseudoHeaderOrder` | W+ | W+ | W+ | `TestProfilesMatchCaptures` |
| `firstStreamId`, `streamWindowUpdate` | — (1, none) | W+: stream 3, 12451840 per stream | — | `TestProfilesMatchCaptures` |
| `maxDataFrame` (upload DATA cap) | W+: 16375 | W+: 16000 | W+: 16374 | `TestConnectionMatchesBrowserTrace` (largest frame equal, under the capture server's default SETTINGS), `TestDataFrameCap` (frame sizes, END_STREAM on the last DATA frame). The browsers keep their cap when the server allows larger frames (`large-settings`); no mimicry test raises the server's MAX_FRAME_SIZE |
| `streamUpdateThreshold`, `connectionUpdateThreshold` | W | W | W | `TestConnectionMatchesBrowserTrace`: every increment within the browser's own observed range ± one frame. Firefox's `streamUpdateThreshold` is range-checked only: its stream credit follows the page's reads, and the range it spans is too wide to pin the threshold. Safari's connection overshoot is timing-driven; only the range is matched. |
| `windowUpdateIntervalMs` (credit returned after 5 s however little) | W (`slow-idle`) | — | — | `TestWindowUpdateInterval` (unit, fake clock) |
| `idlePingMs` (PING after 10 s idle, close if unanswered) | W (`slow-idle`) | — (never pings) | — (never pings) | `TestIdlePing` (unit, fake clock) |
| `splitCookie` (one HPACK field per cookie) | W+ | W+ | W+ | `TestCookiesMatchBrowserTrace` |

## HPACK

All rows **F**, checked by `TestHPACKReproducesBrowserBlocks`: every header
block of every sample of every scenario, and of every connection of the six
replayed pages for every profile (4249 connections), re-encoded byte for
byte.

| Claim (`http2.hpack.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `withoutIndexing` | `:path`; `:method` (seen only on `OPTIONS` preflights in replays: GET and POST are exact static-table matches, so the rule cannot show for them); `:protocol` (the `websocket-h2` extended CONNECTs) | `:path` | `:path`, `content-length` | as Safari 27, verified on its own scenarios and replays |
| `neverIndexed` | — | `authorization` (replays) | `authorization` (replays: nytimes 3, zillow 9) | `authorization` (its own replays: nytimes 3, zillow 9, as Safari 27) |
| `neverIndexBelow` | — (indexes every cookie) | `cookie` under 20 bytes (19 never, 20 indexed) | same | same |
| `literalOnExactMatch` | — | yes | — | — |
| `nameMatch` | static first | highest index | static first | static first |
| `huffman` | when shorter | always | when shorter | when shorter |
| `maxTableSize` | server's | server's | 4096 (`huge-table`) | 4096 |
| `sizeUpdates` | on change (`huge-table`, `explicit-table`) | always | always | always |

## Request headers

| Claim | Evidence | Checked by |
|---|---|---|
| `headers`: navigation header names, order, default values over HTTP/2 | W+ (Safari 26: W+, differs from 27 only in user agent and `accept-encoding`) | `TestProfilesMatchCaptures` |
| `referer`, `cookie` slots in `headers` | W (the `/nav` navigation over HTTP/1.1; `cookies`, `c-nav-get.so`, a script navigation carrying both, over HTTP/2, 3 samples each, all alike) | over HTTP/1.1 by `TestHTTP1MatchesBrowserTrace`; over HTTP/2 by `TestCookiesScenarioMatchesBrowser` (HPACK-decoded lines against every sample; swapping the two slots in Chrome's or Firefox's `headers` fails it) |
| `content-length` slot in `headers` | **K**: placed where each browser put it on `fetch()` uploads (Chrome first, Firefox after `referer`, Safari before `accept-language`). A navigation POST takes `variants` instead (below) | `TestKindsMatchBrowserTraces` (form POST) |
| `variants`: the navigation's order for a form POST to its own origin (Chrome adds `cache-control: max-age=0`; Firefox moves `referer`, `cookie`; Safari uses its CORS-checked order) | W (`kinds`, `kinds-http1`, Safari 26 included) | `TestKindsMatchBrowserTraces` |
| `variants` `{POST, cross-site, corsChecked}`: a cross-site form POST navigation | W (`cookies`, `c-nav-post.so`; HTTP/2 only, so no `http1` list) | `TestCookiesScenarioMatchesBrowser`. A same-site form POST was not captured: it is an error, since the navigation's own lists have no Origin slot (the rule: `TestRefusedRequests`) |
| `accept-language` value | W, but it is this machine's language setting, not a browser constant | `TestProfilesMatchCaptures` |
| `http1.headers`: HTTP/1.1 names as sent (casing), order, values, Host and Connection positions | W+ (`http1` scenario, every browser and Safari 26) | `TestHTTP1MatchesBrowserTrace` (`/` and `/nav`, line for line) |
| `Content-Length` slot in HTTP/1.1 lists (`http1` of kinds and navigation `variants`; `internal/h1` places it by the list) | W (`kinds-http1`, `kinds-plain`: `fetch-json.{so,ss,cs}` 7 bytes, `beacon.{so,ss,cs}` 1 byte, `form-post.so` 3 bytes, Safari's preflights' `Content-Length: 0`; every profile). The navigation's own `http1.headers` slot is **K** (`fetch()` uploads) and no measured request reaches it: a navigation POST takes `variants` | `TestKindsMatchBrowserTraces` (kinds-http1, kinds-plain: line for line, `Content-Length` included). Mutation, 2026-10-24: moving `Content-Length` last in `internal/h1` (`-overlay`) fails it for all four profiles, both scenarios (`fetch-json`, `beacon`, `form-post.so`, Safari's `fetch-custom` preflights), and `TestHeadersOrderMatchesBrowser` (Safari's HTTP/1.1 preflights) |

## Cookies

`cookies` section ([decision 0012](../docs/decisions/0012-cookie-rules-in-profiles.md)),
from the `set-cookie`, `kinds`, `cookies`, `cookies-edge` and `cookies-edge2` captures
([report](../internal/harness/reports/cookies-2026-10-24.md),
[cookies-edge, cookies-edge2](../internal/harness/reports/cookies-edge-2026-10-24.md)). Checked end to end by
`TestCookiesScenarioMatchesBrowser` (the cookies page walked through
mimicry: every probe's cookies, in order), `TestCookiesEdgeMatchesBrowser`
and `TestCookiesEdge2MatchesBrowser`
(every cookies-edge and cookies-edge2 step's Cookie header against each sample),
`TestKindsMatchBrowserTraces` (every kinds request's cookies) and
`TestCookiesMatchBrowserTrace` (crumbs, HPACK representation); the shared
RFC 6265bis rules by `cookies` unit tests. Safari 26 (captured on the
second Mac, on macOS 26, 2026-10-24): W from its own `set-cookie`, `cookies`,
`cookies-edge` and `cookies-edge2` captures, all as Safari 27 except `redirects`: **`chain`**
(Safari 26 sends cookies on cross-site → same-site redirect hops; 27 blocks
them), and `secureLoopback`: **`false`**.

| Claim (`cookies.…`) | Chrome 155 | Firefox 158 | Safari 27 |
|---|---|---|---|
| `unsetSamesite` | W: `lax` (third-party unset cookie not sent; withheld on cross-site POST only past the window) | W: `none` | W: `none` |
| `laxAllowsUnsafeSeconds` | 120: a seconds-old cookie sent on a cross-site form POST is W; the 120-second end is Chromium's documented value, not captured | — | — |
| `noneRequiresSecure` | W | W | W: `false` (kept and sent) |
| `thirdParty` | W: `samesite`; `Partitioned` keyed by top-level site (withheld under site C) | W: `partition_all` (nothing third-party crosses top-level sites) | W: `partitioned_only` (blocking at store or send is not observable; the jar does both) |
| `redirects` | W: `ignore` | W: `chain` | W: `block_after_cross_site` for subresources (cross-site → same-site and → same-origin image hops alike); a navigation's hops through another site carry their cookies (`nav-redirect-{page,form,click}-cookies`, decision 0048) |
| `nameless` | W: `value` (`knoeq`) | W: `name` (`knoeq=`) | W: `drop` |
| `emptyName` | W (`cookies-edge2`, `nameless-probe`): `keep` (`=x`, `=ctl` sent as `x; ctl`; `=__Secure-x`, `= __Host-x`, `=__secure-x` dropped: the prefix rule in any case, after a space) | W: `drop` (all five dropped, the bare `=x` and the `Secure` `=ctl` too) | W: `drop` (all five) |
| `latin1Values` | W: no | W: no | W: yes (`cafÃ©`) |
| `order` | W+: `rfc` (path length, then creation) | W+: `rfc` | W+: `domain_name` (path length, `Domain` cookies first, name); ordering between `Domain` cookies of different levels not captured |
| `newValueRenews` | W (`cookies-edge`, `rot-probe-2`): `true` (a new value moves the cookie last; the same value again does not) | W: `false` (keeps its place) | — (`domain_name` order hides creation; validation forbids it) |
| `samesiteScheme` | W (`sch-to-http`, `sch-to-https`: `Strict` withheld both ways; `cookies-edge2` again): `schemeful` | W: `schemeless` (`Strict` sent both ways; `cookies-edge2` again) | W (`cookies-edge2`, plain-only port: `sch-to-http` over plain HTTP/1.1 carries `Strict`, `sch-to-https` too): `schemeless` |
| `hostPrefixPath` | W (`prefix-probe`): `attribute` (`__Host-` without `Path` dropped) | W: `attribute` | W: `stored` (kept, default path `/`); a non-`/` default path not captured |
| `httpPrefix` | W (`prefix-probe`): `true` (`__Http-`, `__Host-Http-` without `HttpOnly` dropped); W (`cookies-edge2`, `http-probe`): without `Secure` dropped too | W: `true` (both halves) | W: `false` (kept; `__Http-` without `Secure` kept too, `__Host-Http-` without `Secure` dropped by `__Host-`'s rule) |
| `secureLoopback` | W (`lo-probe`): `true` for `localhost`; other loopback hosts from the Secure Contexts specification | W: `true` | W: `true`; Safari 26 W: `false` |
| `maxPerSite`, `evictTo` | W (`many-probe`): 200 set, the newest 169 sent (180, then down to 150: the numbers are Chromium's source, the wire shows 169); LRU among equals not separable from oldest-first | W: same 169 | W: no limit (all 200 sent, 4,998 bytes) |
| `partitionedFirst` | — (not shown either way: `Partitioned` cookies were the newest in every capture, so `rfc` order puts them last too) | — (same) | W (`websocket`, `websocket-h2`: every request carrying `ws_a_part` with `ws_a_lax` and `ws_a_none`, navigations and sockets alike, sends `ws_a_part` first, against `domain_name`'s name order); Safari 26 W: same. Path length against partition is not captured (every cookie at `/`): the jar puts partition first |
| `websocketUnpartitioned` | W: `false` (cross-site socket: `ws_b_none; ws_b_part3p`, the credentialed-subresource rule) | W: `false` (`ws_b_part3p` only) | W: `false` (`ws_b_part3p` only); Safari 26 W: **`true`** (`ws_b_part3p; ws_b_lax; ws_b_none`, all 6 samples, HTTP/1.1 and HTTP/2). No browser sent `ws_b_part` (the socket host's own partition); a `Strict` cookie on a cross-site socket is not captured (the jar withholds it) |
| A WebSocket handshake's cookies (`cookies.Request.WebSocket`) are a credentialed subresource's of its page; `ws://` and `wss://` count as `http://` and `https://` | W (`websocket`, `websocket-h2`: same-origin sockets carry all three `ws_a_*`; `plain-so` from the `http://` page only the non-`Secure` `ws_a_lax`) | W | W; Safari 26 W. Set-Cookie on a handshake response is not captured |
| A nameless pair whose value holds `=` is refused (code, every browser) | W (`nameless-probe`: `=e_nlctl=1` and the prefixed ones dropped by all) | W | W |
| A nameless value starting with `__Secure-` or `__Host-` (any case, after whitespace) is refused (code) | W (`cookies-edge2`, `nameless-probe`: `=__Secure-x`, `= __Host-x`, `=__secure-x` dropped, `=x` and `=ctl` kept) | — (`emptyName: drop`: no nameless cookie kept to compare) | — (same) |

## Request kinds

Derived from the `kinds`, `kinds-http1`, `kinds-plain`, `cookies` and `cors`
captures by `internal/harness/cmd/kindsgen` (it refuses captures that disagree), for
every profile; Safari 26's equal Safari 27's apart from its user agent and
`accept-encoding`. `TestRegenerateReproducesProfiles` keeps the committed
profiles equal to what kindsgen derives from the committed captures.
Rows from the kinds pages are checked by `TestKindsMatchBrowserTraces`: every
request of the kinds page, made through mimicry as its kind from the page,
equals the browser's (names, order, values, HTTP/2 weight; over HTTP/1.1 line
for line), cookies included. Where the browser's own samples differ, any form
it sent passes and the variance is logged: one Chrome HTTP/1.1 sample sends
`Sec-Fetch-User` on cross-origin frames and the form POST, the others do not
(activation state). `kb_part`, set by an image the page loads, races Safari's
CSS font request (`font-css.cs`): of three samples each, Safari 27 sends it in
all over HTTP/1.1 and two over HTTP/2, Safari 26 in two over HTTP/1.1 and none
over HTTP/2. For that label only (`raceCookieLabels`), the test also accepts
mimicry's request carrying the cookie where no browser sample does, if nothing
else differs, and logs it. Rows from the `cookies` capture are checked by
`TestCookiesScenarioMatchesBrowser` (every probe's full header lines and
weight); rows only the `cors` capture shows are checked by their derivation
only: `TestCORSMatchesBrowser` compares those requests' methods, paths,
cookies and `Access-Control-Request-*` headers, not their header order.

| Claim | Evidence | Notes |
|---|---|---|
| `kinds.*.headers`, `http1`: names, order, values per kind | W | `font` is the CSS `@font-face` load: Firefox's sends `accept-encoding: identity`, unlike its `FontFace` loads |
| `kinds.*.variants`: order by method, site relation, CORS check, page-set headers, redirect (and, for a redirect's request, `crossOriginRedirect` and `originBeforeRedirect`) | W | Safari has many (a CORS-checked request starts `sec-fetch-site, accept, origin`; a cross-site beacon differs from a same-site one; its fetches redirected to another origin drop `sec-fetch-*`, `accept-language` and `priority`, same-origin redirects reorder every header). Every browser orders a redirect's request its own way. Firefox also for cross-origin fetches and preload fonts. A combination not captured falls back to the kind's default order, unless that order has no place for an Origin the request sends: then it is an error (`TestRefusedRequests`). A redirect's request of a kind whose redirects were captured (fetch, image, injected image) takes only its exact variant, else it is an error |
| `pageSet` (a variant for requests carrying a header the page set itself, not one the kind's list names; it implies `corsChecked`) | W (`kinds`: `fetch-custom`; `cors`; `headers-order`: 60 fetch/XHR steps, 3 samples each, every profile) | Firefox: the `*` slot, in the fetch and XHR base lists and its cross-site variants. Chrome, Safari: only where `context.headerTable` cannot place them (redirects: Safari's); elsewhere the table does and kindsgen drops the variant (Chrome's redirected `cors` fetch: its base, the cross-site fetch redirected within its origin, `acao-redirect.cs.hop1`, is now a variant of its own). `TestKindsMatchBrowserTraces`, `TestHeadersOrderMatchesBrowser` |
| `context.headerTable` (Chrome: `blink`, page's first, own `sec-ch-ua*`/`user-agent`, `content-type` as the page's; Safari 26/27: `cf`, own `accept`, `content-type`, `origin`, `referer`, `sec-fetch-*`, `user-agent`, `content-length` late; Firefox: none) | W (`headers-order`, all 3 samples identical; `kinds`, `kinds-http1`, `kinds-plain`, `cors` fetches re-checked) + S (Blink `hash_table.h`, `case_folding_hash.h`, rapidhash; CoreFoundation `CFBasicHash`, `__CFStrHashCharacters`) | Derived by kindsgen (`deriveTable`): every non-redirected fetch/XHR capture with page headers is reproduced from the list without them. Chrome's `content-type` role is a tie (no capture has it with another page header; Blink source says as-page). `TestHeadersOrderMatchesBrowser`, `TestRegenerateReproducesProfiles` |
| Page headers set twice combine into one line `v1, v2` under the first spelling; fetch() sends page names lowercase over HTTP/1.1, XHR as spelled; forbidden `sec-a` dropped (mimicry does not drop it) | W (`headers-order`, `case` set and `around`, every profile) | `TestHeadersOrderMatchesBrowser` |
| Fetch `PUT` variants `{PUT, cross-site, corsChecked}` and the same with `pageSet` | W (`cors`: `put.cs`, and the `maxage.cs` PUT with the page's header; HTTP/2 only) | Firefox, Safari 27 and 26 (Safari places `content-length` differently in the two). Chrome's PUTs merge into its base fetch order, so it has none. Derivation only (see above) |
| `kinds.image`, `kinds.injected_image` variant `{same-site, redirected}` | W (`cookies`, `c-redirect.cs-ss.hop`; HTTP/2 only) | `TestCookiesScenarioMatchesBrowser` |
| Safari 27's `cookie` slot in the redirected same-origin image lists (HTTP/2) | W (`cookies`, `c-redirect.ss-so.hop`: Safari 27 sends cookies on that hop) | `TestCookiesScenarioMatchesBrowser` |
| Firefox's `sec-fetch-storage-access` slot in the base fetch list | W (`fetch-include.cs`: `none` on credentialed cross-site fetches) | `TestKindsMatchBrowserTraces` |
| Variants with no `http1` list (those only the `cookies` or `cors` capture shows) | W over HTTP/2 only | Such a request over HTTP/1.1 is refused with `ErrNotMeasured` rather than sent in a guessed order (`TestHTTP2OnlyVariantOverHTTP1`) |
| `insecure` variants (kinds and navigation): requests to a non-secure origin, HTTP/1.1 lists as sent | W (`kinds-plain`, every profile incl. Safari 26; [report](../internal/harness/reports/plain-http-2026-10-24.md)) | No `Sec-Fetch-*` (Chrome: no client hints either), `Accept-Encoding: gzip, deflate`, Chrome's `Origin` after `User-Agent`, Safari's reorders and `Upgrade-Insecure-Requests` on frames. A variant's values come from the kind's own plain requests, in its condition where captured, else in any (kindsgen `TestRegenerateReproducesProfiles`, `TestKindsMatchBrowserTraces`): Firefox image `Priority: u=5, i`, font `Accept-Encoding: identity`, preload_font `gzip, deflate`; Safari async_script `u=3, i`, script, defer_script and preload_script `u=1, i`, image `u=5, i`. A combination not captured takes the kind's non-secure default, never the secure lists. Loopback hosts count as secure (Secure Contexts specification, not captured). Also `TestKindsMatchBrowserTraces` (`kinds-plain`) |
| `kinds.*.weight` | W | Chrome and Firefox; Safari sends no priority |
| `kinds.websocket` (handshake lists: HTTP/2 extended CONNECT and HTTP/1.1 Upgrade, the cross-site socket folded in, an `insecure` variant for `ws://` from an `http://` page) | W (`websocket` and `websocket-h2`, 3 samples each, every profile: each of the 30 handshakes per browser has its list's order and defaults) | Generated by `kindsgen` (never CORS-checked: Origin is on every handshake). Weight: Chrome 146 exclusive, Firefox 21, Safari none. `:protocol` follows `http2.pseudoHeaderOrder` in every browser: a rule, not a field, and `kindsgen` refuses a capture that breaks it. The `insecure` variant has no `Sec-WebSocket-Protocol` slot (`plain-so` asked for none): protocols there are not measured |
| `kinds.fetch.high`, `.low`; `kinds.injected_image.high`, `.low` | W | Other kinds' fetchpriority is not measured: asking for it is an error |
| `*` (where page-set headers go) | W (`fetch-custom`: X-Mimicry's position) | One position for every page-set header; the browsers place them by name (see "Not covered") |
| `context.storageAccess` | W: Chrome `active`, Firefox `none`, Safari none | Sent on credentialed cross-site requests |
| `context.sameOriginElementOrigin` | W: Chrome, Safari | Origin on same-origin CORS element requests |
| `context.stylesheetReferrer` | W: Chrome, Firefox | Safari refers CSS fonts to the page; only same-origin fonts captured, at `/` |
| `context.anonymousConnections`, `frameConnections` | W: Chrome | Requests without credentials, and a cross-site frame's navigation, go on connections of their own; checked by `TestKindsMatchBrowserTraces` (which requests share a connection, every sample). Safari: one connection per host, as mimicry's default. Firefox: no rule (see "Not covered"); the test pins its known differences label by label (`partitionGaps`), and fails on any other or on one no longer seen |
| Referer by strict-origin-when-cross-origin; Origin per the Fetch standard; credentials per mode | W where captured, otherwise the standard | The page sits at `/`, so full URL and origin coincide except for the CSS font |
| After a redirect: sec-fetch-site the chain's most distant relation (a navigation's: `redirects.navigationSite`), `Origin: null` once the chain is tainted, the page's Referer (not `http.Client`'s) with the policy applied to each hop in turn (origin only once the chain left the page's origin), storage access by the URL's own relation | W (`fetch-redirect`, `image-redirect`, both directions; `nav-redirect-page` for the Referer back on the page's origin) | |

## CORS preflights

`kinds.preflight` and `context.preflightCache` ([decision 0017](../docs/decisions/0017-cors-preflights.md)),
from the `kinds`, `kinds-http1`, `kinds-plain` and `cors` captures
([report](../internal/harness/reports/cors-2026-10-24.md)), every profile including
Safari 26. All rows **W**.

| Claim | Checked by | Notes |
|---|---|---|
| `kinds.preflight`: the `OPTIONS` request's names, order, values, weight; HTTP/1.1 lines; non-secure variants | `TestKindsMatchBrowserTraces` (every preflight of the kinds page; none where the browser sent none) | Priority and weight are the browser's `fetch()` ones; Safari sends `content-length: 0` |
| When a request is preflighted: `fetch()` in CORS mode or XHR, to another origin, with a method other than GET/HEAD/POST or a header outside the Fetch safelist | `TestKindsMatchBrowserTraces`, `TestCORSMatchesBrowser`; the safelist's edges by `TestPreflightWhenNeeded` | The safelist's edges (long values, ranges, languages) follow the Fetch standard, not captures |
| `Access-Control-Request-Headers`: lowercase, sorted, joined by `,`; absent without unsafe headers | `TestCORSMatchesBrowser` (`multi.cs`, `put.cs`) | |
| The preflight carries no cookies; Chrome sends it on its credential-less connection | `TestCORSMatchesBrowser` (`include.cs`) | Firefox and Safari: the requests' own connection |
| Cache lifetime: `Access-Control-Max-Age`, else 5 s | `TestCORSMatchesBrowser` (Max-Age 600, the default at once and after 6 s); `TestPreflightCacheLifetime` (the bounds, fake clock) | Max-Age caps not measured: honoured as sent |
| `context.preflightCache`: `replace` (Chrome, Safari 27, 26), `merge` (Firefox) | `TestCORSMatchesBrowser` (`maxage.cs`, the request after another header's preflight) | Derived by kindsgen from the `cors` capture |
| A refused preflight (status not 2xx, or a header not allowed): no request | `TestCORSMatchesBrowser` (`denied-*.cs`); the other refusal rules by `TestPreflightRefused` | Origin, credentials and wildcard rules follow the Fetch standard; only these two refusals were captured |
| A redirected request is preflighted again for its new URL | `TestCORSMatchesBrowser` (`redirect.cs`) | |

### Redirects

`redirects` and the transport's redirect rules, from the `cors-edge`
capture ([report](../internal/harness/reports/cors-edge-2026-10-24.md)), three
samples per browser, every profile including Safari 26, all identical
(ports aside).

| Claim | Evidence | Checked by | Notes |
|---|---|---|---|
| A request in CORS mode to another origin whose 3xx fails the CORS check (no `Access-Control-Allow-Origin`; with credentials, no `Access-Control-Allow-Credentials: true`): the next URL is never requested | W (`cors-edge`: `noacao-redirect.cs`, `noacao-redirect-pf.cs`; `redirects2`: `xhr-noacao.cs`, `img-noacao.cs` (`<img crossorigin>`), `script-noacao.cs` (`<script crossorigin>`), `fetch-noacao.ss`, `fetch-noacao.cs-cs`, `cred-noacac.cs-cs`; their `acao`/`acac` controls followed, the cs-cs hop with `Origin: null`) | `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser` (a redirect's `*CORSError`) | Wildcard rule follows the Fetch standard. Other CORS-mode element kinds (styles, fonts, preloads) take the same check unmeasured (one Fetch-standard path in every engine) |
| PUT, DELETE and PATCH answered 301 or 302 keep method, body and `Content-Type`; POST answered 301 or 302, and every method but HEAD answered 303, become a GET without body or `Content-Type`; HEAD stays HEAD; 307 and 308 keep everything (PUT, POST, PATCH; fetch() and form POSTs) | W (`cors-edge` `m-{put,delete,post}-{301,302,303}.so`; `redirects2` `m-put-{307,308}.so`, `m-post-{307,308}.{so,cs}`, `m-head-{301,302,303}.so`, `m-patch-{301..308}.{so,cs}`, `form-{301..308}.{so,cs}`) | `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser` (method, body length, lines); `TestRefusedRequests` (a body without `GetBody`) | |
| `redirects.methodChangeOrigin`: `keep` (Safari 27, 26: the fetch() GET carries the page's `Origin`), `drop` (Chrome, Firefox: none) | W (the GET hops of `cors-edge` `m-*.so` and `redirects2` `m-patch-303.so`) | `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser` | Hand-edited. A navigation's GET (form POST answered 301–303) carries none in every browser (`form-30{1,2,3}.{so,cs}.hop1`) |
| A fetch() chain stops after 20 redirects; `redirects.navigationLimit`: a navigation after 19 (Chrome) or 20 (Firefox, Safari 27, 26) | W (`cors-edge` `chain.so`; `redirects2` `nav-chain.so`, 25 redirects: Chrome requested `.hop19` last, the others `.hop20`) | `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser` | Hand-edited. Chrome's error page reloads `.hop19` a second later (`cache-control: max-age=0`) and follows the rest: browser UI, not reproduced. Firefox's and Safari's pages were finished by the watchdog worker. Frames keep 20 (not measured) |
| `redirects.hopPreflight`: `inherit` (Firefox, Safari 27, 26: the GET a preflighted cross-site PATCH answered 303 became is preflighted, `access-control-request-method: GET`), `own` (Chrome: not) | W (`redirects2` `m-patch-303.cs.hop1`) | `TestRedirects2MatchesBrowser` | Hand-edited. Only measured for a hop to the same cross-site origin |
| Navigation redirects are ordered by variants: Chrome moves `sec-ch-ua*` after `sec-fetch-dest` on every hop and keeps `cache-control: max-age=0` after a form POST; Firefox swaps `referer`/`origin` on a 307/308 POST hop; Safari rebuilds every hop, a typed one (`site: none`) otherwise than a page's, one from `http://` (`insecureBeforeRedirect`) with `upgrade-insecure-requests`, and a hop to another site than the URL before it (`crossSiteRedirect`, navigations only) with `referer` before `sec-fetch-dest` (after it on a hop within a site: apex→www, www→static, a form's within a cross-site origin). Variant conditions `methodChangedByRedirect` (Chrome re-adds Origin after `sec-fetch-*` on a fetch() hop the redirect turned into a GET), `insecureBeforeRedirect` and `crossSiteRedirect` are new. A user's navigation (a clicked link or submit button, a typed URL) is ordered as a script's with `sec-fetch-user: ?1` between `sec-fetch-mode` and `sec-fetch-dest` (Chrome) or after `sec-fetch-site` (Firefox), on its first leg and every hop; Safari sends none and orders a click as a script | W (`redirects2`: form POSTs, `nav-chain.so`; `nav-redirect-{so,ss,cs,http}`: typed navigations redirected within the origin, apex→www, to another site, http→https; `nav-redirect-page`: a page's script and form navigations redirected to another origin; `nav-redirect-form`: form POSTs by script answered 301–308 between every pair of relations; `nav-redirect-click`: the same clicked, and clicked links answered 301/302 from and to every relation (the capture tool's real clicks: DevTools `Input.dispatchMouseEvent`, BiDi `input.performActions`, Safari the system's mouse events); 3 samples each, every clicked request's header names a script's plus `sec-fetch-user`; Safari's first sample of `nav-302.cs-so.hop1` left out, see below) | `TestRedirects2MatchesBrowser`, `TestNavRedirectsMatchBrowser` (a click sent as a script's fails it); kindsgen `TestRegenerateReproducesProfiles` | Derived by kindsgen. A user's navigation in a condition measured only on a script's is `ErrNotMeasured`, and over HTTP/1.1 where that list has no `Sec-Fetch-User` slot (Firefox's form POST: no clicked form was captured over HTTP/1.1). Safari 26's clicks not captured (`missingCaptures`) |
| A redirect hop carrying cookies has the `cookie` slot of its variant: Chrome 156 (macOS, Windows) between `accept-language` and `priority` on every hop (navigations, images, fetch()); Firefox 158 after `referer` (or `origin`) and before `upgrade-insecure-requests` on navigations, before `sec-fetch-dest` on images and fetch(), after `accept-encoding` on a typed navigation's hop; Safari 27, 26 last, after `accept-encoding` | W (the cookie twins of decision 0048, 3 samples each, all alike: `nav-redirect-{so,ss,cs,http,page,form,click}-cookies`, `redirects2-cookies`, `cors-edge-cookies`; Safari 26 without clicks; every twin request's other header names its original's; Chrome 156.0.8078.79 through powhttp on httpbin.org's `/cookies/set` → `/cookies` sent Chrome's order) | `TestNavRedirectsMatchBrowser`, `TestRedirects2MatchesBrowser`, `TestCORSEdgeMatchesBrowser` (a variant without the slot sends the cookies after its list: after `priority` (Chrome), `te` (Firefox), and fails them); kindsgen `TestRegenerateReproducesProfiles` | Derived by kindsgen, each twin's hops merged into its original's conditions. Chrome 155 lacks the slot on these hops: the twins came after it, Chrome 155 is no longer installable as branded Chrome, and it cannot inherit Chrome 156's captures (`missingCaptures`). Safari 26's clicked hops not captured (`missingCaptures`). Hops no twin reaches with cookies keep no slot: cross-site subresources with credentials, hops from `http://`, and Safari 27's image hop to another origin of the page's site (backlog) |
| A navigation hop's `Referer` applies the policy to the `Referer` the request before it sent: once a chain left the page's origin, only the origin, even back on it; `redirects.navigationSite`: after a redirect to another site, `sec-fetch-site` counts the whole chain (`chain`: Chrome, Firefox, Safari 26) or only the URLs from that redirect on (`restart`: Safari 27, `same-origin` on `nav-302.cs-so.hop1`) | W (`nav-redirect-page`: `nav-302.cs-so.hop1`, `nav-301.ss-so.hop1`, every browser) | `TestNavRedirectsMatchBrowser` (setting Safari 27 to `chain` fails it) | `navigationSite` hand-edited, measured on one hop (a chain to another site and back); longer chains follow the rule unmeasured. Each Safari's first (cold) sample sent that hop on a new connection, ordered as a hop within a site (Safari 27: with `cross-site`): browser state, left out by kindsgen (`sampleGaps`) and accepted (logged) by the test |
| `redirects.navigationOrigin`: a form POST's `Origin` kept by a 307/308 becomes `null` once any redirect went to another origin than the URL before it (`cross_origin`: Chrome, Safari 27, 26), or once the chain is tainted as the Fetch standard has it, a redirect from another origin than the page's to a third (`taint`: Firefox, which keeps the page's origin from the page's origin to static.navr-a.test or another site) | W (`nav-redirect-form`, `nav-redirect-click`: `*-30{7,8}.{so-ss,so-cs,ss-so,cs-ss,cs-so}.hop1` `null` or not, `*-30{7,8}.{so,ss,cs}.hop1` the page's origin, every browser, 3 samples) | `TestNavRedirectsMatchBrowser` (setting Chrome to `taint` fails it) | Hand-edited. Measured on one hop; longer chains follow the rule unmeasured |
| Safari sends no `content-length` on a body redirected after a preflight (cross-site PATCH, POST 307/308 hops); its preflight without `access-control-request-headers` starts `sec-fetch-site, origin` | W (`redirects2` `m-patch-*.cs`, `m-post-30{7,8}.cs`) | `TestRedirects2MatchesBrowser` | Plan `NoContentLength` (HTTP/2); preflight variant `pageSet` means "asks for headers" |
| A redirect's request is ordered by how it was made: variant conditions `crossOriginRedirect` (the redirect went to another origin than the request it answered) and `originBeforeRedirect` (that request carried `Origin`). Chrome: an `Origin` the request before carried keeps its place (before `sec-fetch-site`), one the redirect adds goes after `sec-fetch-*`. Firefox: hops order otherwise than first legs (`referer` on the PUT hop, `origin` on the cross-site one) but agree within each condition. Safari 27, 26: a fetch redirected to another origin drops `sec-fetch-*`, `accept-language` and `priority`; a same-origin redirect reorders every header, differently with and without `Origin` | W (every hop of `kinds`, `kinds-http1`, `kinds-plain`, `cookies`, `cors`, `cors-edge`, `redirects2`, `nav-redirect-*`; all samples identical) | `TestCORSEdgeMatchesBrowser`, `TestRedirects2MatchesBrowser`, `TestNavRedirectsMatchBrowser` (every request's lines and weight), `TestKindsMatchBrowserTraces`, `TestCookiesScenarioMatchesBrowser`, `TestCORSMatchesBrowser`; kindsgen `TestRegenerateReproducesProfiles` | Derived by kindsgen from the capture of the request each hop follows. A hop combination not captured is `ErrNotMeasured` (e.g. a no-cors image redirected within another site; [report](../internal/harness/reports/redirects2-2026-10-24.md)) |

## Proxies

`proxy` section ([decision 0014](../docs/decisions/0014-proxies.md)), from
the `proxy-*` captures ([report](../internal/harness/reports/proxies-2026-10-24.md);
WebSockets: [report](../internal/harness/reports/proxy-websocket-2026-10-26.md)).
All checked by `TestProxyMatchesBrowser` (mimicry through the capture proxy
against every browser's capture), `TestWebSocketsMatchBrowser` (its proxy
scenarios) and `TestUnmeasuredProxyIsAnError`, with `Config.BrowserProxyAuth`
(by default mimicry sends the `connectAuth` and `forwardAuth` placements
from the first request, decision 0039: checked in the same tests' upfront
cases). Safari 26's credential rows
are **C**: its `proxy-auth`, `proxy-plain-auth` and `proxy-websocket-auth`
captures were never taken (`missingCaptures` in the test). Safari 27's
`proxy-websocket-auth` was taken on 2026-10-26 (device profile, macOS
answering with remembered credentials).

| Claim (`proxy.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `connect` (CONNECT header names, order, values) | W: Host, Proxy-Connection, User-Agent | W: User-Agent, Proxy-Connection, Connection, Host | W: Host, Proxy-Connection, Connection | W: as Safari 27 |
| `connectAuth` (CONNECT with credentials) | W: `connect` + Proxy-Authorization last (retry and unasked alike) | W: same | W: Host, Connection, Proxy-Authorization, Proxy-Connection | **C** from Safari 27 |
| `connectCredentials` | W: `after_challenge` (preconnects aside, net log) | W: `after_challenge` | W: `on_challenge` (every tunnel) | **C** from Safari 27 |
| `forwardCredentials` | W: `after_challenge` | W: `after_challenge` | W: `always` (fresh Safari; `proxy-websocket-auth` sample 0's first forwarded request was challenged: see the known-gap row below) | **C** from Safari 27 |
| `forwardConnection` (plain HTTP to the proxy) | W: Proxy-Connection | W: none (Connection) | W: none | W: none |
| `forwardAuth` | W: after connection, content-length | W: before connection; last on the challenged retry | W: after accept-encoding | **C** from Safari 27 |
| `socks5` | W: `05 01 00`, host name | W: same | W: same | W: same |
| Tunnelled connection equals the direct one | W | W | W | W |
| `connectNoResume`: no session offered through an HTTP proxy's CONNECT tunnel (SOCKS5 and direct resume). Checked by `TestWebSocketsMatchBrowser` (the socket connections' hellos; setting Firefox to false fails it) | W: false (resumes through both) | W: true (`proxy-auth`: none of 12 connections; `proxy-websocket`: neither socket connection, 3 samples; `proxy-websocket-socks5`: both resume) | n/a (never resumes) | n/a |
| `websocket.plain`: a ws:// socket through an HTTP proxy. Checked by `TestWebSocketsMatchBrowser` (setting Safari 27 to `tunnel` fails it) | W: `tunnel` (CONNECT host:port, `connect` lines) | W: `tunnel` | W: `forward` (`GET ws://host:port/path`, its direct header lines, a proxy connection of its own) | W: `forward` |
| A wss:// socket through a proxy: an extended CONNECT stream on the page's HTTP/2 connection, in its tunnel, where the server allows it, else a tunnel of its own (`connect` lines, SOCKS5 bytes) with the `websocketH1` hello. Checked by `TestWebSocketsMatchBrowser` (`checkProxyRoute`, transport, hello) | W (`proxy-websocket`, `-socks5`) | W | W | W |
| `websocket.credentials`: socket tunnels give credentials as `connectCredentials` says; a ws:// handshake sent whole carries them unasked (needs `forwardCredentials: always`), placed by `forwardAuth`. Checked by `TestWebSocketsMatchBrowser` (setting Safari 27's `connectCredentials` to `after_challenge` fails `px-h1` and `px-h1-again`; its `forwardAuth` after `accept-language` fails `px-plain`; unset, every socket is refused), `TestUnmeasuredProxyIsAnError` | W: unasked after the page's challenge (`proxy-websocket-auth`) | W: same | W: every socket tunnel challenged, then again on its connection with `connectAuth`; `px-h2` in the page's challenged tunnel; ws:// sent whole with Proxy-Authorization unasked after Accept-Encoding (3 samples) | **C** from Safari 27 (`proxy-websocket-auth` never taken; its `proxy-websocket` captures match Safari 27's on every other claim) |
| Proxy-Authorization inside the tunnel, to the server (**known gap**) | W: never | W: never | W, not modelled: on every tunnelled request once Safari has answered a challenge to a forwarded request (`proxy-websocket-auth` samples 1 and 2 from their first request, sample 0 after its forwarded page's 407; sample 0's first forwarded request went without credentials; never in `proxy-auth`, which forwards nothing); mimicry never sends it | not captured |
| ws:// through SOCKS5. Checked by `TestUnmeasuredProxyIsAnError` | not captured: refused (`ErrNotMeasured`) | same | same | same |

## Connections

`connections` section, from the `connections` capture
([report](../internal/harness/reports/connections-2026-10-24.md)), three samples
per browser, Safari 26 included, identical across samples. Checked by
`TestConnectionsMatchBrowser` (every sample) and the unit tests named.
`Config.DisableCoalescing` turns `coalesce` off, each host on its own
connection ([decision 0037](../docs/decisions/0037-coalescing-opt-out.md)),
which `TestConnectionsMatchBrowser` checks against the same samples with
the coalesced hosts on connections of their own. `Config.MaxConnsPerHost`
replaces `http1PerHost` when set.

| Claim (`connections.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 | Checked by |
|---|---|---|---|---|---|
| `http1PerHost`: 6; requests over it wait and go out on connections as they free up, later requests never before earlier ones | W: 10 parallel requests, 6 connections, the first six made one per connection | W: same | W: same | W: same | `TestConnectionsMatchBrowser`, `TestWaitersAreServedInOrder`. First come first served among the waiting requests is the browsers' source, not captured: they leave together |
| `coalesce` | W: `lookup` (no connection to `static` or `y`) | W: `connect` (one to each, SETTINGS, WINDOW_UPDATE, GOAWAY NO_ERROR, no request) | W: `lookup` | W: `lookup` | `TestConnectionsMatchBrowser` |
| The connection taken has a certificate covering the host; being same-site is not enough | W (`solo`: its own connection) | W | W | W | `TestConnectionsMatchBrowser` |
| Same address, same port, same partition, no proxy | not shown (one address, one port, credentialed requests, no proxy); required, as Chrome's and Firefox's source do ([INFERENCE] for Safari) | same | same | same | no test (a conservative choice; not shown by any capture) |
| Later requests to a coalesced host take the adopted connection (no lookup; Firefox: no new connection) | not captured ([INFERENCE], source) | same | same | same | no test (a conservative choice; not shown by any capture) |

## WebSockets

`websocket` section, from the `websocket` (HTTP/1.1 sockets) and
`websocket-h2` (extended CONNECT) captures, three samples per browser and
scenario, Safari 26 included
([report](../internal/harness/reports/websocket-2026-10-24.md); compression:
[websocket-deflate report](../internal/harness/reports/websocket-deflate-2026-10-26.md)).
Handshake headers are `kinds.websocket`, the socket connection's hello
`tls.websocketH1` (rows above). Hand-written; every value holds in all 6
samples of every browser. `compression`, `uncompressed` and
`windowBits8` are checked by `TestDeflateReproducesBrowserMessages`
(`ws.Compressor` replays every message of every deflate socket, the
server's `client_no_context_takeover` and `client_max_window_bits` 8–15
included: RSV1 and bytes must be the browser's) and by
`TestWebSocketsMatchBrowser` (the same messages sent through mimicry).
Level 6 is measured: level 7 fails on the varied text of
`so-deflate-sizes` for every library.

`TestWebSocketsMatchBrowser` (subtests `<profile>/<scenario>/<step>`)
replays every socket step of both captures through mimicry and compares
each sample: the handshake against `kinds.websocket` (HTTP/1.1 lines
verbatim, casing, cookies and `Sec-Fetch-*` included, the key elided;
HTTP/2 fields in order with `:protocol` last, cookie crumbs'
never-indexed flag, HEADERS flags and priority from `kinds.websocket.weight`
and `http2.headersPriority`, the stream WINDOW_UPDATE from
`http2.streamWindowUpdate`), the `insecure` variant (`plain-so`), the
cookie rules (`cookies.Request.WebSocket`, `partitionedFirst`,
`websocketUnpartitioned`) through the Cookie line, the socket
connection's hello against `tls.websocketH1` (`diff.Compare`), and every
claim below: `extensions` on the handshake; RSV1 and compressed bytes
(every message, compressed or not); DATA packing per WebSocket frame
(frames within the cap exactly, Safari's 2 / 4 / payload replies
included; larger ones within the cap, header alone where the browser's
was); the transport's end (who ends it, how, and the delay after the
last client frame within the browser's range plus 250 ms; Firefox:
mimicry never ends it within the capture).

| Claim (`websocket.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `extensions` | W: `permessage-deflate; client_max_window_bits` | W: `permessage-deflate` | W: `permessage-deflate` | W: same |
| `compression` | W: `chromium_zlib6` (Chromium's zlib fork, its 4-byte hash; upstream zlib differs from the first varied message on) | W: `zlib_rs6` (zlib-rs 0.6.3, as vendored in Firefox 158, fed 4096-byte output buffers; source: `WebSocketChannel.cpp` `PMCECompression`) | W: `zlib6` (upstream zlib) | W: same as 27 |
| `uncompressed` | W: `never` (empty → RSV1 `00`; incompressible and tie-length messages compressed, longer) | W: `empty_or_longer` (empty raw; with `client_no_context_takeover`, a message whose compressed form is longer raw, an equal one compressed; with takeover, longer ones compressed) | W: `empty_or_not_shorter` (empty raw, context kept; a message whose compressed form is not shorter raw, equal included, the context then restarting: the last message's bytes need the reset) | W: same as 27 |
| `windowBits8` | W: `as_9` (bytes equal to the 9-bit socket's) | not captured (source): `fail`: Firefox hangs (zlib-rs refuses −8; `HandleExtensions` deadlocks on `mCompressorMutex`, seen as a page that never finishes); the page skips the socket and mimicry fails the dial, a known difference | W: `fail` (onopen, then no frame and RST_STREAM NO_ERROR / TCP closed at once; 1006) | W: same as 27 |
| `h2Data` (DATA cap: `http2.maxDataFrame`) | W: `together` (DATA up to 16375, header and payload in one) | W: `header_alone` (a payload over 1000 bytes, `kCopyBreak` in `WebSocketChannel.h`: its 8- or 14-byte header alone, then the payload in DATA of at most 16000; compressed messages of 1030–8107 bytes show the 1000 rule) | W: `split_control` (DATA up to 16374; the pong and the close reply as 2 / 4 / payload DATA frames, its own close whole) | W: same as 27 |
| `closeH1`, `closeWaitMs` | W: `after_wait`, 2000 (TCP closed 2001–2016 ms after the handshake, the close handshake done by 5 ms; the server waits 3 s) | W: not measured (`""`): no transport end in any of 30 sockets | W: `at_once` (0–5 ms; `plain-so` 14–21 ms, at once after its frames) | W: same |
| `closeH2` | W: `end_stream` (END_STREAM on the close frame's DATA, its own or its reply) | W: not measured (`""`): neither END_STREAM nor RST_STREAM | W: `rst_stream` (RST_STREAM NO_ERROR after the close echo) | W: same |

## QUIC and HTTP/3

`quic` and `http3`, from the `h3-discovery` and `h3-page` captures, three
samples per browser and scenario, unforced and (Chrome, Firefox) with
`-h3-local-roots` ([report](../internal/harness/reports/http3-2026-10-25.md)):
Chrome and Firefox 33 QUIC connections each (21 completed handshakes, 12
full and 9 resumed; the 12 unforced ones refused the capture server's CA and show the first
flight only), Safari 27 and 26 3 each (`h3-discovery` only: Safari opened
no QUIC connection in `h3-page`). Generated, never hand-edited: `h3gen`
derives `quic` and `http3` (but `headers`, `high`, `low`), `kindsgen` those
three, `hellogen` `tls.quic`; each checks its values against every
connection of every sample and refuses a difference not pinned with a
reason (`h3gen`'s `sampleGaps`: Chrome's one Initial ACK and two tokenless
reconnections; `kindsgen`'s `h3Unmatched`: the XHR POST, which no HTTP/2
list covers; `h3PriorityGaps`: Firefox's second-page image). Their
`TestRegenerateReproducesProfiles` keep the files equal to what the
captures give. `TestHTTP3MatchesBrowser` replays `H3DiscoverySteps` and
`H3PageSteps` through mimicry against every sample, holds what each
browser draws per connection (packet numbers, DCID length, CRYPTO cuts,
PINGs, parameter order, GREASE and ECH lengths, ACK delays, spin bit) to
the rule below for mimicry's connections and every captured one, and
`TestQPACKReproducesBrowserSections` re-encodes every captured section.
Mutations, 2026-10-25 (`-overlay`, each fails `TestHTTP3MatchesBrowser`):
no PRIORITY_UPDATE (Chrome), no QUIC dial on Alt-Svc (Firefox), HTTP/3
on Safari's next request, Firefox's HTTP/2 pseudo-header order over
HTTP/3, the SNI cut keeping the head piece whole on a resumed hello,
Safari's request before HANDSHAKE_DONE, a dial on the Alt-Svc that came
with a GOAWAY (Firefox), Chrome's transport parameters unshuffled.

| Claim | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `quic.version`, `datagramSize`, `padding` | W: 1; 1250; `scattered` | W: 1; 1252; `trailing` (zero bytes after the packets; PADDING only in a coalesced 1-RTT packet) | W: 1; 1200; `end` | W: as 27 |
| `quic.crypto` | W: `shuffle`: head 55–86, 2–10 added CRYPTO frames and 2–10 PINGs per padded packet, the first packet holding floor((len-4)/2) bytes, in all 66 hello packets (quiche's chaos protection) | W: `sni_cut` at the host's middle (6 of 13 bytes), tail then head, the longer piece cut to len/n, in all 33 (fresh: 2 packets, middle 948 bytes; resumed: 3, the tail piece as planned and the head piece what fits after it beside the 90-byte token, so a host early in the hello stays whole: cut inside it in 4 of 9) | W: `ordered`, `firstPacket` 999 | W: as 27 |
| `quic.packetNumbers` | W: `shared` from 1; 2-byte second Initial PN iff no token (33 of 33) | W: `random_initial` (first Initial PN 2–32, four of 33 at 497–813: neqo's formula, 1–1024); 1-RTT after 0-RTT | W: `per_space` | W: as 27 |
| `quic.dcidLength`, `dcidLengthRule`, `scidLength`, `newConnectionIds` | W: 8; fixed; 0; 0 | W: 8 with `biased` (23 of 33 at 8, others 10–17, neqo's max(8, 5 + (v & v>>4))); 3; 4 in all 21 completed, retire_prior_to 0, against a server limit of 4 | W: 8; 0; 0 | W: as 27 |
| `quic.transportParameters`, `transportParameterOrder` | W: 12 parameters + GREASE (0–15 bytes) + `initial_rtt` when resumed; `version_information` 1, then {1, GREASE} shuffled; `shuffled` | W: 14, incl. 0x1d empty and `min_ack_delay` 0xff02de1a = 1000; `version_information` 1, GREASE, v2, 1; `fixed` | W: 8, `max_idle_timeout` 300000; `rotated` (rotations of one cycle in 3 samples: a rotation cannot be told from another permutation). Until midday 2026-10-25 Safari 27 also sent 0xff080808 = 09, last (the first h3-discovery capture, 3 of 3); every connection since leaves it out: the edge captures (33), the h3-discovery recapture (3), and fp.impersonate.pro over HTTP/3 on port 443 (`internal/harness/reports/realcheck-2026-10-25.md`). Same Safari and macOS build throughout: an Apple-side switch, not modelled | W: 8, no idle timeout, 0xff080808 = 07 (still sent, also on the internet: realcheck); `rotated` |
| `quic.ackInitial`, `ackBeforeFinished`, `coalesce` | W: false (1 of 12 full handshakes ACKed an Initial: pinned); false; true | W: true; true (full handshakes; resumed ones ACK with the Finished); true | W: true; true; false (no datagram holds two packets) | W: as 27 |
| `quic.ackDelay`, `ackDelayConstant` | W: `actual` | W: `actual` | W: `constant`, raw 27–29 in the h3-discovery recapture (one value per connection; the first capture's three were 19–24, so the range is narrower than the browser's) | W: `constant`, 9–27 |
| `quic.ackOnlyLimit`, `ackOnlyFrame` | W: 19, `MAX_DATA` (every ACK + MAX_DATA packet follows exactly 19 ACK-only packets, no run longer) | W: 0 (runs up to 24, never made ack-eliciting) | W: 0 (no run that long: not measured) | W: as 27 |
| `quic.spinBit` | W: `zero` | W: `random` (varies within connections) | W: `spin` (0, then 1 against quic-go) | W: as 27 |
| `quic.tokens`, `zeroRtt`, `closeReply` | W: true (two reconnections after idle without one: pinned); true (9 of 9 resumed sent 0-RTT); false (no reply to the server's close, 3 of 3) | W: true; true (9 of 9); true (CONNECTION_CLOSE NO_ERROR, frame type 0x1d, 3 of 3) | W: false; not measured (never resumed); not measured (the server never closed) | W: as 27 |
| `http3.settings`, `greaseSetting`, `greaseFrame` | W: 4 settings; a GREASE setting last (N and value 32-bit); GREASE frame (N 32-bit, 0–3 bytes) | W: 6 settings; none; GREASE frame (any N, 0–7 bytes) | W: 2 settings (table 16383); GREASE setting; none | W: as 27 |
| `http3.uniStreams`, `streamsWithFinished`, `earlyRequestFirst` | W: control at the handshake, 6 never written (decoder, [INFERENCE]: the only type left), encoder on the first insert; true; false | W: control, encoder, decoder at the handshake; false; true | W: control only; false; — | W: as 27 |
| `http3.requestsAfterConfirmed` | not measured (no request waited for a full handshake: the first came 300 ms or more after it) | not measured (the first came 47–74 ms after the Finished, from the parser) | W: true: the request waiting for the connection leaves 0.7–2.4 ms after the Finished with the ACK of the server's next 1-RTT packet (pn 3, its HANDSHAKE_DONE), after the control stream (3 of 3); its spin bit 1 | W: as 27 |
| `http3.qpack` | W: `index_all`, inserts first, `shorter` | W: `new_names`, request first, `always` | W: encoder not measured (no table offered); `always` | W: as 27 |
| `http3.priorityUpdate`, `maxDataFrame`, `firstDataFrame` | W: true (102 of 102 prioritised requests); one DATA frame per body | W: false; 32768, first 32224 (64 KiB: 32224 + 32768 + 544, 3 of 3) | W: false; not measured (no body) | W: as 27 |
| `http3.headers` | W: nil (114 requests: HTTP/2 order and pseudo order) | W: pseudo `:method :scheme :authority :path`, `te` dropped, `alt-used` before `referer` (before `cookie` for fetch: cookie never went over HTTP/3, its side is assumed) in 129 requests of 11 kinds and the navigation; kinds not captured over HTTP/3 have no entry (not measured). `alt-used` value `<host>:<port>` (every capture, unprivileged ports), the port left out when it is 443 (code, `altUsedValue`): seen once on the wire through a public echo (fp.impersonate.pro over HTTP/3, `internal/harness/reports/realcheck-2026-10-25.md`), no capture reaches port 443; `TestAltUsedPort` | W: nil (9 fetches) | W: as 27 |
| `http3.high`, `http3.low` | W: low `injected_image` `i` (HTTP/2 has no image-low); every other priority header the kind's HTTP/2 value | W: low `injected_image` `u=6, i`; `markup-image2.so` `u=4, i` pinned (layout-driven) | W: none; `kinds.injected_image.low` `u=7, i` from its `h3-page` HTTP/2 requests | W: as 27 |
| `kinds.xhr` POST variant (`corsChecked`, no `http1`) | K: the `h3-page` XHR POST over HTTP/3 (= its HTTP/2 list), 3 samples; `cookie` slot placed after its predecessor (assumed) | K: over HTTP/3 in HTTP/2 form (`alt-used` out, `te` back last), 3 samples; `*` and `cookie` slots assumed | W: its `h3-page` HTTP/2 requests, 3 samples; `cookie` slot assumed | W: as 27 |
| `http3.discovery` | W (local roots): `next_request`; no TCP GOAWAY; no race (0-RTT QUIC alone after the server's GOAWAY) | W (local roots): `on_alt_svc`; `goawayTcp` (every HTTP/2 connection QUIC replaced, 3 of 3; also one opened while the resumed attempt was pending, h3-broken sample 3; mimicry sends it once the connection's request is answered, whenever the connection was established: `TestGoAwayReachesTCPConnectionEstablishedAfterQUIC`); `raceAfterGoaway`, 6–9 ms, QUIC only after the TCP connection is up, its request sent and its GOAWAY out (1 ms before QUIC's first datagram, h3-broken 3 of 3; TCP first in h3-discovery 3 of 3): mimicry holds that QUIC dial until the request's response on TCP (`TestRaceAfterGoAwayWaitsForTheTCPResponse`) | W: `new_connection` (QUIC only after the server's HTTP/2 GOAWAY; `h3-b` stayed on HTTP/2); race not measured | W: as 27 |

Not measured, documented on `profile.QUIC` and `profile.HTTP3`: PMTU
probing, ECN, DATAGRAM and Retry; Safari's QPACK with a table, request
bodies, PRIORITY_UPDATE and resumption (Safari resumed QUIC in
`h3-broken`, `h3-early` and `h3-too-early`, but the profile has no Safari
resumption and so no `http3.earlyData`: a gap); what
`-h3-local-roots` might change after the handshake; a v2 connection's
NEW_TOKEN reused by a v1 one; broken-alternative durations beyond a page;
credit growth beyond 50 MiB or at lower bandwidth; key updates beyond
37,000 received packets; Chrome's blackhole close ("85:Network blackhole
detected") after its migrated path also went quiet; probes of resumed
attempts (Chrome's first carries four Initials, Firefox's alternate
layouts: the rules hold for fresh attempts).

### Failure, version negotiation, long transfers

`quic.loss`, `keepAlive`, `ack`, `credit`, `keyUpdate`,
`versionNegotiation` and `http3.discovery.fallback`, from the
`h3-blackhole`, `h3-broken`, `h3-vn` and `h3-download` captures
([report](../internal/harness/reports/http3-edge-2026-10-25.md);
`server.H3EdgeCaptures`), three samples each. Generated by `h3gen`
(`edge*.go`), which checks every unanswered attempt, silenced connection,
download and fallback request against the rule and refuses a difference
not pinned in `sampleGaps` (Firefox's sample-3 resumed attempt: no cached
RTT, give-up 9.2 s; Firefox's 254 ms late keep-alive; Safari's irregular
stream credit, six samples; Safari 27's TCP 139/202 ms after a Version
Negotiation). Kept true by `TestRegenerateReproducesProfiles` (h3gen,
hellogen), and on mimicry's side by `TestHTTP3EdgeMatchesBrowser`,
`TestHTTP3DownloadMatchesBrowser` and `TestQUICInterop`. How far a batching browser's ACKs advance on average
is how many packets arrive while it works through the last (ACKs 19–47 µs
apart at the median, no fixed count or interval): machine timing, which no
field holds and the download test logs without checking.

| Claim | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `quic.loss` probes | W: first PTO 300 ms, doubling (+300, +900, +2100 ms, 6 fresh attempts); resumed from the cached RTT, max_ack_delay counted (first probe 30–33 ms); one 1250-B Initial per probe, the flight's packets' CRYPTO in turn, re-cut; one PN skipped before each probe, handshake and 1-RTT | W: 300 ms, doubling up to 15% late (+19.0–19.8 s last); resumed 3 ms (cached RTT, no ack delay; 1 of 3 pinned); both Initials again, CRYPTO as first sent, no skip | not measured: no probe before TCP won | as 27 |
| `quic.loss` give-up | W: fresh and resumed at 4.0 s (+0–4 ms): Initial CONNECTION_CLOSE NO_ERROR, quiche's "25:No recent network activity after {elapsed_us}us. Timeout:4s …", padded to 1250, with a 0-RTT close when 0-RTT was sent (the report's first "no CONNECTION_CLOSE", since corrected) | W: fresh silent until the idle timeout; resumed at 5.0 s: application close (0x1c APPLICATION_ERROR) in a 255-B datagram (1 of 3 at 9.2 s, pinned) | W: abandoned when TCP wins: nothing (fresh), one 0-RTT close (resumed) | as 27 |
| `quic.loss.pathDegrading` | W: migrate 4 unbacked-off PTOs after the request (+115–131 ms): PATH_CHALLENGE padded to 1250 from a new port, RETIRE_CONNECTION_ID, nothing more on the old path | W: none; the connection idles out at 30 s | W: close 12.0 s after the last datagram received (+0–16 ms), reason "keepalive limit reached" | as 27 |
| `quic.keepAlive` | W: 15 s after the last ack-eliciting packet sent (15001 ms) | W: 15 s after the idle timer's restart (first ack-eliciting packet after the last receipt; 1 of 3 254 ms late, pinned) | W: 2 s after the last receipt | as 27 |
| `quic.ack` | W: every 10 packets (97% of ACKs) after 100 at every 2nd | W: every 2, batched (advances 2–7; 5.0–5.7 on average) | W: every 2, batched (mode 3; 4.3–4.6 on average) | W: every 2, batched (6.1–10.8 on average, an older Mac) |
| `quic.credit` | W: stream and connection every half window (6 MiB, 15 MiB; steps within 2% above), windows fixed | W: every quarter (12 MiB, 24 MiB) | W: stream about half the 2 MiB window as its application reads (irregular: pinned); connection = bytes received + 2 MiB (by its ACKs: never more, the median raise within 3.3 KB; a quarter to a third of its raises from older counts, up to 1.7 MB lower, and in other packets than its MAX_STREAM_DATA) | W: as 27, but every raise after the first (two in one sample) rides with a MAX_STREAM_DATA and all but two (that sample) are on the rule; that sample grew the stream window to 3.2 MB |
| `quic.keyUpdate` | W: follows only (none started in 3,793 packets sent, 37,050 received) | W: follows only | W: follows only | W: follows only |
| `quic.versionNegotiation` | W: no retry; no QUIC after | W: retry in v2 1 ms after the VN; close 0x53f8 when the server's parameters lack version_information | W: no retry | W: no retry |
| `http3.discovery.fallback` | W: `race`, TCP 305–306 ms; 0-RTT holds requests 4.0 s, then fresh QUIC + TCP together; broken after 1 fresh failure; pending → TCP | W: `background`; `hold` behind the re-validation after a close; broken after 1 fresh failure; revalidate on a request 797–3020 ms after a failure or death; pending → TCP (a connection per request) | W: `race`, TCP 100–129 ms; abandon on TCP; broken after 2, resumed counted; pending → new QUIC 10–15 ms after the close | W: as 27; TCP 88–109 ms; reconnect 7–9 ms |

### Early data

`http3.earlyData`, from the `h3-early` and `h3-too-early` captures
([report](../internal/harness/reports/h3-early-2026-10-26.md);
`server.H3EdgeCaptures`), three samples each: on each page every request
(fetch POST, PUT, DELETE, an XHR POST, a GET and a POST made together, a
GET, HEAD and OPTIONS fetch, a form POST navigation) is the first on a
connection resumed after the last idled out; the `h3-too-early` server
answers every request whose stream started in a 0-RTT packet with 425.
Generated by `h3gen` (`edge_early.go`) from each request made before its
connection's Finished (24 in 0-RTT and 36 held per browser, 12 answered
425), refusing a method seen both early and held and a held request that
leaves both with and after the Finished unless pinned in `sampleGaps`
(Firefox, 4). Required with `quic.zeroRtt`, and only then (`Validate`).
Kept true by `TestRegenerateReproducesProfiles` (h3gen) and, on
mimicry's side, by `TestHTTP3EdgeMatchesBrowser` (each stream of a 0-RTT
connection: in 0-RTT, held with or after the Finished, sent again after
a 425) and `TestQUICInterop` (`0rtt-too-early`: the retry on the same
connection against quic-go). Mutations, 2026-10-26 (each fails
`TestHTTP3EdgeMatchesBrowser`): every method in 0-RTT (Chrome and
Firefox, both pages); no retry after 425 (both; `0rtt-too-early` too);
no `heldWithFinished` (Firefox, both pages). A request whose body
cannot be read again (no `GetBody`) returns the 425 instead of retrying:
code, not captured (browsers always have the body).

| Claim | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `http3.earlyData.methods` | W: GET, HEAD, OPTIONS in 0-RTT (24 of 24); POST, PUT, DELETE (fetch), XHR POST, form POST held (36 of 36), the POST made with a GET held while the GET went early | W: as Chrome (24, 36); the connection dialled for a held request sends its control and QPACK streams in 0-RTT without it | not in the profile (no resumption): the same rule (24, 36), held until the handshake is confirmed, 0.8–2.3 ms after the Finished | as 27, 0.5–2.1 ms |
| `http3.earlyData.heldWithFinished` | W: false: own datagram, 0.05–2.1 ms after the Finished (36 of 36) | W: true: in the Finished's datagram, 31 of the 35 made before it; 4 left 0.2–18.6 ms later (necko against neqo timing: pinned) | — | — |
| `http3.earlyData.retryTooEarly` | W: true: every 425 sent again on the same connection, a new stream in 1-RTT, 0.4–0.9 ms after the Finished (12 of 12); never on a new connection | W: true (12 of 12, 0.2–2.2 ms after the Finished); beside 3–4 of them per sample a TCP connection opened and declined (pinned in `h3EdgeGaps`) | not in the profile: true (12 of 12, 3.3–4.3 ms after the Finished) | as 27 (2.1–3.2 ms) |

## Behaviour in code, not in profiles

| Behaviour | Evidence | Checked by |
|---|---|---|
| A fully read response is not reset (no RST_STREAM; waits for END_STREAM up to 10 s) | W: no browser resets streams it read | `TestClosingFullyReadBodyDoesNotReset` |
| Uploads read in 65535-byte chunks | W for the first 64 KiB of Chrome and Safari uploads; later chunks and all of Firefox's are not modelled | `TestConnectionMatchesBrowserTrace` (largest frame only) |
| HTTP/1.1-only servers get parallel connections at once; HTTP/2 candidates wait for the first | W (`resume`: three parallel requests, three connections opened together) | `TestResumptionMatchesBrowser` |
| END_STREAM on an upload's last DATA frame (body of known length), no empty frame after | W (the capture page's upload, every profile, every sample) | `TestDataFrameCap` |
| `Config.Headers`' Authorization and Cookie are not sent once a redirect chain has left an origin | The Fetch standard (Authorization on a cross-origin redirect); not captured | `TestConfigCredentialsStayBehindCrossOriginRedirects` |
| A CORS preflight carries only the `Config.Headers` the preflight kind's lists name (a User-Agent or Accept-Language override), never others such as an API key | Follows the request it precedes; not captured (browsers have no `Config.Headers`) | no test |
| HelloRetryRequest cookie placed at a random non-pinned position (uTLS fork patch 2) | **not Chrome's mechanism**: Chrome places it by its per-handshake permutation | known gap |
| WebSocket messages go as one frame each | W for Firefox and Safari (every message); Chrome sends the 71680-byte text whole in 3 of 6 samples, split 65464 + 6216 in the others: mimicry always sends the whole form | `TestWebSocketsMatchBrowser` |
| `ws://` from an `https://` page is refused before connecting | W: Chrome throws `SecurityError`; Firefox and Safari fail and no handshake reaches the server | `TestWebSocketsMatchBrowser` (`mixed`, every profile, both scenarios: the dial errors and no handshake reached the server) |
| Firefox's socket transport: left to the server, ended by mimicry 10 s after the close handshake (HTTP/1.1: TCP close; HTTP/2: RST_STREAM NO_ERROR); a stream the server ended is forgotten without a frame | Firefox's own end is not captured: an unmeasured fallback so a socket cannot leak | no test (known gap) |
| Chrome over HTTP/2: `Close` returns once the server has ended the socket's stream (mimicry's END_STREAM went with its close frame), so a later request's chained priority never depends on it; a stream the server does not end is reset after 10 s (Chromium waits 2 s, then drops the stream) | Chromium's `WebSocketChannel` fires its close event only after the stream closed ([INFERENCE], from its source); every captured socket handshake, each opened after the one before had closed, depends on stream 0 (`websocket-h2`, `proxy-websocket*`); the 10 s fallback is not captured | `TestWebSocketsMatchBrowser` (each socket's handshake priority); the fallback no test (known gap) |
| A close handshake the server never answers ends the transport after 10 s | not captured | no test (known gap) |
| Firefox's HTTP/2 segmenting that counts a frame header into its 16000 bytes (14 + 15986) | W, not modelled: only the cap and header-alone are | known gap |
| `ErrNotMeasured`: an empty message on a compressing socket; a server answering `client_max_window_bits` under 15 or `client_no_context_takeover` (on sending); WebSockets through a proxy without `proxy.websocket`, with proxy credentials without `proxy.websocket.credentials`, ws:// through SOCKS5 | not captured | `TestWebSocketsMatchBrowser` (Firefox deflate); `TestUnmeasuredProxyIsAnError` (the proxy cases); the rest no test |
| A cross-site socket over HTTP/2 may share the cross-site host's own first-party connection (no top-level-site partition) | W: browsers use only the page's partition's connection (the one that carried `b-set-3p`) | known gap |
| Read limits: 64 MiB per message, 64 queued messages | safety bounds, not captured | `TestReadingServerFrames` (server frames, hostile input) |

## Not covered by any claim

What a profile does not describe, so mimicry does not imitate it. Each
item, with when it shows, its detection relevance and what closing it
takes, is in the [backlog](../docs/backlog.md), which also notes the
bullets below that later work has closed:

- Headers: a same-site form POST
  navigation (not captured: an error); where Cookie goes on a non-secure
  origin (no browser sent one on the `kinds-plain` page); `FontFace`
  loads; `crossorigin` stylesheets.
- CORS: preflight Max-Age caps (honoured as sent), partitioning of the
  preflight cache by top-level site, and preflights from cross-site frames.
- Cookies: total limits, and Chrome's priority and `Secure`-aware eviction
  within the per-site limit; adjusting `Expires` for the server's `Date`;
  whether Chrome refuses a nameless value starting with `__Http-`;
  loopback hosts other than `localhost`; Safari's `__Host-` with a default
  path other than `/`; the order between Safari's `Domain` cookies of
  different levels.
- Connections: Firefox's connections for credential-less requests (their
  own to the same-site host, shared to the page's own and the cross-site
  host, in every sample; no rule found); connections for what a cross-site
  frame itself loads; Chrome's preconnects (one more handshake to the
  page's host, no request, on the connections page as on the kinds pages);
  whether the per-host cap holds over plain HTTP and per proxy route too
  (measured over HTTPS; mimicry applies it to both: [INFERENCE]); Chrome's
  total and per-proxy limits; coalescing through a proxy (mimicry never
  does); the 90 s after which an idle connection is closed (HTTP/1.1,
  HTTP/2, and HTTP/3 where QUIC's own idle timeout is longer), and how
  Chrome and Safari close an idle HTTP/2 connection (mimicry: the close
  alone; Firefox profiles GOAWAY first, as its `goawayTcp`); the TLS 1.2
  ticket lifetime.
- HTTP/2: Chrome's mid-load re-prioritisation of a script (and so how it
  moves the stream in its dependency lists); a stream whose response ended
  mid-upload, which Chromium keeps in its lists until the upload's
  END_STREAM ([INFERENCE], from its source); an upload
  of unknown length ends with an empty END_STREAM DATA frame; a
  REFUSED_STREAM makes mimicry stop using the connection (Chrome retries
  on the same session: [INFERENCE], from its source); on errors, RST_STREAM
  before the credit for discarded bytes, and GOAWAY before closing
  ([INFERENCE], from Chromium's source; never captured).
- TLS and below: ECH payloads; the TCP/IP fingerprint.
- WebSockets: Firefox's compressor and its transport ending; Safari's
  compression threshold between 6 and 4095 bytes; a `Strict` cookie on
  Safari 26's cross-site socket; protocols on a `ws://` socket from an
  `http://` page; a full-handshake socket hello for Chrome and Firefox;
  DATA frames cut short by flow-control windows (they vary between
  samples); what Firefox
  and Safari do with `ws://` from an `https://` page beyond failing
  (whether they try `wss://` on the plain port is only in the server's
  log).
- Proxies: Chrome's speculative preconnects to a proxy; `https://`
  proxies; Safari 26's credential flow, WebSockets included (never
  captured, copied from Safari 27); Safari's `Proxy-Authorization` inside
  tunnels once its credential state is on (see the Proxies table); and
  `ws://` through SOCKS5 for any browser (refused, `ErrNotMeasured`).
- By decision ([0016](../docs/decisions/0016-literal-urls-no-https-upgrade.md)):
  Safari's HTTPS upgrade attempt (TLS on the same port) before a
  plain-HTTP request, Chrome's HTTPS-Upgrades on port 80, and HSTS:
  requests go to the URL given.

## What checks each behaviour

Per concept page, the tests and captures behind it. Facts already in a row
above are linked, not repeated.

### Cookies

The edge-case rows (`knoeq`, `=x`, `__Http-` and `__Host-Http-`) are the
`nameless`, `emptyName` and `httpPrefix` rows under [Cookies](#cookies)
([decision 0012](../docs/decisions/0012-cookie-rules-in-profiles.md)).
What the jar does not model is listed in
[Not covered by any claim](#not-covered-by-any-claim). Safari's store-or-send
blocking and the end of Chrome's two-minute window are in the `thirdParty`
and `laxAllowsUnsafeSeconds` rows.

### CORS preflights

Rows: [CORS preflights](#cors-preflights) and [Redirects](#redirects).

- `TestKindsMatchBrowserTraces` compares every preflight of the kinds page
  with the browser's: lists, values and weight over HTTP/2, HTTP/1.1 and
  plain HTTP, and that mimicry sends none where the browser did not.
- `TestCORSMatchesBrowser` replays the `cors` page. On each connection to
  the cross-site host, the same requests and preflights must arrive in the
  same order as the browser's. The refused steps must return a
  `*CORSError`.
- `TestCORSEdgeMatchesBrowser` replays the `cors-edge` page the same way,
  on every connection: a cross-site redirect without
  `Access-Control-Allow-Origin` (no next request), PUT, DELETE and POST
  answered 301, 302 and 303 (method, body length, `Origin`), and a chain
  of 25 redirects (the 21st URL never requested).
- `TestRedirects2MatchesBrowser` and `TestNavRedirectsMatchBrowser` replay
  the `redirects2` and `nav-redirect-*` pages: redirects of every kind and
  the navigation chain's limit.
- Unit tests in `preflight_test.go` cover when a preflight is sent (the
  safelist, and which `Config.Headers` a preflight carries), the expiry
  with a fake clock, and the refusal rules. The cache's pruning
  (`internal/cors`) has no test of its own. Of the redirect rules, only
  the credentials' (`TestConfigCredentialsStayBehindCrossOriginRedirects`)
  is tested outside the e2e replays above.

### How a request is built

In `internal/harness/e2e`: `TestProfilesMatchCaptures` (first request),
`TestHTTP1MatchesBrowserTrace`, `TestCookiesMatchBrowserTrace`,
`TestKindsMatchBrowserTraces` (every request of the kinds page and its
preflights, redirects, connection sharing),
`TestCookiesScenarioMatchesBrowser` (the cookies page),
`TestCORSMatchesBrowser` (the preflight cache and refusals),
`TestConnectionsMatchBrowser` (the per-host HTTP/1.1 cap and coalescing,
with `Config.DisableCoalescing` and without) and
`TestHPACKReproducesBrowserBlocks`.

The page replays of the kinds, headers-order, cors, cors-edge, redirects2,
nav-redirect, cookies, cookies-edge and h3 pages also plan every request
just before sending it and check the plan against what the server
received (`checkPlans`).

Outside the e2e suite: the resolver's rules by unit tests in the root's
`request_test.go`, `mimicry_test.go` and `preflight_test.go`; the pool's by
`internal/pool`'s, with fake connections and a fake clock;
`Config.MaxConnsPerHost` by `internal/engine/native`'s `TestMaxConnsPerHost`,
with fake servers. Header placement by `TestHeaderPlacement`
(`placement_test.go`): each placed request against the same one unplaced,
over HTTP/1.1 and a raw HTTP/2 peer, and its error paths. Over HTTP/3 by
`TestHeaderPlacementOverHTTP3` in `internal/engine/native`, against a QUIC
server decoding the field sections.

### HTTP/3

Rows: [QUIC and HTTP/3](#quic-and-http3),
[Failure, version negotiation, long transfers](#failure-version-negotiation-long-transfers)
and [Early data](#early-data). Each per-browser value of the QUIC
connection (first flight, packet numbers, transport parameters, ACKs,
credit, key updates, versions, 0-RTT) is a row there.

**Where the profile data comes from.** The h3 captures
(`internal/harness/testdata/captures/<name>.h3-{discovery,page}*`) feed
three generators, run in this order: `hellogen` (`tls.quic`), `h3gen`
(`quic`, and `http3` but its header lists and priorities), `kindsgen`
(`http3.headers`, `http3.high`, `http3.low`, and the kinds the h3 pages
add). `hellogen` and `h3gen` also read the `h3-blackhole`, `h3-broken`,
`h3-vn`, `h3-download`, `h3-early` and `h3-too-early` captures
(`server.H3EdgeCaptures`): `hellogen` for `tls.quic.versionNegotiation`,
`h3gen` for `quic.loss`, `quic.keepAlive`, `quic.ack`, `quic.credit`,
`quic.keyUpdate`, `quic.versionNegotiation`, `http3.discovery.fallback` and
`http3.earlyData`. Each refuses a sample its rule does not describe.

**Tests.**

- `TestHTTP3MatchesBrowser` walks both h3 pages through mimicry for every
  profile. It compares each request's transport, each QUIC connection and
  its HTTP/3 layer with every browser sample. What the browser draws per
  connection is held to the profile's rule, for mimicry's connections and
  every browser connection alike. So the test is deterministic, and a rule
  the captures contradict fails.
- `TestHTTP3EdgeMatchesBrowser` (`internal/harness/e2e/http3_edge_test.go`)
  walks the `h3-blackhole`, `h3-broken`, `h3-vn`, (with aioquic) `h3-v2`,
  `h3-early` and `h3-too-early` pages through mimicry on the browser's
  schedule. Per request it compares every connection it went on and how
  long it was held. On a connection that sent 0-RTT it compares each
  stream (in 0-RTT, held with or after the Finished, or sent again after a
  425). Per QUIC attempt it compares versions, the server's answer,
  datagrams and give-up, and when the attempt and each TCP connection
  started after the request that caused them (the race), within the
  samples' range. A migration must send the browser's probes first and
  come 4 of the connection's own PTOs after the request, for mimicry and
  every browser sample, so it holds on a loaded machine too. Each page runs
  with `Config.BrowserQUICFallback` and with the fast default. The
  default's differences are listed one by one (`h3EdgeFastGaps`), all on
  pages where QUIC fails, and each must appear.
- `TestHTTP3DownloadMatchesBrowser` replays `h3-download`. It holds
  mimicry's acknowledgements and key phases to the profile's rules, which
  every sample must follow too, and its credit increases to the range of
  the browser's. Where the connection limit follows what arrived (Safari),
  how far it moves is timing, so mimicry and every sample are held to the
  rule instead (the limit against what the client's ACKs acknowledged,
  plus the window).
- `TestQPACKReproducesBrowserSections` re-encodes every captured field
  list and must reproduce the browser's bytes.
- `TestQUICInterop` (`internal/harness/e2e/quic_interop_test.go`) runs
  every profile against quic-go's HTTP/3 server through a seeded relay
  (`internal/harness/internal/udprelay`: loss, reordering, duplication,
  corruption, delay, blackholes, a small MTU, NAT rebinding). It covers the
  handshake, large and concurrent transfers, Retry, resumption and 0-RTT
  (accepted, rejected, and answered 425 Too Early), ChaCha20, key updates,
  the anti-amplification limit, GOAWAY, idle and server closes, stream
  resets, tiny flow control windows, and a new Transport sharing or
  loading an `AltSvcCache` (its first request on QUIC for Chrome and
  Safari, with no TCP connection). It also covers HTTP/3 through SOCKS5
  proxies (`socks5*` rows: no datagram around the proxy, the same first
  flight as directly, a dropped or refused association). Each case is
  checked byte for byte and against a time limit.

**Measured detail behind the rows.**

- 0-RTT methods (`http3.earlyData.methods`: GET, HEAD, OPTIONS): a request
  with another method made while the handshake runs waits and leaves in
  1-RTT. Firefox sends it in the datagram with its Finished
  (`heldWithFinished`; 31 of 35, the rest a thread's timing). Chrome sends
  it in a datagram of its own 0.05–2.1 ms after. A `425 Too Early` is
  retried on the same connection, new stream, in 1-RTT after the Finished
  (`retryTooEarly`; 12 of 12 per browser), never on a new one.
- Long transfers (`h3-download`, 50 MiB): Firefox's batched ACKs cover 2–7
  packets, Chrome's exactly 10 in 97% of ACKs. Safari raises stream credit
  as its application reads (62 B to 2.1 MB apart, median about 1 MB), its
  connection limit first at 15.2–15.9 MB. No browser started a key update
  in 3,476–8,619 packets sent and 37,000 received.
- After a GOAWAY, Firefox races (`raceAfterGoaway`): the request goes on a
  new TCP connection, and a 0-RTT QUIC connection follows 6–9 ms later,
  carrying only its control and QPACK streams. In every sample the TCP
  request and Firefox's GOAWAY on it (3–4 ms after the request) were out
  before QUIC's first datagram, 1 ms after the GOAWAY. So mimicry starts
  that QUIC connection no earlier than the request's response on TCP.
  Firefox's restart with a saved validated alternative was not captured.

**Fallback** (`http3.discovery.fallback`), measured against servers that
answer nothing, go silent mid-request, close and then answer nothing, or
speak only QUIC v2
([report](../internal/harness/reports/http3-edge-2026-10-25.md); captures
`h3-blackhole`, `h3-broken`, `h3-vn`). This is what mimicry does with
`Config.BrowserQUICFallback`; by default it shortens the holds.

- **Chrome (`race`):** a request that needs a QUIC connection waits for
  the attempt 300 ms (`tcpDelayMs`; 305–306 measured), then goes on TCP
  while the attempt runs on. A request sent in 0-RTT, and every request
  after it, waits for that attempt with no TCP until it gives up
  (`zeroRttHolds`: 4.0 s). Then a fresh attempt and TCP start together,
  and requests made while it runs go on TCP at once.
- **Firefox (`background`):** an attempt never holds a request, except
  those made while its resumed attempt after the server's close is pending
  (`hold`). They go on it in 0-RTT, and to a new TCP connection when it
  gives up (5 s). After a failed resumed attempt or a dead connection, a
  request at least `revalidateMs` later validates afresh beside TCP.
  `revalidateMs` (797–3,020 ms) is bounded by the captures: a request 0.8 s
  after a failure did not start a fresh attempt, one 3.05 s after did.
- **Safari (`race`):** QUIC first, TCP after 100 ms (`tcpDelayMs`,
  88–129 ms measured: Safari 26 88–109, Safari 27 100–129). The attempt is
  dropped once the request goes on TCP (`abandonOnTcp`).

Failed attempts in a row (`brokenAfter`: Chrome and Firefox 1, Safari 2,
resumed ones counting for Safari only) leave the origin on TCP for the rest
of the Transport's life: no browser tried again within a page (Chrome's
5-minute back-off is from its source, not measured). An attempt counts
once its first datagram has gone out
([decision 0028](../docs/decisions/0028-quic-failure-model.md)). Firefox
answers Version Negotiation with a new connection in v2 at once, and closes
it with 0x53f8 when the server's transport parameters lack
`version_information`; that counts as one failed attempt. Chrome's source
delays its TCP job by 1.5 × the origin's smoothed RTT when it expects QUIC
to work; no loopback capture can tell that from no delay, so it is not
modelled.

A live connection whose path stops answering: Chrome migrates
(`quic.loss.pathDegrading`, row above; a socket from
`quic.DialConfig.NewPath`). When that path dies too, Chrome closes with
`85:Network blackhole detected` and sends the stuck request on TCP (not
modelled). Safari closes after 12 s and sends the pending requests again on
a new connection 7–15 ms later (`pending` quic, `reconnectMs`: Safari 26
7–9, Safari 27 10–15). Firefox keeps probing until its 30 s idle timeout,
then sends them all on TCP (`pending` tcp), opening a TCP connection for
each and declining all but the first.

### Proxies

Rows: [Proxies](#proxies). Idle tunnels per rotated identity are a
performance setting, not a profile claim; their memory cost is measured in
the [proxyidle report](../internal/bench/reports/focus-2026-10-29/ctor/after/proxyidle-2026-10-29.md).

### WebSockets

Rows: [WebSockets](#websockets), the proxy rows under [Proxies](#proxies)
(`connectNoResume`, `websocket.plain`, `websocket.credentials`) and the
WebSocket rows under
[Behaviour in code, not in profiles](#behaviour-in-code-not-in-profiles).
Capture reports: [websocket](../internal/harness/reports/websocket-2026-10-24.md),
[proxy-websocket](../internal/harness/reports/proxy-websocket-2026-10-26.md),
[websocket-deflate](../internal/harness/reports/websocket-deflate-2026-10-26.md)
(Firefox hangs on `client_max_window_bits=8`: the `windowBits8` row).

`TestWebSocketsMatchBrowser` replays every socket of both capture pages,
and of the three proxy pages through the capture proxy, and compares them
with every browser sample: transport and TLS hello; what reached the proxy
(CONNECT, SOCKS5, or the handshake sent whole); handshake lines or fields;
every frame, including the compressed bytes; pongs and HTTP/2 DATA
packing; how the transport ends. Those proxy pages run with
`Config.BrowserProxyAuth`. The page with proxy credentials runs again
without it (`/upfront`): the sockets must still be the browser's, every
CONNECT must carry the credentials, and none may be answered 407
([decision 0039](../docs/decisions/0039-proxy-credentials-up-front.md)).

`TestDeflateReproducesBrowserMessages` replays every captured message
through the profile's compressor and names the first byte that differs.

Not in a row: Safari 26 also sends a cross-site host's unpartitioned Lax
and None cookies on sockets. Safari's `ws://` handshake sent whole through
a proxy carries credentials after `Accept-Encoding`, as its other
plain-HTTP requests do; Safari 26 copies Safari 27 there (not captured).

### Refreshing profiles

`TestDeriveReproducesProfiles` (in `cmd/refresh`) derives every built-in
profile from its own captures and requires it unchanged. The last quick
refresh is the
[Chrome 156 report](../internal/harness/reports/refresh-chrome-156-macos-2026-10-26.md);
the first full refresh on Windows the
[Chrome 156 on Windows report](../internal/harness/reports/refresh-chrome-156-windows-2026-10-29.md).

## Keeping it true

Change a profile, change its row. A new browser version starts as a copy of
the previous one ([decision 0004](../docs/decisions/0004-complete-profile-files.md);
`cmd/refresh` does the copy): mark what it copied, everything a capture
cannot show (`http2.hpack`, header slots, `cookies`, `http1` slots), **C**
until its own captures pass the tests above.
