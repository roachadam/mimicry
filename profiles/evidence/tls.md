# TLS ClientHello

## How the claims are checked

- **Field-by-field diff.** All rows are checked by `TestProfilesMatchCaptures`.
  - It diffs mimicry's ClientHello against the capture, field by field.
  - Extension order is compared as the capture says.
  - The same e2e diff checks every built-in profile's record-layer version, legacy_version, session ID length, key share lengths and GREASE ECH shape.
- **Refused profiles.** `profile.Validate` refuses a profile whose bodies differ from the ones uTLS sends for:
  - GREASE, extended_master_secret, signed_certificate_timestamp, status_request, renegotiation_info.
- It also refuses a shape uTLS cannot send:
  - legacy_version other than 0x0303;
  - a session ID other than 32 bytes;
  - a key length not its group's;
  - an ECH key other than X25519's.
- So a profile cannot claim bytes the handshake would not send.

## Where the shape comes from

- **Generated, never hand-edited.** `hellogen` generates the ClientHello's shape into the profile and the capture.
  - Fields: `recordVersion`, `legacyVersion`, `sessionIdLength`, `keyShareLengths`, `x25519Reuse`, `ech`, `resumption.echPayloadLengths`.
  - Source: `<name>.clienthello`.
  - For the GREASE ECH sets, also every connection of every page capture that recorded its hello's shape.
  - It checks `x25519Reuse` against every page and QUIC hello that kept its bytes.
  - Test: `TestRegenerateReproducesProfiles` in `internal/harness/cmd/hellogen`.
  - It also generates `http2.openingRecords` and `http2.goawayReply` (in the HTTP/2 evidence).
- **`<name>.clienthello`** holds one ClientHello per line.
  - Lines 1–3: the 2026-10-24 retakes (3 browser launches, TLS records starting 0x16).
  - Safari 26's retakes are from the second Mac, on macOS 26.
  - Older lines are bare handshake messages (starting 0x01).
  - They are the previous committed capture (`3b1c36f`; Safari 26: `f403006`).
  - For Chrome 155, Firefox 158 and Safari 27, also earlier captures restored from git history (`053c80c`, `7958315`, `8f56566`, then `2efb66b` or `965674c`).
- **Sample size.** The full-hello shape rests on 8 hellos per browser (4 for Safari 26).
  - Plus every page-capture connection with a recorded shape.
  - Chrome: 118 full and 21 TLS 1.3 resumed hellos.
  - Firefox: 111 and 21.
  - Each Safari: 120 full.
  - The default scenario kept no other raw hellos, so none is **W+**.
- **Parser test.** `TestParseClientHelloSeesEveryShapeChange` changes each recorded value in every browser's real hello.
  - It requires a difference or a refusal.

## Short claims

| Claim (`tls.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `cipherSuites`, `compressionMethods` | W+ | W+ | W+ | W+ |
| `extensionOrder` | W: shuffled [1] | W: fixed | W: fixed | W: fixed, as Safari 27 [2] |
| `extensions` [3] | W+ [4] | W+ | W+ | W+ |
| `legacyVersion`, `sessionIdLength` [5] | W (8 + 139): 0x0303, 32 | W (8 + 132): 0x0303, 32 | W (8 + 120): 0x0303, 32 | W (4 + 120): 0x0303, 32 |
| `extensions[key_share].keyShareLengths` [6] | W (8 + 139) [7] | W (8 + 132) [8] | W (8 + 120): as Chrome | W (4 + 120): as Chrome |
| padding (0x0015), total length [9] | none [10] | none | none | none |
| `recordVersion` [11] | W (3 + 139): 0x0301 | W (3 + 132): 0x0301 | W (3 + 120): 0x0301 | W (3 + 120): 0x0301 |
| random values [12] | generated per connection | | | |

Notes:

1. Chrome's order: 8 connections, 8 orders, GREASE pinned first and last.
   - Shuffle uniformity: `TestShuffleIsUniform`. GREASE pinned: `TestProfilesMatchCaptures`.
2. Safari 26's order is identical to Safari 27's. Two installs on two macOS versions agree, so Safari's order is not per install.
3. `extensions` covers types and contents: groups, key shares, signature algorithms, versions, ALPN, ALPS, point formats, PSK modes, certificate compression, record size limit, and raw data such as Chrome's trust anchors 0xca34.
4. Chrome's trust anchor list was identical in all samples.
5. Counts are hellos + page connections (Chrome: 8 hellos + 139 page connections).
   - The session ID's bytes are random per connection.
   - Resumed hellos (TLS 1.3 and 1.2) are alike.
6. A group fixes its key's length; only GREASE's is a choice. Resumed hellos are alike.
7. Chrome key shares: GREASE 1, X25519MLKEM768 1216, x25519 32.
8. Firefox key shares: X25519MLKEM768 1216, x25519 32, secp256r1 65.
9. Padding is 0x0015. No padding extension in any hello.
   - Totals: Chrome 1987 or 1955 bytes (the ECH payload choice), Firefox 1893.
   - Both with a 25-byte server_name body.
   - Safari 1527, with a 14-byte one: the capture host names differ.
   - Totals are implied by the fields above, not stored.
   - The capture parser refuses padding that is not zeros or not BoringSSL's length, the only rule a profile can describe.
10. No padding extension in any hello.
11. Counts are `.clienthello` records + page connections, all 3 samples of every page capture.
    - The 2026-10-24 retakes keep the records.
    - The older `.clienthello` lines (bare messages) carry none and are left out of the check (`capture.Combine`).
    - mimicry sends 0x0301: uTLS writes TLS 1.0 records before a version is negotiated.
    - `handshake.Spec` refuses anything else.
    - The e2e diff of mimicry's hello, `TestProfilesMatchCaptures`, checks it.
    - `diff.Compare` compares it where both sides kept it.
12. Random values: key share material, random, session ID bytes, ECH config_id, key and payload bytes. Values, not claims.

## Long claims

Each claim below gives the same labels in this order: **Chrome**, **Firefox**, **Safari 27**, **Safari 26**, **Checks**. Browser versions are Chrome 155, Firefox 158, Safari 27 and Safari 26 unless a label names another.

### `x25519Reuse`

One X25519 key in both key shares: X25519MLKEM768's last 32 bytes are the x25519 share's, in every hello carrying both.

- **Chrome:** W (8 `.clienthello`): no, a separate key in all 8.
  - Chrome 156: 3 of 3.
  - Chrome 156 `resume` 2026-10-28: 12 full and 13 resumed TCP hellos, none.
  - Chrome 156 QUIC hellos: 24 full and 9 resumed, none.
- **Firefox:** W (8): yes, the same key in all 8.
  - `resume` (2026-10-28, 2 samples): 10 full and 14 resumed TCP hellos, all 24.
  - QUIC (the h3 captures, which keep each hello's bytes): 54 full and 69 resumed, all 123.
  - The 3 `h3-vn` retries send x25519 alone.
- **Safari 27:** W (8): no.
  - `resume` (2026-10-28, 2 samples): 24 full TCP hellos, none.
  - QUIC: 33 full and 60 resumed, none.
- **Safari 26:** W (4): no.
  - `resume` (2026-10-28): 24 full TCP hellos, none.
  - QUIC: 33 and 60, none.
- **Checks:** every hello: full and resumed, TCP and QUIC.
  - Page captures keep each TCP hello's bytes since 2026-10-28 (`clientHello`, hex, beside the parsed `hello`). QUIC captures always did.
  - No key repeats across connections in any capture (150 Firefox hellos, 117 Safari 26, 117 Safari 27, 58 Chrome 156): keys are fresh per connection.
  - Derived by `hellogen` from the full hellos (`capture.Combine` refuses samples that disagree).
  - `hellogen` then refuses any page or QUIC hello with both shares that disagrees.
  - Checked from the hellos' bytes by `TestProfilesMatchCaptures` (full hellos), `TestResumptionMatchesBrowser` (resumed TCP) and `TestHTTP3MatchesBrowser` (QUIC). `diff.Compare` leaves it out.
  - uTLS sends the hybrid's X25519 key as the x25519 share (`ReuseHybridAndClassicalKeyShares`).
  - That saves one X25519 keygen per hello (about 29 µs and 5 allocations on an Apple M3 Pro).

### GREASE ECH (0xfe0d) `ech`

- **Chrome:** W (8 `.clienthello` + 118 full page connections).
  - One suite in all: KDF 0x0001 (HKDF-SHA256) and AEAD 0x0001 (AES-128-GCM).
  - `encLength` 32.
  - `payloadLengths` {144, 176, 208, 240}: one per connection, BoringSSL's four lengths.
  - On page connections: 32, 34, 27, 25 times.
- **Firefox:** W (8 + 111 full).
  - `cipherSuites` {0x0001/0x0001, 0x0001/0x0003 (ChaCha20Poly1305)}: one per connection.
  - On page connections: 56 and 55 times.
  - `encLength` 32.
  - `payloadLengths` 240 in every full hello, TLS 1.2 resumptions included.
- **Safari 27:** —
- **Safari 26:** —
- **Checks:**
  - Type outer in every hello.
  - config_id is a random byte per connection (not stored).
  - The sets used to come from the `.clienthello` samples only (Chrome {176, 208}). Every page connection now feeds them (`hellogen`).
  - mimicry picks the suite and payload length per connection from the profile's sets (uTLS `GREASEEncryptedClientHelloExtension`).
  - `diff.Compare` requires each connection's to be among the browser's.

### `resumption`

Over TCP. TLS 1.3: full hello minus `drop`, `pre_shared_key` last, single-use tickets, no 0-RTT. `tls12`: ticket in `session_ticket`, used as the policy says. Over QUIC `quic.zeroRtt` decides (decision 0050).

- **Chrome:** W: drops nothing; `tls12: reuse`.
- **Firefox:** W: drops `session_ticket`; `tls12: single_use`.
- **Safari 27:** W: none. It never resumes over TCP, TLS 1.3 or 1.2. It resumes over QUIC: `quic.zeroRtt`.
- **Safari 26:** W: none, as Safari 27.
- **Checks:**
  - `resume`, `resume-tls12` scenarios ([report](../../internal/harness/reports/resume-2026-10-24.md)).
  - `TestResumptionMatchesBrowser`: used connections and resumptions per host, every resumed hello diffed.
  - A TLS 1.3 ticket past its lifetime or its certificate's expiry is not offered, so the hello is a full one.
  - Test: `TestTicketPastItsLifetimeIsNotOffered` (certificate expiry: no test).
  - A TLS 1.2 ticket lasts until its certificate expires (its lifetime is not measured).

### A resumed ClientHello's shape

Fields: `legacyVersion`, session ID and key share lengths, ECH (`resumption.echPayloadLengths`).

- **Chrome:** W (21 TLS 1.3 resumed hellos: `resume` 3 samples, `cookies-edge` 2 of 3): as the full hello.
  - ECH suite AES only.
  - `payloadLengths` {144, 176, 208, 240} (5, 2, 7, 7 times), the full hello's set.
  - BoringSSL's GREASE length does not depend on resumption.
  - Chrome 156's own `resume` (2026-10-28, 2 samples, 13 resumed): AES only, {144, 176, 208, 240} (2, 4, 3, 4).
- **Firefox:** W (14, `resume` 2026-10-28, 2 samples, 7 each): as the full hello.
  - The 2026-10-24 capture showed the same in 21.
  - ECH suites both (8 AES, 6 ChaCha).
  - `payloadLengths` 400 in all 14 (240 when full).
  - NSS pads for the inner hello, which carries the PSK.
- **Safari 27:** —
- **Safari 26:** —
- **Checks:**
  - `TestResumptionMatchesBrowser` diffs each mimicry resumed hello against the browser's.
  - ECH sets are taken from every resumed connection of every sample (the profile-shape fallback is gone).
  - Firefox's 400 was measured with the capture server's tickets only.
  - That it scales with ticket size is [INFERENCE], unmeasured.
  - TLS 1.2 resumptions (session_ticket) keep the full hello's ECH lengths (Chrome {144..240}, Firefox 240).

### `websocketH1`

The hello of a WebSocket's own HTTP/1.1 connection: `alpn`, `drop`.

- **Chrome:** W (`websocket`: 12 socket connections, 4 per sample, all TLS 1.3 resumed).
  - `alpn` `http/1.1`, `drop` ALPS (0x44cd).
  - Otherwise the resumed hello (`pre_shared_key`).
- **Firefox:** W (12, all resumed).
  - `alpn` `http/1.1`, `drop` `compress_certificate` (0x001b), which Firefox's other resumed hellos keep.
  - `session_ticket` dropped as `resumption.drop`.
- **Safari 27:** W (12, full): `alpn` `http/1.1`, nothing dropped.
- **Safari 26:** W (12, full): as Safari 27.
- **Checks:**
  - Generated by `hellogen`. It diffs every socket connection's hello against the profile's with these changes and refuses any other difference.
  - A full-handshake socket hello for Chrome and Firefox (no ticket for the host) is not captured.
  - The same changes are assumed ([websocket report](../../internal/harness/reports/websocket-2026-10-24.md)).

### `helloRetry`

Where the hello sent again after a HelloRetryRequest with a cookie puts the cookie: `cookieAfter`, `cookieShuffled`.

- **Chrome:** W (Chrome 156, `hrr` 2026-11-03, 18 retried connections over 3 samples).
  - `cookieShuffled`: in 18 different places, never among the GREASE ends.
  - Chrome 155: — (not captured).
- **Firefox:** W (Firefox 158 on the macOS 26 Mac, 95 retried connections over 3 samples).
  - 81 of them are Firefox's fallback hellos without `compress_certificate`.
  - After `signature_algorithms` (0x000d), before `psk_key_exchange_modes`, in all 95.
- **Safari 27:** W (Safari 27, 36 retried connections over 3 samples).
  - After `supported_versions` (0x002b), before `compress_certificate`, in all 36.
- **Safari 26:** — (not captured).
- **Checks:**
  - Generated by `hellogen` from every retried connection of `hrr`.
  - Each second hello is its connection's first with the cookie added and one key share for the group asked for.
  - The group was P-256 here; the other shares, GREASE included, are gone.
  - `hellogen` refuses any other difference, and a fixed-order cookie that ever moves.
  - Checked by `TestHelloRetryMatchesBrowser` (20 retried connections, each against the browser's second hello).
  - Not measured: TCP hellos of resumed or WebSocket connections after a HelloRetryRequest. They place the cookie as the full hello does.
  - Not measured: Windows, iOS, Chrome 155 and Safari 26. They keep uTLS's random placement.

### `clientAuth.tls13`, `clientAuth.tls12`

The CertificateVerify scheme per key type and TLS version; the certificate sent.

- **Chrome:** W (Chrome 156 macOS and Windows, 157 Linux; Edge 157 macOS and Windows alike).
  - `client-cert-*` 2026-11-08, 3 samples each, 143 signed handshakes, Edge 95.
  - TLS 1.3: `0x0403` (P-256), `0x0804` (RSA).
  - TLS 1.2: `0x0403`, `0x0401` (RSA PKCS#1).
  - The leaf alone in all.
  - Chrome 155: — (not captured).
- **Firefox:** W (Firefox 159 macOS, Windows, Linux, 432 signed).
  - TLS 1.3 and 1.2: `0x0403`, `0x0804`.
  - The leaf alone in all.
  - Firefox 158: — (not captured).
- **Safari 27:** — (not capturable: Safari sent an empty Certificate or stalled).
- **Safari 26:** —
- **Checks:**
  - Generated by `hellogen` from every signed handshake of the four `client-cert-*` captures. Samples that disagree fail it.
  - Only P-256 and RSA-2048 identities captured. Other keys are `ErrNotMeasured`.
  - `TestClientCertMatchesBrowser` ([0064](../../docs/decisions/0064-client-certificates.md)).

### `clientAuth.resume`

A session the certificate was sent in is resumed, without it.

- **Chrome:** W: true. Chrome 289 and Edge 193 resumed connections after the one that sent it.
- **Firefox:** W: false. No ticket or PSK offered on any of 432 connections, each a full handshake with the certificate.
- **Safari 27:** —
- **Safari 26:** —
- **Checks:** generated by `hellogen`; every sample must agree. `TestClientCertMatchesBrowser`.

### `clientAuth.restart`

The first connection a host and port asks on is closed with nothing sent; the request goes again on a new one.

- **Chrome:** W: true.
  - In every sample the first connection to each of the 3 hosts sent nothing after the CertificateRequest.
  - No connection answered without the certificate.
  - Chrome: 144 drops over 36 samples, the page's host twice, its preconnect. Edge: 72 over 24.
- **Firefox:** W: false in every sample.
  - Each has a first connection that answered, or an empty Certificate.
  - Its dropped connections are speculative, carrying no request.
- **Safari 27:** —
- **Safari 26:** —
- **Checks:**
  - Generated by `hellogen`, per sample. Samples that disagree fail it.
  - mimicry drops once per host and port per Transport and dials again at once.
  - The browsers took a few ms to 1.7 s choosing.
  - Chrome's second drop on the page's host is its preconnect (Not covered).
  - `TestClientCertMatchesBrowser`.

### `realEch`

A host whose DNS HTTPS record carries an ECH config list: `ignore`, `kems`, `skippedKems`, `cipherSuites`, `malformed`.

- **Chrome:** W (Chrome 157 macOS and Linux, Edge 157 macOS and Windows, Brave 157 macOS, Windows and Linux).
  - `ech` 2026-11-09, 3 samples each, 27 ECH connections per capture, Chrome on Linux 30.
  - `kems` X25519 (0x0020), `skippedKems` P-256 (0x0010).
  - `www.ech-suite.test`'s list starts with a P-256 config, and every sample used the X25519 one after it.
  - `cipherSuites`: HKDF-SHA256 with AES-128-GCM, AES-256-GCM and ChaCha20Poly1305, each the first suite of the config used.
  - The retry config lists ChaCha20Poly1305 first, the suite host's AES-256-GCM.
  - `malformed` `fail`: no connection to `www.ech-bad.test` in any sample.
  - Chrome 155 and 156: — (not captured).
- **Firefox:** W (Firefox 159 macOS, Windows and Linux, 3 samples each, 24 to 28 ECH connections per capture).
  - `kems` P-256 and X25519, nothing skipped (the suite host's P-256 config taken).
  - `cipherSuites` AES-128-GCM and ChaCha20Poly1305. AES-256-GCM was never a used config's first suite.
  - `malformed` `grease`: the usual hello with the GREASE extension.
  - Firefox 158: —
- **Safari 27:** W (`ech`, 3 samples, 24 connections).
  - `ignore`: no `encrypted_client_hello`, real or GREASE, on any connection.
- **Safari 26:** — (not captured).
- **Checks:**
  - Generated by `hellogen` (`realECH`).
  - It refuses a connection that did not take the first config with a KEM the browser uses, or not that config's first suite.
  - mimicry looks the record up beside the dial for direct `https://` connections only (`internal/httpsrr`, through `Config.Resolver`).
  - It picks the config as the browser did (`handshake.SelectECH`).
  - A config with a KEM or first suite the browser was never seen to use or pass over is `ErrNotMeasured` (`TestSelectECH`).
  - A profile without `realEch`, or with `ignore`, looks nothing up.
  - `TestECHMatchesBrowser` replays the page: each connection's host, config, suite and acceptance, in order.
  - It also expects an HTTPS lookup before each host's first connection.
  - [Decision 0068](../../docs/decisions/0068-real-ech.md).

### `realEch.inner`, `realEch.outerPsk`

The ClientHelloInner; the outer hello of a resumed inner one.

- **Chrome:** W (as above, every ECH connection).
  - Own extensions: `encrypted_client_hello` (inner), `server_name` and `supported_versions` (GREASE, TLS 1.3), shuffled per connection.
  - Then `ech_outer_extensions` (0xfd00), naming the outer hello's others in its order, GREASE included.
  - Excluded (`drop`): `extended_master_secret`, `renegotiation_info`, `ec_point_formats` and `session_ticket`.
  - Cipher suites: GREASE and TLS 1.3's three.
  - `outerPsk` `none`: no `pre_shared_key` in the outer hello when the inner one resumes.
- **Firefox:** W.
  - The same own extensions in a fixed order (ECH, `server_name`, `supported_versions`, 0xfd00).
  - The same `drop`.
  - Cipher suites: its full outer list (15).
  - `outerPsk` `grease`: a random `pre_shared_key` of the inner one's lengths.
- **Safari 27:** —
- **Safari 26:** —
- **Checks:**
  - Generated by `hellogen`.
  - It also checks every inner hello's padding against RFC 9849, section 6.1.3 (the configs' `maximum_name_length` 0, 64 and 32).
  - Sent through the uTLS fork's `ECHInnerSpec` (branch `fork/ech-inner-spec`).
  - `TestECHMatchesBrowser` checks outer and inner hellos (a shuffled order compared as a set).
  - It also checks the extensions copied through `ech_outer_extensions`, padding and session offers.

### `realEch.retryResume`, `retryPlain`, `retryDrop`

What follows a rejection.

- **Chrome:** W.
  - Rejected with retry configs (`www.ech-retry.test`, every sample): one more connection with them, accepted.
  - The next connection there offered the DNS config again with that connection's session (`retryResume`).
  - Rejected without (`www.ech-noretry.test`): one more connection, the usual hello with GREASE and the real `server_name`.
  - Nothing dropped.
- **Firefox:** W, in every sample.
  - Both reconnections as Chrome's but without `compress_certificate` (`retryDrop`).
  - After the connection with retry configs, on macOS (the system's resolver): the DNS config offered again without a session.
  - On Windows and Linux (its own DoH): the usual resumed hello with GREASE and the real `server_name` (`retryPlain`).
- **Safari 27:** —
- **Safari 26:** —
- **Checks:**
  - Generated by `hellogen`.
  - A rejecting server must prove itself the public name (a certificate valid for it), or the handshake fails.
  - mimicry reconnects once (`echRetry`). A second rejection fails the request.
  - Not covered: Firefox's racing rejected offers, and the speculative connections of Chrome on Linux and Firefox over DoH.
  - `TestECHMatchesBrowser`.

### `realEch.altSvcIgnored`

No HTTP/3 from `Alt-Svc` for a host whose ECH config is used.

- **Chrome:** W (Chrome 157 macOS, `ech-h3` with `-h3-local-roots`, 3 samples): true.
  - All 21 connections on HTTP/2 with ECH accepted and no QUIC packet.
  - `Alt-Svc: h3=":443"` on every response, and QUIC served on UDP 443.
  - Chrome on Linux, Edge, Brave: — (not captured).
- **Firefox:** W (Firefox 159 macOS, 21 connections): true, as Chrome. Windows, Linux: —
- **Safari 27:** — (not capturable: Safari ignores the configs, and its `Alt-Svc` cache for :443 outlived a sample).
- **Safari 26:** —
- **Checks:**
  - Generated by `hellogen` from `ech-h3`. Any QUIC connection there fails it, so false is never measured.
  - Trials not committed: the same record without its ECH config moved both browsers to HTTP/3.
  - mimicry learns no `Alt-Svc` from a response on an ECH-accepted connection where it is set.
  - `TestECHH3MatchesBrowser`.

### `quic`

The hello sent over QUIC: `cipherSuites`, `sessionIdLength`, `extensionOrder`, `last`, `drop`, `replace`, `transportParametersBefore`, `resumedEchPayloadLengths`.

- **Chrome:** W (`h3-discovery`, `h3-page`, with and without `-h3-local-roots`: 33 QUIC connections, 9 resumed).
  - TLS 1.3 suites without GREASE; session ID 0.
  - Shuffled, no GREASE extension.
  - Drops GREASE, `ec_point_formats`, SCT, EMS, `renegotiation_info`, `session_ticket`, `status_request`.
  - Replaces ALPS and ALPN (`h3`), key shares and groups (no GREASE), signature algorithms (no ML-DSA, `0201` added), `supported_versions` `0304`.
  - ECH from the TLS sets (9 resumed showed 144 and 240 of the four).
- **Firefox:** W (33, 9 resumed).
  - Suites `1301 1303 1302`.
  - Shuffled with `quic_transport_parameters`, then `encrypted_client_hello` last.
  - Drops `ec_point_formats`, `session_ticket`, SCT.
  - Replaces ALPN, `supported_versions`, signature algorithms (`0203` 4th), `compress_certificate` `0001 0003 0002`.
  - Resumed ECH payload 432 (TCP 400).
- **Safari 27:** W (3).
  - GREASE + TLS 1.3 suites.
  - Fixed: TCP's order without EMS, `renegotiation_info`, `ec_point_formats`.
  - `quic_transport_parameters` before `compress_certificate`.
  - ALPN `h3`, `supported_versions` GREASE `0304`.
- **Safari 26:** W (3): as Safari 27.
- **Checks:**
  - Generated by `hellogen` from every QUIC connection's ClientHello, and every resumed one of the edge captures.
  - It diffs them against the TLS hello with these changes, and refuses any other difference.
  - Order compared exactly when fixed; `last` and `pre_shared_key` at the end when shuffled.
  - A resumed hello is also without `resumption.drop`, with `earlyData` 0x002a.
  - Safari resumed over QUIC only on the edge pages (next claim).
  - The transport parameters' body is `quic.transportParameters` (in the HTTP/3 evidence).

### `quic.earlyDataBefore` and the resumed QUIC hello

A resumed QUIC hello's `early_data` place in a fixed order.

- **Chrome:** — (shuffled; its resumed hellos also checked from the edge captures).
- **Firefox:** — (shuffled; as Chrome).
- **Safari 27:** W (the edge captures' resumed hellos, 60: `h3-broken` 6, `h3-early` 27, `h3-too-early` 27).
  - `early_data` before `supported_versions`, `pre_shared_key` last.
  - Otherwise the full QUIC hello, every one.
- **Safari 26:** W: as 27 (60).
- **Checks:**
  - Generated by `hellogen`. Since decision 0050 it checks every resumed QUIC hello of the edge captures too and refuses any other difference.
  - mimicry's resumed Safari hello is built from it (`fingerprint.ResumedQUICHello`).
  - A ticket allowing no 0-RTT (`max_early_data_size` 0) gets the resumed hello without `early_data`, every profile (#52): derived, not measured.
  - Every resumed QUIC hello captured (Chrome 9, Firefox 9, Safari 60 each) was to a server allowing 0-RTT.
  - The rule is BoringSSL's `should_offer_early_data`, NSS's `tls13_ClientAllow0Rtt` and RFC 8446 section 4.2.10.
  - Chrome's 13 extensions without it (14 with) agree with httpcloak PR 145's report.
  - `TestQUICInterop` `0rtt-offer-follows-ticket` checks both against quic-go.

### `quic.versionNegotiation`

The hello of a connection retried after Version Negotiation: `drop`, `replace`, `padding`.

- **Chrome:** — (no retry).
- **Firefox:** W (`h3-vn`, 3 connections retried in v2).
  - The full QUIC hello without `compress_certificate` and GREASE ECH.
  - Groups and key shares without X25519MLKEM768 (key share X25519 only, 32 B).
  - Padding (0x0015) after `quic_transport_parameters`, last.
  - The hello is 512 bytes (NSS's 256–511 → 512).
  - Shuffled as the full one.
- **Safari 27:** — (no retry).
- **Safari 26:** —
- **Checks:**
  - Generated by `hellogen` from every h3-vn connection that followed a Version Negotiation in another version.
  - Diffed against the full QUIC hello; any other difference is refused.
  - Required iff `quic.versionNegotiation.retry`.

## Cross-checks outside mimicry

- Firefox 158's JA3 matches what tls.peet.ws reports for the same browser.
- Safari 27's JA3, JA4 and Akamai fingerprints match what tls.peet.ws reports.
