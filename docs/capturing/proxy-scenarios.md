# Proxy scenarios

Proxy scenarios (`proxy-*`) put the capture proxy (`internal/proxy`) in front of the server.

- The proxy records HTTP CONNECT, absolute-form, SOCKS5, and SOCKS4 and 4a.
- It also takes CONNECT over TLS (see [HTTPS proxy scenarios](proxy-scenarios.md#https-proxy-scenarios)).
- A scenario with a plain port gets what goes to that port (tunnels and absolute-form requests) on the server's plain side, as if the proxy had connected there.
- Each connection the server records carries the proxy connection it came through (`capture.WebSocket.ProxyConn`).

How each browser gets the proxy:

- **Chrome:** `--proxy-server`.
- **Firefox:** proxy prefs.
- **Safari:** the system's proxy settings (see [Safari's proxy setup](#safaris-proxy-setup)).

## Proxy credentials

The credential scenarios are `proxy-auth`, `proxy-plain-auth` and `proxy-websocket-auth`.
In them the tool answers the browser's 407 as a user typing into the login dialog:

- **Chrome:** over Chrome's DevTools protocol (browser-level `Fetch`).
- **Firefox:** over WebDriver BiDi, with `remote.prefs.recommended` off, so the remote agent changes no networking prefs.
- **Safari:** macOS itself must hold the credentials (see [Safari's credential flow](#safaris-credential-flow)).

## SOCKS4 scenarios

The two SOCKS4 scenarios record the browser's one SOCKS4 request as sent (`kind: socks4`, `request` in hex).

- **`proxy-socks4`:** gives the browser `socks4://`.
  - Chrome and Edge get it as `--proxy-server`.
  - Firefox gets `network.proxy.socks_version` 4 with `network.proxy.socks_remote_dns` off.
  - Both resolve the host themselves and send its IPv4 address.
- **`proxy-socks4a`:** has the browser send the host name (SOCKS4a, `DSTIP` 0.0.0.x).
  - Firefox does this with `network.proxy.socks_remote_dns` on.
  - Safari does it too.

Chromium has no SOCKS4a.
Given `socks4a://`, Chrome 156 sent nothing to the proxy.
So the tool skips `proxy-socks4a` for Chrome, Edge and Brave (`Scenario.UnsupportedBy`):

- The skip is logged, and the browser counts as failed.
- `cmd/refresh` leaves it out of those browsers' runs.

### Safari and SOCKS4

Safari speaks SOCKS4 only through the Mac's SOCKS proxy setting.
The setting needs `kCFStreamPropertySOCKSVersion` set to `kCFStreamSocketSOCKSVersion4` in the service's proxies.

- Neither System Settings nor `networksetup` writes that key.
- In the system's proxy script, `SOCKS` is SOCKS5 and `SOCKS4` is ignored ([decision 0063](../decisions/0063-socks4-proxies.md)).
- Given the setting, Safari sends SOCKS4A for both scenarios.

The setting sends every URL the Mac loads to the capture proxy at `127.0.0.1:15355`.

- The proxy passes other hosts through unrecorded.
- So with the setting, the tool (and `cmd/refresh`, preflight `socks4`) runs only the SOCKS4 scenarios.
- Without it, they skip the SOCKS4 scenarios.

To capture:

1. Find `<id>`, the active service's: `scutil --prefs`, then `list /NetworkServices`.
2. Set the setting as an administrator at the Mac's own screen. Over ssh, `scutil --prefs` is denied.
3. Run.
4. Remove the setting at once after.

```sh
printf 'get /NetworkServices/<id>/Proxies\nd.add SOCKSEnable # 1\nd.add SOCKSProxy 127.0.0.1\nd.add SOCKSPort # 15355\nd.add kCFStreamPropertySOCKSVersion kCFStreamSocketSOCKSVersion4\nset /NetworkServices/<id>/Proxies\ncommit\napply\nquit\n' | scutil --prefs
go run ./cmd/refresh -browser safari -scenarios proxy-socks4:3,proxy-socks4a:3 -capture-only -yes
printf 'get /NetworkServices/<id>/Proxies\nd.remove SOCKSEnable\nd.remove SOCKSProxy\nd.remove SOCKSPort\nd.remove kCFStreamPropertySOCKSVersion\nset /NetworkServices/<id>/Proxies\ncommit\napply\nquit\n' | scutil --prefs
```

The removal leaves the service's proxies as they were only if they held no SOCKS proxy before.

- It removes the SOCKS keys, a SOCKS proxy set earlier included.
- Note `scutil --proxy` first, and set any such proxy again after.

### Safari 26 and SOCKS4

Safari 26 (`safari-26-macos`) has no SOCKS4 captures of its own.

- By the maintainer's decision, its `capturesFrom` takes Safari 27's: assumed, not captured (decision 0063).
- `assumedFromNewer` in `cmd/profilegen` lists it. That is the one newer-version source allowed.

A real capture replaces it:

1. Capture with `-browser safari-mini`, running the commands above at the second Mac's own screen.
2. Remove both entries from its `capturesFrom` and from `assumedFromNewer`.
3. Derive.

## Safari's proxy setup

Safari takes its proxy from the system.
Point the active network service at the tool's PAC script once, and turn it off after.

- The script proxies only `*.test`.
- The first run after setting it may time out while Safari fetches the script. Run again.
- The commands below use `sudo`. An administrator account may not need it.

```sh
sudo networksetup -setautoproxyurl <service> http://127.0.0.1:15356/mimicry.pac
sudo networksetup -setautoproxystate <service> off
```

### Manual proxies

The tool also accepts manual web and secure web proxies at `127.0.0.1:15354` instead of the script.

- The whole Mac uses the proxy, so the tool passes every non-`*.test` request through to the network unrecorded.
- The Mac has no working proxy between samples or after the run, until the proxies are turned off.
- Manual proxies send everything to the HTTP proxy. So with them, the tool skips Safari's SOCKS5 scenarios (logged, and Safari counts as failed).
- Safari's SOCKS5 scenarios need the PAC script.
- Safari's SOCKS4 scenarios need the SOCKS proxy setting instead (see [Safari and SOCKS4](#safari-and-socks4)).

### Safari's credential flow

For `proxy-auth`, `proxy-plain-auth` and `proxy-websocket-auth`, macOS itself must hold the credentials.

1. Install a `com.apple.proxy.http.global` device profile: manual proxy at `127.0.0.1:15354`, `ProxyUsername`/`ProxyPassword`, `PayloadScope: System`.
2. Type the credentials into the proxy login dialog macOS raises.
3. Remove the profile afterwards.

Saving the credentials in System Settings, `networksetup` or the keychain was not enough (see [the proxies report](../../internal/harness/reports/proxies-2026-10-24.md)).

`cmd/refresh` leaves these scenarios out of Safari's runs: capture them by hand.

## HTTPS proxy scenarios

`proxy-https`, `proxy-https-h1`, `proxy-https-auth` and `proxy-https-h1-auth` put the capture proxy behind TLS.

### Reaching the proxy

The browser reaches the proxy at `https://proxy.mimicry.test:<port>` (`server.ProxyHost`).

- The name maps to 127.0.0.1, as the capture hosts do.
- The capture CA issues its certificate, and every browser checks it.
- So Firefox trusts the CA as it does for pages across hosts (`security.enterprise_roots.enabled`, or on Linux its profile's database).

How each browser gets the proxy:

- **Chrome and Edge:** `--proxy-server=https://…`.
- **Firefox:** its manual proxy settings take no proxy reached over TLS. It gets a proxy script answering `HTTPS proxy.mimicry.test:<port>` for every URL, as a `data:` URL (`network.proxy.type` 2).

### What the proxy speaks

- The proxy offers `h2` and `http/1.1` in ALPN.
- In the `-h1` scenarios it offers only `http/1.1`.
- Over HTTP/1.1 it answers CONNECT as the HTTP proxy does.
- Over HTTP/2 each CONNECT is a stream (Go's HTTP/2 server):
  - Hosts outside `*.test` are refused with 403.
  - With credentials, the stream is challenged with a 407 response.

The `-auth` scenarios load the resume page (a new connection per request).
So over HTTP/1.1, every tunnel takes a proxy connection of its own.
That shows whether TLS to the proxy resumes.

### Proxy TLS trace

Each scenario writes `<name>.<scenario>.proxytls.json.gz` beside the proxy trace and the page's trace.
Per sample, it holds every connection to the proxy (`capture.ProxyTLS`), including:

- those that carried only the browser's own traffic (refused, not in the proxy trace)
- those that never finished their handshake

Each connection has:

- its ClientHello's records as sent (a `.clienthello` line)
- those records parsed as a page connection's: server name, the protocol the proxy picked, the session offered and taken
- over HTTP/2: every frame the browser sent, each header block's HPACK instructions, and its first SETTINGS

In the proxy trace:

- A CONNECT over HTTP/2 has its stream and its decoded header fields, pseudo-headers included, as lines.
- Every accepted tunnel has the first ClientHello sent through it (`clientHello`), to compare with the direct one.

### Firefox and the HTTP/2 proxy

The proxy's HTTP/2 server sends its connection preface only after the browser's has arrived.
RFC 9113 allows either order.

Firefox 159 lost a server preface that arrived while the connection waited in its pool.

- At that point its TLS handshake was done, but HTTP/2 had not yet started.
- It then read the server's WINDOW_UPDATE as the first frame.
- It ended the connection with PROTOCOL_ERROR ("First Frame Type Must Be Settings" in its HTTP log).
- This failed the page about one sample in two when its tunnel was on that connection.

Through the HTTP/2 proxy, Firefox ends a tunnelled connection the server sent GOAWAY on by resetting the tunnel's stream (CANCEL).
It sends no GOAWAY reply inside the tunnel.
So `hellogen` leaves `proxy-https-auth` (and any scenario whose proxy offers `h2`) out of `http2.goawayReply` ([gaps](../../profiles/evidence/gaps.md)).

### Safari and iPhone

Safari and the browsers on an iPhone cannot take these scenarios (`Scenario.UnsupportedBy`, `Scenario.UnsupportedOnIOS`).
macOS offers no proxy reached over TLS:

- Its secure web proxy setting is a plain HTTP proxy for https:// URLs.
- The system's proxy script answered `HTTPS proxy.mimicry.test:<port>` (or `HTTPS 127.0.0.1:<port>`) for `*.test`. With that, Safari 27 on macOS 27.0.1 connected to the site directly, and nothing reached the proxy (2026-11-09).

Not tried:

- The iPhone's Wi-Fi proxy. It goes through the same system networking.
- A managed Network Relay (`com.apple.relay.managed` configuration profile). That is an MDM relay, not a proxy setting.
