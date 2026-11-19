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
([report](../../internal/harness/reports/refresh-chrome-156-macos-2026-10-26.md)),
and `realcheck` showed none against the real Chrome 156.

**`chrome-157-macos`** (quick refresh, 2026-11-09, Chrome 157.0.8103.39 on
the second Mac, macOS 26.2,
[report](../../internal/harness/reports/refresh-chrome-157-macos-2026-11-09.md))
has no column of its own either. Read its class from `chrome-155-macos`'s
column. Rows whose evidence is a quick-refresh scenario (`page`, `http1`,
`kinds`, `kinds-http1`, `kinds-plain`, `websocket`, `websocket-h2`,
`h3-discovery`, `h3-page`, `hrr`, `client-hints`) are its own **W**: three
samples for `page`, `h3-discovery`, `h3-page`, `hrr` and `client-hints`,
one for the others. Every other row is inherited: `I: from chrome-156`
where Chrome 156 took the scenario itself (`resume`, `urls`, the cookie
twins, `bodiless`, `h2-upload`, `http1-upload`, `h3-resume-table`,
`h3-streams-blocked`, `client-cert-*`, `proxy-https*`), `I: from
chrome-155` for the rest. Nothing differs from `chrome-156-macos` beyond
the version tokens but:

- **W, client hints:** `fullVersion` 157.0.8103.39 and the second Mac's
  machine (macOS 26.2.0, arm, 16 GB, reduced motion and transparency,
  viewport 1200×1195), as `edge-157-macos`'s; Chrome 156's `client-hints`
  capture was taken on this Mac (macOS 27.0.1, 32 GB, 1200×953). `width`
  100, as Chrome 156's.
- **W, `quic.crypto.head`:** 56–86 (Chrome 156: 55–86), the range of
  quiche's random head length over the samples.
- **Pinned as Chrome 156's:** the server's Initial acknowledged in 2 of
  21 full handshakes and two PRIORITY_UPDATEs read 1 ms after their
  HEADERS (h3gen's `sampleGaps`), the client-hints third party's
  connection and `width` (`h3Gaps`); through the inherited captures the
  plain-HTTP page's link navigation (`proxyPlainGaps`) and the pooled
  host ending in a dot (`urlsKnown`), as Chrome 156's and 155's.

`realcheck` on the second Mac found no difference between Chrome 157 and
`chrome-157-macos` on the echo endpoints (tls.peet.ws, browserleaks' TLS
and HTTP/2 pages, impersonate.pro over HTTP/2 and HTTP/3).

**`chrome-156-windows`** (2026-10-29, Chrome 156.0.8078.80 on Windows 10,
[report](../../internal/harness/reports/refresh-chrome-156-windows-2026-10-29.md);
[decision 0047](../../docs/decisions/0047-windows-profiles.md)) has no column
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

**`firefox-158-windows`** (2026-11-01, Firefox 158.0.1 on Windows 10,
[report](../../internal/harness/reports/refresh-firefox-158-windows-2026-11-01.md);
[decision 0047, amendment](../../docs/decisions/0047-windows-profiles.md#amendment-2026-11-01-firefox-on-windows))
has no column of its own either. It inherits nothing: every scenario and
replay was captured on Windows, three samples each, so each row is its
own, with the class and value of `firefox-158-macos`'s column, and the
same tests keep it true. The ClientHello, the HTTP/2 preface and SETTINGS,
every header list and every kind are macOS Firefox's but for the
platform. Where Windows differs:

- **The platform:** `user-agent` says `Windows NT 10.0; Win64; x64` in
  every header list (the script kinds' variants come in another order,
  the same set).
- **W, `websocket.closeH1`:** `after_wait`, `closeWaitMs` 1000: Firefox
  closed the TCP connection of every HTTP/1.1 socket (`websocket`,
  `websocket-h2`'s `plain-so`, the three proxied pages) 1002–1095 ms after
  the close handshake, before the server's 3 s. macOS's captures recorded
  no transport end, so `firefox-158-macos` leaves it unmeasured.
- **W, from the edge captures (h3gen):** `revalidateMs` 47–5675 (macOS
  797–3020); `raceDelayMs` 3 (`firefox-158-macos` 6–9); no `resumedDialDelayMs` (it dialled
  at once; macOS 0–34 ms); `quic.loss.cachedRtt` false (the resumed
  attempt after the server's close first probed 1.7–1.9 s after its first
  flight, macOS's 3 ms in) and `quic.loss.resumed.afterMs` 9800 (macOS
  5000); `http3.firstDataFrame` 32228 (32224).
- **The unused opening connection:** a second TCP connection to the
  page's host 0–27 ms after the first, its HTTP/2 preface then GOAWAY, in
  131 of the 135 samples of pages opened directly over HTTPS (none
  through a proxy, none on macOS). Not modelled, as Chrome's preconnect is
  not (Not covered, below); the tests leave it out of the browser's pages
  (`unusedOpenings`). Its full handshake left Firefox one more TLS 1.2
  ticket, so on `resume-tls12` one more connection to the page's host
  resumed (5/3 in every sample, macOS and mimicry 5/2: `resumeCountGaps`).
- **Pinned as on macOS:** the XHR header order (`headersOrderGaps`), the
  credential-less connections (`partitionGaps`; 2 of 3 Windows samples),
  `www.redir-c.test`'s connections and the markup images' priority
  (`redirectsMerged`, `redirectsLineGaps`), the FontFace load (`h3Gaps`),
  the plain-HTTP page's script and link navigation (`proxyPlainGaps`),
  the resumed probes, the TCP connections beside a 425 and the
  STREAMS_BLOCKED per raise (`h3EdgeGaps`), the burst order
  (`h3EdgeSchedulingGaps`) and the fast fallback (`h3EdgeFastGaps`).
- **Pinned on Windows only:** one first Initial's packet number (242) in
  1 byte, 1 of 5 draws of 128 or more (`h3Gaps`); `/big`'s largest credit
  increment 3.56 MB, below mimicry's 4 MiB, which 2 of 3 macOS samples
  sent (`creditGaps`); the cross-site HTTP/2 socket's end
  (`webSocketEndGaps`); in h3gen's `sampleGaps`, one `h3-broken` sample
  whose Firefox opened 14 QUIC connections beside the page's and closed
  each within 5 ms (one `h3-download` sample did the same for 7
  requests), whose resumed attempt probed from a cached RTT and ended
  silently at 4.5 s with nothing held; the resumed give-up at 10.2 s in
  one sample (9.8 s in the other); a keep-alive PING 97 ms late; and one
  MAX_STREAM_DATA 2.4% above 3 MiB.
- **Timing only:** the pair's POST in `h3-early` and `h3-too-early` came
  4 ms after its GET, after the resumed handshake's Finished, and went in
  1-RTT (6 of 6; `h3EdgeSchedulingGaps`); the requests waiting on a
  silenced QUIC connection went to TCP 73–470 ms after the server's idle
  record (macOS 7–22 ms; h3gen times them from the client's own death);
  the page's timers fired late (`h3-broken`'s keep2.cs 4.9 s after
  silent5.so where the page waits 2 s, later ones 3.7–5.5 s late, every
  sample; macOS on time), so one connection's time after its trigger is
  pinned (`h3EdgeGaps`).
- **The recorder on Windows:** as for Chrome, its clock moves in steps of
  about 0.5 ms (h3gen finds a retry after Version Negotiation that shares
  the first attempt's millisecond).

`realcheck` on the Windows PC found no difference between Firefox and
`firefox-158-windows` on the echo endpoints that answered
(browserleaks' HTTP/2 page, impersonate.pro over HTTP/2 and HTTP/3: the
TLS and QUIC hellos, HTTP/2 and HTTP/3); tls.peet.ws refused connections
that day, and browserleaks' TLS page gave the browser no JSON.

**`firefox-159-macos`** and **`firefox-159-windows`** (2026-11-03,
Firefox 159.0.1, quick refreshes:
[macOS report](../../internal/harness/reports/refresh-firefox-159-macos-2026-11-03.md),
[Windows report](../../internal/harness/reports/refresh-firefox-159-windows-2026-11-03.md))
have no columns of their own. The fingerprint layer (`page`, `http1`,
`kinds*`, `websocket*`, `h3-discovery`, `h3-page`, `hrr`) was captured
again, so its rows keep `firefox-158-macos`'s and `firefox-158-windows`'s
class and value; every other scenario is inherited (**I**: from
firefox-158, its class). Nothing differs beyond the version tokens but:

- **W, `websocket.closeH1` on macOS:** `after_wait`, `closeWaitMs` 1000,
  as on Windows: Firefox 159 on macOS closed every HTTP/1.1 socket's
  connection 1.0 s after the close handshake (14 of 14), which
  `firefox-158-macos`'s older captures did not record. The cross-site
  HTTP/2 socket's end is pinned on macOS too (`webSocketEndGaps`).
- **W, `tls.helloRetry` on Windows:** `cookieAfter` 0x000d, macOS's,
  from Windows' first `hrr` capture (`firefox-158-windows` had none).
- **Side connections:** in 3 of 6 macOS and 4 of 6 Windows
  `h3-discovery`/`h3-page` samples with locally trusted roots, Firefox
  opened QUIC connections beside the page's, without a request, each
  closed 2–5 ms after its handshake (Not covered, below). The first
  resumed with 0-RTT and the NEW_TOKEN token; every later connection to
  that host in the sample started fresh (no ticket, no token), and a
  side connection sent 3 NEW_CONNECTION_IDs before closing, not 4. Token
  use follows resumption in all 81 macOS connections of 158 and 159, and
  the samples without side connections reuse both as 158 did, so
  `quic.tokens` and the NEW_CONNECTION_ID count stay as 158's; h3gen's
  `sampleGaps` pin the side connections (`tokens`, `newConnectionIDs`,
  `http3` where the control stream never arrived) and, in
  `h3-discovery`, the dial after the server's close
  (`raceAfterGoAway`: no TCP race in those samples) and kindsgen's the
  fetch that then went on a fresh QUIC connection without `alt-used`
  (`h3AltUsedGaps`).
- **W, `http3.discovery.raceDelayMs`:** macOS 6 (158: 6–9), Windows 2
  (158: 3), from the one `h3-discovery` sample per platform without side
  connections (the others raced no TCP: pinned, below).
- **Pinned as for 158:** the markup image's priority over HTTP/3 (2 of 3
  macOS samples, 1 of 3 Windows: kindsgen's `h3PriorityGaps`), the
  FontFace load (`h3Gaps`), `/big`'s largest credit increment (2.36 MB on
  macOS, 2.67 MB on Windows, below mimicry's 4 MiB: `creditGaps`), one
  first Initial's packet number in 1 byte (macOS 1, Windows 2: `h3Gaps`)
  and, on Windows, the credential-less connections (`partitionGaps`).

`realcheck` found no difference between Firefox 159 and either profile
on the echo endpoints (tls.peet.ws, browserleaks' TLS and HTTP/2 pages,
impersonate.pro over HTTP/2 and HTTP/3); on macOS browserleaks' HTTP/2
page showed one field on one side only.

**`chrome-157-linux`** (2026-11-05, Chrome 157.0.8103.38, Google's
`.deb`, in WSL2 on the Windows PC with Xvfb as its display,
[report](../../internal/harness/reports/refresh-chrome-157-linux-2026-11-05.md);
[decision 0058](../../docs/decisions/0058-linux-profiles.md)) has no column
of its own. It inherits nothing: every scenario and replay was captured on
Linux, three samples each, so each row is its own, with the class and
value of `chrome-155-macos`'s column, and the same tests keep it true.
Chrome on Fedora 45 sent the same default page in every field. Where
Linux differs:

- **The platform:** `user-agent` says `X11; Linux x86_64` and
  `sec-ch-ua-platform` `"Linux"` in every header list.
- **W, the client hints' machine:** `platformVersion` `""` (Chrome sends
  none on Linux), `arch` x86, the Xvfb screen (`dpr` 1, viewport 945×973,
  light scheme) and `deviceMemory` 16. `downlink` is 1.55, the centre of
  the 1.3–1.75 Mbps sent: Chrome's estimate rose by 30% or more within a
  sample, one host getting both ends, beyond its fixed per-host noise of
  ±10% (kindsgen's `downlinkGaps`).
- **W, from the edge captures (h3gen):** `tcpDelayMs` 302–303 (macOS
  305–306); `quic.crypto.addedFrames.min` 1 (2).
- **W, `http3.requestsAfterConfirmed` false:** its full handshakes'
  first requests read as waiting for the handshake to be confirmed, but
  16 of the 30 requests held out of 0-RTT left before it, in all six
  `h3-early` and `h3-too-early` samples, and h3gen now lets those decide.
  Chrome 155 and 156 on macOS are false the same way (19 of 31 held
  requests, six of six samples), where before the rule was not measured.
- **Pinned as on macOS and Windows:** NEW_TOKEN presented on some
  reconnections and the server's Initial acknowledged in 2 of 12 full
  handshakes and 1 of 10 resumed ones, that one also ACKing before its
  Finished (macOS 1 of 21 full; h3gen's `sampleGaps`), the unused preconnect
  (`connectionsPreconnects`), the XHR header order (`headersOrderGaps`),
  the error-page reload after hop 19 (`redirectsReload`), the plain-HTTP
  page's link navigation (`proxyPlainGaps`), the client-hints third
  party's connection and `width` (`h3Gaps`), the blackhole resend, the
  resumed probes and the idle origin's move to a new TCP connection after
  8.0 s, as on macOS (`h3EdgeGaps`), the fast fallback (`h3EdgeFastGaps`)
  and the burst and `h3-too-early` timing (`h3EdgeSchedulingGaps`).
- **Pinned on Linux only:** in h3gen's `sampleGaps`, one control stream
  after its Finished's datagram (1 of 12), one MAX_STREAM_DATA 2.07% above
  3 MiB and the after-raise STREAMS_BLOCKED pair sent twice in one sample.
  The `h3-page` sample whose page went on a resumed connection, filling
  the QPACK table in 0-RTT and setting the capacity again in 1-RTT, is
  `http3.qpack.resumed`'s rule now (decision 0060).

`realcheck` in WSL found no difference between Chrome and
`chrome-157-linux` on the echo endpoints (tls.peet.ws, browserleaks' TLS
and HTTP/2 pages, impersonate.pro over HTTP/2 and HTTP/3).

**`firefox-159-linux`** (2026-11-05, Firefox 159.0.1, Mozilla's build
from packages.mozilla.org, captured as Chrome was,
[report](../../internal/harness/reports/refresh-firefox-159-linux-2026-11-05.md);
[decision 0058](../../docs/decisions/0058-linux-profiles.md)) has no column
of its own. It inherits nothing; each row has the class and value of
`firefox-158-macos`'s column. Mozilla's Firefox on Fedora 45 sent the same
default page but for the ECH GREASE cipher suite, drawn per connection.
The ClientHello, the HTTP/2 preface and SETTINGS and every header list
are macOS Firefox 159's but for the platform. Where Linux differs:

- **The platform:** `user-agent` says `X11; Linux x86_64` in every
  header list.
- **W, from the edge captures (h3gen):** `raceDelayMs` 2–3 (macOS 6),
  `dialDelayMs` 0–6 (not measured on macOS), `resumedDialDelayMs` 0–4
  (0–34), `revalidateMs` 0–3000 (797–3020) and `http3.firstDataFrame`
  32238 (32224); `quic.loss.resumed.afterMs` 5000, as on macOS, from 3
  of the 6 `h3-broken` samples: the 2 with side connections ended their
  resumed attempt silently and sample 2 padded its close to 600 bytes,
  each pinned (below and under Side connections). A first
  three-sample capture's only closing attempt gave up at 6.1 s, so
  `h3-broken` was captured six times.
- **Side connections, as on macOS and Windows:** Firefox opened its
  burst of QUIC connections without a request in 1 of 3 `h3-discovery`
  samples, 2 of 3 `h3-page` and `h3-early`, 1 of 3 `h3-too-early` and 2
  of 6 `h3-broken` (15 each time), and in every `h3-streams-blocked`
  sample (6, each beside a fetch of the page). In those samples it sent
  no GET or HEAD in 0-RTT and its held requests after the Finished's
  datagram, raced no TCP after the server's close, and its resumed
  attempt ended silently with nothing held. Pinned in h3gen's
  `sampleGaps`, kindsgen's `h3AltUsedGaps` and the e2e tests'
  `h3EdgeGaps`; three first Initials' packet numbers in 1 byte, all on
  side connections (`h3Gaps`).
- **Pinned as on macOS and Windows:** the markup image's priority (3 of
  3: kindsgen's `h3PriorityGaps`, `redirectsLineGaps`), the FontFace load
  (`h3Gaps`), `/big`'s largest credit increment (3.16 MB:
  `creditGaps`), the credential-less connections (1 of 3:
  `partitionGaps`), the XHR header order (`headersOrderGaps`),
  `www.redir-c.test`'s connections (2 of 3: `redirectsMerged`), the
  plain-HTTP page's script and link navigation (`proxyPlainGaps`), the
  cross-site HTTP/2 socket's end (`webSocketEndGaps`), the client-hints
  third party's connection (`h3Gaps`), the resumed probes (`h3EdgeGaps`)
  and the spacing of requests made together (`h3EdgeSchedulingGaps`).
- **Pinned on Linux only (h3gen's `sampleGaps`):** the first
  MAX_STREAM_DATA 21% above 3 MiB in one `h3-download` sample (sent after
  more was read) and one 2.3% above; in two `h3-streams-blocked`
  samples, the second burst's two waiting streams opened in one packet
  behind one STREAMS_BLOCKED; one 0-RTT close padded to 600 bytes, not
  255 (1 of 10 closes on any platform); the one resumed connection of 14
  whose ACK left before its Finished (`h3-discovery` sample 1, QUIC
  connection 4: `resumedAckBeforeFinished`).

`realcheck` in WSL found no difference between Firefox and
`firefox-159-linux` on the echo endpoints; browserleaks' HTTP/2 page
showed Firefox's stream WINDOW_UPDATE on the browser's side only (on
macOS, on mimicry's).

**`edge-157-macos`** (2026-11-06, Edge 157.0.4341.42 on the second Mac,
macOS 26.2,
[report](../../internal/harness/reports/refresh-edge-157-macos-2026-11-06.md);
[decision 0059](../../docs/decisions/0059-edge-profiles.md)) has no column
of its own. It inherits nothing: every scenario and replay was captured
from Edge, three samples each, and its profile started as
`chrome-156-macos`'s, every generated section derived again and every
copied one checked against Edge's captures. Each row has the class and
value of `chrome-155-macos`'s column but where Edge differs:

- **The browser:** `user-agent` ends `Edg/157.0.0.0`, `sec-ch-ua` names
  `"Microsoft Edge"`, in every header list.
- **W, TLS:** no Trust Anchor IDs (`0xca34`) in any hello, so 16
  extensions where Chrome sends 17; everything else is Chrome's. JA4
  `t13d1516h2_8daaf6152771_806a8c22fdea` (Chrome 157's:
  `t13d1517h2_8daaf6152771_cb7bf5808d99`), as realcheck showed for the
  real Edge and mimicry alike.
- **W, `accept`:** no `image/jxl`, in navigations, frames and every image
  kind and variant.
- **W, client hints:** `fullVersion` 157.0.4341.42 and the new
  `chromiumFullVersion` 157.0.8103.39, the Chromium brand's version in
  `sec-ch-ua-full-version-list`; the second Mac's machine (macOS 26.2,
  arm, 16 GB, reduced motion and transparency); `width` 100.
- **W, `quic.packetNumbers`:** `shared_second_long`: Chrome's counter,
  but the second Initial's number in 2 bytes in 115 of 123 connections,
  token or not (Chrome: only without a token, 283 of 283); the 8 others,
  all with a token, are pinned (Not covered, below).
- **W, from the edge captures (h3gen):** `tcpDelayMs` 313–315 (Chrome on
  macOS 305–306); the server's Initial acknowledged in 4 of 12 full
  handshakes (pinned, as Chrome's).
- **Pinned as Chrome's:** NEW_TOKEN presented on some reconnections
  (h3gen's `sampleGaps`), the XHR header order (`headersOrderGaps`), the
  error-page reload after hop 19 (`redirectsReload`), markup images at
  `u=1` in 8 of 24 requests (`redirectsLineGaps`), the plain-HTTP page's
  link navigation (`proxyPlainGaps`), the client-hints third party's
  connection and `width` (`h3Gaps`), the blackhole resend 54.4 s in and
  the resumed probes (2 Initials, Chrome on macOS 4), the idle origin's
  moves to new TCP connections after 8.0 and 10.0 s (`h3EdgeGaps`), the
  fast fallback (`h3EdgeFastGaps`) and the burst order
  (`h3EdgeSchedulingGaps`).

`realcheck` on the second Mac found no difference between Edge and
`edge-157-macos` on the echo endpoints (tls.peet.ws, browserleaks' TLS
and HTTP/2 pages, impersonate.pro over HTTP/2 and HTTP/3); browserleaks'
HTTP/2 page also logged Edge's own `/favicon.ico` request.

**`edge-157-windows`** (2026-11-06, Edge 157.0.4341.42 on the Windows
PC, Windows 10,
[report](../../internal/harness/reports/refresh-edge-157-windows-2026-11-06.md);
[decision 0059](../../docs/decisions/0059-edge-profiles.md)) has no column of
its own either. It started from `edge-157-macos` and inherits nothing;
every scenario and replay was captured on Windows with the PC's Edge
policies lifted (they made Edge send `dnt: 1`; a first run was
discarded). Its rows are `edge-157-macos`'s: the same hello, JA4,
headers, client-hint rules and QUIC parameters. Where Windows differs:

- **The platform:** `user-agent` says `Windows NT 10.0; Win64; x64` and
  `sec-ch-ua-platform` `"Windows"`, in every header list.
- **W, the machine's client hints:** `platformVersion` 10.0.0, x86, `dpr`
  1, 32 GB, dark scheme, viewport 1241×1288.
- **W, from the edge captures (h3gen):** `tcpDelayMs` 303–313 (macOS
  313–315); 114 of 123 second Initials in 2 bytes (macOS 115), the 9
  others with a token.
- **Pinned on Windows only (h3gen's `sampleGaps`):** one `h3-blackhole`
  attempt whose first flight's two Initials went 2.1 ms apart, which the
  2 ms flight boundary split (`probes`); two MAX_STREAM_DATA 2.08% above
  3 MiB (`credit`); in the e2e tests, one `h3-broken` sample's move to a
  new path after 2.91 PTOs where the other two moved after 3.89 and 3.93
  (`pathMoveGaps`).

`realcheck` on the Windows PC, the policies lifted again for it, found no
difference between Edge and `edge-157-windows` on the echo endpoints;
browserleaks' HTTP/2 page logged Edge's own `/favicon.ico` request.

**`brave-157-macos`** (2026-11-09, Brave 1.99.55, reported as 157.1.99.55,
on the second Mac, macOS 26.2,
[report](../../internal/harness/reports/refresh-brave-157-macos-2026-11-09.md);
[decision 0067](../../docs/decisions/0067-brave-profiles.md)) has no column
of its own. It inherits nothing: every scenario and replay, and the four
`proxy-https*` ones, captured from Brave with its Shields at their
defaults, three samples each; its profile started as `chrome-156-macos`'s.
Each row has the class and value of `edge-157-macos`'s (the same hello,
JA4 `t13d1516h2_8daaf6152771_806a8c22fdea`, HTTP/2 preface, QUIC transport
parameters and `shared_second_long`) but where Brave differs:

- **The browser:** the user agent is Chrome's, word for word;
  `sec-ch-ua` names `"Brave"`; `sec-gpc: 1` after `accept` in every list.
- **W, `accept`:** the navigation's keeps `image/jxl` (Edge's has none)
  but has no `application/signed-exchange;v=b3;q=0.7`.
- **W, `context.acceptLanguageQ`:** 0.5 to 0.9, all five seen; kindsgen
  derives the set from every request of every capture as sent (CORS
  preflights excluded, which all carry the lists' `en-US,en;q=0.9`).
  `TestAcceptLanguageFarbling` checks the rule (one q per page origin,
  redirect hops keeping the first URL's) against the samples and
  mimicry's draws; every other test compares the captures unfarbled.
- **W, `cookies.thirdParty`:** `partition_all` (hand-written): no
  first-party cookie on a cross-site request (`cookies`: `c-part.cs`,
  `c-third.so`), every cookie, CORS, redirect and WebSocket scenario
  matching.
- **W, `context.blocksCrossSiteBeacons`:** the kinds pages' beacon to
  `cdn.kinds-b.test` absent in 9 of 9 samples, the same-site ones sent.
- **W, client hints:** only the platform version, arch, bitness, model,
  WoW64 and the full version list (every brand `157.0.0.0`, so
  `fullVersion` 157.0.0.0); the machine's other fields are the seed's and
  never sent.
- **W, from the edge captures (h3gen):** `tcpDelayMs` 3–5 (Chrome 305,
  Edge 313), read as a race since the 0-RTT attempt held its request to
  its give-up (`zeroRttHolds`); `pathDegrading.afterPtos` 4, as Chrome's
  and Edge's (after its second probe); 143 of 150 second Initials in 2 bytes, the 7 others with a
  token; dial delays for resumed attempts.
- **Pinned:** as Edge's (NEW_TOKEN reconnections, the XHR header order,
  the error-page reload, markup images' hops at `u=1` in 3 of 6 samples,
  the plain-HTTP page's link navigation, the client-hints third party's
  connection, the blackhole resend, the resumed probes, the idle origin's
  move to one new TCP connection, the fast fallback, the burst order);
  one second Initial in 1 byte; the one connection that acknowledged the
  server's Initial (1 of 21), numbered after the Handshake and 1-RTT
  packets it led (`coalescedOrder`); two `h3-broken` samples' path moves
  after 3.48 and 3.41 PTOs by the e2e measure (`pathMoveGaps`).

**`brave-157-windows`** (2026-11-09, Brave 1.99.55 on the Windows PC,
Windows 10, a per-user install,
[report](../../internal/harness/reports/refresh-brave-157-windows-2026-11-09.md))
started from `brave-157-macos` and inherits nothing. Its rows are
`brave-157-macos`'s but for the platform strings, the machine's client
hints, `tcpDelayMs` (305–313, as Chrome on Windows; macOS 3–5, Linux
2–5) and the dial delays (h3gen), and the pins: 138 of 150
second Initials in 2 bytes (12 with a token in 1); one `h3-page`
PRIORITY_UPDATE read 1 ms after its HEADERS and one `h3-streams-blocked`
sample with 2 more STREAMS_BLOCKED (h3gen's `sampleGaps`); the second
page's image at `u=1` in 2 of 3 `h3-discovery` samples (kindsgen's
`h3PriorityGaps`); one markup image's hop at `u=1` in 1 of 6
`redirects2` samples; one `h3-broken` sample's path move after 3.29
PTOs by the e2e measure; 3 of 5 HTTP/1.1 connections resumed on
`client-cert-ec` (`clientCertH1Resumes`); and the replay's scheduling on
`h3-resume-table`, `h3-too-early` and `h3-broken`
(`h3EdgeSchedulingGaps`).

**`brave-157-linux`** (2026-11-09, Brave 1.99.55 from Brave's apt
repository in WSL2, Ubuntu 24.04, on the Windows PC, on Xvfb,
[report](../../internal/harness/reports/refresh-brave-157-linux-2026-11-09.md))
started from `brave-157-macos` and inherits nothing. Its rows are
`brave-157-macos`'s but for the platform strings, the machine's client
hints (`x86`, an empty platform version, as Chrome on Linux), and from
the edge captures (h3gen): `tcpDelayMs` 2–5; no dial delays (every one
within the `?t=` clock's 3 ms); `pathDegrading.afterPtos` 4 (moves after
3.93–3.97 PTOs by the e2e measure, as Chrome and Edge); 148 of 156
second Initials in 2 bytes (8 with a token in 1). Pinned: the token
cache's reconnections; one `h3-discovery` PRIORITY_UPDATE read 1 ms after
its HEADERS; one download credit raise 2.01% above 3 MiB; `h3-broken`
sample 1, which opened a second TCP connection to `www.h3-b.test` 9.2 s
in, so its fresh attempt after the give-up came with no new TCP (h3gen's
`hold`; the e2e test logs the features its samples then disagree on);
markup images and their hops at `u=1` in 4 to 5 of 6 `redirects2`
samples; and the replay's scheduling on `h3-resume-table` and the fast
fallback.

**`safari-27-ios`** (2026-11-01, Safari 27.2 on an iPhone 15 Pro, iOS
27.2, over Wi-Fi,
[report](../../internal/harness/reports/refresh-safari-27-ios-2026-11-01.md);
[decision 0051](../../docs/decisions/0051-ios-safari-over-the-network.md)) has
no column of its own either. Rows whose evidence is one of the iPhone's
scenarios (`server.IOSScenarios`: `page`, `kinds`, `kinds-http1`,
`kinds-plain`, `cookies`, `cors`, `cors-edge` and its twin, `redirects2`
and its twin, `headers-order`, `http1`, `h3-discovery`, `h3-page`,
`resume`, `websocket`, `websocket-h2`, `proxy-connect`, `proxy-plain`;
three samples each, in the phone's own Safari but for the default page's
scenarios; `h3-blackhole` and `h3-broken` came after its run) are its
own, with the class of `safari-27-macos`'s column. Rows from
`cookies-edge`, `cookies-edge2`, `nav-redirect-{so,ss,cs,http}` and their
twins, `set-cookie` and `slow-idle` are `I: from safari-27-macos`. Rows
from any other scenario are not measured for it: their tests skip it
(`missingWhy` in the e2e tests). Where the iPhone differs:

- **The platform:** `user-agent` is `Mozilla/5.0 (iPhone; CPU iPhone OS
  18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko)
  Version/27.2 Mobile/15E148 Safari/604.1` in every list.
- **Accept, Sec-Fetch-Site, Origin:** added in that order in every
  request but preflights; macOS Safari 27.0.1 adds Sec-Fetch-Site first
  in GETs and Origin before Sec-Fetch-Site in same-origin requests with a
  body. Every kind's lists and variants are the iPhone's (kindsgen). The
  order is the Safari app's on iOS, not the platform's: Chrome on the same
  iPhone has macOS Safari's (`chrome-157-ios`, decision 0053).
- **Plain HTTP** (`kinds-plain`, taken on the iPhone after the first run,
  when its `proxy-plain` contradicted macOS's): User-Agent right after
  Host, or after Referer, in every subresource and fetch; macOS Safari 27
  puts it after Accept.
- **QUIC and HTTP/3:** every fresh hello ends its transport parameters
  with `0xff080808 = 9`; the QPACK encoder stream opens on the server's
  SETTINGS with only Set Dynamic Table Capacity (`uniStreams` open
  `settings`); QPACK Huffman is `shorter`; the first request of a full
  handshake goes with the Finished (`requestsAfterConfirmed` false).
- **Connections:** credential-less requests on connections of their own
  (`context.anonymousConnections`, as Chrome's).
- **C: from safari-27-macos:** `http3.discovery.fallback`,
  `quic.keepAlive`, `quic.ack` and `quic.credit`, kept by h3gen without
  edge captures for a profile sharing macOS's network stack
  (`internal/platform.Stack`). The iPhone's success path agrees (after the server's
  GOAWAY, the next request on a new QUIC connection, no TCP raced, 3 of
  3); its failure handling was never captured. An iPhone `h3-blackhole`
  and `h3-broken` run through devicectl replaces them.
- **Not measured:** QUIC loss, key update, 0-RTT, dial delays and close
  on GOAWAY (no edge captures); the proxy 407 flow
  (`connectCredentials`, `forwardCredentials` empty: `BrowserProxyAuth`
  is `ErrNotMeasured`); credentials keep macOS Safari 27's placement, the
  iPhone's proxy requests being otherwise byte-identical.
- **Pinned:** the beacon on the credential-less connection
  (`partitionGaps`) and over HTTP/3 (`h3Gaps`); `headers-order` steps
  with 26 or more page-set names and XHR's repeated name (kindsgen's
  `headersGaps`, `headersOrderGaps`); markup image priorities raised by
  layout (`redirectsLineGaps`); two `redirects2` samples whose Safari
  skipped the 25-redirect navigation it had just failed
  (`redirectsSampleGaps`); one `h3-discovery` sample whose idle ran 69 s
  (h3gen's `sampleGaps`).

realcheck does not run for it: it drives browsers on the Mac.

**`safari-26-ios`** (2026-11-01, Safari 26.6.2 on an iPhone 12 mini, iOS
26.7.1, over Wi-Fi,
[report](../../internal/harness/reports/refresh-safari-26-ios-2026-11-01.md);
[decision 0051, amendment](../../docs/decisions/0051-ios-safari-over-the-network.md#amendment-2026-11-01-safari-26-on-the-iphone-and-the-failure-pair-measured))
is read as `safari-27-ios` is, against `safari-26-macos`'s column: the
iPhone's scenarios, now with `kinds-plain`, `h3-blackhole` and
`h3-broken`, are its own; `cookies-edge`, `nav-redirect-{so,ss,cs}` and
their twins, `set-cookie` and `slow-idle` are `I: from safari-26-macos`.
`nav-redirect-http`, its twin and `cookies-edge2` are not inherited:
their typed plain-HTTP navigation is macOS Safari 26's (Upgrade-Insecure-Requests,
Accept, User-Agent), the iPhone's own `kinds-plain` and `proxy-plain`
have User-Agent right after Host. Where it differs from
`safari-26-macos`:

- **The platform:** `user-agent` is `Mozilla/5.0 (iPhone; CPU iPhone OS
  18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko)
  Version/26.6.2 Mobile/15E148 Safari/604.1`; `accept-encoding` adds
  `zstd`.
- **Accept, Sec-Fetch-Site, Origin:** the iPhone's order, as Safari 27.2's
  on the iPhone (kindsgen).
- **Plain HTTP:** the iPhone's order, as Safari 27.2's on the iPhone.
- **WebSocket cookies:** a cross-site handshake carries only the
  partitioned cookie (`websocket`, `websocket-h2`, 3 of 3), as Safari 27;
  macOS Safari 26 also sent the unpartitioned ones. `cookies` is
  hand-written, so `websocketUnpartitioned` was taken out after the
  derive (W).
- **QUIC:** no `0xff080808`; the ACK delay a constant 39–45 ms; the spin
  bit random; the first request of a full handshake with the Finished
  (`requestsAfterConfirmed` false).
- **W from the iPhone's failure pair:** `quic.keepAlive` (2000 ms after
  the last packet received), `quic.loss.pathDegrading` (close after 12 s,
  "keepalive limit reached"), `http3.closeOnGoaway`; the fallback's mode,
  `abandonOnTcp` and `pending` checked against the copied rule. h3gen
  widens its windows by 200 ms for the network path on these pages only.
- **C: from safari-26-macos:** the fallback's timing (`tcpDelayMs`,
  `reconnectMs`, `brokenAfter`, `brokenCountsResumed`), `quic.ack` and
  `quic.credit`: the phone's timing over Wi-Fi includes the path.
- **Not measured:** the rest of QUIC loss, key update, 0-RTT, dial delays;
  the proxy 407 flow, as for `safari-27-ios`.
- **Pinned:** `headers-order` as for `safari-27-ios` (`headersGaps`,
  `headersOrderGaps`); the edge test's times against these samples (not
  compared), the blackholed attempt's second Initial pair and the 0-RTT
  reconnections (`h3EdgeGaps`); one `h3-broken` connection's opening
  records (`openingGaps`); one `redirects2` sample without the
  25-redirect navigation (`redirectsSampleGaps`).

**`chrome-157-ios`** (2026-11-01, Chrome 157.0.8103.24 on the iPhone 15
Pro, iOS 27.2, over Wi-Fi through devicectl;
[decision 0053](../../docs/decisions/0053-chrome-on-ios.md)) is read as
`safari-27-ios` is, against `safari-27-macos`'s column. Chrome on iOS
loads pages through WebKit and Apple's network stack. All 23 iPhone
scenarios are its own (three samples each; `redirects2` and its twin
with Chrome restarted before each sample). `cookies-edge`,
`cookies-edge2`, `nav-redirect-{so,ss,cs,http}` and their twins,
`set-cookie` and `slow-idle` are `I: from safari-27-macos`: Safari of
its WebKit generation, whose header order Chrome's own captures show
(`internal/platform.Engine`). Rows from any other scenario are not
measured for it, as for `safari-27-ios`. Where it differs from
`safari-27-ios`:

- **User agent:** `Mozilla/5.0 (iPhone; CPU iPhone OS 27_2_0 like Mac OS
  X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/157.0.8103.24
  Mobile/15E148 Safari/604.1` in every list: the real iOS version, where
  Safari sends `18_7`. No client hints.
- **Header order, macOS Safari 27's** (kindsgen, 3 of 3): CORS requests
  add Sec-Fetch-Site before Accept (`kinds`: 25 requests); plain HTTP puts
  Accept before User-Agent, and before Upgrade-Insecure-Requests on
  iframes (`kinds-plain`, as macOS Safari 27's); custom headers and
  Sec-Fetch-Mode where macOS Safari puts them (`headers-order`). The
  HTTP/1.1 upload POST puts Sec-Fetch-Site before Origin (`http1`,
  `http1-upload`, 3 of 3), as the phone's own Safari does.
- **The same as `safari-27-ios`** (**W**): the ClientHello over TCP and
  QUIC, HTTP/2 SETTINGS, window, HPACK and no HEADERS priority, the QUIC
  transport parameters and HTTP/3 SETTINGS, when it moves to HTTP/3, the
  connections per host (credential-less requests on their own), cookies,
  redirect hops, resumption, WebSocket handshakes and frames, the CONNECT
  head, HTTP/1.1 upload records (head apart, 3 of 3) and HTTP/2 upload
  records. With the `http://` page of `proxy-plain` it sent no CONNECT to
  the capture host (Safari on the iPhone: four per sample).
- **W from the iPhone's failure pair**, as `safari-26-ios`:
  `quic.keepAlive` (2000 ms after the last packet received: samples 1
  and 3, and the ~2000 ms PING cadence of sample 2, pinned),
  `quic.loss.pathDegrading` and `http3.closeOnGoaway`; the fallback's mode,
  `abandonOnTcp` and `pending` checked against the copied rule.
  `abandonOnTcp` holds for all 12 of 12 unanswered attempts: Chrome
  probes until the request that waited for the attempt goes on TCP, then
  sends nothing more. In blackholed attempts that means one or two
  retransmitted Initial pairs 28–137 ms in, the last up to 8 ms after the
  TCP ClientHello; in `h3-broken`, up to four. A resumed attempt's 0-RTT
  close goes out with the request
  ([0028, amendment](../../docs/decisions/0028-quic-failure-model.md#amendment-2026-11-03-what-counts-as-an-abandoned-attempt)).
- **C: from safari-27-ios** (the profile it was derived from): the
  fallback's timing, `quic.ack` and `quic.credit`; the hand-written
  cookie, redirect, connection, WebSocket and proxy sections, each
  checked against Chrome's own captures where the iPhone set reaches.
- **Not measured:** as for `safari-27-ios`: the rest of QUIC loss, key
  update, 0-RTT, dial delays, the proxy 407 flow.
- **Pinned:** three first connections whose navigation devicectl handed
  Chrome in the preface's millisecond (`openingGaps`); one `h3-broken`
  sample's keep-alive (h3gen's `sampleGaps`).

**`firefox-159-ios`** (2026-11-03, Firefox 159.1 on the same iPhone 15
Pro, iOS 27.2, over Wi-Fi through devicectl;
[decision 0053](../../docs/decisions/0053-chrome-on-ios.md), amended) is read
as `chrome-157-ios` is. Firefox on iOS loads pages through WebKit and
Apple's network stack. All 23 iPhone scenarios are its own (three samples
each); `redirects2`, `redirects2-cookies` and `http1` were taken as three
single-sample runs each and joined (docs/capturing.md), the redirect pair
with Firefox restarted before each run. It inherits the same 12 scenarios
as `chrome-157-ios`, `I: from safari-27-macos`. Its user agent names no
WebKit (Safari's frozen `18_7`): that it is WebKit 27 is the capture's
word (iOS 27.2), which `cmd/profilegen` holds it to (`capturedOnIOS`) and
checks: its ClientHello, HTTP/2 section and QUIC transport parameters
must be `safari-27-ios`'s. Where it differs from `chrome-157-ios`:

- **User agent:** `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X)
  AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/159.1 Mobile/15E148
  Safari/604.1` in every list: Safari's frozen iOS version, unlike
  Chrome's real one. No client hints.
- **Header order, macOS Safari 27's** (kindsgen, 3 of 3), Chrome's too,
  and also in the POST and beacon requests where Chrome on the iPhone
  puts Sec-Fetch-Site before Origin (`kinds`): Firefox puts Origin first,
  as macOS Safari 27 does. Every kind's header lists match
  `safari-27-macos`'s but the font stylesheet's, which macOS's own
  samples vary in; `kinds-plain` asked nothing (no insecure-form
  warning).
- **The same as `chrome-157-ios` and `safari-27-ios`** (**W**): the
  ClientHello over TCP and QUIC (JA4
  `t13d2013h2_a09f3c656075_7f0f34a4126d`), HTTP/2 SETTINGS, window and
  HPACK (Akamai `2:0;3:100;4:2097152;9:1|10420225|0|m,s,a,p`), the QUIC
  transport parameters and HTTP/3 SETTINGS, cookies, redirect hops,
  resumption, WebSocket handshakes and frames, the CONNECT head and the
  HTTP/1.1 and HTTP/2 upload records; `quic.ackDelay` is its own
  (h3gen).
- **W from the iPhone's failure pair**, as `chrome-157-ios`; 11 of 12
  unanswered attempts abandoned once the request went on TCP. In the
  twelfth, no request fell back while it ran.
- **C: from safari-27-ios**, as for `chrome-157-ios`.
- **Pinned:** three first connections whose navigation devicectl handed
  Firefox in the preface's millisecond (`openingGaps`); in h3gen's
  `sampleGaps`, one `h3-page` Initial probe (a 1038-byte unpadded
  datagram, 35 ms in; also `h3Gaps`) and one `h3-broken` request that
  reached the server on TCP 1 ms before the QUIC attempt it started; in
  the e2e tests, the gaps `chrome-157-ios` shares with the Safari
  profiles (macOS Safari's headers-order list, the iPhone's Initial
  probes and 0-RTT reconnections, the beacon).
