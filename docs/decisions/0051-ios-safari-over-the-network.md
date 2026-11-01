# 0051: Safari on an iPhone: the fingerprint captured on the iPhone, from the Mac

- Status: Accepted
- Date: 2026-11-01
- Commits: the one adding `safari-27-ios` ([#11](https://github.com/roachadam/mimicry/issues/11))

**Summary.** `safari-27-ios` (`profiles.SafariIOS()`) comes from Safari
27.2 on an iPhone, captured by the capture tool on a Mac over Wi-Fi:
`-listen <mac-ip>` serves the captures on the Mac's network address with a
DNS stub on port 53 as the phone's DNS server, and devicectl opens each
URL in the phone's own Safari over USB. safaridriver's automation window
orders headers otherwise, so it takes only the default page's scenarios.
The fingerprint and everything kindsgen derives are the iPhone's own
captures; a scenario is inherited from macOS Safari 27's captures only
where the iPhone's own captures show no difference in that area. The
iPhone differs from macOS Safari in its QUIC hello, its QPACK encoder
stream, the order of Accept, Sec-Fetch-Site and Origin, its connection
pools and its proxy credentials. Its QUIC fallback, keep-alive,
acknowledgement and credit rules are copied from macOS Safari 27, the
same network stack, unverified on the phone; the challenge flow of proxy
credentials, the other HTTP/3 failure rules and several rule scenarios
are not measured for it: requesting them is `ErrNotMeasured`, or the
default behaviour every profile has.

## Problem

Issue #11 asks for iOS Safari profiles. Every capture so far runs the
browser and the capture tool on one machine over loopback (the Windows
port, [decision 0047](0047-windows-profiles.md), copies the tool to the
PC). An iPhone cannot run the tool, cannot be given a hosts file or
flags, and resolves names through the network's DNS. And a full capture
(every scenario and replay, about two hours a version over Wi-Fi) is
costly for a browser assumed to share macOS Safari's network stack.

## Options

What the profile is made of (the maintainer's decision, 2026-11-01):

1. **A full capture on the iPhone**, as decision 0047's first Windows
   profile: about two hours a version; the proxy-credential, localhost
   and click scenarios still cannot be taken. Rejected for now.
2. **The fingerprint on the iPhone, every other scenario inherited from
   macOS Safari** (first plan): the iPhone's default-scenario captures
   matched macOS's but for the user agent. Rejected once the iPhone's own
   captures of the other fingerprint scenarios showed differences
   (Evidence): an inherited scenario in such an area would claim macOS
   behaviour for the iPhone.
3. **Fingerprint first** (chosen): the iPhone captures the fingerprint
   layer and the scenarios the generators need (`server.IOSScenarios`);
   a scenario is inherited from macOS Safari only where the iPhone's own
   captures show no difference in its area
   (`Scenario.NotInheritedOnIOS` keeps the others out); a difference is
   modelled where it is part of the fingerprint, recorded as a known
   difference where it is not, and what neither captures cover is not
   measured.

Name resolution on the phone: manual DNS pointing at a stub on the Mac
that forwards every other name (chosen; the phone's DNS setting takes an
address, no port, and macOS lets a user bind port 53 on the wildcard
address only), rather than an encrypted DNS profile (the stub would need
DoH or DoT) or a PAC script (every connection a proxy tunnel).

Opening the URL: devicectl (chosen; `xcrun devicectl device process
launch --payload-url <url> com.apple.mobilesafari` brings the phone's
own Safari to the front on the URL, with nobody at the phone), rather
than by hand (a person per sample), safaridriver (a WebDriver session
per sample in an automation window, whose header order is not the
phone's own Safari's: Evidence) or Web Inspector (a script's navigation
changes `Sec-Fetch-*`).

## Decision

- **Tooling.** `cmd/capture -listen <ipv4>` serves the captures on that
  address (server, UDP twin, plain-HTTP port) and starts a DNS stub on
  port 53 answering `*.test` with it, forwarding other names to the
  Mac's resolver (64 queries at a time at most), and saying no name
  under `resolver.arpa` exists (RFC 9462); it answers only the `-listen`
  interface's network, so it is no open resolver. `-browsers ios-safari`
  opens each URL through devicectl (`-ios-driver devicectl`, the
  default), by hand (`manual`, the fallback and the only way to the
  click scenarios) or through safaridriver (only `page`, `http1`,
  `proxy-connect` and `proxy-plain`: `Scenario.AutomationDiffersOnIOS`,
  which refresh also enforces); every capture file and summary line
  records the driver (the committed captures' were filled in from the
  runs). `-ios-ca-profile` writes and serves the capture CA as a
  configuration profile. The proxy scenarios use a proxy on
  `<mac-ip>:15354` the phone's Wi-Fi setting names, serving that network
  only; the tool asks for the setting and waits for Enter. `cmd/refresh
  -browser safari-ios -listen <mac-ip>` plans, captures (devicectl),
  derives, installs and tests.
- **What the iPhone captures** (`server.IOSScenarios`, three samples
  each): `page`, `kinds`, `kinds-http1`, `cookies`, `cors`, `cors-edge`,
  `cors-edge-cookies`, `redirects2`, `redirects2-cookies`,
  `headers-order`, `http1`, `h3-discovery`, `h3-page`, `resume`,
  `websocket`, `websocket-h2`, `proxy-connect`, `proxy-plain`.
- **Inherited from `safari-27-macos`** (no difference seen in their
  areas; `capturesFrom`, with the user agent mapped whole by
  `capture.Rewriter`): `cookies-edge`, `cookies-edge2`, `kinds-plain`,
  `nav-redirect-{so,ss,cs,http}` and their cookie twins, `set-cookie`,
  `slow-idle`. `internal/platform.Stack` says Safari on iOS may inherit
  from Safari on macOS of its own or an earlier version; `cmd/profilegen`
  enforces it, and `profile.Validate` checks only that an inherited
  capture is the same browser's (kept out of the public package).
- **Neither captured nor inherited** (`NotInheritedOnIOS`, listed in the
  e2e tests' `missingCaptures`): the HTTP/3 edge scenarios (macOS
  Safari's QUIC hellos lack the iPhone's transport parameter); the
  scenarios whose macOS captures hold CORS requests in macOS's order
  (the other nav-redirect pages and twins, the SETTINGS scenarios'
  pages, `headers-order`'s relatives, `proxy-socks5`, the
  proxy-WebSocket scenarios, `resume-tls12`, every replay);
  `connections` (pools differ); the proxy-credential scenarios (the
  iPhone answers no 407). The click scenarios (devicectl and safaridriver
  do not tap; by hand only), `cookies-edge`'s localhost steps and the
  SOCKS5 proxy scenarios cannot be taken on the iPhone unattended
  (`UnsupportedOnIOS`).
- **Modelled** (the fingerprint): the transport parameter `0xff080808 =
  9` after the rotated set in every fresh QUIC hello (h3gen, from the
  iPhone's `h3-discovery` and `h3-page`); the QPACK encoder stream opened
  on the server's SETTINGS with only Set Dynamic Table Capacity to the
  server's maximum (`http3.uniStreams` open `settings`, new; h3gen
  derives it, the HTTP/3 client sends it; macOS Safari never met a
  server allowing a table over QUIC, so its profiles are unchanged); the
  order of Accept, Sec-Fetch-Site and Origin (kindsgen, from the
  iPhone's captures); uncredentialed requests on connections of their
  own (`context.anonymousConnections`, as Chrome's); the first request
  of a full handshake sent with the Finished (`requestsAfterConfirmed`
  false; h3gen now derives it as the e2e rule checks it).
- **Copied from `safari-27-macos`, not measured**
  (`http3.discovery.fallback`, `quic.keepAlive`, `quic.ack`,
  `quic.credit`; h3gen keeps them for a profile sharing another
  platform's network stack, `internal/platform.Stack`, with no edge
  captures; any other profile still needs them):
  the iPhone's success path matches them (after the server's GOAWAY,
  `after-goaway.so` went on a new QUIC connection with no TCP raced, 3 of
  3, as the race rule sends it), and the network stack is the same, but
  its failure handling was never captured. `Config.BrowserQUICFallback`
  follows the copied rule. An iPhone `h3-blackhole` and `h3-broken` run
  through devicectl measures them.
- **Not measured.** QUIC loss and key-update rules are empty (the QUIC
  stack's defaults), no 0-RTT, no dial delays, no close on GOAWAY.
  Proxy credentials keep macOS Safari 27's placement
  (`proxy.connectAuth`, `forwardAuth`), sent with the first request: the
  iPhone's CONNECT and absolute-form heads are otherwise byte-identical
  to macOS Safari 27's, only the proxy sees the placement, and the
  iPhone never answers a 407, so there is no iPhone placement to
  capture. The challenge flow (`connectCredentials`,
  `forwardCredentials`, now optional) is empty: `Config.BrowserProxyAuth`
  is `ErrNotMeasured`, and so is a WebSocket through a proxy with
  credentials.
- **Known differences, not modelled** (`profiles/EVIDENCE.md`, Not
  covered; pinned in the e2e tests and generators): the beacon sent on
  the credential-less connection, and over HTTP/3 in `h3-page`; other
  waiting requests' HEADERS in an HTTP/2 connection's opening record;
  page-set header orders with 26 or more names, and XHR's repeated name
  (kindsgen's `headersGaps`).

## Evidence

All 2026-11-01, an iPhone 15 Pro on iOS 27.2, Safari 27.2, over Wi-Fi,
three samples each. The profile's captures: `page`, `http1`,
`proxy-connect` and `proxy-plain` through safaridriver; `kinds`,
`kinds-http1`, `cors-edge`, `cors-edge-cookies`, `h3-discovery`,
`h3-page`, `redirects2` and `redirects2-cookies` opened by hand in the
phone's own Safari; `cookies`, `cors`, `headers-order`, `resume`,
`websocket` and `websocket-h2` through devicectl.

- The default scenario, by hand and through safaridriver: identical
  (`cmd/diff`), and equal to `safari-27-macos` but for the user agent
  (`Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X)
  AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148
  Safari/604.1`).
- safaridriver against the phone's own Safari. Accept, Origin and
  Sec-Fetch-Site share a bucket of CFNetwork's header table, so their
  order is the order WebKit adds them. In `cors-edge`, 9 of 23 requests
  differed between the two, all same-origin requests with a body
  (`m-post`, `m-put`, `m-delete`): safaridriver sent `accept
  content-type origin sec-fetch-site`, the phone's own Safari `accept
  content-type sec-fetch-site origin`. Its `cookies` sent the cross-site
  form POST with Origin first too, its image redirect hops in another
  order (`… accept-language sec-fetch-mode … priority …` against `…
  priority sec-fetch-mode … accept-language …`), and its service-worker
  script fetch in another order. Under safaridriver, `redirects2`
  stalled at its 25-redirect step: the error page's watchdog service
  worker, which ended the page by hand, never did.
- devicectl against by hand (`kinds`, `cors-edge`, three samples each):
  every request's header list, HPACK representations and non-random
  values (ports masked), the connections per host and every ClientHello
  identical. One timing difference: on `cors-edge`'s first connection the
  page's request went with the preface by hand, 6 to 109 ms later
  through devicectl (after Safari's SETTINGS ACK in 2 samples, as on
  macOS), the navigation being ready later than a typed URL's.
  `h2trace.Opening` now counts a first request written after the
  preface's millisecond as not waiting, so such a connection does not
  decide `http2.openingRecords`.
- The phone's own Safari adds Accept, then Sec-Fetch-Site, then Origin
  in every request but the preflights (`kinds`: `accept,
  sec-fetch-site, origin`); macOS Safari 27.0.1 adds Sec-Fetch-Site
  first in GETs and Origin before Sec-Fetch-Site in same-origin requests
  with a body [INFERENCE: the 27.0.1 → 27.2 build could explain it as
  well as the platform].
- QUIC (`h3-discovery`, `h3-page`): every client hello's transport
  parameters end with `0xff080808 = 9` after the rotated set; macOS
  Safari 27's carry none. In `h3-page` the encoder stream (id 6) carried
  one Set Dynamic Table Capacity of 65536, the server's maximum, in a
  datagram of its own after the request, and no insert;
  `h3-discovery`'s server allows no table, and there no encoder stream
  opened. After the server's GOAWAY, `after-goaway.so` went on a new
  QUIC connection with no TCP raced (3 of 3), the first request of a
  full handshake in the client's first 1-RTT packet, with its Finished.
  The beacon went over HTTP/3 on a QUIC connection of its own beside the
  page's HTTP/2 one (macOS Safari: on HTTP/2). In one hand sample of
  `h3-discovery` the page's 35 s idle lasted 69 s (the page's timers
  held back), and the idle HTTP/2 connection's next request went on a
  new connection, over QUIC, in 0-RTT with `early-data: 1` (pinned in
  h3gen; kindsgen leaves 0-RTT requests to the 0-RTT rules).
- `kinds`: uncredentialed requests (fetch omit and CORS, CORS images
  and scripts, XHR) went on a second connection to each host, and the
  beacon, cookies and all, with them: 6 connections against macOS's 3;
  `cors` the same. With three requests waiting as `cdn.kinds-b.test`'s
  connection opened, all three HEADERS shared the opening's TLS record.
- `headers-order`: page-set headers follow CFNetwork's table as on
  macOS up to 4 page-set names; with 26 or more (`letters`, `around`)
  no insertion order of Safari's own names gives the iPhone's order, and
  its XHR puts a repeated name elsewhere (`case`).
- `redirects2` by hand: after sample 1 ended its 25-redirect navigation
  on Safari's error page, samples 2 and 3 never requested `nav-chain.so`
  (browser state; left out in the e2e test); the cookie twin, a fresh
  run, requested it in all three.
- Proxies (`proxy-connect`, `proxy-plain`, Wi-Fi proxy Manual, no
  authentication): `CONNECT host:port HTTP/1.1`, `Host`,
  `Proxy-Connection: keep-alive`, `Connection: keep-alive`, and the
  absolute-form request's header list, exactly as macOS Safari 27's.
  With the `http://` page of `proxy-plain`, the iPhone also sent 4
  CONNECTs to the capture host per sample, each on a connection of its
  own and answered 502 by the capture proxy (macOS Safari 27: 1), their
  heads identical to macOS's; mimicry sends none there.
  With Authentication on and the credentials filled in, a logging proxy
  saw about 40 CONNECTs (the capture host, example.com, system services)
  each on a new connection, none with Proxy-Authorization, and Safari
  failed with NSURLErrorDomain -1013, by hand and under safaridriver.
- safaridriver's new session failed in about half the tool's runs
  ("device is not paired", "Web Inspector is not enabled", "could not be
  used") and succeeded on a retry seconds later. Its Element Click gave
  a page `pointerdown` and `touchstart` but no `click`, and touch
  actions reached no page.

## Consequences

- The iOS profile costs a short unattended run per version through
  devicectl, with someone at the phone only for the proxy settings and
  the phone's DNS.
- A capture through safaridriver is no evidence for the phone's own
  Safari beyond the default page: the capture tool refuses the rest.
- Behaviour only the not-measured or not-inherited scenarios show is not
  claimed for iOS: HTTP/3 failure handling (copied), connection reuse,
  the replays, most redirect pages. Their e2e tests skip `safari-27-ios`.
- An iPhone `h3-blackhole` and `h3-broken` run, unattended through
  devicectl, would measure the copied fallback rule; the rest of the
  edge set the QUIC loss and 0-RTT rules.

## Revisit when

- An iPhone capture of a not-inherited scenario: it joins the iPhone set
  and its rows their classes.
- A macOS Safari 27.2 capture shows the iPhone's order of Accept,
  Sec-Fetch-Site and Origin: the order is the version's, not the
  platform's, and the scenarios it kept out can be inherited again.
- devicectl stops opening URLs in the phone's own Safari, or something
  can tap the phone's pages (the click scenarios).
- Manual DNS proves unreliable (an encrypted DNS profile), or a wired
  network path becomes available for the timing scenarios.
