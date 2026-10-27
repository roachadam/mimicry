# Capturing browsers and deriving profiles

How the `harness/` module captures the real browsers, how a profile's
generated sections are derived from those captures, and the harness's other
tools. Commands run in `harness/` unless they say otherwise. For the routine
job of bringing a profile up to a new browser version, use `cmd/refresh`
([Refreshing profiles](concepts/refreshing-profiles.md)); this page is what
it runs underneath and what to do by hand. Why captures are the evidence:
[decision 0003](decisions/0003-evidence-rule.md); HTTP/3:
[decision 0026](decisions/0026-http3-capture.md); versions and inherited
captures: [decision 0035](decisions/0035-version-policy-and-capture-inheritance.md).

## The capture tool

`moon run harness:capture` launches every installed browser (Chrome,
Firefox, Safari) against an in-process capture server and writes new
captures to `harness/testdata/captures/`. It never overwrites; delete a
capture to retake it. Use visible windows for captures you commit: headless
Chrome reports `HeadlessChrome` in its user agent. moon skips this task
when `CI=true`.

Each browser is sampled three times (`-samples`, or per scenario as
`-scenarios page:3,http1:1`), each on a fresh server and port so no
connection or TLS session carries over. If the samples differ only in
extension order, the capture records `"extensionOrder": "shuffled"`
(Chrome), otherwise `"fixed"` (so the default scenario needs at least two
samples to tell). Any other difference between samples fails the capture:
it is randomness a profile cannot describe and needs investigating.
`-summary FILE` appends a JSON line per written scenario (profile name,
user agent, samples, seconds); `-preflight` prints what a capture on this
Mac needs (browser versions, console user, CA trust, resolver, aioquic,
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
server (on the Mac mini, warm: rebuilds at 0.4–3 s after launch). A cold
Mac can take longer: a single-host page whose finishing connection does not
start with the navigation (`/`) moved connection partway, and the tool
takes that sample again, up to twice, rather than record a subresource as
the profile's request.

## Browser setup

- **Chrome and Firefox** run with throwaway profiles (no extensions,
  default settings) and resolve the capture hosts to 127.0.0.1 themselves;
  Chrome ignores certificate errors and Firefox gets a certificate
  exception (or, for pages across several hosts, trusts the system's
  roots). Nothing on the system changes.
- **Safari** cannot be isolated: it opens a tab in the user's own window
  (closed afterwards). It must have **no content blocker or other extension
  enabled** while capturing (they block requests and strip cookies) and
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
  Chrome's HTTP/3 (see below). The CA lives in
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
- **The Mac mini** (your ssh alias; Chrome, Firefox and Safari 26) has
  no Go: `cmd/refresh` cross-compiles `cmd/capture` and `cmd/realcheck` for
  darwin/arm64 and copies them to `~/mimicry-capture/refresh-<stamp>/`
  there. It needs, once: its ssh user logged in at its screen (Screen
  Sharing is enough; browsers opened over ssh show on that session), its
  own capture CA trusted there (`security add-trusted-cert` as above, at
  its screen), `/etc/resolver/test` (Safari), python3 with aioquic (full
  mode), and each newly installed browser's first launch approved by
  Gatekeeper (open it once at the screen, or `xattr -d
  com.apple.quarantine` the app). The preflight checks each of these.

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
line that does not end in CRLF instead of recording it as if it did. Add a
scenario when a behaviour might depend on the server; reuse the default
page wherever possible.

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
there). Chrome is clicked over the DevTools protocol
(`Input.dispatchMouseEvent`) and Firefox over WebDriver BiDi
(`input.performActions`, with `remote.prefs.recommended` off), both on
their throwaway profiles. Safari is clicked through the system's mouse
events (`CGEventPost`, after its window is brought to the front), which
needs Accessibility access for the app running the tool; an ssh session has
none, so the second Mac cannot take this scenario. The mouse moves to
Safari's window: leave the machine alone while it runs.

### Proxy scenarios

Proxy scenarios (`proxy-*`) put the capture proxy (`harness/internal/proxy`:
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
script; run again; on these Macs `networksetup` changed it without
`sudo`):

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
not enough (see [the proxies report](../harness/reports/proxies-2026-10-24.md)).
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

Failure, version, transfer and early-data scenarios:

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

Their steps start on a fixed schedule whatever the browser takes
(`H3Step.NoWait`; each URL carries `?t=`, the script's
`performance.now()`), the pages' HTTP/2 traces carry `ms` on the datagrams'
clock, and every QUIC connection also records the server's datagrams
(`server`: Initial and Handshake packets opened, Version Negotiation
versions, Retry integrity checked, 1-RTT headers unmasked), its transport
parameters, and datagrams from another client port (`path`, a migration).
Chrome and Firefox need `-h3-local-roots` for all but `h3-blackhole` (no
certificate ever arrives). hellogen and h3gen read all but `h3-v2`
(`server.H3EdgeCaptures`); `TestHTTP3EdgeMatchesBrowser` and
`TestHTTP3DownloadMatchesBrowser` replay them. Results:
[http3-edge](../harness/reports/http3-edge-2026-10-25.md),
[h3-early](../harness/reports/h3-early-2026-10-26.md).

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
`harness/testdata/captures/` is not finished.

A new version starts as a copy of the previous one
([decision 0004](decisions/0004-complete-profile-files.md)): replace every
captured field from the new capture, and treat everything copied that a
capture cannot show (`http2.hpack`, header slots, `cookies`, `http1` slots)
as unverified until the e2e tests pass against the new traces. `cmd/refresh`
does the copy, sets `capturesFrom` and runs the generators below.

### The generators

Sections derived from captures are regenerated, never hand-edited. Run the
three generators in this order, each with
`-profile ../profiles/data/<name>.json` and each refusing a sample its rule
does not describe:

1. `go run ./cmd/hellogen`: the ClientHello's shape; also `tls.quic`, the
   QUIC ClientHello, from every h3 connection's hello,
   `tls.quic.versionNegotiation`, Firefox's other hello after Version
   Negotiation, from the `h3-vn` captures, and `tls.websocketH1` from the
   `websocket` capture.
2. `go run ./cmd/h3gen`: `quic`, and `http3` but for kindsgen's header lists
   and priorities. It checks every value against every connection and pins
   the few samples that differ in `sampleGaps`.
3. `go run ./cmd/kindsgen`: the profile's `kinds`, `context` and navigation
   `variants`, from the captures listed below.

hellogen and h3gen read the edge captures too (`server.H3EdgeCaptures`:
`h3-blackhole`, `h3-broken`, `h3-vn`, `h3-download`, `h3-early`,
`h3-too-early`), from which h3gen derives `quic.loss` (probes, give-ups,
abandon closes, path degrading), `quic.keepAlive`, `quic.ack`,
`quic.credit`, `quic.keyUpdate`, `quic.versionNegotiation`,
`http3.discovery.fallback` and `http3.earlyData` (which methods go in
0-RTT, where a held request leaves, the retry after 425); kindsgen reads
only `h3-discovery` and `h3-page` (`server.H3ProfileCaptures`). Each
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
  client varies where browsers do not). `harness/reports/` holds
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
  against a container started by hand. Results go to
  `harness/reports/quic-interop-<date>.md`.
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
  `compare-full.md`: a browser `browser-<name>-<major>` against the profile
  `<name>-<major>-macos`, with per-connection randomness the profile models
  (shuffled extension and transport parameter order, GREASE ECH lengths and
  suites, random packet numbers) told apart from differences.
  `-echo=false -sites=false -profiles none` only compares. Results go to
  `harness/reports/realcheck-<date>.md`.
