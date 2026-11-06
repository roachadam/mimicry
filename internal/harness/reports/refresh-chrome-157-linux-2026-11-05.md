# Refresh of chrome-157-linux, 2026-11-05

chrome 157.0.8103.38 on the Linux machine (WSL2 on the Windows PC), full set: new profile, copied from chrome-156-macos, the first on linux (nothing inherited). Made by `go run ./cmd/refresh` (staging `.refresh/20261105-113113`). Captured on 2026-11-05 in Ubuntu 24.04, the browser on Xvfb ([0058](../../../docs/decisions/0058-linux-profiles.md)); `h3-v2` failed on Ubuntu's aioquic 0.9, so it and the scenarios after it were captured in a second run with aioquic 1.7 (`.refresh/20261105-124744`) and merged. `h3gen` first refused the captures (unpinned tokenless reconnections and the server's Initial acknowledged, as on macOS; a `requestsAfterConfirmed` that the 0-RTT pages' held requests contradicted), kindsgen the moving `downlink` estimate and the profile check the empty `platformVersion`, and the e2e tests failed on gap tables keyed by profile and one repeated QPACK capacity; after those fixes `-resume -realcheck=false` derived, installed and tested. realcheck is from the first resume, with the same profile.

## Captures

Every capture names chrome-157-linux with one user agent, and the browser's version was 157.0.8103.38 before and after the run.

User agent: `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 16s | 57s |
| set-cookie | 3 | 16s | 57s |
| large-settings | 3 | 16s | 57s |
| small-limits | 3 | 16s | 57s |
| huge-table | 3 | 16s | 57s |
| explicit-table | 3 | 16s | 57s |
| slow-idle | 3 | 2m35s | 2m3s |
| http1 | 3 | 16s | 57s |
| http1-upload | 3 | 17s | 57s |
| h2-upload | 3 | 16s | 57s |
| proxy-connect | 3 | 16s | 57s |
| proxy-socks5 | 3 | 16s | 57s |
| proxy-plain | 3 | 16s | 57s |
| proxy-auth | 3 | 25s | 1m33s |
| proxy-plain-auth | 3 | 17s | 57s |
| kinds | 3 | 21s | 1m33s |
| kinds-http1 | 3 | 20s | 1m33s |
| kinds-plain | 3 | 20s | 1m33s |
| cors | 3 | 34s | 2m3s |
| cookies | 3 | 16s | 2m33s |
| resume | 3 | 24s | 1m33s |
| resume-tls12 | 3 | 24s | 1m33s |
| cookies-edge | 3 | 16s | 2m3s |
| cors-edge | 3 | 16s | 1m33s |
| cors-edge-cookies | 3 | 16s | 1m33s |
| connections | 3 | 26s | 1m18s |
| headers-order | 3 | 16s | 1m48s |
| cookies-edge2 | 3 | 16s | 2m3s |
| redirects2 | 3 | 20s | 2m48s |
| redirects2-cookies | 3 | 20s | 2m48s |
| nav-redirect-so | 3 | 16s | 1m3s |
| nav-redirect-ss | 3 | 16s | 1m3s |
| nav-redirect-cs | 3 | 16s | 1m3s |
| nav-redirect-http | 3 | 16s | 1m3s |
| nav-redirect-page | 3 | 16s | 1m33s |
| nav-redirect-form | 3 | 19s | 3m33s |
| nav-redirect-click | 3 | 31s | 6m33s |
| nav-redirect-so-cookies | 3 | 16s | 1m3s |
| nav-redirect-ss-cookies | 3 | 16s | 1m3s |
| nav-redirect-cs-cookies | 3 | 16s | 1m3s |
| nav-redirect-http-cookies | 3 | 16s | 1m3s |
| nav-redirect-page-cookies | 3 | 17s | 1m33s |
| nav-redirect-form-cookies | 3 | 19s | 3m33s |
| nav-redirect-click-cookies | 3 | 31s | 6m33s |
| websocket | 3 | 1m46s | 6m33s |
| websocket-h2 | 3 | 22s | 6m33s |
| proxy-websocket | 3 | 34s | 3m33s |
| proxy-websocket-auth | 3 | 35s | 3m33s |
| proxy-websocket-socks5 | 3 | 28s | 3m33s |
| h3-discovery | 3 | 2m46s | 3m48s |
| h3-page | 3 | 40s | 2m3s |
| h3-blackhole | 3 | 2m46s | 3m33s |
| hrr | 3 | 20s | 1m18s |
| urls | 3 | 17s | 5m3s |
| replay-github.com | 3 | 52s | 55s |
| replay-nike.com | 3 | 1m24s | 1m26s |
| replay-nytimes.com | 3 | 1m33s | 1m33s |
| replay-ticketmaster.com | 3 | 1m30s | 1m31s |
| replay-walmart.com | 3 | 1m26s | 1m28s |
| replay-zillow.com | 3 | 1m12s | 1m14s |
| h3-discovery.local-roots | 3 | 2m46s | 3m48s |
| h3-page.local-roots | 3 | 40s | 2m3s |
| h3-broken.local-roots | 3 | 3m52s | 4m48s |
| h3-v2.local-roots | 3 | 2m25s | 3m3s |
| h3-vn.local-roots | 3 | 1m19s | 2m3s |
| h3-download.local-roots | 3 | 55s | 2m48s |
| h3-early.local-roots | 3 | 2m38s | 4m3s |
| h3-too-early.local-roots | 3 | 2m38s | 4m3s |
| h3-streams-blocked.local-roots | 3 | 40s | 1m33s |
| bodiless.local-roots | 3 | 30s | 1m33s |
| client-hints.local-roots | 3 | 1m10s | 3m33s |
| total | | 54m31s | 2h36m50s |

Wall time of the capture run (launches, copies and checks included): 1h28m50s.

## Inherited scenarios

None: the profile is checked against its own captures only.

## Changes from chrome-156-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36` → `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`
- sec-ch-ua: `"Not:A-Brand";v="8", "Chromium";v="156", "Google Chrome";v="156"` → `"Not-A.Brand";v="99", "Google Chrome";v="157", "Chromium";v="157"`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `capturesFrom`: `{"connections":"chrome-155-macos","cookies":"chrome-155-macos","cookies-edge":"chrome-155-macos","cookies-edge2":"chrome-155-macos","cors":"chrome-155-macos","cors-edge":"chrome-155-macos","explicit-table":"chrome-155-macos","h3-blackhole":"chrome-155-macos","h3-broken":"chrome-155-macos","h3-downlo…` → (none)
- `name`: `"chrome-156-macos"` → `"chrome-157-linux"`
- `platform`: `"macos"` → `"linux"`
- `version`: `"156"` → `"157"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

- `headers[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `http1.headers[5].value`: `"\"macOS\""` → `"\"Linux\""`

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic; h3gen: quic, http3; kindsgen: kinds, context, variants, http3 headers and priorities)

- `http3.discovery.fallback.tcpDelayMs.max`: `306` → `303`
- `http3.discovery.fallback.tcpDelayMs.min`: `305` → `302`
- `kinds.async_script.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.async_script.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.async_script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.beacon.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.beacon.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.defer_script.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.defer_script.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.defer_script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[0].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[0].http1[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[1].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[1].http1[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[2].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[3].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[4].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[5].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[6].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[7].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[8].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[9].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[10].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[11].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[12].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[13].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.fetch.variants[14].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.font.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.font.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.frame.headers[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.frame.http1[4].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.variants[0].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.variants[0].http1[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.variants[1].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.variants[1].http1[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.variants[2].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.variants[3].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.variants[4].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.image.variants[5].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.variants[0].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.variants[0].http1[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.variants[1].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.variants[1].http1[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.variants[2].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.variants[3].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.variants[4].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_image.variants[5].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_script.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_script.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.injected_script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.preload_font.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.preload_font.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.preload_script.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.preload_script.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.preload_script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.script.headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.script.http1[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.script.variants[0].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.style.headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.style.http1[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.xhr.headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.xhr.http1[2].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.xhr.variants[0].headers[0].value`: `"\"macOS\""` → `"\"Linux\""`
- `kinds.xhr.variants[4].headers[1].value`: `"\"macOS\""` → `"\"Linux\""`
- `quic.crypto.addedFrames.min`: `2` → `1`
- `variants[0].headers[4].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[0].http1[6].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[1].headers[4].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[2].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[3].headers[13].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[4].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[5].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[6].headers[13].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[7].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[8].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[9].headers[13].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[10].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[11].headers[13].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[12].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[13].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[14].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[15].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[16].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[17].headers[13].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[18].headers[4].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[19].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[20].headers[13].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[21].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[22].headers[13].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[23].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[24].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[25].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[26].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[27].headers[13].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[28].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[29].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[30].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[31].headers[9].value`: `"\"macOS\""` → `"\"Linux\""`

### Hand-written, copied from the previous profile

- `clientHints.fullVersion`: `"156.0.8078.80"` → `"157.0.8103.38"`
- `clientHints.machine.arch`: `"arm"` → `"x86"`
- `clientHints.machine.colorScheme`: `"dark"` → `"light"`
- `clientHints.machine.deviceMemory`: `32` → `16`
- `clientHints.machine.downlink`: `1.6` → `1.55`
- `clientHints.machine.dpr`: `2` → `1`
- `clientHints.machine.platformVersion`: `"27.0.1"` → `""`
- `clientHints.machine.viewportHeight`: `953` → `973`
- `clientHints.machine.viewportWidth`: `1200` → `945`

## Deprecation

No chrome profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests (`MIMICRY_PROFILES=chrome-157-linux go test -count=1 ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/ (harness), go test -count=1 ./profiles/ ./cmd/profilegen/ (library)`, 4m23s): passed. Output: `.refresh/20261105-113113/chrome-linux/tests.log`.

realcheck (`realcheck -browsers chrome -profiles chrome-157-linux (on the Linux machine (WSL2 on the Windows PC))`, 1m41s): ran. Output: `.refresh/20261105-113113/chrome-linux/realcheck/compare.md`.

```
# Echo endpoints: browser vs mimicry

## browser-chrome-157-linux vs chrome-157-linux

| Endpoint | Browser proto (tries) | mimicry proto (tries) | Fields | Same | Random by design | Differ | One side only |
|---|---|---|---|---|---|---|---|
| peet | h2 (1) | h2 (1) | 62 | 59 | 3 | 0 | 0 |
| browserleaks-tls |  (1) |  (1) | 11 | 7 | 4 | 0 | 0 |
| browserleaks-h2 |  (1) |  (1) | 23 | 23 | 0 | 0 | 0 |
| impersonate-h2 | h2 (1) | h2 (1) | 53 | 50 | 3 | 0 | 0 |
| impersonate-h3 | h3 (2) | h3 (2) | 47 | 41 | 6 | 0 | 0 |

| Field | Browser | mimicry | |
|---|---|---|---|
| JA4 | `t13d1517h2_8daaf6152771_cb7bf5808d99` | `t13d1517h2_8daaf6152771_cb7bf5808d99` | ✓ |
| JA3 hash, extensions sorted (JA3N) | `bd4930bd9b000ee684830e44bab76fdf` | `bd4930bd9b000ee684830e44bab76fdf` | ✓ |
| JA3 hash, as sent | `4c65a0e8ca1f2fe4ef03b389fa8b9ac1` | `0c51be78f15cdee054fa146f27510da6` | ✓ random: extension order, shuffled per connection |
| peetprint hash | `fc97c1cdfb1409c9a9326c1b726d1dee` | `fc97c1cdfb1409c9a9326c1b726d1dee` | ✓ |
| Akamai HTTP/2 | `1:65536;2:0;4:6291456;6:262144\|15663105\|0\|m,a,s,p` | `1:65536;2:0;4:6291456;6:262144\|15663105\|0\|m,a,s,p` | ✓ |
| HTTP/2 header order | `:method,:authority,:scheme,:path,sec-ch-ua,sec-ch-ua-mobile,sec-ch-ua-platform,upgrade-ins…` | `:method,:authority,:scheme,:path,sec-ch-ua,sec-ch-ua-mobile,sec-ch-ua-platform,upgrade-ins…` | ✓ |
| HTTP/2 HEADERS flags and priority | `{"flags":["EndStream (0x1)","EndHeaders (0x4)","Priority (0x20)"],"priority":{"depends_on"…` | `{"flags":["EndStream (0x1)","EndHeaders (0x4)","Priority (0x20)"],"priority":{"depends_on"…` | ✓ |
| impersonate.pro /api/http3 protocol | `http3` | `http3` | ✓ |
| QUIC JA3N | `18555a4ad0bd66d5fd99e1a493aa9f70` | `18555a4ad0bd66d5fd99e1a493aa9f70` | ✓ |
| HTTP/3 fingerprint (perk, normalized) | `1:65536;6:262144;7:100;51:1;GREASE\|m,a,s,p\|1:30000;3:1472;4:15728640;5:6291456;6:6291456;7…` | `1:65536;6:262144;7:100;51:1;GREASE\|m,a,s,p\|1:30000;3:1472;4:15728640;5:6291456;6:6291456;7…` | ✓ |
| HTTP/3 header order | `:method,:authority,:scheme,:path,sec-ch-ua,sec-ch-ua-mobile,sec-ch-ua-platform,upgrade-ins…` | `:method,:authority,:scheme,:path,sec-ch-ua,sec-ch-ua-mobile,sec-ch-ua-platform,upgrade-ins…` | ✓ |

Random by design (the browser's value among the profile's choices):

- extension order, shuffled per connection: peet tls.ja3, peet tls.ja3_hash, browserleaks-tls ja4_o, browserleaks-tls ja4_ro, browserleaks-tls ja3_hash, browserleaks-tls ja3_text, impersonate-h2 tls.ja3.text, impersonate-h2 tls.ja3.hash, impersonate-h3 tls.ja3.text, impersonate-h3 tls.ja3.hash
- GREASE ECH payload length, one of 144/176/208/240: peet ext ECH payload length, impersonate-h2 ext ECH payload length, impersonate-h3 ext ECH payload length
- QUIC transport parameter order, shuffled per connection: impersonate-h3 http3.perk_hash, impersonate-h3 http3.perk_text, impersonate-h3 ext quic_transport_parameters order

Differences:

- none
```

## To do by hand

- Review the changes above; a hand-written section copied from the previous profile is unverified until the e2e tests pass against its inherited or new captures.
- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and a line in its Not covered by any claim for any new gap pinned.
- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `capturesFrom`; a scenario recaptured for this version needs its own entry if the oddity recurs.
