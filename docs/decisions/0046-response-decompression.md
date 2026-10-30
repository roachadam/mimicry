# 0046: Responses are decoded whoever set Accept-Encoding; Config.DisableDecompression keeps them raw

- Status: Accepted
- Date: 2026-10-29
- Commits: `9039936`

**Summary.** A response body in gzip, deflate, Brotli or zstd is decoded
by default, whether the profile or the caller set `Accept-Encoding`: the
header now only changes what the request sends. The old rule, decoding
only when the caller had not set it, turned decoding off silently for
code ported from clients where every request sets it.
`Config.DisableDecompression` returns every body exactly as sent, per
client.

## Problem

mimicry decoded a response only when the profile sent `Accept-Encoding`
and the caller had not set it, on the request or in `Config.Headers`
(net/http's rule: whoever asks for compression decodes it). The use case
is automating user actions on sites, mostly ported from tls-client, whose
codebases set `accept-encoding` on every request as part of the browser's
header literal. Ported as is, every body came back compressed, with
nothing to say why but a porting note ("do not set `Accept-Encoding`").
And a caller who wanted a different `Accept-Encoding` on the wire (one
kind's value, `identity`) had no way to keep decoding.

## Options

- **net/http's rule** (the old one): decode only what the transport
  itself asked for. Sound for net/http, which adds `Accept-Encoding:
  gzip` itself; here the header is the browser's, and the caller who sets
  it is copying the browser, not asking for raw bytes. It ties two
  separate wishes (what goes on the wire, what the body looks like) to
  one header.
- **tls-client's rule**: over HTTP/1.1, decode when the transport added
  `Accept-Encoding` or the caller's value contains the substring `gzip`;
  over HTTP/2, decode whatever the `Content-Encoding` (fhttp
  `transport.go` and `http2/transport.go`, v0.7.1).
  `TransportOptions.DisableCompression` turns it off. Whether a body is
  decoded depends on the protocol and on a substring of a request header:
  `br, zstd` over HTTP/1.1 comes back raw, the same request over HTTP/2
  decoded.
- **Always decode, with a switch** (azuretls's
  `Session.DisableAutoDecompression`, v1.13.2): decode any known
  `Content-Encoding`, whoever set `Accept-Encoding`, and one setting
  returns raw bodies.

## Decision

The maintainer's decision: always decode, with a switch.

- By default a body whose `Content-Encoding` is one of gzip (`x-gzip`
  too), deflate, br or zstd is decoded, whoever set `Accept-Encoding`;
  `Content-Encoding` and `Content-Length` are removed,
  `Response.ContentLength` is -1 and `Response.Uncompressed` true. The
  decompressor opens on the first `Read`, so `RoundTrip` never blocks on
  the body, and a caller that reads only the status starts none.
- `Config.DisableDecompression` returns every body exactly as sent,
  headers intact.
- Any other encoding, and several stacked (`gzip, br` on one line or on
  several), come back raw with their headers: mimicry does not decode
  stacked encodings. HEAD, 204, 304 and empty responses are untouched.
- Per client only: no per-request switch. A caller wanting both builds
  two clients, which cost little (decision 0044).

## Evidence

Browsers decode every encoding they advertise and never hand a page
compressed bytes, so decoding changes nothing on the wire and is no
fingerprint question. The other clients' rules are from their source,
cited above. `TestDecompression` in the root package covers the rule: a
request's and `Config.Headers`' `Accept-Encoding` reach the server and
the body is still decoded; `DisableDecompression`, an unknown encoding
and stacked ones give the server's bytes with `Content-Encoding` and
`Content-Length`; a status read alone never reads the body.

## Consequences

- Code ported from tls-client works with its `accept-encoding` literal
  left in (it still overrides the profile's value on the wire, and
  `Transport.Plan` still names it as an override).
- A caller who relied on setting `Accept-Encoding` to get raw bytes now
  gets decoded ones and needs `Config.DisableDecompression`.
- A server sending an encoding mimicry does not decode (or several
  stacked) hands the caller raw bytes with `Content-Encoding` set, as
  before.

## Revisit when

Callers need raw and decoded bodies from one client often enough for a
per-request switch, or servers are seen sending stacked encodings.
