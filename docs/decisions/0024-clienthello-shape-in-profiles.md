# 0024: The ClientHello's whole shape is profile data

- Status: Accepted; amended 2026-10-27 (`x25519Reuse` added); amended 2026-10-28 (`x25519Reuse` checked on every hello with both shares); amended 2026-11-03 (`helloRetry`: the HelloRetryRequest cookie's slot)
- Date: 2026-10-24
- Commits: `62d9b2a`

**Summary.** The ClientHello's whole shape (record and legacy versions,
session-ID and key-share lengths, each browser's own GREASE ECH, and
`x25519Reuse`) is required profile data, derived by hellogen and checked
against every capture. The parser had dropped these per-browser constants, so
a wrong hello passed e2e and every profile sent BoringSSL's ECH. Every hello
mimicry sends, TCP and QUIC, resumed or not, now matches the browser in these
values; the amendments added `x25519Reuse` and checked it on every hello with
both shares.

## Problem

The capture parser kept extension types, cipher suites and similar
lists. It dropped values that are constant per browser:
- the record version;
- `legacyVersion`;
- the session-ID length;
- the key-share key lengths;
- the GREASE ECH shape.

So a wrong hello passed e2e (the review's repro: zero differences). Every
profile also sent BoringSSL's GREASE ECH, Firefox's included.

## Evidence

Every connection of every capture that records the hello's shape, plus
`.clienthello` samples (new ones with their TLS records, and four older
ones per browser from git history)
([report](../../internal/harness/reports/hello-record-2026-10-24.md)):
- **Record version:** `0x0301` in every browser.
- **`legacyVersion`, session ID, key shares:** `0x0303`, 32 bytes, and
  the key-share lengths are constant. They are the same in resumed hellos.
- **ECH, Chrome:** AES-128-GCM; the payload is one of 144, 176, 208 or
  240 bytes, chosen per connection, resumed or not.
- **ECH, Firefox:** AES-128-GCM or ChaCha20-Poly1305 per connection; the
  payload is 240 bytes full and 400 resumed.
- **ECH, Safari:** none.

## Decision

`profile.TLS` carries:
- `recordVersion`, `legacyVersion`, `sessionIdLength`;
- `keyShareLengths`;
- a typed `ech` (cipher suites and payload lengths as observed sets);
- `resumption.echPayloadLengths` for resumed hellos;
- `x25519Reuse`: one X25519 key in X25519MLKEM768's and x25519's shares
  (added 2026-10-27: Firefox 158 reuses it in all 8 `.clienthello`
  samples, Chrome and Safari in none; widened 2026-10-28 to every hello
  with both shares, resumed and QUIC included, below).

These are required (0008). `internal/harness/cmd/hellogen` derives them from every
capture and refuses a connection that contradicts them; it is never
hand-edited. The parser, `capture.Combine` and `diff.Compare` record and
compare them. The handshake sends them: Firefox's own GREASE ECH,
choosing from the observed sets per connection.

`x25519Reuse` is the exception to `diff.Compare`, which does not see key
bytes: `TestProfilesMatchCaptures` and `capture.Combine` compare it on the
full TCP hellos in `.clienthello`, and since 2026-10-28, when page
captures began keeping each TCP hello's bytes (QUIC captures always
had), `hellogen` refuses any page or QUIC hello with both shares that
disagrees, and `TestResumptionMatchesBrowser` and `TestHTTP3MatchesBrowser`
check mimicry's resumed and QUIC hellos from their bytes. Firefox 158
reuses the key in all 24 TCP hellos of a `resume` capture (14 resumed)
and in all 123 QUIC hellos with both shares (69 resumed); Chrome and
Safari in none. One flag covers every hello because the captures show
no hello kind that differs. Rejected: a key_share extension field (it
would follow the extension into QUIC hellos whose key share is
replaced, Firefox's retry after Version Negotiation, which sends x25519
alone); applying NSS's rule from its source (a lead, not evidence).

## Consequences

`TestProfilesMatchCaptures` and `TestResumptionMatchesBrowser` now see
each of these values. Older captures without records still count for
everything but the record version.

## Revisit when

Firefox's resumed payload is captured against a server issuing tickets
of another size. The 400 bytes probably follow the ticket's size, which
the profile cannot express yet.

## Amendment 2026-11-03: the HelloRetryRequest cookie's slot

A second ClientHello after a HelloRetryRequest with a cookie took the
cookie at a random position among the extensions that are not pinned
(uTLS fork patch). For a fixed-order browser that is a tell: one probe
catches it in 5 of 6 tries, and two catch it outright (#61). The new `hrr`
scenario answers every hello to one host with a HelloRetryRequest carrying a
32-byte cookie and records the second hello:
- **Firefox 158:** the cookie comes after `signature_algorithms`, in 95 of
  95 retries, its fallback hellos included.
- **Safari 27:** after `supported_versions`, in 36 of 36.
- **Chrome 156:** 18 places in 18 retries, inside the GREASE pair.

In every browser, the second hello is otherwise the first one, with a
single key share for the requested group.

`tls.helloRetry` is profile data, derived by hellogen from every retried
connection:
- `cookieAfter` names the extension the cookie follows, in a fixed order.
- `cookieShuffled` puts the cookie in the connection's permutation, as one
  more movable extension.

The fingerprint plan puts an empty cookie in that slot. The uTLS fork
writes nothing for an empty `CookieExtension` and fills it on retry (fork
patch "Let an empty CookieExtension mark the HelloRetryRequest cookie's
slot").

Rejected:
- **An index into the extension list:** it shifts with a dropped
  extension, such as Firefox's fallback hellos or the WebSocket hello.
- **A fork API taking a position:** the placeholder needs no new API, and
  uTLS already filled a `CookieExtension` it found in the spec.

A profile without an `hrr` capture leaves `helloRetry` out. It keeps the
random placement and does not fail with `ErrNotMeasured`: the server
chooses whether to send a HelloRetryRequest, so failing would break real
sites for those profiles (Windows, iOS, Chrome 155, Safari 26). Over QUIC
the placement stays random: no QUIC HelloRetryRequest with a cookie has
been captured.
