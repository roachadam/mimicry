# Refresh of safari-27-ios, 2026-11-01

safari 27.2 on the iPhone, served from this Mac, ios set: new profile, copied from safari-27-macos, inheriting what it does not capture from macos safari's captures, whose network stack it shares (decision 0051). Made by `go run ./cmd/refresh` (staging `.refresh/20261101-130316`).

## Captures

Every capture names safari-27-ios with one user agent, and the browser's version was 27.2 before and after the run.

User agent: `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1`

| Scenario | Samples | Driver | Measured | Estimated |
|---|---|---|---|---|
| page | 3 | safaridriver | 16s | 1m9s |
| kinds | 3 | manual | 1m14s | 1m45s |
| kinds-http1 | 3 | manual | 1m5s | 0s |
| cookies | 3 | devicectl | 2s | 2m45s |
| cors | 3 | devicectl | 20s | 2m15s |
| cors-edge | 3 | manual | 50s | 0s |
| cors-edge-cookies | 3 | manual | 44s | 0s |
| redirects2 | 3 | manual | 1m34s | 0s |
| redirects2-cookies | 3 | manual | 1m14s | 0s |
| headers-order | 3 | devicectl | 5s | 0s |
| http1 | 3 | safaridriver | 14s | 1m9s |
| h3-discovery | 3 | manual | 4m14s | 4m0s |
| h3-page | 3 | manual | 1m0s | 2m15s |
| resume | 3 | devicectl | 11s | 1m45s |
| websocket | 3 | devicectl | 39s | 6m45s |
| websocket-h2 | 3 | devicectl | 37s | 6m45s |
| proxy-connect | 3 | safaridriver | 13s | 1m9s |
| proxy-plain | 3 | safaridriver | 13s | 1m9s |
| total | | | 14m52s | 32m51s |

## Inherited scenarios

Checked against these profiles' captures, version tokens rewritten (`capturesFrom`, decision 0035):

- cookies-edge: safari-27-macos
- cookies-edge2: safari-27-macos
- kinds-plain: safari-27-macos
- nav-redirect-cs: safari-27-macos
- nav-redirect-cs-cookies: safari-27-macos
- nav-redirect-http: safari-27-macos
- nav-redirect-http-cookies: safari-27-macos
- nav-redirect-so: safari-27-macos
- nav-redirect-so-cookies: safari-27-macos
- nav-redirect-ss: safari-27-macos
- nav-redirect-ss-cookies: safari-27-macos
- set-cookie: safari-27-macos
- slow-idle: safari-27-macos

## Changes from safari-27-macos

Version strings, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.0.1 Safari/605.1.15` → `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `capturesFrom`: (none) → `{"cookies-edge":"safari-27-macos","cookies-edge2":"safari-27-macos","kinds-plain":"safari-27-macos","nav-redirect-cs":"safari-27-macos","nav-redirect-cs-cookies":"safari-27-macos","nav-redirect-http":"safari-27-macos","nav-redirect-http-cookies":"safari-27-macos","nav-redirect-so":"safari-27-macos",…`
- `name`: `"safari-27-macos"` → `"safari-27-ios"`
- `platform`: `"macos"` → `"ios"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

No change.

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic; h3gen: quic, http3; kindsgen: kinds, context, variants, http3 headers and priorities)

- `context.anonymousConnections`: (none) → `true`
- `http3.closeOnGoaway`: `true` → (none)
- `http3.discovery.dialDelayMs`: `[3,4,4,4,5,6,6,6,6,6,7,7,7,7,7,7,7,8,8,8,8,8,8,9,9,9,10,10,10,10]` → (none)
- `http3.discovery.resumedDialDelayMs`: `[5,5,6,6,7,7,7,7,7,7,8,8,8,8,8,8,8,8,8,8,8,9,9,9,9,9,9,9,9,9,9,9,9,9,9,9,9,9,9,10,10,10,10,10,10,10,10,11,11,11,22]` → (none)
- `http3.earlyData`: `{"methods":["GET","HEAD","OPTIONS"],"retryTooEarly":true}` → (none)
- `http3.earlyStreamsWithRequest`: `true` → (none)
- `http3.qpack.huffman`: `"always"` → `"shorter"`
- `http3.requestsAfterConfirmed`: `true` → (none)
- `http3.uniStreams`: `[{"open":"handshake","type":"control"}]` → `[{"open":"handshake","type":"control"},{"open":"settings","type":"qpack_encoder"}]`
- `kinds.async_script.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.async_script.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.async_script.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.async_script.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.async_script.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.async_script.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.async_script.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.async_script.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.async_script.variants[1].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.async_script.variants[1].headers[0].value`: (none) → `"*/*"`
- `kinds.async_script.variants[1].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.async_script.variants[1].headers[1].value`: `"*/*"` → (none)
- `kinds.async_script.variants[1].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.async_script.variants[1].http1[1].value`: (none) → `"*/*"`
- `kinds.async_script.variants[1].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.async_script.variants[1].http1[2].value`: `"*/*"` → (none)
- `kinds.async_script.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.async_script.variants[2].headers[0].value`: (none) → `"*/*"`
- `kinds.async_script.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.async_script.variants[2].headers[1].value`: `"*/*"` → (none)
- `kinds.async_script.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.async_script.variants[2].http1[1].value`: (none) → `"*/*"`
- `kinds.async_script.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.async_script.variants[2].http1[2].value`: `"*/*"` → (none)
- `kinds.beacon.headers[2].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.beacon.headers[3].name`: `"sec-fetch-site"` → `"origin"`
- `kinds.beacon.http1[3].name`: `"Origin"` → `"Sec-Fetch-Site"`
- `kinds.beacon.http1[4].name`: `"Sec-Fetch-Site"` → `"Origin"`
- `kinds.beacon.variants`: `[{"corsChecked":true,"headers":[{"name":"accept","value":"*/*"},{"name":"content-type"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"content-length"},{"name":"sec-fetch-mode","value":"no-cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/6…` → `[{"corsChecked":true,"http1":[{"name":"Host"},{"name":"Accept","value":"*/*"},{"name":"Content-Type"},{"name":"Origin"},{"name":"User-Agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"Ref…`
- `kinds.defer_script.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.defer_script.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.defer_script.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.defer_script.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.defer_script.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.defer_script.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.defer_script.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.defer_script.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.defer_script.variants[1].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.defer_script.variants[1].headers[0].value`: (none) → `"*/*"`
- `kinds.defer_script.variants[1].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.defer_script.variants[1].headers[1].value`: `"*/*"` → (none)
- `kinds.defer_script.variants[1].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.defer_script.variants[1].http1[1].value`: (none) → `"*/*"`
- `kinds.defer_script.variants[1].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.defer_script.variants[1].http1[2].value`: `"*/*"` → (none)
- `kinds.defer_script.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.defer_script.variants[2].headers[0].value`: (none) → `"*/*"`
- `kinds.defer_script.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.defer_script.variants[2].headers[1].value`: `"*/*"` → (none)
- `kinds.defer_script.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.defer_script.variants[2].http1[1].value`: (none) → `"*/*"`
- `kinds.defer_script.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.defer_script.variants[2].http1[2].value`: `"*/*"` → (none)
- `kinds.fetch.variants[0].headers[2].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.fetch.variants[0].headers[3].name`: `"sec-fetch-site"` → `"origin"`
- `kinds.fetch.variants[0].http1[3].name`: `"Origin"` → `"Sec-Fetch-Site"`
- `kinds.fetch.variants[0].http1[4].name`: `"Sec-Fetch-Site"` → `"Origin"`
- `kinds.fetch.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.fetch.variants[2].headers[0].value`: (none) → `"*/*"`
- `kinds.fetch.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.fetch.variants[2].headers[1].value`: `"*/*"` → (none)
- `kinds.fetch.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.fetch.variants[2].http1[1].value`: (none) → `"*/*"`
- `kinds.fetch.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.fetch.variants[2].http1[2].value`: `"*/*"` → (none)
- `kinds.fetch.variants[3].crossOriginRedirect`: (none) → `true`
- `kinds.fetch.variants[3].headers`: `[{"name":"accept","value":"*/*"},{"name":"content-type"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"sec-fetch-mode","value":"cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safar…` → `[{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"accept","value":"*/*"},{"name":"referer"},{"name":"origin"},{"name":"accept-encoding","value":"gzip, deflate, br, zstd"},{…`
- `kinds.fetch.variants[3].http1`: `[{"name":"Host"},{"name":"Accept","value":"*/*"},{"name":"Content-Type"},{"name":"Sec-Fetch-Site"},{"name":"Origin"},{"name":"Sec-Fetch-Mode","value":"cors"},{"name":"User-Agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mob…` → `[{"name":"Host"},{"name":"User-Agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Origin"},{"name":"Accept-Encoding","value":"gzip, defla…`
- `kinds.fetch.variants[3].method`: `"POST"` → (none)
- `kinds.fetch.variants[3].redirected`: (none) → `true`
- `kinds.fetch.variants[4].crossOriginRedirect`: `true` → (none)
- `kinds.fetch.variants[4].headers`: `[{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"accept","value":"*/*"},{"name":"referer"},{"name":"origin"},{"name":"accept-encoding","value":"gzip, deflate, br, zstd"},{…` → `[{"name":"accept","value":"*/*"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"sec-fetch-mode","value":"cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"refer…`
- `kinds.fetch.variants[4].http1`: `[{"name":"Host"},{"name":"User-Agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"Accept","value":"*/*"},{"name":"Referer"},{"name":"Origin"},{"name":"Accept-Encoding","value":"gzip, defla…` → `[{"name":"Host"},{"name":"Accept","value":"*/*"},{"name":"Sec-Fetch-Site"},{"name":"Origin"},{"name":"Sec-Fetch-Mode","value":"cors"},{"name":"User-Agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"…`
- `kinds.fetch.variants[4].redirected`: `true` → (none)
- `kinds.fetch.variants[4].site`: `"cross-site"` → `"same-site"`
- `kinds.fetch.variants[5].headers`: `[{"name":"sec-fetch-site"},{"name":"accept","value":"*/*"},{"name":"origin"},{"name":"sec-fetch-mode","value":"cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"refer…` → `[{"name":"accept","value":"*/*"},{"name":"content-type"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"sec-fetch-mode","value":"cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safar…`
- `kinds.fetch.variants[5].http1`: `[{"name":"Host"},{"name":"Sec-Fetch-Site"},{"name":"Accept","value":"*/*"},{"name":"Origin"},{"name":"Sec-Fetch-Mode","value":"cors"},{"name":"User-Agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"…` → `[{"name":"Host"},{"name":"Accept","value":"*/*"},{"name":"Content-Type"},{"name":"Sec-Fetch-Site"},{"name":"Origin"},{"name":"Sec-Fetch-Mode","value":"cors"},{"name":"User-Agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mob…`
- `kinds.fetch.variants[5].method`: (none) → `"POST"`
- `kinds.fetch.variants[6].site`: `"same-site"` → `"cross-site"`
- `kinds.fetch.variants[7].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.fetch.variants[7].headers[0].value`: (none) → `"*/*"`
- `kinds.fetch.variants[7].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.fetch.variants[7].headers[1].value`: `"*/*"` → (none)
- `kinds.fetch.variants[9].headers[2].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.fetch.variants[9].headers[3].name`: `"sec-fetch-site"` → `"origin"`
- `kinds.fetch.variants[12].headers[2].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.fetch.variants[12].headers[3].name`: `"sec-fetch-site"` → `"origin"`
- `kinds.fetch.variants[18].headers[2].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.fetch.variants[18].headers[3].name`: `"sec-fetch-site"` → `"origin"`
- `kinds.fetch.variants[20].headers`: `[{"name":"accept","value":"*/*"},{"name":"origin"},{"name":"sec-fetch-site"},{"name":"sec-fetch-mode","value":"cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"refer…` → `[{"name":"accept","value":"*/*"},{"name":"content-type"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"sec-fetch-mode","value":"cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safar…`
- `kinds.fetch.variants[20].method`: (none) → `"POST"`
- `kinds.fetch.variants[20].site`: `"same-site"` → `"cross-site"`
- `kinds.fetch.variants[21].method`: `"POST"` → `"PATCH"`
- `kinds.fetch.variants[21].originBeforeRedirect`: `true` → (none)
- `kinds.fetch.variants[21].redirected`: `true` → (none)
- `kinds.fetch.variants[22].originBeforeRedirect`: (none) → `true`
- `kinds.fetch.variants[22].redirected`: (none) → `true`
- `kinds.fetch.variants[23].headers`: `[{"name":"accept","value":"*/*"},{"name":"content-type"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"sec-fetch-mode","value":"cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safar…` → `[{"name":"accept","value":"*/*"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"sec-fetch-mode","value":"cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1"},{"name":"refer…`
- `kinds.fetch.variants[23].method`: `"PATCH"` → (none)
- `kinds.fetch.variants[23].methodChangedByRedirect`: (none) → `true`
- `kinds.fetch.variants[24].methodChangedByRedirect`: `true` → (none)
- `kinds.fetch.variants[24].site`: `"cross-site"` → `"same-site"`
- `kinds.font.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.font.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.font.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.font.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.font.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.font.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.font.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.font.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.image.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.image.variants[0].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.image.variants[0].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.image.variants[0].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.image.variants[0].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[1].headers[2].name`: `"accept-language"` → `"priority"`
- `kinds.image.variants[1].headers[2].value`: `"en-US,en;q=0.9"` → `"u=5, i"`
- `kinds.image.variants[1].headers[5].name`: `"referer"` → `"accept-language"`
- `kinds.image.variants[1].headers[5].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.image.variants[1].headers[7].name`: `"priority"` → `"referer"`
- `kinds.image.variants[1].headers[7].value`: `"u=5, i"` → (none)
- `kinds.image.variants[1].http1[3].name`: `"Accept-Language"` → `"Priority"`
- `kinds.image.variants[1].http1[3].value`: `"en-US,en;q=0.9"` → `"u=5, i"`
- `kinds.image.variants[1].http1[6].name`: `"Referer"` → `"Accept-Language"`
- `kinds.image.variants[1].http1[6].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.image.variants[1].http1[8].name`: `"Priority"` → `"Referer"`
- `kinds.image.variants[1].http1[8].value`: `"u=5, i"` → (none)
- `kinds.image.variants[2].corsChecked`: `true` → (none)
- `kinds.image.variants[2].crossOriginRedirect`: (none) → `true`
- `kinds.image.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.image.variants[2].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.image.variants[2].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[2].headers[2].name`: `"origin"` → `"priority"`
- `kinds.image.variants[2].headers[2].value`: (none) → `"u=5, i"`
- `kinds.image.variants[2].headers[5].name`: `"referer"` → `"accept-language"`
- `kinds.image.variants[2].headers[5].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.image.variants[2].headers[7].name`: `"accept-language"` → `"referer"`
- `kinds.image.variants[2].headers[7].value`: `"en-US,en;q=0.9"` → (none)
- `kinds.image.variants[2].headers[8].name`: `"priority"` → `"accept-encoding"`
- `kinds.image.variants[2].headers[8].value`: `"u=5, i"` → `"gzip, deflate, br, zstd"`
- `kinds.image.variants[2].headers[9].name`: `"accept-encoding"` → `"cookie"`
- `kinds.image.variants[2].headers[9].value`: `"gzip, deflate, br, zstd"` → (none)
- `kinds.image.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.image.variants[2].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.image.variants[2].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[2].http1[3].name`: `"Origin"` → `"Priority"`
- `kinds.image.variants[2].http1[3].value`: (none) → `"u=5, i"`
- `kinds.image.variants[2].http1[6].name`: `"Referer"` → `"Accept-Language"`
- `kinds.image.variants[2].http1[6].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.image.variants[2].http1[8].name`: `"Accept-Language"` → `"Referer"`
- `kinds.image.variants[2].http1[8].value`: `"en-US,en;q=0.9"` → (none)
- `kinds.image.variants[2].http1[9].name`: `"Priority"` → `"Accept-Encoding"`
- `kinds.image.variants[2].http1[9].value`: `"u=5, i"` → `"gzip, deflate, br, zstd"`
- `kinds.image.variants[2].http1[10].name`: `"Accept-Encoding"` → `"Cookie"`
- `kinds.image.variants[2].http1[10].value`: `"gzip, deflate, br, zstd"` → (none)
- `kinds.image.variants[2].redirected`: (none) → `true`
- `kinds.image.variants[3].corsChecked`: (none) → `true`
- `kinds.image.variants[3].crossOriginRedirect`: `true` → (none)
- `kinds.image.variants[3].headers[2].name`: `"priority"` → `"origin"`
- `kinds.image.variants[3].headers[2].value`: `"u=5, i"` → (none)
- `kinds.image.variants[3].headers[5].name`: `"accept-language"` → `"referer"`
- `kinds.image.variants[3].headers[5].value`: `"en-US,en;q=0.9"` → (none)
- `kinds.image.variants[3].headers[7].name`: `"referer"` → `"accept-language"`
- `kinds.image.variants[3].headers[7].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.image.variants[3].headers[8].name`: `"accept-encoding"` → `"priority"`
- `kinds.image.variants[3].headers[8].value`: `"gzip, deflate, br, zstd"` → `"u=5, i"`
- `kinds.image.variants[3].headers[9].name`: `"cookie"` → `"accept-encoding"`
- `kinds.image.variants[3].headers[9].value`: (none) → `"gzip, deflate, br, zstd"`
- `kinds.image.variants[3].http1[3].name`: `"Priority"` → `"Origin"`
- `kinds.image.variants[3].http1[3].value`: `"u=5, i"` → (none)
- `kinds.image.variants[3].http1[6].name`: `"Accept-Language"` → `"Referer"`
- `kinds.image.variants[3].http1[6].value`: `"en-US,en;q=0.9"` → (none)
- `kinds.image.variants[3].http1[8].name`: `"Referer"` → `"Accept-Language"`
- `kinds.image.variants[3].http1[8].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.image.variants[3].http1[9].name`: `"Accept-Encoding"` → `"Priority"`
- `kinds.image.variants[3].http1[9].value`: `"gzip, deflate, br, zstd"` → `"u=5, i"`
- `kinds.image.variants[3].http1[10].name`: `"Cookie"` → `"Accept-Encoding"`
- `kinds.image.variants[3].http1[10].value`: (none) → `"gzip, deflate, br, zstd"`
- `kinds.image.variants[3].redirected`: `true` → (none)
- `kinds.image.variants[3].site`: `"cross-site"` → `"same-site"`
- `kinds.image.variants[4].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.image.variants[4].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[4].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.image.variants[4].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[4].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.image.variants[4].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[4].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.image.variants[4].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[4].site`: `"same-site"` → `"cross-site"`
- `kinds.image.variants[5].headers[2].name`: `"accept-language"` → `"priority"`
- `kinds.image.variants[5].headers[2].value`: `"en-US,en;q=0.9"` → `"u=5, i"`
- `kinds.image.variants[5].headers[5].name`: `"referer"` → `"accept-language"`
- `kinds.image.variants[5].headers[5].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.image.variants[5].headers[7].name`: `"priority"` → `"referer"`
- `kinds.image.variants[5].headers[7].value`: `"u=5, i"` → (none)
- `kinds.image.variants[7].headers[2].name`: `"accept-language"` → `"priority"`
- `kinds.image.variants[7].headers[2].value`: `"en-US,en;q=0.9"` → `"u=5, i"`
- `kinds.image.variants[7].headers[5].name`: `"referer"` → `"accept-language"`
- `kinds.image.variants[7].headers[5].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.image.variants[7].headers[7].name`: `"priority"` → `"referer"`
- `kinds.image.variants[7].headers[7].value`: `"u=5, i"` → (none)
- `kinds.injected_image.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.injected_image.variants[0].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.injected_image.variants[0].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.injected_image.variants[0].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.injected_image.variants[0].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[1].headers[2].name`: `"accept-language"` → `"priority"`
- `kinds.injected_image.variants[1].headers[2].value`: `"en-US,en;q=0.9"` → `"u=5, i"`
- `kinds.injected_image.variants[1].headers[5].name`: `"referer"` → `"accept-language"`
- `kinds.injected_image.variants[1].headers[5].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.injected_image.variants[1].headers[7].name`: `"priority"` → `"referer"`
- `kinds.injected_image.variants[1].headers[7].value`: `"u=5, i"` → (none)
- `kinds.injected_image.variants[1].http1[3].name`: `"Accept-Language"` → `"Priority"`
- `kinds.injected_image.variants[1].http1[3].value`: `"en-US,en;q=0.9"` → `"u=5, i"`
- `kinds.injected_image.variants[1].http1[6].name`: `"Referer"` → `"Accept-Language"`
- `kinds.injected_image.variants[1].http1[6].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.injected_image.variants[1].http1[8].name`: `"Priority"` → `"Referer"`
- `kinds.injected_image.variants[1].http1[8].value`: `"u=5, i"` → (none)
- `kinds.injected_image.variants[2].corsChecked`: `true` → (none)
- `kinds.injected_image.variants[2].crossOriginRedirect`: (none) → `true`
- `kinds.injected_image.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.injected_image.variants[2].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.injected_image.variants[2].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[2].headers[2].name`: `"origin"` → `"priority"`
- `kinds.injected_image.variants[2].headers[2].value`: (none) → `"u=5, i"`
- `kinds.injected_image.variants[2].headers[5].name`: `"referer"` → `"accept-language"`
- `kinds.injected_image.variants[2].headers[5].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.injected_image.variants[2].headers[7].name`: `"accept-language"` → `"referer"`
- `kinds.injected_image.variants[2].headers[7].value`: `"en-US,en;q=0.9"` → (none)
- `kinds.injected_image.variants[2].headers[8].name`: `"priority"` → `"accept-encoding"`
- `kinds.injected_image.variants[2].headers[8].value`: `"u=5, i"` → `"gzip, deflate, br, zstd"`
- `kinds.injected_image.variants[2].headers[9].name`: `"accept-encoding"` → `"cookie"`
- `kinds.injected_image.variants[2].headers[9].value`: `"gzip, deflate, br, zstd"` → (none)
- `kinds.injected_image.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.injected_image.variants[2].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.injected_image.variants[2].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[2].http1[3].name`: `"Origin"` → `"Priority"`
- `kinds.injected_image.variants[2].http1[3].value`: (none) → `"u=5, i"`
- `kinds.injected_image.variants[2].http1[6].name`: `"Referer"` → `"Accept-Language"`
- `kinds.injected_image.variants[2].http1[6].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.injected_image.variants[2].http1[8].name`: `"Accept-Language"` → `"Referer"`
- `kinds.injected_image.variants[2].http1[8].value`: `"en-US,en;q=0.9"` → (none)
- `kinds.injected_image.variants[2].http1[9].name`: `"Priority"` → `"Accept-Encoding"`
- `kinds.injected_image.variants[2].http1[9].value`: `"u=5, i"` → `"gzip, deflate, br, zstd"`
- `kinds.injected_image.variants[2].http1[10].name`: `"Accept-Encoding"` → `"Cookie"`
- `kinds.injected_image.variants[2].http1[10].value`: `"gzip, deflate, br, zstd"` → (none)
- `kinds.injected_image.variants[2].redirected`: (none) → `true`
- `kinds.injected_image.variants[3].corsChecked`: (none) → `true`
- `kinds.injected_image.variants[3].crossOriginRedirect`: `true` → (none)
- `kinds.injected_image.variants[3].headers[2].name`: `"priority"` → `"origin"`
- `kinds.injected_image.variants[3].headers[2].value`: `"u=5, i"` → (none)
- `kinds.injected_image.variants[3].headers[5].name`: `"accept-language"` → `"referer"`
- `kinds.injected_image.variants[3].headers[5].value`: `"en-US,en;q=0.9"` → (none)
- `kinds.injected_image.variants[3].headers[7].name`: `"referer"` → `"accept-language"`
- `kinds.injected_image.variants[3].headers[7].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.injected_image.variants[3].headers[8].name`: `"accept-encoding"` → `"priority"`
- `kinds.injected_image.variants[3].headers[8].value`: `"gzip, deflate, br, zstd"` → `"u=5, i"`
- `kinds.injected_image.variants[3].headers[9].name`: `"cookie"` → `"accept-encoding"`
- `kinds.injected_image.variants[3].headers[9].value`: (none) → `"gzip, deflate, br, zstd"`
- `kinds.injected_image.variants[3].http1[3].name`: `"Priority"` → `"Origin"`
- `kinds.injected_image.variants[3].http1[3].value`: `"u=5, i"` → (none)
- `kinds.injected_image.variants[3].http1[6].name`: `"Accept-Language"` → `"Referer"`
- `kinds.injected_image.variants[3].http1[6].value`: `"en-US,en;q=0.9"` → (none)
- `kinds.injected_image.variants[3].http1[8].name`: `"Referer"` → `"Accept-Language"`
- `kinds.injected_image.variants[3].http1[8].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.injected_image.variants[3].http1[9].name`: `"Accept-Encoding"` → `"Priority"`
- `kinds.injected_image.variants[3].http1[9].value`: `"gzip, deflate, br, zstd"` → `"u=5, i"`
- `kinds.injected_image.variants[3].http1[10].name`: `"Cookie"` → `"Accept-Encoding"`
- `kinds.injected_image.variants[3].http1[10].value`: (none) → `"gzip, deflate, br, zstd"`
- `kinds.injected_image.variants[3].redirected`: `true` → (none)
- `kinds.injected_image.variants[3].site`: `"cross-site"` → `"same-site"`
- `kinds.injected_image.variants[4].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.injected_image.variants[4].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[4].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.injected_image.variants[4].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[4].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.injected_image.variants[4].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[4].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.injected_image.variants[4].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[4].site`: `"same-site"` → `"cross-site"`
- `kinds.injected_image.variants[5].headers[2].name`: `"accept-language"` → `"priority"`
- `kinds.injected_image.variants[5].headers[2].value`: `"en-US,en;q=0.9"` → `"u=5, i"`
- `kinds.injected_image.variants[5].headers[5].name`: `"referer"` → `"accept-language"`
- `kinds.injected_image.variants[5].headers[5].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.injected_image.variants[5].headers[7].name`: `"priority"` → `"referer"`
- `kinds.injected_image.variants[5].headers[7].value`: `"u=5, i"` → (none)
- `kinds.injected_image.variants[7].headers[2].name`: `"accept-language"` → `"priority"`
- `kinds.injected_image.variants[7].headers[2].value`: `"en-US,en;q=0.9"` → `"u=5, i"`
- `kinds.injected_image.variants[7].headers[5].name`: `"referer"` → `"accept-language"`
- `kinds.injected_image.variants[7].headers[5].value`: (none) → `"en-US,en;q=0.9"`
- `kinds.injected_image.variants[7].headers[7].name`: `"priority"` → `"referer"`
- `kinds.injected_image.variants[7].headers[7].value`: `"u=5, i"` → (none)
- `kinds.injected_script.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.injected_script.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.injected_script.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.injected_script.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.injected_script.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.injected_script.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.injected_script.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.injected_script.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.injected_script.variants[1].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.injected_script.variants[1].headers[0].value`: (none) → `"*/*"`
- `kinds.injected_script.variants[1].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.injected_script.variants[1].headers[1].value`: `"*/*"` → (none)
- `kinds.injected_script.variants[1].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.injected_script.variants[1].http1[1].value`: (none) → `"*/*"`
- `kinds.injected_script.variants[1].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.injected_script.variants[1].http1[2].value`: `"*/*"` → (none)
- `kinds.injected_script.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.injected_script.variants[2].headers[0].value`: (none) → `"*/*"`
- `kinds.injected_script.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.injected_script.variants[2].headers[1].value`: `"*/*"` → (none)
- `kinds.injected_script.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.injected_script.variants[2].http1[1].value`: (none) → `"*/*"`
- `kinds.injected_script.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.injected_script.variants[2].http1[2].value`: `"*/*"` → (none)
- `kinds.preload_font.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.preload_font.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.preload_font.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.preload_font.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.preload_font.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.preload_font.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.preload_font.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.preload_font.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.preload_script.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.preload_script.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.preload_script.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.preload_script.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.preload_script.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.preload_script.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.preload_script.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.preload_script.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.preload_script.variants[1].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.preload_script.variants[1].headers[0].value`: (none) → `"*/*"`
- `kinds.preload_script.variants[1].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.preload_script.variants[1].headers[1].value`: `"*/*"` → (none)
- `kinds.preload_script.variants[1].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.preload_script.variants[1].http1[1].value`: (none) → `"*/*"`
- `kinds.preload_script.variants[1].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.preload_script.variants[1].http1[2].value`: `"*/*"` → (none)
- `kinds.preload_script.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.preload_script.variants[2].headers[0].value`: (none) → `"*/*"`
- `kinds.preload_script.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.preload_script.variants[2].headers[1].value`: `"*/*"` → (none)
- `kinds.preload_script.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.preload_script.variants[2].http1[1].value`: (none) → `"*/*"`
- `kinds.preload_script.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.preload_script.variants[2].http1[2].value`: `"*/*"` → (none)
- `kinds.script.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.script.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.script.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.script.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.script.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.script.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.script.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.script.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.script.variants[1].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.script.variants[1].headers[0].value`: (none) → `"*/*"`
- `kinds.script.variants[1].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.script.variants[1].headers[1].value`: `"*/*"` → (none)
- `kinds.script.variants[1].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.script.variants[1].http1[1].value`: (none) → `"*/*"`
- `kinds.script.variants[1].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.script.variants[1].http1[2].value`: `"*/*"` → (none)
- `kinds.script.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.script.variants[2].headers[0].value`: (none) → `"*/*"`
- `kinds.script.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.script.variants[2].headers[1].value`: `"*/*"` → (none)
- `kinds.script.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.script.variants[2].http1[1].value`: (none) → `"*/*"`
- `kinds.script.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.script.variants[2].http1[2].value`: `"*/*"` → (none)
- `kinds.xhr.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.xhr.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.xhr.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.xhr.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.xhr.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.xhr.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.xhr.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.xhr.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.xhr.variants[0].site`: `"cross-site"` → `"same-site"`
- `kinds.xhr.variants[1].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.xhr.variants[1].headers[0].value`: (none) → `"*/*"`
- `kinds.xhr.variants[1].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.xhr.variants[1].headers[1].value`: `"*/*"` → (none)
- `kinds.xhr.variants[1].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.xhr.variants[1].http1[1].value`: (none) → `"*/*"`
- `kinds.xhr.variants[1].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.xhr.variants[1].http1[2].value`: `"*/*"` → (none)
- `kinds.xhr.variants[1].site`: `"same-site"` → `"cross-site"`
- `kinds.xhr.variants[6].headers[2].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.xhr.variants[6].headers[3].name`: `"sec-fetch-site"` → `"origin"`
- `quic.ackDelay`: `"constant"` → `"actual"`
- `quic.ackDelayConstant`: `{"max":29,"min":27}` → (none)
- `quic.loss.abandonClose`: `"zero_rtt"` → (none)
- `quic.loss.pathDegrading`: `{"action":"close","afterMs":12000,"closeReason":"keepalive limit reached"}` → (none)
- `quic.spinBit`: `"spin"` → `"random"`
- `quic.streamsBlocked`: `{"atRequest":"first_request"}` → (none)
- `quic.tokens`: (none) → `true`
- `quic.transportParameters`: `[{"id":"0x01","value":300000},{"id":"0x04","value":16777216},{"id":"0x05","value":2097152},{"id":"0x06","value":2097152},{"id":"0x07","value":2097152},{"id":"0x09","value":8},{"id":"0x0e","value":64},{"id":"0x0f"}]` → `[{"id":"0x01","value":300000},{"id":"0x04","value":16777216},{"id":"0x05","value":2097152},{"id":"0x06","value":2097152},{"id":"0x07","value":2097152},{"id":"0x09","value":8},{"id":"0x0e","value":64},{"id":"0x0f"},{"data":"09","id":"0xff080808","last":true}]`
- `quic.zeroRtt`: `true` → (none)
- `variants`: `[{"corsChecked":true,"headers":[{"name":"accept","value":"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},{"name":"content-type"},{"name":"origin"},{"name":"sec-fetch-site"},{"name":"sec-fetch-mode","value":"navigate"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone O…` → `[{"corsChecked":true,"headers":[{"name":"accept","value":"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},{"name":"content-type"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"sec-fetch-mode","value":"navigate"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone O…`

### Hand-written, copied from the previous profile

- `proxy.connectCredentials`: `"on_challenge"` → (none)
- `proxy.forwardCredentials`: `"always"` → (none)
- `proxy.websocket.credentials`: `true` → (none)

## Deprecation

No safari profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests: not run.

realcheck: not run.

## To do by hand

- Review the changes above; a hand-written section copied from the previous profile is unverified until the e2e tests pass against its inherited or new captures.
- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and a line in its Not covered by any claim for any new gap pinned.
- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `capturesFrom`; a scenario recaptured for this version needs its own entry if the oddity recurs.
