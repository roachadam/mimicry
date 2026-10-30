# Refresh of chrome-156-macos, 2026-10-26

chrome 156.0.8078.48 on the second Mac, quick mode: new profile, copied from chrome-155-macos. Made by `go run ./cmd/refresh` (staging `internal/harness/.refresh/20261026-213734`).

## Captures

Every capture names chrome-156-macos with one user agent, and the browser's version was 156.0.8078.48 before and after the run.

User agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 16s | 57s |
| http1 | 1 | 5s | 19s |
| kinds | 1 | 7s | 31s |
| kinds-http1 | 1 | 7s | 31s |
| kinds-plain | 1 | 7s | 31s |
| websocket | 1 | 36s | 2m11s |
| websocket-h2 | 1 | 7s | 2m11s |
| h3-discovery | 3 | 2m46s | 3m48s |
| h3-page | 3 | 40s | 2m3s |
| h3-discovery.local-roots | 3 | 2m47s | 3m48s |
| h3-page.local-roots | 3 | 40s | 2m3s |
| total | | 8m22s | 18m53s |

Wall time of the capture run (launches, copies and checks included): 8m24s.

## Inherited scenarios

Checked against these profiles' captures, version tokens rewritten (`captures_from`, decision 0035):

- connections: chrome-155-macos
- cookies: chrome-155-macos
- cookies-edge: chrome-155-macos
- cookies-edge2: chrome-155-macos
- cors: chrome-155-macos
- cors-edge: chrome-155-macos
- explicit-table: chrome-155-macos
- h3-blackhole: chrome-155-macos
- h3-broken: chrome-155-macos
- h3-download: chrome-155-macos
- h3-early: chrome-155-macos
- h3-too-early: chrome-155-macos
- h3-v2: chrome-155-macos
- h3-vn: chrome-155-macos
- headers-order: chrome-155-macos
- huge-table: chrome-155-macos
- large-settings: chrome-155-macos
- nav-redirect-click: chrome-155-macos
- nav-redirect-cs: chrome-155-macos
- nav-redirect-form: chrome-155-macos
- nav-redirect-http: chrome-155-macos
- nav-redirect-page: chrome-155-macos
- nav-redirect-so: chrome-155-macos
- nav-redirect-ss: chrome-155-macos
- proxy-auth: chrome-155-macos
- proxy-connect: chrome-155-macos
- proxy-plain: chrome-155-macos
- proxy-plain-auth: chrome-155-macos
- proxy-socks5: chrome-155-macos
- proxy-websocket: chrome-155-macos
- proxy-websocket-auth: chrome-155-macos
- proxy-websocket-socks5: chrome-155-macos
- redirects2: chrome-155-macos
- replay-github.com: chrome-155-macos
- replay-nike.com: chrome-155-macos
- replay-nytimes.com: chrome-155-macos
- replay-ticketmaster.com: chrome-155-macos
- replay-walmart.com: chrome-155-macos
- replay-zillow.com: chrome-155-macos
- resume: chrome-155-macos
- resume-tls12: chrome-155-macos
- set-cookie: chrome-155-macos
- slow-idle: chrome-155-macos
- small-limits: chrome-155-macos

## Changes from chrome-155-macos

Version strings, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36` → `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36`
- sec-ch-ua: `"Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"` → `"Not:A-Brand";v="8", "Chromium";v="156", "Google Chrome";v="156"`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `captures_from`: (none) → `{"connections":"chrome-155-macos","cookies":"chrome-155-macos","cookies-edge":"chrome-155-macos","cookies-edge2":"chrome-155-macos","cors":"chrome-155-macos","cors-edge":"chrome-155-macos","explicit-table":"chrome-155-macos","h3-blackhole":"chrome-155-macos","h3-broken":"chrome-155-macos","h3-downlo…`
- `name`: `"chrome-155-macos"` → `"chrome-156-macos"`
- `version`: `"155"` → `"156"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

No change.

### Generated (hellogen: the hello's shape, tls.websocket_h1, tls.quic; h3gen: quic, http3; kindsgen: kinds, context, variants, http3 headers and priorities)

No change.

### Hand-written, copied from the previous profile

No change.

## Deprecation

No chrome profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests (`MIMICRY_PROFILES=chrome-156-macos go test -count=1 ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/ (harness), go test -count=1 ./profiles/ ./cmd/profilegen/ (library)`, 3m48s): passed. Output: `internal/harness/.refresh/20261026-213734/chrome/tests.log`.

realcheck (`realcheck -browsers chrome -profiles chrome-156-macos (on the second Mac)`, 1m23s): ran. Output: `internal/harness/.refresh/20261026-213734/chrome/realcheck/compare.md`.

```
# Echo endpoints: browser vs mimicry

## browser-chrome-156 vs chrome-156-macos

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
| JA3 hash, as sent | `2135eef00c0be6194f9aba7d54824f94` | `5e47744b332a3284f834b5c2a52372cf` | ✓ random: extension order, shuffled per connection |
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
- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and `docs/backlog.md` for any new gap pinned.
- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `captures_from`; a scenario recaptured for this version needs its own entry if the oddity recurs.
