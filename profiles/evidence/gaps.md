# Not covered by any claim

Known differences from the browsers, and behaviour no capture measured, with no work planned.

- Each gap below gives the same four facts:
  - **Browsers**: the browsers or profiles it affects.
  - **What differs**: what mimicry does against the browser, and when it shows.
  - **Detection**: how likely a vendor is to see it.
  - **To close**: what closing it would take, or "not planned" with its decision.
- By [decision 0029](../../docs/decisions/0029-scope-what-gets-modelled.md), a gap is closed when a
  vendor is seen checking it or a caller notices it.
- Open work is tracked as [GitHub issues](https://github.com/roachadam/mimicry/issues).
- A gap that is closed is removed here, with its pinned gap entry.

## TLS and below

- **TCP/IP fingerprint**
  - **Browsers**: All.
  - **What differs**: The SYN is the host OS's (or the proxy's).
  - **Detection**: High when the OS differs from the profile's, none on it.
  - **To close**: Not planned ([0029,
    amendment](../../docs/decisions/0029-scope-what-gets-modelled.md#amendment-2026-10-30-the-tcpip-fingerprint)).
- **Real ECH in profiles without an `ech` capture**
  - **Browsers**: `chrome-155-macos`, `chrome-156-macos`, `chrome-156-windows`, Firefox 158, Safari
    26, the iPhone profiles.
  - **What differs**: No `tls.realEch`, so no HTTPS lookup and GREASE ECH everywhere, where Chrome
    and Firefox of later versions send real ECH to a host publishing a config (Chrome 157 on Windows
    was not captured: no `chrome-157-windows` profile).
  - **Detection**: Medium for the Chrome and Firefox ones on such hosts, none where the browser
    ignores configs as Safari 27 does.
  - **To close**: An `ech` capture of each.
- **ECH on WebSocket and proxy connections**
  - **Browsers**: Chrome, Firefox.
  - **What differs**: A WebSocket's own HTTP/1.1 connection (`tls.websocketH1`), the TLS to an
    `https://` proxy, and every connection through a proxy keep GREASE ECH and look nothing up; no
    capture shows what the browsers do there.
  - **Detection**: Low to medium (only a host publishing a config can tell).
  - **To close**: `ech` captures of a WebSocket and through each proxy kind
    ([0068](../../docs/decisions/0068-real-ech.md)).
- **Whether the browser sees the record**
  - **Browsers**: Chrome.
  - **What differs**: Chrome on macOS resolving through getaddrinfo (with a scoped resolver,
    `/etc/resolver/test`) asked for no HTTPS record and sent GREASE; with secure DNS it asked and
    sent ECH, which is what mimicry always does; a user's Chrome may take either path by its DNS
    settings.
  - **Detection**: Low.
  - **To close**: Captures of Chrome's default DNS path on each platform.
- **DNS queries around ECH**
  - **Browsers**: Chrome, Firefox, Safari.
  - **What differs**: mimicry asks HTTPS beside Go's A and AAAA, in plain DNS to the system's
    nameservers (or `Config.Resolver`'s `Dial`); Chrome asked A and HTTPS (no AAAA) and Firefox
    HTTPS and A (IPv6 off in the harness), both over DoH in the captures but Firefox on macOS;
    Safari asks HTTPS, A and AAAA, where a Safari profile asks no HTTPS.
  - **Detection**: None at the server, only the resolver sees the queries.
  - **To close**: Not planned.
- **Firefox's resumed GREASE ECH size**
  - **Browsers**: Firefox.
  - **What differs**: Fixed at 400 bytes (432 over QUIC), measured against one ticket size.
  - **Detection**: Low.
  - **To close**: A `resume` capture with other ticket sizes
    ([report](../../internal/harness/reports/hello-record-2026-10-24.md)).
- **Firefox's next connection after ECH retry configs**
  - **Browsers**: Firefox.
  - **What differs**: Each profile records what its own `ech` capture showed in every sample
    (`firefox-159-macos`, the system's resolver: the DNS config offered again, without the session;
    `firefox-159-windows` and `firefox-159-linux`, Firefox's own DoH: no ECH, the retry connection's
    session resumed with GREASE ECH and the real server_name, `tls.realEch.retryPlain`), but an
    uncommitted trial on macOS through Firefox's own DoH showed both in one run (the latter in 1 of
    3 samples), so Firefox picks between them by something the captures do not show.
  - **Detection**: Low.
  - **To close**: More `ech` samples per platform and resolver.
- **A WebSocket's full hello**
  - **Browsers**: Chrome, Firefox.
  - **What differs**: `tls.websocketH1` captured resumed only; first socket with no ticket.
  - **Detection**: Low.
  - **To close**: A `websocket` capture on a fresh host.
- **HelloRetryRequest cookie position**
  - **Browsers**: Windows, iOS, Chrome 155, Safari 26; every QUIC hello.
  - **What differs**: The uTLS fork places it at random among the extensions that are not pinned.
  - **Detection**: Low.
  - **To close**: An `hrr` capture on each machine, and a QUIC server sending a HelloRetryRequest
    with a cookie.
- **Resumption forms**
  - **Browsers**: All.
  - **What differs**: TCP 0-RTT, several tickets at once, session-ID resumption never captured;
    mimicry uses one single-use ticket per key.
  - **Detection**: Low.
  - **To close**: `resume` captures.
- **TLS 1.2 ticket lifetime**
  - **Browsers**: Chrome, Firefox.
  - **What differs**: Used until the certificate expires; long TLS 1.2 sessions.
  - **Detection**: Low.
  - **To close**: A long `resume-tls12` capture.
- **Resumed hello record version**
  - **Browsers**: All.
  - **What differs**: Assumed 0x0301, not compared.
  - **Detection**: None.
  - **To close**: Add it to the resumed-hello diff.
- **Client certificates from Safari**
  - **Browsers**: Safari, every iPhone profile.
  - **What differs**: Not capturable: Safari 27 sent an empty Certificate holding the identity in a
    temporary keychain, and stalled with it in the login keychain (2026-11-08); nothing installs an
    identity on the iPhone; no `tls.clientAuth`, so a certificate is `ErrNotMeasured`.
  - **Detection**: None (refused).
  - **To close**: A way to give Safari an identity without a prompt
    ([0064](../../docs/decisions/0064-client-certificates.md)).
- **Client certificates over QUIC**
  - **Browsers**: All.
  - **What differs**: No browser was captured answering a CertificateRequest over QUIC; with
    `GetClientCertificate` set, a server asking over HTTP/3 sends the request, and the origin's
    later ones, over TCP, where the certificate is sent as measured (not counted as a QUIC failure).
  - **Detection**: Low (a browser might answer over QUIC).
  - **To close**: An h3 client-certificate capture.
- **A CertificateRequest not allowing the browser's scheme**
  - **Browsers**: Chrome, Edge, Brave, Firefox.
  - **What differs**: What a browser does when the server's `signature_algorithms` leave out the
    scheme it signs the key with is not captured; mimicry fails the request with `ErrNotMeasured`.
  - **Detection**: None (refused).
  - **To close**: A capture server sending a narrower list.
- **Coalescing onto a connection with a client certificate**
  - **Browsers**: Chrome, Edge, Brave, Firefox.
  - **What differs**: Not captured (no scenario has two hosts on one certificate); mimicry never
    coalesces onto a connection that sent a certificate or resumed a session that did, as Chrome's
    and Firefox's source say they do not.
  - **Detection**: Low.
  - **To close**: A client-certificate scenario with two hosts on one certificate.
- **Firefox's speculative connections to a server asking for a certificate**
  - **Browsers**: Firefox.
  - **What differs**: Connections that carry no request, some dropped at the CertificateRequest,
    some answering it with an empty Certificate (55 and 115 over 36 samples, no pattern across
    samples); mimicry makes none.
  - **Detection**: Low (a connection count).
  - **To close**: Not planned, as Chrome's preconnect ([0023,
    amendment](../../docs/decisions/0023-connection-limits-and-coalescing.md#amendment-2026-10-30-no-preconnects);
    pinned in `clientCertSpeculative`).
- **Chrome's preconnect meeting a CertificateRequest**
  - **Browsers**: Chrome.
  - **What differs**: Dropped like the request's first connection, so Chrome drops two connections
    to the page's host where mimicry drops one; see Chrome's preconnect below (pinned in
    `clientCertPreconnects`).
  - **Detection**: As [Chrome's preconnect](#connections).
  - **To close**: As [Chrome's preconnect](#connections).
- **Client key types**
  - **Browsers**: Chrome, Edge, Brave, Firefox.
  - **What differs**: Only ECDSA P-256 and RSA-2048 identities were captured; any other key (P-384,
    P-521, Ed25519) is `ErrNotMeasured`, and RSA of another size is assumed to sign as
    RSA-2048 does.
  - **Detection**: None for refused keys.
  - **To close**: A scenario per key type.
- **Client certificate chains**
  - **Browsers**: Chrome, Edge, Brave, Firefox.
  - **What differs**: Every browser sent its identity's leaf alone, but none held an identity with
    intermediates, so what a browser sends then is not measured; mimicry sends the chain the caller
    gives.
  - **Detection**: Low.
  - **To close**: A capture with an intermediate CA.
- **No client identity**
  - **Browsers**: Chrome, Edge, Brave.
  - **What differs**: Whether Chrome drops the first connection when it holds no identity to send is
    not captured; without `GetClientCertificate` mimicry answers on the first connection with an
    empty Certificate.
  - **Detection**: Low.
  - **To close**: A client-certificate scenario with no identity installed.
- **Parallel HTTP/1.1 connections resumed with a client certificate**
  - **Browsers**: Brave on Windows.
  - **What differs**: On `client-cert-ec` Brave resumed 3 of its 5 HTTP/1.1 connections to
    `h1.kinds-a.test` in 3 of 3 samples, where mimicry resumed 2 (as Brave on macOS, 6 of 6): how
    many open before the first one's ticket arrives is timing, which no field holds.
  - **Detection**: Low.
  - **To close**: None planned (`clientCertH1Resumes`).

## HTTP/1.1

- **HTTPS upgrades and HSTS**
  - **Browsers**: Safari, Chrome.
  - **What differs**: By [decision 0016](../../docs/decisions/0016-literal-urls-no-https-upgrade.md)
    requests go to the URL given, with no TLS attempt first.
  - **Detection**: Low to medium.
  - **To close**: Captures on ports 80 and 443, then an opt-in option
    ([report](../../internal/harness/reports/plain-http-2026-10-24.md)).
- **`Cookie` on a non-secure origin**
  - **Browsers**: All.
  - **What differs**: Its slot in plain-HTTP lists is not measured.
  - **Detection**: Medium on plain-HTTP targets.
  - **To close**: A `kinds-plain` run with cookies.
- **Idle close**
  - **Browsers**: All.
  - **What differs**: 90 s on every protocol; how and when browsers close an idle connection is not
    captured (longest gap 35 s).
  - **Detection**: Low.
  - **To close**: A long idle capture
    ([0040](../../docs/decisions/0040-idle-connection-cleanup.md)).

## HTTP/2

- **Chrome 155's opening records**
  - **Browsers**: Chrome 155.
  - **What differs**: From Chrome for Testing, not branded Chrome.
  - **Detection**: None expected.
  - **To close**: A branded `resume` capture.
- **A connection opened before its request**
  - **Browsers**: Safari, Firefox; Chrome on iOS.
  - **What differs**: A page opened from outside a tab (a typed URL, a bookmark) has its first
    connection opened before the request exists, so the browser sends the opening alone,
    acknowledges the server's SETTINGS, and sends HEADERS 7–412 ms later (Safari 27 macOS 174 of
    185 such connections, Safari 26 macOS 154 of 161, Firefox macOS 192 of 212; Firefox on Windows,
    Safari and Chrome on iOS 32–67%, varying between samples of one page); every other connection
    (1038 of 1047 for Safari 27 macOS) sends HEADERS at once and ACKs after, as mimicry always does.
    mimicry has the request when it connects, so even a typed navigation sends HEADERS first.
  - **Detection**: Low to medium (a typed navigation's first connection).
  - **To close**: Not planned, intentionally ([0042,
    amendments](../../docs/decisions/0042-http2-opening-records.md#amendment-2026-11-04-counted-kept-not-planned),
    [#80](https://github.com/roachadam/mimicry/issues/80)).
- **Chrome on Windows's ACK in the same millisecond**
  - **Browsers**: Chrome on Windows.
  - **What differs**: On 953 of 1552 connections after a page's first, the SETTINGS ACK went before
    the first HEADERS within the same millisecond over loopback, a scheduling race a local capture
    cannot settle for real paths; mimicry sends HEADERS first.
  - **Detection**: Low.
  - **To close**: Real-network captures.
- **Chrome's re-prioritisation**
  - **Browsers**: Chrome.
  - **What differs**: A PRIORITY frame mid-load, and a mid-upload stream kept in its lists
    ([INFERENCE]); never sent.
  - **Detection**: Medium.
  - **To close**: Model `OnStreamUpdate` against the page traces.
- **Several requests in the opening record**
  - **Browsers**: Safari on iOS.
  - **What differs**: With three requests waiting as a connection opened, the iPhone put all three
    HEADERS in the opening's TLS record; `http2.openingRecords` describes the first request's only.
  - **Detection**: Low.
  - **To close**: A capture with requests of known count waiting, and a record rule for them.
- **Upload of unknown length**
  - **Browsers**: All.
  - **What differs**: Ends with an empty END_STREAM DATA frame; streamed bodies.
  - **Detection**: Low to medium.
  - **To close**: A streaming-upload capture.
- **Idle origin moved to new TCP**
  - **Browsers**: Chrome on macOS and Linux, Edge, Brave.
  - **What differs**: After a broken HTTP/3 origin, a new TCP connection after 6–10 s idle (Linux:
    8.0 s, 3 of 3 samples; Edge: 8.0 s, and again after 10.0 s, 3 of 3; Brave: one new connection,
    3 of 3 on macOS, Windows and Linux).
  - **Detection**: Low.
  - **To close**: Captures to find the cause
    ([`h3EdgeGaps`](../../internal/harness/e2e/http3_edge_test.go)).
- **Brave on Linux's early second connection**
  - **Browsers**: Brave on Linux.
  - **What differs**: In 1 of 3 `h3-broken` samples Brave opened a second, resumed TCP connection to
    the page's second host 4.0 s after its first, which carried nothing until the page's next
    request to that host 35 s later; mimicry opens none.
  - **Detection**: Low.
  - **To close**: More samples (h3gen's `hold`, `braveLinuxBrokenVaries`).
- **Upload DATA chunking**
  - **Browsers**: All; Firefox entirely.
  - **What differs**: 65535-byte chunks match only the first chunk; uploads over 64 KiB.
  - **Detection**: Low.
  - **To close**: Upload captures of several sizes.
- **Blob and File bodies joined with the head**
  - **Browsers**: Chrome.
  - **What differs**: Chrome sends a Blob or File body apart from its head even when the two fit
    1400 bytes (`http1-upload`'s `/up/blob-100`, `-500`); mimicry cannot tell a Blob from bytes and
    joins every small body up to `http1.uploadRecords.joinUpTo`; small file uploads over HTTP/1.1.
  - **Detection**: Low.
  - **To close**: None planned (decision 0052).
- **Chunked HTTP/1.1 request bodies**
  - **Browsers**: All.
  - **What differs**: No browser sends one from a page (Chrome and Safari refuse a ReadableStream
    body over HTTP/1.1, Firefox sends its string form); mimicry's chunked body, which a caller asks
    for with an unknown length, goes out with its head as one stream in 16 KiB writes.
  - **Detection**: Low.
  - **To close**: None planned (decision 0052).
- **HTTP/2 upload records**
  - **Browsers**: All.
  - **What differs**: Browsers' upload records follow their DATA frames and flow control
    (`h2-upload`), which mimicry's HTTP/2 writer was not compared against record by record; large
    HTTP/2 uploads.
  - **Detection**: Low.
  - **To close**: An e2e comparison of `h2-upload`'s records.
- **Credit return timing**
  - **Browsers**: Firefox, Safari.
  - **What differs**: Returned at the profile's threshold, not paced by reads; large downloads.
    Firefox's largest `/big` stream increment was 3.56 MB in 3 samples on Windows and 3.16 MB on
    Linux, below mimicry's 4 MiB step, which 2 of 3 macOS samples sent (`creditGaps`).
  - **Detection**: Low.
  - **To close**: A read-paced model.
- **REFUSED_STREAM**
  - **Browsers**: Safari, iOS profiles, Chrome 155.
  - **What differs**: Unmeasured, so a refused stream retires the connection and the request goes on
    a new one, where Chrome 156 and Firefox 158 resend on the same (`http2.refusedStreamSends`); a
    refused extended CONNECT (HTTP/2 WebSocket) opens the socket on a connection of its own in every
    profile.
  - **Detection**: Low.
  - **To close**: A refusing scenario.
- **Error-path frame order**
  - **Browsers**: Chrome.
  - **What differs**: RST_STREAM before discarded credit, GOAWAY before close ([INFERENCE]); not
    checked.
  - **Detection**: Low.
  - **To close**: Error injection.

## QUIC and HTTP/3

- **HTTP/3 and ECH from DNS HTTPS records**
  - **Browsers**: All; ECH over QUIC: Chrome, Firefox.
  - **What differs**: Records are looked up for their ECH configs only, and only by profiles with
    `tls.realEch` ([0068](../../docs/decisions/0068-real-ech.md)); their `alpn` is never used, so a
    site whose record names `h3` is reached over TCP until `Alt-Svc` moves it (for an ECH host,
    where `altSvcIgnored` holds, never), and no QUIC hello carries real ECH, only GREASE.
    Uncommitted `ech-h3` trials (a record with alpn `h3,h2` and an ECH config, QUIC on UDP 443) sent
    Chrome 157 and Firefox 159 straight to QUIC with real ECH (outer `server_name` the public name),
    as such a record would in the wild (`cloudflare-ech.com`'s, 2026-11-10: alpn `h3,h2` and `ech`);
    mimicry stays on TCP with ECH there.
  - **Detection**: Medium.
  - **To close**: HTTP/3 discovery from records, and `quictrace` finding the keys of an ECH-accepted
    QUIC connection to capture its inner hello
    ([report](../../internal/harness/reports/http3-2026-10-25.md)).
- **`Alt-Svc` on ECH connections where not measured**
  - **Browsers**: Chrome on Linux, Edge, Brave, Firefox on Windows and Linux.
  - **What differs**: `realEch.altSvcIgnored` comes from `ech-h3`, captured on macOS only, so these
    profiles move a host whose ECH config they use to HTTP/3 (with GREASE ECH) after its `Alt-Svc`,
    where Chrome and Firefox on macOS stayed on TCP with ECH.
  - **Detection**: Medium.
  - **To close**: An `ech-h3` capture on each machine.
- **A returning browser's first request**
  - **Browsers**: All; Firefox most.
  - **What differs**: A saved alternative starts like any known one; no browser captured restarting.
  - **Detection**: Low.
  - **To close**: Restart captures.
- **Dead-path waits**
  - **Browsers**: Firefox, Safari.
  - **What differs**: A sent request on a silent path waits as the browser's does (30 s, 12 s).
  - **Detection**: None.
  - **To close**: A dead-path signal and a resend rule.
- **Safari's other kinds**
  - **Browsers**: Safari.
  - **What differs**: Only `fetch()` measured over HTTP/3, so other kinds stay on TCP; also its
    QPACK table, PRIORITY_UPDATE, bodies, close reply.
  - **Detection**: Medium.
  - **To close**: A fuller `h3-page` capture.
- **Firefox's declined TCP beside a 425**
  - **Browsers**: Firefox.
  - **What differs**: It opens and drops a TCP connection on most 425 retries.
  - **Detection**: Low.
  - **To close**: More samples ([`h3EdgeGaps`](../../internal/harness/e2e/http3_edge_test.go)).
- **When Firefox's held request leaves**
  - **Browsers**: Firefox.
  - **What differs**: With its Finished in 31 of 35, after it in 4; mimicry always with it.
  - **Detection**: Low.
  - **To close**: Thread timing, nothing likely (`sampleGaps`).
- **Safari's OPTIONS fetch**
  - **Browsers**: Safari.
  - **What differs**: Not in its kinds captures, so refused (`ErrNotMeasured`).
  - **Detection**: Low.
  - **To close**: An OPTIONS fetch on the kinds page
    ([`h3EdgeGaps`](../../internal/harness/e2e/http3_edge_test.go)).
- **Safari on iOS's beacon over HTTP/3**
  - **Browsers**: Safari 27 on iOS.
  - **What differs**: The iPhone sent `h3-page`'s beacon on a QUIC connection of its own beside the
    page's HTTP/2 one (3 of 3; Safari 26 on the iPhone kept the page on TCP, as on macOS); mimicry
    keeps it on TCP, Safari's kinds being measured over HTTP/3 for `fetch()` only.
  - **Detection**: Low.
  - **To close**: A fuller `h3-page` capture (`h3Gaps`).
- **Safari on iOS's QUIC failure rules**
  - **Browsers**: Safari on iOS.
  - **What differs**: For Safari 27, fallback, keep-alive, acknowledgement and credit copied from
    Safari 27 on macOS (**C**), loss, key update, 0-RTT, dial delays and close on GOAWAY not
    measured; for Safari 26, the failure pair's structure measured (**W**: keep-alive, path
    degrading, close on GOAWAY, the fallback's mode), its timing, acknowledgement and credit copied
    from Safari 26 on macOS (**C**), the rest of loss, key update, 0-RTT and dial delays not
    measured; the iPhone's second Initial pair in a blackholed attempt and its unpadded Initials
    (1038 and 1129 bytes) not imitated (the stack's defaults, no 0-RTT).
  - **Detection**: Low.
  - **To close**: The failure pair's timing over a wired path, and an iPhone `h3-early` capture.
- **Chrome and Firefox on iOS's extra Initial probes**
  - **Browsers**: Chrome and Firefox on iOS.
  - **What differs**: In a blackholed QUIC attempt the iPhone's Chrome sent one or two retransmitted
    Initial pairs (a 1038-byte unpadded datagram and a 1200-byte one) 28–137 ms in, before or as the
    raced TCP connection opened, 6 of 6 attempts (the "6 Initials"; Safari 26 on the iPhone: none or
    one pair); Firefox on the iPhone the same in both blackholed attempts, and once in `h3-page`
    when the server's first flight came late (`h3Gaps`); mimicry's attempt probes as its QUIC stack
    does.
  - **Detection**: Low.
  - **To close**: The iPhone's loss rules over a wired path.
- **When an abandoned attempt stops**
  - **Browsers**: Safari; Chrome and Firefox on iOS.
  - **What differs**: The browser drops its QUIC attempt once the request is on the TCP connection.
    Safari on macOS sends its 0-RTT close 8–11 ms after its TCP ClientHello and 0–2 ms after the
    request reached the server (6 of 6). Chrome on the iPhone sent an Initial pair 0–8 ms after the
    ClientHello in 3 of 6 blackholed attempts. mimicry drops the attempt as the request leaves for
    TCP, before its dial (`internal/engine/native`: `await`, `roundTripEarly`), so its 0-RTT close
    goes out before its TCP ClientHello and it probes no later than the race delay.
  - **Detection**: Low.
  - **To close**: Closing it means dropping the attempt on the TCP connection's first request
    ([0028,
    amendment](../../docs/decisions/0028-quic-failure-model.md#amendment-2026-11-03-what-counts-as-an-abandoned-attempt)).
- **Chrome's insecure-form warning**
  - **Browsers**: Chrome on iOS.
  - **What differs**: Before submitting a form over plain HTTP Chrome asks ("Send anyway"), Safari
    does not; behaviour on the screen, not on the wire, so not modelled; `kinds-plain` on the iPhone
    needs someone to tap it (docs/capturing.md).
  - **Detection**: None.
  - **To close**: None stated.
- **Firefox on iOS's HTTP/1.1-only ALPN**
  - **Browsers**: Firefox on iOS.
  - **What differs**: In two three-sample `http1` runs the capture tool's log showed sample 1's
    ClientHello offering ALPN `http/1.1` alone and sample 3's `h2, http/1.1`, in both runs; no rule
    found (the committed `http1` samples are single-sample runs, and its trace keeps the
    HTTP/1.1 requests, not the ClientHello's ALPN, so no test can check it); not imitated.
  - **Detection**: Low.
  - **To close**: An `http1` capture that records the ALPN per sample.
- **Firefox on iOS's redirect stall**
  - **Browsers**: Firefox on iOS.
  - **What differs**: About one `redirects2` or `redirects2-cookies` sample in three stalled after
    the "too many redirects" page, even with Firefox restarted before each sample, and never opened
    the next URL; behaviour on the screen, not on the wire, so not modelled; the samples were taken
    as single-sample runs (docs/capturing.md).
  - **Detection**: None.
  - **To close**: None stated.
- **Safari's close on GOAWAY with requests open**
  - **Browsers**: Safari.
  - **What differs**: Every captured GOAWAY found no request open, and Safari closed at once; with
    requests open mimicry waits for them, then closes (an assumption, decision 0050).
  - **Detection**: Low.
  - **To close**: A GOAWAY that leaves a request in flight.
- **Safari's pair on a 0-RTT connection**
  - **Browsers**: Safari.
  - **What differs**: With a GET and a POST made together, Safari sent the control stream alone,
    then the GET (6 of 6); mimicry sends the GET in the control stream's packet, as for a lone GET
    (`earlyStreamsWithRequest`).
  - **Detection**: Low.
  - **To close**: More samples to say what decides it.
- **Chrome 156 Windows's GREASE transport parameter**
  - **Browsers**: Chrome on Windows.
  - **What differs**: 3 resumed hellos of its edge captures carry a 15-byte one, beyond the
    profile's 14, which the `h3-discovery` and `h3-page` connections set.
  - **Detection**: Low.
  - **To close**: Derive the maximum from the edge captures too (h3gen reads them for resumed first
    flights only where the h3 captures have none).
- **Chrome's packet numbers in some resumed first flights**
  - **Browsers**: Chrome.
  - **What differs**: In some edge resumed attempts (`h3-broken` sample 1 connection 3, `h3-early`
    sample 1 connection 2) the 0-RTT packets do not continue the shared numbering the `h3-discovery`
    connections follow; not checked (as the GREASE line).
  - **Detection**: Low.
  - **To close**: A look at those flights.
- **nginx drops Chrome's port migration**
  - **Browsers**: Chrome.
  - **What differs**: Nginx answers nothing on the new path, so a 30 s stall; mimicry migrates as
    Chrome.
  - **Detection**: None.
  - **To close**: A capture of Chrome doing it.
- **Chrome after declining to move**
  - **Browsers**: Chrome.
  - **What differs**: A server's `disable_active_migration` keeps Chrome on its degrading path
    ([Chromium
    source](https://chromium.googlesource.com/chromium/src/+/refs/tags/156.0.8078.85/net/quic/quic_chromium_client_session.cc#3282),
    a lead; [RFC 9000 §18.2](https://www.rfc-editor.org/rfc/rfc9000#section-18.2)), and mimicry
    stays with its keep-alives, probes and idle timeout; what Chrome does then (probes, close
    timing, where the stuck request goes) is not captured.
  - **Detection**: Low.
  - **To close**: A Chrome `h3-broken` capture against a server sending the parameter.
- **Chrome's blackhole close**
  - **Browsers**: Chrome, Edge, Brave.
  - **What differs**: Chrome closes after 54–61 s and resends on TCP (Edge and Brave 54.4–54.5 s,
    macOS and Windows, Brave on Linux too); mimicry falls back at 30 s.
  - **Detection**: Low.
  - **To close**: Not planned ([0028,
    amendment](../../docs/decisions/0028-quic-failure-model.md#amendment-2026-10-30-blackhole-detection-and-resumed-probes)).
- **Edge on Windows's early path move**
  - **Browsers**: Edge on Windows.
  - **What differs**: In 1 of 3 `h3-broken` samples Edge moved its silenced connection to a new path
    after 2.91 of its PTOs, 13 ms after its second probe, where the others and every other Chromium
    sample moved after about 4; mimicry moves after 4 (`afterPtos`).
  - **Detection**: Low.
  - **To close**: More Windows samples, to tell a rule from a timer (`pathMoveGaps`).
- **Brave's path move timing**
  - **Browsers**: Brave, macOS and Windows.
  - **What differs**: Brave moved its silenced connection 3.3–3.9 of its PTOs after its last request
    by the e2e test's measure (macOS 3.41, 3.48, 3.72; Windows 3.29, 3.87, 3.89), always after its
    second probe; mimicry moves after 4 (`afterPtos`, as Chrome's; 3 would race the second probe,
    [0067,
    amendment](../../docs/decisions/0067-brave-profiles.md#amendment-2026-11-16-braves-path-move-after-4-ptos-as-chromes)),
    so the earlier moves differ by about 20–25 ms.
  - **Detection**: Low.
  - **To close**: More samples, to tell a rule from a timer (`pathMoveGaps`).
- **Resumed attempts' probes**
  - **Browsers**: Chrome, Firefox.
  - **What differs**: Chrome's 4 Initials and lone 0-RTT datagrams, Firefox's varied probes; only to
    a silent server.
  - **Detection**: Low.
  - **To close**: Not planned (same amendment).
- **Firefox's short-lived QUIC side connections**
  - **Browsers**: Firefox, macOS, Windows and Linux.
  - **What differs**: Firefox intermittently opens QUIC connections beside the page's that carry no
    request and closes each 1–5 ms after its handshake, H3_NO_ERROR: Firefox 158 on Windows in 2 of
    its 36 HTTP/3 page samples (`h3-broken`, `h3-download`, one per request: 14 and 7); Firefox
    159 in 3 of 6 macOS and 4 of 6 Windows `h3-discovery`/`h3-page` samples with locally trusted
    roots (8–20 per sample, about 300 ms apart), after which later connections to the host in the
    sample resume nothing and carry no token, and the server's close in `h3-discovery` got no TCP
    race; on Linux in about half of the `h3-*` samples (15 per sample) and in every
    `h3-streams-blocked` sample (6, each within 4 ms of a fetch), and in those samples no GET or
    HEAD went in 0-RTT and the resumed attempt after the server's close ended silently; mimicry
    opens none; trigger unknown.
  - **Detection**: Low.
  - **To close**: More samples to find when (h3gen's `sampleGaps`, kindsgen's `h3AltUsedGaps`,
    `h3EdgeGaps`).
- **Firefox on Windows's resumed attempt**
  - **Browsers**: Firefox on Windows.
  - **What differs**: After the server's close, its resumed attempt first probed 1.7–1.9 s after its
    first flight (`cachedRtt` false) and gave up at 9.8 s or 10.2 s, or (1 of 3 samples) probed from
    a cached RTT and ended silently at 4.5 s with nothing held (6 further samples: 9.8, 9.8, 10.2,
    10.3, 5.0 s and one silent end); mimicry gives up at 9.8 s; only to a silent server.
  - **Detection**: Low.
  - **To close**: More samples to find what decides (h3gen's `sampleGaps`).
- **Firefox on Windows's late timers**
  - **Browsers**: Firefox on Windows.
  - **What differs**: Its keep-alive PING came 15.00–15.39 s after its idle timer restarted, 4 of
    9 samples more than 50 ms late, and requests waiting on a silenced connection moved to TCP
    73–470 ms after the server's idle record (macOS 7–22 ms); mimicry's timers fire on time.
  - **Detection**: None.
  - **To close**: Nothing likely (h3gen's `sampleGaps`).
- **How long an origin stays broken**
  - **Browsers**: All.
  - **What differs**: For the Transport's life; Chrome's source backs off 5 min.
  - **Detection**: Low to medium.
  - **To close**: A multi-page, minutes-long capture.
- **Locally trusted roots**
  - **Browsers**: Chrome, Firefox.
  - **What differs**: HTTP/3 captured with QUIC forced; a public root may race differently.
  - **Detection**: Unknown.
  - **To close**: A capture with a public certificate.
- **HTTP/3 lists with cookies**
  - **Browsers**: All.
  - **What differs**: No cookie went over HTTP/3, so its slot (and Firefox's `alt-used` beside it)
    is assumed.
  - **Detection**: Medium.
  - **To close**: `h3-page` with cookies.
- **Firefox's `alt-used` on credential-less requests**
  - **Browsers**: Firefox.
  - **What differs**: Its source omits it there; never compared.
  - **Detection**: Low.
  - **To close**: Those kinds in `h3-page`.
- **Safari's handshake loss timing**
  - **Browsers**: Safari.
  - **What differs**: Probes and give-up are the stack's defaults; heavy loss.
  - **Detection**: Low.
  - **To close**: A capture with QUIC blackholed and no TCP
    ([report](../../internal/harness/reports/quic-interop-2026-10-25.md)).
- **Firefox's v2 resumption against old lsquic**
  - **Browsers**: Firefox.
  - **What differs**: lsquic's v1-typed Retry stalls it until TCP; real Firefox there not captured.
  - **Detection**: Low.
  - **To close**: None stated.
- **Server bugs browsers hit too**
  - **Browsers**: Chrome.
  - **What differs**: lsquic before 4.10, nghttp3's PRIORITY_UPDATE limit, kwik on 0-RTT; as in
    Chrome. One unexplained close by www.akamai.com (Safari 26), not reproduced.
  - **Detection**: None.
  - **To close**: None stated.
- **Retry**
  - **Browsers**: All.
  - **What differs**: No browser captured answering a Retry.
  - **Detection**: Low to medium.
  - **To close**: A Retry-forcing server.
- **PMTU probing, ECN, DATAGRAM, ACK_FREQUENCY**
  - **Browsers**: All.
  - **What differs**: Not done or ignored; not measured; real paths.
  - **Detection**: Low to medium.
  - **To close**: Real-network captures.
- **Server QPACK dynamic table**
  - **Browsers**: All.
  - **What differs**: The decoder side is not measured.
  - **Detection**: Low to medium.
  - **To close**: Such a server.
- **A navigation's 0-RTT**
  - **Browsers**: Chrome, Edge.
  - **What differs**: On a resumed connection Chrome and Edge held a navigation (GET) until after
    the Finished, 3 of 3 `h3-resume-table` samples each, where their fetch, XHR and image GETs went
    in 0-RTT; `http3.earlyData` holds methods only, so mimicry sends the navigation in 0-RTT.
  - **Detection**: Low.
  - **To close**: A navigation rule in `http3.earlyData` (`h3EdgeGaps`).
- **Chrome's NEW_TOKEN cache**
  - **Browsers**: Chrome.
  - **What differs**: Chrome often omits a token it has (3 of 9); mimicry always presents it.
  - **Detection**: Low.
  - **To close**: Token-cache captures (`sampleGaps`).
- **Edge's second Initial in 1 byte**
  - **Browsers**: Edge and Brave.
  - **What differs**: 8 of 123 connections on macOS and 9 of 123 on Windows sent the second
    Initial's packet number in 1 byte (Brave: 7 of 150 on macOS, 12 of 150 on Windows, 8 of 156 on
    Linux), all with a token, against `shared_second_long`'s 2; nothing on the wire tells them apart
    (scenario, timing, `initial_rtt`, hello size); mimicry always sends 2.
  - **Detection**: Low.
  - **To close**: More captures, or Edge's field trials (h3gen's `sampleGaps`, `h3Gaps`).
- **Brave's late Initial ACK out of order**
  - **Browsers**: Brave on macOS.
  - **What differs**: The one connection of 21 that acknowledged the server's Initial sent that
    Initial packet after the Handshake and 1-RTT packets it led; mimicry never acknowledges an
    Initial (`ackInitial` false).
  - **Detection**: Low (h3gen's `coalescedOrder` pin).
  - **To close**: None stated.
- **Chrome's late PRIORITY_UPDATE**
  - **Browsers**: Chrome 156 and 157, Brave on Windows and Linux.
  - **What differs**: Sometimes 1 ms after HEADERS under load; mimicry sends it first.
  - **Detection**: Low.
  - **To close**: More samples (`sampleGaps`).
- **`h3-early` POST on Windows**
  - **Browsers**: Chrome and Firefox on Windows.
  - **What differs**: The replay makes both requests at once, so mimicry's POST is held.
  - **Detection**: None.
  - **To close**: Replay at the browser's issue times (`h3EdgeSchedulingGaps`).
- **Chrome's TCP delay from the smoothed RTT**
  - **Browsers**: Chrome.
  - **What differs**: 1.5 × RTT, invisible on loopback.
  - **Detection**: Low.
  - **To close**: A real-network capture.
- **Migration by Firefox and Safari**
  - **Browsers**: Firefox, Safari.
  - **What differs**: Never seen, never done; NAT rebinding.
  - **Detection**: Low.
  - **To close**: A rebinding capture.
- **Long and slow transfers**
  - **Browsers**: All.
  - **What differs**: Key updates past 37,000 packets and credit past 50 MiB not measured; no client
    key update.
  - **Detection**: Low.
  - **To close**: Longer captures.
- **Safari's connection credit across lost data**
  - **Browsers**: Safari.
  - **What differs**: Its MAX_DATA is what arrived plus the stream window
    (`quic.credit.connectionStreamWindow`), but no `h3-download` sample lost a packet (none of its
    about 37,000 server data packets went unacknowledged), so whether data beyond a gap counts
    before the gap is resent is not measured; mimicry counts the largest offset received, as RFC
    9000's flow control does, so with loss its raises run ahead of what it acknowledged by the gap
    (the download test's `lostData`).
  - **Detection**: Low.
  - **To close**: A download capture with loss.
- **Keep-alive with a short server idle timeout**
  - **Browsers**: All.
  - **What differs**: PINGs at half of it.
  - **Detection**: Low.
  - **To close**: A short-idle capture.
- **`requestsAfterConfirmed` on full handshakes**
  - **Browsers**: Chrome, Firefox.
  - **What differs**: The rule is false from the requests held out of 0-RTT, which leave before the
    handshake is confirmed; no request waited for a full handshake, so whether one ready during it
    waits is not captured.
  - **Detection**: Low.
  - **To close**: An immediate-request scenario.
- **Firefox v2 edges**
  - **Browsers**: Firefox.
  - **What differs**: A v2 connection's NEW_TOKEN not passed on; a v2-only server sending
    `version_information` untested; the resumed hello's SNI cut varies.
  - **Detection**: Low.
  - **To close**: More samples.
- **QUIC stack gaps**
  - **Browsers**: All.
  - **What differs**: The server's `preferred_address` is not used, its `max_udp_payload_size` not
    acted on.
  - **Detection**: Low ([UPSTREAM.md](../../internal/quic/UPSTREAM.md)).
  - **To close**: None stated.
- **STREAMS_BLOCKED where not captured**
  - **Browsers**: Chrome 155, Chrome 156 on Windows, Safari 26.
  - **What differs**: No `h3-streams-blocked` capture (`missingCaptures`), and none can inherit
    another's (decision 0035: from an older version of the same browser and platform only), so
    `quic.streamsBlocked` is not measured and they send none; only against servers allowing few
    streams.
  - **Detection**: Low.
  - **To close**: The capture on the Windows PC and the macOS 26 Mac.
- **Firefox's spacing of requests made together**
  - **Browsers**: Firefox, macOS, Windows and Linux.
  - **What differs**: A burst of fetches a page makes together leaves 3.2–5.7 ms apart (macOS;
    Windows 3.7–5.2, Linux 3–14), each request in its own packet; mimicry sends requests made
    together as they come, the first two of an `h3-streams-blocked` burst in one packet. Against
    quic-go, which raises its stream limit per stream acknowledged, the server then answers both in
    one packet and raises by two, and mimicry sends one STREAMS_BLOCKED per two waiting streams (7,
    13) where Firefox sends one per stream (6 7 8, 12 13 14; on Linux 2 of 3 samples sent the second
    burst's two waiting streams in one packet behind 13); with the requests 5 ms apart mimicry sends
    Firefox's (tried 2026-11-02); whether the spacing is the page's script or Firefox's network code
    is not measured.
  - **Detection**: Low ([`h3EdgeGaps`](../../internal/harness/e2e/http3_edge_test.go)).
  - **To close**: None stated.
- **Chrome's small-packet padding**
  - **Browsers**: Chrome.
  - **What differs**: Chrome's 29-byte 1-RTT packets (a lone PING, RETIRE_CONNECTION_ID or
    STREAMS_BLOCKED) carry their PADDING before the frame; mimicry's STREAMS_BLOCKED packet is the
    same 29 bytes with it after (upstream's header-protection minimum; its other small packets
    likewise, [INFERENCE]); seen only by the server, after decryption.
  - **Detection**: Low.
  - **To close**: Where Chrome pads, from its small packets.
- **Opaque transport parameters**
  - **Browsers**: Firefox, Safari 26, Safari on iOS.
  - **What differs**: `0x1d` and `0xff080808` sent verbatim, meaning unknown; Safari 27 on macOS
    stopped sending the latter, Safari 27.2 on iOS sends it (= 9, last).
  - **Detection**: None.
  - **To close**: Source research.

## Request headers and kinds

- **Page header order**
  - **Browsers**: All; XHR, Firefox's object-literal `fetch()`.
  - **What differs**: Name order, not the page's.
  - **Detection**: Medium.
  - **To close**: `Request.HeaderPlacement` by hand, no new API ([0021,
    amendment](../../docs/decisions/0021-page-headers-hash-table-order.md#amendment-2026-10-30-no-api-for-the-pages-header-order)).
- **Safari's common names**
  - **Browsers**: Safari.
  - **What differs**: Page-set `Authorization`, `Cache-Control` may enter its hash table among its
    own; only `content-type` captured.
  - **Detection**: Medium.
  - **To close**: A `headers-order` capture.
- **Safari on iOS's large page-set header sets**
  - **Browsers**: Safari on iOS.
  - **What differs**: With 26 or more page-set names its order is not CFNetwork's table as modelled,
    and its XHR puts a name set twice elsewhere; mimicry orders them as on macOS.
  - **Detection**: Low (`headersGaps` in kindsgen, `headersOrderGaps`).
  - **To close**: None stated.
- **Chrome's `content-type` role**
  - **Browsers**: Chrome.
  - **What differs**: Page header or its own, unresolved; Blink's source followed.
  - **Detection**: Low to medium.
  - **To close**: A `headers-order` capture.
- **Requests from a cross-site frame**
  - **Browsers**: All.
  - **What differs**: Their top-level site, headers and preflights are not modelled. Brave farbles
    their `Accept-Language` by its tab's page; mimicry by `Request.From`, the frame's origin.
  - **Detection**: Medium.
  - **To close**: A frame capture and an API.
- **Brave's blocking lists**
  - **Browsers**: Brave.
  - **What differs**: Shields refuse requests to listed ads and trackers before they leave; mimicry
    sends every request it is asked for (no capture page uses a listed URL).
  - **Detection**: Low (a blocked request never reaches a server).
  - **To close**: Not planned.
- **`Sec-Fetch-User` on frames and HTTP/1.1**
  - **Browsers**: Chrome, Firefox.
  - **What differs**: Either accepted on frames; Firefox's clicked form POST over HTTP/1.1 refused.
  - **Detection**: Medium.
  - **To close**: A clicked form on `kinds-http1`.
- **Pages below `/`**
  - **Browsers**: All.
  - **What differs**: The full-path `Referer` follows the standard, not a capture.
  - **Detection**: Medium if wrong.
  - **To close**: A deep kinds page.
- **A lone `%` in a path**
  - **Browsers**: All.
  - **What differs**: Browsers send it as written (`urls`, `p-lonepct`); Go's `url.Parse` refuses
    the URL, so `http.NewRequest` fails before mimicry sees it; a caller can send it with
    `req.URL.Opaque`, which mimicry sends as written (decision 0057).
  - **Detection**: None (mimicry sends nothing otherwise) (`urlsKnown`).
  - **To close**: None stated.
- **Pooling a host ending in a dot**
  - **Browsers**: Chrome, Edge, Firefox.
  - **What differs**: Chrome sends a fetch() of `host.` on `host`'s connection and opens a new one
    for a navigation; Firefox does the opposite; mimicry runs the case once and matches one of the
    two.
  - **Detection**: Low (hosts typed with a dot).
  - **To close**: Top-level-site partitioning (Chrome, decisions 0011 and 0029) and Firefox's
    connection grouping (`urlsKnown`).
- **URL rules without a `urls` capture**
  - **Browsers**: `chrome-155-macos`.
  - **What differs**: Branded Chrome 155 cannot be installed, and its testing build gives only the
    network stack's `tls.trailingDotSni`; a `\|` in a path or an invalid `xn--` label is
    `ErrNotMeasured`, every other URL as the browsers measured agree.
  - **Detection**: None.
  - **To close**: None stated.
- **A host ending in a dot over HTTP/3**
  - **Browsers**: All.
  - **What differs**: The `urls` page is TCP only, so its SNI over QUIC follows `tls.trailingDotSni`
    by inference (each browser builds both hellos from the host); with `omit` (Safari) such a QUIC
    connection keeps no ticket, since uTLS keys QUIC tickets by server name, so it never resumes.
  - **Detection**: Low.
  - **To close**: An Alt-Svc case for a trailing-dot host.
- **IPv6 literals**
  - **Browsers**: All.
  - **What differs**: Kept as written; the URL standard compresses them (`[0:0::1]` is `[::1]`); not
    captured (the capture server listens on IPv4 only).
  - **Detection**: Low.
  - **To close**: A `::1` listener and `ip6-*` cases.
- **`FontFace` loads and `crossorigin` stylesheets**
  - **Browsers**: All.
  - **What differs**: No kind.
  - **Detection**: Low to medium.
  - **To close**: Add both to the kinds page.
- **Layout-driven image priorities**
  - **Browsers**: Firefox, Chrome, Edge, Brave.
  - **What differs**: Layout changes them (`u=4`, `u=1`).
  - **Detection**: Low (`redirectsLineGaps`, `h3Gaps`, `h3PriorityGaps`).
  - **To close**: None stated.
- **Safari 26's font cookie race**
  - **Browsers**: Safari 26.
  - **What differs**: It sent a font before a cookie was set.
  - **Detection**: None (`raceCookieLabels`).
  - **To close**: None stated.
- **Edges from source only**
  - **Browsers**: Safari, all.
  - **What differs**: Safari's hash tables past 1732 names or for names over 96 characters; the
    navigation's `content-length` slot, which no request reaches.
  - **Detection**: None.
  - **To close**: None stated.
- **Bodiless POST and PUT without a `bodiless` capture**
  - **Browsers**: Chrome and Firefox on Windows, every iOS profile, `chrome-155-macos`,
    `safari-26-macos`.
  - **What differs**: No `emptyBody`, so a POST or PUT with no body goes without
    `content-length: 0`, its stream ended on HEADERS. The `cors` captures already show every
    profile's cross-site fetch PUT with no body (`put.cs`, the `maxage.cs` PUT) sending
    `content-length: 0` (Chrome and Safari in the list's place with END_STREAM on HEADERS, Firefox
    last with an empty DATA frame); kindsgen does not read them for `emptyBody` yet, and POST is
    unmeasured there.
  - **Detection**: Medium.
  - **To close**: A `bodiless` capture per profile
    ([#56](https://github.com/roachadam/mimicry/issues/56)).
- **Safari's bodiless fetch() order**
  - **Browsers**: Safari 27 macOS.
  - **What differs**: Its fetch() POST, PUT and PATCH without a body start
    `sec-fetch-site, accept, origin` (its header table without `content-type`); mimicry sends the
    order of its lists measured with a body, `accept, origin, sec-fetch-site`, as it did before;
    `content-length: 0` and the framing match.
  - **Detection**: Medium.
  - **To close**: The header table applied to Safari's own headers whenever the list's measured set
    differs (`bodilessGaps`).
- **`Width` and `Sec-CH-Width`**
  - **Browsers**: Chrome, Edge.
  - **What differs**: An `<img>` with `sizes` sends its layout width once the page's origin opted in
    (`client-hints`, `next.width-img`: 50px × DPR); mimicry has no way to say an image's width and
    sends neither.
  - **Detection**: Low (sizes images on opted-in sites).
  - **To close**: A request field for the width (`h3Gaps`, the `client-hints` header list of
    `/h3/next.width-img`).
- **`Clear-Site-Data` on a redirect**
  - **Browsers**: Chrome.
  - **What differs**: Emptied like any other response's, not captured.
  - **Detection**: Low ([0055](../../docs/decisions/0055-client-hints-opt-in.md)).
  - **To close**: None stated.
- **Network client hints**
  - **Browsers**: Chrome.
  - **What differs**: `rtt` is mimicry's last TCP connect time (QUIC connections do not update it)
    and `downlink` the machine's captured estimate (1.6 Mbps; 1.55 on Linux), each with ±10% noise
    and fixed per host for the Transport's life, where Chrome's network quality estimator measures
    HTTP round trips and throughput and changes over time: on Linux it rose by 30% or more within
    one sample (1.3 and 1.75 Mbps to one host; kindsgen's `downlinkGaps`).
  - **Detection**: Low ([0055](../../docs/decisions/0055-client-hints-opt-in.md)).
  - **To close**: None stated.
- **Client hints opt-in without a capture**
  - **Browsers**: `chrome-155-macos`.
  - **What differs**: No `clientHints`, so `Accept-CH` is ignored as before.
  - **Detection**: High on sites that opt in.
  - **To close**: A `client-hints` capture, which Chrome 155 can no longer give (not installable as
    branded Chrome).

## Cookies

- **Safari's `Domain` cookies of two levels**
  - **Browsers**: Safari.
  - **What differs**: Order a guess; shop cookies.
  - **Detection**: Low to medium.
  - **To close**: A `cookies-edge` step.
- **Limits and eviction**
  - **Browsers**: All.
  - **What differs**: No total limit; Chrome's priority and `Secure`-aware eviction not modelled;
    180 → 150 from source; partitions counted apart.
  - **Detection**: Low.
  - **To close**: `cookies-edge` captures.
- **Unmeasured rules**
  - **Browsers**: All.
  - **What differs**: Chrome's 120 s Lax window end; `Expires` by the local clock; Safari refusing
    third-party cookies on store or send; Safari's partition against path length; `__Host-` from a
    deep path; loopback hosts other than `localhost`; Chrome and nameless `__Http-` values.
  - **Detection**: Low or none.
  - **To close**: A capture each, small.
- **WebSocket handshake cookies**
  - **Browsers**: All.
  - **What differs**: A `Strict` cookie on a cross-site socket, the host's partition cookie,
    `Set-Cookie` on the upgrade.
  - **Detection**: Low.
  - **To close**: A fuller `websocket` capture.

## CORS

- **Preflight `Max-Age` caps**
  - **Browsers**: All.
  - **What differs**: Honoured as sent; browsers cap it (2 h, 24 h, 10 min, by source).
  - **Detection**: Low to medium.
  - **To close**: A long session.
- **Safelist and refusal edges**
  - **Browsers**: All.
  - **What differs**: From the Fetch standard, not captures.
  - **Detection**: Low.
  - **To close**: A fuller `cors` page.
- **Preflight cache keys**
  - **Browsers**: Chrome.
  - **What differs**: Keying by top-level site not measured; a hop's preflight over plain HTTP not
    tested.
  - **Detection**: Low.
  - **To close**: Small captures.

## Redirects

- **Safari 26's clicked navigations**
  - **Browsers**: Safari 26.
  - **What differs**: Not captured; follow its script's.
  - **Detection**: Low (`missingCaptures`).
  - **To close**: None stated.
- **Chrome 155's hop `cookie` slot**
  - **Browsers**: Chrome 155.
  - **What differs**: No twin captures, so cookies go after the list.
  - **Detection**: Medium.
  - **To close**: Ends with its deprecation.
- **`cookie` slot on hops no twin carried cookies on**
  - **Browsers**: All.
  - **What differs**: Credentialed cross-site hops, hops from `http://`, Safari 27's same-site image
    hop.
  - **Detection**: Low to medium ([0048](../../docs/decisions/0048-cookie-twins.md)).
  - **To close**: None stated.
- **Chrome's error-page reload**
  - **Browsers**: Chrome, Edge, Brave.
  - **What differs**: Reloads after the redirect limit; mimicry returns the error, by [decision
    0022](../../docs/decisions/0022-redirects-as-the-browser-follows-them.md).
  - **Detection**: Low (`redirectsReload`).
  - **To close**: None stated.
- **Unmeasured chain rules**
  - **Browsers**: All.
  - **What differs**: `navigationSite` on longer chains; `hopPreflight` to another cross-site
    origin; CORS checks of style, font and preload redirects; Safari's `keep` Origin through a third
    origin; the frame redirect limit.
  - **Detection**: Low to medium.
  - **To close**: A `redirects2` step each.
- **Safari's cold first sample**
  - **Browsers**: Safari.
  - **What differs**: A hop on a new connection with another order.
  - **Detection**: Low (`sampleGaps` in kindsgen).
  - **To close**: None stated.

## WebSockets

- **Firefox and an 8-bit window**
  - **Browsers**: Firefox.
  - **What differs**: Firefox hangs, mimicry fails the dial as Safari.
  - **Detection**: None ([report](../../internal/harness/reports/websocket-deflate-2026-10-26.md)).
  - **To close**: None stated.
- **Firefox's transport end**
  - **Browsers**: Firefox.
  - **What differs**: Firefox 158 on macOS never ended one, so `firefox-158-macos` ends it 10 s
    after the close; on Windows and Linux, and with Firefox 159 on macOS, its HTTP/1.1 ones are
    modelled (`closeH1`), its HTTP/2 streams it left to the server but one: the cross-site socket on
    a connection of its own, which it reset with RST_STREAM CANCEL 1.0 s after the close (every
    sample, Firefox 159 on macOS and Linux too; `webSocketEndGaps`).
  - **Detection**: Low.
  - **To close**: A longer macOS capture, and a rule for which HTTP/2 sockets Firefox ends.
- **Chrome's stream end after a close**
  - **Browsers**: Chrome.
  - **What differs**: Chrome drops it after 2 s, mimicry resets after 10 s; servers that leave it
    open.
  - **Detection**: Low.
  - **To close**: None stated.
- **Chrome's fragmented large message**
  - **Browsers**: Chrome.
  - **What differs**: Split in some samples, one frame in mimicry.
  - **Detection**: Low.
  - **To close**: More sizes.
- **Firefox's HTTP/2 DATA segmenting**
  - **Browsers**: Firefox.
  - **What differs**: The header counted into 16000 bytes, and flow-control cuts, not modelled.
  - **Detection**: Low.
  - **To close**: None stated.
- **`ws://` from an `https://` page**
  - **Browsers**: Firefox, Safari.
  - **What differs**: Refused as the browser fails it; whether they try `wss://` first is only in
    the server's log.
  - **Detection**: Low.
  - **To close**: None stated.
- **Modelled, unexplained**
  - **Browsers**: Firefox, Safari 26.
  - **What differs**: Firefox's socket hello drops `compress_certificate`; Safari 26 sends a
    cross-site host's Lax and None cookies on cross-site sockets.
  - **Detection**: None.
  - **To close**: None stated.
- **Safari's compression threshold**
  - **Browsers**: Safari.
  - **What differs**: Between 6 and 4095 bytes, not measured.
  - **Detection**: Low.
  - **To close**: None stated.

## Connections

- **No top-level-site partition**
  - **Browsers**: All.
  - **What differs**: A third-party host reached from two sites shares a connection, a cross-site
    socket may share its host's own, and a shared `AltSvcCache` spans sites.
  - **Detection**: Medium.
  - **To close**: One Transport per site
    ([0011](../../docs/decisions/0011-connection-partitioning.md)).
- **Chrome's preconnect**
  - **Browsers**: Chrome.
  - **What differs**: One more handshake to the page's host per page load (through a proxy, extra
    CONNECTs).
  - **Detection**: Low to medium.
  - **To close**: Not planned ([0023,
    amendment](../../docs/decisions/0023-connection-limits-and-coalescing.md#amendment-2026-10-30-no-preconnects)).
- **Speculative and raced ECH connections**
  - **Browsers**: Chrome on Linux, Firefox.
  - **What differs**: In the `ech` captures Chrome on Linux opened a second connection to the page's
    host about 7 ms after the first in every sample, and Firefox over its DoH (Windows, Linux) one
    more too, each carrying no request; Firefox on macOS raced two connections with the config the
    server rejects in 1 of 3 samples (`echGaps`); mimicry opens neither (the e2e test leaves them
    out).
  - **Detection**: Low.
  - **To close**: Not planned, as Chrome's preconnect ([0023,
    amendment](../../docs/decisions/0023-connection-limits-and-coalescing.md#amendment-2026-10-30-no-preconnects)).
- **Firefox on Windows's unused opening connection**
  - **Browsers**: Firefox on Windows.
  - **What differs**: A second TCP connection to the page's host 0–27 ms after the first, its
    HTTP/2 preface and GOAWAY, unused (131 of 135 page samples opened directly over HTTPS, none
    through a proxy); its handshake leaves Firefox one more TLS 1.2 ticket, so one more connection
    resumes (`resume-tls12`: 5/3 to the page's host, mimicry 5/2).
  - **Detection**: Low to medium.
  - **To close**: Not planned, as Chrome's preconnect ([0023,
    amendment](../../docs/decisions/0023-connection-limits-and-coalescing.md#amendment-2026-11-01-firefox-on-windowss-unused-connection);
    `unusedOpenings`, `resumeCountGaps`).
- **Firefox's credential-less connections**
  - **Browsers**: Firefox.
  - **What differs**: Their own connection to some hosts (every macOS sample, 2 of 3 on Windows,
    1 of 3 on Linux), no rule found.
  - **Detection**: Low to medium (`partitionGaps`).
  - **To close**: None stated.
- **Safari on iOS's beacon connection**
  - **Browsers**: Safari 27 on iOS.
  - **What differs**: Its beacon, cookies and all, goes on the credential-less connection; mimicry
    sends it on the credentialed one.
  - **Detection**: Low.
  - **To close**: A rule for which kinds share that pool (`partitionGaps`).
- **Tabs**
  - **Browsers**: All.
  - **What differs**: A Transport is one session; what tabs share is not measured.
  - **Detection**: Medium if misused
    ([0019](../../docs/decisions/0019-transport-is-one-browsing-entity.md)).
  - **To close**: None stated.
- **Coalescing conditions**
  - **Browsers**: All.
  - **What differs**: Address, port, partition and proxy never varied, and mimicry never coalesces
    through a proxy.
  - **Detection**: Low to medium.
  - **To close**: A fuller `connections` capture.
- **Limits beyond the per-host cap**
  - **Browsers**: All.
  - **What differs**: The 6 per host applied to plain HTTP and per proxy route ([INFERENCE]);
    Chrome's 256 total and 32 per proxy, Firefox's 900, and priority queueing not modelled.
  - **Detection**: Low.
  - **To close**: None stated.
- **Connections per page**
  - **Browsers**: All.
  - **What differs**: Not compared against the replays; extra unused connections in the WebSocket
    captures not analysed.
  - **Detection**: Low.
  - **To close**: None stated.

## Proxies

- **Safari's `Proxy-Authorization` inside tunnels**
  - **Browsers**: Safari.
  - **What differs**: Sent to servers once its credential state is on; mimicry never does.
  - **Detection**: Low.
  - **To close**: Captures of the trigger and a decision on sending proxy credentials to servers
    ([report](../../internal/harness/reports/proxy-websocket-2026-10-26.md)).
- **Safari 26's credential flow**
  - **Browsers**: Safari 26.
  - **What differs**: Copied from Safari 27, never captured.
  - **Detection**: Low (`missingCaptures`).
  - **To close**: None stated.
- **Safari on iOS's 407 flow**
  - **Browsers**: Safari on iOS.
  - **What differs**: The iPhone answered no proxy's 407, even with the Wi-Fi proxy's credentials
    set; credentials go up front, and `BrowserProxyAuth` is `ErrNotMeasured`.
  - **Detection**: Low.
  - **To close**: None planned ([0051](../../docs/decisions/0051-ios-safari-over-the-network.md)).
- **Safari's CONNECTs beside a plain-HTTP page**
  - **Browsers**: Safari; most on iOS.
  - **What differs**: Through an HTTP proxy, with the page at `http://`, Safari also sent CONNECTs
    to the capture host, each on a connection of its own (answered 502 by the capture proxy): one
    per sample on macOS, four on the iPhone (Safari 26 and 27), heads identical; mimicry sends none.
  - **Detection**: Low.
  - **To close**: A capture where the CONNECT is answered, to see what it is for.
- **A link or script navigation from a plain-HTTP page**
  - **Browsers**: All.
  - **What differs**: No profile list measures one (kinds-plain's plain navigations are the typed
    page and a form POST), so mimicry sends the typed navigation's list: no Referer, Safari's Cookie
    after Connection, macOS Safari's typed order.
  - **Detection**: Low to medium.
  - **To close**: A kinds-plain link navigation
    ([`proxyPlainGaps`](../../internal/harness/e2e/proxy_test.go)).
- **Safari's plain-HTTP upload without Content-Type**
  - **Browsers**: Safari.
  - **What differs**: Content-Length after Accept, where kinds-plain's POST fetches, all with a
    Content-Type, have it after Origin as mimicry does.
  - **Detection**: Low.
  - **To close**: A kinds-plain fetch of a typeless body (`proxyPlainGaps`).
- **Firefox's last-in-body script over plain HTTP**
  - **Browsers**: Firefox.
  - **What differs**: Sent without Priority; kinds-plain's head script went at u=2, the profile's
    one script list.
  - **Detection**: Low.
  - **To close**: A body-script kind (`proxyPlainGaps`).
- **Challenge state per proxy**
  - **Browsers**: All.
  - **What differs**: Shared by tunnels and forwarded requests ([INFERENCE]).
  - **Detection**: Low.
  - **To close**: A mixed capture.
- **Chrome's preconnects to the proxy**
  - **Browsers**: Chrome.
  - **What differs**: See [Chrome's preconnect](#connections) above.
  - **Detection**: As [Chrome's preconnect](#connections).
  - **To close**: As [Chrome's preconnect](#connections).
- **Ending a tunnel over an HTTP/2 proxy**
  - **Browsers**: Firefox.
  - **What differs**: Through an `https://` proxy speaking HTTP/2 (`proxy-https-auth`, Firefox
    159 on macOS, every sample), each connection the server sent GOAWAY on was ended by resetting
    its tunnel's stream to the proxy (RST_STREAM CANCEL right after its last record), with no GOAWAY
    inside the tunnel, where Firefox answers NO_ERROR on every other connection, an HTTP/1.1
    `https://` proxy's tunnels (`proxy-https-h1-auth`) included; `hellogen` leaves the scenarios
    whose proxy may speak HTTP/2 out of `goawayReply`, and a Firefox profile answers NO_ERROR inside
    such a tunnel too before the reset.
  - **Detection**: Low (the server sees one GOAWAY more).
  - **To close**: A rule for when Firefox skips its reply.
- **Firefox's TLS resumption to an `https://` proxy**
  - **Browsers**: Firefox.
  - **What differs**: Over HTTP/1.1, 1–2 of the 11 tunnel connections after the first resumed per
    sample (the resume page's last, in 7 of 9), the rest full handshakes; a Firefox profile never
    resumes to the proxy (`proxy.https.resume` false).
  - **Detection**: Low (the proxy sees it).
  - **To close**: A rule for which connections resume.
- **Firefox's background connections to an `https://` proxy**
  - **Browsers**: Firefox.
  - **What differs**: Its own requests (refused by the capture proxy) send another hello, no session
    ID and 11 extensions; mimicry makes no such connections.
  - **Detection**: None for a caller's traffic.
  - **To close**: None stated.
- **Chrome and Edge's parallel CONNECTs before a 407**
  - **Browsers**: Chrome, Edge.
  - **What differs**: Over HTTP/2, 3–4 (Chrome) or 2 (Edge) CONNECTs without credentials before the
    challenge is answered, its preconnects; mimicry sends one.
  - **Detection**: Low (the proxy sees it).
  - **To close**: Not planned, as Chrome's preconnect.
- **HTTP/3 through an HTTP proxy**
  - **Browsers**: Chrome, Edge, Firefox; with `Config.ConnectUDPProxy` only.
  - **What differs**: No browser sends HTTP/3 through a configured proxy, so a site that knows the
    address is a proxy's sees HTTP/3 where a browser would use TCP; the site's QUIC is the profile's
    (first flights equal through the proxy, `TestQUICInterop` `connect-udp`), but its packets over
    about 1420 bytes (Chrome's max_udp_payload_size 1472 less the proxy's framing) are dropped on
    the way, a smaller path MTU than direct. The CONNECT-UDP proxy sees a CONNECT-UDP no browser
    sends (its fields and order unmeasured), the profile's QUIC with datagrams 64 bytes larger
    (1314 for Chrome) and no resumption.
  - **Detection**: Low to medium when the caller opts in, none otherwise.
  - **To close**: Not planned beyond the option
    ([0066](../../docs/decisions/0066-http3-over-connect-udp.md)).
- **SOCKS4 from the older versions**
  - **Browsers**: Chrome 155, Firefox 158 on macOS and Windows.
  - **What differs**: No `proxy-socks4` capture (not captured, the maintainer's decision,
    2026-11-10), so they refuse SOCKS4 (`ErrNotMeasured`).
  - **Detection**: None (nothing is sent).
  - **To close**: None planned, they age out.
- **Safari 26's SOCKS4 is assumed, not captured**
  - **Browsers**: safari-26-macos.
  - **What differs**: It takes Safari 27's `proxy-socks4` and `proxy-socks4a` captures (the
    maintainer's decision, 2026-11-10; decision 0063); if Safari 26 sends other bytes (another
    SOCKS4A address, or resolving), the proxy sees Safari 27's.
  - **Detection**: By the proxy only, low.
  - **To close**: A capture at the second Mac (docs/capturing.md, Proxy scenarios).
- **SOCKS4 on the iPhone**
  - **Browsers**: safari-26-ios, safari-27-ios, chrome-157-ios, firefox-159-ios.
  - **What differs**: Refused for good (`ErrNotMeasured`; the maintainer's decision, 2026-11-10;
    [0063](../../docs/decisions/0063-socks4-proxies.md)): iOS offers no SOCKS4 route (the Wi-Fi
    proxy's Manual setting and configuration profiles are HTTP only, a proxy script's `SOCKS` is
    SOCKS5 on the networking stack shared with macOS, as measured there, and the hidden SOCKS
    version key cannot be set on iOS; [INFERENCE] for iOS itself, not tried on a phone), and a
    SOCKS4 form no iPhone sends would show to the proxy.
  - **Detection**: None (nothing is sent).
  - **To close**: Not planned.
- **SOCKS4's address choice**
  - **Browsers**: All.
  - **What differs**: With several IPv4 addresses mimicry sends the first the resolver returns,
    looking up IPv4 only; which one a browser picks, whether it opens another proxy connection with
    the next when the proxy refuses, and what Firefox does for a host with IPv6 addresses too (its
    captures run with `network.dns.disableIPv6`; the IPv4-only lookup is read from Chrome's source),
    is not measured.
  - **Detection**: Low.
  - **To close**: A capture with a multi-address, dual-stack host and a refusing proxy.
- **An IPv4 address through `socks4a://`**
  - **Browsers**: Firefox.
  - **What differs**: Sent as plain SOCKS4 (its DSTIP), never captured.
  - **Detection**: Low.
  - **To close**: A `proxy-socks4a` capture of an IP-literal URL.
- **TLS resumption through SOCKS4**
  - **Browsers**: Chrome, Firefox, Safari.
  - **What differs**: Resumed as directly (as through SOCKS5, `connectNoResume` covers CONNECT
    tunnels only), not compared over several tunnels.
  - **Detection**: Low.
  - **To close**: A SOCKS4 `resume`-style capture.
- **SOCKS4 user ID**
  - **Browsers**: All.
  - **What differs**: Browsers send it empty; mimicry sends a SOCKS4 URL's user name there, which no
    browser does.
  - **Detection**: By the proxy only.
  - **To close**: None planned ([0063](../../docs/decisions/0063-socks4-proxies.md)).

