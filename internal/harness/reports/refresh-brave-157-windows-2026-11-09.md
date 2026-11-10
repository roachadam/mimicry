# Refresh of brave-157-windows, 2026-11-09

brave 157.1.99.55 on the Windows PC, full set: new profile, copied from brave-157-macos, the first brave profile on windows (nothing inherited). Made by `go run ./cmd/refresh` (staging `.refresh/20261109-win`).

## Captures

Every capture names brave-157-windows with one user agent, and the browser's version was 157.1.99.55 before and after the run.

User agent: `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`

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
| http1-upload | 3 | 18s | 57s |
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
| nav-redirect-form | 3 | 20s | 3m33s |
| nav-redirect-click | 3 | 33s | 6m33s |
| nav-redirect-so-cookies | 3 | 17s | 1m3s |
| nav-redirect-ss-cookies | 3 | 17s | 1m3s |
| nav-redirect-cs-cookies | 3 | 17s | 1m3s |
| nav-redirect-http-cookies | 3 | 17s | 1m3s |
| nav-redirect-page-cookies | 3 | 17s | 1m33s |
| nav-redirect-form-cookies | 3 | 20s | 3m33s |
| nav-redirect-click-cookies | 3 | 33s | 6m33s |
| websocket | 3 | 1m48s | 6m33s |
| websocket-h2 | 3 | 23s | 6m33s |
| proxy-websocket | 3 | 35s | 3m33s |
| proxy-websocket-auth | 3 | 36s | 3m33s |
| proxy-websocket-socks5 | 3 | 29s | 3m33s |
| h3-discovery | 3 | 2m47s | 3m48s |
| h3-page | 3 | 41s | 2m3s |
| h3-blackhole | 3 | 2m47s | 3m33s |
| hrr | 3 | 21s | 1m18s |
| urls | 3 | 18s | 5m3s |
| client-cert-ec | 3 | 34s | 9m33s |
| client-cert-rsa | 3 | 33s | 9m33s |
| client-cert-ec-tls12 | 3 | 34s | 9m33s |
| client-cert-rsa-tls12 | 3 | 33s | 9m33s |
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
| h3-resume-table.local-roots | 3 | 1m57s | 2m48s |
| h3-streams-blocked.local-roots | 3 | 41s | 1m33s |
| bodiless.local-roots | 3 | 31s | 1m33s |
| client-hints.local-roots | 3 | 1m11s | 3m33s |
| proxy-https | 3 | not captured | 0s |
| proxy-https-h1 | 3 | not captured | 0s |
| proxy-https-auth | 3 | not captured | 0s |
| proxy-https-h1-auth | 3 | not captured | 0s |
| total | | 58m57s | 3h17m50s |

Wall time of the capture run (launches, copies and checks included): 59m3s.

## Inherited scenarios

None: the profile is checked against its own captures only.

## Changes from brave-157-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36` → `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`
- sec-ch-ua: `"Not-A.Brand";v="99", "Brave";v="157", "Chromium";v="157"` → `"Not-A.Brand";v="99", "Brave";v="157", "Chromium";v="157"`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `name`: `"brave-157-macos"` → `"brave-157-windows"`
- `platform`: `"macos"` → `"windows"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

- `headers[3].value`: `"\"macOS\""` → `"\"Windows\""`
- `http1.headers[5].value`: `"\"macOS\""` → `"\"Windows\""`

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic, tls.helloRetry, tls.x25519Reuse, tls.trailingDotSni, tls.clientAuth, http2.openingRecords, http2.goawayReply, http1.uploadRecords; h3gen: quic, http3; kindsgen: kinds, context, variants, emptyBody, clientHints, urls, http3 headers and priorities)

- `clientHints.machine.arch`: `"arm"` → `"x86"`
- `clientHints.machine.platformVersion`: `"26.2.0"` → `"10.0.0"`
- `http3.discovery.fallback.tcpDelayMs.max`: `5` → `313`
- `http3.discovery.fallback.tcpDelayMs.min`: `3` → `305`
- `http3.discovery.resumedDialDelayMs`: `[0,0,0,0,0,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,3,3,3,3,3,3,3,3,3,3,4]` → (none)
- `http3.greaseFrame.maxLength`: `3` → `2`
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
- `quic.crypto.head.max`: `86` → `84`
- `quic.transportParameters[13].maxLength`: `15` → `14`
- `variants[0].headers[4].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[0].http1[6].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[1].headers[4].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[2].headers[11].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[3].headers[14].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[4].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[5].headers[11].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[6].headers[14].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[7].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[8].headers[11].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[9].headers[14].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[10].headers[11].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[11].headers[14].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[12].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[13].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[14].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[15].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[16].headers[11].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[17].headers[14].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[18].headers[4].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[19].headers[11].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[20].headers[14].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[21].headers[11].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[22].headers[14].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[23].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[24].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[25].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[26].headers[11].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[27].headers[14].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[28].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[29].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[30].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`
- `variants[31].headers[10].value`: `"\"macOS\""` → `"\"Windows\""`

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
