# Client certificates

A server that asks for a client certificate (mutual TLS) gets one from
`Config.GetClientCertificate`, sent the way the profile's browser sends
its own.

```go
cert, err := tls.LoadX509KeyPair("client.pem", "client-key.pem")
if err != nil {
	return err
}
transport, err := mimicry.NewTransport(mimicry.Config{
	Profile: profiles.ChromeMacOS(),
	GetClientCertificate: func(host string, req *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		if host != "api.bank.example" {
			return nil, nil // no certificate for anyone else
		}
		return &cert, nil
	},
})
```

The callback has crypto/tls's shape, with the host the connection is for.
It runs once per handshake that is asked for a certificate, and not on a
resumed one (the server does not ask). Its error fails the request.

## What goes out

- **The chain you give.** Every captured browser sent its certificate
  alone, never the CA that issued it. mimicry sends the certificates in
  `tls.Certificate.Certificate` as given: give the leaf alone to look like
  a browser. A chain with intermediates is sent with them; no capture shows
  what a browser does with one (it never had one to send).
- **The browser's signature.** The CertificateVerify is signed with the
  scheme the browser used for that key type and TLS version
  (`tls.clientAuth`): ECDSA P-256 with `ecdsa_secp256r1_sha256`
  everywhere; RSA with `rsa_pss_rsae_sha256`, except that Chrome and Edge
  sign with PKCS#1 v1.5 (`rsa_pkcs1_sha256`) in TLS 1.2. Whatever the
  server lists first does not change it.
- **No certificate.** A nil certificate, or one with no chain, sends an
  empty Certificate message, as a browser holding none. Without
  `GetClientCertificate` every server that asks gets that.

## Chrome and Edge: the first connection is dropped

The first time a host and port asks, Chrome and Edge close that
connection without answering (no Certificate, no alert), choose the
certificate, and send the request again on a new connection, which sends
it. Later connections to that host and port answer at once. A Transport
does the same (`tls.clientAuth.restart`): once per host and port, for the
Transport's lifetime; `GetClientCertificate` is called on the new
connection only. Firefox answers on the first connection.

The browsers took a few milliseconds to 1.7 s choosing (Windows'
certificate store is the slow one); mimicry dials again at once.

## Resumption

- **Chrome and Edge** resume a session in which they sent a certificate:
  later connections offer its ticket and send no certificate, as any
  resumed connection.
- **Firefox** never offers to resume such a session: every connection to
  the host makes a full handshake and sends the certificate again. A
  Transport with a Firefox profile drops the host's tickets once it sends
  a certificate and keeps none from that connection.

## Limits

- **Measured profiles only.** A profile without `tls.clientAuth` (Safari,
  the iPhone profiles, Chrome 155, Firefox 158) fails a request whose
  server asks with an error wrapping `ErrNotMeasured` when
  `GetClientCertificate` returns a certificate. Safari could not be made
  to send an identity on the capture machines.
- **Key types.** Only ECDSA P-256 and RSA (measured with 2048-bit keys)
  have a measured scheme; any other key (P-384, P-521, Ed25519) is
  `ErrNotMeasured`.
- **HTTP/3.** No browser was captured sending a client certificate over
  QUIC: a certificate asked for there is `ErrNotMeasured`. Set
  `DisableHTTP3` for such a host.
- **Firefox's speculative connections.** Firefox opens extra connections
  that carry no request, some answering with an empty Certificate; mimicry
  opens none.
- **Chrome's preconnect** to a page's host meets the CertificateRequest
  first and is dropped too, so Chrome drops two connections there where
  mimicry drops one ([decision 0023](../decisions/0023-connection-limits-and-coalescing.md#amendment-2026-10-30-no-preconnects)).

See [decision 0064](../decisions/0064-client-certificates.md) for the
evidence.
