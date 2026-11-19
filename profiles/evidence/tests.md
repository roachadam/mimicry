# What checks each behaviour

Per concept page, the tests and captures behind it. Facts already in a row
above are linked, not repeated.

### Client certificates

Rows: `clientAuth` under [TLS ClientHello](tls.md#tls-clienthello); the gaps
are in [Not covered by any claim](gaps.md#not-covered-by-any-claim).

- `TestClientCertMatchesBrowser` replays the four `client-cert-*` pages
  through mimicry holding the run's identity and compares each
  connection's flight, certificates and CertificateVerify scheme, the
  connections per host carrying requests, offering a session, resumed
  and sending the certificate, and the connections dropped at the
  CertificateRequest and how they ended (Chrome's preconnect and
  Firefox's speculative connections pinned).
- `TestClientCertificate` (root package) checks what no capture shows:
  `ErrNotMeasured` without `tls.clientAuth` or for a P-384 key, the empty
  Certificate for a nil certificate, and a second host kept off a
  connection that sent a certificate or resumed a session that did,
  through a saved `TicketCache`.
- `TestClientCertificateSchemeNotAllowed` (`internal/handshake`):
  `ErrNotMeasured` for a CertificateRequest not allowing the browser's
  scheme. `TestQUICClientCertificateRequestStaysOnTCP`
  (`internal/engine/native`): a request over QUIC leaves the origin on
  TCP without counting a failure or asking the callback.
- `TestValidate` (`profile`) refuses PKCS#1 in TLS 1.3, two schemes for
  a key type, a scheme no client signs with, an empty `clientAuth` and
  `resume` without `tls.resumption`.

### Cookies

The edge-case rows (`knoeq`, `=x`, `__Http-` and `__Host-Http-`) are the
`nameless`, `emptyName` and `httpPrefix` rows under [Cookies](requests.md#cookies)
([decision 0012](../../docs/decisions/0012-cookie-rules-in-profiles.md)).
What the jar does not model is listed in
[Not covered by any claim](gaps.md#not-covered-by-any-claim). Safari's store-or-send
blocking and the end of Chrome's two-minute window are in the `thirdParty`
and `laxAllowsUnsafeSeconds` rows.

### CORS preflights

Rows: [CORS preflights](requests.md#cors-preflights) and [Redirects](requests.md#redirects).

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
  safelist, and which `Config.Header` a preflight carries), the expiry
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
with `Config.DisableCoalescing` and without),
`TestBodilessMatchesBrowser` (POST, PUT and PATCH without a body, over
HTTP/2 and HTTP/3), `TestURLsMatchBrowser` (the urls page's URLs: request
target, `Host` or `:authority` and SNI) and
`TestHPACKReproducesBrowserBlocks`.

The page replays of the kinds, headers-order, cors, cors-edge, redirects2,
nav-redirect, cookies, cookies-edge and h3 pages also plan every request
just before sending it and check the plan against what the server
received (`checkPlans`).

Outside the e2e suite: the resolver's rules by unit tests in the root's
`request_test.go`, `mimicry_test.go` and `preflight_test.go`; the URL
parser's edges (dot segments at the root, IPv4 past its limits, each
profile's `urls` rules) by `TestURLParserEdges` (`url_test.go`); the pool's by
`internal/pool`'s, with fake connections and a fake clock;
`Config.MaxConnsPerHost` by `internal/engine/native`'s `TestMaxConnsPerHost`,
with fake servers. Header placement by `TestHeaderPlacement`
(`placement_test.go`): each placed request against the same one unplaced,
over HTTP/1.1 and a raw HTTP/2 peer, and its error paths. Over HTTP/3 by
`TestHeaderPlacementOverHTTP3` in `internal/engine/native`, against a QUIC
server decoding the field sections.

ECH (the `realEch` rows): `TestECHMatchesBrowser` replays the `ech` page
with mimicry looking its hosts up in a DNS stub publishing the same
configs, and `TestECHH3MatchesBrowser` the `ech-h3` page (no QUIC, every
connection's ECH accepted). Outside the e2e suite, `TestSelectECH`
(`internal/handshake`) the configs and lists no capture reaches (unknown
KEMs and suites, other versions, mandatory extensions, lists that do not
parse), and `TestLookup` and `TestLookupSharesQuery` (`internal/httpsrr`)
the DNS client against fake nameservers: truncation, TTLs, negative and
failure caching, aliases and `Config.Resolver`'s `Dial`.

### HTTP/3

Rows: [QUIC and HTTP/3](http3.md#quic-and-http3),
[Failure, version negotiation, long transfers](http3.md#failure-version-negotiation-long-transfers)
and [Early data](http3.md#early-data). Each per-browser value of the QUIC
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
  the captures contradict fails. A page's markup requests go out as
  Chrome's parser sent them, each once the one before reached the server,
  and the server answers no QUIC client that appears meanwhile until they
  all have: Chrome's went before the QUIC connection the first started was
  up, so over HTTP/2, an order a loaded machine would otherwise lose. Each
  must reach the server within 150 ms, so a request that waits for QUIC
  still fails (`TestHTTP3MarkupMatchesBrowserWhenSlow` sends them 100 ms
  apart). Frame types are compared after the first flight, probes left
  out: whether a connection probes is when its server answered.
- `TestHTTP3EdgeMatchesBrowser` (`internal/harness/e2e/http3_edge_test.go`)
  walks the `h3-blackhole`, `h3-broken`, `h3-vn`, (with aioquic) `h3-v2`,
  `h3-early`, `h3-too-early`, `h3-resume-table` and `h3-streams-blocked`
  pages through mimicry on the browser's schedule. Per request it
  compares every connection it went on and how long it was held. On a
  connection that sent 0-RTT it compares each stream (in 0-RTT, held with
  or after the Finished, or sent again after a 425). Per host it compares the
  STREAMS_BLOCKED frames sent alone (count and limits) and those sent
  with a stream (limit and place: whole where the samples agree, as sets
  where they differ, `compareStreamsBlockedWith`); on
  `h3-streams-blocked`, each burst's waits for the stream limit, sorted,
  since the replay makes a burst's requests together
  (`compareBurstWaits`). Per QUIC attempt it
  compares versions, the server's answer, datagrams and give-up, and when
  the attempt and each TCP connection started after the request that
  caused them (the race), within the samples' range. A migration must
  send the browser's probes first and come 4 of the connection's own
  PTOs after the request, for mimicry and
  every browser sample, so it holds on a loaded machine too. Each page runs
  with `Config.BrowserQUICFallback`, and the pages where the modes can
  differ (`h3EdgeFast`: `h3-broken`, `h3-vn`, for a profile with a dial
  delay `h3-early` and `h3-too-early`, and with a fresh one `h3-blackhole`)
  with the fast default too. The
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
([report](../../internal/harness/reports/http3-edge-2026-10-25.md); captures
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
  gives up (5 s; Firefox on Windows 9.8 s). After a failed resumed attempt
  or a dead connection, a request at least `revalidateMs` later validates
  afresh beside TCP. `revalidateMs` (797–3,020 ms; Windows 47–5,675) is
  bounded by the captures: a request 0.8 s after a failure did not start
  a fresh attempt, one 3.05 s after did (on macOS).
- **Safari (`race`):** QUIC first, TCP after 100 ms (`tcpDelayMs`,
  88–129 ms measured: Safari 26 88–109, Safari 27 100–129). The attempt is
  dropped once the request goes on TCP (`abandonOnTcp`).

Failed attempts in a row (`brokenAfter`: Chrome and Firefox 1, Safari 2,
resumed ones counting for Safari only) leave the origin on TCP for the rest
of the Transport's life: no browser tried again within a page (Chrome's
5-minute back-off is from its source, not measured). An attempt counts
once its first datagram has gone out
([decision 0028](../../docs/decisions/0028-quic-failure-model.md)). Firefox
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

Rows: [Proxies](proxies.md#proxies). Idle tunnels per rotated identity are a
performance setting, not a profile claim; their memory cost is measured in
the [proxyidle report](../../internal/bench/reports/focus-2026-10-29/ctor/after/proxyidle-2026-10-29.md).

### WebSockets

Rows: [WebSockets](connections.md#websockets), the proxy rows under [Proxies](proxies.md#proxies)
(`connectNoResume`, `websocket.plain`, `websocket.credentials`) and the
WebSocket rows under
[Behaviour in code, not in profiles](../EVIDENCE.md#behaviour-in-code-not-in-profiles).
Capture reports: [websocket](../../internal/harness/reports/websocket-2026-10-24.md),
[proxy-websocket](../../internal/harness/reports/proxy-websocket-2026-10-26.md),
[websocket-deflate](../../internal/harness/reports/websocket-deflate-2026-10-26.md)
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
([decision 0039](../../docs/decisions/0039-proxy-credentials-up-front.md)).

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
[Chrome 157 report](../../internal/harness/reports/refresh-chrome-157-macos-2026-11-09.md);
the first full refresh on Windows the
[Chrome 156 on Windows report](../../internal/harness/reports/refresh-chrome-156-windows-2026-10-29.md)
(Firefox's: the
[Firefox 158 on Windows report](../../internal/harness/reports/refresh-firefox-158-windows-2026-11-01.md));
the first on an iPhone the
[Safari 27 on iOS report](../../internal/harness/reports/refresh-safari-27-ios-2026-11-01.md);
the first on Linux the
[Chrome 157 on Linux report](../../internal/harness/reports/refresh-chrome-157-linux-2026-11-05.md)
(Firefox's: the
[Firefox 159 on Linux report](../../internal/harness/reports/refresh-firefox-159-linux-2026-11-05.md));
the first Edge profile the
[Edge 157 on macOS report](../../internal/harness/reports/refresh-edge-157-macos-2026-11-06.md)
(on Windows: the
[Edge 157 on Windows report](../../internal/harness/reports/refresh-edge-157-windows-2026-11-06.md));
the first Brave profile the
[Brave 157 on macOS report](../../internal/harness/reports/refresh-brave-157-macos-2026-11-09.md)
(Windows: the
[Brave 157 on Windows report](../../internal/harness/reports/refresh-brave-157-windows-2026-11-09.md);
Linux: the
[Brave 157 on Linux report](../../internal/harness/reports/refresh-brave-157-linux-2026-11-09.md)).
