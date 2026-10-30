# 0013: TLS session resumption as the browser does it

- Status: Accepted
- Date: 2026-10-24
- Commits: `d29d30f`, `89b05bc`

**Summary.** mimicry resumes TLS sessions as the profile's browser does,
from its own ticket store rather than uTLS's cache: Chrome and Firefox use
each TLS 1.3 ticket once with their own resumed hello, follow their TLS 1.2
ticket policy, and Safari never resumes. A client that always does a full
handshake is visible and slower; for a library user, later connections now
resume with no configuration, with tickets kept per host, partition and
proxy.

## Problem

Every mimicry connection was a full handshake. Browsers resume on later
connections to a server they hold a ticket for, so a client that never
does is visible, and slower.

## Evidence

`internal/harness/reports/resume-2026-10-24.md`: Chrome and Firefox resume each new
connection with one ticket, never the same ticket twice, so of three
connections opened together only one resumes. Their resumed ClientHello is
the full one plus `pre_shared_key` last; Firefox also leaves
`session_ticket` out. Neither sends 0-RTT data over TCP (QUIC 0-RTT is
captured and modelled: `quic.zeroRtt`, [0026](0026-http3-capture.md)).
Safari never resumes.

From a TLS 1.2-only server (`resume-tls12`), both put the ticket in the
full hello's `session_ticket`; Chrome reuses a TLS 1.2 ticket for every
new connection, concurrent ones included, while Firefox uses it once and
keeps no ticket a resumed connection receives, so its connections
alternate between resumed and full.

## Options

- uTLS's session cache as is: tickets are reused across connections, and
  the hello is whatever the spec says, so Firefox's dropped extension and
  single use would be wrong.
- mimicry's own store, with the hello built for the connection before the
  handshake.

## Decision

- Profile `tls.resumption` (nil: never resumes): `drop`, the extensions the
  TLS 1.3 resumed hello leaves out (`pre_shared_key` always goes last, RFC
  8446; uTLS writes it there too), and `tls12`, the TLS 1.2 ticket policy:
  `reuse` (Chrome), `single_use` (Firefox), or empty for no TLS 1.2
  resumption.
- `internal/handshake.Tickets` keeps the latest ticket per pool key
  (scheme, host, port, partition, proxy). A TLS 1.3 ticket is taken by one
  connection; tickets the server sends on it replace it. A TLS 1.2 ticket
  follows the policy. `session_ticket` became a generated extension (empty,
  or the TLS 1.2 ticket): profiles record its position only.
- The fingerprint plan builds the resumed hello (same shuffle as a full
  one). A TLS 1.3 ticket past its lifetime, or whose server certificate
  expired, is dropped before the hello is built, so the hello is full
  (review 2026-10-24: uTLS would otherwise decline it after the resumed
  shape was chosen, sending a hello no browser sends). A ticket uTLS still
  will not use leaves `pre_shared_key` out (`OmitEmptyPsk`). TLS 1.2 ticket
  lifetime is unmeasured: only certificate expiry applies.
- Found on the way: the engine made a request wait for another's dial even
  to a server known to speak HTTP/1.1 only, so it opened "parallel"
  connections one after another (and each resumed). Browsers open them at
  once; the engine now does for HTTP/1.1-only servers.

## Consequences

`TestProfilesMatchCaptures` builds a fresh client per sample, as browser
samples were fresh launches. `TestResumptionMatchesBrowser` walks the
resume page through mimicry.

## Revisit when

A browser is captured using 0-RTT over TCP, several tickets at once, or
session-ID (not ticket) resumption with a server that issues no tickets.
