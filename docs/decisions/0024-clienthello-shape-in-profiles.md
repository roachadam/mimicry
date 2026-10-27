# 0024: The ClientHello's whole shape is profile data

- Status: Accepted
- Date: 2026-10-24
- Commits: (the wave-3 commit)

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
([report](../../harness/reports/hello-record-2026-10-24.md)):
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
- `resumption.echPayloadLengths` for resumed hellos.

These are required (0008). `harness/cmd/hellogen` derives them from every
capture and refuses a connection that contradicts them; it is never
hand-edited. The parser, `capture.Combine` and `diff.Compare` record and
compare them. The handshake sends them: Firefox's own GREASE ECH,
choosing from the observed sets per connection.

## Consequences

`TestProfilesMatchCaptures` and `TestResumptionMatchesBrowser` now see
each of these values. Older captures without records still count for
everything but the record version.

## Revisit when

Firefox's resumed payload is captured against a server issuing tickets
of another size. The 400 bytes probably follow the ticket's size, which
the profile cannot express yet.
