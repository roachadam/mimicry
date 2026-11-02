# Refresh of firefox-158-windows, 2026-11-01

firefox 158.0.1 on the Windows PC, full set: new profile, copied from firefox-158-macos, the first on windows (nothing inherited). Made by `go run ./cmd/refresh` (staging `.refresh/20261101-222714`).

## Captures

Every capture names firefox-158-windows with one user agent, and the browser's version was 158.0.1 before and after the run.

User agent: `Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:158.0) Gecko/20100101 Firefox/158.0`

| Scenario | Samples | Measured | Estimated |
|---|---|---|---|
| page | 3 | 4s | 42s |
| set-cookie | 3 | 4s | 42s |
| large-settings | 3 | 4s | 42s |
| small-limits | 3 | 4s | 42s |
| huge-table | 3 | 4s | 42s |
| explicit-table | 3 | 4s | 42s |
| slow-idle | 3 | 1m25s | 1m48s |
| http1 | 3 | 4s | 42s |
| http1-upload | 3 | 7s | 42s |
| h2-upload | 3 | 4s | 42s |
| proxy-connect | 3 | 4s | 42s |
| proxy-socks5 | 3 | 4s | 42s |
| proxy-plain | 3 | 4s | 42s |
| proxy-auth | 3 | 15s | 1m18s |
| proxy-plain-auth | 3 | 6s | 42s |
| kinds | 3 | 10s | 1m18s |
| kinds-http1 | 3 | 10s | 1m18s |
| kinds-plain | 3 | 9s | 1m18s |
| cors | 3 | 22s | 1m48s |
| cookies | 3 | 5s | 2m18s |
| resume | 3 | 14s | 1m18s |
| resume-tls12 | 3 | 14s | 1m18s |
| cookies-edge | 3 | 5s | 1m48s |
| cors-edge | 3 | 4s | 1m18s |
| cors-edge-cookies | 3 | 4s | 1m18s |
| connections | 3 | 15s | 1m3s |
| headers-order | 3 | 5s | 1m33s |
| cookies-edge2 | 3 | 5s | 1m48s |
| redirects2 | 3 | 39s | 2m33s |
| redirects2-cookies | 3 | 38s | 2m33s |
| nav-redirect-so | 3 | 4s | 48s |
| nav-redirect-ss | 3 | 4s | 48s |
| nav-redirect-cs | 3 | 4s | 48s |
| nav-redirect-http | 3 | 4s | 48s |
| nav-redirect-page | 3 | 6s | 1m18s |
| nav-redirect-form | 3 | 10s | 3m18s |
| nav-redirect-click | 3 | 24s | 6m18s |
| nav-redirect-so-cookies | 3 | 4s | 48s |
| nav-redirect-ss-cookies | 3 | 4s | 48s |
| nav-redirect-cs-cookies | 3 | 4s | 48s |
| nav-redirect-http-cookies | 3 | 4s | 48s |
| nav-redirect-page-cookies | 3 | 7s | 1m18s |
| nav-redirect-form-cookies | 3 | 10s | 3m18s |
| nav-redirect-click-cookies | 3 | 23s | 6m18s |
| websocket | 3 | 10s | 6m18s |
| websocket-h2 | 3 | 16s | 6m18s |
| proxy-websocket | 3 | 13s | 3m18s |
| proxy-websocket-auth | 3 | 15s | 3m18s |
| proxy-websocket-socks5 | 3 | 13s | 3m18s |
| h3-discovery | 3 | 2m45s | 3m33s |
| h3-page | 3 | 28s | 1m48s |
| h3-blackhole | 3 | 2m47s | 3m18s |
| replay-github.com | 3 | 41s | 40s |
| replay-nike.com | 3 | 1m13s | 1m11s |
| replay-nytimes.com | 3 | 1m22s | 1m18s |
| replay-ticketmaster.com | 3 | 1m19s | 1m16s |
| replay-walmart.com | 3 | 1m15s | 1m13s |
| replay-zillow.com | 3 | 1m0s | 59s |
| h3-discovery.local-roots | 3 | 2m44s | 3m33s |
| h3-page.local-roots | 3 | 28s | 1m48s |
| h3-broken.local-roots | 3 | 4m22s | 4m33s |
| h3-v2.local-roots | 3 | 2m31s | 2m48s |
| h3-vn.local-roots | 3 | 1m7s | 1m48s |
| h3-download.local-roots | 3 | 42s | 2m33s |
| h3-early.local-roots | 3 | 2m28s | 3m48s |
| h3-too-early.local-roots | 3 | 2m28s | 3m48s |
| h3-streams-blocked.local-roots | 3 | 28s | 1m18s |
| total | | 39m40s | 2h8m38s |

Wall time of the capture run (launches, copies and checks included): 39m45s.

## Inherited scenarios

None: the profile is checked against its own captures only.

## Changes from firefox-158-macos

Version strings and the navigation's accept-encoding, rewritten in the previous profile before comparing (`capture.NewRewriter`):

- user-agent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:158.0) Gecko/20100101 Firefox/158.0` → `Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:158.0) Gecko/20100101 Firefox/158.0`
- accept-encoding: `gzip, deflate, br, zstd` → `gzip, deflate, br, zstd`

Lists the browser shuffles on every connection (the QUIC transport parameters, their shuffled versions) are compared as sets.

### Name, version, inheritance

- `name`: `"firefox-158-macos"` → `"firefox-158-windows"`
- `platform`: `"macos"` → `"windows"`

### From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)

No change.

### Generated (hellogen: the hello's shape, tls.websocketH1, tls.quic; h3gen: quic, http3; kindsgen: kinds, context, variants, http3 headers and priorities)

- `http3.discovery.fallback.revalidateMs.max`: `3020` → `5675`
- `http3.discovery.fallback.revalidateMs.min`: `797` → `47`
- `http3.discovery.raceDelayMs.max`: `9` → `3`
- `http3.discovery.raceDelayMs.min`: `6` → `3`
- `http3.discovery.resumedDialDelayMs`: `[0,0,1,1,1,2,2,2,2,3,3,4,4,4,4,4,4,4,4,5,5,5,5,5,5,5,5,5,5,5,5,5,5,5,6,6,6,6,6,6,6,6,7,7,8,8,9,34]` → (none)
- `http3.firstDataFrame`: `32224` → `32228`
- `kinds.async_script.variants[1].site`: `"cross-site"` → (none)
- `kinds.async_script.variants[2].site`: (none) → `"cross-site"`
- `kinds.defer_script.variants[1].site`: `"cross-site"` → (none)
- `kinds.defer_script.variants[2].site`: (none) → `"cross-site"`
- `kinds.injected_script.variants[1].site`: `"cross-site"` → (none)
- `kinds.injected_script.variants[2].site`: (none) → `"cross-site"`
- `kinds.preload_script.variants[1].site`: `"cross-site"` → (none)
- `kinds.preload_script.variants[2].site`: (none) → `"cross-site"`
- `kinds.script.variants[1].site`: `"cross-site"` → (none)
- `kinds.script.variants[2].site`: (none) → `"cross-site"`
- `quic.loss.cachedRtt`: `true` → (none)
- `quic.loss.resumed.afterMs`: `5000` → `9800`

### Hand-written, copied from the previous profile

- `websocket.closeH1`: (none) → `"after_wait"`, `websocket.closeWaitMs`: (none) → `1000`, by hand after the derive: Firefox on Windows closed every HTTP/1.1 socket's connection 1002–1095 ms after the close handshake, every sample (`profiles/EVIDENCE.md`, WebSockets).

## Deprecation

No firefox profile beyond the newest 5 supported ones; none deprecated.

## Verification

The first test run (by the tool, before the gap tables and the e2e tests knew the profile) failed; after the changes below, `MIMICRY_PROFILES=firefox-158-windows go test -race ./e2e/` and `MIMICRY_PROFILES=all go test -run TestRegenerateReproducesProfiles ./cmd/h3gen/ ./cmd/hellogen/ ./cmd/kindsgen/` pass.

What the failures were, and what was done:

- **The unused opening connection** (131 of 135 page samples opened directly over HTTPS): numbering and counts in the connections, h3-discovery and edge tests; not modelled (decision 0023, amendment), left out of the browser's pages (`unusedOpenings`); its extra TLS 1.2 ticket's resumption pinned (`resumeCountGaps`).
- **The gaps macOS Firefox has:** `headersOrderGaps`, `partitionGaps` (now a gap one sample shows suffices: 2 of 3 on Windows), `proxyPlainGaps`, `redirectsMerged`, `redirectsLineGaps`, the FontFace `h3Gaps`, the edge gaps (resumed probes, TCP beside a 425, STREAMS_BLOCKED per raise, burst order, fast fallback).
- **Modelled:** `websocket.closeH1` (above).
- **Pinned on Windows only:** the cross-site HTTP/2 socket's RST_STREAM CANCEL (`webSocketEndGaps`), `/big`'s credit (`creditGaps`), one first Initial's 1-byte packet number (`h3Gaps`), the pair's POST after the Finished (`h3EdgeSchedulingGaps`), and keep2.cs's late page timer (`h3EdgeGaps`).
- **h3-streams-blocked's 323/645 ms against the browser's 0–2 ms:** which of a burst's requests waits is the replay's scheduling (the burst order gap, as on macOS). The sorted waits pass but come 10–60 ms after Firefox's on macOS and Windows alike: Firefox acknowledges at once after two packets, counting the server's ACK-only ones, where `quic.ack` counts ack-eliciting packets only (Not covered: Firefox's ACK of a lone response).

realcheck (`realcheck -browsers firefox -profiles firefox-158-windows`, on the Windows PC, 2026-11-02): echo endpoints that answered show no difference (browserleaks-h2 22 of 22 fields, impersonate-h2 49 with 1 random by design, impersonate-h3 48 with 6); tls.peet.ws refused connections, and browserleaks' TLS page gave the browser no JSON. The protected sites challenged mimicry where they need JavaScript, as for every profile.

## To do by hand

Done: `profiles/EVIDENCE.md` (the profile's paragraph and Not covered lines), decision 0047's and 0023's amendments, the README and concept docs.
