# Refresh of brave-157-macos, 2026-11-09

brave 157.1.99.55 on the second Mac, full set: new profile, copied from chrome-156-macos, the first brave profile on macos (nothing inherited). Made by `go run ./cmd/refresh` (staging `.refresh/20261109-mac`).

## Captures

Every capture names brave-157-macos with one user agent, and the browser's version was 157.1.99.55 before and after the run.

User agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`

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
| kinds-plain | 3 | 21s | 1m33s |
| cors | 3 | 34s | 2m3s |
| cookies | 3 | 16s | 2m33s |
| resume | 3 | 24s | 1m33s |
| resume-tls12 | 3 | 25s | 1m33s |
| cookies-edge | 3 | 16s | 2m3s |
| cors-edge | 3 | 16s | 1m33s |
| cors-edge-cookies | 3 | 16s | 1m33s |
| connections | 3 | 26s | 1m18s |
| headers-order | 3 | 16s | 1m48s |
| cookies-edge2 | 3 | 16s | 2m3s |
| redirects2 | 3 | 21s | 2m48s |
| redirects2-cookies | 3 | 21s | 2m48s |
| nav-redirect-so | 3 | 16s | 1m3s |
| nav-redirect-ss | 3 | 16s | 1m3s |
| nav-redirect-cs | 3 | 16s | 1m3s |
| nav-redirect-http | 3 | 15s | 1m3s |
| nav-redirect-page | 3 | 17s | 1m33s |
| nav-redirect-form | 3 | 21s | 3m33s |
| nav-redirect-click | 3 | 33s | 6m33s |
| nav-redirect-so-cookies | 3 | 16s | 1m3s |
| nav-redirect-ss-cookies | 3 | 16s | 1m3s |
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
| client-cert-ec | 3 | 26s | 9m33s |
| client-cert-rsa | 3 | 26s | 9m33s |
| client-cert-ec-tls12 | 3 | 26s | 9m33s |
| client-cert-rsa-tls12 | 3 | 26s | 9m33s |
| replay-github.com | 3 | 52s | 55s |
| replay-nike.com | 3 | 1m24s | 1m26s |
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
| h3-early.local-roots | 3 | 2m39s | 4m3s |
| h3-too-early.local-roots | 3 | 2m39s | 4m3s |
| h3-resume-table.local-roots | 3 | 1m56s | 2m48s |
| h3-streams-blocked.local-roots | 3 | 40s | 1m33s |
| bodiless.local-roots | 3 | 30s | 1m33s |
| client-hints.local-roots | 3 | 1m13s | 3m33s |
| proxy-https | 3 | not captured | 0s |
| proxy-https-h1 | 3 | not captured | 0s |
| proxy-https-auth | 3 | not captured | 0s |
| proxy-https-h1-auth | 3 | not captured | 0s |
| total | | 57m27s | 3h17m50s |

Wall time of the capture run (launches, copies and checks included): 57m29s.

## Inherited scenarios

None: the profile is checked against its own captures only.

## Changes from chrome-156-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36` → `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`
- sec-ch-ua: `"Not:A-Brand";v="8", "Chromium";v="156", "Google Chrome";v="156"` → `"Not-A.Brand";v="99", "Brave";v="157", "Chromium";v="157"`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `browser`: `"chrome"` → `"brave"`
- `capturesFrom`: `{"connections":"chrome-155-macos","cookies":"chrome-155-macos","cookies-edge":"chrome-155-macos","cookies-edge2":"chrome-155-macos","cors":"chrome-155-macos","cors-edge":"chrome-155-macos","explicit-table":"chrome-155-macos","h3-blackhole":"chrome-155-macos","h3-broken":"chrome-155-macos","h3-downlo…` → (none)
- `name`: `"chrome-156-macos"` → `"brave-157-macos"`
- `version`: `"156"` → `"157"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

- `tls.extensions`: `[{"type":"GREASE"},{"pointFormats":[0],"type":"0x000b"},{"data":"00b80582df1302010582df1302060582df13020d0582df13020e0582df13020f0582df1302120582df1302130582df13021408839a648c9b2d010708839a648c9b2d010808839a648c9b2d010908839a648c9b2d010a08839a648c9b2d010b08839a648c9b2d010c08839a648c9b2d010d08839a648…` → `[{"type":"GREASE"},{"type":"0x0017"},{"type":"0x0012"},{"data":"00","type":"0xff01"},{"pskModes":[1],"type":"0x002d"},{"data":"0100000000","type":"0x0005"},{"pointFormats":[0],"type":"0x000b"},{"ech":{"cipherSuites":[{"aead":"0x0001","kdf":"0x0001"}],"encLength":32,"payloadLengths":[144,176,208,240]…`

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic, tls.helloRetry, tls.x25519Reuse, tls.trailingDotSni, tls.clientAuth, http2.openingRecords, http2.goawayReply, http1.uploadRecords; h3gen: quic, http3; kindsgen: kinds, context, variants, emptyBody, clientHints, urls, http3 headers and priorities)

- `clientHints.fullVersion`: `"156.0.8078.80"` → `"157.0.0.0"`
- `clientHints.hints`: `["device-memory","sec-ch-device-memory","dpr","sec-ch-dpr","viewport-width","sec-ch-viewport-width","sec-ch-viewport-height","rtt","downlink","ect","sec-ch-ua","sec-ch-ua-mobile","sec-ch-ua-full-version","sec-ch-ua-arch","sec-ch-ua-platform","sec-ch-ua-platform-version","sec-ch-ua-model","sec-ch-ua-…` → `["sec-ch-ua","sec-ch-ua-mobile","sec-ch-ua-arch","sec-ch-ua-platform","sec-ch-ua-platform-version","sec-ch-ua-model","sec-ch-ua-bitness","sec-ch-ua-wow64","sec-ch-ua-full-version-list"]`
- `clientHints.machine.platformVersion`: `"27.0.1"` → `"26.2.0"`
- `clientHints.tableOrder`: `["device-memory","sec-ch-device-memory","rtt","downlink","ect","sec-ch-ua","sec-ch-ua-mobile","sec-ch-ua-arch","sec-ch-ua-platform","sec-ch-ua-platform-version","sec-ch-ua-model","sec-ch-ua-full-version","sec-ch-ua-full-version-list","sec-ch-ua-bitness","sec-ch-ua-wow64","sec-ch-ua-form-factors","se…` → `["sec-ch-ua","sec-ch-ua-mobile","sec-ch-ua-arch","sec-ch-ua-platform","sec-ch-ua-platform-version","sec-ch-ua-model","sec-ch-ua-full-version-list","sec-ch-ua-bitness","sec-ch-ua-wow64"]`
- `context.acceptLanguageQ`: (none) → `["0.5","0.6","0.7","0.8","0.9"]`
- `context.storageAccess`: `"active"` → `"none"`
- `http3.discovery.fallback.tcpDelayMs.max`: `306` → `5`
- `http3.discovery.fallback.tcpDelayMs.min`: `305` → `3`
- `http3.discovery.resumedDialDelayMs`: (none) → `[0,0,0,0,0,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,3,3,3,3,3,3,3,3,3,3,4]`
- `kinds.async_script.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.async_script.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.async_script.variants[3].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.async_script.variants[4].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.async_script.variants[5].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.async_script.variants[6].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.beacon.headers[7].name`: `"origin"` → `"sec-gpc"`
- `kinds.beacon.headers[7].value`: (none) → `"1"`
- `kinds.beacon.headers[8].name`: `"sec-fetch-site"` → `"origin"`
- `kinds.beacon.headers[9].name`: `"sec-fetch-mode"` → `"sec-fetch-site"`
- `kinds.beacon.headers[9].value`: `"no-cors"` → (none)
- `kinds.beacon.headers[10].name`: `"sec-fetch-dest"` → `"sec-fetch-mode"`
- `kinds.beacon.headers[10].value`: `"empty"` → `"no-cors"`
- `kinds.beacon.headers[11].name`: `"sec-fetch-storage-access"` → `"sec-fetch-dest"`
- `kinds.beacon.headers[11].value`: (none) → `"empty"`
- `kinds.beacon.http1[9].name`: `"Origin"` → `"Sec-GPC"`
- `kinds.beacon.http1[9].value`: (none) → `"1"`
- `kinds.beacon.http1[10].name`: `"Sec-Fetch-Site"` → `"Origin"`
- `kinds.beacon.http1[11].name`: `"Sec-Fetch-Mode"` → `"Sec-Fetch-Site"`
- `kinds.beacon.http1[11].value`: `"no-cors"` → (none)
- `kinds.beacon.http1[12].name`: `"Sec-Fetch-Dest"` → `"Sec-Fetch-Mode"`
- `kinds.beacon.http1[12].value`: `"empty"` → `"no-cors"`
- `kinds.beacon.http1[13].name`: `"Sec-Fetch-Storage-Access"` → `"Sec-Fetch-Dest"`
- `kinds.beacon.http1[13].value`: (none) → `"empty"`
- `kinds.defer_script.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.defer_script.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.defer_script.variants[3].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.defer_script.variants[4].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.defer_script.variants[5].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.defer_script.variants[6].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.fetch.variants[15].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.fetch.variants[17].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Origin"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Origin"},{"name":"…`
- `kinds.fetch.variants[18].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.fetch.variants[20].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Origin"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Origin"},{"name":"…`
- `kinds.fetch.variants[21].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.fetch.variants[23].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Origin"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Origin"},{"name":"…`
- `kinds.fetch.variants[24].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Origin"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Origin"},{"name":"…`
- `kinds.font.variants[0].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.font.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.font.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.injected_script.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.injected_script.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.injected_script.variants[3].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.injected_script.variants[4].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.injected_script.variants[5].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.injected_script.variants[6].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.preload_font.variants[0].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.preload_font.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.preload_font.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.preload_script.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.preload_script.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.preload_script.variants[3].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.preload_script.variants[4].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.preload_script.variants[5].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.preload_script.variants[6].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.script.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.script.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.script.variants[3].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.script.variants[4].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.script.variants[5].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.script.variants[6].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Origin"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"…`
- `kinds.style.variants[0].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"text/css,*/*;q=0.1"},{"name":"Referer"},{"name":"Accept-Encoding…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"text/css,*/*;q=0.1"},{"name":"Sec-GPC","value":"1"},{"name":"Ref…`
- `kinds.style.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"text/css,*/*;q=0.1"},{"name":"Referer"},{"name":"Accept-Encoding…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"text/css,*/*;q=0.1"},{"name":"Sec-GPC","value":"1"},{"name":"Ref…`
- `kinds.style.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"text/css,*/*;q=0.1"},{"name":"Referer"},{"name":"Accept-Encoding…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"text/css,*/*;q=0.1"},{"name":"Sec-GPC","value":"1"},{"name":"Ref…`
- `kinds.xhr.variants[1].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Accept-Encoding","value":"gzip…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Referer"},{"name":…`
- `kinds.xhr.variants[2].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Origin"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Origin"},{"name":"…`
- `kinds.xhr.variants[3].http1`: `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Origin"},{"name":"Referer"},{"name":"Accept-Encod…` → `[{"name":"Host"},{"name":"Connection","value":"keep-alive"},{"name":"User-Agent","value":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"},{"name":"Accept","value":"*/*"},{"name":"Sec-GPC","value":"1"},{"name":"Origin"},{"name":"…`
- `quic.loss.pathDegrading.afterPtos`: `4` → `3`
- `quic.packetNumbers`: `"shared"` → `"shared_second_long"`
- `tls.quic.drop[1]`: `"0x000b"` → `"0x0017"`
- `tls.quic.drop[3]`: `"0x0017"` → `"0xff01"`
- `tls.quic.drop[4]`: `"0xff01"` → `"0x0005"`
- `tls.quic.drop[5]`: `"0x0023"` → `"0x000b"`
- `tls.quic.drop[6]`: `"0x0005"` → `"0x0023"`
- `tls.quic.replace[0].alps`: `["h3"]` → (none)
- `tls.quic.replace[0].signatureAlgorithms`: (none) → `["0x0403","0x0804","0x0401","0x0503","0x0805","0x0501","0x0806","0x0601","0x0201"]`
- `tls.quic.replace[0].type`: `"0x44cd"` → `"0x000d"`
- `tls.quic.replace[1].alpn`: `["h3"]` → (none)
- `tls.quic.replace[1].groups`: (none) → `["0x11ec","0x001d","0x0017","0x0018"]`
- `tls.quic.replace[1].type`: `"0x0010"` → `"0x000a"`
- `tls.quic.replace[2].alps`: (none) → `["h3"]`
- `tls.quic.replace[2].keyShareLengths`: `[1216,32]` → (none)
- `tls.quic.replace[2].keyShares`: `["0x11ec","0x001d"]` → (none)
- `tls.quic.replace[2].type`: `"0x0033"` → `"0x44cd"`
- `tls.quic.replace[3].groups`: `["0x11ec","0x001d","0x0017","0x0018"]` → (none)
- `tls.quic.replace[3].keyShareLengths`: (none) → `[1216,32]`
- `tls.quic.replace[3].keyShares`: (none) → `["0x11ec","0x001d"]`
- `tls.quic.replace[3].type`: `"0x000a"` → `"0x0033"`
- `tls.quic.replace[4].alpn`: (none) → `["h3"]`
- `tls.quic.replace[4].signatureAlgorithms`: `["0x0403","0x0804","0x0401","0x0503","0x0805","0x0501","0x0806","0x0601","0x0201"]` → (none)
- `tls.quic.replace[4].type`: `"0x000d"` → `"0x0010"`

### Hand-written, copied from the previous profile

No change.

## Deprecation

No brave profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests: not run.

realcheck: not run.

## To do by hand

- Review the changes above; a hand-written section copied from the previous profile is unverified until the e2e tests pass against its inherited or new captures.
- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and a line in its Not covered by any claim for any new gap pinned.
- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `capturesFrom`; a scenario recaptured for this version needs its own entry if the oddity recurs.
