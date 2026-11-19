# Safari on an iPhone

An iPhone cannot run the capture tool, so the Mac serves the captures on
its network address and the phone's Safari reaches them over Wi-Fi
([decision 0051](../decisions/0051-ios-safari-over-the-network.md)). Use an
iPhone, not an iPad: iPadOS Safari sends a Mac user agent, which would name
the capture `safari-NN-macos`. Its captures are named `safari-NN-ios`.

`-browsers ios-safari` (never picked by default) needs `-listen <mac-ip>`,
the Mac's IPv4 address on the phone's network (`ipconfig getifaddr en0`
for Wi-Fi). With it the capture server, its UDP twin and its plain-HTTP
port listen on that address, and the capture hosts resolve to it (Chrome's
`--host-resolver-rules` too; Firefox maps hosts to loopback only, so it
cannot be captured with `-listen`). A second DNS stub, the phone's DNS
server for the run, listens on port 53 of every IPv4 address of the Mac
(macOS lets a user bind a port below 1024 only on the wildcard address):
it answers `*.test` with `<mac-ip>`, says no name under `resolver.arpa`
exists (so the phone never moves to an encrypted resolver that bypasses
it, RFC 9462), and forwards every other name to the Mac's resolver (the
first `nameserver` of `/etc/resolv.conf`), so the phone keeps working. It
answers only addresses on the `-listen` interface's network, refusing
everyone else, and forwards at most 64 queries at a time. It stops with
the run; nothing on the Mac changes.

`-ios-driver` says how the phone is sent to each sample's URL; each
capture file records it (`driver` in every page, HTTP/2 trace and HTTP/1.1
or proxy request), as does the `-summary` line:

- `devicectl` (default): Xcode's devicectl on the Mac launches Safari on
  the iPhone over USB with the URL as its payload (`xcrun devicectl device
  process launch --payload-url <url> com.apple.mobilesafari`), which
  brings the phone's own Safari to the front on that URL; nobody needs to
  be at the phone. `-ios-device <udid>` picks one of several connected
  iPhones (`xcrun devicectl list devices` lists them); without it the tool
  takes the only paired iPhone connected. Tabs are not closed; close them
  now and then. It cannot take the click scenarios: nothing taps the page.
  Its captures of `kinds` and `cors-edge` matched hand-opened ones request
  for request (decision 0051).
- `manual`: the tool prints the URL and waits 2 minutes longer than
  `-timeout`; type or paste it into Safari's address bar on the phone
  (Universal Clipboard pastes what was copied on the Mac), and tap the
  middle of each page titled `mimicry-click` in the click scenarios. It is
  the phone's own Safari opened on a URL, as on macOS: the fallback, and
  the only driver for the click scenarios.
- `safaridriver`: safaridriver(1) on the Mac opens a WebDriver session on
  the iPhone over USB, navigates to the URL and ends the session after the
  sample (retrying the session for up to 20 s while it reports the ready
  phone as unpaired). The session runs in an automation window with fresh
  storage, which is not the phone's own Safari: it adds Origin before
  Sec-Fetch-Site in requests with a body, orders redirect hops and
  service-worker scripts otherwise, and never ran a redirect page's
  watchdog service worker (decision 0051). The tool takes only the
  default page's scenarios through it (`page`, `http1`, `proxy-connect`,
  `proxy-plain`; `Scenario.AutomationDiffersOnIOS`). It needs Web
  Inspector and Remote Automation on in the phone's Safari settings.

The proxy scenarios need the phone's own proxy setting (Settings > Wi-Fi
> (i) > Configure Proxy), which applies to every connection while set.
For them the capture tool's HTTP proxy listens on `<mac-ip>:15354`
(`browser.IOSProxyPort`); before the first proxy scenario, and whenever
the next one needs another setting, the tool prints what to enter
(Manual, server `<mac-ip>`, port 15354, Authentication off) and waits for
Enter; after the run it asks for the proxy Off. While the proxy is set it
also carries the phone's own traffic, passed through unrecorded; it
serves only addresses on the `-listen` network.

Unsupported on the iPhone (`Scenario.UnsupportedOnIOS`; the tool skips
them, logged): the proxy-credential scenarios (Safari on iOS answers no
407, even with the credentials in the Wi-Fi proxy setting: decision
0051), the SOCKS5 and SOCKS4 proxy scenarios (the phone's setting takes an HTTP
proxy or a proxy script; iOS offers no SOCKS4 route, so the iPhone
profiles refuse SOCKS4 for good and never inherit macOS Safari's SOCKS4
captures: decision 0063) and `cookies-edge` (its `lo-*` steps navigate to
`http://localhost`, the phone itself, so the page stops there). The click
scenarios need `manual`. Wi-Fi adds latency and loss that the loopback
captures never see, which the HTTP/3 and timing scenarios can show: from
`h3-blackhole` and `h3-broken` the iPhone's profile takes the structure,
its windows widened for the path, and keeps macOS Safari's timing
(decision 0051).

Once on the phone (as for the Macs, the trust persists):

1. Install the capture CA. On the Mac, in `internal/harness/`, with the
   phone on the same network:

   ```sh
   go run ./cmd/capture -ios-ca-profile ~/Downloads/mimicry-capture-ca.mobileconfig -listen <mac-ip>
   ```

   It writes the profile (outside the repo) and serves it until ctrl-C at
   the URL it prints. Open that URL in Safari on the phone and allow the
   download (or AirDrop the file), then Settings > General > VPN & Device
   Management > mimicry capture CA > Install. Without `-listen` it only
   writes the file.
2. Settings > General > About > Certificate Trust Settings: turn on full
   trust for "mimicry capture CA (local, .test)"; an installed profile's
   root is not trusted for TLS until then.
3. Settings > Apps > Safari: no content blockers or other extensions,
   default privacy settings. iCloud Private Relay off, and Limit IP Address
   Tracking off for the Wi-Fi network (Settings > Wi-Fi > (i)): either can
   send Safari's lookups past the DNS stub.
4. For `-ios-driver devicectl` (and `safaridriver`): the phone connected
   to the Mac by cable, trusted and unlocked. `safaridriver` also needs
   Settings > Apps > Safari > Advanced, Web Inspector and Remote
   Automation on.

For each run: Settings > Wi-Fi > (i) > Configure DNS > Manual, with
`<mac-ip>` as the only server, and Auto-Lock Never with the phone
unlocked (a locked phone or Safari in the background holds the page's
timers back); put DNS back to Automatic after the run (without the tool
running, the phone has no DNS).
The Mac's firewall must let the capture tool accept connections, and the
network must not isolate its clients (guest networks often do). Then, in
`internal/harness/`:

```sh
go run ./cmd/capture -browsers ios-safari -listen <mac-ip> -scenarios page:3
```

An iOS profile comes from `cmd/refresh -browser safari-ios -listen
<mac-ip>` ([Refreshing profiles](../refreshing-profiles.md#the-iphone)): the
iPhone captures the fingerprint layer and the scenarios the generators
derive from, and the profile inherits a scenario from macOS Safari's
captures only where the iPhone's own show no difference in its area
([decision 0051](../decisions/0051-ios-safari-over-the-network.md)).

### Chrome on an iPhone

`-browsers ios-chrome` captures Chrome on the same iPhone, with the same
`-listen`, DNS stub, capture CA and proxy setting as Safari above. Its
user agent says `CriOS/NNN` and no `Chrome/`; its captures are named
`chrome-NNN-ios`. The preflight lists it with the version of the Chrome
app installed on the phone (from devicectl's list of apps), not the iOS
version.

Only the `devicectl` and `manual` drivers take it; `safaridriver` drives
Safari alone, and the tool refuses it for `ios-chrome`.

- `devicectl` launches `com.google.chrome.ios` with the URL in Chrome's
  own scheme: `https://host:port/path` becomes
  `googlechromes://host:port/path`, and `http://` becomes
  `googlechrome://`. Chrome ignores a payload URL with a plain `https://`
  scheme.
- Before each sample of `redirects2` and `redirects2-cookies`, devicectl
  ends Chrome and launches it afresh (`--terminate-existing`): after the
  chain's "too many redirects" page, Chrome does not open the next
  sample's URL. Those two were captured that way. The other scenarios
  were captured with Chrome left running, so the tool never restarts it
  there: a restart would be unmeasured. The rule is `iosRedirectRestarts` in
  `internal/browser/ios.go`.
- `kinds-plain` needs someone at the phone. Chrome asks before it submits
  a form over plain HTTP (Safari does not): tap "Send anyway" on each
  insecure-form warning. The tool says when, and waits 2 minutes longer.
  Run it with someone there, or with `manual`.

```sh
go run ./cmd/capture -browsers ios-chrome -listen <mac-ip> -scenarios page:3
```

`cmd/refresh -browser chrome-ios -listen <mac-ip>` refreshes its profile
the same way as `safari-ios`, with the same scenarios.

### Firefox on an iPhone

`-browsers ios-firefox` captures Firefox on the same iPhone, with the same
setup as Chrome above. Its user agent says `FxiOS/NNN` and Safari's frozen
`iPhone OS 18_7`; its captures are named `firefox-NNN-ios`. The preflight
lists it with the version of the Firefox app installed on the phone.
Only the `devicectl` and `manual` drivers take it.

- `devicectl` launches `org.mozilla.ios.Firefox` with the payload
  `firefox://open-url?url=<the capture URL, query-escaped>`.
- Before each sample of `redirects2` and `redirects2-cookies`, devicectl
  ends Firefox and launches it afresh, as for Chrome
  (`iosRedirectRestarts`). Even so, about one sample in three stalled
  after the "too many redirects" page and never opened the next URL.
  `firefox-159-ios`'s three samples of each were taken as single-sample
  runs (`-scenarios redirects2:1`), one run per sample, and their trace
  files joined into one, sample after sample: a trace file is the JSON
  array of its samples.
- `kinds-plain` asks nothing: Firefox submits a form over plain HTTP
  without the warning Chrome shows.
- `http1` (`firefox-159-ios`'s three samples were single-sample runs too)
  can't show the ClientHello's ALPN: its trace keeps HTTP/1.1 requests
  only. In two earlier three-sample runs, the capture tool's log showed
  sample 1's ClientHello offering `http/1.1` alone and sample 3's `h2,
  http/1.1` ([gaps](../../profiles/evidence/gaps.md)).

```sh
go run ./cmd/capture -browsers ios-firefox -listen <mac-ip> -scenarios page:3
```

`cmd/refresh -browser firefox-ios -listen <mac-ip>` refreshes its profile
the same way as `safari-ios`, with the same scenarios.
