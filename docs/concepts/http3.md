# HTTP/3

mimicry speaks HTTP/3 over QUIC where the profile's browser would: to an
origin that advertised it, moving to it when and how that browser moves,
with the browser's packets, transport parameters, SETTINGS and field
sections. See decisions [0026](../decisions/0026-http3-capture.md) (how the
browsers were captured), [0027](../decisions/0027-quic-base.md) (the
QUIC stack) and [0028](../decisions/0028-quic-failure-model.md) (how it
fails), and the
[capture report](../../internal/harness/reports/http3-2026-10-25.md).

It is on by default for a profile with `quic` and `http3` sections (every
built-in one). `Config.DisableHTTP3` keeps every request on TCP.

## When a request goes over HTTP/3

Only `https://` requests sent directly or, with `Config.HTTP3OverSOCKS5`,
through a SOCKS5 proxy (never through an `http://` proxy; see
[Proxies](proxies.md#http3-through-socks5)), of a kind the profile
measured over HTTP/3, to an origin whose response carried
`Alt-Svc: h3=…` that has not expired. Then the profile's
`http3.discovery` decides:

- **Chrome (`next_request`):** the first request after the Alt-Svc still
  goes on HTTP/2, and starts the QUIC connection beside it; the requests
  after that use HTTP/3 once it is up. The HTTP/2 connection stays open,
  unused.
- **Firefox (`on_alt_svc`):** the response carrying Alt-Svc starts the
  QUIC connection at once, as a validation in the background: requests
  go on TCP until it is up, never waiting for it. Once it is up, every
  HTTP/2 connection to the origin gets a GOAWAY (`goawayTcp`), once the
  request that opened it has its response: one still being established
  as QUIC came up gets it too.
- **Safari (`new_connection`):** HTTP/3 only for a request that needs a
  new connection; a live HTTP/2 connection is kept, whatever Alt-Svc says.

Once an origin has used HTTP/3, a request finding no QUIC connection
dials a new one, resumed with 0-RTT where the profile resumes, even with
an HTTP/2 connection open, with one exception: after the server sent
GOAWAY, Firefox **races** (`raceAfterGoaway`). The request goes on a new
TCP connection, and a 0-RTT QUIC connection follows 6–9 ms later,
carrying only its control and QPACK streams. In every sample the TCP
connection is up, the request sent and Firefox's GOAWAY on it out
(3–4 ms after the request, which the capture's local server answers at
once) before QUIC's first datagram, 1 ms after the GOAWAY. So mimicry
starts that QUIC connection no earlier than the request's response on
TCP, however long the TCP handshake takes. On any machine load the
order stays the same. The TCP connection gets its GOAWAY once its
request is answered. Every HTTP/2 connection established while an
origin's QUIC connection is up gets one too. The Alt-Svc of the response
that came with a GOAWAY starts nothing. QUIC resolves the host through
`Config.Resolver`; `Config.DialContext` has no part in it (through a
SOCKS5 proxy the proxy resolves it, and `DialContext` opens the
association's control connection).

### Alternatives a Transport did not learn itself

A Transport keeps the alternatives it learns to itself unless
`Config.AltSvc` gives it an `AltSvcCache`: Transports given the same one
share every alternative any of them learns (or a `clear` removes), as a
browser's tabs do, and `WriteJSON`/`ReadJSON` save and load it, as
Chrome (HTTP server properties) and Firefox (alternate services) keep
theirs across restarts. An origin found there is one whose alternative
the Transport knows: its first request goes by the rules above as if the
Transport had learnt it, so nothing appears on the wire that a Transport
which learnt it itself would not send.

- **Chrome and Safari:** the request needs a new connection and there is
  no HTTP/2 one, so it goes as a request that needs a QUIC connection
  does ([below](#when-quic-fails)): a fresh QUIC connection, with TCP
  racing it after 300 ms (Chrome) or 100 ms (Safari) if it is not up. On
  a working network the page starts on HTTP/3.
- **Firefox:** the Transport learns the alternative at the first request,
  which starts the background validation an `Alt-Svc` response would
  start and itself goes on TCP; the requests after it move once QUIC is
  up (and the HTTP/2 connection gets its GOAWAY). Firefox's own restart
  with a validated alternative it saved was not captured; its source (a
  lead, not evidence) sends the first request on HTTP/3 directly.

Only alternatives are shared: whether an origin's QUIC failed (broken),
session tickets (so a returning Transport's first QUIC connection is a
full handshake, no 0-RTT, as after a browser restart) and connections
stay per Transport. Entries are keyed as the pool is (origin and
connection partition), not by top-level site, which browsers also key
them by; top-level-site partitioning is deferred
([decision 0011](../decisions/0011-connection-partitioning.md)), so one
cache shared by Transports used for unrelated sites tells each the
others' alternatives.

## When QUIC fails

What a request does while its origin's QUIC connection is not up, and
when an attempt or a live connection fails, is
`http3.discovery.fallback`, measured against servers that answer nothing,
go silent mid-request, close and then answer nothing, or speak only QUIC
v2 ([report](../../internal/harness/reports/http3-edge-2026-10-25.md); captures
`h3-blackhole`, `h3-broken`, `h3-vn`). That is what mimicry does with
`Config.BrowserQUICFallback`; by default it shortens the holds, below:

- **Chrome (`race`):** a request that needs a QUIC connection waits for
  the attempt 300 ms (`tcpDelayMs`; 305–306 measured), then goes on TCP
  while the attempt runs on. A request sent in 0-RTT, and every request
  after it, waits for that attempt with no TCP until it gives up
  (`zeroRttHolds`: 4.0 s); then a fresh attempt and TCP start together,
  and the requests made while it runs go on TCP at once.
- **Firefox (`background`):** an attempt never holds a request, but for
  those made while its resumed attempt after the server's close is
  pending (`hold`): they go on it in 0-RTT and to a new TCP connection
  when it gives up (5 s). After a failed resumed attempt or a dead
  connection, a request at least `revalidateMs` later validates afresh
  beside TCP.
- **Safari (`race`):** QUIC first, TCP after 100 ms (`tcpDelayMs`,
  89–129 ms measured), and the attempt is dropped once the request goes
  on TCP (`abandonOnTcp`).

An attempt fails when it gives up (`quic.loss`: Chrome at 4 s with an
Initial CONNECTION_CLOSE padded to 1250 bytes; Firefox silently at its
idle timeout for a fresh one, at 5 s with an Initial application close in
255 bytes for a resumed one), gets Version Negotiation it does not retry
(Chrome, Safari), or is dropped (Safari's, with a 0-RTT close if it sent
0-RTT). Failed
attempts in a row (`brokenAfter`: Chrome and Firefox 1, Safari 2,
resumed ones counting for Safari only) leave the origin on TCP for the
rest of the Transport's life: no browser tried again within a page
(Chrome's source has a 5-minute back-off, not measured). An attempt
counts once its first datagram has gone out: one dropped before (a loaded
process still building its hello when the race ended) put nothing on the
network and leaves no trace ([decision 0028](../decisions/0028-quic-failure-model.md)). Firefox answers
Version Negotiation with a new connection in v2 at once, which it closes
with 0x53f8 when the server's transport parameters lack
`version_information`; that counts as one failed attempt. Chrome's source
also delays its TCP job by 1.5 × the origin's smoothed RTT when it expects
QUIC to work; no loopback capture can tell that from no delay, so it is not
modelled. Firefox's re-validation (`revalidateMs`) is bounded by the
captures: a request 0.8 s after a failure did not start a fresh attempt,
one 3.05 s after did.

A live connection whose path stops answering: Chrome moves to a new UDP
port once its oldest unacknowledged packet is 4 of its PTOs old
(`quic.loss.pathDegrading`, migrate; a socket from the engine's dialer,
`quic.DialConfig.NewPath`, which such a profile requires) and carries on
(when that path dies too, Chrome closes with `85:Network blackhole
detected` and sends the stuck request on TCP; not modelled); Safari closes
it after 12 s and sends the pending requests again on a new connection
8–16 ms later (`pending` quic); Firefox keeps probing and sending requests
on it until its 30 s idle timeout, then sends them all on TCP (`pending`
tcp), opening a TCP connection for each and declining all but the first.

### Fast fallback (the default)

Without `Config.BrowserQUICFallback`, two of the waits above end sooner
([decision 0036](../decisions/0036-fast-quic-fallback.md)); everything else,
the race delays included, is as above:

- **An unanswered 0-RTT connection holds requests only until the race
  delay.** A request on a resumed connection whose handshake the server
  has not answered goes on TCP once the profile's `tcpDelayMs` (Chrome
  305–306 ms) has passed since the connection started, or for a profile
  without one its `quic.loss.initialPtoMs` (Firefox 300 ms), instead of at
  the give-up (Chrome's `zeroRttHolds`, 4 s; Firefox's `hold`, 5 s).
  Requests made after that go on TCP at once. A request already sent in
  0-RTT stays on the QUIC connection, as the browser's would, and its
  answer is dropped; one waiting for the handshake (a POST) is withdrawn
  unsent. The QUIC connection carries on as the browser's: its probes,
  give-up and close, and what follows them (Chrome's fresh attempt beside
  TCP, Firefox's re-validation) are unchanged. If its handshake completes
  late after all, later requests use it, and a Firefox profile then sends
  GOAWAY on the HTTP/2 connection the requests went to (`goawayTcp`); that
  connection gets none while the QUIC connection is overdue.
- **A failed attempt holds nothing.** A request waiting for an attempt
  that failed before the race delay (Version Negotiation not retried)
  goes on TCP at once instead of when the delay ends.

A connection whose server answers its handshake within the race delay is
untouched, so a working network looks the same in both modes. A path
slower than that (round trips over about 300 ms, or a resumed handshake's
first flight lost) sends the request on TCP as well, where the browser
would have waited. The dead-path waits (Firefox's 30 s, Safari's 12 s) keep
the browser's timing: their requests were sent and the path may recover,
so sending them again early would show the server a duplicate (backlog).

## The QUIC connection

Every connection has a UDP socket of its own, as browsers do (through a
SOCKS5 proxy, a UDP association of its own). What the
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
  (fixed, shuffled per connection, or Safari's rotation, Safari 26's with
  `0xff080808` last), GREASE where it sends one, Chrome's `initial_rtt`
  when resuming.
- **Afterwards:** coalescing as the browser does, its ACK delays (Safari a
  constant per connection), Chrome's MAX_DATA after 19 ACK-only packets,
  the spin bit (Chrome 0, Firefox random, Safari spinning), NEW_TOKEN
  tokens, Firefox's reply to the server's CONNECTION_CLOSE. An idle
  connection ends silently at its idle timeout.
- **Long transfers** (`h3-download`, 50 MiB): acknowledgements as the
  browser spaces them (`quic.ack`: Chrome every 10th packet after its
  first 100, every 2nd before; Firefox and Safari every 2nd, batched over
  what has arrived; `Config.HTTP3ACKEvery` acknowledges less often, not as
  the browser does, for a server whose one worker a client saturates:
  [decision 0043](../decisions/0043-http3-ack-rate-opt-in.md)); credit
  from fixed windows, the limit set to bytes
  consumed plus the window once a share of it is used, consumption
  counted packet by packet as the data arrived, whatever the reader
  takes at once (`quic.credit`:
  Chrome half of 6 MiB per stream and of 15 MiB per connection, Firefox a
  quarter of 12 and 24 MiB, Safari half of 2 MiB per stream, its
  connection limit the sum of its streams'); no key update of its own
  (`quic.keyUpdate`): each browser only follows the server's (so against
  a server that never updates, a connection's keys outlive AES-GCM's
  2^23-packet limit after about 10 GB sent).
  Measured detail: Firefox's batched ACKs cover 2–7 packets, Chrome's
  exactly 10 in 97% of ACKs; Safari raises stream credit as its
  application reads (62 B to 2.1 MB apart, median about 1 MB), its
  connection limit first at 15.2–15.9 MB, Safari 27 sending a quarter to
  a third of the raises in other packets; no browser started a key
  update in 3,476–8,619 packets sent and 37,000 received.
- **Versions:** Firefox lists v2 in `version_information` and moves to it
  when the server upgrades it compatibly (RFC 9368), then resumes that
  origin in v2; it answers Version Negotiation that lists v2 with a new
  connection in v2 (`quic.versionNegotiation`). Chrome and Safari stay
  on v1.
- **0-RTT:** a connection resumes a ticket from the origin's last one and
  sends its first requests in 0-RTT, where the profile does (Chrome,
  Firefox), but only those whose method the browser sends there
  (`http3.earlyData.methods`: GET, HEAD and OPTIONS). Any other request
  made while the handshake runs (a POST, PUT, DELETE, a form submission)
  waits for it and leaves in 1-RTT: Firefox's in the datagram with its
  Finished (`heldWithFinished`; 31 of 35, the rest a thread's timing),
  Chrome's in a datagram of its own 0.05–2.1 ms after it. A GET and a
  POST made together split: the GET in 0-RTT, the POST held. A Firefox
  connection dialled for a held request sends its control and QPACK
  streams in 0-RTT without it. A request the server answers `425 Too
  Early` (RFC 8470) goes again on the same connection, new stream, in
  1-RTT after the Finished (`retryTooEarly`; 12 of 12 per browser),
  never on a new one. Safari does the same but holds until the handshake
  is confirmed, and resumes QUIC after closing a connection itself
  (`h3-broken`) or after one idled out (`h3-early`); mimicry does not
  model Safari's resumption: its connections are all fresh.

## HTTP/3

From `http3`: the SETTINGS and GREASE setting and frame, which
unidirectional streams open and when (with the Finished, after it, or at
the first QPACK insert), the QPACK encoder's strategy and Huffman rule
(Chrome indexes every field, Firefox inserts only names missing from the
static table, Safari uses the static table only), PRIORITY_UPDATE before
each request with a priority (Chrome), DATA frame sizes (Firefox caps
them at 32768 bytes, the first at 32224), and Safari's
first request waiting until the handshake is confirmed
(`requestsAfterConfirmed`). Header lists are the request's HTTP/2 list,
with Firefox's HTTP/3 changes (`http3.headers`: pseudo-header order
`:method :scheme :authority :path`, no `te`, `alt-used` placed per kind,
its value the alternative's host with `:<port>` unless the port is 443).
A priority measured only over HTTP/3 (`http3.high`, `http3.low`) is
`ErrNotMeasured` over TCP.

## Not measured

PMTU probing, ECN, DATAGRAM frames, Retry from the browser's side,
discovery through DNS HTTPS records, a server that uses QPACK's dynamic
table in responses, migration by Firefox or Safari (none seen), credit
growth beyond 50 MiB or at lower bandwidth, key updates beyond 37,000
received packets, how long an origin stays broken beyond a page, what
Safari does after more than two failed attempts, a browser restarted with
the alternatives it saved, and a NEW_TOKEN from a v2
connection used by a later one; for Safari also its resumption (above),
QPACK with a dynamic table and any request kind but `fetch()` over
HTTP/3. Kinds the profile has no HTTP/3 data for stay on TCP. Each item,
with its detection relevance and what closing it takes, is in the
[backlog](../backlog.md#http3-and-quic).

## Where the profile data comes from

The h3 captures (`internal/harness/testdata/captures/<name>.h3-{discovery,page}*`)
feed three generators, run in this order: `hellogen` (`tls.quic`),
`h3gen` (`quic`, `http3` but its header lists and priorities), `kindsgen`
(`http3.headers`, `http3.high`, `http3.low`, and the kinds the h3 pages
add). `hellogen` and `h3gen` also read the `h3-blackhole`, `h3-broken`,
`h3-vn`, `h3-download`, `h3-early` and `h3-too-early` captures
(`server.H3EdgeCaptures`): hellogen for `tls.quic.versionNegotiation`
(Firefox's other hello after Version Negotiation), h3gen for
`quic.loss`, `quic.keepAlive`, `quic.ack`, `quic.credit`,
`quic.keyUpdate`, `quic.versionNegotiation`,
`http3.discovery.fallback` and `http3.earlyData`. Each refuses a sample
its rule does not describe.

## What checks it

- `TestHTTP3MatchesBrowser` walks both h3 pages through mimicry for every
  profile and compares each request's transport, each QUIC connection and
  its HTTP/3 layer with every browser sample. What the browser draws per
  connection is held to the profile's rule, for mimicry's connections and
  for every browser connection alike, so the test is deterministic and a
  rule the captures contradict fails.
- `TestHTTP3EdgeMatchesBrowser` (`internal/harness/e2e/http3_edge_test.go`) walks
  the `h3-blackhole`, `h3-broken`, `h3-vn`, (with aioquic) `h3-v2`,
  `h3-early` and `h3-too-early` pages through mimicry on the browser's
  schedule and compares, per request, every connection it went on, how
  long it was held and, on a connection that sent 0-RTT, each of its
  streams (in 0-RTT, held with or after the Finished, or sent again after
  a 425), per QUIC attempt its versions, what the server answered, its
  datagrams and give-up, and when it and each TCP connection started
  after the request that caused them (the race), within the samples'
  range. A migration must send the browser's probes first and come 4 of
  the connection's own PTOs after the request, for mimicry and every
  browser sample, so it holds on a loaded machine too. Each page runs
  with `Config.BrowserQUICFallback` and with the fast default; the
  default's differences are listed one by one (`h3EdgeFastGaps`), all on
  pages where QUIC fails, and each must appear.
- `TestHTTP3DownloadMatchesBrowser` replays `h3-download` and holds
  mimicry's acknowledgements and key phases to the profile's rules, which
  every sample must follow too, and its credit increases to the range of
  the browser's; where the connection limit follows what arrived
  (Safari), how far it moves is timing, so mimicry and every sample are
  held to the rule instead (the limit against what the client's ACKs
  acknowledged, plus the window).
- `TestQPACKReproducesBrowserSections` re-encodes every captured field
  list and must reproduce the browser's bytes.
- `TestQUICInterop` (`internal/harness/e2e/quic_interop_test.go`) runs every
  profile against quic-go's HTTP/3 server through a seeded relay
  (`internal/harness/internal/udprelay`: loss, reordering, duplication,
  corruption, delay, blackholes, a small MTU, NAT rebinding): handshake,
  large and concurrent transfers, Retry, resumption and 0-RTT (accepted,
  rejected, and answered 425 Too Early), ChaCha20, key updates, the
  anti-amplification limit, GOAWAY, idle and server closes, stream resets,
  tiny flow control windows, and a new Transport sharing or loading an
  `AltSvcCache` (its first request on QUIC for Chrome and Safari, with no
  TCP connection), and HTTP/3 through SOCKS5 proxies (`socks5*` rows: no
  datagram around the proxy, the same first flight as directly, a
  dropped or refused association), each checked byte for byte and
  against a time limit.

## Under loss and failure

Measured where a server stopped answering (`quic.loss`,
`quic.keepAlive`); beyond that, RFC 9002:

- An unanswered handshake probes after 300 ms (Chrome, Firefox; from the
  origin's last RTT when it has one), doubling: Chrome sends one
  1250-byte Initial with the next share of its ClientHello and skips a
  packet number before each probe, Firefox its whole first flight again.
  Safari's probes were never seen (TCP won first).
- While a request waits on a quiet connection it sends PINGs
  (`quic.keepAlive`: Chrome and Firefox after 15 s, Safari every 2 s, or
  half the negotiated idle timeout if that is shorter); an idle connection
  still ends silently.
- A replayable request whose connection ended before its response (the
  server's CONNECTION_CLOSE crossing it) goes again on a new connection,
  as over a reused HTTP/1.1 connection; one whose connection died as
  [When QUIC fails](#when-quic-fails) says goes where the profile sends
  it.
