# 0013: TLS 1.3 session resumption as the browser does it

- Status: Accepted
- Date: 2026-10-24

## Problem

Every mimicry connection was a full handshake. Browsers resume on later
connections to a server they hold a ticket for, so a client that never
does is visible, and slower.

## Evidence

`harness/reports/resume-2026-10-24.md`: Chrome and Firefox resume each new
connection with one ticket, never the same ticket twice, so of three
connections opened together only one resumes. Their resumed ClientHello is
the full one plus `pre_shared_key` last; Firefox also leaves
`session_ticket` out. Neither sends 0-RTT data. Safari never resumes.

## Options

- uTLS's session cache as is: tickets are reused across connections, and
  the hello is whatever the spec says, so Firefox's dropped extension and
  single use would be wrong.
- Our own store, with the hello built for the connection before the
  handshake.

## Decision

- Profile `tls.resumption` (nil: never resumes): `drop`, the extensions the
  resumed hello leaves out. `pre_shared_key` always goes last (RFC 8446;
  uTLS writes it there too).
- `internal/handshake.Tickets` keeps the latest TLS 1.3 ticket per pool
  (scheme, host, port, partition). A new connection takes it, so a ticket
  is used once and concurrent connections resume one at a time; tickets
  the server sends on that connection replace it. TLS 1.2 sessions are not
  kept: the resumed hello is TLS 1.3's.
- The fingerprint plan builds the resumed hello (same shuffle as a full
  one); a ticket uTLS will not use leaves `pre_shared_key` out
  (`OmitEmptyPsk`) and the handshake is full.
- Found on the way: the engine made a request wait for another's dial even
  to a server known to speak HTTP/1.1 only, so it opened "parallel"
  connections one after another (and each resumed). Browsers open them at
  once; the engine now does for HTTP/1.1-only servers.

## Consequences

`TestProfilesMatchCaptures` builds a fresh client per sample, as browser
samples were fresh launches. `TestResumptionMatchesBrowser` walks the
resume page through mimicry.

## Revisit when

A browser is captured using 0-RTT, several tickets at once, or TLS 1.2
resumption against a server that offers only TLS 1.2.
