# Refresh of firefox-159-linux, 2026-11-05

firefox 159.0.1 on the Linux machine (WSL2 on the Windows PC), full set: new profile, copied from firefox-159-macos, the first on linux (nothing inherited). Made by `go run ./cmd/refresh` (staging `.refresh/20261105-113113`). Captured on 2026-11-05 in Ubuntu 24.04, the browser on Xvfb ([0058](../../../docs/decisions/0058-linux-profiles.md)); `h3-v2` failed on Ubuntu's aioquic 0.9, so it and the scenarios after it were captured in a second run with aioquic 1.7 (`.refresh/20261105-124744`) and merged. `h3gen` first refused the captures (Firefox's side connections, as on macOS and Windows, and the samples they changed; a resumed give-up time from one sample at 6.1 s, so `h3-broken` was captured again with six samples, `.refresh/20261105-132522`, which replaced the first three), and the e2e tests failed on gap tables keyed by profile; after those fixes `-resume -realcheck=false` derived, installed and tested. realcheck is from the first resume, with the same profile.

## Captures

Every capture names firefox-159-linux with one user agent, and the browser's version was 159.0.1 before and after the run.

User agent: `Mozilla/5.0 (X11; Linux x86_64; rv:159.0) Gecko/20100101 Firefox/159.0`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 5s | 42s |
| set-cookie | 3 | 4s | 42s |
| large-settings | 3 | 4s | 42s |
| small-limits | 3 | 4s | 42s |
| huge-table | 3 | 4s | 42s |
| explicit-table | 3 | 4s | 42s |
| slow-idle | 3 | 1m22s | 1m48s |
| http1 | 3 | 4s | 42s |
| http1-upload | 3 | 7s | 42s |
| h2-upload | 3 | 5s | 42s |
| proxy-connect | 3 | 4s | 42s |
| proxy-socks5 | 3 | 4s | 42s |
| proxy-plain | 3 | 4s | 42s |
| proxy-auth | 3 | 15s | 1m18s |
| proxy-plain-auth | 3 | 6s | 42s |
| kinds | 3 | 11s | 1m18s |
| kinds-http1 | 3 | 10s | 1m18s |
| kinds-plain | 3 | 10s | 1m18s |
| cors | 3 | 22s | 1m48s |
| cookies | 3 | 5s | 2m18s |
| resume | 3 | 13s | 1m18s |
| resume-tls12 | 3 | 13s | 1m18s |
| cookies-edge | 3 | 6s | 1m48s |
| cors-edge | 3 | 6s | 1m18s |
| cors-edge-cookies | 3 | 5s | 1m18s |
| connections | 3 | 15s | 1m3s |
| headers-order | 3 | 6s | 1m33s |
| cookies-edge2 | 3 | 7s | 1m48s |
| redirects2 | 3 | 40s | 2m33s |
| redirects2-cookies | 3 | 39s | 2m33s |
| nav-redirect-so | 3 | 4s | 48s |
| nav-redirect-ss | 3 | 4s | 48s |
| nav-redirect-cs | 3 | 5s | 48s |
| nav-redirect-http | 3 | 4s | 48s |
| nav-redirect-page | 3 | 7s | 1m18s |
| nav-redirect-form | 3 | 14s | 3m18s |
| nav-redirect-click | 3 | 26s | 6m18s |
| nav-redirect-so-cookies | 3 | 5s | 48s |
| nav-redirect-ss-cookies | 3 | 4s | 48s |
| nav-redirect-cs-cookies | 3 | 4s | 48s |
| nav-redirect-http-cookies | 3 | 5s | 48s |
| nav-redirect-page-cookies | 3 | 7s | 1m18s |
| nav-redirect-form-cookies | 3 | 15s | 3m18s |
| nav-redirect-click-cookies | 3 | 26s | 6m18s |
| websocket | 3 | 11s | 6m18s |
| websocket-h2 | 3 | 17s | 6m18s |
| proxy-websocket | 3 | 16s | 3m18s |
| proxy-websocket-auth | 3 | 17s | 3m18s |
| proxy-websocket-socks5 | 3 | 16s | 3m18s |
| h3-discovery | 3 | 2m35s | 3m33s |
| h3-page | 3 | 29s | 1m48s |
| h3-blackhole | 3 | 2m35s | 3m18s |
| hrr | 3 | 9s | 1m3s |
| urls | 3 | 22s | 4m48s |
| replay-github.com | 3 | 41s | 40s |
| replay-nike.com | 3 | 1m13s | 1m11s |
| replay-nytimes.com | 3 | 1m21s | 1m18s |
| replay-ticketmaster.com | 3 | 1m18s | 1m16s |
| replay-walmart.com | 3 | 1m14s | 1m13s |
| replay-zillow.com | 3 | 1m0s | 59s |
| h3-discovery.local-roots | 3 | 2m35s | 3m33s |
| h3-page.local-roots | 3 | 29s | 1m48s |
| h3-broken.local-roots | 6 | 3m41s | 4m33s |
| h3-v2.local-roots | 3 | 2m13s | 2m48s |
| h3-vn.local-roots | 3 | 1m8s | 1m48s |
| h3-download.local-roots | 3 | 43s | 2m33s |
| h3-early.local-roots | 3 | 2m27s | 3m48s |
| h3-too-early.local-roots | 3 | 2m27s | 3m48s |
| h3-streams-blocked.local-roots | 3 | 28s | 1m18s |
| bodiless.local-roots | 3 | 19s | 1m18s |
| client-hints.local-roots | 3 | 58s | 3m18s |
| total | | 40m33s | 2h19m5s |

Wall time of the capture run (launches, copies and checks included): 55m43s.

## Inherited scenarios

None: the profile is checked against its own captures only.

## Changes from firefox-159-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:159.0) Gecko/20100101 Firefox/159.0` → `Mozilla/5.0 (X11; Linux x86_64; rv:159.0) Gecko/20100101 Firefox/159.0`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `capturesFrom`: `{"bodiless":"firefox-158-macos","connections":"firefox-158-macos","cookies":"firefox-158-macos","cookies-edge":"firefox-158-macos","cookies-edge2":"firefox-158-macos","cors":"firefox-158-macos","cors-edge":"firefox-158-macos","cors-edge-cookies":"firefox-158-macos","explicit-table":"firefox-158-maco…` → (none)
- `name`: `"firefox-159-macos"` → `"firefox-159-linux"`
- `platform`: `"macos"` → `"linux"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

No change.

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic; h3gen: quic, http3; kindsgen: kinds, context, variants, http3 headers and priorities)

- `http3.discovery.dialDelayMs`: (none) → `[0,0,0,0,1,1,1,1,2,2,3,6]`
- `http3.discovery.fallback.revalidateMs.max`: `3020` → `3000`
- `http3.discovery.fallback.revalidateMs.min`: `797` → `0`
- `http3.discovery.raceDelayMs.max`: `6` → `3`
- `http3.discovery.raceDelayMs.min`: `6` → `2`
- `http3.discovery.resumedDialDelayMs`: `[0,0,1,1,1,2,2,2,2,3,3,4,4,4,4,4,4,4,4,5,5,5,5,5,5,5,5,5,5,5,5,5,5,5,6,6,6,6,6,6,6,6,7,7,8,8,9,34]` → `[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,1,1,1,1,1,1,1,1,1,1,1,2,2,2,2,2,2,2,2,2,2,3,3,3,3,3,3,4,4]`
- `http3.firstDataFrame`: `32224` → `32238`

### Hand-written, copied from the previous profile

No change.

## Deprecation

No firefox profile beyond the newest 5 supported ones; none deprecated.

## Verification

Tests (`MIMICRY_PROFILES=firefox-159-linux go test -count=1 ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/ (harness), go test -count=1 ./profiles/ ./cmd/profilegen/ (library)`, 3m52s): passed. Output: `.refresh/20261105-113113/firefox-linux/tests.log`.

realcheck (`realcheck -browsers firefox -profiles firefox-159-linux (on the Linux machine (WSL2 on the Windows PC))`, 1m20s): ran. Output: `.refresh/20261105-113113/firefox-linux/realcheck/compare.md`.

```
# Echo endpoints: browser vs mimicry

## browser-firefox-159-linux vs firefox-159-linux

| Endpoint | Browser proto (tries) | mimicry proto (tries) | Fields | Same | Random by design | Differ | One side only |
|---|---|---|---|---|---|---|---|
| peet | h2 (1) | h2 (1) | 58 | 58 | 0 | 0 | 0 |
| browserleaks-tls |  (1) |  (1) | 11 | 11 | 0 | 0 | 0 |
| browserleaks-h2 |  (1) |  (1) | 22 | 21 | 0 | 0 | 1 |
| impersonate-h2 | h2 (1) | h2 (1) | 49 | 48 | 1 | 0 | 0 |
| impersonate-h3 | h3 (1) | h3 (2) | 48 | 44 | 4 | 0 | 0 |

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

- GREASE ECH cipher suite, one of 0x0001/0x0001, 0x0001/0x0003: impersonate-h2 ext ECH cipher suite, impersonate-h3 ext ECH cipher suite
- random first packet number: impersonate-h3 quic.initial_packet_number
- extension order, shuffled per connection: impersonate-h3 tls.ja3.text, impersonate-h3 tls.ja3.hash

Differences:

- browserleaks-h2 `frame 3` (browser only): browser `{"id":8,"length":4,"name":"WINDOW_UPDATE","stream_id":3,"window_size_increment":12451840}`, mimicry –
```

## To do by hand

- Review the changes above; a hand-written section copied from the previous profile is unverified until the e2e tests pass against its inherited or new captures.
- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and a line in its Not covered by any claim for any new gap pinned.
- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `capturesFrom`; a scenario recaptured for this version needs its own entry if the oddity recurs.
