# 0032: Certificate pinning and VerifyConnection

- Status: Accepted
- Date: 2026-10-26
- Commits: `10ed0b2`

**Summary.** `Config.Pins` and `Config.VerifyConnection` check the server's
certificate on every TLS and QUIC handshake, resumed ones included, and
before a request reuses another host's coalesced connection; a mismatch
fails the dial with a `*PinError` before anything is sent. The point is to
notice an intercepting proxy before credentials or cookies pass through it,
without changing a byte on the wire. `resp.TLS` is now set, and
`FetchPins` with `cmd/pins` shows which keys to pin (an intermediate plus a
backup is recommended).

## Problem

Automation code pins the sites it talks to, to notice an intercepting
proxy (a corporate one, or a site's own interception of suspected bots)
and stop before credentials or session cookies go through it. tls-client
has `WithCertificatePinning`; mimicry had `Config.RootCAs` only, no hook
on the connection, and `resp.TLS` was never set
([coming from tls-client](../concepts/coming-from-tls-client.md)).
Nothing here is on the wire: a browser does not pin (Chrome's static pins
are for Google's own properties), and checking a certificate sends
nothing. It is a deliberate deviation in what the client accepts, never
in what it sends, so it must not change a byte.

## Options

Where the check runs:

- **`VerifyPeerCertificate`.** crypto/tls skips it on a resumed session,
  and Chrome and Firefox profiles resume (decision 0013): the second
  connection to a host would never be checked. A test that changes the
  pins between connections shows it.
- **After the response, in the Transport.** The request has gone out by
  then, cookies and all, which is what pinning is meant to prevent.
- **`VerifyConnection` in the uTLS config, for TCP and QUIC.** It runs on
  every handshake, resumed ones included (with the chains verified when
  the ticket was issued), after normal verification and before the
  connection carries anything; uTLS sends an alert and the dial fails.
  Chosen. A session ticket is only kept from a connection it accepted,
  since tickets arrive after the handshake, so a resumed session never
  carries a chain that failed. 0-RTT requests on a resumed QUIC
  connection go out before the server's flight is checked; that is the
  ticket's earlier, accepted chain, the same trust a resumed TCP session
  gives.

What a connection is checked for:

- **The handshake's host only.** HTTP/2 coalescing (decision 0023) puts a
  request to host B on host A's connection when A's certificate covers B;
  B's pins would never be consulted. So the check also runs before a
  request takes another host's connection, for that host, with the
  state's `ServerName` set to it; a refusal opens B's own connection
  (whose handshake then fails, or passes, on its own).

Pin forms: HPKP's `sha256/<base64>` of the SubjectPublicKeyInfo (what
pinning libraries and tls-client take), the same hash in hex, and a
certificate's SHA-256 fingerprint in hex (what browsers' certificate
viewers show, colons optional). A hex value could be either hash; it is
compared with both, and a collision between a key's hash and another
certificate's would need SHA-256 broken.

Which certificate counts: any of a verified chain (leaf, intermediate,
root), as HPKP and Chrome's static pins do. The intermediate is
recommended (FetchPins, `cmd/pins`): a leaf's key changes with most
renewals, a root covers everything it signs.

Hosts: a Pin names a host, and with `IncludeSubdomains` the hosts under
it; the host's own Pin wins over a parent's, the nearest parent over
others (HSTS's rule). The same host twice is a configuration error.

## Decision

- `Config.Pins []Pin` (`Pin{Host, IncludeSubdomains, SPKI []string}`),
  parsed in `NewTransport` (a malformed pin is an error there);
  `*PinError{Host, Presented}` on mismatch, `Presented` being the first
  verified chain's keys as `sha256/<base64>`, leaf first.
- `Config.VerifyConnection func(tls.ConnectionState) error` (crypto/tls's
  type, converted from uTLS's; `ExportKeyingMaterial` is not carried),
  run after normal verification and the pins, wherever they run.
- Both run in the uTLS config of every TCP TLS handshake (HTTP/1.1,
  HTTP/2, a WebSocket's own connection, through proxies) and of every
  QUIC handshake, and before coalescing.
- `resp.TLS` is the connection's state on HTTP/1.1, HTTP/2 (WebSocket
  handshakes too) and HTTP/3 responses, shared by the connection's
  responses as net/http shares it.
- `FetchPins(ctx, cfg, url)` handshakes with the profile's hello (normal
  verification, cfg's proxy and dialer, no request, no pins) and returns
  the chain with its pins; `cmd/pins` prints it and marks the
  intermediate nearest the root.

## Evidence

`TestPins` (root): pin match and mismatch over HTTP/2 and HTTP/1.1, the
three forms, subdomains, a resumed connection with changed pins
(`SetPins`, test only), `VerifyConnection` order and resumed calls, a
WebSocket's connection, through the CONNECT proxy (a request's, and since
2026-10-26 a WebSocket's own tunnel), and coalescing on
lookup allowed and refused. `TestHTTP3Pins` (harness, quic-go): an origin
whose QUIC server's key no pin names stays on TCP; one whose pins name
it moves to HTTP/3 with `resp.TLS` from the QUIC connection. Each of the
resumed, coalescing, WebSocket and QUIC checks was removed in turn and
its test failed.

## Consequences

A Transport with pins refuses an intercepting proxy's certificate even
when `RootCAs` trusts its CA. A pin that rotates locks the host out until
the configuration changes: pin an intermediate and a backup. Nothing
changes on the wire; with neither field set no hook is installed beyond
the ticket bookkeeping that was there before.

## Revisit when

Someone needs pins per request or per proxy, or a check on the proxy's
own certificate (no `https://` proxies yet).
