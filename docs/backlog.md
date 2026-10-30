# Backlog: known gaps and deferred work

Every known way mimicry differs from the browsers it imitates, every
behaviour no capture has measured yet, and every open item from reviews,
in one place. By
[decision 0029](decisions/0029-scope-what-gets-modelled.md), a behaviour is
modelled only if an anti-bot vendor plausibly checks it or a user would
notice it (a stall, a failure, an error). Everything else waits here until
there is a reason to close it.

The ledger ([`profiles/EVIDENCE.md`](../profiles/EVIDENCE.md)) stays the
record of what the profiles claim and the evidence behind each claim. This
file is the to-do list. The pinned gap tables in the tests and generators
say which differences the tests tolerate; each one appears here too:
`sampleGaps` (h3gen and kindsgen), `partitionGaps`, `headersOrderGaps`,
`connectionsPreconnects`, `redirectsLineGaps`, `redirectsMerged`,
`redirectsReload`, `h3Gaps`, `h3EdgeGaps`, `h3EdgeSchedulingGaps`,
`h3EdgeFastGaps`, `h3PriorityGaps`, `raceCookieLabels` and
`missingCaptures`.

## How to read an entry

- **Differs:** what mimicry does, and what the browser does or might do.
- **Shows:** which request or situation puts it on the wire.
- **Detection:** how likely an anti-bot vendor is to see it and care:
  - high: on every connection or request, in data vendors are known to
    fingerprint (the TLS hello, the HTTP/2 preface, navigation headers);
  - medium: on common flows, and comparable against the claimed browser;
  - low: only in rare situations, or only in timing and failure paths;
  - none: not visible on the wire, or identical to the browser.
- **User-visible:** whether a caller would notice: an error, a stall, a
  wrong result.
- **Close:** the capture or change it needs, and a rough size (small: a
  day or less; medium: a few days; large: a week or more).
- **Source:** where the gap is recorded in this repository: a decision, a
  report, the ledger or the test that pins it.

Within each area, entries that are plausibly detection-relevant or
user-visible come first. When an item is closed, delete it here and update
its ledger row and pinned gap entry in the same commit.

## Where to start

These matter most under decision 0029:

1. [The TCP/IP fingerprint](#tls-and-below) is the host's, not the
   profile's.
2. [Partitioning connections by top-level site](#connections-and-pool)
   (all browsers). The workaround is one Transport per site.
3. [The page's own header order](#headers-and-request-kinds) for XHR.

## TLS and below

- **The TCP/IP fingerprint** (all browsers).
  - Differs: the SYN (TTL, window size, MSS, TCP options and their order)
    is the host operating system's. A profile claiming macOS, sent from
    Linux, has a Linux SYN; `chrome-156-windows` sent from a Mac has a
    macOS SYN (the systems' default TTLs alone differ, 128 against 64).
    Through a proxy, the server sees the proxy's SYN instead. No capture
    measured the SYN on either platform.
  - Shows: every direct connection.
  - Detection: high when the host OS differs from the profile's
    (p0f-style checks compare the SYN with the User-Agent); none when
    running on the profile's platform. User-visible: no.
  - Close: socket options through `net.Dialer.Control` for TTL and
    window (medium). An exact SYN needs raw sockets, which is out of
    scope. Running on the profile's platform, or through a proxy on it,
    is the practical fix.
  - Source: [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    ("TLS and below").
- **Real ECH** (Chrome, Firefox).
  - Differs: mimicry always sends GREASE ECH. A browser that finds an ECH
    config in the site's DNS HTTPS record sends real ECH instead.
  - Shows: sites that publish ECH configs (Cloudflare does by default).
  - Detection: medium; the front end can see GREASE where a browser would
    have encrypted the inner hello. User-visible: no.
  - Close: DNS HTTPS record lookups (shared with
    [DNS discovery of HTTP/3](#http3-and-quic)), a capture against an ECH
    server, and the inner-hello layout per browser; large.
  - Source: [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    ("ECH payloads").
- **Firefox's resumed GREASE ECH payload size** (Firefox).
  - Differs: the profile fixes the payload of a resumed hello at 400
    bytes (432 over QUIC), measured against the capture server's tickets.
    NSS probably sizes it from the ticket, so a server with other ticket
    sizes would see another length from Firefox.
  - Shows: every resumed Firefox connection to a real server.
  - Detection: low to medium; the length is in the hello, but nobody is
    known to compare it with the ticket size. User-visible: no.
  - Close: a `resume` capture against servers issuing tickets of several
    sizes (small), plus a profile rule if it varies (medium).
  - Source: [hello-record report](../internal/harness/reports/hello-record-2026-10-24.md);
    [EVIDENCE.md, TLS ClientHello](../profiles/EVIDENCE.md#tls-clienthello);
    [decision 0024](decisions/0024-clienthello-shape-in-profiles.md).
- **A WebSocket's full (not resumed) hello** (Chrome, Firefox).
  - Differs: the hello on a WebSocket's own HTTP/1.1 connection
    (`tls.websocketH1`) was captured only resumed. The full one is
    assumed to make the same changes to the main hello.
  - Shows: the first WebSocket to a host mimicry holds no ticket for,
    where the server does not allow HTTP/2 extended CONNECT.
  - Detection: low to medium; it is a ClientHello. User-visible: no.
  - Close: a `websocket` capture against a host with no earlier
    connection; small.
  - Source: [EVIDENCE.md, TLS ClientHello](../profiles/EVIDENCE.md#tls-clienthello)
    and Not covered (WebSockets).
- **HelloRetryRequest cookie position** (Chrome).
  - Differs: the uTLS fork (patch 2) puts the cookie extension of the
    second hello at a random unpinned position. BoringSSL places it by
    the hello's own permutation: a similar distribution, not the same
    mechanism.
  - Shows: only when a server answers with HelloRetryRequest and a
    cookie.
  - Detection: low; it needs a statistical look at second hellos.
    User-visible: no.
  - Close: the fingerprint plan picks the cookie's slot in its
    permutation, and a fork hook places it there; small to medium.
  - Source: [EVIDENCE.md, Behaviour in code](../profiles/EVIDENCE.md#behaviour-in-code-not-in-profiles);
    `internal/fingerprint/plan.go`.
- **Resumption forms never captured** (all).
  - Differs: 0-RTT over TCP TLS, several tickets in use at once, and
    session-ID resumption with a server that issues no tickets. mimicry
    uses one single-use ticket per pool key and never sends TCP 0-RTT.
  - Shows: servers that issue several tickets, or none.
  - Detection: low. User-visible: no.
  - Close: `resume` captures against those server behaviours; medium.
  - Source: [decision 0013](decisions/0013-tls-resumption.md) (Revisit
    when).
- **TLS 1.2 ticket lifetime** (Chrome, Firefox).
  - Differs: a TLS 1.2 ticket is used until its server certificate
    expires; the lifetime hint is ignored. How long browsers keep one is
    not measured.
  - Shows: a long session to a TLS 1.2-only server.
  - Detection: low. User-visible: no.
  - Close: a long-running `resume-tls12` capture; small, slow to run.
  - Source: [decision 0013](decisions/0013-tls-resumption.md);
    [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (Connections); `internal/handshake/tickets.go`.
- **The resumed hello's record version** (all).
  - Differs: assumed to be 0x0301, like the full hello, because uTLS
    writes both alike. Every browser capture agrees, but mimicry's own
    resumed hellos are not compared on it.
  - Shows: resumed connections.
  - Detection: none. User-visible: no.
  - Close: add the record version to the resumed-hello diff; small.
  - Source: [hello-record report](../internal/harness/reports/hello-record-2026-10-24.md).

## HTTP/1.1

- **Variants measured over HTTP/2 only are refused over HTTP/1.1** (all).
  - Differs: requests that only the `cookies`, `cors` and `cors-edge`
    captures show (the cross-site form POST navigation, PUT fetches, the
    redirected-image variants), and the XHR POST measured on `h3-page`,
    have no HTTP/1.1 header list. Sent on an HTTP/1.1 connection, they
    return `ErrNotMeasured`.
  - Shows: those requests to a server that speaks HTTP/1.1 only.
  - Detection: none (nothing is sent). User-visible: yes, an error.
  - Close: rerun those scenarios over HTTP/1.1 and regenerate; small to
    medium.
  - Source: [capturing.md, The generators](capturing.md#the-generators);
    [EVIDENCE.md, Request kinds](../profiles/EVIDENCE.md#request-kinds);
    `internal/h1/conn.go`.
- **HTTPS upgrades and HSTS** (Safari, Chrome; Firefox not measured).
  - Differs: mimicry sends an `http://` URL as given
    ([decision 0016](decisions/0016-literal-urls-no-https-upgrade.md)).
    Safari first tries TLS on the same host and port and falls back;
    Chrome's HTTPS-Upgrades try `https://` on 443 for port-80 URLs; a
    browser that has seen HSTS never sends the plain request. Whether
    Chrome and Firefox try TLS on a plain port is not known (the page
    trace cannot show refused hellos).
  - Shows: any `http://` request, mostly typed navigations.
  - Detection: low to medium; a server can see a plain request with no
    TLS attempt before it. Low in practice, because callers mostly use
    `https://` URLs. User-visible: no.
  - Close: captures on ports 80 and 443 per browser, keeping the server's
    refused hellos, then an opt-in `Config` option (decision 0016);
    medium.
  - Source: [plain-http report](../internal/harness/reports/plain-http-2026-10-24.md);
    [cookies-edge report](../internal/harness/reports/cookies-edge-2026-10-24.md);
    [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    ("By decision").
- **Where `Cookie` goes on a non-secure origin** (all).
  - Differs: no browser sent a cookie on the `kinds-plain` page, so where
    the header sits in plain-HTTP lists is not measured.
  - Shows: plain-HTTP requests that carry cookies.
  - Detection: medium for plain-HTTP targets (header order); few
    shopping sites serve plain HTTP. User-visible: no.
  - Close: a `kinds-plain` run with cookies set; small.
  - Source: [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (Headers).
- **Idle timeouts** (all).
  - Differs: an idle connection is closed after `Config.IdleConnTimeout`
    (90 s by default) on every protocol: HTTP/1.1 and HTTP/2 after their
    last request ended (Firefox profiles send GOAWAY first, as Firefox
    lets its HTTP/2 connection go once HTTP/3 is up; Chrome and Safari
    close alone), and HTTP/3 silently when QUIC's own idle timeout is
    longer (Safari's). A host with no connections is forgotten after the
    same time (including that it speaks HTTP/1.1 only). No capture shows
    a browser closing an idle connection (the longest gap captured is
    35 s), so the value and Chrome's and Safari's way of closing are not
    measured.
  - Shows: sessions with pauses longer than 90 s, against servers that
    keep idle connections longer.
  - Detection: low. User-visible: no.
  - Close: a long idle capture per browser, over HTTP/1.1 and HTTP/2
    (a `slow-idle`-style capture); small, slow to run.
  - Source: [decision 0040](decisions/0040-idle-connection-cleanup.md);
    [decision 0020](decisions/0020-connection-pool-package.md);
    [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (Connections).

## HTTP/2

- **Chrome 155's HTTP/2 opening is measured on Chrome for Testing**
  (Chrome 155).
  - Differs: possibly nothing. `http2.openingRecords` for chrome-155
    comes from Chrome for Testing 155.0.8059.84 (the branded Chrome on
    the capture machines is 156), which shares the branded build's network
    stack: `[preface SETTINGS WINDOW_UPDATE] [HEADERS]`, as branded
    Chrome 156. Every other chrome-155 field is from branded captures.
  - Shows: every new HTTP/2 connection (record count and sizes).
  - Detection: none expected; the record cut is the network stack's, the
    same in both builds. User-visible: no.
  - Close: a `resume` capture of a branded Chrome 155, should one be at
    hand; small.
  - Source: [EVIDENCE.md, HTTP/2 connection](../profiles/EVIDENCE.md#http2-connection)
    (`openingRecords`); [decision 0042](decisions/0042-http2-opening-records.md).
- **A connection opened ahead of its request** (Safari, Firefox).
  - Differs: when the browser opens a connection before it has the
    request (the tab's first connection for a URL opened from outside the
    page, Safari; Firefox likewise), it sends the opening alone, waits for
    the server's SETTINGS, acknowledges them, and only then sends HEADERS
    in a record of its own. mimicry always has the request when it
    connects and sends it at once, cut as `openingRecords` says (Safari
    27 and 26: in the opening's record).
  - Shows: a client's first connection to a site, where a browser
    preconnected (typed or opened URLs).
  - Detection: low to medium; the record pattern and the ACK-before-HEADERS
    order are visible, but a navigation from another page (joined, Safari
    26 `kinds` and `nav-redirect-page`, staged 2026-10-28) is as common.
    User-visible: no.
  - Close: a `Config` option to connect ahead of the first request
    (preconnect), with the browser's delay; medium. Chrome's extra
    connection per page is the same mechanism: see
    [Chrome's preconnect](#connections-and-pool).
  - Source: [EVIDENCE.md, HTTP/2 connection](../profiles/EVIDENCE.md#http2-connection)
    (`openingRecords`); [decision 0042](decisions/0042-http2-opening-records.md).
- **Chrome's re-prioritisation** (Chrome).
  - Differs: Chrome sends a PRIORITY frame while a page loads (raising a
    script from weight 182 to 219, in 2 of 3 samples); mimicry never
    does. Chained exclusive dependencies (`chainPriorities`) are compared
    with every Chrome trace (`TestChainedPrioritiesMatchBrowserTraces`);
    no captured request depends on a stream Chrome re-prioritised, whose
    place in Chromium's lists the PRIORITY frame changes. Also from
    Chromium's code, not captured: a stream whose response ended while its
    upload was still going stays in Chrome's chain until the upload's
    END_STREAM; mimicry ends the stream (and the upload) at the
    response's.
  - Shows: pages loading many subresources in parallel.
  - Detection: medium; priority frames are part of the Akamai HTTP/2
    fingerprint. User-visible: no.
  - Close: model `OnStreamUpdate` (the frame and the move in the lists)
    against the page traces; medium.
  - Source: [EVIDENCE.md, HTTP/2 connection](../profiles/EVIDENCE.md#http2-connection)
    and Not covered (HTTP/2).
- **Firefox answers the server's GOAWAY with its own** (Firefox).
  - Differs: Firefox answers a server's GOAWAY at once with a GOAWAY of
    its own (NO_ERROR); mimicry sends none.
  - Shows: whenever a server sends GOAWAY (graceful restarts, connection
    limits).
  - Detection: low to medium; visible at every graceful shutdown.
    User-visible: no.
  - Close: one profile flag, already captured (`h3-blackhole`, `h3-vn`);
    small.
  - Source: `h3EdgeGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go).
- **An upload of unknown length** (all).
  - Differs: a body of unknown length ends with an empty DATA frame
    carrying END_STREAM. Browsers set END_STREAM on the last data frame.
  - Shows: POST or PUT with a streamed body (`ContentLength` -1).
  - Detection: low to medium. User-visible: no.
  - Close: a capture of a streaming upload (fetch with a ReadableStream);
    small to medium.
  - Source: [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (HTTP/2); `internal/h2/conn.go`.
- **Chrome moves an idle origin to a new TCP connection** (Chrome on
  macOS).
  - Differs: once an origin's HTTP/3 broke, Chrome on macOS moved its
    requests to a new TCP connection after 6–10 s idle on the last one.
    mimicry keeps using its HTTP/2 connection, as Chrome 156 on Windows
    did in all 3 `h3-broken` samples. Why Chrome on macOS does this is not
    captured.
  - Shows: after an HTTP/3 failure, pauses of 6 s or more.
  - Detection: low. User-visible: no.
  - Close: captures to find the cause; medium.
  - Source: `h3EdgeGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go).
- **An HTTP/2 PING keep-alive** (considered, not added).
  - Differs: nothing; tls-client sets net/http2's `ReadIdleTimeout`,
    which PINGs an idle connection every interval to keep it open and
    detect a dead one. mimicry sends only the profile's idle PING
    (Chrome's, once after 10 s idle), as captured; periodic PINGs on an
    idle connection are wire traffic no browser sends in that pattern.
  - Shows: would show to servers counting PINGs on idle connections.
  - Detection: medium if added. User-visible: a dead idle connection is
    found by the next request on it, not before (see the next entry).
  - Close: add only if a browser is captured doing it.
  - Source: [decision 0040](decisions/0040-idle-connection-cleanup.md)
    (Amendment).
- **Dead connections without a health check** (Firefox, Safari).
  - Differs: as measured, these browsers send no PING, so a connection
    that died silently is noticed only when a request on it fails or
    times out. mimicry does the same. Only Chrome pings after 10 s idle.
  - Shows: a network path that drops without a reset.
  - Detection: none (it is the browser's behaviour). User-visible: yes,
    a request stalls until its context ends.
  - Close: nothing to capture. `NewClient`'s 30 s `Config.Timeout`
    bounds it; a Transport used directly needs a request deadline. A
    periodic PING keep-alive is not added (previous entry).
  - Source: [EVIDENCE.md, HTTP/2 connection](../profiles/EVIDENCE.md#http2-connection)
    (`idlePingMs`).
- **Upload DATA chunking after the first 64 KiB** (all; Firefox
  entirely).
  - Differs: bodies are read in 65535-byte chunks. That matches Chrome's
    and Safari's first chunk; their later chunks, and all of Firefox's
    (1576, 16000, 14720, …), follow their internal pipes. Only the frame
    size cap is checked.
  - Shows: uploads over 64 KiB, and every Firefox upload.
  - Detection: low. User-visible: no.
  - Close: upload captures of several sizes, and a chunking model;
    medium.
  - Source: [EVIDENCE.md, Behaviour in code](../profiles/EVIDENCE.md#behaviour-in-code-not-in-profiles).
- **Credit return timing** (Firefox, Safari).
  - Differs: mimicry returns credit at the profile's threshold. Firefox's
    stream credit follows how fast the page reads (16 KiB to 4 MiB);
    Safari's connection credit overshoots half the window by up to
    1.2 MB. Increment sizes are held to the browsers' range; their timing
    is not modelled.
  - Shows: large downloads.
  - Detection: low. User-visible: no.
  - Close: model read-paced credit from a download capture; medium.
  - Source: [EVIDENCE.md, HTTP/2 connection](../profiles/EVIDENCE.md#http2-connection).
- **REFUSED_STREAM** (Chrome).
  - Differs: mimicry stops using a connection on REFUSED_STREAM and sends
    the request on another. Chrome retries on the same session
    ([INFERENCE], from its source).
  - Shows: only when a server refuses streams.
  - Detection: low. User-visible: no (one extra connection).
  - Close: a scenario that refuses streams; small.
  - Source: [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (HTTP/2).
- **Error-path frame order** (Chrome).
  - Differs: on errors Chrome sends RST_STREAM before the credit for
    discarded bytes, and GOAWAY before closing ([INFERENCE], from
    Chromium's source; never captured). mimicry's order on error paths is
    not checked.
  - Shows: error paths only.
  - Detection: low. User-visible: no.
  - Close: an error-injection scenario; medium.
  - Source: [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (HTTP/2).

## HTTP/3 and QUIC

- **Discovery through DNS HTTPS records** (all).
  - Differs: mimicry learns that an origin speaks HTTP/3 only from
    `Alt-Svc`. Browsers also read DNS HTTPS records and can use HTTP/3 on
    the first request. The capture DNS stub does not answer them, so this
    is not measured.
  - Shows: the first visit to a site that publishes HTTPS records
    (Cloudflare does).
  - Detection: medium; the first request's protocol differs from the
    browser's on the largest CDNs. User-visible: no.
  - Close: HTTPS records in the DNS stub, captures, and a resolver that
    asks for them (shared with [real ECH](#tls-and-below)); medium to
    large.
  - Source: [concepts/http3.md, Not supported](concepts/http3.md#not-supported);
    [http3 report](../internal/harness/reports/http3-2026-10-25.md);
    [decision 0026](decisions/0026-http3-capture.md).
- **A returning browser's first request** (all; Firefox most).
  - Differs: with `Config.AltSvc`, a new Transport that finds an origin's
    alternative starts it as the profile starts any origin whose
    alternative it knows (Chrome and Safari: QUIC first, TCP after the
    race delay; Firefox: TCP, validating QUIC beside it). No browser was
    captured restarting with a saved alternative. Firefox's source (a
    lead) marks saved alternatives validated and sends the first request
    on HTTP/3 directly; Chrome's delays its TCP job by 1.5 × a saved
    smoothed RTT (see Chrome's TCP delay from the smoothed RTT, below).
    Broken alternatives (Chrome saves them too) are not shared or saved.
  - Shows: a returning visit through a shared or loaded cache.
  - Detection: low; Firefox's first request goes on TCP where the browser
    may use HTTP/3. User-visible: Firefox's first page loads on TCP.
  - Close: captures of each browser restarted with a saved alternative
    (a profile directory kept between two runs); medium.
  - Source: [concepts/http3.md](concepts/http3.md#sharing-what-a-transport-learned).
- **Dead-path waits** (Firefox, Safari).
  - Differs: nothing; listed as a stall. A request pending on a live QUIC
    connection whose path stops answering waits as the browser's does:
    Firefox until its 30 s idle timeout, Safari 12 s, also with the fast
    fallback default ([decision 0036](decisions/0036-fast-quic-fallback.md)),
    which does not shorten them: the request was sent, and sending it on
    TCP early would duplicate it where the path recovers.
  - Shows: a network that drops UDP mid-connection.
  - Detection: none. User-visible: a stall of up to 30 s.
  - Close: a dead-path signal for Firefox (no acknowledgement for N
    PTOs, as Chrome's `pathDegrading`) and a rule for when to resend;
    medium. Firefox's migration is the other half: see Migration by
    Firefox and Safari, below.
  - Source: [concepts/http3.md, If QUIC is blocked](concepts/http3.md#if-quic-is-blocked).
- **Safari over HTTP/3** (Safari 27, 26).
  - Differs: only Safari's `fetch()` was measured over HTTP/3, so its
    other kinds (navigations included) stay on TCP where Safari would use
    HTTP/3. Also not measured: its QPACK encoder with a dynamic table,
    PRIORITY_UPDATE, request bodies (`maxDataFrame`), its close reply,
    and whether it ever moves a live HTTP/2 connection to HTTP/3.
  - Shows: every Safari visit to an HTTP/3 origin beyond `fetch()`.
  - Detection: medium; Safari's page loads look like TCP-only clients.
    User-visible: no.
  - Close: an `h3-page` capture where Safari uses QUIC for more kinds,
    against a server offering a QPACK table; medium. Its unmeasured loss
    and close values are listed below (Safari's handshake loss timing,
    Safari's other unmeasured QUIC values).
  - Source: [http3 report](../internal/harness/reports/http3-2026-10-25.md);
    [EVIDENCE.md, QUIC and HTTP/3](../profiles/EVIDENCE.md#quic-and-http3).
- **Safari's QUIC resumption and 0-RTT** (Safari 27, 26).
  - Differs: Safari resumes QUIC with 0-RTT after closing a connection
    itself (`h3-broken`: it replays the held requests in 0-RTT) and after
    one idled out (`h3-early`, `h3-too-early`). In 0-RTT it sends GET,
    HEAD and OPTIONS and holds every other method until the handshake is
    confirmed (0.5–2.3 ms after its Finished, with HANDSHAKE_DONE), and a
    request answered `425 Too Early` goes again on the same connection:
    Chrome's `http3.earlyData` rule but for when a held request leaves
    (36 held and 24 early requests, 12 retries, per version). Its profile
    has no `tls.resumption` and no `http3.earlyData`, so mimicry's
    reconnections are fresh, in 1-RTT, with no 0-RTT close when such an
    attempt is dropped. Safari also starts each resumed connection 5–11 ms
    after the request that needs it (one 22 ms), mimicry's at once, so of
    a GET and a POST made together Safari's connection follows both and
    mimicry's may start before the POST is made.
  - Shows: Safari reconnecting to an HTTP/3 origin.
  - Detection: low to medium; a returning Safari's hello lacks the
    pre-shared key. User-visible: no.
  - Close: derive Safari's resumption rule and its `http3.earlyData`
    (held until confirmed: a third way a held request leaves) from
    `h3-broken` and the h3-early pages, already captured; medium.
  - Source: `h3EdgeGaps` and `h3EdgeSchedulingGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go);
    [h3-early report](../internal/harness/reports/h3-early-2026-10-26.md);
    [decision 0028](decisions/0028-quic-failure-model.md);
    [concepts/http3.md](concepts/http3.md).
- **Firefox's declined TCP connections beside a 425 retry** (Firefox).
  - Differs: on 3–4 of the 4 requests answered `425 Too Early` per
    sample, Firefox also opened a TCP connection to the origin and closed
    it without a request, while the retry went on the QUIC connection.
    Which retries get one varies between samples; mimicry opens none.
  - Shows: a server answering 425 to Firefox's 0-RTT requests.
  - Detection: low (rare server behaviour). User-visible: no.
  - Close: more samples to find the rule (Firefox's connection
    management on a 425); medium.
  - Source: `h3EdgeGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go);
    [h3-early report](../internal/harness/reports/h3-early-2026-10-26.md).
- **When Firefox's held request leaves** (Firefox).
  - Differs: a request Firefox holds for the handshake left in the
    datagram with its Finished in 31 of the 35 made before the Finished;
    4 left 0.2–18.6 ms after it. mimicry's always rides with the Finished
    (`heldWithFinished`).
  - Shows: a POST (or other unsafe method) as the first request on a
    resumed HTTP/3 connection.
  - Detection: low (packet layout). User-visible: no.
  - Close: nothing likely; it is necko handing the request to neqo
    against neqo writing the Finished (thread timing).
  - Source: `sampleGaps` in `internal/harness/cmd/h3gen/load.go`;
    [h3-early report](../internal/harness/reports/h3-early-2026-10-26.md).
- **Safari's OPTIONS fetch** (Safari 27, 26).
  - Differs: Safari's kinds captures have no `fetch()` with method
    OPTIONS, so its profile has none and mimicry refuses one
    (`ErrNotMeasured`). Safari sent it on the `h3-early` page, over
    HTTP/3.
  - Shows: a page's `fetch()` with method OPTIONS.
  - Detection: low (rare). User-visible: yes, `ErrNotMeasured`.
  - Close: an OPTIONS fetch in the kinds page, or kindsgen reading the
    h3-early pages; small.
  - Source: `h3EdgeGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go).
- **A response that stops mid-stream on a live HTTP/3 connection** (all;
  seen with Chrome's profile against nginx).
  - Differs: unknown whether a browser does the same. The server (nginx
    1.31.8, OpenSSL 3.5.7) sent part of a response and then nothing for
    60+ s while acknowledging the waiting request's keep-alive PINGs
    (`quic.Conn.KeepAlive`), so the connection never idles out. The
    client had received every server packet, had its own acknowledged
    and no PTO armed: the server stopped sending. Why is not established.
  - Shows: benchmark layer 6 (rotation, HTTP/3 through SOCKS5, dropped
    clients) at 86462a1: once in the first run; then 0 in 10 points at
    145b90b, and with the client on 1 CPU in 3 of 30 points (one request
    in about 87,000 each). Not seen against quic-go under 2–15% loss.
  - Detection: none. User-visible: yes, a request without a deadline
    hangs; `NewClient`'s client times out after 30 s by default
    (`Config.Timeout`).
  - Close: capture both sides of a stuck connection (a client qlog and
    nginx's debug log, or a decrypted capture at the proxy) from the
    1-CPU repro; medium.
  - Source: [multiclient-rotation report](../internal/bench/reports/multiclient-rotation-2026-10-28.md)
    (Layer 6, "Errors, protocol and load": routines stuck a minute after
    the window).
- **nginx drops Chrome's port migration** (Chrome).
  - Differs: nothing; mimicry migrates as Chrome does. When a path counts
    as degrading (4 PTOs without an ACK), the connection moves to a new
    port; through a SOCKS5 proxy that means a new association. The
    benchmark's nginx (8 workers, `reuseport`, no `quic_bpf`) answers no
    packet on the new path. Requests on the connection wait for the 30 s
    idle timeout, then go on TCP (`Pending: tcp`). Migration was forced
    after a connection's third response: 100 of 100 went silent, directly
    and through the proxy. Against quic-go every one worked (6,871
    through the test SOCKS5 relay, at most 47 ms per request).
    [INFERENCE] Chrome's zero-length client connection ID leaves nginx
    no new one for the path, or reuseport sends the new path's packets
    to another worker.
  - Shows: a path that stops acknowledging for 4 PTOs, a loaded client
    process included, against such an nginx.
  - Detection: none. User-visible: yes, a 30 s stall per migration (the
    browser's own wait there was 54–61 s: see Chrome's blackhole
    detection, below).
  - Close: a capture of Chrome migrating against nginx; nothing in
    mimicry unless the browser is seen to behave differently. Small.
  - Source: the benchmark's HTTP/3 rows through SOCKS5 (no report
    records the forced-migration runs).
- **nginx refuses about 1 in 6,000 Chrome handshakes: "missing transport
  parameters"** (Chrome).
  - Differs: unknown. In about 23,600 Chrome-profile handshakes through
    the benchmark's SOCKS5 proxy at 145b90b, nginx closed 4 with
    CRYPTO_ERROR 0x16d ("missing transport parameters") on the first
    round trip, 40–47 ms in. Every hello carries
    `quic_transport_parameters` (`startClientTLS` sets it before
    `Start`), so the cause is not found: possibly how nginx reassembles
    Chrome's shuffled, split CRYPTO frames.
  - Shows: new QUIC connections to nginx.
  - Detection: low. User-visible: yes, rarely. With `BrokenAfter` 1 the
    origin is broken for the Transport, and its requests stay on TCP.
  - Close: capture a failing first flight (decrypted at the proxy) and
    replay it against nginx; small to medium.
  - Source: the benchmark's HTTP/3 rows through SOCKS5 (no report
    records it).
- **Chrome's blackhole detection** (Chrome).
  - Differs: when the path Chrome migrated to dies too, Chrome closes
    with `85:Network blackhole detected` after 54–61 s (on Windows too,
    60.5 s after the connection started in every sample) and sends the
    stuck request again on a new TCP connection. mimicry waits for QUIC's
    30 s idle timeout, then sends it on TCP (`pending: tcp`), without
    Chrome's close.
  - Shows: a server or path that goes silent mid-connection (see nginx
    drops Chrome's port migration, above).
  - Detection: low (failure path). User-visible: no; mimicry recovers
    sooner than Chrome.
  - Close: model the close and its timing from `h3-broken`, already
    captured; medium.
  - Source: `h3EdgeGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go);
    [decision 0028](decisions/0028-quic-failure-model.md).
- **How long an origin stays broken** (all; Chrome by source).
  - Differs: after its failed attempts, an origin stays on TCP for the
    Transport's life. Chrome's source backs off for 5 minutes; no capture
    covers more than one page. What Safari does after more than two
    failures is not measured either.
  - Shows: long sessions after one QUIC failure.
  - Detection: low to medium; a client that never retries HTTP/3 for
    hours. User-visible: no.
  - Close: a capture spanning several pages and minutes; small, slow to
    run.
  - Source: [concepts/http3.md](concepts/http3.md#if-quic-is-blocked);
    [http3-edge report](../internal/harness/reports/http3-edge-2026-10-25.md);
    [decision 0028](decisions/0028-quic-failure-model.md).
- **Captures with locally trusted roots** (Chrome, Firefox).
  - Differs: Chrome and Firefox refuse QUIC to a locally trusted CA, so
    their h3 captures were taken with QUIC forced (`-h3-local-roots`).
    With a built-in root, switching and racing may differ.
  - Shows: all HTTP/3 behaviour of those profiles.
  - Detection: low to medium (unknown). User-visible: no.
  - Close: a capture against a publicly trusted certificate; medium.
  - Source: [http3 report](../internal/harness/reports/http3-2026-10-25.md);
    [EVIDENCE.md, QUIC and HTTP/3](../profiles/EVIDENCE.md#quic-and-http3).
- **HTTP/3 header lists not captured with cookies** (all).
  - Differs: no cookie went over HTTP/3 in the captures, so the `cookie`
    slot in every browser's HTTP/3 lists, and Firefox's `alt-used` place
    relative to it, are assumed. The XHR POST over HTTP/3 was measured on
    `h3-page` without cookies (Chrome, Firefox); Safari's is its HTTP/2 list.
  - Shows: credentialed requests over HTTP/3, which is most of them on a
    shop.
  - Detection: medium; header order. User-visible: no.
  - Close: `h3-page` with cookies set and more kinds; medium.
  - Source: [EVIDENCE.md, QUIC and HTTP/3](../profiles/EVIDENCE.md#quic-and-http3).
- **Firefox's `alt-used` on credential-less requests** (Firefox).
  - Differs: Firefox's source adds `alt-used` only to requests that are
    not anonymous and not private browsing (`nsHttpChannel::BeginConnect`:
    `!(mLoadFlags & LOAD_ANONYMOUS) && !mPrivateBrowsing`); no capture
    sent a credential-less request over HTTP/3, so whether mimicry's
    credential-less kinds over HTTP/3 carry it was never compared.
  - Shows: `fetch()` with `credentials: "omit"`, crossorigin anonymous
    elements, over HTTP/3.
  - Detection: low; one header. User-visible: no.
  - Close: those kinds in `h3-page`; small.
  - Source: [realcheck report](../internal/harness/reports/realcheck-2026-10-25.md).
- **Safari's handshake loss timing** (Safari).
  - Differs: Safari's first probe was never seen (TCP always won), so its
    profile uses the QUIC stack's default 333 ms initial RTT: probes at
    +1 s, +2 s, +4 s. Under 30 % loss in interop runs, 1–3 of 20
    connections hit the 10 s handshake timeout, and two runs hit the 30 s
    per-connection deadline.
  - Shows: lossy networks.
  - Detection: low. User-visible: yes, slow or failed handshakes under
    heavy loss (TCP takes over for the request).
  - Close: a Safari capture with QUIC blackholed and no TCP alternative;
    medium. Check the two 30 s runs' traces with the key log first
    (small).
  - Source: [quic-interop report](../internal/harness/reports/quic-interop-2026-10-25.md);
    `internal/quic/client_loss.go` (`initialPTO`).
- **Firefox's v2 resumption against older lsquic** (Firefox).
  - Differs: on www.litespeedtech.com a resumed v2 connection fails:
    lsquic sends a Retry with v1 type bits, mimicry never sees it, and the
    connection times out. It is lsquic's bug, but what real Firefox does
    there is not captured.
  - Shows: Firefox resuming in v2 against old lsquic servers.
  - Detection: low. User-visible: yes, a stall on that origin until TCP
    takes over.
  - Close: capture real Firefox against such a server; small to medium.
  - Source: [quic-interop report](../internal/harness/reports/quic-interop-2026-10-25.md).
- **The error of a 0-RTT request whose handshake never finishes** (all).
  - Differs: the request fails after 10 s with `read from closed stream`
    instead of the cause (the connection was lost).
  - Shows: heavy loss.
  - Detection: none. User-visible: yes, a confusing error.
  - Close: map the error in `internal/h3`; small.
  - Source: [quic-interop report](../internal/harness/reports/quic-interop-2026-10-25.md).
- **An unexplained close by www.akamai.com** (Safari 26).
  - Differs: once, the server closed the second probe connection right
    after its handshake. Not reproduced.
  - Detection: unknown. User-visible: yes, once.
  - Close: rerun `interop -public`; small.
  - Source: [quic-interop report](../internal/harness/reports/quic-interop-2026-10-25.md).
- **Server bugs real browsers hit too** (Chrome).
  - Differs: nothing; mimicry does what Chrome does. lsquic before 4.10
    rejects Chrome's shuffled CRYPTO frames (occasional INTERNAL_ERROR);
    nghttp3 closes with EXCESSIVE_LOAD after about 1000 PRIORITY_UPDATE
    frames on one connection; kwik crashes on Chrome's 0-RTT (inferred to
    be kwik's bug, not decrypted to confirm).
  - Detection: none. User-visible: yes, occasionally, as in Chrome.
  - Close: nothing, except confirming the kwik trace (small).
  - Source: [quic-interop report](../internal/harness/reports/quic-interop-2026-10-25.md).
- **Retry from the browser's side** (all).
  - Differs: no capture shows a browser answering a server's Retry.
    mimicry follows the QUIC stack's RFC 9000 path.
  - Shows: servers that send Retry under load.
  - Detection: low to medium. User-visible: no.
  - Close: a capture against a Retry-forcing server; small to medium.
  - Source: [http3-edge report](../internal/harness/reports/http3-edge-2026-10-25.md).
- **PMTU probing, ECN and DATAGRAM frames** (all).
  - Differs: not measured; loopback shows none of them. The QUIC stack
    does no path MTU discovery and ignores ECN feedback.
  - Shows: real network paths.
  - Detection: low to medium; ECN counts and PMTU probes are visible to a
    QUIC-aware edge. User-visible: no.
  - Close: captures over a real network path; medium.
  - Source: [concepts/http3.md, Not supported](concepts/http3.md#not-supported);
    `internal/quic/doc.go`.
- **A server's QPACK dynamic table in responses** (all).
  - Differs: no capture server used one, so the decoder side (decoder
    stream instructions) is not measured.
  - Shows: servers that compress response headers with a dynamic table.
  - Detection: low to medium. User-visible: no.
  - Close: a capture against such a server; medium.
  - Source: [EVIDENCE.md, QUIC and HTTP/3](../profiles/EVIDENCE.md#quic-and-http3).
- **Safari closes QUIC on the server's GOAWAY** (Safari 27, 26).
  - Differs: Safari closes its QUIC connection itself when the server's
    HTTP/3 GOAWAY arrives; mimicry does not.
  - Shows: servers that send HTTP/3 GOAWAY.
  - Detection: low. User-visible: no.
  - Close: one profile field, already captured; small.
  - Source: `h3EdgeGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go).
- **Resumed attempts' probes** (Chrome, Firefox).
  - Differs: Chrome's resumed attempt first probes with 4 Initials (the
    whole hello, cut afresh; on Windows 2 in 1 of 3 samples) and later
    sends lone 0-RTT datagrams; mimicry sends one Initial per probe and no
    lone 0-RTT datagrams.
    Firefox's resumed probes vary (pairs from a cached 3 ms RTT, lone
    40-byte 0-RTT datagrams); mimicry replays its whole first flight.
  - Shows: a resumed QUIC attempt to a server that does not answer.
  - Detection: low (failure path). User-visible: no; give-up times
    match.
  - Close: resumed-probe rules from `h3-broken`, already captured;
    medium.
  - Source: `h3EdgeGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go).
- **Chrome's NEW_TOKEN cache** (Chrome).
  - Differs: Chrome's 0-RTT reconnection after 35 s idle carried no
    NEW_TOKEN token in 2 of 3 samples, though the first connection had
    received one. Chrome 156 presents it even less: on 3 of 9
    reconnections whose host had given it one (captured on the second Mac, on macOS 26;
    3 of 6 in a check on the Mac you run it on), which ones varying between
    samples. Chrome 156 on Windows the same: 3 of 9 in `h3-discovery`,
    15 of 27 in `h3-early`, 12 of 27 in `h3-too-early`. mimicry always
    presents the token.
  - Shows: QUIC reconnections after idle.
  - Detection: low. User-visible: no.
  - Close: captures of Chrome's token cache rules; medium.
  - Source: `sampleGaps` in `internal/harness/cmd/h3gen/load.go`;
    `profile/quic.go` (`Tokens`).
- **Chrome's late PRIORITY_UPDATE** (Chrome 156, macOS 26 and Windows).
  - Differs: in 3 of the second Mac's `h3-discovery` samples, and in 1 of
    the Windows PC's, the server read a request's PRIORITY_UPDATE 1 ms
    after its HEADERS (another datagram); everywhere else, and in a
    2-sample check on the Mac you run it on, it came first. mimicry sends it
    first (both are flushed under one cork, control stream first), though
    one test flake showed otherwise (see load flakes in the HTTP/3 page
    tests, under [Tooling and tests](#tooling-and-tests)). Chrome's packet
    flushing under load, likely (unconfirmed; seen on two machines now).
  - Shows: HTTP/3 requests on a busy or slow client.
  - Detection: low. User-visible: no.
  - Close: the datagram each frame came in, or more samples on each
    machine; small.
  - Source: `sampleGaps` (`priorityUpdate <path>`) in
    `internal/harness/cmd/h3gen/load.go`.
- **The `h3-early` pair's POST on Windows** (Chrome on Windows; replay
  timing).
  - Differs: nothing measured in mimicry's rules. Chrome on Windows made
    the pair's POST 4–6 ms after its GET, by when the resumed connection
    the GET started had finished its handshake on loopback, so the POST
    went in 1-RTT (5 of 6 samples; on macOS it came within 3 ms of the
    Finished and was held for it). The e2e replay makes both requests in
    the same millisecond, so mimicry's POST is held for the handshake as
    `http3.earlyData` says, and the edge test cannot tell which request
    started the connection.
  - Shows: an unsafe request made just after a GET on a resumed QUIC
    connection.
  - Detection: none. User-visible: no.
  - Close: replay each step at the browser's own issue times, and a
    faster loopback handshake under `-race`; medium.
  - Source: `h3EdgeSchedulingGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go).
- **ACK_FREQUENCY and IMMEDIATE_ACK** (Firefox advertises
  `min_ack_delay`).
  - Differs: no capture server sent them; mimicry ignores both frames.
  - Shows: servers that send them.
  - Detection: low. User-visible: no.
  - Close: a capture against a server sending ACK_FREQUENCY; small to
    medium.
  - Source: [http3 report](../internal/harness/reports/http3-2026-10-25.md);
    `internal/quic/client.go`.
- **Chrome's TCP delay from the smoothed RTT** (Chrome).
  - Differs: Chrome's source delays its TCP job by 1.5 × the origin's
    smoothed RTT when it expects QUIC to work. Loopback cannot tell that
    from no delay, so it is not modelled.
  - Shows: the race on real networks (tens of milliseconds).
  - Detection: low (timing). User-visible: no.
  - Close: a capture over a real network; medium.
  - Source: [concepts/http3.md](concepts/http3.md#if-quic-is-blocked);
    [decision 0028](decisions/0028-quic-failure-model.md). A saved
    alternative's smoothed RTT is the same rule (a returning browser's
    first request, above).
- **Migration by Firefox and Safari** (Firefox, Safari).
  - Differs: neither was seen migrating, so neither profile does. Chrome's
    port migration is modelled.
  - Shows: NAT rebinding or a path dying mid-connection.
  - Detection: low. User-visible: possibly a stall (Firefox waits for its
    30 s idle timeout, then TCP).
  - Close: a capture under NAT rebinding; medium. Firefox's dead-path
    wait is listed above (dead-path waits).
  - Source: [concepts/http3.md, Not supported](concepts/http3.md#not-supported).
- **Long and slow transfers** (all).
  - Differs: key updates beyond 37,000 received packets and credit growth
    beyond 50 MiB or at lower bandwidth are not measured. mimicry never
    starts a key update itself (no browser did), so against a server that
    never updates, a connection's keys outlive AES-GCM's limit after about
    10 GB sent.
  - Shows: very long or slow downloads and uploads.
  - Detection: low. User-visible: no.
  - Close: longer and throttled download and upload captures; small to
    medium.
  - Source: [concepts/http3.md](concepts/http3.md);
    [http3-edge report](../internal/harness/reports/http3-edge-2026-10-25.md).
- **Congestion control** (all).
  - Differs: the QUIC stack uses NewReno; the browsers' algorithms (Cubic,
    BBR) are timing no capture measured.
  - Shows: sending rate on long transfers.
  - Detection: low. User-visible: no.
  - Close: real-network captures; large.
  - Source: [decision 0027](decisions/0027-quic-base.md).
- **Keep-alive against a short server idle timeout** (all).
  - Differs: against a server idle timeout under 30 s, mimicry pings at
    half of it while a request waits; what browsers do there is not
    measured.
  - Shows: servers with short idle timeouts.
  - Detection: low. User-visible: no.
  - Close: a short-idle capture; small.
  - Source: [profiles/EVIDENCE.md](../profiles/EVIDENCE.md#failure-version-negotiation-long-transfers);
    [decision 0028](decisions/0028-quic-failure-model.md).
- **Safari's other unmeasured QUIC values** (Safari).
  - Differs: its ACK-only run limit, `zeroRtt`, `closeReply`, handshake
    give-up and probe kind were never seen (TCP won first).
  - Shows: lossy networks and server closes.
  - Detection: low. User-visible: no.
  - Close: h3-edge captures where QUIC wins; medium. See also Safari's
    handshake loss timing and Safari over HTTP/3, above.
  - Source: [EVIDENCE.md, QUIC and HTTP/3](../profiles/EVIDENCE.md#quic-and-http3);
    `internal/quic/client_loss.go`.
- **`requestsAfterConfirmed` for Chrome and Firefox** (Chrome,
  Firefox).
  - Differs: no request of theirs waited for a full handshake, so whether
    one would is not measured. Chrome's sixth unidirectional stream is
    inferred to be the QPACK decoder.
  - Shows: a request made before the first handshake completes.
  - Detection: low. User-visible: no.
  - Close: an h3 scenario with an immediate request; small.
  - Source: [EVIDENCE.md, QUIC and HTTP/3](../profiles/EVIDENCE.md#quic-and-http3).
- **NEW_TOKEN from a v2 connection** (Firefox).
  - Differs: whether Firefox presents a v2 connection's token on a later
    connection is not measured (aioquic sent none); mimicry does not pass
    it on.
  - Detection: low. User-visible: no.
  - Close: a v2 server that sends NEW_TOKEN; small.
  - Source: [http3-edge report](../internal/harness/reports/http3-edge-2026-10-25.md);
    [`internal/quic/UPSTREAM.md`](../internal/quic/UPSTREAM.md).
- **A v2-only server that sends `version_information`** (Firefox).
  - Differs: untested; mimicry's Firefox should reach HTTP/3 there
    ([INFERENCE]).
  - Detection: low. User-visible: no.
  - Close: an interop run against such a server; small.
  - Source: [quic-interop report](../internal/harness/reports/quic-interop-2026-10-25.md).
- **Firefox's resumed SNI cut** (Firefox).
  - Differs: where Firefox cuts a resumed hello varies (4 of 9 cut inside
    the host name); the profile's rule covers fresh hellos.
  - Detection: low. User-visible: no.
  - Close: more resumed samples; small.
  - Source: [EVIDENCE.md, QUIC and HTTP/3](../profiles/EVIDENCE.md#quic-and-http3)
    (`quic.crypto`).
- **Gaps in the QUIC stack** (all).
  - Differs: the server's `preferred_address` is not used; its
    `max_udp_payload_size` and `disable_active_migration` are parsed but
    not acted on (Chrome-style migration does not appear to check the
    last: [INFERENCE] from `client_path.go`); no STREAMS_BLOCKED is sent.
  - Shows: servers that rely on those features.
  - Detection: low. User-visible: no.
  - Close: medium.
  - Source: `internal/quic/client_path.go`, `internal/quic/conn_id.go`;
    [`internal/quic/UPSTREAM.md`](../internal/quic/UPSTREAM.md).
- **Opaque transport parameters** (Firefox, Safari).
  - Differs: nothing on the wire; what Firefox's `0x1d` and Safari 26's
    `0xff080808` mean is unknown, and they are sent verbatim. Safari 27
    sent `0xff080808` too until midday 2026-10-25 and has not since (same
    build): an Apple-side switch that may flip back; a recapture shows it.
  - Detection: none while the profile follows the browser; the set is in
    HTTP/3 fingerprints (fp.impersonate.pro's perk). User-visible: no.
  - Close: source research; small.
  - Source: [http3 report](../internal/harness/reports/http3-2026-10-25.md),
    [realcheck report](../internal/harness/reports/realcheck-2026-10-25.md).
- **Sample outliers left out** (Chrome, Firefox, Safari).
  - Differs: pinned and not modelled, each seen once or by timing:
    Chrome's ACK of the server's Initial (1 of 12); Firefox's sample-3
    resumed attempt (306 ms probe, 9.2 s give-up) and a keep-alive 254 ms
    late; Safari's irregular stream credit; Safari 27's TCP dial 139 and
    202 ms after a Version Negotiation; the ACK batching rate of
    batching browsers (machine timing, logged only).
  - Detection: none. User-visible: no.
  - Close: more samples, if ever.
  - Source: `sampleGaps` in `internal/harness/cmd/h3gen/load.go`;
    [EVIDENCE.md, QUIC and HTTP/3](../profiles/EVIDENCE.md#quic-and-http3).
- **HTTP/3 bulk throughput against nginx** (Chrome; performance).
  - Differs: nothing on the wire. nginx serves a connection from one
    saturated worker and is ACK-clocked, so its throughput follows the
    datagrams the client sends: Chrome's ACK every 10 packets (captured)
    costs bulk throughput against clients that ACK less often. A larger
    receive buffer helps at 64 requests at once but overflows nginx's
    socket with ACK bursts at 256, so the default stays at Chrome's 1 MB.
    The measurements are in decisions 0043 and 0045.
  - Detection: none (it is the browser's ACK rate). User-visible: bulk
    downloads from a single-worker HTTP/3 server are somewhat slower.
  - Close: by default none: fewer ACKs are not Chrome's.
    `Config.HTTP3ACKEvery` is the opt-in. Sending flow-control frames
    past the client's congestion window would remove the larger buffers'
    stall, and a larger default with it (RFC 9002 says MUST NOT; medium).
  - Source: decisions [0043](decisions/0043-http3-ack-rate-opt-in.md)
    and [0045](decisions/0045-udp-receive-buffer-default.md);
    `internal/bench/reports/focus-2026-10-29/udpbuf-*`.
- **HTTP/3 receive path runs on about one core** (all; performance).
  - Differs: nothing on the wire. The endpoint's reader hands each
    datagram to the connection's loop through a channel; under bulk load
    the two run one after the other on one P while the stream readers the
    loop wakes, one per STREAM frame, fill the others (execution trace:
    the reader runnable but waiting 66% of the time). A connection takes
    about 650-740k datagrams/s (mimicry 634k against tls-client's 693k
    from the Go server at 10 MB, 64 at once). At equal buffers (416 KB)
    mimicry dropped about three times net/http's datagrams at 100 KB, 64
    at once: presumably most of what keeps it under the quic-go clients
    there (4,725 against about 7,600 req/s on a Linux test host
    (i5-13500) at 1 MB).
  - Detection: none. User-visible: no (about 1 GB/s per connection).
  - Close: wake a stream's reader once per datagram batch, not per frame
    (medium).
  - Source: `udpbuf-*-stock-mac` in
    `internal/bench/reports/focus-2026-10-29/`.
- **HTTP/3 request starts are serialized per connection** (all;
  performance).
  - Differs: nothing on the wire. Each request opens its stream, encodes
    its QPACK section and writes PRIORITY_UPDATE and HEADERS under one
    lock and cork per connection (stream IDs and frames in the browser's
    order), and response sections decode under the decoder's lock: at
    1 KB and 64 at once a request waited about 50 µs for the start lock,
    and these two locks are most of the remaining contention.
  - Detection: none. User-visible: no (HTTP/3 at 1 KB ran 30-40% above
    tls-client after `internal/quic/UPSTREAM.md` change 15).
  - Close: decode sections that use no dynamic table without the lock
    (per-call scratch); start requests already queued under one cork
    (medium).
  - Source: the bench's HTTP/3 1 KB rows
    ([bench README](../internal/bench/README.md)).
- **Fast QUIC fallback, by default** (all).
  - Differs: when a QUIC attempt goes unanswered, mimicry moves the
    request to TCP after its race delay or first probe timeout; Chrome
    and Firefox hold it on the QUIC attempt until they give up (4–5 s),
    and Safari waits out its 100 ms race delay after Version Negotiation.
  - Shows: sites where QUIC is blocked or broken.
  - Detection: low; request timing on failure paths only. User-visible:
    no (faster).
  - Close: `Config.BrowserQUICFallback` restores the browsers' waits.
  - Source: `h3EdgeFastGaps` in [`http3_edge_test.go`](../internal/harness/e2e/http3_edge_test.go);
    [decision 0036](decisions/0036-fast-quic-fallback.md).

## Headers and request kinds

- **`Accept-Language` and the machine behind the profile** (all).
  - Differs: the profiles' `accept-language` is the capture machine's
    setting, not a browser constant. Vendors compare it with the IP's
    location and the client's time zone.
  - Shows: every request.
  - Detection: medium when it does not fit the exit IP. User-visible: no.
  - Close: document the override (`Config.Header` with an
    `Accept-Language`, which preflights carry too), or make it a profile
    parameter; small.
  - Source: [EVIDENCE.md, Request headers](../profiles/EVIDENCE.md#request-headers).
- **The page's own header order** (all; XHR, and Firefox's `fetch()` with
  an object literal).
  - Differs: `http.Header` has no order, so headers the page set go in name
    order. Browsers follow the order the page set them in for XHR
    (Firefox in insertion order; Chrome and Safari keep the first place
    of a repeated name), and Firefox for `fetch()` given an object
    literal. `HeaderOrderKey` replaces the whole order, so it is no
    substitute; `Request.HeaderPlacement` can put the page's headers in
    its order by hand ([decision 0030](decisions/0030-header-placement.md)),
    one `Place` per header, but nothing does it from the page's own order.
  - Shows: XHR and fetch requests with two or more custom headers set out
    of name order.
  - Detection: medium; header order is fingerprinted, though custom
    headers are site-specific. User-visible: no.
  - Close: a `Request` field for the page's order, already captured;
    small to medium (API change, by decision
    [0021](decisions/0021-page-headers-hash-table-order.md)).
  - Source: `headersOrderGaps` in [`headersorder_test.go`](../internal/harness/e2e/headersorder_test.go);
    [headers-order report](../internal/harness/reports/headers-order-2026-10-24.md).
- **Caller-set forbidden headers are sent** (all).
  - Differs: a browser drops a header a page may not set (`Sec-*`,
    `Proxy-*` and the other forbidden names); mimicry sends one if the
    caller sets it on a request of a page kind.
  - Shows: only when the caller sets such a header.
  - Detection: medium; a header no page can produce. User-visible: no.
  - Close: drop or refuse forbidden names in the resolver (the list
    exists in `internal/cors`); small.
  - Source: [EVIDENCE.md, Request kinds](../profiles/EVIDENCE.md#request-kinds);
    [headers-order report](../internal/harness/reports/headers-order-2026-10-24.md).
- **Safari's placement of `Authorization` and other common names**
  (Safari).
  - Differs: page-set headers that are among WebKit's common names
    (`Authorization`, `Cache-Control`, …) may enter Safari's hash table
    among its own headers. Only `content-type` was captured.
  - Shows: Safari `fetch()` and XHR with an `Authorization` header set by
    the page, common for APIs.
  - Detection: medium; header order. User-visible: no.
  - Close: a `headers-order` capture with those names; small.
  - Source: [decision 0021](decisions/0021-page-headers-hash-table-order.md);
    [headers-order report](../internal/harness/reports/headers-order-2026-10-24.md).
- **Requests a cross-site frame makes** (all).
  - Differs: a frame's own navigation is modelled, but not what the frame
    then loads: its top-level site for cookies and connections, and its
    headers. Preflights from a cross-site frame are not captured either.
  - Shows: automation inside iframes, such as a payment provider's card
    form at checkout.
  - Detection: medium. User-visible: no.
  - Close: a frame-subresource capture and an API to say a request comes
    from a frame; large.
  - Source: [decision 0005](decisions/0005-request-kinds-api.md);
    [decision 0011](decisions/0011-connection-partitioning.md);
    [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim).
- **User activation on frames and over HTTP/1.1** (Chrome, Firefox).
  - Differs: a navigation says whether a user made it by its
    `Sec-Fetch-User` (nil for a script's), measured with real clicks
    over HTTP/2 (`nav-redirect-click`). A frame cannot say: one Chrome
    HTTP/1.1 sample sent `Sec-Fetch-User` on cross-origin frames and on
    the form POST, the others did not, and the tests accept either. No
    clicked form POST was captured over HTTP/1.1: Firefox's list there
    has no `Sec-Fetch-User` slot, so its user's form POST over HTTP/1.1
    returns `ErrNotMeasured` (Chrome's has the slot that sample showed).
  - Shows: frames after a click; a clicked form to a server that speaks
    HTTP/1.1 only.
  - Detection: medium; `Sec-Fetch-User` is checked for consistency.
    User-visible: yes for Firefox's form POST over HTTP/1.1 (an error).
  - Close: a clicked form on `kinds-http1`, small; frames after a
    click, medium.
  - Source: `resolve.go` (`resolve`);
    [EVIDENCE.md, Request kinds](../profiles/EVIDENCE.md#request-kinds);
    [decision 0005](decisions/0005-request-kinds-api.md);
    [nav-click report](../internal/harness/reports/nav-click-2026-10-26.md).
- **Pages at deeper paths** (all).
  - Differs: the kinds page is at `/`, where the full URL and the origin
    are the same. The full-path `Referer` of same-origin requests follows
    the standard (strict-origin-when-cross-origin), not a capture; so does
    Safari's referrer of CSS fonts off `/` and cross-origin.
  - Shows: subresources of any page below `/`, which is most pages.
  - Detection: medium if wrong; `Referer` is on most requests.
    User-visible: no.
  - Close: a kinds page at a deep path; small.
  - Source: [EVIDENCE.md, Request kinds](../profiles/EVIDENCE.md#request-kinds).
- **A same-site form POST navigation** (all).
  - Differs: not captured. It sends `Origin`, and the navigation list has
    no `Origin` slot, so it returns `ErrNotMeasured`.
  - Shows: submitting a form to another origin of the same site (for
    example `www.` to `checkout.`).
  - Detection: none (nothing is sent). User-visible: yes, an error.
  - Close: add a same-site form POST to the kinds page; small.
  - Source: [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (Headers); [how a request is built](concepts/how-a-request-is-built.md).
- **Other combinations no variant covers** (all).
  - Differs: a request to an `http://` origin with no matching variant and
    no non-secure default for its kind, or one that sends `Origin` where
    the fallback list has no `Origin` slot, returns `ErrNotMeasured`.
  - Shows: unusual kind and method combinations to `http://` origins, or
    cross-origin requests in conditions no capture showed.
  - Detection: none. User-visible: yes, an error.
  - Close: a capture per condition met in practice; small each.
  - Source: `resolve.go` (`resolve`);
    [how a request is built](concepts/how-a-request-is-built.md).
- **`fetchpriority` on other kinds** (all).
  - Differs: high and low priorities are measured for `Fetch` and
    `InjectedImage` only. Asking for one on another kind returns
    `ErrNotMeasured`.
  - Shows: only when a caller sets `Request.Priority` on another kind.
  - Detection: none. User-visible: yes, an error.
  - Close: extend the kinds page with `fetchpriority` per kind; small.
  - Source: [EVIDENCE.md, Request kinds](../profiles/EVIDENCE.md#request-kinds);
    `resolve.go`.
- **No HTTP cache** (all).
  - Differs: mimicry keeps no cache, so it never adds `If-None-Match` or
    `If-Modified-Since`, and a reload looks like a first visit unless the
    caller sets those headers. Since mimicry fetches no subresources by
    itself (see [what mimicry does](concepts/what-mimicry-does.md)), which
    requests a returning visitor would skip is also the caller's choice.
  - Shows: repeat visits.
  - Detection: low to medium; a returning browser revalidates. Vendors
    rarely check it. User-visible: no.
  - Close: a decision first (a cache is a large feature); then
    captures of revalidation headers per kind; large.
  - Source: found while writing [what mimicry does](concepts/what-mimicry-does.md).
- **`FontFace` loads and `crossorigin` stylesheets** (all; Firefox
  differs most).
  - Differs: neither has a kind. `Font` is a stylesheet's `@font-face`
    load; Firefox's `FontFace().load()` sends `accept-encoding: identity`
    and other priorities (Chrome `u=0`, Firefox none).
  - Shows: only if the caller needs these request types.
  - Detection: low to medium. User-visible: no kind to ask for.
  - Close: add both to the kinds page (FontFace is already captured);
    small to medium.
  - Source: [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (Headers); [kinds report](../internal/harness/reports/kinds-2026-10-23.md);
    [`http3_test.go`](../internal/harness/e2e/http3_test.go).
- **Chrome's `content-type` role** (Chrome).
  - Differs: whether Chrome's `content-type` counts as a page header or its
    own in the hash-table order is a tie no capture resolves; kindsgen
    follows Blink's source.
  - Shows: Chrome fetch or XHR with `content-type` and another page header.
  - Detection: low to medium. User-visible: no.
  - Close: a `headers-order` capture with `content-type` beside other
    names; small.
  - Source: [headers-order report](../internal/harness/reports/headers-order-2026-10-24.md);
    [EVIDENCE.md, Request kinds](../profiles/EVIDENCE.md#request-kinds).
- **Layout-driven image priorities** (Firefox, Chrome).
  - Differs: Firefox gives some images in the HTML `u=4` (weight 13) where
    the kind sends `u=5, i`; Chrome raises a visible markup image, or its
    redirect hop, to `u=1` (Chrome 156's `redirects2-cookies` on macOS: on more of
    them, in some samples). Both depend on layout.
  - Shows: images in the page's markup.
  - Detection: low; it depends on layout, so vendors cannot expect a
    value. User-visible: no.
  - Close: captures of images placed above and below the fold; medium.
  - Source: `redirectsLineGaps` in [`redirects2_test.go`](../internal/harness/e2e/redirects2_test.go);
    `h3Gaps` in [`http3_test.go`](../internal/harness/e2e/http3_test.go);
    `h3PriorityGaps` in `internal/harness/cmd/kindsgen/http3.go`;
    [redirects2 report](../internal/harness/reports/redirects2-2026-10-24.md).
- **HTTP/3-only priorities refused over TCP** (latent).
  - Differs: a priority measured only over HTTP/3 (`http3.high`,
    `http3.low`) returns `ErrNotMeasured` when the request goes over TCP.
    No built-in profile has any yet.
  - Detection: none. User-visible: only with custom profiles.
  - Source: `internal/engine/native/engine.go`;
    [concepts/http3.md](concepts/http3.md).
- **Safari 26's font cookie race** (Safari 26).
  - Differs: Safari 26 sent the CSS font before processing the response
    that set `kb_part`, so that cookie was missing; mimicry sends it. The
    test accepts it (`raceCookieLabels`).
  - Detection: none (timing). User-visible: no.
  - Close: nothing practical.
  - Source: [`kinds_test.go`](../internal/harness/e2e/kinds_test.go);
    [EVIDENCE.md, Request kinds](../profiles/EVIDENCE.md#request-kinds).
- **Safari's hash tables at extreme sizes** (Safari).
  - Differs: tables past 1732 names, and the hash of names over 96
    characters, come from CoreFoundation's source only.
  - Detection: none. User-visible: no.
  - Source: [headers-order report](../internal/harness/reports/headers-order-2026-10-24.md).
- **The navigation's `content-length` slot** (all).
  - Differs: placed as on `fetch()` uploads, but no request reaches it: a
    POST navigation takes a variant.
  - Detection: none. User-visible: no.
  - Source: [EVIDENCE.md, Request headers](../profiles/EVIDENCE.md#request-headers).
- **Stacked and unknown content encodings** (all).
  - Differs: a response with an encoding mimicry does not decode, or
    several stacked (`gzip, br` on one line or on several), comes back
    raw with its `Content-Encoding`; browsers decode stacked encodings.
  - Shows: rare servers that encode twice.
  - Detection: none. User-visible: yes, the caller gets encoded bytes.
  - Close: decode each listed encoding in turn; small.
  - Source: [decision 0046](decisions/0046-response-decompression.md).

## Cookies

- **Safari's order of `Domain` cookies of different levels** (Safari).
  - Differs: not captured; the jar's order there is a guess.
  - Shows: Safari requests carrying cookies set for both the parent domain
    and a subdomain, common on shops.
  - Detection: low to medium; cookie order. User-visible: no.
  - Close: a `cookies-edge` step; small.
  - Source: [cookies report](../internal/harness/reports/cookies-2026-10-24.md);
    [concepts/cookies.md](concepts/cookies.md#the-rules).
- **Limits and eviction** (all).
  - Differs: no total jar limit; Chrome's priority-aware and
    `Secure`-aware eviction within the per-site limit is not modelled; the
    180 → 150 eviction comes from source (the wire showed 169 kept); LRU
    and oldest-first could not be told apart; how partitions count
    towards the limit is a guess (the jar counts each apart). Safari kept
    200 without a cap.
  - Shows: sites setting more than about 150 cookies.
  - Detection: low. User-visible: no.
  - Close: `cookies-edge` captures with priorities, `Secure` mixes and
    staggered setting; medium.
  - Source: [cookies-edge report](../internal/harness/reports/cookies-edge-2026-10-24.md);
    [concepts/cookies.md](concepts/cookies.md#the-rules).
- **Chrome's two-minute Lax window** (Chrome).
  - Differs: the end of the window in which a cookie without `SameSite`
    goes on a cross-site POST is Chromium's documented 120 s, not
    measured (only a seconds-old cookie was).
  - Shows: a cross-site POST navigation one to three minutes after the
    cookie was set.
  - Detection: low. User-visible: no.
  - Close: a timed capture; small.
  - Source: [cookies report](../internal/harness/reports/cookies-2026-10-24.md);
    [concepts/cookies.md](concepts/cookies.md#the-rules).
- **`Expires` and the server's clock** (all).
  - Differs: `Expires` is judged by the local clock, with no correction
    for the server's `Date` header.
  - Shows: servers with skewed clocks setting near-term expiries.
  - Detection: low. User-visible: no.
  - Close: a capture with a skewed `Date`; small.
  - Source: [concepts/cookies.md](concepts/cookies.md#the-rules).
- **Safari's third-party cookies: stored or not sent** (Safari).
  - Differs: whether Safari refuses unpartitioned third-party cookies when
    storing or when sending is not visible; the jar does both.
  - Shows: a later first-party visit to that third party.
  - Detection: low. User-visible: no.
  - Close: a capture that later visits the third party first-party;
    small.
  - Source: [decision 0012](decisions/0012-cookie-rules-in-profiles.md);
    [concepts/cookies.md](concepts/cookies.md#the-rules).
- **WebSocket handshake cookies** (all).
  - Differs: not captured: a `Strict` cookie on a cross-site socket (the
    jar withholds it, Safari 26 included), the socket host's own partition
    cookie, and `Set-Cookie` on a handshake response.
  - Shows: cross-site sockets; servers setting cookies on the upgrade.
  - Detection: low. User-visible: no.
  - Close: extend the `websocket` capture; small.
  - Source: [EVIDENCE.md, Cookies](../profiles/EVIDENCE.md#cookies) and Not
    covered (WebSockets).
- **Safari's partitioned cookies against path length** (Safari).
  - Differs: `partitionedFirst` puts the partition before path length;
    the two together were not captured (all cookies were at `/`).
  - Detection: low. User-visible: no.
  - Close: a capture with partitioned, path-scoped cookies; small.
  - Source: [EVIDENCE.md, Cookies](../profiles/EVIDENCE.md#cookies).
- **Safari's `__Host-` cookie from a deeper path** (Safari).
  - Differs: a `__Host-` cookie without `Path` set from a URL below `/`
    was not captured.
  - Detection: low. User-visible: no.
  - Close: a `prefix-probe` from a deep path; small.
  - Source: [cookies-edge report](../internal/harness/reports/cookies-edge-2026-10-24.md).
- **Loopback hosts other than `localhost`** (all).
  - Differs: `127.0.0.1`, `::1` and `*.localhost` count as secure from the
    Secure Contexts specification, for cookies and for non-secure lists;
    not captured.
  - Detection: none (local targets only). User-visible: no.
  - Close: a loopback probe with addresses; small.
  - Source: [decision 0015](decisions/0015-non-secure-origins.md);
    [concepts/cookies.md](concepts/cookies.md#the-rules).
- **Chrome and nameless `__Http-` values** (Chrome).
  - Differs: whether Chrome refuses a nameless cookie whose value starts
    with `__Http-` is not captured; the jar does not.
  - Detection: none. User-visible: no.
  - Close: add it to the nameless probe; small.
  - Source: [cookies-edge report](../internal/harness/reports/cookies-edge-2026-10-24.md).

## CORS

- **Preflight `Max-Age` caps** (all).
  - Differs: mimicry honours `Access-Control-Max-Age` as sent. Browsers cap
    it (Chrome 2 h, Firefox 24 h, Safari 10 min, from their sources), so
    in a long session they preflight again where mimicry does not.
  - Shows: sessions longer than the cap against an API with a large
    `Max-Age`.
  - Detection: low to medium; preflight cadence on long sessions.
    User-visible: no.
  - Close: a long-session capture; small, slow to run.
  - Source: [cors report](../internal/harness/reports/cors-2026-10-24.md);
    [decision 0017](decisions/0017-cors-preflights.md).
- **Safelist and refusal edges** (all).
  - Differs: long values, ranges and language values in the safelist, the
    refusal rules other than a non-2xx status and a disallowed header, and
    the origin, credentials and wildcard rules follow the Fetch standard,
    not captures.
  - Shows: edge-case CORS requests.
  - Detection: low. User-visible: possibly, a refusal the browser would
    not make or the reverse.
  - Close: extend the `cors` page; small to medium.
  - Source: [EVIDENCE.md, CORS preflights](../profiles/EVIDENCE.md#cors-preflights).
- **Chrome's preflight cache by top-level site** (Chrome).
  - Differs: the cache is keyed by page origin, URL and credentials mode.
    Whether Chrome also keys it by top-level site is not measured.
  - Shows: the same API called from two sites within the cache lifetime.
  - Detection: low. User-visible: no.
  - Close: a two-site `cors` capture; small.
  - Source: [cors report](../internal/harness/reports/cors-2026-10-24.md);
    [decision 0017](decisions/0017-cors-preflights.md).
- **A hop's preflight over plain HTTP** (all).
  - Differs: whether a redirect hop's preflight over plain HTTP uses
    another list than the first preflight is not tested.
  - Detection: low. User-visible: no.
  - Close: a `kinds-plain` redirect with a preflight; small.
  - Source: [decision 0017](decisions/0017-cors-preflights.md) (Revisit
    when).
- **Preflights from cross-site frames**: see
  [requests a cross-site frame makes](#headers-and-request-kinds).

## Redirects

- **Safari 26's clicked navigations** (Safari 26).
  - Differs: not captured (`nav-redirect-click` and its cookie twin): the
    capture tool clicks in Safari through the system's mouse events, which
    need Accessibility access, and the second Mac's captures run over ssh,
    which has none (`missingCaptures`). Safari sends no `Sec-Fetch-User`,
    and Safari 27's clicked navigations were sent as its script's, so
    Safari 26's follow its script's captures (`nav-redirect-page`,
    `nav-redirect-form` and their twins).
  - Detection: low. User-visible: no.
  - Close: grant Terminal Accessibility access on the second Mac and run
    the capture from Terminal there; small.
  - Source: `missingCaptures` in [`captures_test.go`](../internal/harness/e2e/captures_test.go);
    [nav-click report](../internal/harness/reports/nav-click-2026-10-26.md).
- **Other hop combinations not captured** (all; Safari has the most).
  - Differs: once a kind's redirects were captured, a hop in a condition
    no variant matches returns `ErrNotMeasured` rather than guess an
    order. Examples from the reports: a no-cors image redirected within
    another site, a same-site fetch hop, Safari's GET hop of a fetch with
    page-set headers, a redirect after page-set headers where the header
    table was not captured redirected, and a typed navigation's later
    hops that change origin or scheme otherwise than captured. A page's
    navigation (link or form POST, clicked or by script) is captured
    redirected once from and to every relation (`nav-redirect-click`,
    `nav-redirect-form`); a chain whose later hops change origin
    otherwise than these is not. Check the current profiles before
    capturing: `redirects2` and the `nav-redirect-*` captures closed
    several cases the earlier reports list.
  - Shows: less common redirect chains.
  - Detection: none. User-visible: yes, an error.
  - Close: broader redirect captures; medium.
  - Source: `resolve.go` (`resolve`);
    [EVIDENCE.md, Redirects](../profiles/EVIDENCE.md#redirects);
    [cors-edge report](../internal/harness/reports/cors-edge-2026-10-24.md);
    [redirects2 report](../internal/harness/reports/redirects2-2026-10-24.md).
- **Chrome 155's hop `cookie` slot** (Chrome 155).
  - Differs: the cookie twins (decision 0048) came after Chrome 155, which
    is no longer installable as branded Chrome and cannot inherit Chrome
    156's captures (`capturesFrom` names an older version). Its hop
    variants have no `cookie` slot, so a hop carrying cookies sends them
    after the list (after `priority`).
  - Shows: a redirect that sets or meets cookies, as Chrome 155.
  - Detection: medium; navigation header order on login flows.
    User-visible: no.
  - Close: by deprecation; or the twins captured with Chrome for Testing
    155, if its hop lists match the brand's; small.
  - Source: [EVIDENCE.md, Redirects](../profiles/EVIDENCE.md#redirects);
    `missingCaptures` in [`captures_test.go`](../internal/harness/e2e/captures_test.go).
- **`cookie` slot on hops no twin carries cookies on** (all).
  - Differs: a hop variant whose captured hops carried no cookie has no
    `cookie` slot, and mimicry sends the jar's cookies after its list. The
    twins (decision 0048) leave three kinds of hop without: cross-site
    subresources with credentials (`credentials: "include"`,
    `withCredentials`, `crossorigin="use-credentials"`; the captured ones
    went without, or to a site never visited), hops from `http://`
    (`insecureBeforeRedirect` image and fetch hops), and Safari 27's image
    hop to another origin of the page's site.
  - Shows: credentialed cross-site API calls and trackers that redirect;
    plain-HTTP subresources redirected to HTTPS.
  - Detection: low to medium. User-visible: no.
  - Close: a credentialed cross-site step on `redirects2-cookies` after a
    navigation to that site, and a `kinds-plain` twin; small to medium.
  - Source: [EVIDENCE.md, Redirects](../profiles/EVIDENCE.md#redirects);
    [decision 0048](decisions/0048-cookie-twins.md).
- **`navigationSite` on longer chains** (all).
  - Differs: how `sec-fetch-site` counts a navigation's chain was measured
    on one chain (to another site and back). Longer chains follow the rule
    unmeasured.
  - Shows: login and payment redirect chains through several sites.
  - Detection: medium; `sec-fetch-site` consistency is checked.
    User-visible: no.
  - Close: a longer `nav-redirect-page` chain; small.
  - Source: [EVIDENCE.md, Redirects](../profiles/EVIDENCE.md#redirects).
- **Chrome's error-page reload** (Chrome).
  - Differs: when a navigation hits its redirect limit, Chrome's error page
    reloads the last URL a second later (`cache-control: max-age=0`) and
    follows the rest of the chain, on macOS and Windows alike. mimicry
    returns the error. In one sample of Chrome 156's `redirects2-cookies` on macOS
    the reload went on a new connection, and `/done` after it; the test
    compares `www.redir-a.test`'s connections as one there
    (`redirectsMerged`).
  - Shows: navigation chains longer than 19 redirects.
  - Detection: low. User-visible: yes, an error where Chrome would carry
    on; deferred as browser UI.
  - Close: by decision 0022, not planned.
  - Source: [decision 0022](decisions/0022-redirects-as-the-browser-follows-them.md);
    [redirects2 report](../internal/harness/reports/redirects2-2026-10-24.md);
    `redirectsReload`, `redirectsMerged` in [`redirects2_test.go`](../internal/harness/e2e/redirects2_test.go).
- **`hopPreflight` to another cross-site origin** (Firefox, Safari).
  - Differs: measured only for a hop to the same cross-site origin.
  - Detection: low. User-visible: no.
  - Close: a `redirects2` step; small.
  - Source: [EVIDENCE.md, Redirects](../profiles/EVIDENCE.md#redirects).
- **CORS check of other element kinds' redirects** (all).
  - Differs: styles, fonts and preloads in CORS mode take the redirect
    CORS check from the standard, not a capture.
  - Detection: low. User-visible: possibly.
  - Close: a `redirects2` addition; small.
  - Source: [EVIDENCE.md, Redirects](../profiles/EVIDENCE.md#redirects).
- **Safari's `keep` Origin on tainted chains** (Safari).
  - Differs: what `redirects.methodChangeOrigin: keep` does on a chain
    through a third origin is not captured.
  - Detection: low. User-visible: no.
  - Close: a capture; small.
  - Source: [cors-edge report](../internal/harness/reports/cors-edge-2026-10-24.md).
- **Safari's cold first sample** (Safari 27, 26).
  - Differs: in the first sample of a run, Safari sent a hop back to the
    page's site on a new connection with another header order (Safari 27
    also with `cross-site`). mimicry sends the warm form; kindsgen leaves
    the sample out (`sampleGaps`).
  - Detection: low; browser state. User-visible: no.
  - Close: cold-start captures; medium.
  - Source: `internal/harness/cmd/kindsgen/derive.go`;
    [decision 0022](decisions/0022-redirects-as-the-browser-follows-them.md).
- **Firefox's `www.redir-c.test` connections**: see
  [connections](#connections-and-pool).
- **The redirect limit of frames** (all).
  - Differs: frames keep the fetch limit of 20; not measured.
  - Detection: none. User-visible: rarely.
  - Close: a capture; small.
  - Source: [EVIDENCE.md, Redirects](../profiles/EVIDENCE.md#redirects).
- **`Config.Header` credentials after a chain leaves its origin**
  (all).
  - Differs: `Authorization` and `Cookie` from `Config.Header` stop once a
    chain has left an origin, following the Fetch standard. Browsers have
    no counterpart to capture.
  - Detection: none. User-visible: no.
  - Source: [EVIDENCE.md, Behaviour in code](../profiles/EVIDENCE.md#behaviour-in-code-not-in-profiles).
- **`CheckRedirect` sees `http.Client`'s hops** (API).
  - Differs: nothing on the wire. A caller's `CheckRedirect` and `via` show
    the hops as `http.Client` built them (a PUT answered 301 shows as a
    GET), while the requests sent are the browser's. Documented on
    `NewClient`.
  - Detection: none. User-visible: possibly confusing.
  - Source: [cors-edge report](../internal/harness/reports/cors-edge-2026-10-24.md).

## WebSockets

- **Firefox and an 8-bit compression window** (Firefox).
  - Differs: a server answering `client_max_window_bits=8` hangs Firefox
    158: zlib-rs refuses the window, and
    `WebSocketChannel::HandleExtensions` calls `AbortSession` while holding
    `mCompressorMutex`, which `DoStopSession` takes again (the capture
    page then never finishes, so it skips that socket in Firefox).
    mimicry fails the dial and ends the transport at once, as Safari does.
  - Shows: only servers that answer 8, which RFC 7692 allows but no
    common server sends.
  - Detection: none (Firefox itself stops talking). User-visible: an
    error instead of a hang.
  - Close: nothing to close; revisit if Firefox fixes the deadlock.
  - Source: [websocket-deflate report](../internal/harness/reports/websocket-deflate-2026-10-26.md).
- **WebSockets through a SOCKS5 proxy: ws://** (all).
  - Differs: not measured; refused (`ErrNotMeasured`). Everything else
    through a proxy is captured and replayed (proxy-websocket captures,
    2026-10-26; Safari 27 with credentials the same day, Safari 26's
    copied from it: see [Safari 26's credential flow](#proxies)).
  - Shows: a `ws://` socket through a `socks5`/`socks5h` proxy.
  - Detection: none. User-visible: yes, an error.
  - Close: Safari's proxy script must send a second port to SOCKS5 (the
    page's plain port), which changes the script every Mac has cached;
    small once the setup is there.
  - Source: [proxy-websocket report](../internal/harness/reports/proxy-websocket-2026-10-26.md);
    `internal/engine/native/websocket.go` (`webSocketRoute`).
- **Subprotocols the handshake list cannot place** (all).
  - Differs: asking for subprotocols where the profile's handshake list
    for that page condition has no place for `Sec-WebSocket-Protocol`
    (such as `ws://` from an `http://` page) returns `ErrNotMeasured`.
  - Shows: `WebSocketOptions.Protocols` in those conditions.
  - Detection: none. User-visible: yes, an error.
  - Close: captures with subprotocols per page condition; small.
  - Source: `websocket.go` (`DialWebSocket`);
    [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (WebSockets).
- **A cross-site socket's connection**: see
  [connections](#connections-and-pool) (top-level-site partitioning).
- **Firefox's transport end** (Firefox).
  - Differs: Firefox ended neither the TCP connection nor the HTTP/2 stream
    in any captured socket. mimicry leaves it to the server and ends it
    itself 10 s after the close handshake. A close the server never
    answers also ends after 10 s (not captured).
  - Shows: every Firefox socket close.
  - Detection: low. User-visible: no.
  - Close: a longer `websocket` capture; small.
  - Source: [EVIDENCE.md, Behaviour in code](../profiles/EVIDENCE.md#behaviour-in-code-not-in-profiles).
- **Chrome's HTTP/2 stream end after a close** (Chrome).
  - Differs: Chrome's close event waits for the server to end the
    socket's stream (its own END_STREAM went with its close frame), at
    most 2 s in Chromium's code (`kUnderlyingConnectionCloseTimeoutSeconds`),
    then drops the stream. `Close` waits as long, but mimicry resets a
    stream the server has not ended after 10 s. Not captured: the capture
    server always ends its stream.
  - Shows: servers that never end a socket's stream after the close
    handshake.
  - Detection: low. User-visible: `Close` takes up to 10 s there.
  - Close: a capture whose server leaves the stream open; small.
  - Source: `internal/ws/conn.go` (`waitsForEnd`, `drain`).
- **Chrome's fragmented large message** (Chrome).
  - Differs: Chrome split a 70 KiB message into two frames (65464 bytes
    and a continuation) in 3 of 6 Chrome 155 samples and 2 of 3 Chrome 156
    ones; mimicry always sends one frame. The test accepts mimicry's whole
    frame against any sample, its split messages joined, so one sample (a
    quick refresh's) is enough.
  - Detection: low; both forms occur. User-visible: no.
  - Close: more sizes to find the rule; small to medium.
  - Source: [`websocket_test.go`](../internal/harness/e2e/websocket_test.go);
    [websocket report](../internal/harness/reports/websocket-2026-10-24.md).
- **Firefox's HTTP/2 DATA segmenting** (Firefox).
  - Differs: Firefox counts the frame header into its 16000 bytes
    (14 + 15986); not modelled, only the cap and header-alone are. DATA
    frames cut short by flow control vary between samples and are not
    modelled either.
  - Detection: low. User-visible: no.
  - Close: an `h2Data` rule; small.
  - Source: [EVIDENCE.md, Behaviour in code](../profiles/EVIDENCE.md#behaviour-in-code-not-in-profiles).
- **`ws://` from an `https://` page** (Firefox, Safari).
  - Differs: mimicry refuses it before connecting, as browsers fail it.
    Whether Firefox and Safari try `wss://` on the plain port first shows
    only in the server's log, not the trace.
  - Detection: low. User-visible: yes, refused (as in the browser).
  - Close: keep the server log with captures; small.
  - Source: [websocket report](../internal/harness/reports/websocket-2026-10-24.md).
- **Unexplained but modelled** (Firefox, Safari 26).
  - Differs: nothing; recorded so nobody "fixes" them. Firefox's
    HTTP/1.1 socket hello drops `compress_certificate` while its other
    resumed hellos keep it; Safari 26 sends a cross-site host's Lax and
    unpartitioned None cookies on cross-site sockets where Safari 27 does
    not. Both are compared against every sample.
  - Detection: none. User-visible: no.
  - Source: [websocket report](../internal/harness/reports/websocket-2026-10-24.md).
- **Read limits** (all).
  - Differs: 64 MiB per message and 64 queued messages are safety bounds,
    not browser values.
  - Detection: none. User-visible: only against hostile servers.
  - Source: [EVIDENCE.md, Behaviour in code](../profiles/EVIDENCE.md#behaviour-in-code-not-in-profiles).

## Connections and pool

- **Partitioning by top-level site** (all).
  - Differs: browsers key connections by top-level site, so a navigation
    to another site goes on a new connection, and a third-party host
    reached from two sites gets two connections. mimicry's pool key has no
    top-level site: one connection to a CDN or payment host can carry
    requests made from two sites. A cross-site WebSocket over HTTP/2 can
    likewise share the host's own first-party connection, and an
    `AltSvcCache` shared by several Transports (`Config.AltSvc`) gives
    each the alternatives learnt for the others' sites.
  - Shows: browsing unrelated sites that share third-party hosts through
    one Transport.
  - Detection: medium; a host serving both sites can link the two visits
    by the connection. User-visible: no.
  - Workaround: one Transport per site, sharing one cookie jar through
    `Config.Cookies` (deferral note in
    [decision 0011](decisions/0011-connection-partitioning.md)).
  - Close: a capture of which requests share the top-level key per
    browser, a decision, and a pool-key change; large.
  - Source: [decision 0011](decisions/0011-connection-partitioning.md);
    [decision 0025](decisions/0025-websockets.md);
    `redirectsApart` in [`redirects2_test.go`](../internal/harness/e2e/redirects2_test.go);
    [`websocket_test.go`](../internal/harness/e2e/websocket_test.go);
    [redirects2 report](../internal/harness/reports/redirects2-2026-10-24.md).
- **Chrome's preconnect** (Chrome).
  - Differs: Chrome opens one more TLS connection to the page's host (a
    handshake, no request) on every page load, on macOS and Windows, and
    sometimes uses it later (after a GOAWAY, in 2 of 3 samples). Through
    a proxy these show
    as extra CONNECTs that only get a 407. mimicry makes no speculative
    connections.
  - Shows: every Chrome page load.
  - Detection: low to medium; the connection count per page differs from
    Chrome's. User-visible: no.
  - Close: model it (already captured); small to medium.
  - Source: [`connections_test.go`](../internal/harness/e2e/connections_test.go)
    (the pinned difference); [`proxy_test.go`](../internal/harness/e2e/proxy_test.go);
    [connections report](../internal/harness/reports/connections-2026-10-24.md);
    [decision 0023](decisions/0023-connection-limits-and-coalescing.md).
- **Firefox's connections for credential-less requests** (Firefox).
  - Differs: Firefox puts credential-less requests on a connection of
    their own to the same-site host only, and shares one with the page's
    own host and the cross-site host. No rule explains it, so it is not
    modelled. On `redirects2` it also put the credential-less and
    credentialed hops to `www.redir-c.test` on two connections, where it
    shared one on `www.redir-b.test`.
  - Shows: Firefox pages with CORS-mode fonts, `crossorigin` elements and
    fetches without credentials.
  - Detection: low to medium; connection reuse is visible, rarely
    checked. User-visible: no.
  - Close: a capture designed to find the rule (more hosts, orders,
    timings); medium.
  - Source: `partitionGaps` in [`kinds_test.go`](../internal/harness/e2e/kinds_test.go);
    [`redirects2_test.go`](../internal/harness/e2e/redirects2_test.go);
    [decision 0011](decisions/0011-connection-partitioning.md);
    [kinds report](../internal/harness/reports/kinds-2026-10-23.md).
- **Several tabs of one browser** (all).
  - Differs: a Transport is one browsing session
    ([decision 0019](decisions/0019-transport-is-one-browsing-entity.md)).
    Tabs sharing a jar but with separate pages are not modelled, and what
    browsers share between tabs is not measured.
  - Shows: callers sharing one client across parallel tasks, who then get
    one task's `Origin`, `Referer` and `sec-fetch-site` on another's
    requests unless they set `Request.From`.
  - Detection: medium if misused. User-visible: no.
  - Close: measure what tabs share, then design a tab API; large.
  - Source: [decision 0019](decisions/0019-transport-is-one-browsing-entity.md).
- **Coalescing conditions** (all).
  - Differs: mimicry coalesces only on the same port, same partition, no
    proxy, and (for `lookup`) an address match, checking the existing
    connection's certificate. The capture had every host on 127.0.0.1, one
    port, credentialed requests and no proxy, so none of these conditions
    was varied, and later requests to a coalesced host were not captured.
    None of it has a test.
  - Shows: CDN hosts on different addresses under one certificate,
    several ports, proxies.
  - Detection: low to medium; an extra connection where the browser
    coalesced, or the reverse. User-visible: no.
  - Close: a `connections` capture varying address, port, partition and
    proxy; medium.
  - Source: [connections report](../internal/harness/reports/connections-2026-10-24.md);
    [decision 0023](decisions/0023-connection-limits-and-coalescing.md);
    [EVIDENCE.md, Connections](../profiles/EVIDENCE.md#connections).
- **Connection limits beyond the per-host cap** (all).
  - Differs: the cap of 6 HTTP/1.1 connections per host was measured over
    HTTPS; mimicry also applies it to plain HTTP and per proxy route
    ([INFERENCE]). Chrome's 256 total and 32 per proxy, and Firefox's 900
    total, are not modelled. Requests waiting for a connection are served
    first come, first served; browsers order them by priority first
    (source).
  - Shows: heavy parallel loads, proxies.
  - Detection: low. User-visible: possibly, in queueing.
  - Close: `connections` captures over plain HTTP, through a proxy and
    with mixed priorities; small to medium.
  - Source: [connections report](../internal/harness/reports/connections-2026-10-24.md);
    [EVIDENCE.md, Not covered](../profiles/EVIDENCE.md#not-covered-by-any-claim)
    (Connections).
- **Connections per page** (all).
  - Differs: on the nike replay Chrome used 64 connections, Firefox 71–74,
    Safari 44. mimicry's count follows from its own rules and has not been
    compared on replays.
  - Detection: low. User-visible: no.
  - Close: compare mimicry against the replays; medium.
  - Source: [capturing.md, Real-page replays](capturing.md#real-page-replays).
- **Unexplained extra connections in the WebSocket captures** (Firefox,
  Chrome).
  - Differs: Firefox opened 2–3 extra HTTP/2 connections that sent only
    SETTINGS, WINDOW_UPDATE and GOAWAY (perhaps its coalescing decline),
    and Chrome had 2 unused connections. Not analysed.
  - Detection: low. User-visible: no.
  - Close: analyse them against the coalescing rules; small.
  - Source: [websocket report](../internal/harness/reports/websocket-2026-10-24.md).
- **State kept per key** (all).
  - Differs: nothing on the wire. TLS tickets (swept when expired), each
    origin's HTTP/3 state (per proxy too, with `Config.HTTP3OverSOCKS5`)
    and, with `Config.BrowserProxyAuth`, which proxies have asked for
    credentials are kept per key for the Transport's life: a Transport
    rotating through many proxies keeps a small entry per proxy. Its
    connections are not kept ([decision 0040](decisions/0040-idle-connection-cleanup.md)).
  - Detection: none. User-visible: memory only, for very long sessions
    across many hosts or proxies.
  - Source: [decision 0020](decisions/0020-connection-pool-package.md).
    What stays once a proxy is swept is under [Proxies](#proxies):
    per-identity memory after a proxy is swept.

## Proxies

- **`https://` proxies** (all).
  - Differs: TLS to the proxy is not supported.
  - Shows: callers whose proxy needs TLS.
  - Detection: none. User-visible: yes, unsupported.
  - Close: an implementation and a capture; medium.
  - Source: [concepts/proxies.md](concepts/proxies.md#limits);
    [decision 0014](decisions/0014-proxies.md).
- **Basic authentication only** (all).
  - Differs: a proxy asking for another scheme (Digest, NTLM, Negotiate)
    fails the request.
  - Shows: corporate proxies.
  - Detection: none. User-visible: yes, an error.
  - Close: per scheme, a capture and an implementation; medium.
  - Source: [concepts/proxies.md](concepts/proxies.md#credentials).
- **Per-identity memory after a proxy is swept** (none: memory only).
  - Differs: a rotating Transport keeps each identity's TLS session
    ticket until it expires and the pool's key for 90 s after its
    connections close, about 1.4 KB per identity; at a new identity
    every 120 ms that grows about 9 KB per second per client, even with
    `Config.MaxIdleProxies` 1 (300 KB after 30 s against azuretls's 67
    KB). The dialer's per-proxy-URL sets (credentials asked for, UDP
    refused) are never pruned either.
  - Shows: nothing on the wire. User-visible: memory of long-running
    rotating clients.
  - Detection: none.
  - Close: forget a swept proxy's tickets, pool keys and dialer entries
    when `MaxIdleProxies` is set (a returning identity then makes a full
    handshake); small, with a benchmark of the plateau over minutes.
  - Source: [decision 0040](decisions/0040-idle-connection-cleanup.md)
    (amendment: `Config.MaxIdleProxies`); see also
    [state kept per key](#connections-and-pool).
- **Proxy credentials before the 407, by default** (all browsers).
  - Differs: the first CONNECT, and the first request sent to the proxy
    whole, carry `Proxy-Authorization` where the browser waits for the
    proxy's 407 (Chrome and Firefox once per proxy URL, Safari on every
    CONNECT). The header list is the browser's own credentialed one.
  - Shows: the proxy only; the site receives the same bytes.
  - Detection: none for sites; a proxy operator could tell.
    User-visible: no (one round trip less per new proxy URL).
  - Close: `Config.BrowserProxyAuth`.
  - Source: [decision 0039](decisions/0039-proxy-credentials-up-front.md).
- **Chrome's preconnects to the proxy**: see
  [Chrome's preconnect](#connections-and-pool).
- **Safari's Proxy-Authorization to the server** (Safari 27, 26).
  - Differs: once Safari has answered a proxy's challenge to a
    plain-HTTP request it sent the proxy whole, it puts
    `Proxy-Authorization` on every later request through that proxy,
    inside CONNECT tunnels too, where the server reads it (after
    `Accept-Encoding`, over HTTP/1.1 and HTTP/2; WebSocket handshakes
    and extended CONNECTs included), and the state outlives the page: in
    the `proxy-websocket-auth` capture (2026-10-26) sample 0's tunnelled
    requests carry none until its forwarded page was challenged and
    every one after does, and samples 1 and 2 (the same Safari) carry it
    from their first request. Sample 0's first forwarded request also
    went without credentials and was challenged, where
    `forwardCredentials` says `always` (every `proxy-plain-auth` sample,
    2026-10-24, sent them unasked from the first). mimicry never sends
    `Proxy-Authorization` inside a tunnel: Safari's first state.
    `TestWebSocketsMatchBrowser` takes any sample's handshake, so it
    passes against sample 0 and logs the variance.
  - Shows: Safari profiles through an authenticating HTTP proxy, every
    HTTPS request, once the state is on.
  - Detection: low; the server sees it, but only through an
    authenticating proxy, and imitating it hands the proxy's credentials
    to every server. User-visible: no.
  - Close: captures that pin the trigger (Safari restarted, then
    `proxy-auth`, `proxy-plain-auth`, `proxy-auth` again), and a
    decision whether to send the caller's proxy credentials to servers;
    medium.
  - Source: [proxy-websocket report](../internal/harness/reports/proxy-websocket-2026-10-26.md)
    (addendum); [EVIDENCE.md, Proxies](../profiles/EVIDENCE.md#proxies).
- **Safari 26's credential flow** (Safari 26).
  - Differs: copied from Safari 27, WebSockets included; the
    `proxy-auth`, `proxy-plain-auth` and `proxy-websocket-auth` captures
    were never taken (`missingCaptures`).
  - Shows: a Safari 26 profile through an authenticating proxy.
  - Detection: low; only the proxy sees it. User-visible: no.
  - Close: the three captures on the second Mac, which need a
    person at its screen for the proxy login dialog; small.
  - Source: [`proxy_test.go`](../internal/harness/e2e/proxy_test.go);
    [proxies report](../internal/harness/reports/proxies-2026-10-24.md);
    [proxy-websocket report](../internal/harness/reports/proxy-websocket-2026-10-26.md);
    [EVIDENCE.md, Proxies](../profiles/EVIDENCE.md#proxies).
- **SOCKS5 credentials** (all).
  - Differs: no browser sends SOCKS5 credentials. mimicry offers them
    (method 2) when the URL has them, visibly the caller's choice.
  - Detection: low; only the proxy sees it. User-visible: no.
  - Close: by decision; revisit if a browser is captured doing it.
  - Source: [decision 0014](decisions/0014-proxies.md).
- **Challenge state shared per proxy** (all).
  - Differs: whether a proxy has asked for credentials is kept per proxy
    URL and shared by tunnels and forwarded requests ([INFERENCE], not
    measured separately).
  - Detection: low. User-visible: no.
  - Close: a capture mixing CONNECT and plain-HTTP requests through one
    authenticating proxy; small.
  - Source: [decision 0014](decisions/0014-proxies.md).
- **Dialling the server while using a proxy** (API).
  - Differs: `Config.DialContext` opens the connection to the proxy, so a
    caller cannot also pin the server's address through it.
  - Detection: none. User-visible: yes, unsupported.
  - Close: an API change; small.
  - Source: [decision 0010](decisions/0010-dial-context.md).
- **HTTP/3 through an HTTP proxy: MASQUE** (API).
  - Differs: HTTP/3 through a proxy goes only through SOCKS5 UDP
    associations (`Config.HTTP3OverSOCKS5`); an `http://` proxy keeps
    every request on TCP. CONNECT-UDP (MASQUE, RFC 9298) would carry QUIC
    through an HTTP proxy.
  - Shows: callers whose only proxy is an HTTP one and who want HTTP/3.
  - Detection: none (no browser uses HTTP/3 through a proxy; with the
    proxy speaking MASQUE the site would see the same datagrams).
    User-visible: yes, TCP only.
  - Close: deferred: few proxy providers offer CONNECT-UDP; an
    extended-CONNECT client over HTTP/2 or HTTP/3 to the proxy and HTTP
    Datagrams (RFC 9297) when callers have such proxies; large.
  - Source: [decision 0038](decisions/0038-http3-over-socks5.md).
- **SOCKS5 proxies that accept UDP ASSOCIATE but relay nothing** (API).
  - Differs: a refusal of UDP ASSOCIATE keeps a proxy's requests on TCP
    from then on; a proxy that accepts and drops the datagrams (common
    among residential providers) is not marked: each origin's QUIC
    attempts fail until `Fallback.BrokenAfter` marks the origin broken,
    each request waiting at most the race delay with fast fallback.
  - Shows: `Config.HTTP3OverSOCKS5` with such a proxy.
  - Detection: none. User-visible: a race delay per request until each
    origin is marked.
  - Close: a per-proxy mark after handshakes that got no answer through
    it; small.
  - Source: [decision 0038](decisions/0038-http3-over-socks5.md).

## Options that do not exist

Behaviour mimicry adds on its own (see
[what mimicry does](concepts/what-mimicry-does.md)) that no `Config` or
`Request` field turns off. Browsers have no such switches either, so none
is needed to look like one. Each is a candidate only if a caller asks for
it.

- **TLS session resumption**: connections resume as the profile's browser
  does (Chrome and Firefox; Safari never). A fresh Transport is the only
  way to start without tickets.
- **Retrying on another connection**: a request nothing of which was sent
  (or that the server rejected unprocessed), an idempotent one that
  failed on a reused HTTP/1.1 connection, and an idempotent one whose
  HTTP/3 connection ended before its response go again on another
  connection, up to 5 attempts in all.
- **The connection limits beyond HTTP/1.1's per host**: one HTTP/2 and
  one HTTP/3 connection per host, and no total limit, are fixed;
  `Config.MaxConnsPerHost` changes only the per-host HTTP/1.1 limit, and
  `Config.DisableCoalescing` turns coalescing off.
- **Chrome's idle PING and credit timer**: fixed by the profile.
- **Broken origins**: an origin whose QUIC failed stays on TCP for the
  Transport's life and cannot be read or reset, other than with a new
  Transport; `Config.AltSvc` shares the alternatives, not the failures.
  `Config.DisableHTTP3` turns HTTP/3 off as a whole.
- **0-RTT while keeping HTTP/3**: no switch. A request the profile's
  browser holds (any method but GET, HEAD and OPTIONS) waits for the
  handshake, and a `425 Too Early` is retried as the browser does; see
  [what mimicry does](concepts/what-mimicry-does.md#tls-resumption-and-0-rtt).
- **Skipping a CORS preflight while keeping the profile's headers**:
  `Exact` skips it, but drops the profile's headers too; `NoCORS` changes
  the request's mode.
- **Firefox's extra TCP connections**: when a QUIC connection dies with
  requests pending, a Firefox profile opens one TCP connection per pending
  request and declines all but the first. A Firefox profile's coalescing
  likewise opens a connection and then declines it
  (`Config.DisableCoalescing` turns coalescing off as a whole).

## Tooling and tests

- **POST and uploads are barely benchmarked.** Only layer 1 sends a POST
  (`POST /echo`, small JSON, one warm request; mimicry is the fastest
  impersonating client there on HTTP/1.1, HTTP/2 and HTTP/3). Missing:
  POST under load in layer 3 (1 KB, 100 KB and 1 MB bodies at 1 and 64
  at once, on all three protocols: requests/s, upload MB/s, CPU and
  allocations per request); a GET-then-POST route in layer 6 (an
  add-to-cart flow); and one POST on a resumed HTTP/3 connection, where
  mimicry holds unsafe methods until the handshake ends as browsers do
  (time to response). Needs a `POST /sink` endpoint on both bench servers
  that reads the body and returns a small response (`/echo` sends the
  body back, mixing upload with download). Medium.
- **Load flake in `TestQUICInterop`: `stream-reset` (Firefox)** (1 in 40,
  on a loaded machine with `-race`): `GET /reject`'s resend went over
  HTTP/1.1. Firefox has no race delay; fast fallback gives its resumed
  0-RTT attempt its first PTO (`earlyLimit`), which a loaded handshake
  outlasts. `BrowserQUICFallback` for such cases would lift it, at the
  cost of slower failure paths in them. Small. (The other load flakes
  found with it were fixed in f5772cb.)
- **Load flake in `TestHTTP3MatchesBrowser/chrome-156-macos/h3-discovery`**
  (seen three times): resumed connections' 0-RTT packets with the
  request's STREAM(0) before the control stream's PRIORITY_UPDATE
  (Chrome: `STREAM(2) | STREAM(2) STREAM(0)`). Not reproduced yet; both
  are flushed under one cork, control first, so the request must have
  reached `queueMeta` (resent data, or a stream opened with no window) or
  the data ring's head moved past the control stream. Small to medium.
- **Load flake: `TestResumptionMatchesBrowser/firefox-158-macos/resume-tls12`.**
  Once, in a full harness `go test -race` run (2026-10-26):
  `h1.kinds-a.test` 5 connections, 2 resumed where every sample has 1;
  the next two full runs passed. Likely a TLS 1.2 ticket that arrived
  (or did not) before the next HTTP/1.1 connection dialled, under load.
  Not investigated yet. Small.
- **Checks that are unit-only or missing.** Each small:
  - the navigation's slots over HTTP/2;
  - the coalescing conditions (see
    [coalescing conditions](#connections-and-pool));
  - the preflight's `Config.Header` rule;
  - dropping a ticket whose certificate expired;
  - a server raising MAX_FRAME_SIZE (`large-settings`);
  - Firefox's WebSocket transport end and the 10 s close fallback (see
    [Firefox's transport end](#websockets));
  - most WebSocket `ErrNotMeasured` cases;
  - the header order of the request rows only the `cors` capture shows
    (`TestCORSMatchesBrowser` compares method, path, cookies and
    `Access-Control-Request-*`, not order).

  Source: [EVIDENCE.md](../profiles/EVIDENCE.md).
- **The QUIC recorder groups datagrams by client address**, so a
  migrating client would show as two. Source:
  [decision 0026](decisions/0026-http3-capture.md).
- **Flaky or unexplained runs**:
  - one Safari `http1` run in six had samples whose first requests
    differed (the diff was not kept);
  - Safari can fail the first sample of the scenario after
    `cookies-edge2` ("no request within 1m30s");
  - Safari's nytimes replay reaches 1217 of 1286 requests, cause not
    established;
  - Safari 27's `h3-page` capture holds a second TCP connection from
    another system client (a capture artefact).

  Source: [kinds report](../internal/harness/reports/kinds-2026-10-23.md);
  [`http3_test.go`](../internal/harness/e2e/http3_test.go).
- **Capture ergonomics**: browsers come to the front during captures.
  `cmd/refresh` runs Chrome and Firefox on the second Mac, so only Safari 27
  still takes the capturing Mac's screen (options: `open -g`, batching
  runs); closing Safari's tab by AppleScript times out on the second Mac.
  Source: [capturing.md](capturing.md).
- **Parallel refresh: covered-window timer throttling.** `cmd/refresh`
  captures Chrome and Firefox at once on the second Mac in quick mode.
  Nothing a capture depends on is shared (ports, servers, no focus or
  system mouse), but whether a browser throttles the timers of a window
  entirely covered by the other's is unchecked; it would stretch the
  pauses of `h3-discovery` and the WebSocket pages. Closing it: one
  parallel quick run compared with a `-serial` one (page timings in the
  h3 captures); until then, if those e2e tests fail on timing after a
  parallel run, recapture with `-serial`. Detection relevance low
  (timing of the capture, not of mimicry); small.
- **Planned tools not built**: browser captures as a scheduled GitHub
  Action, and server-stack handshake checks (Go, OpenSSL, BoringSSL,
  nginx).
- **`internal/quic` maintenance**: the upstream sync from x/net is manual,
  the linters other than depguard are off for the package (mimicry's lines
  there are checked by hand), and `UPSTREAM.md` does not yet say the
  interop command dials through a wrapped `PacketConn`
  (`udp_packetconn.go`). Source:
  [`internal/quic/UPSTREAM.md`](../internal/quic/UPSTREAM.md);
  [quic-interop report](../internal/harness/reports/quic-interop-2026-10-25.md).
- **Refactors not done**: one internal request value, splitting the
  server package and moving the Fetch-standard logic to an internal
  package (left out to limit the refactor); `origin()` knows default
  ports for `http` and `https` only (harmless while WebSocket URLs are
  mapped first).
- **The clients comparison is not independent evidence**: mimicry's
  profiles come from the same captures, and the comparison stops after
  the second request and has no HTTP/3. Source:
  [clients report](../internal/harness/reports/clients-2026-10-23.md).
- **Inherited captures miss rule changes until a full refresh.** A
  profile refreshed in quick mode is checked, for every scenario its
  `capturesFrom` names (cookies, CORS, redirects, proxies, connections,
  resumption, the HTTP/3 failure model...), against an earlier version's
  captures with version strings rewritten, never against its own
  traffic. A rule the browser changed there is missed until that
  browser's next full refresh. Differs: possibly anything those scenarios
  pin. Detection: medium at worst (the fingerprint layer is always
  recaptured). User-visible: possibly (a cookie or CORS rule). Closing it:
  a full refresh before each release, and whenever a quick capture shows
  more than version strings changed; no period is set yet. Source:
  [decision 0035](decisions/0035-version-policy-and-capture-inheritance.md).
- **Version tokens the rewriter does not know.** `capture.Rewriter`
  rewrites the user agent's version token and the whole `sec-ch-ua`. A
  browser sending its version elsewhere by default (a high-entropy client
  hint, a new header) fails every inherited comparison until recaptured
  or taught to the rewriter. Small. Source:
  [decision 0035](decisions/0035-version-policy-and-capture-inheritance.md).
- **A per-request hook with the plan** (API).
  - Differs: nothing on the wire. Logging what each request sent takes a
    `Transport.Plan` call of your own, or a wrapping `http.RoundTripper`;
    tls-client users have pre- and post-request hooks. A `Config.OnRequest`
    called with each request's plan as it is sent would cover it.
  - Detection: none. User-visible: convenience only.
  - Close: a decision record, then the hook; small.
  - Source: [coming from tls-client](concepts/coming-from-tls-client.md#building-a-client)
    (`WithPreHook`, `WithPostHook`).
- **More profiles, and a way to make them** (catalogue).
  - Differs: the profiles are desktop ones: five on macOS (Chrome,
    Firefox, Safari) and Chrome on Windows
    ([decision 0047](decisions/0047-windows-profiles.md)). There is no iOS
    Safari, Android Chrome, Linux, or Windows Firefox or Edge profile, and
    nothing documents how a capture of your own becomes a profile.
  - Shows: sites whose mobile endpoints or app traffic a caller imitates,
    or sessions tied to a user agent from elsewhere.
  - Detection: high where another platform's client is expected (every
    request carries the platform). User-visible: no profile to pick.
  - Close: iOS Safari and Android Chrome first, then Firefox and Edge on
    Windows, each a capture setup and a refresh (large); a guide to making
    a profile from your own captures with `cmd/refresh` (medium). Each
    needs a decision record.
  - Source: [coming from tls-client](concepts/coming-from-tls-client.md#where-mimicry-is-weaker);
    [decision 0035](decisions/0035-version-policy-and-capture-inheritance.md).
- **Chrome on Windows from one machine** (Chrome on Windows).
  - Differs: not measured. `chrome-156-windows` comes from one Windows 10
    PC (x64, English), on loopback. Chrome's user agent says
    `Windows NT 10.0` on Windows 11 too by its source (a lead, not
    evidence); no Windows 11 or ARM capture shows what else might differ.
    The platform version a site can ask for with `Accept-CH`
    (`sec-ch-ua-platform-version`) is not captured on any platform, and
    mimicry sends no high-entropy hints.
  - Shows: every request, if Windows 11 or ARM differs; sites that ask
    for high-entropy client hints.
  - Detection: low. User-visible: no.
  - Close: a refresh on a Windows 11 PC; small once the machine exists.
  - Source: [decision 0047](decisions/0047-windows-profiles.md).
