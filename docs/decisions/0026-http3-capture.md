# 0026: HTTP/3 is captured from the client's own packets

- Status: Accepted
- Date: 2026-10-25
- Commits: (the HTTP/3 capture commit)

## Problem

Before mimicry can speak HTTP/3 as a browser does, the capture server must
show what each browser sends over QUIC: its datagrams and how it pads and
coalesces them, its Initial packets and ClientHello, its transport
parameters, the frames it sends after the handshake (ACK cadence, flow
control credit, connection IDs), its HTTP/3 SETTINGS, QPACK use, request
field sections and priorities, and how it discovers HTTP/3 (Alt-Svc) and
races it against TCP. A QUIC library's server API hides almost all of
this: it hands over decoded requests.

## Options

- **A QUIC library's HTTP/3 server with its tracing.** quic-go's tracing is
  qlog (`qlogwriter`, since v0.55): received frames as JSON events, in the
  library's own vocabulary, without the datagram's bytes, padding as it
  sent it, or the header's bits. Its HTTP/3 server decodes field sections
  with a static-table-only QPACK decoder, so a server that allows a dynamic
  table (Google's does, 64 KiB) cannot be offered, and it does not expose
  the client's control stream or QPACK instructions.
- **Our own QUIC stack.** Full control, but a second implementation of
  loss recovery, flow control and TLS to keep correct for a server whose
  only job is to make browsers finish.
- **quic-go for the transport, everything else recorded by us** (chosen).

## Decision

The harness serves QUIC with quic-go (v0.64.1, harness module only; the
library is untouched) and records from below it:

- A tap on the UDP socket keeps every datagram the client sends, with its
  arrival time. The recorder (`internal/harness/internal/quictrace`) splits each
  into its coalesced packets, removes header and packet protection itself
  (Initial keys from the client's first Destination Connection ID, RFC 9001
  section 5.2; Handshake and 1-RTT keys from the server's TLS key log,
  `tls.Config.KeyLogWriter`; 0-RTT keys derived from the PSK of the ticket
  the ClientHello offers, kept when the server unwraps it, since crypto/tls
  logs no early traffic secret on the server), and parses every frame. Its
  Initial decryption is checked against RFC 9001 appendix A's sample
  packets. The server accepts 0-RTT, so a resuming browser shows what it
  sends before its handshake completes.
- HTTP/3 is our own, on quic-go's streams: the server's SETTINGS come from
  the scenario, every client stream's bytes are parsed as they are read
  (control frames, QPACK encoder and decoder instructions, request frames),
  and field sections are decoded by our QPACK decoder with a dynamic table,
  checked against RFC 9204 appendix B. Responses use the static table only.
- The QUIC listener shares the TLS port number (Alt-Svc `h3=":port"`), and
  each host's certificate is the one its TCP connections get.

Browsers refuse QUIC with a certificate that does not chain to a root built
into them; the capture tool's `-h3-local-roots` lifts that for Chrome
(`--origin-to-force-quic-on` on an unused port: the hosts may use an
unknown root, no origin the page visits is forced) and Firefox
(`network.http.http3.disable_when_third_party_roots_found=false`), and a
capture taken with it records the setting (`Page.Conditions`) and is named
`<scenario>.local-roots`. Without it, the capture shows what the browser
does by default with such a server.

## Evidence

Browser source, read 2026-10-25:
- Chrome: `ProofVerifierChromium` fails a certificate not issued by a known
  root with `ERR_QUIC_CERT_ROOT_NOT_KNOWN` unless its host is among
  `origins_to_force_quic_on`'s hosts (`net/quic/crypto/proof_verifier_chromium.cc`,
  `net/quic/quic_session_pool.cc`), while forcing QUIC is per origin, port
  included (`HttpNetworkSession::ShouldForceQuic`). Alt-Svc is processed
  only when the response's certificate verified without errors
  (`HttpNetworkTransaction`), so Chrome also needs the capture CA trusted
  by the system, as Safari does.
- Firefox: `Http3Session::Authenticated` abandons the connection and
  disables HTTP/3 for the transaction when the chain's root is not built in
  and `network.http.http3.disable_when_third_party_roots_found` is true
  (its default).
- Google's QUIC server (quiche) allows a 64 KiB QPACK table and 100 blocked
  streams by default (`http_constants.h`); the `h3-page` scenario offers
  that, `h3-discovery` no dynamic table.

## Consequences

A capture holds the client's packets frame by frame with no dependency on
how quic-go reports them; a new quic-go version can change how the server
behaves (its ACKs, its flow control, NEW_TOKEN), not what is recorded.
Datagrams are grouped by client address, so a client that migrates its
connection mid-page would show as two. A packet whose keys were never
logged (a handshake abandoned before its secrets) keeps its header and
says why its payload is missing.

## Revisit when

A browser's HTTP/3 needs a server behaviour quic-go cannot give (a
transport parameter, ECN, Retry, version negotiation), or browsers start
discovering HTTP/3 through DNS HTTPS records instead of Alt-Svc, which the
DNS stub does not answer.
