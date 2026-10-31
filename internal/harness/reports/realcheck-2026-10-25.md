# Real-world check, 2026-10-25

mimicry against the real browsers on this Mac (Chrome 155.0.8059.85,
Firefox 158.0.1, Safari 27.0.1, macOS 27.0.1) and Safari 26.2 on the Mac
mini (macOS 26.2), as public fingerprinting services see them, plus a
handful of homepages behind bot-management vendors. Tool:
`go run ./cmd/realcheck` (in `internal/harness/`; see `docs/capturing.md`). Raw
answers stayed in `$TMPDIR` (they hold the public IP).

## Method

- **Echo endpoints**, each visited as a typed URL (mimicry: a plain
  request, i.e. a navigation; browsers: DevTools `Page.navigate`, BiDi
  `browsingContext.navigate`, AppleScript `set URL`):
  - `https://tls.peet.ws/api/all`: JA3, JA4, peetprint, Akamai, HTTP/2
    frames and header list, TCP SYN (p0f). It advertises `h3=":443"` but
    answers no QUIC Initial (quic-go timed out too), so TCP only.
  - `https://tls.browserleaks.com/json`: JA3/JA3N/JA4 (sorted and as sent),
    Akamai. `https://tls.browserleaks.com/http2`: HTTP/2 frames, header
    list in order.
  - `https://fp.impersonate.pro/api/http2` and `/api/http3`: the only
    HTTP/3 echo found. Over HTTP/3 it reports the QUIC ClientHello
    (JA3/JA3N, every extension), transport parameters in sent order, HTTP/3
    SETTINGS and "perk" fingerprint, the field list, and Initial packet
    facts. `/api/http3` is visited after `/api/http2` until it reports
    HTTP/3 (up to 5 tries), as Alt-Svc moves each client.
- **Comparison**: each answer split into fields (extensions by name, ECH
  into cipher suite / enc / payload length, transport parameters into set
  and order, header order and each header's value). Per-connection
  randomness is told apart only where the profile models it and the
  browser's value is among the profile's choices: extension order (Chrome;
  Firefox's QUIC hello), GREASE ECH payload length (Chrome 144/176/208/240)
  and cipher suite (Firefox AES/ChaCha), QUIC transport parameter order
  (Chrome shuffled, Safari rotated), Firefox's random first packet number,
  and fp.impersonate.pro's sorting of the random GREASE version.
- **Sites**: one cookieless homepage GET per client, 3 s apart, browsers
  first, Go's `net/http` last.

## Echo results (final run)

Fields compared / same / random by design / differ. Every browser:
**no differences** after the two fixes below.

| Endpoint | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| tls.peet.ws (h2) | 62 / 59 / 3 / 0 | 58 / 58 / 0 / 0 | 51 / 51 / 0 / 0 | 51 / 51 / 0 / 0 |
| browserleaks tls | 11 / 7 / 4 / 0 | 11 / 11 / 0 / 0 | 11 / 11 / 0 / 0 | 11 / 11 / 0 / 0 |
| browserleaks http2 | 23 / 23 / 0 / 0 | 22 / 22 / 0 / 0 | 18 / 18 / 0 / 0 | 18 / 18 / 0 / 0 |
| impersonate.pro h2 | 53 / 50 / 3 / 0 | 49 / 49 / 0 / 0 | 42 / 42 / 0 / 0 (first batch)¹ | 42 / 42 / 0 / 0 |
| impersonate.pro h3 | 47 / 41 / 6 / 0 | 48 / 43 / 5 / 0 | 41 / 38 / 3 / 0 | 41 / 38 / 3 / 0 |

¹ In the later batch Safari already had Alt-Svc for the host and went to
HTTP/3 on its first new connection, so `/api/http2` came over HTTP/3 and
was not compared; the first batch's HTTP/2 answer matched field for field.

Headline values (browser = mimicry in every row):

| | Chrome 155 | Firefox 158 | Safari 27 / 26 |
|---|---|---|---|
| JA4 | `t13d1517h2_8daaf6152771_cb7bf5808d99` | `t13d1517h2_8daaf6152771_3cbfd9057e0d` | `t13d2013h2_a09f3c656075_7f0f34a4126d` |
| JA3N (sorted) | `bd4930bd9b000ee684830e44bab76fdf` | `f9c6a2c424206e61efb6c1a6325596b5` | `63eaa93caec132011d68ceb96955c1ee` |
| JA3 as sent | varies per connection (shuffle) | `9d42e90b0225e779f03141ddcd699df2` | `ecdf4f49dd59effc439639da29186671` |
| peetprint | `fc97c1cdfb1409c9a9326c1b726d1dee` | `a4bbbc7e3adf3db97d276fd0adb1efc4` | `62b834de729e78a9f0ebd1dd099314a7` |
| Akamai | `1:65536;2:0;4:6291456;6:262144\|15663105\|0\|m,a,s,p` | `1:65536;2:0;4:131072;5:16384\|12517377\|0\|m,p,a,s` | `2:0;3:100;4:2097152;9:1\|10420225\|0\|m,s,a,p` |
| QUIC JA3N | `18555a4ad0bd66d5fd99e1a493aa9f70` | `872c3d8d0635e4167234d6c87532fb0b` | `ae37a74ca5321143271d3f6c6821eaa2` |
| HTTP/2 and HTTP/3 header lists | names, order, values identical | identical (after fix 1) | identical |
| TCP SYN (p0f) | `4:47+?:0:1400:65535,6:mss,nop,ws,nop,nop,ts,sok,eol+1:df,ecn:0` for every client: same macOS stack, mimicry sets no socket options | | |

Values that could legitimately differ did not: the user agents (Chrome's
reduced `155.0.0.0`, `rv:158.0`, `Version/27.0.1`, `Version/26.2`) and
`accept-language` (`en-US,en;q=0.9`, this Mac's locale) are the browsers'.
Only GREASE values, ECH config IDs and keys, client randoms and connection
IDs vary, all left out of the comparison. Side notes: tls.peet.ws writes
JA4's extension count without zero padding for curl (`t13d497h2`), not for
any browser here.

## Fixes

1. **Firefox `alt-used` on port 443** (HTTP/3 header value). Firefox sent
   `alt-used: fp.impersonate.pro`, mimicry `fp.impersonate.pro:443`.
   Firefox leaves the port out when it is the default
   (`nsHttpChannel::BeginConnect`); every capture ran on an unprivileged
   port, so none could show it. Fixed in `internal/h3` (`altUsedValue`).
   No capture or e2e server can listen on 443 (macOS refuses
   `127.0.0.1:443` without root; Linux CI too), so the proof is this
   check (now identical) plus `TestAltUsedPort` (fails without the fix).
2. **Safari 27 transport parameter `0xff080808`**. Real Safari 27 sent
   eight transport parameters, mimicry nine (`0xff080808 = 09`, from the
   morning h3-discovery capture, 3 of 3 connections). Not a port or
   address rule: experiments tonight left it out on loopback on an
   ephemeral port, on loopback on 443, and on the internet, and the
   afternoon edge captures (33 connections) already lacked it. Same Safari
   and macOS build all day: an Apple-side switch. Safari 26 still sends
   `0xff080808 = 07`, on the internet too. Recaptured Safari 27
   `h3-discovery` and regenerated the profile (hellogen, h3gen, kindsgen):
   the parameter is gone, and `ack_delay_constant` moved from 19–24 to
   27–29 (three new samples; a per-connection constant drawn by Safari,
   the range narrower than the browser's). EVIDENCE.md records the switch
   ([EVIDENCE.md, Not covered](../../../profiles/EVIDENCE.md#not-covered-by-any-claim)).

## Protected sites

Status of the first document (browsers: Navigation Timing; Safari reports
none). One request per client per site, 3 s apart.

| Site (vendor) | Chrome | Firefox | Safari 27 / 26 | mimicry (all 4 profiles) | Go net/http |
|---|---|---|---|---|---|
| fp-cf.impersonate.pro/cloudflare/protected (Cloudflare test page) | 403 challenge | 403 challenge | challenge | 403 `cf-mitigated: challenge` | 403 challenge |
| www.g2.com (DataDome behind Cloudflare) | 403 | 403 | captcha page | 403 DataDome captcha | 403 DataDome captcha |
| www.homedepot.com (Akamai) | 200 | 200 | page | **200**, `_abck`, `bm_sz` | **403 Access Denied** |
| www.nike.com (Akamai, Kasada) | 200 | 200 | page | 200, `ak_bmsc` | 200, no `ak_bmsc` |
| www.leboncoin.fr (DataDome) | 200 | 403 | captcha page | 403 DataDome captcha | 403 |
| www.zillow.com (HUMAN) | 200 | 200 | page | **200** | **403 Press & Hold** |
| www.amazon.com (AWS WAF) | 200 | 202 | page after JS | 202 AWS WAF challenge | 200 with captcha |

Chrome's 200s at leboncoin and Amazon came first in the run; everything
after Firefox's request got the challenge. An interleaved A/B, each with a
fresh client (real Chrome on a new profile, then mimicry's Chrome
profile, 8 s apart, three rounds per site, 2026-10-25 23:21–23:24):
leboncoin 403 / 403 every round, Amazon 202 / 202 every round. The
difference was the address's standing at the time, not the fingerprint.
Akamai and HUMAN let every profile through where Go's client was blocked.

## Not covered, or worth knowing

- Safari moves to HTTP/3 only on a new connection; fp.impersonate.pro
  keeps an idle HTTP/2 connection over 200 s, so the first batch's Safari
  stayed on HTTP/2. Later visits (minutes apart) went over HTTP/3 and were
  compared. mimicry's Safari profiles get there by closing idle
  connections between tries.
- Safari's page status and cookies can't be read through AppleScript
  (JavaScript from Apple Events is off), so its site rows show
  titles and text markers only.
- From Firefox's source, not on any wire here: no `alt-used` on anonymous
  (credential-less) or private-browsing requests. Never compared; in
  [EVIDENCE.md, Not covered](../../../profiles/EVIDENCE.md#not-covered-by-any-claim).
- Safari 27's `0xff080808` may come back if Apple flips it again; a
  recapture of `h3-discovery` shows it.

## Verdict

At the layer anti-bot vendors fingerprint (TLS ClientHello, HTTP/2 SETTINGS,
window update, priority and pseudo-header order, header names, order and
values for a navigation, the QUIC ClientHello, transport parameters and
HTTP/3 SETTINGS and fields), every profile is now indistinguishable from
its browser at three public echoes. The two differences found were fixed:
a default-port rule no local capture could reach, and a browser change
after capture. On protected sites mimicry got exactly what the real
browser got when asked side by side, and passed Akamai and HUMAN where
Go's client was blocked.
