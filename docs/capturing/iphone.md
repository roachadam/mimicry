# Safari on an iPhone

An iPhone cannot run the capture tool.
Instead, the Mac serves the captures on its network address, and the phone's Safari reaches them over Wi-Fi ([decision 0051](../decisions/0051-ios-safari-over-the-network.md)).

- Use an iPhone, not an iPad.
  iPadOS Safari sends a Mac user agent, which would name the capture `safari-NN-macos`.
- iPhone captures are named `safari-NN-ios`.

## Network setup

`-browsers ios-safari` is never picked by default.
It needs `-listen <mac-ip>`: the Mac's IPv4 address on the phone's network (`ipconfig getifaddr en0` for Wi-Fi).

With `-listen`:

- The capture server, its UDP twin and its plain-HTTP port listen on that address.
- The capture hosts resolve to it.
  Chrome's `--host-resolver-rules` does too.
- Firefox maps hosts to loopback only, so it cannot be captured with `-listen`.

A second DNS stub acts as the phone's DNS server for the run:

- **Listens:** on port 53 of every IPv4 address of the Mac.
  macOS lets a user bind a port below 1024 only on the wildcard address.
- **`*.test`:** answered with `<mac-ip>`.
- **`resolver.arpa`:** it says no name under it exists.
  So the phone never moves to an encrypted resolver that bypasses the stub (RFC 9462).
- **Other names:** forwarded to the Mac's resolver (the first `nameserver` of `/etc/resolv.conf`), so the phone keeps working.
- **Clients:** it answers only addresses on the `-listen` interface's network and refuses everyone else.
- **Limit:** it forwards at most 64 queries at a time.
- **Lifetime:** it stops with the run. Nothing on the Mac changes.

## Drivers

`-ios-driver` says how the phone is sent to each sample's URL.
Each capture file records it: `driver` in every page, HTTP/2 trace, and HTTP/1.1 or proxy request.
The `-summary` line records it too.

### `devicectl` (default)

- **How:** Xcode's devicectl on the Mac launches Safari on the iPhone over USB, with the URL as its payload:
  `xcrun devicectl device process launch --payload-url <url> com.apple.mobilesafari`.
  This brings the phone's own Safari to the front on that URL.
  Nobody needs to be at the phone.
- **Device:** `-ios-device <udid>` picks one of several connected iPhones (`xcrun devicectl list devices` lists them).
  Without it, the tool takes the only paired iPhone connected.
- **Tabs:** not closed. Close them now and then.
- **Limits:** it cannot take the click scenarios, because nothing taps the page.
- **Evidence:** its captures of `kinds` and `cors-edge` matched hand-opened ones request for request (decision 0051).

### `manual`

- **How:** the tool prints the URL and waits 2 minutes longer than `-timeout`.
  Type or paste the URL into Safari's address bar on the phone.
  Universal Clipboard pastes what was copied on the Mac.
- **Click scenarios:** tap the middle of each page titled `mimicry-click`.
- **Role:** it is the phone's own Safari opened on a URL, as on macOS.
  It is the fallback, and the only driver for the click scenarios.

### `safaridriver`

- **How:** safaridriver(1) on the Mac opens a WebDriver session on the iPhone over USB.
  It navigates to the URL and ends the session after the sample.
- **Retry:** it retries the session for up to 20 s while it reports the ready phone as unpaired.
- **Differences:** the session runs in an automation window with fresh storage, which is not the phone's own Safari (decision 0051):
  - It adds Origin before Sec-Fetch-Site in requests with a body.
  - It orders redirect hops and service-worker scripts otherwise.
  - It never ran a redirect page's watchdog service worker.
- **Limits:** the tool takes only the default page's scenarios through it: `page`, `http1`, `proxy-connect`, `proxy-plain` (`Scenario.AutomationDiffersOnIOS`).
- **Needs:** Web Inspector and Remote Automation on in the phone's Safari settings.

## Proxy scenarios

The proxy scenarios need the phone's own proxy setting: Settings > Wi-Fi > (i) > Configure Proxy.
It applies to every connection while set.

- **Listener:** the capture tool's HTTP proxy listens on `<mac-ip>:15354` (`browser.IOSProxyPort`).
- **Prompts:** before the first proxy scenario, and whenever the next one needs another setting, the tool prints what to enter and waits for Enter.
  The setting is Manual, server `<mac-ip>`, port 15354, Authentication off.
- **After the run:** the tool asks for the proxy Off.
- **Phone traffic:** while the proxy is set, it also carries the phone's own traffic, passed through unrecorded.
- **Clients:** it serves only addresses on the `-listen` network.

## Unsupported scenarios

The tool skips these on the iPhone, logged (`Scenario.UnsupportedOnIOS`):

- **Proxy-credential scenarios:** Safari on iOS answers no 407, even with the credentials in the Wi-Fi proxy setting (decision 0051).
- **SOCKS5 and SOCKS4 proxy scenarios:** the phone's setting takes an HTTP proxy or a proxy script.
  iOS offers no SOCKS4 route.
  So the iPhone profiles refuse SOCKS4 for good, and never inherit macOS Safari's SOCKS4 captures (decision 0063).
- **`cookies-edge`:** its `lo-*` steps navigate to `http://localhost`, which is the phone itself, so the page stops there.

The click scenarios need `manual`.

Wi-Fi adds latency and loss that the loopback captures never see.
The HTTP/3 and timing scenarios can show it.
From `h3-blackhole` and `h3-broken`, the iPhone's profile takes the structure, with its windows widened for the path.
It keeps macOS Safari's timing (decision 0051).

## One-time phone setup

Do this once on the phone. As for the Macs, the trust persists.

1. Install the capture CA.
   On the Mac, in `internal/harness/`, with the phone on the same network:

   ```sh
   go run ./cmd/capture -ios-ca-profile ~/Downloads/mimicry-capture-ca.mobileconfig -listen <mac-ip>
   ```

   - It writes the profile (outside the repo) and serves it until ctrl-C at the URL it prints.
   - Open that URL in Safari on the phone and allow the download (or AirDrop the file).
   - Then Settings > General > VPN & Device Management > mimicry capture CA > Install.
   - Without `-listen`, it only writes the file.
2. Settings > General > About > Certificate Trust Settings: turn on full trust for "mimicry capture CA (local, .test)".
   An installed profile's root is not trusted for TLS until then.
3. Settings > Apps > Safari: no content blockers or other extensions, and default privacy settings.
   - Turn iCloud Private Relay off.
   - Turn Limit IP Address Tracking off for the Wi-Fi network (Settings > Wi-Fi > (i)).
   - Either can send Safari's lookups past the DNS stub.
4. For `-ios-driver devicectl` (and `safaridriver`): connect the phone to the Mac by cable, trusted and unlocked.
   `safaridriver` also needs Settings > Apps > Safari > Advanced, Web Inspector and Remote Automation on.

## Each run

On the phone:

- Settings > Wi-Fi > (i) > Configure DNS > Manual, with `<mac-ip>` as the only server.
- Auto-Lock Never, with the phone unlocked.
  A locked phone, or Safari in the background, holds the page's timers back.
- Put DNS back to Automatic after the run.
  Without the tool running, the phone has no DNS.

On the network:

- The Mac's firewall must let the capture tool accept connections.
- The network must not isolate its clients. Guest networks often do.

Then, in `internal/harness/`:

```sh
go run ./cmd/capture -browsers ios-safari -listen <mac-ip> -scenarios page:3
```

## Profiles

An iOS profile comes from `cmd/refresh -browser safari-ios -listen <mac-ip>` ([Refreshing profiles](../refreshing-profiles.md#the-iphone)).

- The iPhone captures the fingerprint layer and the scenarios the generators derive from.
- The profile inherits a scenario from macOS Safari's captures only where the iPhone's own show no difference in its area ([decision 0051](../decisions/0051-ios-safari-over-the-network.md)).

## Chrome on an iPhone

`-browsers ios-chrome` captures Chrome on the same iPhone.
It uses the same `-listen`, DNS stub, capture CA and proxy setting as Safari above.

- **User agent:** says `CriOS/NNN` and no `Chrome/`.
- **Capture name:** `chrome-NNN-ios`.
- **Preflight:** lists it with the version of the Chrome app installed on the phone (from devicectl's list of apps), not the iOS version.
- **Drivers:** only `devicectl` and `manual` take it.
  `safaridriver` drives Safari alone, and the tool refuses it for `ios-chrome`.

Per-driver and per-scenario notes:

- **Launch:** `devicectl` launches `com.google.chrome.ios` with the URL in Chrome's own scheme.
  - `https://host:port/path` becomes `googlechromes://host:port/path`.
  - `http://` becomes `googlechrome://`.
  - Chrome ignores a payload URL with a plain `https://` scheme.
- **Redirect restarts:** before each sample of `redirects2` and `redirects2-cookies`, devicectl ends Chrome and launches it afresh (`--terminate-existing`).
  - After the chain's "too many redirects" page, Chrome does not open the next sample's URL.
  - Those two were captured that way.
  - The other scenarios were captured with Chrome left running, so the tool never restarts it there. A restart would be unmeasured.
  - The rule is `iosRedirectRestarts` in `internal/browser/ios.go`.
- **`kinds-plain`:** needs someone at the phone.
  - Chrome asks before it submits a form over plain HTTP. Safari does not.
  - Tap "Send anyway" on each insecure-form warning.
  - The tool says when, and waits 2 minutes longer.
  - Run it with someone there, or with `manual`.

```sh
go run ./cmd/capture -browsers ios-chrome -listen <mac-ip> -scenarios page:3
```

`cmd/refresh -browser chrome-ios -listen <mac-ip>` refreshes its profile the same way as `safari-ios`, with the same scenarios.

## Firefox on an iPhone

`-browsers ios-firefox` captures Firefox on the same iPhone, with the same setup as Chrome above.

- **User agent:** says `FxiOS/NNN` and Safari's frozen `iPhone OS 18_7`.
- **Capture name:** `firefox-NNN-ios`.
- **Preflight:** lists it with the version of the Firefox app installed on the phone.
- **Drivers:** only `devicectl` and `manual` take it.

Per-driver and per-scenario notes:

- **Launch:** `devicectl` launches `org.mozilla.ios.Firefox` with the payload `firefox://open-url?url=<the capture URL, query-escaped>`.
- **Redirect restarts:** before each sample of `redirects2` and `redirects2-cookies`, devicectl ends Firefox and launches it afresh, as for Chrome (`iosRedirectRestarts`).
  - Even so, about one sample in three stalled after the "too many redirects" page and never opened the next URL.
  - `firefox-159-ios`'s three samples of each were taken as single-sample runs (`-scenarios redirects2:1`), one run per sample.
  - Their trace files were joined into one, sample after sample. A trace file is the JSON array of its samples.
- **`kinds-plain`:** asks nothing. Firefox submits a form over plain HTTP without the warning Chrome shows.
- **`http1`:** can't show the ClientHello's ALPN, because its trace keeps HTTP/1.1 requests only.
  - `firefox-159-ios`'s three samples were single-sample runs too.
  - In two earlier three-sample runs, the capture tool's log showed sample 1's ClientHello offering `http/1.1` alone, and sample 3's `h2, http/1.1` ([gaps](../../profiles/evidence/gaps.md)).

```sh
go run ./cmd/capture -browsers ios-firefox -listen <mac-ip> -scenarios page:3
```

`cmd/refresh -browser firefox-ios -listen <mac-ip>` refreshes its profile the same way as `safari-ios`, with the same scenarios.
