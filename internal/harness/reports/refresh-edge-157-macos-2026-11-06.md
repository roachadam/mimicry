# Refresh of edge-157-macos, 2026-11-06

edge 157.0.4341.42 on the second Mac, full set: new profile, copied from chrome-156-macos, the first edge profile on macos (nothing inherited). Made by `go run ./cmd/refresh` (staging `.refresh/20261106-113312`). Captured on 2026-11-06 on the second Mac (macOS 26.2) after updating Edge from 156 with Microsoft's package ([0059](../../../docs/decisions/0059-edge-profiles.md)). `h3gen` first refused the captures (Edge's second Initial packet number in 2 bytes with a token, which no numbering rule allowed: `shared_second_long` added; tokenless reconnections and the server's Initial acknowledged, as Chrome's), kindsgen the Chromium brand's own full version (`clientHints.chromiumFullVersion` added), and the e2e tests failed on gap tables keyed by profile; after those fixes `-resume -realcheck=false` derived, installed and tested. realcheck is from the first resume, with the same profile.

## Captures

Every capture names edge-157-macos with one user agent, and the browser's version was 157.0.4341.42 before and after the run.

User agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36 Edg/157.0.0.0`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 16s | 57s |
| set-cookie | 3 | 16s | 57s |
| large-settings | 3 | 16s | 57s |
| small-limits | 3 | 16s | 57s |
| huge-table | 3 | 16s | 57s |
| explicit-table | 3 | 16s | 57s |
| slow-idle | 3 | 1m33s | 2m3s |
| http1 | 3 | 16s | 57s |
| http1-upload | 3 | 16s | 57s |
| h2-upload | 3 | 16s | 57s |
| proxy-connect | 3 | 16s | 57s |
| proxy-socks5 | 3 | 16s | 57s |
| proxy-plain | 3 | 16s | 57s |
| proxy-auth | 3 | 26s | 1m33s |
| proxy-plain-auth | 3 | 17s | 57s |
| kinds | 3 | 21s | 1m33s |
| kinds-http1 | 3 | 21s | 1m33s |
| kinds-plain | 3 | 20s | 1m33s |
| cors | 3 | 34s | 2m3s |
| cookies | 3 | 16s | 2m33s |
| resume | 3 | 25s | 1m33s |
| resume-tls12 | 3 | 25s | 1m33s |
| cookies-edge | 3 | 16s | 2m3s |
| cors-edge | 3 | 16s | 1m33s |
| cors-edge-cookies | 3 | 16s | 1m33s |
| connections | 3 | 26s | 1m18s |
| headers-order | 3 | 16s | 1m48s |
| cookies-edge2 | 3 | 16s | 2m3s |
| redirects2 | 3 | 21s | 2m48s |
| redirects2-cookies | 3 | 21s | 2m48s |
| nav-redirect-so | 3 | 15s | 1m3s |
| nav-redirect-ss | 3 | 15s | 1m3s |
| nav-redirect-cs | 3 | 15s | 1m3s |
| nav-redirect-http | 3 | 16s | 1m3s |
| nav-redirect-page | 3 | 17s | 1m33s |
| nav-redirect-form | 3 | 21s | 3m33s |
| nav-redirect-click | 3 | 33s | 6m33s |
| nav-redirect-so-cookies | 3 | 16s | 1m3s |
| nav-redirect-ss-cookies | 3 | 15s | 1m3s |
| nav-redirect-cs-cookies | 3 | 16s | 1m3s |
| nav-redirect-http-cookies | 3 | 16s | 1m3s |
| nav-redirect-page-cookies | 3 | 17s | 1m33s |
| nav-redirect-form-cookies | 3 | 21s | 3m33s |
| nav-redirect-click-cookies | 3 | 33s | 6m33s |
| websocket | 3 | 1m47s | 6m33s |
| websocket-h2 | 3 | 23s | 6m33s |
| proxy-websocket | 3 | 34s | 3m33s |
| proxy-websocket-auth | 3 | 36s | 3m33s |
| proxy-websocket-socks5 | 3 | 28s | 3m33s |
| h3-discovery | 3 | 2m46s | 3m48s |
| h3-page | 3 | 40s | 2m3s |
| h3-blackhole | 3 | 2m46s | 3m33s |
| hrr | 3 | 20s | 1m18s |
| urls | 3 | 17s | 5m3s |
| replay-github.com | 3 | 52s | 55s |
| replay-nike.com | 3 | 1m25s | 1m26s |
| replay-nytimes.com | 3 | 1m32s | 1m33s |
| replay-ticketmaster.com | 3 | 1m29s | 1m31s |
| replay-walmart.com | 3 | 1m26s | 1m28s |
| replay-zillow.com | 3 | 1m12s | 1m14s |
| h3-discovery.local-roots | 3 | 2m46s | 3m48s |
| h3-page.local-roots | 3 | 40s | 2m3s |
| h3-broken.local-roots | 3 | 3m52s | 4m48s |
| h3-v2.local-roots | 3 | 2m25s | 3m3s |
| h3-vn.local-roots | 3 | 1m19s | 2m3s |
| h3-download.local-roots | 3 | 54s | 2m48s |
| h3-early.local-roots | 3 | 2m38s | 4m3s |
| h3-too-early.local-roots | 3 | 2m39s | 4m3s |
| h3-streams-blocked.local-roots | 3 | 40s | 1m33s |
| bodiless.local-roots | 3 | 30s | 1m33s |
| client-hints.local-roots | 3 | 1m12s | 3m33s |
| total | | 53m44s | 2h36m50s |

Wall time of the capture run (launches, copies and checks included): 53m46s.

## Inherited scenarios

None: the profile is checked against its own captures only.

## Changes from chrome-156-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36` → `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36 Edg/157.0.0.0`
- sec-ch-ua: `"Not:A-Brand";v="8", "Chromium";v="156", "Google Chrome";v="156"` → `"Not-A.Brand";v="99", "Microsoft Edge";v="157", "Chromium";v="157"`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `browser`: `"chrome"` → `"edge"`
- `capturesFrom`: `{"connections":"chrome-155-macos","cookies":"chrome-155-macos","cookies-edge":"chrome-155-macos","cookies-edge2":"chrome-155-macos","cors":"chrome-155-macos","cors-edge":"chrome-155-macos","explicit-table":"chrome-155-macos","h3-blackhole":"chrome-155-macos","h3-broken":"chrome-155-macos","h3-downlo…` → (none)
- `name`: `"chrome-156-macos"` → `"edge-157-macos"`
- `version`: `"156"` → `"157"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

- `headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `http1.headers[8].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `tls.extensions`: `[{"type":"GREASE"},{"pointFormats":[0],"type":"0x000b"},{"data":"00b80582df1302010582df1302060582df13020d0582df13020e0582df13020f0582df1302120582df1302130582df13021408839a648c9b2d010708839a648c9b2d010808839a648c9b2d010908839a648c9b2d010a08839a648c9b2d010b08839a648c9b2d010c08839a648c9b2d010d08839a648…` → `[{"type":"GREASE"},{"certCompression":["0x0002"],"type":"0x001b"},{"data":"00","type":"0xff01"},{"groups":["GREASE","0x11ec","0x001d","0x0017","0x0018"],"type":"0x000a"},{"type":"0x0012"},{"signatureAlgorithms":["GREASE","0x0904","0x0905","0x0906","0x0403","0x0804","0x0401","0x0503","0x0805","0x0501…`

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic, tls.helloRetry, tls.x25519Reuse, http2.openingRecords, http2.goawayReply, http1.uploadRecords; h3gen: quic, http3; kindsgen: kinds, context, variants, emptyBody, clientHints, urls, http3 headers and priorities)

- `clientHints.chromiumFullVersion`: (none) → `"157.0.8103.39"`
- `clientHints.fullVersion`: `"156.0.8078.80"` → `"157.0.4341.42"`
- `clientHints.machine.deviceMemory`: `32` → `16`
- `clientHints.machine.platformVersion`: `"27.0.1"` → `"26.2.0"`
- `clientHints.machine.reducedMotion`: `"no-preference"` → `"reduce"`
- `clientHints.machine.reducedTransparency`: `"no-preference"` → `"reduce"`
- `clientHints.machine.viewportHeight`: `953` → `1198`
- `clientHints.machine.viewportWidth`: `1200` → `1192`
- `http3.discovery.fallback.tcpDelayMs.max`: `306` → `315`
- `http3.discovery.fallback.tcpDelayMs.min`: `305` → `313`
- `kinds.frame.headers[5].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `kinds.frame.http1[7].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `kinds.frame.variants[0].http1[4].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `kinds.frame.variants[1].http1[4].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `kinds.frame.variants[2].http1[4].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `kinds.image.headers[5].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.http1[7].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[0].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[0].http1[6].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[1].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[1].http1[6].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[2].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[3].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[4].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[5].headers[5].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[6].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[7].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[8].http1[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[9].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[10].http1[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[11].http1[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[12].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.image.variants[13].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.headers[5].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.http1[7].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[0].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[0].http1[6].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[1].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[1].http1[6].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[2].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[3].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[4].headers[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[5].headers[5].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[6].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[7].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[8].http1[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[9].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[10].http1[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[11].http1[4].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[12].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `kinds.injected_image.variants[13].http1[3].value`: `"image/jxl,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"` → `"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"`
- `quic.packetNumbers`: `"shared"` → `"shared_second_long"`
- `tls.quic.drop[1]`: `"0x000b"` → `"0xff01"`
- `tls.quic.drop[3]`: `"0x0017"` → `"0x0005"`
- `tls.quic.drop[4]`: `"0xff01"` → `"0x0023"`
- `tls.quic.drop[5]`: `"0x0023"` → `"0x000b"`
- `tls.quic.drop[6]`: `"0x0005"` → `"0x0017"`
- `tls.quic.replace[0].alps`: `["h3"]` → (none)
- `tls.quic.replace[0].groups`: (none) → `["0x11ec","0x001d","0x0017","0x0018"]`
- `tls.quic.replace[0].type`: `"0x44cd"` → `"0x000a"`
- `tls.quic.replace[1].alpn`: `["h3"]` → (none)
- `tls.quic.replace[1].signatureAlgorithms`: (none) → `["0x0403","0x0804","0x0401","0x0503","0x0805","0x0501","0x0806","0x0601","0x0201"]`
- `tls.quic.replace[1].type`: `"0x0010"` → `"0x000d"`
- `tls.quic.replace[2].keyShareLengths`: `[1216,32]` → (none)
- `tls.quic.replace[2].keyShares`: `["0x11ec","0x001d"]` → (none)
- `tls.quic.replace[2].type`: `"0x0033"` → `"0x002b"`
- `tls.quic.replace[2].versions`: (none) → `["0x0304"]`
- `tls.quic.replace[3].groups`: `["0x11ec","0x001d","0x0017","0x0018"]` → (none)
- `tls.quic.replace[3].keyShareLengths`: (none) → `[1216,32]`
- `tls.quic.replace[3].keyShares`: (none) → `["0x11ec","0x001d"]`
- `tls.quic.replace[3].type`: `"0x000a"` → `"0x0033"`
- `tls.quic.replace[4].alps`: (none) → `["h3"]`
- `tls.quic.replace[4].signatureAlgorithms`: `["0x0403","0x0804","0x0401","0x0503","0x0805","0x0501","0x0806","0x0601","0x0201"]` → (none)
- `tls.quic.replace[4].type`: `"0x000d"` → `"0x44cd"`
- `tls.quic.replace[5].alpn`: (none) → `["h3"]`
- `tls.quic.replace[5].type`: `"0x002b"` → `"0x0010"`
- `tls.quic.replace[5].versions`: `["0x0304"]` → (none)
- `variants[0].headers[9].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[0].http1[11].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[1].headers[9].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[2].headers[3].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[3].headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[4].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[5].headers[3].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[6].headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[7].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[8].headers[3].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[9].headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[10].headers[3].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[11].headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[12].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[13].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[14].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[15].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[16].headers[3].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[17].headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[18].headers[9].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[19].headers[3].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[20].headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[21].headers[3].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[22].headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[23].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[24].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[25].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[26].headers[3].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[27].headers[6].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[28].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[29].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[30].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[31].headers[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[32].http1[4].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[33].http1[8].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`
- `variants[34].http1[4].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"`

### Hand-written, copied from the previous profile

No change.

## Deprecation

No edge profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests (`MIMICRY_PROFILES=edge-157-macos go test -count=1 ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/ (harness), go test -count=1 ./profiles/ ./cmd/profilegen/ (library)`, 4m24s): passed. Output: `.refresh/20261106-113312/edge/tests.log`.

realcheck (`realcheck -browsers edge -profiles edge-157-macos (on the second Mac)`, 1m24s): ran. Output: `.refresh/20261106-113312/edge/realcheck/compare.md`.

```
# Echo endpoints: browser vs mimicry

## browser-edge-157-macos vs edge-157-macos

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
| JA3 hash, as sent | `93fc1d2cfefa0d6827e6a207cf1ccd8c` | `52d83e1bbef25b158af125e9c14cccea` | ✓ random: extension order, shuffled per connection |
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
- browserleaks-h2 `h2 #2 header sec-ch-ua-platform` (browser only): browser `"macOS"`, mimicry –
- browserleaks-h2 `h2 #2 header user-agent` (browser only): browser `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/<ip> Safari/537.36 Edg/<ip>`, mimicry –
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
