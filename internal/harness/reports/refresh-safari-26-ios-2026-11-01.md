# Refresh of safari-26-ios, 2026-11-01

safari 26.7.1 on the iPhone, served from this Mac, ios set: new profile, copied from safari-26-macos, inheriting what it does not capture from macos safari's captures, whose network stack it shares (decision 0051). Made by `go run ./cmd/refresh` (staging `.refresh/ios26-20261101`).

## Captures

Every capture names safari-26-ios with one user agent, and the browser's version was 26.7.1 before and after the run.

User agent: `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1`

| Scenario | Samples | Driver | Measured | Estimated |
|---|---|---|---|---|
| page | 3 | devicectl | 3s | 0s |
| kinds | 3 | devicectl | 10s | 0s |
| kinds-http1 | 3 | devicectl | 10s | 0s |
| kinds-plain | 3 | devicectl | 11s | 0s |
| cookies | 3 | devicectl | 3s | 0s |
| cors | 3 | devicectl | 20s | 0s |
| cors-edge | 3 | devicectl | 3s | 0s |
| cors-edge-cookies | 3 | devicectl | 3s | 0s |
| redirects2 | 3 | devicectl | 30s | 0s |
| redirects2-cookies | 3 | devicectl | 41s | 0s |
| headers-order | 3 | devicectl | 7s | 0s |
| http1 | 3 | devicectl | 3s | 0s |
| h3-discovery | 3 | devicectl | 2m34s | 0s |
| h3-page | 3 | devicectl | 27s | 0s |
| resume | 3 | devicectl | 11s | 0s |
| websocket | 3 | devicectl | 41s | 0s |
| websocket-h2 | 3 | devicectl | 38s | 0s |
| h3-blackhole | 3 | devicectl | 2m33s | 0s |
| h3-broken | 3 | devicectl | 3m40s | 0s |
| proxy-connect | 3 | devicectl | 3s | 0s |
| proxy-plain | 3 | devicectl | 2s | 0s |
| total | | | 13m24s | 0s |

## Inherited scenarios

Checked against these profiles' captures, version tokens rewritten (`capturesFrom`, decision 0035):

- cookies-edge: safari-26-macos
- nav-redirect-cs: safari-26-macos
- nav-redirect-cs-cookies: safari-26-macos
- nav-redirect-so: safari-26-macos
- nav-redirect-so-cookies: safari-26-macos
- nav-redirect-ss: safari-26-macos
- nav-redirect-ss-cookies: safari-26-macos
- set-cookie: safari-26-macos
- slow-idle: safari-26-macos

## Changes from safari-26-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.2 Safari/605.1.15` → `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1`
- accept-encoding: `gzip, deflate, br` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `capturesFrom`: (none) → `{"cookies-edge":"safari-26-macos","nav-redirect-cs":"safari-26-macos","nav-redirect-cs-cookies":"safari-26-macos","nav-redirect-so":"safari-26-macos","nav-redirect-so-cookies":"safari-26-macos","nav-redirect-ss":"safari-26-macos","nav-redirect-ss-cookies":"safari-26-macos","set-cookie":"safari-26-ma…`
- `name`: `"safari-26-macos"` → `"safari-26-ios"`
- `platform`: `"macos"` → `"ios"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

No change.

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic; h3gen: quic, http3; kindsgen: kinds, context, variants, http3 headers and priorities)

- `http3.discovery.dialDelayMs`: `[3,4,4,4,5,5,5,5,6,6,6,6,7,7,8,8,8,8,8,9,10,10,10,17,18,25,28]` → (none)
- `http3.discovery.resumedDialDelayMs`: `[6,6,6,6,6,6,6,6,7,7,7,7,7,7,7,7,7,8,8,8,8,8,8,8,8,8,8,8,9,9,9,9,9,9,9,9,9,9,9,9,10,10,10,10,10,10,10,10,11,11,23]` → (none)
- `http3.earlyData`: `{"methods":["GET","HEAD","OPTIONS"],"retryTooEarly":true}` → (none)
- `http3.earlyStreamsWithRequest`: `true` → (none)
- `http3.requestsAfterConfirmed`: `true` → (none)
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
- `kinds.async_script.variants[4].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.async_script.variants[4].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.async_script.variants[4].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.async_script.variants[4].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.async_script.variants[5].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.async_script.variants[5].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.async_script.variants[5].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.async_script.variants[5].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.async_script.variants[7].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.async_script.variants[7].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.async_script.variants[7].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.async_script.variants[7].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.beacon.headers[2].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.beacon.headers[3].name`: `"sec-fetch-site"` → `"origin"`
- `kinds.beacon.http1[3].name`: `"Origin"` → `"Sec-Fetch-Site"`
- `kinds.beacon.http1[4].name`: `"Sec-Fetch-Site"` → `"Origin"`
- `kinds.beacon.variants`: `[{"corsChecked":true,"headers":[{"name":"accept","value":"*/*"},{"name":"content-type"},{"name":"sec-fetch-site"},{"name":"origin"},{"name":"content-length"},{"name":"sec-fetch-mode","value":"no-cors"},{"name":"user-agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/6…` → `[{"corsChecked":true,"http1":[{"name":"Host"},{"name":"Accept","value":"*/*"},{"name":"Content-Type"},{"name":"Origin"},{"name":"User-Agent","value":"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"},{"name":"R…`
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
- `kinds.defer_script.variants[4].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.defer_script.variants[4].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.defer_script.variants[4].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.defer_script.variants[4].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.defer_script.variants[5].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.defer_script.variants[5].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.defer_script.variants[5].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.defer_script.variants[5].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.defer_script.variants[7].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.defer_script.variants[7].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.defer_script.variants[7].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.defer_script.variants[7].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
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
- `kinds.fetch.variants[5].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.fetch.variants[5].headers[0].value`: (none) → `"*/*"`
- `kinds.fetch.variants[5].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.fetch.variants[5].headers[1].value`: `"*/*"` → (none)
- `kinds.fetch.variants[5].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.fetch.variants[5].http1[1].value`: (none) → `"*/*"`
- `kinds.fetch.variants[5].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.fetch.variants[5].http1[2].value`: `"*/*"` → (none)
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
- `kinds.fetch.variants[20].headers[1].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.fetch.variants[20].headers[2].name`: `"sec-fetch-site"` → `"origin"`
- `kinds.fetch.variants[26].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.fetch.variants[26].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.fetch.variants[26].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.fetch.variants[26].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.fetch.variants[29].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.fetch.variants[29].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.fetch.variants[29].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.fetch.variants[29].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.fetch.variants[32].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.fetch.variants[32].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.fetch.variants[32].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.fetch.variants[32].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.font.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.font.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.font.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.font.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.font.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.font.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.font.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.font.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.font.variants[2].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.font.variants[2].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.font.variants[2].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.font.variants[2].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.font.variants[3].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.font.variants[3].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.font.variants[3].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.font.variants[3].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.font.variants[4].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.font.variants[4].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.font.variants[4].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.font.variants[4].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.frame.variants[0].http1[2].name`: `"Accept"` → `"Upgrade-Insecure-Requests"`
- `kinds.frame.variants[0].http1[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"` → `"1"`
- `kinds.frame.variants[0].http1[3].name`: `"Upgrade-Insecure-Requests"` → `"Accept"`
- `kinds.frame.variants[0].http1[3].value`: `"1"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"`
- `kinds.frame.variants[1].http1[2].name`: `"Accept"` → `"Upgrade-Insecure-Requests"`
- `kinds.frame.variants[1].http1[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"` → `"1"`
- `kinds.frame.variants[1].http1[3].name`: `"Upgrade-Insecure-Requests"` → `"Accept"`
- `kinds.frame.variants[1].http1[3].value`: `"1"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"`
- `kinds.frame.variants[2].http1[2].name`: `"Accept"` → `"Upgrade-Insecure-Requests"`
- `kinds.frame.variants[2].http1[2].value`: `"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"` → `"1"`
- `kinds.frame.variants[2].http1[3].name`: `"Upgrade-Insecure-Requests"` → `"Accept"`
- `kinds.frame.variants[2].http1[3].value`: `"1"` → `"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"`
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
- `kinds.image.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.image.variants[2].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.image.variants[2].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.image.variants[2].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.image.variants[2].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[4].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.image.variants[4].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[4].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.image.variants[4].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.image.variants[4].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.image.variants[4].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[4].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.image.variants[4].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
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
- `kinds.image.variants[9].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.image.variants[9].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.image.variants[9].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.image.variants[9].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[10].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.image.variants[10].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.image.variants[10].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.image.variants[10].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.image.variants[12].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.image.variants[12].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.image.variants[12].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.image.variants[12].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
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
- `kinds.injected_image.variants[2].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.injected_image.variants[2].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[2].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.injected_image.variants[2].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[2].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.injected_image.variants[2].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[2].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.injected_image.variants[2].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[4].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.injected_image.variants[4].headers[0].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[4].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.injected_image.variants[4].headers[1].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
- `kinds.injected_image.variants[4].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.injected_image.variants[4].http1[1].value`: (none) → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[4].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.injected_image.variants[4].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → (none)
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
- `kinds.injected_image.variants[9].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.injected_image.variants[9].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.injected_image.variants[9].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.injected_image.variants[9].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[10].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.injected_image.variants[10].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.injected_image.variants[10].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.injected_image.variants[10].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
- `kinds.injected_image.variants[12].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.injected_image.variants[12].http1[2].value`: `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.injected_image.variants[12].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.injected_image.variants[12].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"image/webp,image/avif,image/jxl,image/heic,image/heic-sequence,video/*;q=0.8,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"`
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
- `kinds.injected_script.variants[4].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.injected_script.variants[4].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.injected_script.variants[4].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.injected_script.variants[4].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.injected_script.variants[5].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.injected_script.variants[5].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.injected_script.variants[5].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.injected_script.variants[5].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.injected_script.variants[7].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.injected_script.variants[7].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.injected_script.variants[7].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.injected_script.variants[7].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.preload_font.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.preload_font.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.preload_font.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.preload_font.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.preload_font.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.preload_font.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.preload_font.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.preload_font.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.preload_font.variants[2].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.preload_font.variants[2].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.preload_font.variants[2].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.preload_font.variants[2].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.preload_font.variants[3].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.preload_font.variants[3].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.preload_font.variants[3].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.preload_font.variants[3].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.preload_font.variants[4].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.preload_font.variants[4].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.preload_font.variants[4].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.preload_font.variants[4].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
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
- `kinds.preload_script.variants[4].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.preload_script.variants[4].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.preload_script.variants[4].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.preload_script.variants[4].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.preload_script.variants[5].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.preload_script.variants[5].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.preload_script.variants[5].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.preload_script.variants[5].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.preload_script.variants[7].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.preload_script.variants[7].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.preload_script.variants[7].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.preload_script.variants[7].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
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
- `kinds.script.variants[4].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.script.variants[4].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.script.variants[4].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.script.variants[4].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.script.variants[5].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.script.variants[5].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.script.variants[5].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.script.variants[5].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.script.variants[7].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.script.variants[7].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.script.variants[7].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.script.variants[7].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.style.variants[0].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.style.variants[0].http1[2].value`: `"text/css,*/*;q=0.1"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.style.variants[0].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.style.variants[0].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"text/css,*/*;q=0.1"`
- `kinds.style.variants[1].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.style.variants[1].http1[2].value`: `"text/css,*/*;q=0.1"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.style.variants[1].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.style.variants[1].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"text/css,*/*;q=0.1"`
- `kinds.style.variants[2].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.style.variants[2].http1[2].value`: `"text/css,*/*;q=0.1"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.style.variants[2].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.style.variants[2].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"text/css,*/*;q=0.1"`
- `kinds.xhr.variants[0].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.xhr.variants[0].headers[0].value`: (none) → `"*/*"`
- `kinds.xhr.variants[0].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.xhr.variants[0].headers[1].value`: `"*/*"` → (none)
- `kinds.xhr.variants[0].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.xhr.variants[0].http1[1].value`: (none) → `"*/*"`
- `kinds.xhr.variants[0].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.xhr.variants[0].http1[2].value`: `"*/*"` → (none)
- `kinds.xhr.variants[1].headers[0].name`: `"sec-fetch-site"` → `"accept"`
- `kinds.xhr.variants[1].headers[0].value`: (none) → `"*/*"`
- `kinds.xhr.variants[1].headers[1].name`: `"accept"` → `"sec-fetch-site"`
- `kinds.xhr.variants[1].headers[1].value`: `"*/*"` → (none)
- `kinds.xhr.variants[1].http1[1].name`: `"Sec-Fetch-Site"` → `"Accept"`
- `kinds.xhr.variants[1].http1[1].value`: (none) → `"*/*"`
- `kinds.xhr.variants[1].http1[2].name`: `"Accept"` → `"Sec-Fetch-Site"`
- `kinds.xhr.variants[1].http1[2].value`: `"*/*"` → (none)
- `kinds.xhr.variants[3].http1[2].name`: `"Accept"` → `"User-Agent"`
- `kinds.xhr.variants[3].http1[2].value`: `"*/*"` → `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"`
- `kinds.xhr.variants[3].http1[3].name`: `"User-Agent"` → `"Accept"`
- `kinds.xhr.variants[3].http1[3].value`: `"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6.2 Mobile/15E148 Safari/604.1"` → `"*/*"`
- `kinds.xhr.variants[6].headers[2].name`: `"origin"` → `"sec-fetch-site"`
- `kinds.xhr.variants[6].headers[3].name`: `"sec-fetch-site"` → `"origin"`
- `quic.ackDelayConstant.max`: `27` → `45`
- `quic.ackDelayConstant.min`: `9` → `39`
- `quic.loss.abandonClose`: `"zero_rtt"` → (none)
- `quic.spinBit`: `"spin"` → `"random"`
- `quic.transportParameters`: `[{"id":"0x04","value":16777216},{"id":"0x05","value":2097152},{"id":"0x06","value":2097152},{"id":"0x07","value":2097152},{"id":"0x09","value":8},{"id":"0x0e","value":64},{"id":"0x0f"},{"data":"07","id":"0xff080808","last":true}]` → `[{"id":"0x04","value":16777216},{"id":"0x05","value":2097152},{"id":"0x06","value":2097152},{"id":"0x07","value":2097152},{"id":"0x09","value":8},{"id":"0x0e","value":64},{"id":"0x0f"}]`
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
