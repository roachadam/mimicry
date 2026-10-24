# Cookie edge rules in Chrome 155, Firefox 158, Safari 27 and Safari 26, 2026-10-24

What each browser sent on the `cookies-edge` page (step table
`server.CookiesEdgeSteps`, 3 samples each; Safari 26 from the second Mac).
No browser's samples differed on any step. Input for `mimicry/cookies`
(Wave 3); checked end to end by `TestCookiesEdgeMatchesBrowser`.

The page serves `http://` and `https://` on one port: plain requests are the
capture's `H1` entries, TLS requests its HTTP/2 traces.

## Scheme each step went over

| Steps | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| `sch-to-http`, `sch-http-probe` (`http://www.cookies-e.test`) | plain HTTP/1.1 | plain HTTP/1.1 (no HTTPS-First) | **https**, on the page's existing HTTP/2 connection: Safari's automatic HTTPS upgrade tries TLS on the same host and port, and this server answers TLS there |
| `lo-page`, `lo-probe`, `lo-cleanup` (`http://localhost`) | plain | plain | plain (not upgraded) |
| every other step | https (HTTP/2) | https | https |

So the schemeful SameSite question went unanswered for Safari: both its
"cross-scheme" navigations were https → https.

## Prefixes (`prefix-probe`, at `/x/prefix-probe`)

| Cookie (all `Secure`) | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| `__Host-e_ok` (`Path=/`) | kept | kept | kept |
| `__Host-e_nopath` (no `Path`, set at `/`) | dropped | dropped | **kept** |
| `__Host-e_pathx` (`Path=/x`) | dropped | dropped | dropped |
| `__Http-e_http` (`HttpOnly`) | kept | kept | kept |
| `__Http-e_nohttp` (no `HttpOnly`) | **dropped** | **dropped** | kept |
| `__Host-Http-e_hh` (`HttpOnly`) | kept | kept | kept |
| `__Host-Http-e_nohh` (no `HttpOnly`) | **dropped** | **dropped** | kept |

Chrome and Firefox check `__Host-`'s `Path=/` on the attribute sent (RFC
6265bis) and enforce `__Http-`/`__Host-Http-` (`HttpOnly`). Safari checks
the stored path (the default `/` passes) and treats the `__Http-` prefixes
as ordinary names. Every captured `__Http-` cookie was `Secure`, so whether
`__Http-` also needs `Secure` is not on the wire.

## Nameless cookies (`nameless-probe`)

Every browser dropped all five: the control `=e_nlctl=1` as much as
`=__Secure-e_nls=1`, `= __Secure-e_nlss=1`, `=__Host-e_nlh=1` and
`= __Host-e_nlhs=1`. Every value held an `=`, so the capture shows "a
nameless cookie whose value holds `=` is refused" (now a shared rule in the
jar) and cannot show the prefix rule on its own (with or without the
space): the jar keeps the RFC's prefix rule, which the samples agree with.
A nameless `=x` without a second `=` was not captured.

## Order after a value changes (`rot-probe-1`, `rot-probe-2`, path `/r`)

After `e_rot_a`, `b`, `c` set in turn, then `a` set to `2` and `b` to `1`
again:

| | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| `rot-probe-1` | `a=1; b=1; c=1` | same | same |
| `rot-probe-2` | **`b=1; c=1; a=2`** | `a=2; b=1; c=1` | `a=2; b=1; c=1` |

Chrome gives a cookie whose value changes a new creation time; the same
value again keeps its place (`b` did not move). Firefox keeps the creation
time either way. Safari orders same-path host cookies by name, which hides
creation, so its rule is not observable.

## Schemeful SameSite (cookies set from `https://`)

`e_sch_strict` (`Strict`), `e_sch_lax`, `e_sch_unset`, `e_sch_secure`
(`Secure; SameSite=Lax`):

| Step | Chrome | Firefox |
|---|---|---|
| `sch-to-http` (navigation https → http) | `lax; unset` (no `Strict`) | `strict; lax; unset` |
| `sch-http-probe` (same-origin fetch on http) | `strict; lax; unset` | same |
| `sch-to-https` (navigation http → https) | `__Host-…; lax; unset; secure` (no `Strict`) | with `strict` |
| `sch-https-probe` | all | all |

Chrome is schemeful (the jar's behaviour already); Firefox is not. No
browser binds a non-`Secure` cookie to the scheme that set it. Safari: not
captured (above).

## `Secure` over `http://localhost` (`lo-probe`)

| Chrome | Firefox | Safari 27 | Safari 26 |
|---|---|---|---|
| `e_lo_secure=1; e_lo_plain=1` | same | `e_lo_plain=1; e_lo_secure=1` (by name) | **`e_lo_plain=1`** only |

Only `localhost` was captured; other loopback hosts (`*.localhost`,
`127.0.0.0/8`, `::1`) follow the Secure Contexts specification in the jar.

## 200 cookies on one site (`many-probe`)

| | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| Sent | 169: `e_m031` … `e_m199` | same | all 200 |
| Dropped | `e_m000` … `e_m030` (the oldest 31) | same | none |
| Cookie header | 4,223 bytes | 4,223 bytes | 4,998 bytes |

169 is what 180 per site, evicting down to 150 when a cookie makes 181,
gives (Chromium's `kDomainMaxCookies` 180 and `kDomainPurgeCookies` 30;
Firefox's `maxPerHost` 180 and `quotaPerHost` 150; [INFERENCE], from their
source, the wire shows only 169). All 200 were set in one response, so
least-recently-used and oldest-first eviction are the same here; the jar
evicts least recently used, as both sources do. No header was truncated.

## What changed in mimicry

| Rule | Where | Chrome | Firefox | Safari 27 | Safari 26 |
|---|---|---|---|---|---|
| Nameless value holding `=` refused | code (`SetCookies`, `Set`) | all | all | all | all |
| `new_value_renews` | profile | `true` | `false` | — | — |
| `samesite_scheme` | profile | `schemeful` | `schemeless` | `schemeful` (not captured) | same |
| `host_prefix_path` | profile | `attribute` | `attribute` | `stored` | `stored` |
| `http_prefix` | profile | `true` | `true` | `false` | `false` |
| `secure_loopback` | profile | `true` | `true` | `true` | `false` |
| `max_per_site`, `evict_to` | profile | 180, 150 | 180, 150 | none | none |

## Not measured

- Safari's SameSite across `http://` and `https://`: needs the plain page on
  a port where TLS fails (or a host Safari will not upgrade), since its
  HTTPS upgrade reaches TLS on this page's shared port.
- Whether `__Http-` needs `Secure`; a nameless `=x` without a second `=`;
  Safari's `__Host-` without `Path` at a default path other than `/`.
- Loopback hosts other than `localhost`.
- Total cookie limits; Chrome's priority and `Secure`-aware eviction order;
  how partitions count toward the limit (the jar counts each apart).
