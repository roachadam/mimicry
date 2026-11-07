# Refresh of edge-157-windows, 2026-11-06

edge 157.0.4341.42 on the Windows PC, full set: new profile, copied from edge-157-macos, the first edge profile on windows (nothing inherited). Made by `go run ./cmd/refresh` (staging `.refresh/20261106-170220`). Captured on 2026-11-06 on the Windows PC after updating Edge from 156 with Microsoft's MSI ([0059](../../../docs/decisions/0059-edge-profiles.md)). The PC's Edge policies (`SOFTWARE\Policies\Microsoft\Edge`, `ConfigureDoNotTrack` among them) were exported and lifted for the capture and for realcheck, and imported again after: a first run with them in place sent `dnt: 1` and was discarded, and a second lost the PC's network and was restarted. `h3gen` first refused the captures (tokenless reconnections and one one-byte second Initial, as on macOS; a first flight split at 2.1 ms by the 2 ms flight boundary, now pinnable as `probes`; two credit raises), and the e2e tests failed on gap tables keyed by profile and one early path move (`pathMoveGaps`); after those fixes `-resume -realcheck=false` derived, installed and tested. realcheck is from the first resume, with the same profile.

## Captures

Every capture names edge-157-windows with one user agent, and the browser's version was 157.0.4341.42 before and after the run.

User agent: `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36 Edg/157.0.0.0`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 17s | 57s |
| set-cookie | 3 | 17s | 57s |
| large-settings | 3 | 17s | 57s |
| small-limits | 3 | 17s | 57s |
| huge-table | 3 | 17s | 57s |
| explicit-table | 3 | 17s | 57s |
| slow-idle | 3 | 1m34s | 2m3s |
| http1 | 3 | 17s | 57s |
| http1-upload | 3 | 17s | 57s |
| h2-upload | 3 | 17s | 57s |
| proxy-connect | 3 | 17s | 57s |
| proxy-socks5 | 3 | 17s | 57s |
| proxy-plain | 3 | 17s | 57s |
| proxy-auth | 3 | 26s | 1m33s |
| proxy-plain-auth | 3 | 18s | 57s |
| kinds | 3 | 21s | 1m33s |
| kinds-http1 | 3 | 21s | 1m33s |
| kinds-plain | 3 | 21s | 1m33s |
| cors | 3 | 35s | 2m3s |
| cookies | 3 | 17s | 2m33s |
| resume | 3 | 25s | 1m33s |
| resume-tls12 | 3 | 25s | 1m33s |
| cookies-edge | 3 | 17s | 2m3s |
| cors-edge | 3 | 17s | 1m33s |
| cors-edge-cookies | 3 | 17s | 1m33s |
| connections | 3 | 27s | 1m18s |
| headers-order | 3 | 17s | 1m48s |
| cookies-edge2 | 3 | 17s | 2m3s |
| redirects2 | 3 | 21s | 2m48s |
| redirects2-cookies | 3 | 21s | 2m48s |
| nav-redirect-so | 3 | 17s | 1m3s |
| nav-redirect-ss | 3 | 17s | 1m3s |
| nav-redirect-cs | 3 | 17s | 1m3s |
| nav-redirect-http | 3 | 17s | 1m3s |
| nav-redirect-page | 3 | 17s | 1m33s |
| nav-redirect-form | 3 | 21s | 3m33s |
| nav-redirect-click | 3 | 33s | 6m33s |
| nav-redirect-so-cookies | 3 | 17s | 1m3s |
| nav-redirect-ss-cookies | 3 | 17s | 1m3s |
| nav-redirect-cs-cookies | 3 | 17s | 1m3s |
| nav-redirect-http-cookies | 3 | 17s | 1m3s |
| nav-redirect-page-cookies | 3 | 17s | 1m33s |
| nav-redirect-form-cookies | 3 | 21s | 3m33s |
| nav-redirect-click-cookies | 3 | 33s | 6m33s |
| websocket | 3 | 1m48s | 6m33s |
| websocket-h2 | 3 | 23s | 6m33s |
| proxy-websocket | 3 | 34s | 3m33s |
| proxy-websocket-auth | 3 | 36s | 3m33s |
| proxy-websocket-socks5 | 3 | 28s | 3m33s |
| h3-discovery | 3 | 2m47s | 3m48s |
| h3-page | 3 | 41s | 2m3s |
| h3-blackhole | 3 | 2m47s | 3m33s |
| hrr | 3 | 21s | 1m18s |
| urls | 3 | 17s | 5m3s |
| replay-github.com | 3 | 53s | 55s |
| replay-nike.com | 3 | 1m25s | 1m26s |
| replay-nytimes.com | 3 | 1m33s | 1m33s |
| replay-ticketmaster.com | 3 | 1m30s | 1m31s |
| replay-walmart.com | 3 | 1m27s | 1m28s |
| replay-zillow.com | 3 | 1m12s | 1m14s |
| h3-discovery.local-roots | 3 | 2m47s | 3m48s |
| h3-page.local-roots | 3 | 41s | 2m3s |
| h3-broken.local-roots | 3 | 3m53s | 4m48s |
| h3-v2.local-roots | 3 | 2m26s | 3m3s |
| h3-vn.local-roots | 3 | 1m20s | 2m3s |
| h3-download.local-roots | 3 | 55s | 2m48s |
| h3-early.local-roots | 3 | 2m39s | 4m3s |
| h3-too-early.local-roots | 3 | 2m39s | 4m3s |
| h3-streams-blocked.local-roots | 3 | 41s | 1m33s |
| bodiless.local-roots | 3 | 31s | 1m33s |
| client-hints.local-roots | 3 | 1m11s | 3m33s |
| total | | 54m46s | 2h36m50s |

Wall time of the capture run (launches, copies and checks included): 54m53s.

## Inherited scenarios

None: the profile is checked against its own captures only.

## Changes from edge-157-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36 Edg/157.0.0.0` → `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36 Edg/157.0.0.0`
- sec-ch-ua: `"Not-A.Brand";v="99", "Microsoft Edge";v="157", "Chromium";v="157"` → `"Not-A.Brand";v="99", "Microsoft Edge";v="157", "Chromium";v="157"`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `name`: `"edge-157-macos"` → `"edge-157-windows"`
- `platform`: `"macos"` → `"windows"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

- `headers[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `http1.headers[5].value`: `"\"macOS\""` → `"\"Windows\""`

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic, tls.helloRetry, tls.x25519Reuse, http2.openingRecords, http2.goawayReply, http1.uploadRecords; h3gen: quic, http3; kindsgen: kinds, context, variants, emptyBody, clientHints, urls, http3 headers and priorities)

- `clientHints.machine.arch`: `"arm"` → `"x86"`
- `clientHints.machine.deviceMemory`: `16` → `32`
- `clientHints.machine.dpr`: `2` → `1`
- `clientHints.machine.platformVersion`: `"26.2.0"` → `"10.0.0"`
- `clientHints.machine.reducedMotion`: `"reduce"` → `"no-preference"`
- `clientHints.machine.reducedTransparency`: `"reduce"` → `"no-preference"`
- `clientHints.machine.viewportHeight`: `1198` → `1288`
- `clientHints.machine.viewportWidth`: `1192` → `1241`
- `http3.discovery.fallback.tcpDelayMs.max`: `315` → `313`
- `http3.discovery.fallback.tcpDelayMs.min`: `313` → `303`
- `kinds.async_script.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.async_script.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.async_script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.beacon.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.beacon.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.defer_script.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.defer_script.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.defer_script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[0].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[0].http1[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[1].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[1].http1[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[2].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[3].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[4].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[5].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[6].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[7].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[8].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[9].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[10].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[11].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[12].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[13].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.fetch.variants[14].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.font.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.font.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.frame.headers[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.frame.http1[4].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.variants[0].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.variants[0].http1[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.variants[1].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.variants[1].http1[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.variants[2].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.variants[3].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.variants[4].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.image.variants[5].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.variants[0].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.variants[0].http1[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.variants[1].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.variants[1].http1[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.variants[2].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.variants[3].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.variants[4].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_image.variants[5].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_script.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_script.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.injected_script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.preload_font.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.preload_font.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.preload_script.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.preload_script.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.preload_script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.script.headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.script.http1[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.style.headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.style.http1[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.xhr.headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.xhr.http1[2].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.xhr.variants[0].headers[0].value`: `"\"macOS\""` → `"\"Windows\""`
- `kinds.xhr.variants[4].headers[1].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[0].headers[4].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[0].http1[6].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[1].headers[4].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[2].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[3].headers[13].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[4].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[5].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[6].headers[13].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[7].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[8].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[9].headers[13].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[10].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[11].headers[13].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[12].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[13].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[14].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[15].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[16].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[17].headers[13].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[18].headers[4].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[19].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[20].headers[13].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[21].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[22].headers[13].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[23].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[24].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[25].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[26].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[27].headers[13].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[28].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[29].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[30].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[31].headers[9].value`: `"\"macOS\""` → `"\"Windows\""`

### Hand-written, copied from the previous profile

No change.

## Deprecation

No edge profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests (`MIMICRY_PROFILES=edge-157-windows go test -count=1 ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/ (harness), go test -count=1 ./profiles/ ./cmd/profilegen/ (library)`, 4m32s): passed. Output: `.refresh/20261106-170220/edge-windows/tests.log`.

realcheck (`realcheck -browsers edge -profiles edge-157-windows (on the Windows PC)`, 1m26s): ran. Output: `.refresh/20261106-170220/edge-windows/realcheck/compare.md`.

```
# Echo endpoints: browser vs mimicry

## browser-edge-157-windows vs edge-157-windows

| Endpoint | Browser proto (tries) | mimicry proto (tries) | Fields | Same | Random by design | Differ | One side only |
|---|---|---|---|---|---|---|---|
| peet | h2 (1) | h2 (1) | 61 | 58 | 3 | 0 | 0 |
| browserleaks-tls |  (1) |  (1) | 11 | 7 | 4 | 0 | 0 |
| browserleaks-h2 |  (1) |  (1) | 41 | 23 | 0 | 0 | 18 |
| impersonate-h2 | h2 (1) | h2 (1) | 52 | 49 | 3 | 0 | 0 |
| impersonate-h3 | h3 (2) | h3 (2) | 46 | 40 | 6 | 0 | 0 |

| Field | Browser | mimicry | |
|---|---|---|---|
| JA4 | `t13d1516h2_8daaf6152771_806a8c22fdea` | `t13d1516h2_8daaf6152771_806a8c22fdea` | ✓ |
| JA3 hash, extensions sorted (JA3N) | `8e19337e7524d2573be54efb2b0784c9` | `8e19337e7524d2573be54efb2b0784c9` | ✓ |
| JA3 hash, as sent | `92c0241cb57954a5a955cafd4845341b` | `f678a557748f3833b7c1889af869a78a` | ✓ random: extension order, shuffled per connection |
| peetprint hash | `8f568b2a409b82d0ec24cb84bf3bc3fc` | `8f568b2a409b82d0ec24cb84bf3bc3fc` | ✓ |
| Akamai HTTP/2 | `1:65536;2:0;4:6291456;6:262144\|15663105\|0\|m,a,s,p` | `1:65536;2:0;4:6291456;6:262144\|15663105\|0\|m,a,s,p` | ✓ |
| HTTP/2 header order | `:method,:authority,:scheme,:path,sec-ch-ua,sec-ch-ua-mobile,sec-ch-ua-platform,upgrade-ins…` | `:method,:authority,:scheme,:path,sec-ch-ua,sec-ch-ua-mobile,sec-ch-ua-platform,upgrade-ins…` | ✓ |
| HTTP/2 HEADERS flags and priority | `{"flags":["EndStream (0x1)","EndHeaders (0x4)","Priority (0x20)"],"priority":{"depends_on"…` | `{"flags":["EndStream (0x1)","EndHeaders (0x4)","Priority (0x20)"],"priority":{"depends_on"…` | ✓ |
| impersonate.pro /api/http3 protocol | `http3` | `http3` | ✓ |
| QUIC JA3N | `36b3da67265214a919829c5e959f7425` | `36b3da67265214a919829c5e959f7425` | ✓ |
| HTTP/3 fingerprint (perk, normalized) | `1:65536;6:262144;7:100;51:1;GREASE\|m,a,s,p\|1:30000;3:1472;4:15728640;5:6291456;6:6291456;7…` | `1:65536;6:262144;7:100;51:1;GREASE\|m,a,s,p\|1:30000;3:1472;4:15728640;5:6291456;6:6291456;7…` | ✓ |
| HTTP/3 header order | `:method,:authority,:scheme,:path,sec-ch-ua,sec-ch-ua-mobile,sec-ch-ua-platform,upgrade-ins…` | `:method,:authority,:scheme,:path,sec-ch-ua,sec-ch-ua-mobile,sec-ch-ua-platform,upgrade-ins…` | ✓ |

Random by design (the browser's value among the profile's choices):

- extension order, shuffled per connection: peet tls.ja3, peet tls.ja3_hash, browserleaks-tls ja4_o, browserleaks-tls ja4_ro, browserleaks-tls ja3_hash, browserleaks-tls ja3_text, impersonate-h2 tls.ja3.text, impersonate-h2 tls.ja3.hash, impersonate-h3 tls.ja3.text, impersonate-h3 tls.ja3.hash
- GREASE ECH payload length, one of 144/176/208/240: peet ext ECH payload length, impersonate-h2 ext ECH payload length, impersonate-h3 ext ECH payload length
- QUIC transport parameter order, shuffled per connection: impersonate-h3 http3.perk_hash, impersonate-h3 http3.perk_text, impersonate-h3 ext quic_transport_parameters order

Differences:

- browserleaks-h2 `h2 #2 header order` (browser only): browser `:method,:authority,:scheme,:path,sec-ch-ua-platform,user-agent,sec-ch-ua,sec-ch-ua-mobile,accept,sec-fetch-site,sec-fetch-mode,sec-fetch-dest,referer,accept-encoding,accept-language,priority`, mimicry –
- browserleaks-h2 `h2 #2 header :method` (browser only): browser `GET`, mimicry –
- browserleaks-h2 `h2 #2 header :authority` (browser only): browser `tls.browserleaks.com`, mimicry –
- browserleaks-h2 `h2 #2 header :scheme` (browser only): browser `https`, mimicry –
- browserleaks-h2 `h2 #2 header :path` (browser only): browser `/favicon.ico`, mimicry –
- browserleaks-h2 `h2 #2 header sec-ch-ua-platform` (browser only): browser `"Windows"`, mimicry –
- browserleaks-h2 `h2 #2 header user-agent` (browser only): browser `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/<ip> Safari/537.36 Edg/<ip>`, mimicry –
- browserleaks-h2 `h2 #2 header sec-ch-ua` (browser only): browser `"Not-A.Brand";v="99", "Microsoft Edge";v="157", "Chromium";v="157"`, mimicry –
- browserleaks-h2 `h2 #2 header sec-ch-ua-mobile` (browser only): browser `?0`, mimicry –
- browserleaks-h2 `h2 #2 header accept` (browser only): browser `image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8`, mimicry –
- browserleaks-h2 `h2 #2 header sec-fetch-site` (browser only): browser `same-origin`, mimicry –
- browserleaks-h2 `h2 #2 header sec-fetch-mode` (browser only): browser `no-cors`, mimicry –
- browserleaks-h2 `h2 #2 header sec-fetch-dest` (browser only): browser `image`, mimicry –
- browserleaks-h2 `h2 #2 header referer` (browser only): browser `https://tls.browserleaks.com/json`, mimicry –
- browserleaks-h2 `h2 #2 header accept-encoding` (browser only): browser `gzip, deflate, br, zstd`, mimicry –
- browserleaks-h2 `h2 #2 header accept-language` (browser only): browser `en-US,en;q=0.9`, mimicry –
- browserleaks-h2 `h2 #2 header priority` (browser only): browser `u=1, i`, mimicry –
- browserleaks-h2 `frame 3` (browser only): browser `{"flags":[{"id":1,"name":"END_STREAM"},{"id":4,"name":"END_HEADERS"},{"id":32,"name":"PRIORITY"}],"flags_raw":37,"id":1,"name":"HEADERS","priority":{"exclusive":1,"stream_dependency":1,"weight":220},"stream_id":3}`, mimicry –
```

## To do by hand

- Review the changes above; a hand-written section copied from the previous profile is unverified until the e2e tests pass against its inherited or new captures.
- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and a line in its Not covered by any claim for any new gap pinned.
- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `capturesFrom`; a scenario recaptured for this version needs its own entry if the oddity recurs.
