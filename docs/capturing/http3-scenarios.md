# HTTP/3 scenarios

## Discovery and the page capture

`h3-discovery` and `h3-page` also serve QUIC on the TLS port's UDP twin.

- They advertise it with `Alt-Svc: h3=":<port>"; ma=86400` on every response.
- The browser discovers HTTP/3 itself (decision 0026).

The page's capture adds `quic`. For each QUIC connection it holds:

- its datagrams, with every coalesced packet's header and frames
- its ClientHello and transport parameters
- its HTTP/3 streams, frames and QPACK instructions
- how it closed

The recorder, `internal/quictrace`, decrypts the packets:

- **Initial keys:** from the client's first Destination Connection ID.
- **Handshake and 1-RTT keys:** from the server's TLS key log.
- **0-RTT keys:** from the resumed ticket's PSK.

TCP connections carry `ms` (when their ClientHello arrived) on the same clock, so a race shows.

## Local roots

Chrome and Firefox refuse QUIC with a certificate that does not chain to a built-in root.
By default the capture records the attempt.

`-h3-local-roots` lifts the refusal:

- **Chrome:** `--origin-to-force-quic-on` with the page's hosts on unused port 1, which forces nothing the page visits.
- **Firefox:** `network.http.http3.disable_when_third_party_roots_found=false`.

The flag also records itself in the page's `conditions`.
It names the capture `<name>.<scenario>.local-roots.pagetrace.json.gz`.

Chrome also ignores Alt-Svc unless the certificate verified.
So it needs the capture CA trusted by the system, as Safari does (the tool warns).

## Failure, version, transfer, early-data and stream-limit scenarios

- `h3-blackhole`: Alt-Svc names a UDP port that records every datagram and answers none.
- `h3-broken`:
  - HTTP/3 works at first.
  - Then one connection goes silent mid-request.
  - Then the server closes another, and answers no QUIC connection started after that.
- `h3-vn`: quic-go speaking only version 2, so a version 1 Initial gets Version Negotiation.
- `h3-v2`: aioquic behind the server's UDP socket, which relays and records both directions.
  - aioquic upgrades a client that lists version 2 compatibly (RFC 9368).
  - It needs `python3` with aioquic (`pip install aioquic`).
  - The script is embedded (`internal/server/aioquic_h3.py`).
  - Its connections have no `h3` record.
  - Without aioquic, the tool skips the scenario and exits non-zero.
- `h3-download`: a 50 MiB incompressible response over HTTP/3, then small requests: credit, ACKs, key updates.
- `h3-early`: each of these is the first request on a connection resumed after the last idled out:
  - fetch POST, PUT and DELETE
  - an XHR POST
  - a GET and a POST together
  - a GET, HEAD and OPTIONS
  - a form POST navigation
  - The server announces a 3 s idle timeout, since after a server's close Firefox sends the next request over TCP.
- `h3-too-early`: the same page.
  - The server answers 425 Too Early to every request whose stream started in a 0-RTT packet.
  - It reads that from quic-go's per-packet trace (`earlyStreams`), as its HTTP/3 layer only exposes a racy `HandshakeComplete`.
- `h3-resume-table`: the `h3-early` schedule's resumed connections, every request a GET (fetches, a pair made together, an image, an XHR, a navigation).
  - The server allows `h3-page`'s 64 KiB QPACK table.
  - It shows what the browser's encoder does in 0-RTT with the SETTINGS it remembers, and once the server's arrive (issue #90).
  - Two connections carry a late fetch 500 ms in, with a header no request had (`x-late`). So every encoder, Firefox's too, writes after the server's SETTINGS.
- `h3-streams-blocked`: quic-go allowing 2 concurrent bidirectional streams.
  - `initial_max_streams_bidi` is 2, raised by MAX_STREAMS as each stream ends. Unidirectional is left at 100.
  - Two bursts of 6 fetches are answered 300 ms late each, so the page waits on the limit.
  - It shows the browser's STREAMS_BLOCKED and where waiting requests go.

### What these scenarios share

- Their steps start on a fixed schedule whatever the browser takes (`H3Step.NoWait`).
- Each URL carries `?t=`, the script's `performance.now()`.
- The pages' HTTP/2 traces carry `ms` on the datagrams' clock.
- Every QUIC connection also records:
  - the server's datagrams (`server`: Initial and Handshake packets opened, Version Negotiation versions, Retry integrity checked, 1-RTT headers unmasked), and its transport parameters
  - datagrams from another client port (`path`, a migration)
- Chrome and Firefox need `-h3-local-roots` for all but `h3-blackhole` (no certificate ever arrives).
- hellogen and h3gen read all but `h3-v2` (`server.H3EdgeCaptures`).
- `TestHTTP3EdgeMatchesBrowser` and `TestHTTP3DownloadMatchesBrowser` replay them.

Results:

- [http3-edge](../../internal/harness/reports/http3-edge-2026-10-25.md)
- [h3-early](../../internal/harness/reports/h3-early-2026-10-26.md)
- [h3-streams-blocked](../../internal/harness/reports/h3-streams-blocked-2026-10-31.md)

## Bodiless requests

`bodiless` makes fetch() POST, PUT and PATCH and a form POST navigation with no body:

1. First on `www.h3-c.test`, which never advertises HTTP/3 (so over HTTP/2).
2. Then on `www.h3-a.test`, after a warm-up fetch and a navigation 2 s later (over HTTP/3 by then).

It shows whether each sends `content-length: 0`, where, and how the stream ends.

- Chrome and Firefox need `-h3-local-roots` for its HTTP/3 half.
- kindsgen derives `emptyBody` from it.
- `TestBodilessMatchesBrowser` replays it.

## Client hints

`client-hints` runs in these phases:

1. **Baseline:** it starts on `www.ch-a.test` with no opt-in: the low-entropy baseline, and a fetch whose response's `Accept-CH` a subresource cannot set.
2. **Opt-in:** a navigation answers `Accept-CH` for every hint Chrome knows. Then come:
   - its document's fetch, image, script and iframes, to its own origin, `sub.ch-a.test` and the third party `www.ch-b.test`
   - the next navigation
   - navigations to the other two
   - delegation to the third party through `Permissions-Policy` and through a `Delegate-CH` meta tag
   - a narrower `Accept-CH`
   - `Clear-Site-Data: "clientHints"`
3. **`Critical-CH`:** on a fresh site, `www.ch-k.test`, navigations answer `Critical-CH`. They name:
   - a hint not yet sent
   - one already sent
   - one `Permissions-Policy` disables
   - `Width` and `Save-Data`
   - an unknown one
   - The answer is the same every time, so a restart that loops shows. Then comes `Clear-Site-Data: "*"`.
4. **Back on `www.ch-a.test`:** the opt-in again, then:
   - fetches with a header of the page's own and a JSON body
   - a navigation redirected within the origin
   - a form POST answered with `Critical-CH`
   - a navigation redirected from `sub.ch-a.test` into the opted-in origin
   - a redirect answering `Accept-CH` and `Critical-CH`
   - `Clear-Site-Data` on a fetch, followed by a fetch and a navigation
   - Redirects land on steps no page makes (`H3Step.Redirect`).
5. **Other protocols:** the opt-in with `Critical-CH` follows over HTTP/1.1 (`www.ch-h1.test`) and HTTP/3 (`www.ch-h3.test`, as `bodiless`).

Capturing and using it:

- Chrome and Firefox need `-h3-local-roots`.
- Capture Chrome with its window on one display. Beside a display of another DPR, its navigations and subresources report different DPRs, which kindsgen refuses.
- kindsgen derives `clientHints` from it ([decision 0055](../decisions/0055-client-hints-opt-in.md)).
- `TestClientHintsMatchesBrowser` replays it.
- A quick refresh recaptures it, since the full version moves with every release.
