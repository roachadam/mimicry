# 0061: A TLS key log on request, never from SSLKEYLOGFILE

- Status: Accepted
- Date: 2026-11-08
- Commits: `c15f44b`

**Summary.** `Config.KeyLogWriter` receives every connection's TLS secrets
in NSS key log format, over TCP and QUIC, so a packet capture of mimicry's
traffic can be decrypted. mimicry never reads `SSLKEYLOGFILE`: the caller
sets the field. Nothing on the wire changes.

## Problem

Debugging a site that refuses mimicry, or checking a profile against a
browser's capture outside the harness, means reading the encrypted part of
the connection: HTTP/2 and HTTP/3 frames, QUIC packets, the handshake after
the ServerHello. Chrome and Firefox write their secrets to the file
`SSLKEYLOGFILE` names, which is how such captures are read; tls-client
(`TransportOptions.KeyLogWriter`) and httpcloak (`WithKeyLogFile`, and
`SSLKEYLOGFILE`) offer the same. mimicry had no way to get them
([#107](https://github.com/roachadam/mimicry/issues/107)).

## Options

- **A `Config.KeyLogWriter io.Writer`** (crypto/tls's name and format).
  The caller chooses where secrets go and when.
- **Also read `SSLKEYLOGFILE` when the field is unset**, as the browsers
  and httpcloak do. Convenient for one-off debugging, but a library is
  embedded in programs whose authors never chose it: a variable set for a
  browser on the same machine, or inherited by a service, would make every
  program built on mimicry write secrets to that file without its author
  knowing. Go's crypto/tls does not read it either.
- **Nothing.** Callers could wrap `DialContext` but not reach the TLS
  layer, and QUIC never goes through it.

## Decision

`Config.KeyLogWriter`, set on the uTLS configuration every handshake uses
(`handshake.TLSConfig`): TCP connections, WebSocket connections and QUIC
handshakes. Unset, nothing is written. `SSLKEYLOGFILE` is not read; a
caller who wants it opens the file and sets the field.

## Evidence

- `TestKeyLogMatchesServer` (`internal/engine/native`): over HTTP/2 and
  HTTP/3, the client's log is exactly the server's (crypto/tls and the QUIC
  server's own key logs): one handshake's four TLS 1.3 secrets. The
  server's secrets are the ones that decrypt the traffic. Setting the QUIC
  handshake's writer to nil fails the HTTP/3 row.
- Code reviewed: tls-client v1.18.0 `client_options.go:20-24`; httpcloak
  v1.7.3 `transport/keylog.go` (reads `SSLKEYLOGFILE`).

## Consequences

- Captures of mimicry's own traffic can be read in Wireshark or tshark
  ([how a request is built](../concepts/how-a-request-is-built.md#6-seeing-what-was-sent)).
- The log decrypts everything it covers, cookies and credentials included;
  the field's documentation says so.
- Callers used to `SSLKEYLOGFILE` from other tools must set the field.

## Revisit when

Callers show a need to switch the key log on without changing code (a
deployed binary being debugged), which an opt-in environment variable read
only by a `cmd/` tool, not the library, could meet.
