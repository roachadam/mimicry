# Refresh of chrome-157-macos, 2026-11-09

chrome 157.0.8103.39 on the second Mac, quick mode: new profile, copied from chrome-156-macos. Made by `go run ./cmd/refresh` (staging `.refresh/20261109-225649`).

## Captures

Every capture names chrome-157-macos with one user agent, and the browser's version was 157.0.8103.39 before and after the run.

User agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 16s | 57s |
| http1 | 1 | 5s | 19s |
| kinds | 1 | 7s | 31s |
| kinds-http1 | 1 | 7s | 31s |
| kinds-plain | 1 | 7s | 31s |
| websocket | 1 | 35s | 2m11s |
| websocket-h2 | 1 | 7s | 2m11s |
| h3-discovery | 3 | 2m46s | 3m48s |
| h3-page | 3 | 40s | 2m3s |
| hrr | 3 | 20s | 1m18s |
| h3-discovery.local-roots | 3 | 2m46s | 3m48s |
| h3-page.local-roots | 3 | 40s | 2m3s |
| client-hints.local-roots | 3 | 1m11s | 3m33s |
| total | | 9m52s | 23m44s |

Wall time of the capture run (launches, copies and checks included): 9m54s.

## Inherited scenarios

Checked against these profiles' captures, version tokens rewritten (`capturesFrom`, decision 0035):

- bodiless: chrome-156-macos
- client-cert-ec: chrome-156-macos
- client-cert-ec-tls12: chrome-156-macos
- client-cert-rsa: chrome-156-macos
- client-cert-rsa-tls12: chrome-156-macos
- connections: chrome-155-macos
- cookies: chrome-155-macos
- cookies-edge: chrome-155-macos
- cookies-edge2: chrome-155-macos
- cors: chrome-155-macos
- cors-edge: chrome-155-macos
- cors-edge-cookies: chrome-156-macos
- explicit-table: chrome-155-macos
- h2-upload: chrome-156-macos
- h3-blackhole: chrome-155-macos
- h3-broken: chrome-155-macos
- h3-download: chrome-155-macos
- h3-early: chrome-155-macos
- h3-resume-table: chrome-156-macos
- h3-streams-blocked: chrome-156-macos
- h3-too-early: chrome-155-macos
- h3-v2: chrome-155-macos
- h3-vn: chrome-155-macos
- headers-order: chrome-155-macos
- http1-upload: chrome-156-macos
- huge-table: chrome-155-macos
- large-settings: chrome-155-macos
- nav-redirect-click: chrome-155-macos
- nav-redirect-click-cookies: chrome-156-macos
- nav-redirect-cs: chrome-155-macos
- nav-redirect-cs-cookies: chrome-156-macos
- nav-redirect-form: chrome-155-macos
- nav-redirect-form-cookies: chrome-156-macos
- nav-redirect-http: chrome-155-macos
- nav-redirect-http-cookies: chrome-156-macos
- nav-redirect-page: chrome-155-macos
- nav-redirect-page-cookies: chrome-156-macos
- nav-redirect-so: chrome-155-macos
- nav-redirect-so-cookies: chrome-156-macos
- nav-redirect-ss: chrome-155-macos
- nav-redirect-ss-cookies: chrome-156-macos
- proxy-auth: chrome-155-macos
- proxy-connect: chrome-155-macos
- proxy-https: chrome-156-macos
- proxy-https-auth: chrome-156-macos
- proxy-https-h1: chrome-156-macos
- proxy-https-h1-auth: chrome-156-macos
- proxy-plain: chrome-155-macos
- proxy-plain-auth: chrome-155-macos
- proxy-socks5: chrome-155-macos
- proxy-websocket: chrome-155-macos
- proxy-websocket-auth: chrome-155-macos
- proxy-websocket-socks5: chrome-155-macos
- redirects2: chrome-155-macos
- redirects2-cookies: chrome-156-macos
- replay-github.com: chrome-155-macos
- replay-nike.com: chrome-155-macos
- replay-nytimes.com: chrome-155-macos
- replay-ticketmaster.com: chrome-155-macos
- replay-walmart.com: chrome-155-macos
- replay-zillow.com: chrome-155-macos
- resume: chrome-156-macos
- resume-tls12: chrome-155-macos
- set-cookie: chrome-155-macos
- slow-idle: chrome-155-macos
- small-limits: chrome-155-macos
- urls: chrome-156-macos

## Changes from chrome-156-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36` → `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`
- sec-ch-ua: `"Not:A-Brand";v="8", "Chromium";v="156", "Google Chrome";v="156"` → `"Not-A.Brand";v="99", "Google Chrome";v="157", "Chromium";v="157"`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `capturesFrom.bodiless`: (none) → `"chrome-156-macos"`
- `capturesFrom.client-cert-ec`: (none) → `"chrome-156-macos"`
- `capturesFrom.client-cert-ec-tls12`: (none) → `"chrome-156-macos"`
- `capturesFrom.client-cert-rsa`: (none) → `"chrome-156-macos"`
- `capturesFrom.client-cert-rsa-tls12`: (none) → `"chrome-156-macos"`
- `capturesFrom.cors-edge-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.h2-upload`: (none) → `"chrome-156-macos"`
- `capturesFrom.h3-resume-table`: (none) → `"chrome-156-macos"`
- `capturesFrom.h3-streams-blocked`: (none) → `"chrome-156-macos"`
- `capturesFrom.http1-upload`: (none) → `"chrome-156-macos"`
- `capturesFrom.nav-redirect-click-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.nav-redirect-cs-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.nav-redirect-form-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.nav-redirect-http-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.nav-redirect-page-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.nav-redirect-so-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.nav-redirect-ss-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.proxy-https`: (none) → `"chrome-156-macos"`
- `capturesFrom.proxy-https-auth`: (none) → `"chrome-156-macos"`
- `capturesFrom.proxy-https-h1`: (none) → `"chrome-156-macos"`
- `capturesFrom.proxy-https-h1-auth`: (none) → `"chrome-156-macos"`
- `capturesFrom.redirects2-cookies`: (none) → `"chrome-156-macos"`
- `capturesFrom.resume`: (none) → `"chrome-156-macos"`
- `capturesFrom.urls`: (none) → `"chrome-156-macos"`
- `name`: `"chrome-156-macos"` → `"chrome-157-macos"`
- `version`: `"156"` → `"157"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

No change.

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic, tls.helloRetry, tls.x25519Reuse, tls.trailingDotSni, tls.clientAuth, http2.openingRecords, http2.goawayReply, http1.uploadRecords; h3gen: quic, http3; kindsgen: kinds, context, variants, emptyBody, clientHints, urls, http3 headers and priorities)

- `clientHints.fullVersion`: `"156.0.8078.80"` → `"157.0.8103.39"`
- `clientHints.machine.deviceMemory`: `32` → `16`
- `clientHints.machine.platformVersion`: `"27.0.1"` → `"26.2.0"`
- `clientHints.machine.reducedMotion`: `"no-preference"` → `"reduce"`
- `clientHints.machine.reducedTransparency`: `"no-preference"` → `"reduce"`
- `clientHints.machine.viewportHeight`: `953` → `1195`
- `quic.crypto.head.min`: `55` → `56`

### Hand-written, copied from the previous profile

No change.

## Deprecation

No chrome profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests (`MIMICRY_PROFILES=chrome-157-macos go test -count=1 ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/ (harness), go test -count=1 ./profiles/ ./cmd/profilegen/ (library)`, 3m56s): failed: ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/: exit status 1. Output: `.refresh/20261109-225649/chrome/tests.log`.

```
--- FAIL: TestClientHintsMatchesBrowser (0.00s)
--- FAIL: TestClientHintsMatchesBrowser (0.00s)
    --- FAIL: TestClientHintsMatchesBrowser/chrome-157-macos (17.60s)
        clienthints_test.go:41: plans checked against the wire: map[HTTP/1.1:6 HTTP/2.0:71 HTTP/3.0:7 restarts:6], 75 with the protocol decided before sending
        clienthints_test.go:44: /h3/next.width-img #1: header lines differ from every sample's; against the first that sent it:  line 4 of 33 (want 35):
             got device-memory: 16
            want width: 100
        clienthints_test.go:44: /h3/b.nav #1: went h2 www.ch-b.test #2, browser h2 www.ch-b.test #4
        clienthints_test.go:44: /h3/b.fetch #1: went h2 www.ch-b.test #2, browser h2 www.ch-b.test #4
--- FAIL: TestHTTP3DownloadMatchesBrowser (0.00s)
        clienthints_test.go:44: /h3/next.width-img #1: header lines differ from every sample's; against the first that sent it:  line 4 of 33 (want 35):
             got device-memory: 16
            want width: 100
        clienthints_test.go:44: /h3/b.nav #1: went h2 www.ch-b.test #2, browser h2 www.ch-b.test #4
        clienthints_test.go:44: /h3/b.fetch #1: went h2 www.ch-b.test #2, browser h2 www.ch-b.test #4
--- FAIL: TestHTTP3DownloadMatchesBrowser (0.00s)
    --- FAIL: TestHTTP3DownloadMatchesBrowser/chrome-157-macos (12.53s)
        http3_download_test.go:60: sample 3: 3670 of 3718 ACKs during the bulk receive acknowledge every 10 new packets (98.7%), 9.91 on average; the server updated its keys 1 times, the client 1
        http3_download_test.go:63: plans checked against the wire: map[HTTP/2.0:2 HTTP/3.0:7], 8 with the protocol decided before sending
        http3_download_test.go:74: mimicry: 3621 of 3926 ACKs during the bulk receive acknowledge every 10 new packets; want 95%
        http3_download_test.go:77: mimicry: 3621 of 3926 ACKs during the bulk receive acknowledge every 10 new packets (92.2%), 9.52 on average; the server updated its keys 1 times, the client 1
        http3_download_test.go:78: MAX_STREAM_DATA: mimicry raised it 16 times by 3145915–3146578 (median 3146280); the browser 48 times over 3 samples, by 3146172–3157696
        http3_download_test.go:81: MAX_DATA: mimicry raised it 6 times by 7865536–7865706 (median 7865700); the browser 18 times over 3 samples, by 7864457–7871535
--- FAIL: TestHTTPSProxyMatchesBrowser (0.00s)
        http3_download_test.go:63: plans checked against the wire: map[HTTP/2.0:2 HTTP/3.0:7], 8 with the protocol decided before sending
        http3_download_test.go:74: mimicry: 3621 of 3926 ACKs during the bulk receive acknowledge every 10 new packets; want 95%
        http3_download_test.go:77: mimicry: 3621 of 3926 ACKs during the bulk receive acknowledge every 10 new packets (92.2%), 9.52 on average; the server updated its keys 1 times, the client 1
        http3_download_test.go:78: MAX_STREAM_DATA: mimicry raised it 16 times by 3145915–3146578 (median 3146280); the browser 48 times over 3 samples, by 3146172–3157696
        http3_download_test.go:81: MAX_DATA: mimicry raised it 6 times by 7865536–7865706 (median 7865700); the browser 18 times over 3 samples, by 7864457–7871535
--- FAIL: TestHTTPSProxyMatchesBrowser (0.00s)
    --- FAIL: TestHTTPSProxyMatchesBrowser/chrome-157-macos (0.00s)
        http3_download_test.go:81: MAX_DATA: mimicry raised it 6 times by 7865536–7865706 (median 7865700); the browser 18 times over 3 samples, by 7864457–7871535
--- FAIL: TestHTTPSProxyMatchesBrowser (0.00s)
    --- FAIL: TestHTTPSProxyMatchesBrowser/chrome-157-macos (0.00s)
        httpsproxy_test.go:66: proxy-https: chrome-156-macos.proxy-https.proxytrace.json inherited, versions rewritten chrome-156-macos → chrome-157-macos
        httpsproxy_test.go:66: proxy-https: chrome-156-macos.proxy-https.proxytls.json.gz inherited, versions rewritten chrome-156-macos → chrome-157-macos
        httpsproxy_test.go:66: chrome-156-macos.proxy-https.proxytls.json.gz is inherited, and its hellos cannot be rewritten
--- FAIL: TestProxyMatchesBrowser (0.03s)
--- FAIL: TestHTTPSProxyMatchesBrowser (0.00s)
    --- FAIL: TestHTTPSProxyMatchesBrowser/chrome-157-macos (0.00s)
        httpsproxy_test.go:66: proxy-https: chrome-156-macos.proxy-https.proxytrace.json inherited, versions rewritten chrome-156-macos → chrome-157-macos
        httpsproxy_test.go:66: proxy-https: chrome-156-macos.proxy-https.proxytls.json.gz inherited, versions rewritten chrome-156-macos → chrome-157-macos
        httpsproxy_test.go:66: chrome-156-macos.proxy-https.proxytls.json.gz is inherited, and its hellos cannot be rewritten
--- FAIL: TestProxyMatchesBrowser (0.03s)
    --- FAIL: TestProxyMatchesBrowser/chrome-157-macos (0.03s)
    --- FAIL: TestHTTPSProxyMatchesBrowser/chrome-157-macos (0.00s)
        httpsproxy_test.go:66: proxy-https: chrome-156-macos.proxy-https.proxytrace.json inherited, versions rewritten chrome-156-macos → chrome-157-macos
        httpsproxy_test.go:66: proxy-https: chrome-156-macos.proxy-https.proxytls.json.gz inherited, versions rewritten chrome-156-macos → chrome-157-macos
        httpsproxy_test.go:66: chrome-156-macos.proxy-https.proxytls.json.gz is inherited, and its hellos cannot be rewritten
--- FAIL: TestProxyMatchesBrowser (0.03s)
    --- FAIL: TestProxyMatchesBrowser/chrome-157-macos (0.03s)
        --- FAIL: TestProxyMatchesBrowser/chrome-157-macos/plain (0.01s)
    --- FAIL: TestProxyMatchesBrowser/chrome-157-macos (0.03s)
        --- FAIL: TestProxyMatchesBrowser/chrome-157-macos/plain (0.01s)
            proxy_test.go:154: proxy-plain: chrome-155-macos.proxy-plain.h1trace.json inherited, versions rewritten chrome-155-macos → chrome-157-macos
            proxy_test.go:186: forwarded request:
                 got ["GET http://capture.mimicry.test:PORT/nav HTTP/1.1" "Host: capture.mimicry.test:PORT" "Proxy-Connection: keep-alive" "Upgrade-Insecure-Requests: 1" "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36" "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7" "Accept-Encoding: gzip, deflate" "Accept-Language: en-US,en;q=0.9" "Cookie: mimicry_a=1; mimicry_b=2"]
                want ["GET http://capture.mimicry.test:PORT/nav HTTP/1.1" "Host: capture.mimicry.test:PORT" "Proxy-Connection: keep-alive" "Upgrade-Insecure-Requests: 1" "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36" "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7" "Referer: http://capture.mimicry.test:PORT/" "Accept-Encoding: gzip, deflate" "Accept-Language: en-US,en;q=0.9" "Cookie: mimicry_a=1; mimicry_b=2"]
--- FAIL: TestURLsMatchBrowser (0.00s)
        --- FAIL: TestProxyMatchesBrowser/chrome-157-macos/plain (0.01s)
            proxy_test.go:154: proxy-plain: chrome-155-macos.proxy-plain.h1trace.json inherited, versions rewritten chrome-155-macos → chrome-157-macos
            proxy_test.go:186: forwarded request:
                 got ["GET http://capture.mimicry.test:PORT/nav HTTP/1.1" "Host: capture.mimicry.test:PORT" "Proxy-Connection: keep-alive" "Upgrade-Insecure-Requests: 1" "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36" "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7" "Accept-Encoding: gzip, deflate" "Accept-Language: en-US,en;q=0.9" "Cookie: mimicry_a=1; mimicry_b=2"]
                want ["GET http://capture.mimicry.test:PORT/nav HTTP/1.1" "Host: capture.mimicry.test:PORT" "Proxy-Connection: keep-alive" "Upgrade-Insecure-Requests: 1" "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36" "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7" "Referer: http://capture.mimicry.test:PORT/" "Accept-Encoding: gzip, deflate" "Accept-Language: en-US,en;q=0.9" "Cookie: mimicry_a=1; mimicry_b=2"]
--- FAIL: TestURLsMatchBrowser (0.00s)
    --- FAIL: TestURLsMatchBrowser/chrome-157-macos (0.02s)
              browser nothing sent, hello refused: sni=www.urls.test.
              mimicry GET / host=www.urls.test.:{tls} sni=www.urls.test
        urls_test.go:62: the page reports failing: f-h-dot
        urls_test.go:75: sample 2, n-h-dotpool https://www.urls.test.:{tls}{c}:
              browser nothing sent, hello refused: sni=www.urls.test.
              mimicry GET / host=www.urls.test.:{tls} sni=www.urls.test
FAIL
              mimicry GET / host=www.urls.test.:{tls} sni=www.urls.test
        urls_test.go:62: the page reports failing: f-h-dot
        urls_test.go:75: sample 2, n-h-dotpool https://www.urls.test.:{tls}{c}:
              browser nothing sent, hello refused: sni=www.urls.test.
              mimicry GET / host=www.urls.test.:{tls} sni=www.urls.test
FAIL
FAIL	github.com/roachadam/mimicry/internal/harness/e2e	233.355s
              mimicry GET / host=www.urls.test.:{tls} sni=www.urls.test
FAIL
FAIL	github.com/roachadam/mimicry/internal/harness/e2e	233.355s
ok  	github.com/roachadam/mimicry/internal/harness/cmd/hellogen	4.389s
ok  	github.com/roachadam/mimicry/internal/harness/cmd/h3gen	0.684s
ok  	github.com/roachadam/mimicry/internal/harness/cmd/kindsgen	0.501s
FAIL
```

realcheck (`realcheck -browsers chrome -profiles chrome-157-macos (on the second Mac)`, 1m22s): ran. Output: `.refresh/20261109-225649/chrome/realcheck/compare.md`.

```
# Echo endpoints: browser vs mimicry

## browser-chrome-157-macos vs chrome-157-macos

| Endpoint | Browser proto (tries) | mimicry proto (tries) | Fields | Same | Random by design | Differ | One side only |
|---|---|---|---|---|---|---|---|
| peet | h2 (1) | h2 (1) | 62 | 59 | 3 | 0 | 0 |
| browserleaks-tls |  (1) |  (1) | 11 | 7 | 4 | 0 | 0 |
| browserleaks-h2 |  (1) |  (1) | 23 | 23 | 0 | 0 | 0 |
| impersonate-h2 | h2 (1) | h2 (1) | 53 | 51 | 2 | 0 | 0 |
| impersonate-h3 | h3 (2) | h3 (2) | 47 | 42 | 5 | 0 | 0 |

| Field | Browser | mimicry | |
|---|---|---|---|
| JA4 | `t13d1517h2_8daaf6152771_cb7bf5808d99` | `t13d1517h2_8daaf6152771_cb7bf5808d99` | ✓ |
| JA3 hash, extensions sorted (JA3N) | `bd4930bd9b000ee684830e44bab76fdf` | `bd4930bd9b000ee684830e44bab76fdf` | ✓ |
| JA3 hash, as sent | `c754b1189ec1b862d0300a5aecc51a44` | `cf1432c1794a435d658664e4a5fef19e` | ✓ random: extension order, shuffled per connection |
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
- GREASE ECH payload length, one of 144/176/208/240: peet ext ECH payload length
- QUIC transport parameter order, shuffled per connection: impersonate-h3 http3.perk_hash, impersonate-h3 http3.perk_text, impersonate-h3 ext quic_transport_parameters order

Differences:

- none
```

## To do by hand

- Review the changes above; a hand-written section copied from the previous profile is unverified until the e2e tests pass against its inherited or new captures.
- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and a line in its Not covered by any claim for any new gap pinned.
- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `capturesFrom`; a scenario recaptured for this version needs its own entry if the oddity recurs.

## Done by hand (2026-11-09)

- h3gen refused the first two derives. `ackInitial`: Chrome 157 acknowledged the server's Initial in 2 of its 21 completed full handshakes (`h3-discovery.local-roots` samples 2 and 3, connection 2); `priorityUpdate`: two PRIORITY_UPDATEs the server read 1 ms after their HEADERS (`h3-discovery.local-roots` and `h3-page.local-roots`, sample 3, connection 1). Both are timing, as Chrome 156's on the second Mac; pinned in h3gen's `sampleGaps`. The derived `quic` and `http3` sections equal chrome-156-macos's but for `quic.crypto.head.min` (56, was 55).
- `TestClientHintsMatchesBrowser`: this version's own `client-hints` capture shows the gaps every Chrome profile has, the third party's connection (`noSitePartition`) and `width` 100 on `next.width-img` (`noWidth`, 3 of 3 samples); added to `h3Gaps`.
- `TestURLsMatchBrowser` and `TestProxyMatchesBrowser/plain`: their gap tables (`urlsKnown`, `proxyPlainGaps`) were keyed by the profile under test, so chrome-156-macos's `h-dotpool` pin and chrome-155-macos's link-navigation pin did not reach this profile through `capturesFrom`. Both are now keyed by the profile that took the capture (`capture.Source`), as the other e2e gap tables are; `proxyPlainGaps` lost the entries of the profiles that inherit `proxy-plain` (chrome-156-macos, firefox-159-macos, firefox-159-windows and the synthetic inheriting profile), which their sources' entries now cover.
- `TestHTTPSProxyMatchesBrowser` refused an inherited `.proxytls.json.gz`. Its hellos carry no version token; the version is in the CONNECTs' HTTP/2 header blocks, which `capture.Rewriter.ProxyTLS` now rewrites as `Traces` does page traces, so the inherited `proxy-https*` captures are checked (a weight of 147 in a copy of the profile fails against them).
- `TestHTTP3DownloadMatchesBrowser` failed in this run (mimicry's ACKs every 10 packets 92.2%, the test wants 95%). Run alone on this Mac, on battery, the same test failed for chrome-156-macos as well (91.5%, 91.7%) and passed for both in other runs (97.5–99.5%): this machine's timing, not the profile, which keeps chrome-156-macos's `quic.ack`.
- `profiles/EVIDENCE.md`: the profile's paragraph, the URL rows' inheritors and Chrome 157 in the late PRIORITY_UPDATE line; README's table.
- Then `MIMICRY_PROFILES=chrome-157-macos go test -count=1 ./e2e/ ./cmd/...` (harness) and `go test -count=1 ./...` (library) pass, and with `MIMICRY_PROFILES=all` the four tests above and `TestInheritedProfile`.
