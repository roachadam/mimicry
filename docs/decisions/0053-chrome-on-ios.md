# 0053: Chrome on an iPhone: its own iPhone captures, Safari's WebKit for the rest

- Status: Accepted; amended 2026-11-03 (Firefox on an iPhone, `firefox-159-ios`)
- Date: 2026-11-02
- Commits: the one adding `chrome-157-ios` ([#57](https://github.com/roachadam/mimicry/issues/57))

**Summary.** `chrome-157-ios` (`profiles.ChromeIOS()`) is Chrome
157.0.8103.24 on an iPhone (iOS 27.2), captured like `safari-27-ios`
([0051](0051-ios-safari-over-the-network.md)) through devicectl, for the
whole iPhone set. Chrome on iOS loads pages through WebKit and Apple's
network stack: its captures match `safari-27-ios` at the TLS, HTTP/2,
QUIC, HTTP/3, WebSocket, proxy, resumption, cookie and redirect layers,
and differ in the user agent and in the order of request headers, which
is macOS Safari 27's. A Chrome on iOS profile may inherit a scenario's
captures from Safari of its WebKit generation (the iOS major version in
its user agent), on iOS or macOS, only where Chrome's own captures show
no difference in that scenario's area; never the user agent or a header
order from iOS Safari.

## Problem

Issue #57 asks for Chrome on iOS. Every browser on iOS uses the system's
WebKit, so most of its fingerprint is Safari's; yet the profile policy
(decisions 0035, 0051) lets a profile inherit captures only from the same
browser, and a Chrome on iOS profile has no earlier version of itself.
Without inheritance its profile would claim nothing for the scenarios the
iPhone cannot take or the iPhone set leaves out.

## Options

1. **The iPhone set only, nothing inherited.** Every scenario outside
   `server.IOSScenarios` would be not measured, including the cookie and
   redirect rules Chrome's own `cookies` and `redirects2` already show to
   be Safari's. Rejected: it throws away evidence that applies.
2. **Inherit from `safari-27-ios`** (the same phone, the same WebKit).
   Rejected for header order: iOS Safari orders Accept, Sec-Fetch-Site
   and Origin, and plain HTTP's User-Agent, otherwise than Chrome on the
   same phone. And it has nothing to inherit: Chrome took the whole
   iPhone set itself.
3. **Inherit from macOS Safari of the same WebKit generation, where
   Chrome's own captures show no difference** (chosen): Chrome's header
   order is macOS Safari 27's (Evidence), so the scenarios
   `safari-27-ios` inherits from `safari-27-macos` (cookies-edge,
   cookies-edge2, `nav-redirect-{so,ss,cs,http}` and their twins,
   set-cookie, slow-idle) are inherited the same way.

Not done: the scenarios `NotInheritedOnIOS` keeps out only because their
macOS captures hold CORS requests in macOS's order could now be inherited
for Chrome, whose order that is. They also exercise connection pools,
which follow the iPhone's (credential-less requests on connections of
their own), so each would need checking against Chrome's own captures
first; they stay not measured.

## Decision

- **Policy.** `internal/platform.Engine` names Safari as the engine of
  Chrome on iOS, and `WebKitVersion` reads the WebKit generation from a
  user agent (`iPhone OS 27_2_0` is 27). `cmd/profilegen` lets such a
  profile name, per scenario, a Safari profile of that version on iOS or
  on macOS (the platform `Stack` shares Safari's network stack with),
  holding its own captures of the scenario. `profile.Validate` lets a
  Chrome profile on iOS name a Safari one; everything else still needs
  the same browser. h3gen treats Chrome on iOS as sharing a stack
  (failure pair only, other edge rules copied).
- **What is Chrome's own** (three samples each, devicectl): the
  fingerprint, every kind's header lists and variants (kindsgen), the
  plain-HTTP and HTTP/1.1 lists, HTTP/1.1 upload records, the failure
  pair's keep-alive, path-degrading close and close on GOAWAY.
- **Copied from `safari-27-ios`** (the profile it was derived from; the
  same WebKit, checked against Chrome's captures where they reach): the
  hand-written cookie, redirect, connection, WebSocket and proxy
  sections; the fallback's timing, `quic.ack` and `quic.credit`, as for
  the iPhone's Safari (Wi-Fi timing includes the path).
- **Capture tool.** `cmd/capture -browsers ios-chrome` (devicectl or
  manual; safaridriver drives Safari only) launches
  `com.google.chrome.ios` with the URL in Chrome's own scheme
  (`googlechromes://`, `googlechrome://` for http): devicectl's plain
  `https://` payload URL is ignored by Chrome. `redirects2` and its twin
  restart Chrome before each sample (`--terminate-existing`): after its
  "too many redirects" page Chrome does not open the next sample's URL.
  The other scenarios were captured without restarts, so a restart there
  is unmeasured and not done. `kinds-plain` needs someone at the phone to
  tap "Send anyway" on Chrome's insecure-form warnings.
  `capture.ParseUserAgent` reads `CriOS/<version>` as Chrome on iOS.
  `cmd/refresh -browser chrome-ios` plans the same run.

## Evidence

2026-11-01, an iPhone 15 Pro on iOS 27.2, Chrome 157.0.8103.24, over
Wi-Fi through devicectl, three samples of each of the 23 iPhone
scenarios; `redirects2` and its twin with Chrome restarted before each
sample (the twin with every tab closed too).

- **The same as `safari-27-ios`:** the ClientHello over TCP and QUIC (JA4
  `t13d2013h2_a09f3c656075_7f0f34a4126d`), HTTP/2 SETTINGS, window and
  HPACK representations (Akamai `2:0;3:100;4:2097152;9:1|10420225|0|m,s,a,p`,
  no PRIORITY flag on HEADERS), the QUIC transport parameters (16 MiB and
  2 MiB flow control, 8 uni streams, `0xff073e08 = 9`) and HTTP/3
  SETTINGS, when it moves to HTTP/3, WebSocket handshakes and frames, the
  CONNECT head, resumption, the cookies sent, the redirect hops followed
  (20 hops, then `/done`) and the connections per host, HTTP/1.1 and
  HTTP/2 upload records. No client hints. No Google or Chrome-internal
  request reached the capture server.
- **User agent:** `Mozilla/5.0 (iPhone; CPU iPhone OS 27_2_0 like Mac OS
  X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/157.0.8103.24
  Mobile/15E148 Safari/604.1`: the real iOS version, where Safari sends a
  frozen `18_7`.
- **Header order, macOS Safari 27's:** CORS requests add Sec-Fetch-Site
  before Accept (`kinds`: 25 requests, 3 of 3; `cors`, `cors-edge`,
  `resume`, `redirects2`, `websocket` the same); plain HTTP puts Accept
  before User-Agent, and before Upgrade-Insecure-Requests on iframes
  (`kinds-plain`: no difference from `safari-27-macos`); custom headers
  and Sec-Fetch-Mode where macOS Safari puts them (`headers-order`, 33
  requests). The same phone's Safari orders all of these otherwise, so
  the iPhone order decision 0051 called the platform's is the Safari
  app's (0051, corrected).
- **`http1`:** every GET identical to both Safaris 27; the `/upload` POST
  puts Sec-Fetch-Site before Origin (3 of 3), as the phone's own Safari
  26 and 27 do through devicectl (`safari-27-ios`'s `http1` is
  safaridriver's, which swaps the two, 0051). Which connection carries
  `/` (1 of 3 as on the iPhone, 2 of 3 as on macOS) and whether
  `img.png` or `app.js` arrives first is arrival noise.
- **`http1-upload`:** 169 uploads per sample, the head always in a record
  of its own, the body in 16384-byte records (`headApart`, no joining):
  identical in 3 of 3, and to Safari's.
- **Failure pair:** the structure of `safari-26-ios`'s. In every one of
  the 6 unanswered `h3-blackhole` attempts Chrome sent the Initial pair
  and one or two retransmitted pairs (a 1038-byte unpadded datagram and
  a 1200-byte one) 28–137 ms in, the last between 29 ms before and 8 ms
  after the raced TCP connection's ClientHello reached the server, and
  nothing after (Safari 26 on the iPhone: none or one such pair). That
  is the "6 Initials". h3gen counts an attempt abandoned only when it
  sends no probe at all, so it derives `abandonOnTcp` false; the
  profile keeps `true`, which Chrome's captures support, pinned in
  h3gen's `sampleGaps`. Keep-alive PINGs every ~2000 ms in all three
  `h3-broken` samples; in sample 2 the server stopped answering before
  the 7.5 s PING, so the first counted PING is 4069 ms after the last
  datagram received (pinned); 2000 ms after the last received rests on
  samples 1 and 3 and the cadence of 2.
- **devicectl timing:** in sample 1 of `cookies`, `h3-discovery` and
  `h3-broken` the navigation went in a record of its own after the
  HTTP/2 opening, in the preface's millisecond (`openingGaps`), as in
  `safari-26-ios`'s `h3-broken`.

## Consequences

- Chrome on iOS costs the iPhone run per version, with someone at the
  phone for `kinds-plain`'s taps and the proxy settings.
- A Chrome on iOS profile carries Safari's cookie, redirect and proxy
  rules on Chrome's word: they are WebKit's, checked against Chrome's
  own captures in the iPhone set.
- Insecure-form warnings, a Chrome behaviour, are not on the wire and not
  modelled.

## Revisit when

- Chrome on iOS stops using WebKit (alternative engines on iOS), or one
  of its captures differs from Safari's in a layer it inherits.
- iOS Safari's own order shows in Chrome on iOS, or Chrome's order in
  iOS Safari.
- h3gen's abandonment rule is redefined (an attempt that probes until
  the raced TCP connection opens and stops there): the pin goes.

## Amendment 2026-11-03: Firefox on an iPhone

Issue #58 asks for Firefox on iOS. `firefox-159-ios`
(`profiles.FirefoxIOS()`) is Firefox 159.1 on the same iPhone 15 Pro (iOS
27.2), captured through devicectl for the whole iPhone set. Firefox on iOS
loads pages through WebKit and Apple's network stack too, so the rule
above extends to it, unchanged but for how its WebKit generation is known.

- **Policy.** `internal/platform.Engine` names Safari as the engine of
  Firefox on iOS too, and `profile.Validate` lets a Firefox profile on iOS
  name a Safari one. Its user agent says Safari's frozen `iPhone OS 18_7`,
  which names no WebKit (Safari 26 and 27 both send it), so
  `platform.WebKitVersion` reads nothing from it; `cmd/profilegen` then
  takes the iOS major version the profile was captured on from its
  `capturedOnIOS` table (`firefox-159-ios`: 27). A user agent with a real
  iOS version (Chrome's) is still read. A wrong row cannot pass: for any
  profile inheriting from Safari this way, profilegen also requires its
  ClientHello, HTTP/2 section and QUIC transport parameters to equal those
  of Safari of that generation on its platform (`safari-27-ios`); iOS 26
  and 27 differ in the QUIC parameters (`0xff080808`).
- **Inherited:** the 12 scenarios `chrome-157-ios` takes from
  `safari-27-macos`, on the same evidence: Firefox's own captures show
  macOS Safari 27's header order and Safari's cookie, redirect and
  connection behaviour.
- **Capture tool.** `cmd/capture -browsers ios-firefox` launches
  `org.mozilla.ios.Firefox` with the payload
  `firefox://open-url?url=<QueryEscape(url)>`, restarts it before each
  sample of `redirects2` and its twin as for Chrome
  (`iosRedirectRestarts`), and asks nothing at the phone.
  `capture.ParseUserAgent` reads `FxiOS/<version>` as Firefox on iOS;
  `cmd/refresh -browser firefox-ios` plans the same run.

### Evidence

2026-11-03, Firefox 159.1, three samples of each iPhone scenario
(`proxy-connect` and `proxy-plain` in a second run).

- **The same as `safari-27-ios` and `chrome-157-ios`:** the ClientHello
  (JA4 `t13d2013h2_a09f3c656075_7f0f34a4126d`), HTTP/2 (Akamai
  `2:0;3:100;4:2097152;9:1|10420225|0|m,s,a,p`), the QUIC transport
  parameters and HTTP/3 SETTINGS; the failure pair's mode, abandonment
  and pending requests (h3gen derives `abandonOnTcp` true from Firefox's
  captures: the mismatch of issue #60 does not arise).
- **User agent:** `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X)
  AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/159.1 Mobile/15E148
  Safari/604.1`.
- **Header order, macOS Safari 27's:** every kind's header lists equal
  `safari-27-macos`'s on 80 of 81 request types, the 81st (the font
  stylesheet) only because macOS's own samples vary. Chrome on the
  iPhone differs from macOS Safari on 6 POST and beacon types
  (Sec-Fetch-Site before Origin); Firefox does not. No insecure-form
  warning in `kinds-plain`.
- **Redirects:** even restarted before each sample, Firefox stalled after
  the "too many redirects" page in about one sample in three, so
  `redirects2` and its twin were taken as three single-sample runs and
  their trace files joined; `http1` too.
- **`http1` ALPN:** in two three-sample runs the capture tool's log showed
  sample 1's ClientHello offering `http/1.1` alone and sample 3's `h2,
  http/1.1`. No rule explains it from the evidence at hand, and the
  `http1` trace does not record ALPN, so it is listed in EVIDENCE's Not
  covered, not modelled.
- **Pinned:** three first connections whose navigation devicectl handed
  Firefox in the preface's millisecond (`openingGaps`); one `h3-page`
  Initial probe and one `h3-broken` request that reached the server on
  TCP before its QUIC attempt (h3gen's `sampleGaps`).

### Revisit when

- Firefox on iOS's user agent carries a real iOS version: `capturedOnIOS`
  goes for it.
- A rule for Firefox's `http1` ALPN shows in a capture that records it.
