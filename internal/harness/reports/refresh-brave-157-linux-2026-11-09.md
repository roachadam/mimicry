# Refresh of brave-157-linux, 2026-11-09

brave 157.1.99.55 on the Linux machine (WSL2 on the Windows PC), full set: new profile, copied from brave-157-macos, the first brave profile on linux (nothing inherited). Made by `go run ./cmd/refresh` (staging `.refresh/20261109-184241`).

## Captures

Every capture names brave-157-linux with one user agent, and the browser's version was 157.1.99.55 before and after the run.

User agent: `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 16s | 57s |
| set-cookie | 3 | 16s | 57s |
| large-settings | 3 | 16s | 57s |
| small-limits | 3 | 16s | 57s |
| huge-table | 3 | 16s | 57s |
| explicit-table | 3 | 16s | 57s |
| slow-idle | 3 | 2m5s | 2m3s |
| http1 | 3 | 16s | 57s |
| http1-upload | 3 | 18s | 57s |
| h2-upload | 3 | 17s | 57s |
| proxy-connect | 3 | 16s | 57s |
| proxy-socks5 | 3 | 16s | 57s |
| proxy-plain | 3 | 16s | 57s |
| proxy-auth | 3 | 25s | 1m33s |
| proxy-plain-auth | 3 | 17s | 57s |
| proxy-https | 3 | 16s | 57s |
| proxy-https-h1 | 3 | 16s | 57s |
| proxy-https-auth | 3 | 25s | 1m33s |
| proxy-https-h1-auth | 3 | 25s | 1m33s |
| kinds | 3 | 21s | 1m33s |
| kinds-http1 | 3 | 21s | 1m33s |
| kinds-plain | 3 | 21s | 1m33s |
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
| nav-redirect-so | 3 | 16s | 1m3s |
| nav-redirect-ss | 3 | 16s | 1m3s |
| nav-redirect-cs | 3 | 16s | 1m3s |
| nav-redirect-http | 3 | 16s | 1m3s |
| nav-redirect-page | 3 | 17s | 1m33s |
| nav-redirect-form | 3 | 20s | 3m33s |
| nav-redirect-click | 3 | 31s | 6m33s |
| nav-redirect-so-cookies | 3 | 16s | 1m3s |
| nav-redirect-ss-cookies | 3 | 16s | 1m3s |
| nav-redirect-cs-cookies | 3 | 16s | 1m3s |
| nav-redirect-http-cookies | 3 | 16s | 1m3s |
| nav-redirect-page-cookies | 3 | 17s | 1m33s |
| nav-redirect-form-cookies | 3 | 21s | 3m33s |
| nav-redirect-click-cookies | 3 | 32s | 6m33s |
| websocket | 3 | 1m47s | 6m33s |
| websocket-h2 | 3 | 23s | 6m33s |
| proxy-websocket | 3 | 34s | 3m33s |
| proxy-websocket-auth | 3 | 35s | 3m33s |
| proxy-websocket-socks5 | 3 | 28s | 3m33s |
| h3-discovery | 3 | 2m46s | 3m48s |
| h3-page | 3 | 40s | 2m3s |
| h3-blackhole | 3 | 2m46s | 3m33s |
| hrr | 3 | 21s | 1m18s |
| urls | 3 | 17s | 5m3s |
| client-cert-ec | 3 | 25s | 9m33s |
| client-cert-rsa | 3 | 25s | 9m33s |
| client-cert-ec-tls12 | 3 | 25s | 9m33s |
| client-cert-rsa-tls12 | 3 | 25s | 9m33s |
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
| h3-early.local-roots | 3 | 2m38s | 4m3s |
| h3-too-early.local-roots | 3 | 2m39s | 4m3s |
| h3-resume-table.local-roots | 3 | 1m56s | 2m48s |
| h3-streams-blocked.local-roots | 3 | 40s | 1m33s |
| bodiless.local-roots | 3 | 30s | 1m33s |
| client-hints.local-roots | 3 | 1m10s | 3m33s |
| total | | 59m19s | 3h22m50s |

Wall time of the capture run (launches, copies and checks included): 59m26s.

## Inherited scenarios

None: the profile is checked against its own captures only.

## Changes from brave-157-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36` → `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36`
- sec-ch-ua: `"Not-A.Brand";v="99", "Brave";v="157", "Chromium";v="157"` → `"Not-A.Brand";v="99", "Brave";v="157", "Chromium";v="157"`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `name`: `"brave-157-macos"` → `"brave-157-linux"`
- `platform`: `"macos"` → `"linux"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

- `headers[3].value`: `"\"macOS\""` → `"\"Linux\""`
- `http1.headers[5].value`: `"\"macOS\""` → `"\"Linux\""`

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic, tls.helloRetry, tls.x25519Reuse, tls.trailingDotSni, tls.clientAuth, http2.openingRecords, http2.goawayReply, http1.uploadRecords; h3gen: quic, http3; kindsgen: kinds, context, variants, emptyBody, clientHints, urls, http3 headers and priorities)

- `clientHints.machine.arch`: `"arm"` → `"x86"`
- `clientHints.machine.platformVersion`: `"26.2.0"` → `""`
- `http3.discovery.fallback.tcpDelayMs.min`: `3` → `2`
- `http3.discovery.resumedDialDelayMs`: `[0,0,0,0,0,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,2,3,3,3,3,3,3,3,3,3,3,4]` → (none)
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
- `quic.loss.pathDegrading.afterPtos`: `3` → `4`
- `variants[0].headers[4].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[0].http1[6].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[1].headers[4].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[2].headers[11].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[3].headers[14].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[4].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[5].headers[11].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[6].headers[14].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[7].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[8].headers[11].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[9].headers[14].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[10].headers[11].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[11].headers[14].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[12].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[13].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[14].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[15].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[16].headers[11].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[17].headers[14].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[18].headers[4].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[19].headers[11].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[20].headers[14].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[21].headers[11].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[22].headers[14].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[23].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[24].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[25].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[26].headers[11].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[27].headers[14].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[28].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[29].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[30].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`
- `variants[31].headers[10].value`: `"\"macOS\""` → `"\"Linux\""`

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
