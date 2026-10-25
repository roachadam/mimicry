# HTTP/3

mimicry speaks HTTP/3 over QUIC where the profile's browser would: to an
origin that advertised it, moving to it when and how that browser moves,
with the browser's packets, transport parameters, SETTINGS and field
sections. See decisions [0026](../decisions/0026-http3-capture.md) (how the
browsers were captured) and [0027](../decisions/0027-quic-base.md) (the
QUIC stack), and the
[capture report](../../harness/reports/http3-2026-10-25.md).

It is on by default for a profile with `quic` and `http3` sections (all
four built-in ones). `Config.DisableHTTP3` keeps every request on TCP.

## When a request goes over HTTP/3

Only `https://` requests sent directly (never through a proxy), of a kind
the profile measured over HTTP/3, to an origin whose response carried
`Alt-Svc: h3=…` that has not expired. Then the profile's
`http3.discovery` decides:

- **Chrome (`next_request`):** the first request after the Alt-Svc still
  goes on HTTP/2, and starts the QUIC connection beside it; the requests
  after that use HTTP/3. The HTTP/2 connection stays open, unused.
- **Firefox (`on_alt_svc`):** the response carrying Alt-Svc starts the
  QUIC connection at once, and requests wait for it. Once it is up, the
  HTTP/2 connection gets a GOAWAY (`goaway_tcp`).
- **Safari (`new_connection`):** HTTP/3 only for a request that needs a
  new connection; a live HTTP/2 connection is kept, whatever Alt-Svc says.

Once an origin has used HTTP/3, a request finding no QUIC connection
dials a new one and goes on it (in 0-RTT where the profile resumes), with
one exception: after the server sent GOAWAY, Firefox **races**
(`race_after_goaway`). The request goes on a new TCP connection, and a
0-RTT QUIC connection follows 6–9 ms later, carrying only its control and
QPACK streams; when it is up, the TCP connection gets a GOAWAY. The
Alt-Svc of the response that came with a GOAWAY starts nothing.

A QUIC connection that fails to connect sends the request to TCP and
keeps the origin off HTTP/3 for 5 minutes (Chrome's first back-off; the
browsers were only seen not retrying within a page). The fallback comes
only after the handshake timeout, and a 0-RTT connection that never
hears from the server does not trigger it at all; browsers race TCP
instead [INFERENCE] (open: `harness/reports/quic-interop-2026-10-25.md`).
QUIC resolves the host through `Config.Resolver`; `Config.DialContext`
has no part in it.

## The QUIC connection

Every connection has a UDP socket of its own, as browsers do. What the
browser does is profile data (`quic`, `tls.quic`), each value a rule named
after the browser's algorithm rather than a sampled distribution:

- **First flight:** the QUIC ClientHello (`tls.quic`: the TLS hello with
  the browser's QUIC changes), cut into Initial packets as the browser
  does (`crypto.split`):
  - Chrome: quiche's chaos protection, the hello's head and tail in one
    packet and its middle in the other, split further, with PINGs and
    scattered PADDING, all shuffled;
  - Firefox: neqo's SNI slicing, the hello cut in the middle of the
    server name, the tail piece first; the datagram padded with trailing
    zeros;
  - Safari: in order, 999 bytes in the first packet, PADDING at the end.
- **Packet numbers and connection IDs:** Chrome one counter from 1 over
  every space; Firefox a random first Initial number and a biased random
  DCID length, and four NEW_CONNECTION_IDs; Safari each space from 0.
- **Transport parameters:** the browser's set and values, in its order
  (fixed, shuffled per connection, or Safari's rotation with `0xff080808`
  last), GREASE where it sends one, Chrome's `initial_rtt` when resuming.
- **Afterwards:** coalescing as the browser does, its ACK delays (Safari a
  constant per connection), Chrome's MAX_DATA after 19 ACK-only packets,
  the spin bit (Chrome 0, Firefox random, Safari spinning), NEW_TOKEN
  tokens, Firefox's reply to the server's CONNECTION_CLOSE. An idle
  connection ends silently at its idle timeout.
- **0-RTT:** a connection resumes a ticket from the origin's last one and
  sends its first requests in 0-RTT, where the profile does (Chrome,
  Firefox; Safari never resumed QUIC).

## HTTP/3

From `http3`: the SETTINGS and GREASE setting and frame, which
unidirectional streams open and when (with the Finished, after it, or at
the first QPACK insert), the QPACK encoder's strategy and Huffman rule
(Chrome indexes every field, Firefox inserts only names missing from the
static table, Safari uses the static table only), PRIORITY_UPDATE before
each request with a priority (Chrome), DATA frame sizes, and Safari's
first request waiting until the handshake is confirmed
(`requests_after_confirmed`). Header lists are the request's HTTP/2 list,
with Firefox's HTTP/3 changes (`http3.headers`: pseudo-header order
`:method :scheme :authority :path`, no `te`, `alt-used` placed per kind).
A priority measured only over HTTP/3 (`http3.high`, `http3.low`) is
`ErrNotMeasured` over TCP.

## Not measured

Credit growth under large downloads, key updates the browser starts,
PMTU probing, ECN, migration, DATAGRAM frames, Retry and version
negotiation (Firefox offers QUIC v2 in `version_information`, which
`internal/quic` does not speak), loss-recovery timing (the first PTO
comes from x/net's 333 ms initial RTT), discovery through DNS HTTPS
records, a server that uses
QPACK's dynamic table in responses; for Safari also resumption, 0-RTT,
QPACK with a dynamic table and any request kind but `fetch()` over
HTTP/3. Kinds the profile has no HTTP/3 data for stay on TCP.

## Where the profile data comes from

The h3 captures (`harness/testdata/captures/<name>.h3-{discovery,page}*`)
feed three generators, run in this order: `hellogen` (`tls.quic`),
`h3gen` (`quic`, `http3` but its header lists and priorities), `kindsgen`
(`http3.headers`, `http3.high`, `http3.low`, and the kinds the h3 pages
add). Each refuses a sample its rule does not describe.

## What checks it

- `TestHTTP3MatchesBrowser` walks both h3 pages through mimicry for every
  profile and compares each request's transport, each QUIC connection and
  its HTTP/3 layer with every browser sample. What the browser draws per
  connection is held to the profile's rule, for mimicry's connections and
  for every browser connection alike, so the test is deterministic and a
  rule the captures contradict fails.
- `TestQPACKReproducesBrowserSections` re-encodes every captured field
  list and must reproduce the browser's bytes.
- `TestQUICInterop` (`harness/e2e/quic_interop_test.go`) runs every
  profile against quic-go's HTTP/3 server through a seeded relay
  (`harness/internal/udprelay`: loss, reordering, duplication,
  corruption, delay, blackholes, a small MTU, NAT rebinding): handshake,
  large and concurrent transfers, Retry, resumption and 0-RTT (accepted
  and rejected), ChaCha20, key updates, the anti-amplification limit,
  GOAWAY, idle and server closes, stream resets and tiny flow control
  windows, each checked byte for byte and against a time limit.

## Under loss and failure

Not measured from the browsers; their behaviour is taken from their
source where known ([INFERENCE] otherwise) and changes nothing on a clean
path:

- A PTO sends two probes and resends every unacknowledged CRYPTO byte, so
  a ClientHello longer than a packet goes whole.
- While a request is outstanding the connection sends a PING after
  15 s of silence, or half the idle timeout if shorter (Chrome's ping
  alarm with open streams, Firefox's stream keep-alive); an idle
  connection still ends silently.
- A replayable request whose connection ended before its response (the
  server's CONNECTION_CLOSE crossing it) goes again on a new connection,
  as over a reused HTTP/1.1 connection.
