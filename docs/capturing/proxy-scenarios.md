# Proxy scenarios

Proxy scenarios (`proxy-*`) put the capture proxy (`internal/proxy`:
HTTP CONNECT, absolute-form, SOCKS5, SOCKS4 and 4a, all recorded, or CONNECT
over TLS; see [HTTPS proxy scenarios](proxy-scenarios.md#https-proxy-scenarios)) in front of the server;
a scenario with a plain port gets what goes to that port (tunnels and
absolute-form requests) on the server's plain side, as if the proxy had
connected there, and each connection the server records carries the proxy
connection it came through (`capture.WebSocket.ProxyConn`). Chrome gets
`--proxy-server`, Firefox proxy prefs. With credentials (`proxy-auth`,
`proxy-plain-auth`, `proxy-websocket-auth`) the tool answers the browser's
407 over Chrome's DevTools protocol (browser-level `Fetch`) and Firefox's
WebDriver BiDi (with `remote.prefs.recommended` off, so the remote agent
changes no networking prefs), as a user typing into the login dialog.

The two SOCKS4 scenarios record the browser's one SOCKS4 request as sent
(`kind: socks4`, `request` in hex). `proxy-socks4` gives the browser
`socks4://`: Chrome and Edge get it as `--proxy-server`, Firefox
`network.proxy.socks_version` 4 with `network.proxy.socks_remote_dns`
off; both resolve the host themselves and send its IPv4 address.
`proxy-socks4a` has the browser send the host name (SOCKS4a, `DSTIP`
0.0.0.x): Firefox with `network.proxy.socks_remote_dns` on, and Safari.
Chromium has no SOCKS4a (given `socks4a://`, Chrome 156 sent nothing to
the proxy), so the tool skips `proxy-socks4a` for Chrome, Edge and Brave
(`Scenario.UnsupportedBy`; logged, and the browser counts as failed;
`cmd/refresh` leaves it out of those browsers' runs).

Safari speaks SOCKS4 only through the Mac's SOCKS proxy setting with
`kCFStreamPropertySOCKSVersion` set to `kCFStreamSocketSOCKSVersion4` in
the service's proxies, which neither System Settings nor `networksetup`
writes (its proxy script's `SOCKS` is SOCKS5, `SOCKS4` is ignored;
[decision 0063](../decisions/0063-socks4-proxies.md)); given it, Safari sends
SOCKS4A for both scenarios. That setting sends every URL the Mac loads to
the capture proxy at `127.0.0.1:15355`, which passes other hosts through
unrecorded, so the tool (and `cmd/refresh`, preflight `socks4`) runs only
the SOCKS4 scenarios with it and skips them without it. Set it as an
administrator at the Mac's own screen (over ssh `scutil --prefs` is
denied), `<id>` being the active service's (`scutil --prefs`, then
`list /NetworkServices`), run, and remove it at once after:

```sh
printf 'get /NetworkServices/<id>/Proxies\nd.add SOCKSEnable # 1\nd.add SOCKSProxy 127.0.0.1\nd.add SOCKSPort # 15355\nd.add kCFStreamPropertySOCKSVersion kCFStreamSocketSOCKSVersion4\nset /NetworkServices/<id>/Proxies\ncommit\napply\nquit\n' | scutil --prefs
go run ./cmd/refresh -browser safari -scenarios proxy-socks4:3,proxy-socks4a:3 -capture-only -yes
printf 'get /NetworkServices/<id>/Proxies\nd.remove SOCKSEnable\nd.remove SOCKSProxy\nd.remove SOCKSPort\nd.remove kCFStreamPropertySOCKSVersion\nset /NetworkServices/<id>/Proxies\ncommit\napply\nquit\n' | scutil --prefs
```

The removal leaves the service's proxies as they were only if they held
no SOCKS proxy before: it removes the SOCKS keys, a SOCKS proxy set
earlier included. Note `scutil --proxy` first and set any such proxy
again after.

Safari 26 (`safari-26-macos`) has no SOCKS4 captures of its own: by the
maintainer's decision its `capturesFrom` takes Safari 27's, assumed, not
captured (decision 0063; `assumedFromNewer` in `cmd/profilegen`, the one
newer-version source allowed). A real capture (`-browser safari-mini`,
the commands above run at the second Mac's own screen) replaces it:
remove both entries from its `capturesFrom` and from `assumedFromNewer`,
then derive.

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
and Safari counts as failed); those need the PAC script. Safari's SOCKS4
scenarios need the SOCKS proxy setting instead (above).

For Safari's credential flow (`proxy-auth`, `proxy-plain-auth`,
`proxy-websocket-auth`) macOS itself must hold the credentials: install a
`com.apple.proxy.http.global` device profile (manual proxy at
`127.0.0.1:15354`, `ProxyUsername`/`ProxyPassword`, `PayloadScope:
System`), then type the credentials into the proxy login dialog macOS
raises; saving them in System Settings, `networksetup` or the keychain was
not enough (see [the proxies report](../../internal/harness/reports/proxies-2026-10-24.md)).
Remove the profile afterwards. `cmd/refresh` leaves these scenarios out of
Safari's runs: capture them by hand.

### HTTPS proxy scenarios

`proxy-https`, `proxy-https-h1`, `proxy-https-auth` and
`proxy-https-h1-auth` put the capture proxy behind TLS. The browser
reaches it at `https://proxy.mimicry.test:<port>` (`server.ProxyHost`,
mapped to 127.0.0.1 as the capture hosts are), whose certificate the
capture CA issues; every browser checks it, so Firefox trusts the CA as it
does for pages across hosts (`security.enterprise_roots.enabled`, or on
Linux its profile's database). Chrome and Edge get
`--proxy-server=https://…`; Firefox, whose manual proxy settings take no
proxy reached over TLS, a proxy script answering `HTTPS
proxy.mimicry.test:<port>` for every URL, as a `data:` URL
(`network.proxy.type` 2). The proxy offers `h2` and `http/1.1` in ALPN, or
only `http/1.1` in the `-h1` scenarios. Over HTTP/1.1 it answers CONNECT as
the HTTP proxy does; over HTTP/2 each CONNECT is a stream (Go's HTTP/2
server), refused with 403 for hosts outside `*.test` and challenged with a
407 response on the stream with credentials. The `-auth` scenarios load
the resume page (a new connection per request), so over HTTP/1.1 every
tunnel takes a proxy connection of its own and shows whether TLS to the
proxy resumes.

Each writes `<name>.<scenario>.proxytls.json.gz` beside the proxy trace and
the page's trace: per sample, every connection to the proxy
(`capture.ProxyTLS`), those that carried only the browser's own traffic
(refused, not in the proxy trace) and those that never finished their
handshake included. Each has its ClientHello's records as sent (a
`.clienthello` line), parsed as a page connection's (server name, the
protocol the proxy picked, the session offered and taken), and over
HTTP/2 every frame the browser sent, each header block's HPACK
instructions and its first SETTINGS. In the proxy trace a CONNECT over
HTTP/2 has its stream and its decoded header fields, pseudo-headers
included, as lines, and every accepted tunnel the first ClientHello sent
through it (`clientHello`), to compare with the direct one.

The proxy's HTTP/2 server sends its connection preface only after the
browser's has arrived (RFC 9113 allows either order). Firefox 159 lost a
server preface that arrived while the connection waited in its pool, its
TLS handshake done but HTTP/2 not yet started, then read the server's
WINDOW_UPDATE as the first frame and ended the connection with
PROTOCOL_ERROR ("First Frame Type Must Be Settings" in its HTTP log),
failing the page about one sample in two when its tunnel was on that
connection.

Through the HTTP/2 proxy Firefox ends a tunnelled connection the server
sent GOAWAY on by resetting the tunnel's stream (CANCEL), with no GOAWAY
reply inside it, so `hellogen` leaves `proxy-https-auth` (and any scenario
whose proxy offers `h2`) out of `http2.goawayReply`
([gaps](../../profiles/evidence/gaps.md)).

Safari and the browsers on an iPhone cannot take these scenarios
(`Scenario.UnsupportedBy`, `Scenario.UnsupportedOnIOS`): macOS offers no
proxy reached over TLS. Its secure web proxy setting is a plain HTTP proxy
for https:// URLs, and with the system's proxy script answering `HTTPS
proxy.mimicry.test:<port>` (or `HTTPS 127.0.0.1:<port>`) for `*.test`,
Safari 27 on macOS 27.0.1 connected to the site directly and nothing
reached the proxy (2026-11-09). The iPhone's Wi-Fi proxy goes through the
same system networking; it was not tried there. Not tried either: a
managed Network Relay (`com.apple.relay.managed` configuration profile),
which is an MDM relay, not a proxy setting.
