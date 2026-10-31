# STREAMS_BLOCKED at a server's stream limit: Chrome 156, Firefox 158, Safari 27, 2026-10-31

One scenario, `h3-streams-blocked` (`internal/server/h3edge.go`), three
samples per browser, captured on macOS 27 with visible windows (Chrome
156.0.8078.79 and Firefox 158.0.1 with `-h3-local-roots`, Safari 27.0.1
unforced, as the other HTTP/3 captures). The captures are in
`internal/harness/testdata/captures/`:
`chrome-156-macos.h3-streams-blocked.local-roots.pagetrace.json.gz`,
`firefox-158-macos.h3-streams-blocked.local-roots.pagetrace.json.gz`,
`safari-27-macos.h3-streams-blocked.pagetrace.json.gz`. Chrome 156 on
Windows and Safari 26 were not captured (`missingCaptures`).

| Server | Page |
|---|---|
| quic-go v0.64.1 with `MaxIncomingStreams` 2: `initial_max_streams_bidi` 2, `initial_max_streams_uni` left at 100 (RFC 9114 needs at least 3). quic-go raises the limit by one MAX_STREAMS per stream that ends (its request read, its response's FIN acknowledged) | `/` learns `h3-a`'s Alt-Svc; `warm.so`; `goaway.so` ends its HTTP/2 connection (GOAWAY, Safari's way to HTTP/3); `fetch1.so`. Then burst a: six fetches `a1.so`–`a6.so` made together, each answered 300 ms late; 2 s later burst b (`b1.so`–`b6.so`) the same; then `last.so` and `/done` |

The steps start on a fixed schedule (`NoWait`, `?t=` on each URL). Times
below are from when the page made the request (`?t=` on the server's
clock, in whole milliseconds, so "0 ms" means within one).

## 1. Measurements

The limit a burst met is 2 plus the streams the connection had already
ended: the browsers sent different numbers of the earlier requests over
QUIC.

| | Chrome 156 | Firefox 158 | Safari 27 |
|---|---|---|---|
| **Limit when burst a / b started** | 4 / 10 (`goaway.so`, `fetch1.so` over QUIC) | 5 / 11 (`warm.so` too) | 3 / 9 (`fetch1.so` only) |
| **STREAMS_BLOCKED per sample** | 15 (7 or 8 per burst) | 8 (4 per burst) | 2 (1 per burst) |
| **Limits sent, in order** | sample 1: 4 4 4 4 5 6 8, 10 10 10 10 11 12 13 14; sample 2: 4 4 4 4 6 7 8, 10 10 10 10 11 12 13 14; sample 3: 4 4 4 4 5 6 7 8, 10 10 10 10 12 13 14 | 5 6 7 8, 11 12 13 14 (3 of 3) | 3, 9 (3 of 3) |
| **When a request is made at the limit** | one frame per such request, each in a packet of its own (PADDING, STREAMS_BLOCKED; a 29-byte datagram), with the current limit, 0–2.3 ms after the request was made: 4 per burst, 24 of 24 | one frame, for the first such request only (a 29-byte packet, 0–1.2 ms after it); none for the three made after it at the same limit | as Firefox, 0–2.1 ms after the first |
| **When MAX_STREAMS raises the limit** | Chrome opens the waiting requests the new limit allows, and in the same packet sends STREAMS_BLOCKED with the new limit: after each stream it opened (`… STREAM, STREAMS_BLOCKED n-1, STREAM, STREAMS_BLOCKED n`) in 9 of 12 raises, once after both (`n`) in 3 of 12 (one raise per sample, a different one each time). Also after the last raise of each burst, when no request was left waiting (limits 8 and 14) | when a request is still waiting after the one it opens: STREAMS_BLOCKED with the new limit, first in the packet that opens that stream (`STREAMS_BLOCKED, STREAM`). One stream per raise, each after a server packet of its own. Nothing once no request waits | nothing, though two requests of each burst still waited at the new limit (5, 11) for another 300 ms |
| **Type** | bidirectional (0x16) only | as Chrome | as Chrome |
| **Resent** | no frame resent: no packet was lost on loopback (no STREAM frame was sent twice in any connection), so resending is not measured | as Chrome | as Chrome |
| **Where the waiting requests went** | queued on the QUIC connection: two at once, two after 292–330 ms, two after 584–624 ms, in the order made; no other connection, TCP or QUIC | as Chrome: 290–294 and 580–602 ms | as Chrome: 284–305 and 572–631 ms |

Times between a waiting request's stream and the server: every one of
the 72 opened 0.06–2.15 ms after a small server datagram (21–29 bytes,
one of 63), which quic-go sent right after the client acknowledged the
burst's finished responses. The recorder opens only the headers of the
server's 1-RTT packets, so that this datagram carried the MAX_STREAMS is
[INFERENCE] from quic-go's rule (`streams_map_incoming.go`,
`deleteStream`); the client cannot open a stream before it. Chrome
acknowledged a lone packet with both responses 26–30 ms after it
arrived, two packets at once, so some of its waits run that much past
300 ms.

Chrome's sample 1 also has a QUIC connection opened as the page started
that carried no request; its page went on a second, resumed connection
([INFERENCE]: Chrome retiring connections after its certificate
verifiers rebuilt, `docs/capturing.md`). Safari's sample 3 also fetched
its icons over HTTP/2. Neither touches the bursts.

## 2. The browsers' rules, as the captures show them

- **Chrome** sends STREAMS_BLOCKED for every attempt to open a stream at
  the limit: one per request made while blocked, the same limit again
  and again, and one each time it opens streams after a MAX_STREAMS and
  then finds the limit reached, whether or not a request is waiting.
  How many it sends after a raise that allows two streams (one or two)
  varies between samples; a model has to allow both or pin it, not pick
  one sample's count. This matches the QUICHE code read for issue #4
  (`WriteOrBufferStreamsBlocked` on each failed `CanOpenNext…`, also in
  the `OnCanCreateNewOutgoingStream` loop), which was a lead only.
- **Firefox** sends it once per limit at which a request waited, with
  the first request that waits and then with each stream it opens while
  another request still waits. This matches neqo's `blocked()` (once per
  limit value); its resend on loss is not measured.
- **Safari** sends it once per limit, only when a request is made at the
  limit; requests still waiting after a raise send nothing.
- All three queue the waiting requests on the connection, in the order
  made, and open them as the limit allows; none opened TCP or another
  QUIC connection for them.

## 3. In mimicry

No modelling in this change. mimicry's `internal/quic` sends no
STREAMS_BLOCKED (`localStreamLimits.open`, its TODO), and its HTTP/3
requests wait for the limit on the connection, as the browsers' do.
`TestHTTP3EdgeMatchesBrowser` now replays this page for every profile
with the capture and compares, per host, the STREAMS_BLOCKED frames sent
(count, and limits where the samples agree); mimicry's none is pinned in
`h3EdgeGaps` for Chrome (count), Firefox and Safari (count and limits).
The replay makes each burst's six requests together, so which of them
waits is the test process's scheduling: each request's own delay is in
`h3EdgeSchedulingGaps`. What is compared instead is each burst's delays
sorted (`compareBurstWaits`): the k-th shortest of mimicry's against the
range of the samples' k-th shortest, within the edge tests' slack (50 ms,
plus 5% of the longest; net of the test process's stalls), and up to 30
ms later per response the request waited out. That allowance is the
ACK delay the server's MAX_STREAMS waits on: quic-go raises the limit
once the client acknowledges the responses, and a client holds that ACK
back when both responses come in one packet (Chrome 26–30 ms; mimicry's
waits ran 302–333 and 605–661 ms). mimicry's waits come in the browsers'
three waves for every profile (30 of 30 subtests over five runs), and
every request stays on the QUIC connection (compared).

Mutation: with mimicry using one stream fewer than the server allows
(`localStreamLimits.setMax`, `maxStreams-1`), its waits came one at a
time (0, ~325, ~650, … ~1640 ms) and every profile failed
`compareBurstWaits`. A client opening all six at once would put its
third to sixth waits near 0 ms, below the lower bounds of 221–237 ms and
493–523 ms.

The gap is a line in
[EVIDENCE.md, Not covered](../../../profiles/EVIDENCE.md#not-covered-by-any-claim);
the work is [issue #4](https://github.com/roachadam/mimicry/issues/4).
