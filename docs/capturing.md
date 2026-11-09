# Capturing browsers and deriving profiles

For contributors capturing browsers; users don't need this page.

How the `internal/harness/` module captures the real browsers, how a profile's
generated sections are derived from those captures, and the harness's other
tools. Commands run in `internal/harness/`, and paths are relative to it,
unless they say otherwise (`profiles/data/` and `cmd/profilegen` are at the
repository root). For the routine
job of bringing a profile up to a new browser version, use `cmd/refresh`
([Refreshing profiles](refreshing-profiles.md)); this page is what
it runs underneath and what to do by hand. Why captures are the evidence:
[decision 0003](decisions/0003-evidence-rule.md); HTTP/3:
[decision 0026](decisions/0026-http3-capture.md); versions and inherited
captures: [decision 0035](decisions/0035-version-policy-and-capture-inheritance.md).

| Tool | What it does | Run by `cmd/refresh` |
|---|---|---|
| `cmd/refresh` | Brings a profile up to a new browser version, end to end | (itself) |
| `cmd/capture` | Captures the installed browsers against the capture server | yes |
| `cmd/hellogen` → `cmd/h3gen` → `cmd/kindsgen` | Derive a profile's generated sections from its captures | yes |
| `cmd/realcheck` | Compares mimicry with the real browsers through public fingerprinting services | yes (`-realcheck`) |
| `cmd/pageshape` | Records a replayed real page's shape | no; investigation |
| `cmd/tracestat` | Lists features a capture's samples disagree on | no; investigation |
| `cmd/diff` | Diffs two single-connection captures | no; investigation |
| `cmd/clientcapture` | Captures any other client | no; investigation |
| `cmd/interop` | Runs QUIC and HTTP/3 against other implementations | no; investigation |

## The capture tool

`moon run harness:capture` launches every installed browser (Chrome,
Firefox, Safari) against an in-process capture server and writes new
captures to `testdata/captures/`. It never overwrites; delete a
capture to retake it. Use visible windows for captures you commit: headless
Chrome reports `HeadlessChrome` in its user agent. On Linux with no
display (a server, WSL), the tool starts Xvfb for the browsers' windows:
they run windowed there, not headless. moon skips this task
when `CI=true`.

Each browser is sampled three times (`-samples`, or per scenario as
`-scenarios page:3,http1:1`), each on a fresh server and port so no
connection or TLS session carries over. If the samples differ only in
extension order, the capture records `"extensionOrder": "shuffled"`
(Chrome), otherwise `"fixed"` (so the default scenario needs at least two
samples to tell). Any other difference between samples fails the capture:
it is randomness a profile cannot describe and needs investigating.
`-summary FILE` appends a JSON line per written scenario (profile name,
user agent, samples, seconds); `-preflight` prints what a capture on the
capture machine needs (browser versions, console user, CA trust, resolver, aioquic,
Safari's proxy) as JSON and exits.

The capture page loads like a site: a stylesheet, an image and a script on
one connection, then a 16 MiB download, a 200 KB upload, a request carrying
a header whose Huffman coding is longer than its value, and a navigation to
`/nav` with two cookies (scoped to `/nav` and deleted there, so Safari keeps
nothing). The page ends by requesting `/done`; an HTTP/2 connection that
ends before that is a truncated trace and is skipped, logged with its last
frames. `<name>.h2trace.json` is a JSON array with every sample's trace:
every frame the browser sent and each header block's HPACK instructions.
The profile JSON still comes from the first request only.

Chrome starts on `about:blank` and gets the capture URL 5 s later: a fresh
profile rebuilds its certificate verifiers within seconds of launch and
retires every open connection, which a long-running browser never shows a
server (on a warm Mac, rebuilds come 0.4–3 s after launch). A cold
Mac can take longer: a single-host page whose finishing connection does not
start with the navigation (`/`) moved connection partway, and the tool
takes that sample again, up to twice, rather than record a subresource as
the profile's request.

`-chrome-app PATH` captures another Chrome build than
`/Applications/Google Chrome.app`: Chrome for Testing, for a version no
longer installable as the brand. Name such a capture
`<name>.<scenario>.chrome-for-testing.<suffix>`
(`.pagetrace.json.gz`, `.h1trace.json.gz`). The tools that list a
profile's captures skip it; only the network stack's fields read it
(`http2.openingRecords`, `http1.uploadRecords`, `tls.trailingDotSni`,
`http3.qpack.resumed`; `capture.TestingPages`), since the testing build
shares the brand's network stack but not its headers
([decision 0042](decisions/0042-http2-opening-records.md),
[0052](decisions/0052-http1-upload-records.md)). `-firefox-app PATH`
captures another Firefox than the installed one, an application on macOS
or `firefox.exe` on Windows: an older release from Mozilla's archive, kept
beside the installed one with its updates off (a read-only bundle on
macOS, `distribution\policies.json` with `DisableAppUpdate` on Windows).
It is the brand, so its captures are named and read as any other.
`cmd/refresh` passes both flags to every machine of its run, so give it one
machine's browsers at a time. Every Chrome launch gets
`--use-mock-keychain` and `--password-store=basic`: they change only how
Chrome encrypts its stored cookies and passwords, never the wire, and keep
a testing build (which has no login-keychain item of its own) from asking
for the keychain on screen.

## Browser setup

- **Chrome, Edge and Firefox** run with throwaway profiles (no
  extensions, default settings) and resolve the capture hosts to
  127.0.0.1 themselves; Chrome and Edge (both Chromium, launched with the
  same flags) ignore certificate errors and Firefox gets a certificate
  exception (or, for pages across several hosts, trusts the system's
  roots). Nothing on the system changes.
- **Safari** cannot be isolated: it opens a tab in the user's own window
  (closed afterwards), so leave a Safari window open: with none, closing
  the first sample's tab closes its window, and the next sample opened
  nothing (2026-10-30, Safari 27: "no request within 1m30s"; a sample cut
  short that way can leave its cookies for the next run). It must have
  **no content blocker or other extension enabled** while capturing (they
  block requests and strip cookies) and
  default privacy settings. It resolves through the system, so the capture
  tool runs a DNS stub answering `*.test` with 127.0.0.1
  (`internal/dnsstub`), reached through `/etc/resolver/test`, created once:

  ```sh
  sudo mkdir -p /etc/resolver && printf 'nameserver 127.0.0.1\nport 15353\n' | sudo tee /etc/resolver/test
  ```

  Capture names are under `.test`, never `.localhost`: WebKit treats every
  `*.localhost` host as a site of its own, which turns same-site requests
  cross-site.
- **The capture CA.** Safari must trust it, and so must the system for
  Chrome's and Edge's HTTP/3 (see below). The CA lives in
  `<user config dir>/mimicry/capture-ca/`, outside the repo, and is
  name-constrained to `test` and `localhost` with IP addresses excluded, so
  it cannot vouch for any real site. Trust it once; the capture command
  prints this when needed:

  ```sh
  security add-trusted-cert -r trustRoot -p ssl -k ~/Library/Keychains/login.keychain-db \
    ~/Library/Application\ Support/mimicry/capture-ca/ca.pem
  ```

  To undo: `security remove-trusted-cert` with the same file, then delete
  the directory.
- **The second Mac** (a Mac kept on macOS 26, reachable over ssh; pass its
  ssh alias with `cmd/refresh -mini`; Chrome, Firefox and Safari 26) has
  no Go: `cmd/refresh` cross-compiles `cmd/capture` and `cmd/realcheck` for
  darwin/arm64 and copies them to a `refresh-<stamp>/` directory
  there. It needs, once: its ssh user logged in at its screen (Screen
  Sharing is enough; browsers opened over ssh show on that session), its
  own capture CA trusted there (`security add-trusted-cert` as above, at
  its screen), `/etc/resolver/test` (Safari), python3 with aioquic (full
  mode), and each newly installed browser's first launch approved by
  Gatekeeper (open it once at the screen, or `xattr -d
  com.apple.quarantine` the app). The preflight checks each of these.
- **The Windows machine** (Chrome and Firefox on Windows, `cmd/refresh
  -windows`): its setup is in
  [Refreshing profiles](refreshing-profiles.md#the-windows-machine).
- **The Linux machine** (Chrome and Firefox on Linux, `cmd/refresh
  -linux`, a WSL distribution of the Windows machine with `-wsl`): its
  setup is in [Refreshing profiles](refreshing-profiles.md#the-linux-machine).
  Each browser trusts the capture CA in its own throwaway profile, never the
  system.
- **Safari on an iPhone** (`ios-safari`): served from the Mac over the
  network; see [below](#safari-on-an-iphone).
- **Chrome on an iPhone** (`ios-chrome`): the same, with Chrome; see
  [below](#chrome-on-an-iphone).
- **Firefox on an iPhone** (`ios-firefox`): the same, with Firefox; see
  [below](#firefox-on-an-iphone).

## Safari on an iPhone

An iPhone cannot run the capture tool, so the Mac serves the captures on
its network address and the phone's Safari reaches them over Wi-Fi
([decision 0051](decisions/0051-ios-safari-over-the-network.md)). Use an
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
0051), the SOCKS5 proxy scenarios (the phone's setting takes an HTTP
proxy only) and `cookies-edge` (its `lo-*` steps navigate to
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
<mac-ip>` ([Refreshing profiles](refreshing-profiles.md#the-iphone)): the
iPhone captures the fingerprint layer and the scenarios the generators
derive from, and the profile inherits a scenario from macOS Safari's
captures only where the iPhone's own show no difference in its area
([decision 0051](decisions/0051-ios-safari-over-the-network.md)).

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
  http/1.1` ([EVIDENCE](../profiles/EVIDENCE.md), Not covered).

```sh
go run ./cmd/capture -browsers ios-firefox -listen <mac-ip> -scenarios page:3
```

`cmd/refresh -browser firefox-ios -listen <mac-ip>` refreshes its profile
the same way as `safari-ios`, with the same scenarios.

## Scenarios

Scenarios (`internal/server/scenario.go`, `-scenarios`, default all but
replays) capture the same browser under other server behaviour:
server-set cookies, unusual SETTINGS, a slow download, an idle connection,
and HTTP/1.1 only. Each writes `<name>.<scenario>.h2trace.json`; its first
request is compared with the default page's and any change is logged as a
finding. The `http1` scenario offers only `http/1.1` in ALPN and writes
`<name>.http1.h1trace.json` instead: per sample, every request of the page
load across the browser's connections, request line and header lines
verbatim, with the connection each came on. The HTTP/1.1 recorder rejects a
line that does not end in CRLF instead of recording it as if it did. The
upload scenarios (`http1-upload`, `h2-upload`) keep every TLS record the
client sends, not only the first 16: per HTTP/1.1 request, its head and
body lengths and the records that carried it as runs of one size with
their arrival times; per HTTP/2 connection, every record with the frames
starting in it. Their traces are gzipped (`.h1trace.json.gz`,
`.h2trace.json.gz`; [decision 0052](decisions/0052-http1-upload-records.md)).
The `hrr` scenario's page fetches three times from `www.hrr-b.test`,
whose every connection the server answers with a HelloRetryRequest
(raw, since crypto/tls sends no cookie): the first group the hello lists
without a key share, and a 32-byte cookie. It records the second
ClientHello as the connection's `retry` and closes it, so each fetch fails
and browsers retry on fresh connections, Firefox with fallback hellos
(no `compress_certificate`) too; every one of them counts.
Add a scenario when a behaviour might depend on the server; reuse the
default page wherever possible.

### Real-page replays

Replay scenarios load the shape of real pages with synthetic content, to
check what the small capture page cannot: hundreds of requests of every
kind, dozens of hosts and connections, kilobytes of cookies. Record a page
with a throwaway Chrome profile (`--log-net-log=FILE
--net-log-capture-mode=IncludeSensitive`, 5 s on `about:blank` first, as
the capture tool does), then
`go run ./cmd/pageshape -site nike.com -out testdata/pages/nike.com.json FILE`;
delete the log afterwards, it holds real cookies. Only lengths and
character classes leave it. `-scenarios replay-<site>` replays one page,
`replay` all of `testdata/pages` (not part of the default run: each sample
takes the page's length, up to 35 s).

Each host is served under `.test` with its site kept apart
(`www.nike.com` → `www.nike-com.test`, `server.ReplayHost`) and its own
certificate from the capture CA; generated documents (top and every
iframe's) schedule the recorded requests with the element or API giving
their `sec-fetch-dest` and mode; cookies of the recorded shapes are set and
deleted again at the end. The capture is
`<name>.replay-<site>.pagetrace.json.gz` (gzipped: tens of MiB of JSON
otherwise): per sample, every HTTP/2 connection's trace (with its host) and
every HTTP/1.1 request. Its profile keeps only the ClientHello and user
agent, since the first request on the connection that ends the page varies
with timing. HTTP/3 parts of a real page are replayed over HTTP/2.

### The click scenario

The click scenario (`nav-redirect-click`) has the tool click as a user
does, so each navigation has user activation (`Sec-Fetch-User`; a script's
`click()` has none): a page waiting for a click takes the title
`mimicry-click` and fills its window with its link or submit button, and
the tool clicks its middle once per URL (again after 6 s if it is still
there). Chrome and Edge are clicked over the DevTools protocol
(`Input.dispatchMouseEvent`) and Firefox over WebDriver BiDi
(`input.performActions`, with `remote.prefs.recommended` off), both on
their throwaway profiles. Safari is clicked through the system's mouse
events (`CGEventPost`, after its window is brought to the front), which
needs Accessibility access for the app running the tool; an ssh session has
none, so a Mac driven over ssh (such as the second Mac) cannot take this scenario. The mouse moves to
Safari's window: leave the machine alone while it runs.

### Proxy scenarios

Proxy scenarios (`proxy-*`) put the capture proxy (`internal/proxy`:
HTTP CONNECT, absolute-form, SOCKS5, all recorded) in front of the server;
a scenario with a plain port gets what goes to that port (tunnels and
absolute-form requests) on the server's plain side, as if the proxy had
connected there, and each connection the server records carries the proxy
connection it came through (`capture.WebSocket.ProxyConn`). Chrome gets
`--proxy-server`, Firefox proxy prefs. With credentials (`proxy-auth`,
`proxy-plain-auth`, `proxy-websocket-auth`) the tool answers the browser's
407 over Chrome's DevTools protocol (browser-level `Fetch`) and Firefox's
WebDriver BiDi (with `remote.prefs.recommended` off, so the remote agent
changes no networking prefs), as a user typing into the login dialog.

Safari takes its proxy from the system: point the active network service at
the tool's PAC script once, which proxies only `*.test`, and turn it off
after (the first run after setting it may time out while Safari fetches the
script; run again). The commands below use `sudo`; an administrator
account may not need it:

```sh
sudo networksetup -setautoproxyurl <service> http://127.0.0.1:15356/mimicry.pac
sudo networksetup -setautoproxystate <service> off
```

The tool also accepts manual web and secure web proxies at
`127.0.0.1:15354` instead of the script; it then passes every non-`*.test`
request through to the network unrecorded, since the whole Mac uses the
proxy, and the Mac has no working proxy between samples or after the run
until the proxies are turned off. Manual proxies send everything to the
HTTP proxy, so with them the tool skips Safari's SOCKS5 scenarios (logged,
and Safari counts as failed); those need the PAC script.

For Safari's credential flow (`proxy-auth`, `proxy-plain-auth`,
`proxy-websocket-auth`) macOS itself must hold the credentials: install a
`com.apple.proxy.http.global` device profile (manual proxy at
`127.0.0.1:15354`, `ProxyUsername`/`ProxyPassword`, `PayloadScope:
System`), then type the credentials into the proxy login dialog macOS
raises; saving them in System Settings, `networksetup` or the keychain was
not enough (see [the proxies report](../internal/harness/reports/proxies-2026-10-24.md)).
Remove the profile afterwards. `cmd/refresh` leaves these scenarios out of
Safari's runs: capture them by hand.

### HTTP/3 scenarios

`h3-discovery` and `h3-page` also serve QUIC on the TLS port's UDP twin and
advertise it with `Alt-Svc: h3=":<port>"; ma=86400` on every response; the
browser discovers HTTP/3 itself (decision 0026). The page's capture adds
`quic`: each QUIC connection's datagrams with every coalesced packet's
header and frames (the recorder, `internal/quictrace`, decrypts them:
Initial keys from the client's first Destination Connection ID, Handshake
and 1-RTT keys from the server's TLS key log, 0-RTT keys from the resumed
ticket's PSK), its ClientHello and transport parameters, its HTTP/3
streams, frames and QPACK instructions, and how it closed; TCP connections
carry `ms` (when their ClientHello arrived) on the same clock, so a race
shows.

Chrome and Firefox refuse QUIC with a certificate that does not chain to a
built-in root: by default the capture records the attempt.
`-h3-local-roots` lifts the refusal (Chrome: `--origin-to-force-quic-on`
with the page's hosts on unused port 1, which forces nothing the page
visits; Firefox:
`network.http.http3.disable_when_third_party_roots_found=false`), records
it in the page's `conditions` and names the capture
`<name>.<scenario>.local-roots.pagetrace.json.gz`. Chrome also ignores
Alt-Svc unless the certificate verified, so it needs the capture CA trusted
by the system, as Safari does (the tool warns).

Failure, version, transfer, early-data and stream-limit scenarios:

- `h3-blackhole`: Alt-Svc names a UDP port that records every datagram and
  answers none.
- `h3-broken`: HTTP/3 works, then one connection goes silent mid-request,
  then the server closes another and answers no QUIC connection started
  after that.
- `h3-vn`: quic-go speaking only version 2, so a version 1 Initial gets
  Version Negotiation.
- `h3-v2`: aioquic behind the server's UDP socket, which relays and records
  both directions; aioquic upgrades a client that lists version 2
  compatibly (RFC 9368). Needs `python3` with aioquic (`pip install
  aioquic`; the script is embedded, `internal/server/aioquic_h3.py`; its
  connections have no `h3` record); without it the tool skips the scenario
  and exits non-zero.
- `h3-download`: a 50 MiB incompressible response over HTTP/3, then small
  requests: credit, ACKs, key updates.
- `h3-early`: fetch POST, PUT and DELETE, an XHR POST, a GET and a POST
  together, a GET, HEAD and OPTIONS, a form POST navigation, each the first
  request on a connection resumed after the last idled out: the server
  announces a 3 s idle timeout, since after a server's close Firefox sends
  the next request over TCP.
- `h3-too-early`: the same page; the server answers 425 Too Early to every
  request whose stream started in a 0-RTT packet, read from quic-go's
  per-packet trace (`earlyStreams`), as its HTTP/3 layer only exposes a racy
  `HandshakeComplete`.
- `h3-resume-table`: the `h3-early` schedule's resumed connections, every
  request a GET (fetches, a pair made together, an image, an XHR, a
  navigation), against a server allowing `h3-page`'s 64 KiB QPACK table:
  what the browser's encoder does in 0-RTT with the SETTINGS it remembers,
  and once the server's arrive (issue #90). Two connections carry a late
  fetch 500 ms in with a header no request had (`x-late`), so every
  encoder, Firefox's too, writes after the server's SETTINGS.
- `h3-streams-blocked`: quic-go allowing 2 concurrent bidirectional
  streams (`initial_max_streams_bidi` 2, raised by MAX_STREAMS as each
  stream ends; unidirectional left at 100), and two bursts of 6 fetches
  answered 300 ms late each, so the page waits on the limit: the
  browser's STREAMS_BLOCKED and where waiting requests go.

Their steps start on a fixed schedule whatever the browser takes
(`H3Step.NoWait`; each URL carries `?t=`, the script's
`performance.now()`), the pages' HTTP/2 traces carry `ms` on the datagrams'
clock, and every QUIC connection also records the server's datagrams
(`server`: Initial and Handshake packets opened, Version Negotiation
versions, Retry integrity checked, 1-RTT headers unmasked), its transport
parameters, and datagrams from another client port (`path`, a migration).
Chrome and Firefox need `-h3-local-roots` for all but `h3-blackhole` (no
certificate ever arrives). hellogen and h3gen read all but `h3-v2`
(`server.H3EdgeCaptures`);
`TestHTTP3EdgeMatchesBrowser` and `TestHTTP3DownloadMatchesBrowser`
replay them. Results:
[http3-edge](../internal/harness/reports/http3-edge-2026-10-25.md),
[h3-early](../internal/harness/reports/h3-early-2026-10-26.md),
[h3-streams-blocked](../internal/harness/reports/h3-streams-blocked-2026-10-31.md).

`bodiless` makes fetch() POST, PUT and PATCH and a form POST navigation
with no body, first on `www.h3-c.test`, which never advertises HTTP/3
(so over HTTP/2), then on `www.h3-a.test` after a warm-up fetch and a
navigation 2 s later (over HTTP/3 by then): whether each sends
`content-length: 0`, where, and how the stream ends. Chrome and Firefox
need `-h3-local-roots` for its HTTP/3 half; kindsgen derives `emptyBody`
from it and `TestBodilessMatchesBrowser` replays it.

`client-hints` starts on `www.ch-a.test` with no opt-in (the low-entropy
baseline, and a fetch whose response's `Accept-CH` a subresource cannot
set), then a navigation answering `Accept-CH` for every hint Chrome knows:
its document's fetch, image, script and iframes, to its own origin,
`sub.ch-a.test` and the third party `www.ch-b.test`, the next navigation,
navigations to the other two, delegation to the third party through
`Permissions-Policy` and through a `Delegate-CH` meta tag, a narrower
`Accept-CH`, and `Clear-Site-Data: "clientHints"`. On a fresh site,
`www.ch-k.test`, navigations answer `Critical-CH` (a hint not yet sent,
one already sent, one `Permissions-Policy` disables, `Width`, `Save-Data`,
an unknown one), the same answer every time, so a restart that loops
shows; then `Clear-Site-Data: "*"`. Back on `www.ch-a.test`, the opt-in
again, then fetches with a header of the page's own and a JSON body, a
navigation redirected within the origin, a form POST answered with
`Critical-CH`, a navigation redirected from `sub.ch-a.test` into the
opted-in origin, a redirect answering `Accept-CH` and `Critical-CH`, and
`Clear-Site-Data` on a fetch, followed by a fetch and a navigation
(redirects land on steps no page makes, `H3Step.Redirect`). The opt-in
with `Critical-CH` follows over HTTP/1.1 (`www.ch-h1.test`) and HTTP/3
(`www.ch-h3.test`, as `bodiless`). Chrome and Firefox need
`-h3-local-roots`. Capture Chrome with its window on one display: beside a
display of another DPR, its navigations and subresources report different
DPRs, which kindsgen refuses. kindsgen derives `clientHints` from it
([decision 0055](decisions/0055-client-hints-opt-in.md)), and
`TestClientHintsMatchesBrowser` replays it; a quick refresh recaptures it,
since the full version moves with every release.

### URL parsing

`urls` has the browser parse 43 URLs, each fetched (`no-cors`), then each
loaded in an iframe, 5 s at most each: a query and a path with each of
space, `"`, `'`, `<`, `>`, `` ` ``, `{`, `}`, `|`, `\`, `^`, `é`, a lone
`%` and `%41`; dot segments, as `%2e` too; hosts uppercase,
internationalized (`Bücher.urls.test`), ending in a dot (`dot.urls.test.`,
named only so, and `www.urls.test.`, which a browser may pool with
`www.urls.test`), on an explicit `:443` and on the page's port with a
leading zero, and a malformed punycode label (`xn--a.urls.test`). Each
request's path starts with its case, `/c/f-<case>/` or `/c/n-<case>/`,
and the server answers any of them, so whatever the browser makes of the
rest arrives as it was sent. The page then goes to `http://` on the plain
port for the IPv4 forms (`127.1`, `0x7f.1`, `0177.0.0.1`, `2130706433`,
`127.0.0.1.`): from an `https://` page Safari blocks even loopback
`http://`. Each half reports the cases that failed at `/report` (a hint:
the wire is the evidence) before the second asks for `/done`.

The capture tool forwards port 443 to the TLS port for the scenario
(macOS lets a user bind 443 only on every address; it then lets only
loopback peers through). The server answers a ClientHello without SNI,
which Safari sends for a host ending in a dot, with a certificate for
both dotted hosts, and records one naming a host with its dot, which Go's
TLS refuses before anything else, as a connection with `refused` set
(`capture.Connection.Refused`). kindsgen derives `urls` from it
([decision 0057](decisions/0057-url-parsing-measured.md)), and
`TestURLsMatchBrowser` sends every URL through mimicry and compares.
Inheriting works only from older versions, so a browser's newest
profiles need their own capture.

### Client-certificate scenarios

`client-cert-ec`, `client-cert-rsa`, `client-cert-ec-tls12` and
`client-cert-rsa-tls12` serve the resume page (new connections to three
hosts, sequential and parallel, HTTP/2 and HTTP/1.1) from a server that
asks every connection for a client certificate (`RequestClientCert`,
naming the run's test client CA), TLS 1.3 or, in the `-tls12` pair, TLS
1.2 at most, while the browser holds one identity: an EC P-256 or an
RSA-2048 key, one per scenario so nothing is left to choose. Besides the
page (`.pagetrace.json.gz`, whose `connections` leave out failed
handshakes), each writes `<name>.<scenario>.clientauth.json`: per sample,
every TLS connection in handshake order (`capture.ClientAuth`): its page
connection number (0 for a handshake that failed: a client giving up on
it), host, when its ClientHello arrived and how long the handshake took
(`handshakeMs`, where choosing and signing shows), version and suite,
what the ClientHello offered to resume (`pskIdentities`, `ticket`) and
whether the server resumed it, the server's CertificateRequest (its
`signature_algorithms`, `signature_algorithms_cert` and CAs, as sent),
the client's messages after its ClientHello in order (`flight`: an empty
Certificate is `certificate` with no `certificates`), each certificate
sent (subject, issuer, key, whether a CA), the CertificateVerify scheme,
and the paths of the requests the connection carried. The server reads
its own TLS 1.3 key log to decrypt both sides' handshake flights
(`internal/tlsflight`); TLS 1.2's are in the clear up to
ChangeCipherSpec. A resumed handshake has no CertificateRequest: the
server restores the certificate from the ticket.

The identities come from a client CA made for the run, in memory, and
forgotten with it (`internal/clientcert`): "mimicry capture client CA
(test)", constrained to client authentication and to no DNS name or IP
address, never installed as a trust anchor. The tool puts the scenario's
identity (a PKCS#12 file holding it and the untrusted CA) where the
browser reads identities, and takes it out after the scenario, however
it ends:

- **Chrome and Edge on macOS**: a temporary keychain in the user's
  search list (`mimicry-client-cert-*.keychain-db` in the temporary
  directory), its key open to the browser's code-signing team
  (`set-key-partition-list`), plus `AutoSelectCertificateForUrls`
  entries in the browser's policy domain (`defaults write
  com.google.Chrome`, `com.microsoft.Edge`) for the page's hosts,
  filtered on the CA's name; without the policy Chromium asks which
  identity to send even when one fits. Entries of the user's own stay.
  Over ssh (the second Mac) these commands run in the logged-in user's
  session through one-shot launchd jobs: a keychain unlocked from ssh
  stays locked to the browser, which then asks for its password on
  screen (2026-11-08, Edge 157 on macOS 26).
- **Chrome, Edge and Firefox on Windows**: the user's store
  (`Cert:\CurrentUser\My`, the CA among the user's intermediate CAs),
  plus the policy entries for Chrome and Edge under
  `HKCU\Software\Policies\Google\Chrome` and `Microsoft\Edge`
  (`AutoSelectCertificateForUrls`); Firefox reads the store through its
  `osclientcerts` module (`security.osclientcerts.autoload`, its
  default there). On the PC measured, each handshake Firefox answered
  with the identity took 7 to 11 s; the page's wait allows for it.
- **Firefox on macOS and Linux**: its throwaway profile's own NSS
  database (`pk12util`, so NSS's tools are needed on macOS too: `brew
  install nss`), with `osclientcerts` off so no identity of the user's
  keychain is offered.
- **Chrome and Edge on Linux**: the throwaway profile's NSS database
  (HOME is the profile), and the policy in a managed policy file only
  root may write (`/etc/opt/chrome/policies/managed/mimicry-client-cert.json`,
  `/etc/opt/edge/...`): the tool refuses the scenarios without it and
  prints it; `cmd/refresh` writes it as root in WSL around the capture
  step and removes it after.

Firefox gets `security.default_personal_cert` "Select Automatically": the
first matching identity, no dialog. The tool takes the identity out when
it is interrupted, terminated or loses its ssh session (SIGHUP; SIGPIPE
is ignored so that writes to a gone terminal do not end it first).
`capture -cleanup` removes whatever a killed run left, each step
whatever the others do: everywhere the PKCS#12 files' temporary
directories; on macOS the one-shot launchd jobs still loaded
(`org.mimicry.capture.*`, booted out), the keychains (in the search list,
or still only in the temporary directory) and the policy entries; on
Windows the identities, the CA and the policy entries (it finds them by
the CA's name); on Linux it names a policy file left for root to remove.
`cmd/refresh` runs it on every Mac a plan captured a client-certificate
scenario on, however the run ends. Safari
(on macOS and the iPhone) is refused (`Scenario.UnsupportedBy`,
`UnsupportedOnIOS`): Safari 27 sent an empty Certificate with the
identity in a temporary keychain, an identity preference for the host and
the CA trusted, and stalled with it in the login keychain, probably on a
hidden prompt (2026-11-08).

## Deriving a profile

A built-in profile is a JSON file in `profiles/data/`; its accessors are
generated by `cmd/profilegen` (`go generate ./profiles`). Every built-in
profile must pass `profile.Parse`, enforced twice: the generator refuses to
generate from an invalid file (`TestGeneratedFileUpToDate` in
`cmd/profilegen` runs it over every file), and every e2e run loads each
built-in through `profiles.ByName`, which parses it
(`TestCapturesFromNamesCapturedScenarios`). Captures record one connection
and are not complete profiles, so the harness loads them with
`profile.Decode`. A profile without a matching capture in
`testdata/captures/` is not finished.

A new version starts as a copy of the previous one
([decision 0004](decisions/0004-complete-profile-files.md)): replace every
captured field from the new capture, and treat everything copied that a
capture cannot show (`http2.hpack`, header slots, `cookies`, `http1` slots)
as unverified until the e2e tests pass against the new traces. `cmd/refresh`
does the copy, sets `capturesFrom` and runs the generators below.

### The generators

Sections derived from captures are regenerated, never hand-edited. Run the
three generators in this order, each with
`-profile ../../profiles/data/<name>.json` and each refusing a sample its rule
does not describe:

1. `go run ./cmd/hellogen`: the ClientHello's shape; also
   `http2.openingRecords`, how the browser cuts a new HTTP/2 connection's
   opening into TLS records (from every connection whose first request was
   waiting as it opened, testing-build captures included; decision 0042),
   `http2.goawayReply`, the GOAWAY sent on the server's, from every HTTP/2
   connection the capture server sent GOAWAY on,
   `tls.quic`, the QUIC ClientHello, from every h3 connection's hello,
   `tls.quic.versionNegotiation`, Firefox's other hello after Version
   Negotiation, from the `h3-vn` captures, `tls.websocketH1` from the
   `websocket` capture, `tls.helloRetry`, where the hello sent again after
   a HelloRetryRequest puts the cookie, from every retried connection of
   the `hrr` capture (left out, not measured, for a profile without one),
   `http1.uploadRecords`, how the browser cuts
   an HTTP/1.1 request with a body into TLS records, from every request
   of the `http1-upload` capture (testing-build captures included;
   decision 0052), `tls.trailingDotSni`, the server_name of a host
   ending in a dot, from the `urls` capture's `h-dot` case, fetched and
   navigated to (`keep` when the server refused a ClientHello naming it
   with its dot, `omit` when the request came with no SNI; decision 0057),
   and `tls.clientAuth`, how the browser answers a CertificateRequest,
   from every connection of the four `client-cert-*` captures: the
   CertificateVerify scheme per TLS version and key type (samples must
   agree), `resume` (a later connection offered to resume a session the
   certificate went in) and `restart` (per sample: no first connection to
   a host answered the request, a later one carried it, and no connection
   answered with an empty Certificate; samples must agree), left out for
   a profile without the captures (decision 0064).
2. `go run ./cmd/h3gen`: `quic`, and `http3` but for kindsgen's header lists
   and priorities. It checks every value against every connection and pins
   the few samples that differ in `sampleGaps`.
3. `go run ./cmd/kindsgen`: the profile's `kinds`, `context`, navigation
   `variants`, `emptyBody`, `clientHints` and `urls`, from the captures
   listed below.

hellogen and h3gen read the edge captures too (`server.H3EdgeCaptures`:
`h3-blackhole`, `h3-broken`, `h3-vn`, `h3-download`, `h3-early`,
`h3-too-early`, `h3-streams-blocked`), from which h3gen derives
`quic.loss` (probes, give-ups, abandon closes, path degrading),
`quic.keepAlive`, `quic.ack`, `quic.credit`, `quic.keyUpdate`,
`quic.versionNegotiation`, `quic.streamsBlocked` (left out, not measured,
for a profile without an `h3-streams-blocked` capture; every other edge
capture is required), `http3.discovery.fallback` and `http3.earlyData`
(which methods go in 0-RTT, where a held request leaves, the retry after
425), `http3.earlyStreamsWithRequest` (the handshake streams in the
0-RTT request's packet), `http3.closeOnGoaway` (the browser closes a
connection the server sent GOAWAY on, also from `h3-discovery`),
`http3.discovery.dialDelayMs` and `resumedDialDelayMs` (how long after
the request that waits for it a fresh or resumed dial starts), and
`quic.zeroRtt` from the resumed connections of both sets; where the h3
captures have none (Safari) it holds the edge captures' resumed first
flights to the full handshakes' rules. From `h3-resume-table` alone
(`server.H3ResumeTableCaptures`) it derives `http3.qpack.resumed`: a
resumed connection's encoder stream must open with the server's capacity,
in 0-RTT on at least one connection, and every connection whose encoder
wrote again in 1-RTT must agree on whether the capacity was set a second
time there (`remembered_repeat`, Chrome and Edge) or not (`remembered`,
Firefox); without the capture it stays unset. hellogen checks every resumed
QUIC hello of the edge captures and derives `tls.quic.earlyDataBefore`
from them. kindsgen reads only `h3-discovery` and `h3-page`
(`server.H3ProfileCaptures`). Each
generator has its own `TestRegenerateReproducesProfiles`, which fails while
a committed profile differs from what it derives from the committed
captures. `go generate ./profiles` refreshes the accessors afterwards.

kindsgen's captures, and what each gives the profile:

- `<name>.kinds.pagetrace.json.gz` and `<name>.kinds-http1.pagetrace.json.gz`:
  every kind over HTTP/2 and HTTP/1.1, and the context rules;
- `<name>.kinds-plain.pagetrace.json.gz`: the non-secure variants;
- `<name>.cors.pagetrace.json.gz`: `context.preflightCache`, and the cors
  page's fetches, merged into the Fetch kind (its PUT variants, and
  cross-site fetches with and without a header the page set: the
  `pageSet` condition);
- `<name>.cookies.pagetrace.json.gz`: the cross-site form POST navigation
  variant (`c-nav-post.so`), the image and injected_image `{same-site,
  redirected, crossOriginRedirect}` variants, and slots its requests add
  (Safari 27's `cookie` on a redirected same-origin image);
- `<name>.cors-edge.pagetrace.json.gz`: fetch redirects merged into the
  Fetch kind (PUT/DELETE hops, the GET hops of 303s and POSTs, a
  same-origin chain, a cross-site hop within its origin); every redirect's
  request takes its `crossOriginRedirect`, `originBeforeRedirect`,
  `methodChangedByRedirect` and `insecureBeforeRedirect` conditions
  from the captured request it follows;
- `<name>.redirects2.pagetrace.json.gz`: redirects of every kind
  (`redir-<kind>`: images, crossorigin scripts, XHR, fetch() methods and
  their PATCH preflights, form POSTs and the navigation chain; Chrome's
  error-page reload left out);
- `<name>.nav-redirect-*.pagetrace.json.gz` (any number, optional):
  navigations whose start URL `/p/<label>` redirects to `/p/<label>.hop<n>`,
  over HTTP/2 or plain HTTP/1.1: typed ones (no Referer; their requests take
  `site: none`) and a page's (`nav-redirect-page`, `nav-redirect-form`,
  `nav-redirect-click`; the label's sites). A clicked navigation
  (`nav-redirect-click`) is grouped with the script's of its condition, so
  its variant gains the `sec-fetch-user` slot; their orders must merge. A
  request one sample sent in a form no condition explains (browser state)
  is left out only if pinned in `sampleGaps` with why, and a pinned one
  missing from its capture is an error;
- the cookie twins (decision 0048, optional): `<name>.cors-edge-cookies`,
  `<name>.redirects2-cookies` and `<name>.nav-redirect-*-cookies`, each its
  original with cookies set on the hosts its hops go to. Their requests
  are read with their original's and grouped by the same conditions, so a
  hop variant gains the `cookie` slot where the twin's hops carried
  cookies; their orders must merge;
- `<name>.headers-order.pagetrace.json.gz`: `context.headerTable`, and
  fetch/XHR requests with headers the page set;
- `<name>.websocket.pagetrace.json.gz` and
  `<name>.websocket-h2.pagetrace.json.gz`: the `websocket` kind (handshakes
  over HTTP/1.1 and as HTTP/2 extended CONNECTs, whose `:protocol` must
  follow `http2.pseudoHeaderOrder`, and `ws://` from the plain page as its
  `insecure` variant);
- `<name>.h3-discovery*.pagetrace.json.gz` and
  `<name>.h3-page*.pagetrace.json.gz` (unforced and `.local-roots`):
  `http3.headers` (each kind's HTTP/3 list against its HTTP/2 one:
  Firefox's pseudo-header order, dropped `te`, `alt-used` placement),
  `http3.high`/`http3.low` (priorities measured only over HTTP/3), the
  `kinds.xhr` POST variant, and Safari's `injected_image.low` from its
  `h3-page` requests over HTTP/2; a layout-driven priority is pinned in
  `h3PriorityGaps`.
- `<name>.bodiless*.pagetrace.json.gz`: `emptyBody` on the navigation
  (the form POST) and `kinds.fetch` (`deriveEmptyBodies`): the methods
  sending `content-length: 0` with no body, its place (the list's, or
  last before `te`) and, over HTTP/2, an empty DATA frame ending the
  stream; samples, methods and HTTP/3 must agree. Without the capture
  both stay unset.
- `<name>.client-hints*.pagetrace.json.gz`: `clientHints`
  (`deriveClientHints`): the hints and order of the navigation after the
  opt-in to every hint, the header a restart's hints follow, the full
  version and the machine's values (`downlink` the centre of every value
  sent), and `tableOrder`, Blink's order of entry, which must reproduce
  every captured subresource's run. Every captured request's hints must be
  what the model sends. Without the capture, or where no hint beyond the
  lists was sent (Firefox, Safari), it stays unset.
- `<name>.urls.pagetrace.json.gz`: `urls` (`deriveURLs`): the characters
  the browser encodes in a path beyond the set every browser does
  (`pathEncodes`) and whether it refuses the malformed punycode host
  (`checkPunycode`). Samples, and each case's fetch and navigation, must
  agree, and a character the shared set encodes may not go as written.
  Without the capture it stays unset. hellogen reads the same capture for
  `tls.trailingDotSni`.

A variant only the cors, cors-edge or cookies capture shows was measured
over HTTP/2 only, so it has no `http1` list, and mimicry refuses such a
request over HTTP/1.1 (`ErrNotMeasured`). Rerun kindsgen after recapturing
any of these; `TestKindsMatchBrowserTraces` checks the result against the
same traces.

## Other harness tools

- `go run ./cmd/tracestat FILE.h2trace.json...` lists the features a
  capture's samples disagree on (request order, HPACK shapes, priorities,
  frame order exactly; largest DATA frame and median WINDOW_UPDATE within
  2%). A varying feature is either modelled or recorded as a known gap;
  never pick one sample's value.
- `go run ./cmd/diff WANT.json GOT.json` lists every difference in what a
  single-connection capture records (the ClientHello, the HTTP/2 preface and
  the first request's headers), comparing extension order as WANT's
  `extensionOrder` says, and exits 1 if there are any. The rest of a
  profile (`http1`, kinds, variants, context, cookies, proxy, hpack, flow
  control) is not compared.
- `go run ./cmd/clientcapture -scenario NAME -out DIR -name LABEL -- COMMAND {url}...`
  captures any other client: it runs the command three times, each against
  a fresh server, keeps each run's first connection that carried a
  request, and writes the combined capture (logging, not failing, when the
  client varies where browsers do not). `reports/` holds
  comparisons made with it; date them, name the client versions, and keep
  the captures in `testdata/clients/`.
- `go run ./cmd/interop -local` or `-public` (opt-in, never part of
  `go test`) runs every supported profile's QUIC and HTTP/3 against other
  implementations: `-local` starts the QUIC interop runner's server images
  in Docker standalone (handshake, transfer, multiplexing, Retry,
  resumption, 0-RTT, ChaCha20, key update, loss, corruption, blackholes,
  discovery, blocked UDP); `-public` goes to real HTTP/3 sites with
  ordinary homepage GETs (Alt-Svc discovery through the public API, then a
  fresh and a resumed QUIC connection). `-servers`, `-cases`, `-sites` and
  `-profiles` narrow a run; `-addr PORT -setup DIR` reruns the cases
  against a container started by hand. Output goes to a temporary
  directory; copy it to `reports/quic-interop-<date>.md` to keep a run.
- `go run ./cmd/realcheck` (opt-in, never part of `go test`; needs the
  network) compares mimicry with the real browsers as public
  fingerprinting services see them: every supported profile (or those
  `-profiles` names) visits echo endpoints (tls.peet.ws,
  tls.browserleaks.com, fp.impersonate.pro over HTTP/2 and HTTP/3) as a
  navigation, then a few homepages behind bot-management vendors
  (Cloudflare, Akamai, DataDome, HUMAN, Amazon) once each, a few seconds
  apart, with Go's net/http client last for contrast.
  `-browsers chrome,firefox,safari` drives the installed browsers through
  the same visits first; this **takes over the screen** (Chrome and Firefox
  on throwaway profiles over DevTools and WebDriver BiDi, Safari in a new
  window of the user's own, through AppleScript), so keep it to one short
  batch. Answers go to `-out` (default `$TMPDIR/mimicry-realcheck`; it holds
  this machine's public IP, never commit it), and each run compares
  everything there, field by field, into `compare.md` and
  `compare-full.md`: a browser `browser-<name>-<major>-<platform>` (the
  platform realcheck runs on) against the profile
  `<name>-<major>-<platform>`, with per-connection randomness the profile models
  (shuffled extension and transport parameter order, GREASE ECH lengths and
  suites, random packet numbers) told apart from differences.
  `-echo=false -sites=false -profiles none` only compares. Copy
  `compare.md` to `reports/realcheck-<date>.md` to keep a run.
