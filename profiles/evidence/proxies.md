# Proxies

The `proxy` section ([decision 0014](../../docs/decisions/0014-proxies.md)) comes from the `proxy-*` captures.

- Reports:
  - [proxies report](../../internal/harness/reports/proxies-2026-10-24.md).
  - WebSockets: [proxy WebSocket report](../../internal/harness/reports/proxy-websocket-2026-10-26.md).
- Tests that check every claim:
  - `TestProxyMatchesBrowser`: mimicry through the capture proxy, against every browser's capture.
  - `TestWebSocketsMatchBrowser`: its proxy scenarios.
  - `TestUnmeasuredProxyIsAnError`.
- `Config.BrowserProxyAuth`: by default mimicry sends the `connectAuth` and `forwardAuth` placements from the first request.
  - Decision 0039: checked in the same tests' upfront cases.
- Safari 26's credential rows are **C**.
  - Its `proxy-auth`, `proxy-plain-auth` and `proxy-websocket-auth` captures were never taken (`missingCaptures` in the test).
- Safari 27's `proxy-websocket-auth` was taken on 2026-10-26.
  - Device profile; macOS answered with remembered credentials.

## HTTP proxy claims

| Claim (`proxy.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `connect` [1] | W [2] | W [3] | W [4] | W: as Safari 27 |
| `connectAuth` [5] | W [6] | W: same | W [7] | **C** from Safari 27 |
| `connectCredentials` | W: `after_challenge` [8] | W: `after_challenge` | W: `on_challenge` [9] | **C** from Safari 27 |
| `forwardCredentials` | W: `after_challenge` | W: `after_challenge` | W: `always` [10] | **C** from Safari 27 |
| `forwardConnection` [11] | W: Proxy-Connection | W: none (Connection) | W: none | W: none |
| `forwardAuth` | W: after connection, content-length | W [12] | W: after accept-encoding | **C** from Safari 27 |
| Tunnelled connection equals the direct one | W | W | W | W |

Notes:
1. CONNECT header names, order and values.
2. Host, Proxy-Connection, User-Agent.
3. User-Agent, Proxy-Connection, Connection, Host.
4. Host, Proxy-Connection, Connection.
5. CONNECT with credentials.
6. `connect` plus Proxy-Authorization last, on the retry and unasked alike.
7. Host, Connection, Proxy-Authorization, Proxy-Connection.
8. Preconnects aside (net log).
9. On every tunnel.
10. Fresh Safari. In `proxy-websocket-auth` sample 0, the first forwarded request was challenged. See [the known gap](#proxy-authorization-inside-the-tunnel-known-gap).
11. Plain HTTP to the proxy.
12. Before connection; last on the challenged retry.

## SOCKS claims

| Claim (`proxy.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `socks5` | W: `05 01 00`, host name | W: same | W: same | W: same |
| `socks4` [1] | W [2] | W [3] | W: never resolves [4] | **C** from Safari 27 [5] |
| `socks4.hostname`, `hostnameDstip` [6] | false [7] | W: true, `0.0.0.1` [8] | W: true, `0.0.0.16` [9] | **C** from Safari 27 [10] |

Notes:
1. `socks4` evidence:
   - Source: `proxy-socks4`, 3 samples each, 2026-11-08 and 2026-11-10; [decision 0063](../../docs/decisions/0063-socks4-proxies.md); hellogen.
   - Claim: CONNECT, the host resolved by the browser, its IPv4 address, empty USERID: `04 01 <port> <IPv4> 00`.
2. Chromium `socks4`:
   - Measured in Chrome 156 (macOS, Windows), Chrome 157 (macOS, Linux), Edge 157 (macOS, Windows) and Brave 157 (macOS, Windows, Linux). Every sample is the same.
   - Chrome 155: none. Not captured, the maintainer's decision. Refused with `ErrNotMeasured`.
3. Firefox `socks4`:
   - Measured in Firefox 159 (macOS, Windows, Linux; `socks_remote_dns` off), the same bytes.
   - Firefox 158: none (as Chrome 155).
4. Safari 27 `socks4` (`hostnameOnly`):
   - Through macOS's SOCKS proxy setting with `kCFStreamPropertySOCKSVersion` SOCKS4 (a hidden key no UI writes), Safari 27.0.1 sent SOCKS4A (next row) in every `proxy-socks4` sample.
   - `socks4://` is refused.
   - Its proxy script's `SOCKS` gives SOCKS5, and `SOCKS4` is ignored (ad-hoc, Safari 26.2 and 27.0.1).
5. Safari 26 `socks4`:
   - Assumed, not captured: the maintainer's decision, 2026-11-10.
   - `capturesFrom` safari-27-macos, the one newer-version source (decision 0063).
   - Its proxy script's `SOCKS` gave SOCKS5 and `SOCKS4` was ignored, as Safari 27's (ad-hoc, Safari 26.2).
6. SOCKS4A: DSTIP `0.0.0.x`, empty USERID, then the host name and NUL. Scenario `proxy-socks4a`.
7. Chrome, Edge and Brave: no SOCKS4A.
   - Evidence: an ad-hoc run with Chrome 156, 2026-11-08, no committed capture. `--proxy-server=socks4a://` sent nothing through the proxy.
   - The capture tool refuses the scenario for Chromium.
8. Firefox 159 (macOS, Windows, Linux; `socks_remote_dns` on).
9. Safari 27.0.1, both scenarios, every sample.
10. Assumed, not captured.

## Session resumption through a tunnel

**`connectNoResume`**: no session is offered through a CONNECT tunnel, an HTTP or HTTPS proxy's. SOCKS5 and direct connections resume.

- Checked by:
  - `TestWebSocketsMatchBrowser`: the socket connections' hellos. Setting Firefox to false fails it.
  - `TestHTTPSProxyMatchesBrowser` (`auth`, `h1-auth`): the tunnels' hellos.
  - hellogen: refuses https captures that disagree.
- Chrome 155: W, false.
  - Resumes through both.
  - Through the https proxy too (Chrome 156, Edge 157).
- Firefox 158: W, true.
  - `proxy-auth`: none of 12 connections.
  - `proxy-websocket`: neither socket connection, 3 samples.
  - `proxy-websocket-socks5`: both resume.
  - Firefox 159 through the https proxy: none of 12 tunnels, every sample.
- Safari 27: n/a (never resumes).
- Safari 26: n/a.

## WebSockets through a proxy

| Claim | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `websocket.plain` [1] | W: `tunnel` [2] | W: `tunnel` | W: `forward` [3] | W: `forward` |
| wss:// socket route [4] | W [5] | W | W | W |
| `websocket.credentials` [6] | W [7] | W: same | W [8] | **C** from Safari 27 [9] |
| ws:// through SOCKS5 [10] | not captured [11] | same | same | same |

Notes:
1. A ws:// socket through an HTTP proxy. Checked by `TestWebSocketsMatchBrowser`; setting Safari 27 to `tunnel` fails it.
2. CONNECT host:port, `connect` lines.
3. `GET ws://host:port/path`, its direct header lines, a proxy connection of its own.
4. A wss:// socket through a proxy:
   - Where the server allows it: an extended CONNECT stream on the page's HTTP/2 connection, in its tunnel.
   - Else: a tunnel of its own (`connect` lines, SOCKS5 bytes) with the `websocketH1` hello.
   - Checked by `TestWebSocketsMatchBrowser` (`checkProxyRoute`, transport, hello).
5. Scenarios `proxy-websocket` and `-socks5`.
6. `websocket.credentials`:
   - Socket tunnels give credentials as `connectCredentials` says.
   - A ws:// handshake sent whole carries them unasked (needs `forwardCredentials: always`), placed by `forwardAuth`.
   - Checked by `TestWebSocketsMatchBrowser`:
     - Setting Safari 27's `connectCredentials` to `after_challenge` fails `px-h1` and `px-h1-again`.
     - Its `forwardAuth` after `accept-language` fails `px-plain`.
     - Unset, every socket is refused.
   - Also checked by `TestUnmeasuredProxyIsAnError`.
7. Unasked after the page's challenge (`proxy-websocket-auth`).
8. Safari 27:
   - Every socket tunnel is challenged, then again on its connection with `connectAuth`.
   - `px-h2` goes in the page's challenged tunnel.
   - ws:// is sent whole with Proxy-Authorization unasked after Accept-Encoding (3 samples).
9. `proxy-websocket-auth` was never taken. Its `proxy-websocket` captures match Safari 27's on every other claim.
10. Checked by `TestUnmeasuredProxyIsAnError`.
11. Refused (`ErrNotMeasured`).

## Proxy-Authorization inside the tunnel (known gap)

Proxy-Authorization inside the tunnel, to the server (**known gap**).

- Chrome 155: W, never.
- Firefox 158: W, never.
- Safari 27: W, not modelled.
  - Sent on every tunnelled request once Safari has answered a challenge to a forwarded request.
  - `proxy-websocket-auth` samples 1 and 2: from their first request.
  - Sample 0: after its forwarded page's 407. Sample 0's first forwarded request went without credentials.
  - Never in `proxy-auth`, which forwards nothing.
  - mimicry never sends it.
- Safari 26: not captured.

## HTTPS proxies

**`https`**: hellogen claim ([0065](../../docs/decisions/0065-https-proxies.md)).

- Source: `proxy-https`, `-h1`, `-auth`, `-h1-auth`, 3 samples each.
  - Chrome 156 macOS/Windows, Chrome 157 Linux, Edge 157 macOS/Windows, Firefox 159 macOS/Windows/Linux.
- Claim:
  - The hello to the proxy is the profile's (resumed as it resumes), to the proxy's name.
  - Over HTTP/2: the profile's SETTINGS and WINDOW_UPDATE; `connect`/`connectAuth` after `:method`, `:authority`; `weight`, `reset`.
  - Over HTTP/1.1: `proxy.connect`'s lines.
  - `resume` (TLS to the proxy).
- Checked by:
  - `TestHTTPSProxyMatchesBrowser`: every captured profile, the four scenarios.
  - `TestUnmeasuredProxyIsAnError`.
- Chrome 156/157, Edge 157: W.
  - `user-agent` (+ `proxy-authorization`).
  - Weight 146 exclusive and chained.
  - CANCEL.
  - Resume: 12 of 14 later tunnel connections on macOS, all on Windows and Linux.
- Firefox 159: W.
  - `user-agent` (+ `proxy-authorization`).
  - Weight 21 non-exclusive on 0, its stream top-up.
  - CANCEL.
  - No resume: 1–2 of 11 later tunnel connections per sample (Not covered).
- Safari 27: not capturable (macOS offers no proxy over TLS). Refused (`ErrNotMeasured`).
- Safari 26: same.

### Unsupported through an HTTPS proxy

Plain HTTP, `ws://`, and a client certificate through an `https://` proxy.

- Checked by `TestUnmeasuredProxyIsAnError` (plain HTTP, `ws://`).
- Chrome 155: not captured.
  - Plain HTTP and `ws://` are refused (`ErrNotMeasured`).
  - No certificate is offered to the proxy.
- Firefox 158: same.
- Safari 27: n/a.
- Safari 26: n/a.
