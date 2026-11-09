# 0064: Client certificates as each browser sends them

- Status: Accepted
- Date: 2026-11-08
- Commits: `9864806` and the commit that finished it (#108)

**Summary.** `Config.GetClientCertificate` answers a server's
CertificateRequest the way the profile's browser does, from a new
profile field, `tls.clientAuth`, that hellogen derives from four capture
scenarios: the CertificateVerify scheme per key type and TLS version,
whether a session with a certificate is resumed (Chrome, Edge) or never
(Firefox), and whether the first connection a host and port asks on is
dropped and the request sent again (Chrome, Edge). A profile without the
field, an unmeasured key type and HTTP/3 make a certificate
`ErrNotMeasured`.

## Problem

Mutual TLS was the one TLS feature a caller of tls-client
(`TransportOptions.Certificates`) or crypto/tls could use that mimicry
could not ([#108](https://github.com/roachadam/mimicry/issues/108)).
Sending a certificate is not neutral: the client's flight after the
ServerHello (which certificates, the signature scheme, whether the
session is resumed later, what happens on first contact) is visible to the
server that asked, which is the party fingerprinting it.

## Options

- **crypto/tls's behaviour** (uTLS's default): the chain given, the first
  scheme the server lists that the key can sign, every session resumed.
  Simple, and wrong for every browser: Chrome signs RSA keys with PKCS#1
  v1.5 in TLS 1.2 where Go takes the server's first RSA scheme (PSS from
  a Go server), Firefox never resumes such a session, and Chrome and Edge
  drop the first connection a host asks on.
- **Measure and model each browser** (chosen): capture the browsers
  holding an identity against a server that asks, derive what they do
  into the profile, and refuse what was not measured.
- **For Chrome's dropped first connection, three models.** (a) Ignore
  it: one connection fewer per host than Chrome, the first one being the
  one a server sees answering. (b) Drop the first connection per host
  and port, once per Transport, as Chrome's `SSLClientAuthCache`
  remembers its choice per host and port, and dial again at once
  (chosen). (c) Drop every connection that meets a CertificateRequest
  while a choice for its host is pending, for as long as the browser takes
  to choose. mimicry chooses at once (the callback), so (c)'s window is
  empty and it is (b); waiting out the browser's selection time (15 ms to
  1.7 s, the platform's certificate store) would only slow requests.

## Decision

A profile field, `tls.clientAuth`, derived by hellogen from the
`client-cert-ec`, `client-cert-rsa`, `client-cert-ec-tls12` and
`client-cert-rsa-tls12` captures (`internal/harness/cmd/hellogen/clientauth.go`):

- `tls13`, `tls12`: the CertificateVerify scheme of every connection that
  signed one, per version and the certificate's key; samples that
  disagree fail generation. A key type with no scheme is `ErrNotMeasured`.
  The handshake offers uTLS only that scheme
  (`SupportedSignatureAlgorithms`), so the server's preference cannot
  change it.
- `resume`: whether a connection after one that sent the certificate
  offered a session; every sample must agree. False: sending a
  certificate drops the host's tickets and keeps none from the connection.
- `restart`: true when no first connection to a host that was asked for
  a certificate answered, in any sample, and a later connection carried
  the requests. A Transport then closes the first connection a host and
  port asks on before uTLS answers (closing the socket first, since uTLS
  would send an `internal_error` alert and, in TLS 1.3, its buffered
  change_cipher_spec), and dials the request again, which answers
  (`handshake.ErrRestart`, `native.Engine.dial`). `GetClientCertificate`
  is asked on the second connection only. A first connection that
  answered (Firefox does) makes it false: the browser can answer at once,
  and its dropped connections are speculative ones that never carried a
  request.

Without `GetClientCertificate` nothing changes: a server asking gets an
empty Certificate and no connection is dropped (no capture shows a
browser holding no identity). The certificates go out as the caller
gives them; the browsers sent the leaf alone.

## Evidence

Captured 2026-11-08, three samples per scenario and browser, each a fresh
browser launch holding one identity from a client CA made for the run
(`internal/clientcert`), auto-selected by policy (Chrome, Edge) or
preference (Firefox) ([capturing](../capturing.md#client-certificate-scenarios)):
Chrome 156 on macOS 27 (the capture Mac) and on Windows 10, Chrome 157 on
Linux (WSL2 on the Windows PC), Edge 157 on macOS 26 (the second Mac) and
Windows 10, Firefox 159 on macOS 26, Windows 10 and
Linux. Safari 27 sent an empty Certificate or stalled on a hidden prompt
and is not captured (`Scenario.UnsupportedBy`).

- **Chain.** Every certificate sent was the leaf alone: 143 of 143
  signed handshakes for Chrome, 95 of 95 for Edge, 432 of 432 for Firefox.
- **Schemes.** TLS 1.3: `0x0403` (P-256), `0x0804` (RSA) in all three
  browsers. TLS 1.2: `0x0403` (P-256) in all three; RSA `0x0401` (PKCS#1)
  in Chrome and Edge, `0x0804` in Firefox. No sample disagreed.
- **Resumption.** Chrome and Edge offered and resumed on every later
  connection that had a ticket (289 and 193 resumed), sending no
  certificate; Firefox offered no ticket or PSK on any of its 432
  connections that carried requests, each a full handshake with the
  certificate.
- **The dropped connection.** In every Chrome and Edge sample the first
  connection to each of the three hosts received the CertificateRequest,
  sent nothing and closed (the server read EOF, no alert), and the
  request came on the next connection, with the certificate. The retry
  followed 4–30 ms after the drop on macOS and Linux, 0.4–1.7 s on
  Windows (choosing from the user's store). Chrome dropped one more
  connection to the page's host in every sample, its preconnect (decision
  0023, amendment 2026-10-30): its ClientHello arrived as the first was
  dropped and it was dropped at once, as a choice was still pending. No
  sample had two requests reach a new host together, so (c) above is
  untested beyond that preconnect.
- **Firefox.** In some samples its first connection to a host was dropped
  the same way, in others it answered; 55 dropped and 115 empty-Certificate
  connections carried no request: speculative connections, in no pattern
  across samples.
- `TestClientCertMatchesBrowser` (`internal/harness/e2e`) replays each
  scenario through mimicry with the run's identity and compares, per
  connection, the flight, certificates and scheme; per host the
  connections carrying requests, offering a session, resumed and
  certified; and the dropped connections and how they ended. It fails
  when the restart is turned off (no drops), when the socket is not
  closed before uTLS answers (a change_cipher_spec in the dropped
  connection's flight), when Firefox's sessions are kept (resumed
  connections without the certificate), and when TLS 1.2 takes the TLS 1.3
  scheme (Chrome's RSA `0x0804`). `TestClientCertificate` (root) covers
  `ErrNotMeasured` (no `tls.clientAuth`, a P-384 key), the empty
  Certificate, and `resume` both ways against a Go server.

## Consequences

- A Chrome or Edge Transport makes one more connection per host and port
  that asks for a certificate, as the browser does; a server counting
  connections sees the browser's count, less Chrome's preconnect.
- Profiles without captures (Chrome 155, Firefox 158, Safari, the
  iPhones) cannot send a certificate; refreshing a profile captures the
  four scenarios with the rest (`docs/refreshing-profiles.md`).
- Keys other than P-256 and RSA, and client certificates over HTTP/3,
  fail until captured.

## Revisit when

- A browser is seen sending intermediates, or a key type other than P-256
  or RSA is needed (capture it: add a scenario with that key).
- Safari can be made to send an identity on a capture machine.
- A browser's QUIC handshake is captured answering a CertificateRequest.
- A site is seen telling Chrome's dropped connection apart by its timing
  (mimicry dials again at once).
