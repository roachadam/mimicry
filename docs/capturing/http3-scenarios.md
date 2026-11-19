# HTTP/3 scenarios

`h3-discovery` and `h3-page` also serve QUIC on the TLS port's UDP twin and
advertise it with `Alt-Svc: h3=":<port>"; ma=86400` on every response; the
browser discovers HTTP/3 itself (decision 0026). The page's capture adds
`quic`: each QUIC connection's datagrams with every coalesced packet's
header and frames (the recorder, `internal/quictrace`, decrypts them:
Initial keys from the client's first Destination Connection ID, Handshake
and 1-RTT keys from the server's TLS key log, 0-RTT keys from the resumed
ticket's PSK), its ClientHello and transport parameters, its HTTP/3
streams, frames and QPACK instructions, and how it closed; TCP connections
carry `ms` (when their ClientHello arrived) on the same clock, so a race
shows.

Chrome and Firefox refuse QUIC with a certificate that does not chain to a
built-in root: by default the capture records the attempt.
`-h3-local-roots` lifts the refusal (Chrome: `--origin-to-force-quic-on`
with the page's hosts on unused port 1, which forces nothing the page
visits; Firefox:
`network.http.http3.disable_when_third_party_roots_found=false`), records
it in the page's `conditions` and names the capture
`<name>.<scenario>.local-roots.pagetrace.json.gz`. Chrome also ignores
Alt-Svc unless the certificate verified, so it needs the capture CA trusted
by the system, as Safari does (the tool warns).

Failure, version, transfer, early-data and stream-limit scenarios:

- `h3-blackhole`: Alt-Svc names a UDP port that records every datagram and
  answers none.
- `h3-broken`: HTTP/3 works, then one connection goes silent mid-request,
  then the server closes another and answers no QUIC connection started
  after that.
- `h3-vn`: quic-go speaking only version 2, so a version 1 Initial gets
  Version Negotiation.
- `h3-v2`: aioquic behind the server's UDP socket, which relays and records
  both directions; aioquic upgrades a client that lists version 2
  compatibly (RFC 9368). Needs `python3` with aioquic (`pip install
  aioquic`; the script is embedded, `internal/server/aioquic_h3.py`; its
  connections have no `h3` record); without it the tool skips the scenario
  and exits non-zero.
- `h3-download`: a 50 MiB incompressible response over HTTP/3, then small
  requests: credit, ACKs, key updates.
- `h3-early`: fetch POST, PUT and DELETE, an XHR POST, a GET and a POST
  together, a GET, HEAD and OPTIONS, a form POST navigation, each the first
  request on a connection resumed after the last idled out: the server
  announces a 3 s idle timeout, since after a server's close Firefox sends
  the next request over TCP.
- `h3-too-early`: the same page; the server answers 425 Too Early to every
  request whose stream started in a 0-RTT packet, read from quic-go's
  per-packet trace (`earlyStreams`), as its HTTP/3 layer only exposes a racy
  `HandshakeComplete`.
- `h3-resume-table`: the `h3-early` schedule's resumed connections, every
  request a GET (fetches, a pair made together, an image, an XHR, a
  navigation), against a server allowing `h3-page`'s 64 KiB QPACK table:
  what the browser's encoder does in 0-RTT with the SETTINGS it remembers,
  and once the server's arrive (issue #90). Two connections carry a late
  fetch 500 ms in with a header no request had (`x-late`), so every
  encoder, Firefox's too, writes after the server's SETTINGS.
- `h3-streams-blocked`: quic-go allowing 2 concurrent bidirectional
  streams (`initial_max_streams_bidi` 2, raised by MAX_STREAMS as each
  stream ends; unidirectional left at 100), and two bursts of 6 fetches
  answered 300 ms late each, so the page waits on the limit: the
  browser's STREAMS_BLOCKED and where waiting requests go.

Their steps start on a fixed schedule whatever the browser takes
(`H3Step.NoWait`; each URL carries `?t=`, the script's
`performance.now()`), the pages' HTTP/2 traces carry `ms` on the datagrams'
clock, and every QUIC connection also records the server's datagrams
(`server`: Initial and Handshake packets opened, Version Negotiation
versions, Retry integrity checked, 1-RTT headers unmasked), its transport
parameters, and datagrams from another client port (`path`, a migration).
Chrome and Firefox need `-h3-local-roots` for all but `h3-blackhole` (no
certificate ever arrives). hellogen and h3gen read all but `h3-v2`
(`server.H3EdgeCaptures`);
`TestHTTP3EdgeMatchesBrowser` and `TestHTTP3DownloadMatchesBrowser`
replay them. Results:
[http3-edge](../../internal/harness/reports/http3-edge-2026-10-25.md),
[h3-early](../../internal/harness/reports/h3-early-2026-10-26.md),
[h3-streams-blocked](../../internal/harness/reports/h3-streams-blocked-2026-10-31.md).

`bodiless` makes fetch() POST, PUT and PATCH and a form POST navigation
with no body, first on `www.h3-c.test`, which never advertises HTTP/3
(so over HTTP/2), then on `www.h3-a.test` after a warm-up fetch and a
navigation 2 s later (over HTTP/3 by then): whether each sends
`content-length: 0`, where, and how the stream ends. Chrome and Firefox
need `-h3-local-roots` for its HTTP/3 half; kindsgen derives `emptyBody`
from it and `TestBodilessMatchesBrowser` replays it.

`client-hints` starts on `www.ch-a.test` with no opt-in (the low-entropy
baseline, and a fetch whose response's `Accept-CH` a subresource cannot
set), then a navigation answering `Accept-CH` for every hint Chrome knows:
its document's fetch, image, script and iframes, to its own origin,
`sub.ch-a.test` and the third party `www.ch-b.test`, the next navigation,
navigations to the other two, delegation to the third party through
`Permissions-Policy` and through a `Delegate-CH` meta tag, a narrower
`Accept-CH`, and `Clear-Site-Data: "clientHints"`. On a fresh site,
`www.ch-k.test`, navigations answer `Critical-CH` (a hint not yet sent,
one already sent, one `Permissions-Policy` disables, `Width`, `Save-Data`,
an unknown one), the same answer every time, so a restart that loops
shows; then `Clear-Site-Data: "*"`. Back on `www.ch-a.test`, the opt-in
again, then fetches with a header of the page's own and a JSON body, a
navigation redirected within the origin, a form POST answered with
`Critical-CH`, a navigation redirected from `sub.ch-a.test` into the
opted-in origin, a redirect answering `Accept-CH` and `Critical-CH`, and
`Clear-Site-Data` on a fetch, followed by a fetch and a navigation
(redirects land on steps no page makes, `H3Step.Redirect`). The opt-in
with `Critical-CH` follows over HTTP/1.1 (`www.ch-h1.test`) and HTTP/3
(`www.ch-h3.test`, as `bodiless`). Chrome and Firefox need
`-h3-local-roots`. Capture Chrome with its window on one display: beside a
display of another DPR, its navigations and subresources report different
DPRs, which kindsgen refuses. kindsgen derives `clientHints` from it
([decision 0055](../decisions/0055-client-hints-opt-in.md)), and
`TestClientHintsMatchesBrowser` replays it; a quick refresh recaptures it,
since the full version moves with every release.
