# 0050: QUIC resumption separate from TLS's, dial delays and close on GOAWAY

- Status: Accepted
- Date: 2026-10-31
- Commits: the one that closes [#10](https://github.com/roachadam/mimicry/issues/10)

**Summary.** Whether a profile resumes QUIC connections is `quic.zeroRtt`
alone, derived by h3gen from every resumed QUIC connection of the h3 and
edge captures; `tls.resumption` is TCP's. Safari resumes over QUIC (with
0-RTT, GET/HEAD/OPTIONS early, the rest after HANDSHAKE_DONE, a 425 sent
again) though it never resumes over TCP, and closes a QUIC connection
itself on the server's GOAWAY (`http3.closeOnGoaway`); both on by
default. How long after the request that waits for it a QUIC dial starts
is derived per kind (`http3.discovery.dialDelayMs` fresh,
`resumedDialDelayMs` resumed) as the measured delays themselves, a dial
drawing one: Safari's both, Firefox's resumed one, Chrome none. The dial
delay is off by default and `Config.BrowserDialDelay` restores it.

## Problem

`tls.resumption` described resumption for TCP and QUIC together, and
`quic.zeroRtt` required it. Safari never resumes a TCP connection (36 of
36 in `resume`, none in `resume-tls12`) but resumed every QUIC connection
it opened to a host it had used (`h3-broken`, `h3-early`,
`h3-too-early`, both versions), so its profile could not say so and
mimicry's Safari reconnections were full handshakes (issue #10). Safari
also closed the connection the server sent GOAWAY on before the server
did, which no field described, and started every QUIC dial a request waits
for some milliseconds after the request.

## Options

1. **A QUIC-only flag inside `tls.resumption`** (`quicOnly`). Keeps one
   resumption section, but `tls.resumption` is hand-written and its TCP
   policy fields (`tls12`) would be meaningless for Safari; and whether a
   browser resumes QUIC is a fact the h3 captures show, which h3gen
   already reads.
2. **`quic.zeroRtt` alone decides QUIC resumption** (chosen). h3gen
   derives it from every resumed QUIC connection (every one sent 0-RTT,
   or none: resuming without 0-RTT was never captured); a resumed QUIC
   hello applies `tls.resumption.drop` where TLS resumes, and its
   `early_data` position (`tls.quic.earlyDataBefore`, Safari's fixed
   order) comes from hellogen.
3. **One dial delay for all of a browser's dials.** Rejected: Firefox's
   resumed dials wait (4–9 ms, most of them) and its fresh ones are
   background validations no request waits for; Safari's fresh and
   resumed ranges differ (27: 3–10 against 5–22 ms). So the delay is per
   kind, and applies only to dials a request waits for.
4. **Pinning the dial delays' outliers** (Safari 26's 17–28 ms, Safari
   27's and Firefox's 22 and 34 ms on `h3-too-early` sample 1's first
   resumed connection). Rejected: nothing shows they are the slower Mac
   or a capture artefact, so they stay, as rare as in the samples.
5. **How a dial draws its delay.** A range drawn uniformly (the first
   version of this record) overstates it: Firefox's 0–34 ms averages
   17.5 ms against the samples' 5.1 (mode 5), Safari 26's resumed 6–23
   ms 15 against 8.5. Percentiles or a fitted distribution would need a
   choice of points or shape the samples do not make. Chosen: the
   samples themselves (whole ms, sorted, 27–51 per kind), a dial drawing
   one uniformly plus up to 1 ms of jitter for the millisecond the
   capture clock rounds away. Its mean is the samples' plus 0.5 ms
   (Safari 27: fresh 7.6 against 7.1, resumed 9.3 against 8.8; Safari
   26: 9.4 against 8.9, 9.0 against 8.5; Firefox resumed 5.6 against
   5.1), and an outlier comes once in 27–51 dials, as in the captures.
   h3gen writes them; nothing is hand-edited.
6. **The delay by default, or not.** The delay precedes the connection's
   first packet, so nothing a server sees differs but when the dial
   starts, within the jitter of the client's own scheduling and network;
   it costs 3–34 ms on every dial a request waits for (Safari: every new
   QUIC connection). By decision 0037's rule (a browser behaviour that
   costs real performance at a low detection risk defaults to the faster
   setting, with a `Config` option restoring the browser's) it is off by
   default. `BrowserQUICFallback` was not extended: it is the timing of
   QUIC failure paths (decision 0036), and this delay is on the working
   path, so `Config.BrowserDialDelay` is a field of its own. Everything
   else here (0-RTT resumption, early data, close on GOAWAY, the resumed
   hello, the streams with the 0-RTT request) is on the wire and stays
   the browser's by default.

## Decision

- `quic.zeroRtt` (h3gen): resume QUIC with the last ticket a QUIC
  connection to the origin got, 0-RTT every time. Independent of
  `tls.resumption`.
- `tls.quic.earlyDataBefore` (hellogen): in a fixed order, the extension
  `early_data` precedes. hellogen now checks every resumed QUIC hello of
  the edge captures as well as the h3 captures'.
- h3gen holds the edge captures' resumed first flights (padding, packet
  numbers, CRYPTO cut, transport parameters) to the full handshakes'
  rules where the h3 captures have no resumed connection (Safari).
- `http3.earlyData` with `http3.requestsAfterConfirmed`: a held request
  leaves once the handshake is confirmed; h3gen refuses a held request
  that left before.
- `http3.closeOnGoaway` (h3gen, from every QUIC connection the server sent
  GOAWAY on): close with an application CONNECTION_CLOSE, H3_NO_ERROR,
  once no request is open on it.
- `http3.earlyStreamsWithRequest` (h3gen, from the `h3-early` pages'
  connections dialled for a GET, HEAD or OPTIONS with no held request
  beside it): Safari's control stream and the request leave in one
  0-RTT packet (9 of 9 per page and version), Chrome's control stream
  first in its own. Safari's pair (a GET and a POST made together) sent
  the control stream alone, then the GET (3 of 3 per page and version);
  mimicry sends the GET with it (Not covered).
- `http3.discovery.dialDelayMs` and `resumedDialDelayMs` (h3gen): every
  measured delay, sorted, from the request that waits for it (it went on
  the attempt, or on TCP after the race's wait) to a fresh or resumed
  attempt's first datagram; nil where every delay is within 3 ms, the
  `?t=` clock's rounding. With `Config.BrowserDialDelay` a dial a request
  waits for draws one (option 5), and the race's TCP delay counts from
  the dial; not a reconnection after a dead connection (`reconnectMs`
  measured the whole wait) while it lasts. By default dials start at once.
- A race-mode 0-RTT attempt without `zeroRttHolds` (Safari) is raced like
  a fresh one: its requests go on TCP after `tcpDelayMs` without an
  answer, and with `abandonOnTcp` the attempt sends its 0-RTT close.

## Evidence

- Safari 27 and 26, 3 samples per page: 27 resumed connections per
  version on `h3-early`/`h3-too-early`, 3 in `h3-broken` after its own
  12 s close, 3 to the closed host after the server's GOAWAY (unanswered:
  Initial, Initial, 0-RTT, then a 0-RTT close at +107 ms when TCP won);
  every one sent 0-RTT. Resumed hellos: the full one plus `early_data`
  before `supported_versions` and `pre_shared_key` last, all of them.
- GOAWAY: 6 of 6 Safari connections closed by the client 2.2–2.8 ms after
  the server's response and GOAWAY, application close H3_NO_ERROR; Chrome
  and Firefox never closed one (h3-discovery and h3-broken).
- Dial delays (edge pages; a navigation's left out):
  Safari 27 fresh 3–10 ms (30), resumed 5–22 ms (51, all but one 5–11);
  Safari 26 fresh 3–28 ms (27, four 17–28), resumed 6–23 ms (51, all but
  one 6–11); Firefox resumed 0–34 ms (48, all but one 0–9, most 4–6);
  Chrome 155/156 resumed 0–3 ms (50, 51), Chrome 156 Windows 0–2 (34).
  Requests on open connections arrive 0–2 ms after they are made, so the
  delay is the dial's.
- [h3-early report](../../internal/harness/reports/h3-early-2026-10-26.md),
  [http3-edge report](../../internal/harness/reports/http3-edge-2026-10-25.md),
  [resume report](../../internal/harness/reports/resume-2026-10-24.md).

## Consequences

Safari profiles reconnect over HTTP/3 as Safari does. With
`BrowserDialDelay`, Safari's dials and Firefox's resumed ones start a few
milliseconds after the request, as often as in the captures; by default
at once. A profile resuming over QUIC but not TCP needs no hand-written
section. Closing on GOAWAY with requests still open is not captured:
mimicry waits for them (an assumption, in EVIDENCE.md's Not covered).

## Revisit when

A capture shows a browser resuming QUIC without 0-RTT, closing on GOAWAY
with a request open, a fresh dial a Firefox request waits for, or more
samples that explain the dial delays' outliers.
