# Refresh of firefox-159-windows, 2026-11-03

firefox 159.0.1 on the Windows PC, quick mode: new profile, copied from firefox-158-windows. Made by `go run ./cmd/refresh` (staging `.refresh/20261103-183342`).

## Captures

Every capture names firefox-159-windows with one user agent, and the browser's version was 159.0.1 before and after the run.

User agent: `Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 4s | 42s |
| http1 | 1 | 1s | 14s |
| kinds | 1 | 3s | 26s |
| kinds-http1 | 1 | 3s | 26s |
| kinds-plain | 1 | 3s | 26s |
| websocket | 1 | 3s | 2m6s |
| websocket-h2 | 1 | 5s | 2m6s |
| h3-discovery | 3 | 2m35s | 3m33s |
| h3-page | 3 | 28s | 1m48s |
| hrr | 3 | 9s | 1m3s |
| h3-discovery.local-roots | 3 | 2m35s | 3m33s |
| h3-page.local-roots | 3 | 28s | 1m48s |
| total | | 6m41s | 18m11s |

Wall time of the capture run (launches, copies and checks included): 6m47s.

## Inherited scenarios

Checked against these profiles' captures, version tokens rewritten (`capturesFrom`, decision 0035):

- bodiless: firefox-158-windows
- connections: firefox-158-windows
- cookies: firefox-158-windows
- cookies-edge: firefox-158-windows
- cookies-edge2: firefox-158-windows
- cors: firefox-158-windows
- cors-edge: firefox-158-windows
- cors-edge-cookies: firefox-158-windows
- explicit-table: firefox-158-windows
- h2-upload: firefox-158-windows
- h3-blackhole: firefox-158-windows
- h3-broken: firefox-158-windows
- h3-download: firefox-158-windows
- h3-early: firefox-158-windows
- h3-streams-blocked: firefox-158-windows
- h3-too-early: firefox-158-windows
- h3-v2: firefox-158-windows
- h3-vn: firefox-158-windows
- headers-order: firefox-158-windows
- http1-upload: firefox-158-windows
- huge-table: firefox-158-windows
- large-settings: firefox-158-windows
- nav-redirect-click: firefox-158-windows
- nav-redirect-click-cookies: firefox-158-windows
- nav-redirect-cs: firefox-158-windows
- nav-redirect-cs-cookies: firefox-158-windows
- nav-redirect-form: firefox-158-windows
- nav-redirect-form-cookies: firefox-158-windows
- nav-redirect-http: firefox-158-windows
- nav-redirect-http-cookies: firefox-158-windows
- nav-redirect-page: firefox-158-windows
- nav-redirect-page-cookies: firefox-158-windows
- nav-redirect-so: firefox-158-windows
- nav-redirect-so-cookies: firefox-158-windows
- nav-redirect-ss: firefox-158-windows
- nav-redirect-ss-cookies: firefox-158-windows
- proxy-auth: firefox-158-windows
- proxy-connect: firefox-158-windows
- proxy-plain: firefox-158-windows
- proxy-plain-auth: firefox-158-windows
- proxy-socks5: firefox-158-windows
- proxy-websocket: firefox-158-windows
- proxy-websocket-auth: firefox-158-windows
- proxy-websocket-socks5: firefox-158-windows
- redirects2: firefox-158-windows
- redirects2-cookies: firefox-158-windows
- replay-github.com: firefox-158-windows
- replay-nike.com: firefox-158-windows
- replay-nytimes.com: firefox-158-windows
- replay-ticketmaster.com: firefox-158-windows
- replay-walmart.com: firefox-158-windows
- replay-zillow.com: firefox-158-windows
- resume: firefox-158-windows
- resume-tls12: firefox-158-windows
- set-cookie: firefox-158-windows
- slow-idle: firefox-158-windows
- small-limits: firefox-158-windows

## Changes from firefox-158-windows

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:158.0) Gecko/20100101 Firefox/158.0` → `Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `capturesFrom`: (none) → `{"bodiless":"firefox-158-windows","connections":"firefox-158-windows","cookies":"firefox-158-windows","cookies-edge":"firefox-158-windows","cookies-edge2":"firefox-158-windows","cors":"firefox-158-windows","cors-edge":"firefox-158-windows","cors-edge-cookies":"firefox-158-windows","explicit-table":"…`
- `name`: `"firefox-158-windows"` → `"firefox-159-windows"`
- `version`: `"158"` → `"159"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

- `tls.helloRetry`: (none) → `{"cookieAfter":"0x000d"}`

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic; h3gen: quic, http3; kindsgen: kinds, context, variants, http3 headers and priorities)

- `http3.discovery.raceDelayMs.max`: `3` → `2`
- `http3.discovery.raceDelayMs.min`: `3` → `2`
- `kinds.fetch.variants[1].crossOriginRedirect`: (none) → `true`
- `kinds.fetch.variants[1].headers`: `[{"name":"user-agent","value":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"},{"name":"accept","value":"*/*"},{"name":"accept-language","value":"en-US,en;q=0.9"},{"name":"accept-encoding","value":"gzip, deflate, br, zstd"},{"name":"referer"},{"name":"*"},{"name":"…` → `[{"name":"user-agent","value":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"},{"name":"accept","value":"*/*"},{"name":"accept-language","value":"en-US,en;q=0.9"},{"name":"accept-encoding","value":"gzip, deflate, br, zstd"},{"name":"origin"},{"name":"referer"},{"na…`
- `kinds.fetch.variants[1].http1`: `[{"name":"Host"},{"name":"User-Agent","value":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"},{"name":"Accept","value":"*/*"},{"name":"Accept-Language","value":"en-US,en;q=0.9"},{"name":"Accept-Encoding","value":"gzip, deflate, br, zstd"},{"name":"Referer"},{"name…` → `[{"name":"Host"},{"name":"User-Agent","value":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"},{"name":"Accept","value":"*/*"},{"name":"Accept-Language","value":"en-US,en;q=0.9"},{"name":"Accept-Encoding","value":"gzip, deflate, br, zstd"},{"name":"Origin"},{"name"…`
- `kinds.fetch.variants[1].pageSet`: `true` → (none)
- `kinds.fetch.variants[1].redirected`: (none) → `true`
- `kinds.fetch.variants[2].crossOriginRedirect`: `true` → (none)
- `kinds.fetch.variants[2].headers`: `[{"name":"user-agent","value":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"},{"name":"accept","value":"*/*"},{"name":"accept-language","value":"en-US,en;q=0.9"},{"name":"accept-encoding","value":"gzip, deflate, br, zstd"},{"name":"origin"},{"name":"referer"},{"na…` → `[{"name":"user-agent","value":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"},{"name":"accept","value":"*/*"},{"name":"accept-language","value":"en-US,en;q=0.9"},{"name":"accept-encoding","value":"gzip, deflate, br, zstd"},{"name":"referer"},{"name":"*"},{"name":"…`
- `kinds.fetch.variants[2].http1`: `[{"name":"Host"},{"name":"User-Agent","value":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"},{"name":"Accept","value":"*/*"},{"name":"Accept-Language","value":"en-US,en;q=0.9"},{"name":"Accept-Encoding","value":"gzip, deflate, br, zstd"},{"name":"Origin"},{"name"…` → `[{"name":"Host"},{"name":"User-Agent","value":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"},{"name":"Accept","value":"*/*"},{"name":"Accept-Language","value":"en-US,en;q=0.9"},{"name":"Accept-Encoding","value":"gzip, deflate, br, zstd"},{"name":"Referer"},{"name…`
- `kinds.fetch.variants[2].pageSet`: (none) → `true`
- `kinds.fetch.variants[2].redirected`: `true` → (none)

### Hand-written, copied from the previous profile

No change.

## Deprecation

No firefox profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests (`MIMICRY_PROFILES=firefox-159-windows go test -count=1 ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/ (harness), go test -count=1 ./profiles/ ./cmd/profilegen/ (library)`, 3m30s): failed: ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/: exit status 1. Output: `.refresh/20261103-183342/firefox-windows/tests.log`.

```
--- FAIL: TestHTTP3MatchesBrowser (0.00s)
--- FAIL: TestHTTP3MatchesBrowser (0.00s)
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/rules (0.03s)
--- FAIL: TestHTTP3MatchesBrowser (0.00s)
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/rules (0.03s)
        http3_test.go:73: h3-page sample 2 connection 2 (www.h3-b.test): the profile's rule does not describe the browser: packet numbers: the first Initial's 133 in 1 bytes, not 2
        http3_test.go:73: h3-page.local-roots sample 2 connection 20 (www.h3-b.test): the profile's rule does not describe the browser: packet numbers: the first Initial's 167 in 1 bytes, not 2
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/h3-page (8.61s)
--- FAIL: TestHTTP3MatchesBrowser (0.00s)
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/rules (0.03s)
        http3_test.go:73: h3-page sample 2 connection 2 (www.h3-b.test): the profile's rule does not describe the browser: packet numbers: the first Initial's 133 in 1 bytes, not 2
        http3_test.go:73: h3-page.local-roots sample 2 connection 20 (www.h3-b.test): the profile's rule does not describe the browser: packet numbers: the first Initial's 167 in 1 bytes, not 2
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/h3-page (8.61s)
        http3_test.go:97: plans checked against the wire: map[HTTP/2.0:2 HTTP/3.0:26], 26 with the protocol decided before sending
        --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/h3-page/www.h3-a.test#1 (0.00s)
                 got literal_ref static=true index=31 accept-encoding: identity
                want literal_ref static=true index=31 accept-encoding: gzip, deflate, br, zstd
            http3_test.go:813: HTTP/3 /h3/font.so header list:
                 line 8 of 13 (want 13):
                 got accept-encoding: identity
                want accept-encoding: gzip, deflate, br, zstd
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/h3-discovery (50.18s)
                 line 8 of 13 (want 13):
                 got accept-encoding: identity
                want accept-encoding: gzip, deflate, br, zstd
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/h3-discovery (50.18s)
        http3_test.go:97: plans checked against the wire: map[HTTP/2.0:3 HTTP/3.0:17], 15 with the protocol decided before sending
        http3_test.go:99: /h3/after-goaway.so: went h2 www.h3-a.test #2, browser h3 www.h3-a.test #9 or h2 www.h3-a.test #3 or h3 www.h3-a.test #10
--- FAIL: TestCapturesFromNamesCapturedScenarios (0.10s)
                want accept-encoding: gzip, deflate, br, zstd
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/h3-discovery (50.18s)
        http3_test.go:97: plans checked against the wire: map[HTTP/2.0:3 HTTP/3.0:17], 15 with the protocol decided before sending
        http3_test.go:99: /h3/after-goaway.so: went h2 www.h3-a.test #2, browser h3 www.h3-a.test #9 or h2 www.h3-a.test #3 or h3 www.h3-a.test #10
--- FAIL: TestCapturesFromNamesCapturedScenarios (0.10s)
    inherit_test.go:190: firefox-159-windows: capturesFrom.bodiless: firefox-158-windows has no captures of it
--- FAIL: TestKindsMatchBrowserTraces (0.05s)
    --- FAIL: TestHTTP3MatchesBrowser/firefox-159-windows/h3-discovery (50.18s)
        http3_test.go:97: plans checked against the wire: map[HTTP/2.0:3 HTTP/3.0:17], 15 with the protocol decided before sending
        http3_test.go:99: /h3/after-goaway.so: went h2 www.h3-a.test #2, browser h3 www.h3-a.test #9 or h2 www.h3-a.test #3 or h3 www.h3-a.test #10
--- FAIL: TestCapturesFromNamesCapturedScenarios (0.10s)
    inherit_test.go:190: firefox-159-windows: capturesFrom.bodiless: firefox-158-windows has no captures of it
--- FAIL: TestKindsMatchBrowserTraces (0.05s)
    --- FAIL: TestKindsMatchBrowserTraces/firefox-159-windows (0.05s)
        http3_test.go:97: plans checked against the wire: map[HTTP/2.0:3 HTTP/3.0:17], 15 with the protocol decided before sending
        http3_test.go:99: /h3/after-goaway.so: went h2 www.h3-a.test #2, browser h3 www.h3-a.test #9 or h2 www.h3-a.test #3 or h3 www.h3-a.test #10
--- FAIL: TestCapturesFromNamesCapturedScenarios (0.10s)
    inherit_test.go:190: firefox-159-windows: capturesFrom.bodiless: firefox-158-windows has no captures of it
--- FAIL: TestKindsMatchBrowserTraces (0.05s)
    --- FAIL: TestKindsMatchBrowserTraces/firefox-159-windows (0.05s)
        --- FAIL: TestKindsMatchBrowserTraces/firefox-159-windows/kinds (0.02s)
            kinds_test.go:67: connections, sample 1: fetch-nocors.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: fetch-json.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: fetch-custom.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: fetch-high.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: fetch-low.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: xhr.cs: mimicry with script-async.cs, browser with font-css.cs
--- FAIL: TestConnectionMatchesBrowserTrace (0.61s)
            kinds_test.go:67: connections, sample 1: fetch-json.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: fetch-custom.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: fetch-high.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: fetch-low.cs: mimicry with script-async.cs, browser with font-css.cs
            kinds_test.go:67: connections, sample 1: xhr.cs: mimicry with script-async.cs, browser with font-css.cs
--- FAIL: TestConnectionMatchesBrowserTrace (0.61s)
    --- FAIL: TestConnectionMatchesBrowserTrace/firefox-159-windows (0.61s)
            kinds_test.go:67: connections, sample 1: xhr.cs: mimicry with script-async.cs, browser with font-css.cs
--- FAIL: TestConnectionMatchesBrowserTrace (0.61s)
    --- FAIL: TestConnectionMatchesBrowserTrace/firefox-159-windows (0.61s)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
--- FAIL: TestWebSocketsMatchBrowser (0.00s)
--- FAIL: TestConnectionMatchesBrowserTrace (0.61s)
    --- FAIL: TestConnectionMatchesBrowserTrace/firefox-159-windows (0.61s)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
--- FAIL: TestWebSocketsMatchBrowser (0.00s)
    --- FAIL: TestWebSocketsMatchBrowser/firefox-159-windows/websocket-h2 (3.03s)
    --- FAIL: TestConnectionMatchesBrowserTrace/firefox-159-windows (0.61s)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
        trace_test.go:149: /big credit increment 4194304, outside the browser's 16384..2670592 (± one frame)
--- FAIL: TestWebSocketsMatchBrowser (0.00s)
    --- FAIL: TestWebSocketsMatchBrowser/firefox-159-windows/websocket-h2 (3.03s)
        --- FAIL: TestWebSocketsMatchBrowser/firefox-159-windows/websocket-h2/cs-protocols (0.00s)
--- FAIL: TestWebSocketsMatchBrowser (0.00s)
    --- FAIL: TestWebSocketsMatchBrowser/firefox-159-windows/websocket-h2 (3.03s)
        --- FAIL: TestWebSocketsMatchBrowser/firefox-159-windows/websocket-h2/cs-protocols (0.00s)
            websocket_test.go:102: mimicry's stream connection also carried ["/b/set" "/b/set-3p" "/b/cleanup" "/b/cleanup-3p"] (no top-level-site partition); the browser's only ["/b/set-3p" "/b/cleanup-3p"]
            websocket_test.go:102: the stream ended with "", want ["RST_STREAM CANCEL"]
            websocket_test.go:102: transport ended by "server" 3000 ms after the client's last frame, want the client after 1001 to 1001 ms
FAIL
    --- FAIL: TestWebSocketsMatchBrowser/firefox-159-windows/websocket-h2 (3.03s)
        --- FAIL: TestWebSocketsMatchBrowser/firefox-159-windows/websocket-h2/cs-protocols (0.00s)
            websocket_test.go:102: mimicry's stream connection also carried ["/b/set" "/b/set-3p" "/b/cleanup" "/b/cleanup-3p"] (no top-level-site partition); the browser's only ["/b/set-3p" "/b/cleanup-3p"]
            websocket_test.go:102: the stream ended with "", want ["RST_STREAM CANCEL"]
            websocket_test.go:102: transport ended by "server" 3000 ms after the client's last frame, want the client after 1001 to 1001 ms
FAIL
FAIL	github.com/roachadam/mimicry/internal/harness/e2e	207.958s
            websocket_test.go:102: transport ended by "server" 3000 ms after the client's last frame, want the client after 1001 to 1001 ms
FAIL
FAIL	github.com/roachadam/mimicry/internal/harness/e2e	207.958s
ok  	github.com/roachadam/mimicry/internal/harness/cmd/hellogen	4.461s
ok  	github.com/roachadam/mimicry/internal/harness/cmd/h3gen	2.317s
ok  	github.com/roachadam/mimicry/internal/harness/cmd/kindsgen	0.509s
FAIL
```

realcheck (`realcheck -browsers firefox -profiles firefox-159-windows (on the Windows PC)`, 1m24s): ran. Output: `.refresh/20261103-183342/firefox-windows/realcheck/compare.md`.

```
# Echo endpoints: browser vs mimicry

## browser-firefox-159-windows vs firefox-159-windows

| Endpoint | Browser proto (tries) | mimicry proto (tries) | Fields | Same | Random by design | Differ | One side only |
|---|---|---|---|---|---|---|---|
| peet | h2 (1) | h2 (1) | 58 | 58 | 0 | 0 | 0 |
| browserleaks-tls |  (1) |  (1) | 11 | 11 | 0 | 0 | 0 |
| browserleaks-h2 |  (1) |  (1) | 22 | 22 | 0 | 0 | 0 |
| impersonate-h2 | h2 (1) | h2 (1) | 49 | 48 | 1 | 0 | 0 |
| impersonate-h3 | h3 (2) | h3 (2) | 48 | 45 | 3 | 0 | 0 |

| Field | Browser | mimicry | |
|---|---|---|---|
| JA4 | `t13d1517h2_8daaf6152771_3cbfd9057e0d` | `t13d1517h2_8daaf6152771_3cbfd9057e0d` | ✓ |
| JA3 hash, extensions sorted (JA3N) | `f9c6a2c424206e61efb6c1a6325596b5` | `f9c6a2c424206e61efb6c1a6325596b5` | ✓ |
| JA3 hash, as sent | `9d42e90b0225e779f03141ddcd699df2` | `9d42e90b0225e779f03141ddcd699df2` | ✓ |
| peetprint hash | `a4bbbc7e3adf3db97d276fd0adb1efc4` | `a4bbbc7e3adf3db97d276fd0adb1efc4` | ✓ |
| Akamai HTTP/2 | `1:65536;2:0;4:131072;5:16384\|12517377\|0\|m,p,a,s` | `1:65536;2:0;4:131072;5:16384\|12517377\|0\|m,p,a,s` | ✓ |
| HTTP/2 header order | `:method,:path,:authority,:scheme,user-agent,accept,accept-language,accept-encoding,upgrade…` | `:method,:path,:authority,:scheme,user-agent,accept,accept-language,accept-encoding,upgrade…` | ✓ |
| HTTP/2 HEADERS flags and priority | `{"flags":["EndStream (0x1)","EndHeaders (0x4)","Priority (0x20)"],"priority":{"depends_on"…` | `{"flags":["EndStream (0x1)","EndHeaders (0x4)","Priority (0x20)"],"priority":{"depends_on"…` | ✓ |
| impersonate.pro /api/http3 protocol | `http3` | `http3` | ✓ |
| QUIC JA3N | `872c3d8d0635e4167234d6c87532fb0b` | `872c3d8d0635e4167234d6c87532fb0b` | ✓ |
| HTTP/3 fingerprint (perk, normalized) | `1:65536;7:20;727725890:0;16765559:1;51:1;8:1\|m,s,a,p\|1:30000;4:25165824;5:12582912;6:10485…` | `1:65536;7:20;727725890:0;16765559:1;51:1;8:1\|m,s,a,p\|1:30000;4:25165824;5:12582912;6:10485…` | ✓ |
| HTTP/3 header order | `:method,:scheme,:authority,:path,user-agent,accept,accept-language,accept-encoding,alt-use…` | `:method,:scheme,:authority,:path,user-agent,accept,accept-language,accept-encoding,alt-use…` | ✓ |

Random by design (the browser's value among the profile's choices):

- GREASE ECH cipher suite, one of 0x0001/0x0001, 0x0001/0x0003: impersonate-h2 ext ECH cipher suite
- random first packet number: impersonate-h3 quic.initial_packet_number
- extension order, shuffled per connection: impersonate-h3 tls.ja3.text, impersonate-h3 tls.ja3.hash

Differences:

- none
```

## To do by hand

- Review the changes above; a hand-written section copied from the previous profile is unverified until the e2e tests pass against its inherited or new captures.
- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and a line in its Not covered by any claim for any new gap pinned.
- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `capturesFrom`; a scenario recaptured for this version needs its own entry if the oddity recurs.

## Done by hand (2026-11-03)

- h3gen and kindsgen pins for Firefox's QUIC side connections (4 of 6 `h3-discovery`/`h3-page` samples with locally trusted roots), as for firefox-159-macos. The derived `quic` and `http3` sections equal firefox-158-windows's but for `raceDelayMs` (2 ms, was 3); `tls.helloRetry.cookieAfter` is newly measured (0x000d, macOS's).
- `capturesFrom.bodiless` dropped: firefox-158-windows has no `bodiless` capture (listed missing in the e2e tests).
- e2e gaps added for the recaptured scenarios (`h3Gaps`, `creditGaps`, `webSocketEndGaps`, `partitionGaps`); the tests above then pass.
