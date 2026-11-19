# Profile notes

Per-profile notes for the profiles without a column of their own: what each was captured on, whose column to read, what it inherits or copies, where it differs and what is pinned.

Classes as in the columns: **W** (its own capture), **I** (inherited), **C** (copied).

## chrome-156-macos

- **Captured:** quick refresh, 2026-10-26 ([report](../../internal/harness/reports/refresh-chrome-156-macos-2026-10-26.md)).
- **Column:** none of its own. Read its class from `chrome-155-macos`'s column.
- **Own captures:**
  - Rows whose evidence is a quick-refresh scenario are its own **W**: `page`, `http1`, `kinds`, `kinds-http1`, `kinds-plain`, `websocket`, `websocket-h2`, `h3-discovery`, `h3-page`.
  - Three samples for `page`, `h3-discovery` and `h3-page` (the TLS and QUIC hellos, where Chrome shuffles). One for the others.
  - `resume` is its own too: two samples, 2026-10-28. So its resumption rows are its own **W**.
  - The redirect `cookie`-slot row is its own **W**, from the cookie twins (2026-10-30, decision 0048). Chrome 155 lacks it.
- **Differs from chrome-155-macos:** nothing beyond the user agent and `sec-ch-ua`.
- **Inherited / copied:** every other row is `I: from chrome-155`.
- **Checks:** `realcheck` showed no difference against the real Chrome 156.

## chrome-157-macos

- **Captured:** quick refresh, 2026-11-09. Chrome 157.0.8103.39 on the second Mac, macOS 26.2 ([report](../../internal/harness/reports/refresh-chrome-157-macos-2026-11-09.md)).
- **Column:** none of its own. Read its class from `chrome-155-macos`'s column.
- **Own captures:**
  - Rows whose evidence is a quick-refresh scenario are its own **W**: `page`, `http1`, `kinds`, `kinds-http1`, `kinds-plain`, `websocket`, `websocket-h2`, `h3-discovery`, `h3-page`, `hrr`, `client-hints`.
  - Three samples for `page`, `h3-discovery`, `h3-page`, `hrr` and `client-hints`. One for the others.
- **Differs from chrome-156-macos:** nothing beyond the version tokens, except:
  - **W, client hints:** `fullVersion` 157.0.8103.39.
    - The second Mac's machine: macOS 26.2.0, arm, 16 GB, reduced motion and transparency, viewport 1200×1195. As `edge-157-macos`'s.
    - Chrome 156's `client-hints` capture was taken on this Mac (macOS 27.0.1, 32 GB, 1200×953).
    - `width` 100, as Chrome 156's.
  - **W, `quic.crypto.head`:** 56–86 (Chrome 156: 55–86). The range of quiche's random head length over the samples.
- **Inherited / copied:** every other row is inherited.
  - `I: from chrome-156` where Chrome 156 took the scenario itself: `resume`, `urls`, the cookie twins, `bodiless`, `h2-upload`, `http1-upload`, `h3-resume-table`, `h3-streams-blocked`, `client-cert-*`, `proxy-https*`.
  - `I: from chrome-155` for the rest.
- **Pinned gaps:** as Chrome 156's.
  - The server's Initial acknowledged in 2 of 21 full handshakes (h3gen's `sampleGaps`).
  - Two PRIORITY_UPDATEs read 1 ms after their HEADERS (h3gen's `sampleGaps`).
  - The client-hints third party's connection and `width` (`h3Gaps`).
  - Through the inherited captures, as Chrome 156's and 155's: the plain-HTTP page's link navigation (`proxyPlainGaps`) and the pooled host ending in a dot (`urlsKnown`).
- **Checks:** `realcheck` on the second Mac found no difference between Chrome 157 and `chrome-157-macos` on the echo endpoints: tls.peet.ws, browserleaks' TLS and HTTP/2 pages, impersonate.pro over HTTP/2 and HTTP/3.

## chrome-156-windows

- **Captured:** 2026-10-29, Chrome 156.0.8078.80 on Windows 10 ([report](../../internal/harness/reports/refresh-chrome-156-windows-2026-10-29.md); [decision 0047](../../docs/decisions/0047-windows-profiles.md)).
- **Column:** none of its own. Each row has the class and value of `chrome-155-macos`'s column, and the same tests keep it true.
- **Own captures:** every scenario and replay, captured on Windows, three samples each. So each row is its own.
- **Differs from chrome-155-macos:**
  - **The platform:** `user-agent` says `Windows NT 10.0; Win64; x64` and `sec-ch-ua-platform` `"Windows"`. In every header list (the navigation, HTTP/1.1, every kind and variant).
  - **`http3.discovery.fallback.tcpDelayMs`:** 307–314 ms (macOS 305–306).
  - **The GREASE transport parameter's `maxLength`:** 14 (macOS 15). The longest seen; Chrome draws the length per connection.
  - **One gap fewer than on macOS:** Chrome on macOS moved a broken origin's requests to a new TCP connection after 6–10 s idle (pinned there, `h3EdgeGaps`). Chrome on Windows kept its connection in all three `h3-broken` samples, as mimicry does.
  - **Timing only:**
    - The pair's POST in `h3-early` and `h3-too-early` came 4–6 ms after its GET, after the resumed handshake, and went in 1-RTT (5 of 6; `h3EdgeSchedulingGaps`).
    - One migration's PATH_RESPONSE went before its RETIRE_CONNECTION_ID, the server's challenge having arrived first (`h3gen` counts the client's own datagrams).
    - QUIC give-ups 1–15 ms after 4.0 s (macOS 0–4).
  - **The recorder on Windows:**
    - Its clock moves in steps of about 0.5 ms. So packets of both sides often share a timestamp (the download test orders such ties server first).
    - quic-go's NEW_TOKEN tokens were 89 or 90 bytes, by the loopback RTT they encode. The tests compare whether a token is sent, not its length.
- **Inherited / copied:** nothing.
- **Pinned gaps:** as on macOS.
  - NEW_TOKEN presented on 3 of 9 `h3-discovery` reconnections (`h3gen`'s `sampleGaps`; `quic.tokens` true, as macOS).
  - One PRIORITY_UPDATE read 1 ms after its HEADERS (1 of 3 samples).
  - The preconnect (`connectionsPreconnects`).
  - The XHR header order (`headersOrderGaps`).
  - The error-page reload (`redirectsReload`).
  - The blackhole resend and resumed probes (`h3EdgeGaps`). The first resumed probe was 2 Initials in 1 of 3 samples, 4 in the others.
  - The fast fallback's differences (`h3EdgeFastGaps`).
- **Checks:** `realcheck` on the Windows PC found no difference between Chrome and `chrome-156-windows` on the echo endpoints.

## firefox-158-windows

- **Captured:** 2026-11-01, Firefox 158.0.1 on Windows 10 ([report](../../internal/harness/reports/refresh-firefox-158-windows-2026-11-01.md); [decision 0047, amendment](../../docs/decisions/0047-windows-profiles.md#amendment-2026-11-01-firefox-on-windows)).
- **Column:** none of its own. Each row has the class and value of `firefox-158-macos`'s column, and the same tests keep it true.
- **Own captures:** every scenario and replay, captured on Windows, three samples each. So each row is its own.
- **Differs from firefox-158-macos:** the ClientHello, the HTTP/2 preface and SETTINGS, every header list and every kind are macOS Firefox's but for the platform. Where Windows differs:
  - **The platform:** `user-agent` says `Windows NT 10.0; Win64; x64` in every header list. The script kinds' variants come in another order, the same set.
  - **W, `websocket.closeH1`:** `after_wait`, `closeWaitMs` 1000: Firefox closed every HTTP/1.1 socket's TCP connection after a wait.
    - Firefox closed the TCP connection of every HTTP/1.1 socket 1002–1095 ms after the close handshake, before the server's 3 s. That is `websocket`, `websocket-h2`'s `plain-so` and the three proxied pages.
    - macOS's captures recorded no transport end, so `firefox-158-macos` leaves it unmeasured.
  - **W, from the edge captures (h3gen):**
    - `revalidateMs` 47–5675 (macOS 797–3020).
    - `raceDelayMs` 3 (`firefox-158-macos` 6–9).
    - No `resumedDialDelayMs`: it dialled at once (macOS 0–34 ms).
    - `quic.loss.cachedRtt` false. The resumed attempt after the server's close first probed 1.7–1.9 s after its first flight (macOS's 3 ms in).
    - `quic.loss.resumed.afterMs` 9800 (macOS 5000).
    - `http3.firstDataFrame` 32228 (32224).
  - **The unused opening connection:**
    - A second TCP connection to the page's host 0–27 ms after the first, its HTTP/2 preface then GOAWAY.
    - Seen in 131 of the 135 samples of pages opened directly over HTTPS. None through a proxy, none on macOS.
    - Not modelled, as Chrome's preconnect is not (Not covered, below). The tests leave it out of the browser's pages (`unusedOpenings`).
    - Its full handshake left Firefox one more TLS 1.2 ticket. So on `resume-tls12` one more connection to the page's host resumed: 5/3 in every sample (macOS and mimicry 5/2: `resumeCountGaps`).
  - **Timing only:**
    - The pair's POST in `h3-early` and `h3-too-early` came 4 ms after its GET, after the resumed handshake's Finished, and went in 1-RTT (6 of 6; `h3EdgeSchedulingGaps`).
    - The requests waiting on a silenced QUIC connection went to TCP 73–470 ms after the server's idle record (macOS 7–22 ms). h3gen times them from the client's own death.
    - The page's timers fired late, every sample (macOS on time). `h3-broken`'s keep2.cs came 4.9 s after silent5.so where the page waits 2 s; later ones 3.7–5.5 s late. So one connection's time after its trigger is pinned (`h3EdgeGaps`).
  - **The recorder on Windows:** as for Chrome, its clock moves in steps of about 0.5 ms. h3gen finds a retry after Version Negotiation that shares the first attempt's millisecond.
- **Inherited / copied:** nothing.
- **Pinned gaps:**
  - As on macOS:
    - The XHR header order (`headersOrderGaps`).
    - The credential-less connections (`partitionGaps`; 2 of 3 Windows samples).
    - `www.redir-c.test`'s connections and the markup images' priority (`redirectsMerged`, `redirectsLineGaps`).
    - The FontFace load (`h3Gaps`).
    - The plain-HTTP page's script and link navigation (`proxyPlainGaps`).
    - The resumed probes, the TCP connections beside a 425 and the STREAMS_BLOCKED per raise (`h3EdgeGaps`).
    - The burst order (`h3EdgeSchedulingGaps`) and the fast fallback (`h3EdgeFastGaps`).
  - On Windows only:
    - One first Initial's packet number (242) in 1 byte, 1 of 5 draws of 128 or more (`h3Gaps`).
    - `/big`'s largest credit increment 3.56 MB, below mimicry's 4 MiB, which 2 of 3 macOS samples sent (`creditGaps`).
    - The cross-site HTTP/2 socket's end (`webSocketEndGaps`).
    - In h3gen's `sampleGaps`: one `h3-broken` sample whose Firefox opened 14 QUIC connections beside the page's and closed each within 5 ms (one `h3-download` sample did the same for 7 requests). Its resumed attempt probed from a cached RTT and ended silently at 4.5 s with nothing held.
    - The resumed give-up at 10.2 s in one sample (9.8 s in the other).
    - A keep-alive PING 97 ms late.
    - One MAX_STREAM_DATA 2.4% above 3 MiB.
- **Checks:** `realcheck` on the Windows PC found no difference between Firefox and `firefox-158-windows` on the echo endpoints that answered.
  - Answered: browserleaks' HTTP/2 page, impersonate.pro over HTTP/2 and HTTP/3 (the TLS and QUIC hellos, HTTP/2 and HTTP/3).
  - tls.peet.ws refused connections that day. browserleaks' TLS page gave the browser no JSON.

## firefox-159-macos and firefox-159-windows

- **Captured:** quick refreshes, 2026-11-03, Firefox 159.0.1 ([macOS report](../../internal/harness/reports/refresh-firefox-159-macos-2026-11-03.md), [Windows report](../../internal/harness/reports/refresh-firefox-159-windows-2026-11-03.md)).
- **Column:** none of their own. Rows keep `firefox-158-macos`'s and `firefox-158-windows`'s class and value.
- **Own captures:** the fingerprint layer was captured again: `page`, `http1`, `kinds*`, `websocket*`, `h3-discovery`, `h3-page`, `hrr`.
- **Differs from firefox-158:** nothing beyond the version tokens, except:
  - **W, `websocket.closeH1` on macOS:** `after_wait`, `closeWaitMs` 1000, as on Windows.
    - Firefox 159 on macOS closed every HTTP/1.1 socket's connection 1.0 s after the close handshake (14 of 14). `firefox-158-macos`'s older captures did not record this.
    - The cross-site HTTP/2 socket's end is pinned on macOS too (`webSocketEndGaps`).
  - **W, `tls.helloRetry` on Windows:** `cookieAfter` 0x000d, macOS's. From Windows' first `hrr` capture (`firefox-158-windows` had none).
  - **Side connections:**
    - In 3 of 6 macOS and 4 of 6 Windows `h3-discovery`/`h3-page` samples with locally trusted roots, Firefox opened QUIC connections beside the page's, without a request. Each closed 2–5 ms after its handshake (Not covered, below).
    - The first resumed with 0-RTT and the NEW_TOKEN token. Every later connection to that host in the sample started fresh (no ticket, no token).
    - A side connection sent 3 NEW_CONNECTION_IDs before closing, not 4.
    - Token use follows resumption in all 81 macOS connections of 158 and 159. The samples without side connections reuse both as 158 did. So `quic.tokens` and the NEW_CONNECTION_ID count stay as 158's.
    - h3gen's `sampleGaps` pin the side connections (`tokens`, `newConnectionIDs`, `http3` where the control stream never arrived). In `h3-discovery` they also pin the dial after the server's close (`raceAfterGoAway`: no TCP race in those samples).
    - kindsgen's `h3AltUsedGaps` pins the fetch that then went on a fresh QUIC connection without `alt-used`.
  - **W, `http3.discovery.raceDelayMs`:** macOS 6 (158: 6–9), Windows 2 (158: 3). From the one `h3-discovery` sample per platform without side connections. The others raced no TCP: pinned, below.
- **Inherited / copied:** every other scenario is inherited (**I**: from firefox-158, its class).
- **Pinned gaps:** as for 158.
  - The markup image's priority over HTTP/3: 2 of 3 macOS samples, 1 of 3 Windows (kindsgen's `h3PriorityGaps`).
  - The FontFace load (`h3Gaps`).
  - `/big`'s largest credit increment: 2.36 MB on macOS, 2.67 MB on Windows, below mimicry's 4 MiB (`creditGaps`).
  - One first Initial's packet number in 1 byte: macOS 1, Windows 2 (`h3Gaps`).
  - On Windows, the credential-less connections (`partitionGaps`).
- **Checks:** `realcheck` found no difference between Firefox 159 and either profile on the echo endpoints: tls.peet.ws, browserleaks' TLS and HTTP/2 pages, impersonate.pro over HTTP/2 and HTTP/3. On macOS, browserleaks' HTTP/2 page showed one field on one side only.

## chrome-157-linux

- **Captured:** 2026-11-05, Chrome 157.0.8103.38, Google's `.deb`, in WSL2 on the Windows PC with Xvfb as its display ([report](../../internal/harness/reports/refresh-chrome-157-linux-2026-11-05.md); [decision 0058](../../docs/decisions/0058-linux-profiles.md)).
  - Chrome on Fedora 45 sent the same default page in every field.
- **Column:** none of its own. Each row has the class and value of `chrome-155-macos`'s column, and the same tests keep it true.
- **Own captures:** every scenario and replay, captured on Linux, three samples each. So each row is its own.
- **Differs from chrome-155-macos:**
  - **The platform:** `user-agent` says `X11; Linux x86_64` and `sec-ch-ua-platform` `"Linux"` in every header list.
  - **W, the client hints' machine:**
    - `platformVersion` `""` (Chrome sends none on Linux), `arch` x86.
    - The Xvfb screen: `dpr` 1, viewport 945×973, light scheme. `deviceMemory` 16.
    - `downlink` is 1.55, the centre of the 1.3–1.75 Mbps sent. Chrome's estimate rose by 30% or more within a sample, one host getting both ends. That is beyond its fixed per-host noise of ±10% (kindsgen's `downlinkGaps`).
  - **W, from the edge captures (h3gen):** `tcpDelayMs` 302–303 (macOS 305–306); `quic.crypto.addedFrames.min` 1 (2).
  - **W, `http3.requestsAfterConfirmed` false:**
    - Its full handshakes' first requests read as waiting for the handshake to be confirmed.
    - But 16 of the 30 requests held out of 0-RTT left before it, in all six `h3-early` and `h3-too-early` samples. h3gen now lets those decide.
    - Chrome 155 and 156 on macOS are false the same way (19 of 31 held requests, six of six samples). Before, the rule was not measured.
- **Inherited / copied:** nothing.
- **Pinned gaps:**
  - As on macOS and Windows:
    - NEW_TOKEN presented on some reconnections (h3gen's `sampleGaps`).
    - The server's Initial acknowledged in 2 of 12 full handshakes and 1 of 10 resumed ones, that one also ACKing before its Finished (macOS 1 of 21 full; h3gen's `sampleGaps`).
    - The unused preconnect (`connectionsPreconnects`).
    - The XHR header order (`headersOrderGaps`).
    - The error-page reload after hop 19 (`redirectsReload`).
    - The plain-HTTP page's link navigation (`proxyPlainGaps`).
    - The client-hints third party's connection and `width` (`h3Gaps`).
    - The blackhole resend, the resumed probes and the idle origin's move to a new TCP connection after 8.0 s, as on macOS (`h3EdgeGaps`).
    - The fast fallback (`h3EdgeFastGaps`).
    - The burst and `h3-too-early` timing (`h3EdgeSchedulingGaps`).
  - On Linux only, in h3gen's `sampleGaps`:
    - One control stream after its Finished's datagram (1 of 12).
    - One MAX_STREAM_DATA 2.07% above 3 MiB.
    - The after-raise STREAMS_BLOCKED pair sent twice in one sample.
  - Not a gap any more: the `h3-page` sample whose page went on a resumed connection, filling the QPACK table in 0-RTT and setting the capacity again in 1-RTT. It is `http3.qpack.resumed`'s rule now (decision 0060).
- **Checks:** `realcheck` in WSL found no difference between Chrome and `chrome-157-linux` on the echo endpoints: tls.peet.ws, browserleaks' TLS and HTTP/2 pages, impersonate.pro over HTTP/2 and HTTP/3.

## firefox-159-linux

- **Captured:** 2026-11-05, Firefox 159.0.1, Mozilla's build from packages.mozilla.org, captured as Chrome was ([report](../../internal/harness/reports/refresh-firefox-159-linux-2026-11-05.md); [decision 0058](../../docs/decisions/0058-linux-profiles.md)).
  - Mozilla's Firefox on Fedora 45 sent the same default page but for the ECH GREASE cipher suite, drawn per connection.
- **Column:** none of its own. Each row has the class and value of `firefox-158-macos`'s column.
- **Own captures:** every row is its own.
- **Differs from macOS Firefox 159:** the ClientHello, the HTTP/2 preface and SETTINGS and every header list are macOS Firefox 159's but for the platform. Where Linux differs:
  - **The platform:** `user-agent` says `X11; Linux x86_64` in every header list.
  - **W, from the edge captures (h3gen):**
    - `raceDelayMs` 2–3 (macOS 6).
    - `dialDelayMs` 0–6 (not measured on macOS).
    - `resumedDialDelayMs` 0–4 (0–34).
    - `revalidateMs` 0–3000 (797–3020).
    - `http3.firstDataFrame` 32238 (32224).
    - `quic.loss.resumed.afterMs` 5000, as on macOS, from 3 of the 6 `h3-broken` samples. The 2 with side connections ended their resumed attempt silently, and sample 2 padded its close to 600 bytes. Each is pinned (below and under Side connections).
    - A first three-sample capture's only closing attempt gave up at 6.1 s. So `h3-broken` was captured six times.
  - **Side connections, as on macOS and Windows:**
    - Firefox opened its burst of QUIC connections without a request in: 1 of 3 `h3-discovery` samples; 2 of 3 `h3-page` and `h3-early`; 1 of 3 `h3-too-early`; 2 of 6 `h3-broken` (15 each time); every `h3-streams-blocked` sample (6, each beside a fetch of the page).
    - In those samples it sent no GET or HEAD in 0-RTT and its held requests after the Finished's datagram. It raced no TCP after the server's close. Its resumed attempt ended silently with nothing held.
    - Pinned in h3gen's `sampleGaps`, kindsgen's `h3AltUsedGaps` and the e2e tests' `h3EdgeGaps`.
    - Three first Initials' packet numbers in 1 byte, all on side connections (`h3Gaps`).
- **Inherited / copied:** nothing.
- **Pinned gaps:**
  - As on macOS and Windows:
    - The markup image's priority (3 of 3: kindsgen's `h3PriorityGaps`, `redirectsLineGaps`).
    - The FontFace load (`h3Gaps`).
    - `/big`'s largest credit increment (3.16 MB: `creditGaps`).
    - The credential-less connections (1 of 3: `partitionGaps`).
    - The XHR header order (`headersOrderGaps`).
    - `www.redir-c.test`'s connections (2 of 3: `redirectsMerged`).
    - The plain-HTTP page's script and link navigation (`proxyPlainGaps`).
    - The cross-site HTTP/2 socket's end (`webSocketEndGaps`).
    - The client-hints third party's connection (`h3Gaps`).
    - The resumed probes (`h3EdgeGaps`).
    - The spacing of requests made together (`h3EdgeSchedulingGaps`).
  - On Linux only (h3gen's `sampleGaps`):
    - The first MAX_STREAM_DATA 21% above 3 MiB in one `h3-download` sample (sent after more was read), and one 2.3% above.
    - In two `h3-streams-blocked` samples, the second burst's two waiting streams opened in one packet behind one STREAMS_BLOCKED.
    - One 0-RTT close padded to 600 bytes, not 255 (1 of 10 closes on any platform).
    - The one resumed connection of 14 whose ACK left before its Finished (`h3-discovery` sample 1, QUIC connection 4: `resumedAckBeforeFinished`).
- **Checks:** `realcheck` in WSL found no difference between Firefox and `firefox-159-linux` on the echo endpoints. browserleaks' HTTP/2 page showed Firefox's stream WINDOW_UPDATE on the browser's side only (on macOS, on mimicry's).

## edge-157-macos

- **Captured:** 2026-11-06, Edge 157.0.4341.42 on the second Mac, macOS 26.2 ([report](../../internal/harness/reports/refresh-edge-157-macos-2026-11-06.md); [decision 0059](../../docs/decisions/0059-edge-profiles.md)).
- **Column:** none of its own. Each row has the class and value of `chrome-155-macos`'s column, but where Edge differs.
- **Own captures:** every scenario and replay, captured from Edge, three samples each.
  - Its profile started as `chrome-156-macos`'s. Every generated section was derived again and every copied one checked against Edge's captures.
- **Differs from Chrome:**
  - **The browser:** `user-agent` ends `Edg/157.0.0.0`; `sec-ch-ua` names `"Microsoft Edge"`. In every header list.
  - **W, TLS:** no Trust Anchor IDs (`0xca34`) in any hello, so 16 extensions where Chrome sends 17. Everything else is Chrome's.
    - JA4 `t13d1516h2_8daaf6152771_806a8c22fdea` (Chrome 157's: `t13d1517h2_8daaf6152771_cb7bf5808d99`).
    - realcheck showed this for the real Edge and mimicry alike.
  - **W, `accept`:** no `image/jxl`, in navigations, frames and every image kind and variant.
  - **W, client hints:**
    - `fullVersion` 157.0.4341.42.
    - The new `chromiumFullVersion` 157.0.8103.39: the Chromium brand's version in `sec-ch-ua-full-version-list`.
    - The second Mac's machine: macOS 26.2, arm, 16 GB, reduced motion and transparency.
    - `width` 100.
  - **W, `quic.packetNumbers`:** `shared_second_long`. Chrome's counter, but the second Initial's number in 2 bytes in 115 of 123 connections, token or not. Chrome: only without a token, 283 of 283. The 8 others, all with a token, are pinned (Not covered, below).
  - **W, from the edge captures (h3gen):** `tcpDelayMs` 313–315 (Chrome on macOS 305–306). The server's Initial acknowledged in 4 of 12 full handshakes (pinned, as Chrome's).
- **Inherited / copied:** nothing.
- **Pinned gaps:** as Chrome's.
  - NEW_TOKEN presented on some reconnections (h3gen's `sampleGaps`).
  - The XHR header order (`headersOrderGaps`).
  - The error-page reload after hop 19 (`redirectsReload`).
  - Markup images at `u=1` in 8 of 24 requests (`redirectsLineGaps`).
  - The plain-HTTP page's link navigation (`proxyPlainGaps`).
  - The client-hints third party's connection and `width` (`h3Gaps`).
  - In `h3EdgeGaps`: the blackhole resend 54.4 s in; the resumed probes (2 Initials, Chrome on macOS 4); the idle origin's moves to new TCP connections after 8.0 and 10.0 s.
  - The fast fallback (`h3EdgeFastGaps`).
  - The burst order (`h3EdgeSchedulingGaps`).
- **Checks:** `realcheck` on the second Mac found no difference between Edge and `edge-157-macos` on the echo endpoints: tls.peet.ws, browserleaks' TLS and HTTP/2 pages, impersonate.pro over HTTP/2 and HTTP/3. browserleaks' HTTP/2 page also logged Edge's own `/favicon.ico` request.

## edge-157-windows

- **Captured:** 2026-11-06, Edge 157.0.4341.42 on the Windows PC, Windows 10 ([report](../../internal/harness/reports/refresh-edge-157-windows-2026-11-06.md); [decision 0059](../../docs/decisions/0059-edge-profiles.md)).
- **Column:** none of its own. Its rows are `edge-157-macos`'s: the same hello, JA4, headers, client-hint rules and QUIC parameters.
- **Own captures:** every scenario and replay, captured on Windows with the PC's Edge policies lifted.
  - The policies made Edge send `dnt: 1`. A first run was discarded.
  - It started from `edge-157-macos`.
- **Differs from edge-157-macos:**
  - **The platform:** `user-agent` says `Windows NT 10.0; Win64; x64` and `sec-ch-ua-platform` `"Windows"`, in every header list.
  - **W, the machine's client hints:** `platformVersion` 10.0.0, x86, `dpr` 1, 32 GB, dark scheme, viewport 1241×1288.
  - **W, from the edge captures (h3gen):** `tcpDelayMs` 303–313 (macOS 313–315). 114 of 123 second Initials in 2 bytes (macOS 115), the 9 others with a token.
- **Inherited / copied:** nothing.
- **Pinned gaps:** on Windows only.
  - In h3gen's `sampleGaps`: one `h3-blackhole` attempt whose first flight's two Initials went 2.1 ms apart, which the 2 ms flight boundary split (`probes`).
  - In h3gen's `sampleGaps`: two MAX_STREAM_DATA 2.08% above 3 MiB (`credit`).
  - In the e2e tests: one `h3-broken` sample's move to a new path after 2.91 PTOs, where the other two moved after 3.89 and 3.93 (`pathMoveGaps`).
- **Checks:** `realcheck` on the Windows PC, the policies lifted again for it, found no difference between Edge and `edge-157-windows` on the echo endpoints. browserleaks' HTTP/2 page logged Edge's own `/favicon.ico` request.

## brave-157-macos

- **Captured:** 2026-11-09, Brave 1.99.55 (reported as 157.1.99.55) on the second Mac, macOS 26.2 ([report](../../internal/harness/reports/refresh-brave-157-macos-2026-11-09.md); [decision 0067](../../docs/decisions/0067-brave-profiles.md)).
- **Column:** none of its own. Each row has the class and value of `edge-157-macos`'s, but where Brave differs.
  - The same: hello, JA4 `t13d1516h2_8daaf6152771_806a8c22fdea`, HTTP/2 preface, QUIC transport parameters and `shared_second_long`.
- **Own captures:** every scenario and replay, and the four `proxy-https*` ones. Captured from Brave with its Shields at their defaults, three samples each.
  - Its profile started as `chrome-156-macos`'s.
- **Differs from edge-157-macos:**
  - **The browser:** the user agent is Chrome's, word for word. `sec-ch-ua` names `"Brave"`. `sec-gpc: 1` after `accept` in every list.
  - **W, `accept`:** the navigation's keeps `image/jxl` (Edge's has none) but has no `application/signed-exchange;v=b3;q=0.7`.
  - **W, `context.acceptLanguageQ`:** 0.5 to 0.9, all five seen.
    - kindsgen derives the set from every request of every capture as sent. CORS preflights are excluded; they all carry the lists' `en-US,en;q=0.9`.
    - `TestAcceptLanguageFarbling` checks the rule against the samples and mimicry's draws: one q per page origin, redirect hops keeping the first URL's.
    - Every other test compares the captures unfarbled.
  - **W, `cookies.thirdParty`:** `partition_all` (hand-written). No first-party cookie on a cross-site request (`cookies`: `c-part.cs`, `c-third.so`). Every cookie, CORS, redirect and WebSocket scenario matches.
  - **W, `context.blocksCrossSiteBeacons`:** the kinds pages' beacon to `cdn.kinds-b.test` absent in 9 of 9 samples. The same-site ones were sent.
  - **W, client hints:** only the platform version, arch, bitness, model, WoW64 and the full version list.
    - Every brand is `157.0.0.0`, so `fullVersion` 157.0.0.0.
    - The machine's other fields are the seed's and never sent.
  - **W, from the edge captures (h3gen):**
    - `tcpDelayMs` 3–5 (Chrome 305, Edge 313). Read as a race, since the 0-RTT attempt held its request to its give-up (`zeroRttHolds`).
    - `pathDegrading.afterPtos` 4, as Chrome's and Edge's (after its second probe).
    - 143 of 150 second Initials in 2 bytes, the 7 others with a token.
    - Dial delays for resumed attempts.
- **Inherited / copied:** nothing.
- **Pinned gaps:**
  - As Edge's: NEW_TOKEN reconnections, the XHR header order, the error-page reload, markup images' hops at `u=1` in 3 of 6 samples, the plain-HTTP page's link navigation, the client-hints third party's connection, the blackhole resend, the resumed probes, the idle origin's move to one new TCP connection, the fast fallback, the burst order.
  - One second Initial in 1 byte.
  - The one connection that acknowledged the server's Initial (1 of 21), numbered after the Handshake and 1-RTT packets it led (`coalescedOrder`).
  - Two `h3-broken` samples' path moves after 3.48 and 3.41 PTOs by the e2e measure (`pathMoveGaps`).

## brave-157-windows

- **Captured:** 2026-11-09, Brave 1.99.55 on the Windows PC, Windows 10, a per-user install ([report](../../internal/harness/reports/refresh-brave-157-windows-2026-11-09.md)).
- **Column:** its rows are `brave-157-macos`'s, but where it differs (below). It started from `brave-157-macos`.
- **Differs from brave-157-macos:**
  - The platform strings.
  - The machine's client hints.
  - `tcpDelayMs` 305–313, as Chrome on Windows (macOS 3–5, Linux 2–5).
  - The dial delays (h3gen).
  - The pins (below).
- **Inherited / copied:** nothing.
- **Pinned gaps:**
  - 138 of 150 second Initials in 2 bytes (12 with a token in 1).
  - One `h3-page` PRIORITY_UPDATE read 1 ms after its HEADERS, and one `h3-streams-blocked` sample with 2 more STREAMS_BLOCKED (h3gen's `sampleGaps`).
  - The second page's image at `u=1` in 2 of 3 `h3-discovery` samples (kindsgen's `h3PriorityGaps`).
  - One markup image's hop at `u=1` in 1 of 6 `redirects2` samples.
  - One `h3-broken` sample's path move after 3.29 PTOs by the e2e measure.
  - 3 of 5 HTTP/1.1 connections resumed on `client-cert-ec` (`clientCertH1Resumes`).
  - The replay's scheduling on `h3-resume-table`, `h3-too-early` and `h3-broken` (`h3EdgeSchedulingGaps`).

## brave-157-linux

- **Captured:** 2026-11-09, Brave 1.99.55 from Brave's apt repository in WSL2, Ubuntu 24.04, on the Windows PC, on Xvfb ([report](../../internal/harness/reports/refresh-brave-157-linux-2026-11-09.md)).
- **Column:** its rows are `brave-157-macos`'s, but where it differs (below). It started from `brave-157-macos`.
- **Differs from brave-157-macos:**
  - The platform strings.
  - The machine's client hints: `x86`, an empty platform version, as Chrome on Linux.
  - From the edge captures (h3gen):
    - `tcpDelayMs` 2–5.
    - No dial delays: every one within the `?t=` clock's 3 ms.
    - `pathDegrading.afterPtos` 4: moves after 3.93–3.97 PTOs by the e2e measure, as Chrome and Edge.
    - 148 of 156 second Initials in 2 bytes (8 with a token in 1).
- **Inherited / copied:** nothing.
- **Pinned gaps:**
  - The token cache's reconnections.
  - One `h3-discovery` PRIORITY_UPDATE read 1 ms after its HEADERS.
  - One download credit raise 2.01% above 3 MiB.
  - `h3-broken` sample 1, which opened a second TCP connection to `www.h3-b.test` 9.2 s in. So its fresh attempt after the give-up came with no new TCP (h3gen's `hold`; the e2e test logs the features its samples then disagree on).
  - Markup images and their hops at `u=1` in 4 to 5 of 6 `redirects2` samples.
  - The replay's scheduling on `h3-resume-table` and the fast fallback.

## safari-27-ios

- **Captured:** 2026-11-01, Safari 27.2 on an iPhone 15 Pro, iOS 27.2, over Wi-Fi ([report](../../internal/harness/reports/refresh-safari-27-ios-2026-11-01.md); [decision 0051](../../docs/decisions/0051-ios-safari-over-the-network.md)).
- **Column:** none of its own. Its own rows have the class of `safari-27-macos`'s column.
- **Own captures:** rows whose evidence is one of the iPhone's scenarios (`server.IOSScenarios`).
  - The scenarios: `page`, `kinds`, `kinds-http1`, `kinds-plain`, `cookies`, `cors`, `cors-edge` and its twin, `redirects2` and its twin, `headers-order`, `http1`, `h3-discovery`, `h3-page`, `resume`, `websocket`, `websocket-h2`, `proxy-connect`, `proxy-plain`.
  - Three samples each, in the phone's own Safari but for the default page's scenarios.
  - `h3-blackhole` and `h3-broken` came after its run.
- **Differs from safari-27-macos:**
  - **The platform:** `user-agent` is `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1` in every list.
  - **Accept, Sec-Fetch-Site, Origin:** added in that order in every request but preflights.
    - macOS Safari 27.0.1 adds Sec-Fetch-Site first in GETs, and Origin before Sec-Fetch-Site in same-origin requests with a body.
    - Every kind's lists and variants are the iPhone's (kindsgen).
    - The order is the Safari app's on iOS, not the platform's: Chrome on the same iPhone has macOS Safari's (`chrome-157-ios`, decision 0053).
  - **Plain HTTP:** User-Agent right after Host, or after Referer, in every subresource and fetch. macOS Safari 27 puts it after Accept.
    - From `kinds-plain`, taken on the iPhone after the first run, when its `proxy-plain` contradicted macOS's.
  - **QUIC and HTTP/3:**
    - Every fresh hello ends its transport parameters with `0xff080808 = 9`.
    - The QPACK encoder stream opens on the server's SETTINGS with only Set Dynamic Table Capacity (`uniStreams` open `settings`).
    - QPACK Huffman is `shorter`.
    - The first request of a full handshake goes with the Finished (`requestsAfterConfirmed` false).
  - **Connections:** credential-less requests on connections of their own (`context.anonymousConnections`, as Chrome's).
- **Inherited / copied:**
  - `I: from safari-27-macos`: rows from `cookies-edge`, `cookies-edge2`, `nav-redirect-{so,ss,cs,http}` and their twins, `set-cookie` and `slow-idle`.
  - **C: from safari-27-macos:** `http3.discovery.fallback`, `quic.keepAlive`, `quic.ack` and `quic.credit`.
    - h3gen keeps them without edge captures for a profile sharing macOS's network stack (`internal/platform.Stack`).
    - The iPhone's success path agrees: after the server's GOAWAY, the next request on a new QUIC connection, no TCP raced, 3 of 3.
    - Its failure handling was never captured. An iPhone `h3-blackhole` and `h3-broken` run through devicectl replaces them.
  - **Not measured:**
    - Rows from any other scenario: their tests skip it (`missingWhy` in the e2e tests).
    - QUIC loss, key update, 0-RTT, dial delays and close on GOAWAY (no edge captures).
    - The proxy 407 flow (`connectCredentials`, `forwardCredentials` empty: `BrowserProxyAuth` is `ErrNotMeasured`). Credentials keep macOS Safari 27's placement, the iPhone's proxy requests being otherwise byte-identical.
- **Pinned gaps:**
  - The beacon on the credential-less connection (`partitionGaps`) and over HTTP/3 (`h3Gaps`).
  - `headers-order` steps with 26 or more page-set names and XHR's repeated name (kindsgen's `headersGaps`, `headersOrderGaps`).
  - Markup image priorities raised by layout (`redirectsLineGaps`).
  - Two `redirects2` samples whose Safari skipped the 25-redirect navigation it had just failed (`redirectsSampleGaps`).
  - One `h3-discovery` sample whose idle ran 69 s (h3gen's `sampleGaps`).
- **Checks:** realcheck does not run for it: it drives browsers on the Mac.

## safari-26-ios

- **Captured:** 2026-11-01, Safari 26.6.2 on an iPhone 12 mini, iOS 26.7.1, over Wi-Fi ([report](../../internal/harness/reports/refresh-safari-26-ios-2026-11-01.md); [decision 0051, amendment](../../docs/decisions/0051-ios-safari-over-the-network.md#amendment-2026-11-01-safari-26-on-the-iphone-and-the-failure-pair-measured)).
- **Column:** read as `safari-27-ios` is, against `safari-26-macos`'s column.
- **Own captures:** the iPhone's scenarios, now with `kinds-plain`, `h3-blackhole` and `h3-broken`.
- **Differs from safari-26-macos:**
  - **The platform:** `user-agent` is `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1`. `accept-encoding` adds `zstd`.
  - **Accept, Sec-Fetch-Site, Origin:** the iPhone's order, as Safari 27.2's on the iPhone (kindsgen).
  - **Plain HTTP:** the iPhone's order, as Safari 27.2's on the iPhone.
  - **WebSocket cookies:** a cross-site handshake carries only the partitioned cookie (`websocket`, `websocket-h2`, 3 of 3), as Safari 27.
    - macOS Safari 26 also sent the unpartitioned ones.
    - `cookies` is hand-written, so `websocketUnpartitioned` was taken out after the derive (W).
  - **QUIC:** no `0xff080808`. The ACK delay a constant 39–45 ms. The spin bit random. The first request of a full handshake with the Finished (`requestsAfterConfirmed` false).
  - **W from the iPhone's failure pair:**
    - `quic.keepAlive`: 2000 ms after the last packet received.
    - `quic.loss.pathDegrading`: close after 12 s, "keepalive limit reached".
    - `http3.closeOnGoaway`.
    - The fallback's mode, `abandonOnTcp` and `pending` checked against the copied rule.
    - h3gen widens its windows by 200 ms for the network path on these pages only.
- **Inherited / copied:**
  - `I: from safari-26-macos`: `cookies-edge`, `nav-redirect-{so,ss,cs}` and their twins, `set-cookie` and `slow-idle`.
  - Not inherited: `nav-redirect-http`, its twin and `cookies-edge2`. Their typed plain-HTTP navigation is macOS Safari 26's (Upgrade-Insecure-Requests, Accept, User-Agent). The iPhone's own `kinds-plain` and `proxy-plain` have User-Agent right after Host.
  - **C: from safari-26-macos:** the fallback's timing (`tcpDelayMs`, `reconnectMs`, `brokenAfter`, `brokenCountsResumed`), `quic.ack` and `quic.credit`. The phone's timing over Wi-Fi includes the path.
  - **Not measured:** the rest of QUIC loss, key update, 0-RTT, dial delays; the proxy 407 flow, as for `safari-27-ios`.
- **Pinned gaps:**
  - `headers-order` as for `safari-27-ios` (`headersGaps`, `headersOrderGaps`).
  - In `h3EdgeGaps`: the edge test's times against these samples (not compared), the blackholed attempt's second Initial pair and the 0-RTT reconnections.
  - One `h3-broken` connection's opening records (`openingGaps`).
  - One `redirects2` sample without the 25-redirect navigation (`redirectsSampleGaps`).

## chrome-157-ios

- **Captured:** 2026-11-01, Chrome 157.0.8103.24 on the iPhone 15 Pro, iOS 27.2, over Wi-Fi through devicectl ([decision 0053](../../docs/decisions/0053-chrome-on-ios.md)).
  - Chrome on iOS loads pages through WebKit and Apple's network stack.
- **Column:** read as `safari-27-ios` is, against `safari-27-macos`'s column.
- **Own captures:** all 23 iPhone scenarios, three samples each. `redirects2` and its twin with Chrome restarted before each sample.
- **Differs from safari-27-ios:**
  - **User agent:** `Mozilla/5.0 (iPhone; CPU iPhone OS 27_2_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/157.0.8103.24 Mobile/15E148 Safari/604.1` in every list.
    - The real iOS version, where Safari sends `18_7`.
    - No client hints.
  - **Header order, macOS Safari 27's** (kindsgen, 3 of 3):
    - CORS requests add Sec-Fetch-Site before Accept (`kinds`: 25 requests).
    - Plain HTTP puts Accept before User-Agent, and before Upgrade-Insecure-Requests on iframes (`kinds-plain`, as macOS Safari 27's).
    - Custom headers and Sec-Fetch-Mode where macOS Safari puts them (`headers-order`).
    - The HTTP/1.1 upload POST puts Sec-Fetch-Site before Origin (`http1`, `http1-upload`, 3 of 3), as the phone's own Safari does.
  - **W from the iPhone's failure pair,** as `safari-26-ios`:
    - `quic.keepAlive`: 2000 ms after the last packet received (samples 1 and 3; the ~2000 ms PING cadence of sample 2, pinned).
    - `quic.loss.pathDegrading` and `http3.closeOnGoaway`.
    - The fallback's mode, `abandonOnTcp` and `pending` checked against the copied rule.
    - `abandonOnTcp` holds for all 12 of 12 unanswered attempts. Chrome probes until the request that waited for the attempt goes on TCP, then sends nothing more.
    - In blackholed attempts that means one or two retransmitted Initial pairs 28–137 ms in, the last up to 8 ms after the TCP ClientHello. In `h3-broken`, up to four.
    - A resumed attempt's 0-RTT close goes out with the request ([0028, amendment](../../docs/decisions/0028-quic-failure-model.md#amendment-2026-11-03-what-counts-as-an-abandoned-attempt)).
- **The same as `safari-27-ios`** (**W**):
  - The ClientHello over TCP and QUIC.
  - HTTP/2 SETTINGS, window, HPACK and no HEADERS priority.
  - The QUIC transport parameters and HTTP/3 SETTINGS, and when it moves to HTTP/3.
  - The connections per host (credential-less requests on their own).
  - Cookies, redirect hops, resumption, WebSocket handshakes and frames.
  - The CONNECT head, HTTP/1.1 upload records (head apart, 3 of 3) and HTTP/2 upload records.
  - One difference: with the `http://` page of `proxy-plain` it sent no CONNECT to the capture host (Safari on the iPhone: four per sample).
- **Inherited / copied:**
  - `I: from safari-27-macos`: `cookies-edge`, `cookies-edge2`, `nav-redirect-{so,ss,cs,http}` and their twins, `set-cookie` and `slow-idle`. That is Safari of its WebKit generation, whose header order Chrome's own captures show (`internal/platform.Engine`).
  - **C: from safari-27-ios** (the profile it was derived from): the fallback's timing, `quic.ack` and `quic.credit`. Also the hand-written cookie, redirect, connection, WebSocket and proxy sections, each checked against Chrome's own captures where the iPhone set reaches.
  - **Not measured:** rows from any other scenario, as for `safari-27-ios`. Also as for `safari-27-ios`: the rest of QUIC loss, key update, 0-RTT, dial delays, the proxy 407 flow.
- **Pinned gaps:**
  - Three first connections whose navigation devicectl handed Chrome in the preface's millisecond (`openingGaps`).
  - One `h3-broken` sample's keep-alive (h3gen's `sampleGaps`).

## firefox-159-ios

- **Captured:** 2026-11-03, Firefox 159.1 on the same iPhone 15 Pro, iOS 27.2, over Wi-Fi through devicectl ([decision 0053](../../docs/decisions/0053-chrome-on-ios.md), amended).
  - Firefox on iOS loads pages through WebKit and Apple's network stack.
  - Its user agent names no WebKit (Safari's frozen `18_7`). That it is WebKit 27 is the capture's word (iOS 27.2).
  - `cmd/profilegen` holds it to that (`capturedOnIOS`) and checks it: its ClientHello, HTTP/2 section and QUIC transport parameters must be `safari-27-ios`'s.
- **Column:** read as `chrome-157-ios` is.
- **Own captures:** all 23 iPhone scenarios, three samples each.
  - `redirects2`, `redirects2-cookies` and `http1` were taken as three single-sample runs each and joined (docs/capturing.md).
  - The redirect pair with Firefox restarted before each run.
- **Differs from chrome-157-ios:**
  - **User agent:** `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/159.1 Mobile/15E148 Safari/604.1` in every list.
    - Safari's frozen iOS version, unlike Chrome's real one.
    - No client hints.
  - **Header order, macOS Safari 27's** (kindsgen, 3 of 3), Chrome's too.
    - Also in the POST and beacon requests, where Chrome on the iPhone puts Sec-Fetch-Site before Origin (`kinds`). Firefox puts Origin first, as macOS Safari 27 does.
    - Every kind's header lists match `safari-27-macos`'s but the font stylesheet's, which macOS's own samples vary in.
    - `kinds-plain` asked nothing (no insecure-form warning).
  - **W from the iPhone's failure pair,** as `chrome-157-ios`. 11 of 12 unanswered attempts abandoned once the request went on TCP. In the twelfth, no request fell back while it ran.
- **The same as `chrome-157-ios` and `safari-27-ios`** (**W**):
  - The ClientHello over TCP and QUIC (JA4 `t13d2013h2_a09f3c656075_7f0f34a4126d`).
  - HTTP/2 SETTINGS, window and HPACK (Akamai `2:0;3:100;4:2097152;9:1|10420225|0|m,s,a,p`).
  - The QUIC transport parameters and HTTP/3 SETTINGS.
  - Cookies, redirect hops, resumption, WebSocket handshakes and frames.
  - The CONNECT head and the HTTP/1.1 and HTTP/2 upload records.
  - `quic.ackDelay` is its own (h3gen).
- **Inherited / copied:**
  - The same 12 scenarios as `chrome-157-ios`, `I: from safari-27-macos`.
  - **C: from safari-27-ios**, as for `chrome-157-ios`.
- **Pinned gaps:**
  - Three first connections whose navigation devicectl handed Firefox in the preface's millisecond (`openingGaps`).
  - In h3gen's `sampleGaps`: one `h3-page` Initial probe (a 1038-byte unpadded datagram, 35 ms in; also `h3Gaps`).
  - In h3gen's `sampleGaps`: one `h3-broken` request that reached the server on TCP 1 ms before the QUIC attempt it started.
  - In the e2e tests, the gaps `chrome-157-ios` shares with the Safari profiles: macOS Safari's headers-order list, the iPhone's Initial probes and 0-RTT reconnections, the beacon.
