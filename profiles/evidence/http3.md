# QUIC and HTTP/3

Evidence for the `quic` and `http3` profile claims. Each claim lists every browser's value with its class (W, K) and its samples.

### Sources and checks

- **Captures:** `h3-discovery` and `h3-page` ([report](../../internal/harness/reports/http3-2026-10-25.md)).
  - Three samples per browser and scenario.
  - Unforced, and (Chrome, Firefox) with `-h3-local-roots`.
- **Connections:**
  - Chrome and Firefox: 33 QUIC connections each.
    - 21 completed handshakes: 12 full and 9 resumed.
    - The 12 unforced ones refused the capture server's CA and show the first flight only.
  - Safari 27 and 26: 3 each, `h3-discovery` only. Safari opened no QUIC connection in `h3-page`.
- **Generators:** generated, never hand-edited.
  - `h3gen` derives `quic` and `http3` (but `headers`, `high`, `low`).
  - `kindsgen` derives those three.
  - `hellogen` derives `tls.quic`.
  - Each checks its values against every connection of every sample. It refuses a difference not pinned with a reason.
- **Pinned differences:**
  - `h3gen`'s `sampleGaps`: Chrome's one Initial ACK and two tokenless reconnections.
  - `kindsgen`'s `h3Unmatched`: the XHR POST, which no HTTP/2 list covers.
  - `h3PriorityGaps`: Firefox's second-page image.
- **Tests:**
  - Their `TestRegenerateReproducesProfiles` keep the files equal to what the captures give.
  - `TestHTTP3MatchesBrowser` replays `H3DiscoverySteps` and `H3PageSteps` through mimicry against every sample.
    - It holds what each browser draws per connection to the rule below, for mimicry's connections and every captured one.
    - Draws checked: packet numbers, DCID length, CRYPTO cuts, PINGs, parameter order, GREASE and ECH lengths, ACK delays, spin bit.
  - `TestQPACKReproducesBrowserSections` re-encodes every captured section.
- **Mutations, 2026-10-25** (`-overlay`; each fails `TestHTTP3MatchesBrowser`):
  - No PRIORITY_UPDATE (Chrome).
  - No QUIC dial on Alt-Svc (Firefox).
  - HTTP/3 on Safari's next request.
  - Firefox's HTTP/2 pseudo-header order over HTTP/3.
  - The SNI cut keeping the head piece whole on a resumed hello.
  - Safari's request before HANDSHAKE_DONE.
  - A dial on the Alt-Svc that came with a GOAWAY (Firefox).
  - Chrome's transport parameters unshuffled.

### Handshake and connection claims

#### `quic.version`, `datagramSize`, `padding`

| Browser | Value |
|---|---|
| Chrome 155 | W: 1; 1250; `scattered` |
| Firefox 158 | W: 1; 1252; `trailing` [1] |
| Safari 27 | W: 1; 1200; `end` |
| Safari 26 | W: as 27 |

Notes:
1. Zero bytes after the packets. PADDING only in a coalesced 1-RTT packet.

#### `quic.crypto`

- **Chrome 155:** W, `shuffle` (quiche's chaos protection).
  - Head 55–86.
  - 2–10 added CRYPTO frames and 2–10 PINGs per padded packet.
  - The first packet holds floor((len-4)/2) bytes.
  - In all 66 hello packets.
- **Firefox 158:** W, `sni_cut` at the host's middle (6 of 13 bytes), in all 33.
  - Tail then head. The longer piece is cut to len/n.
  - Fresh: 2 packets, middle 948 bytes.
  - Resumed: 3 packets. The tail piece is as planned. The head piece is what fits after it beside the 90-byte token.
  - So a host early in the hello stays whole: cut inside it in 4 of 9.
- **Safari 27:** W, `ordered`, `firstPacket` 999.
- **Safari 26:** W, as 27.

#### `quic.packetNumbers`

- **Chrome 155:** W, `shared` from 1.
  - 2-byte second Initial PN iff no token (33 of 33).
- **Firefox 158:** W, `random_initial`.
  - First Initial PN 2–32; four of 33 at 497–813: neqo's formula, 1–1024.
  - 1-RTT after 0-RTT.
  - Before an ACK, a PN is written in the fewest bytes holding PN + 1, not RFC 9000 A.2's twice that.
  - So 127–254 go in 1 byte: 28 of 28 Initials where the rules differ, Firefox 158 and 159 on every platform ([#91](https://github.com/roachadam/mimicry/issues/91)).
- **Safari 27:** W, `per_space`.
- **Safari 26:** W, as 27.

#### `quic.dcidLength`, `dcidLengthRule`, `scidLength`, `newConnectionIds`

| Browser | Value |
|---|---|
| Chrome 155 | W: 8; fixed; 0; 0 |
| Firefox 158 | W: 8 with `biased` [1]; 3; 4 [2] |
| Safari 27 | W: 8; 0; 0 |
| Safari 26 | W: as 27 |

Notes:
1. 23 of 33 at 8, others 10–17. neqo's max(8, 5 + (v & v>>4)).
2. In all 21 completed, retire_prior_to 0, against a server limit of 4.

#### `quic.transportParameters`, `transportParameterOrder`

- **Chrome 155:** W, `shuffled`.
  - 12 parameters + GREASE (0–15 bytes) + `initial_rtt` when resumed.
  - `version_information` 1, then {1, GREASE} shuffled.
- **Firefox 158:** W, `fixed`.
  - 14, incl. 0x1d empty and `min_ack_delay` 0xff02de1a = 1000.
  - `version_information` 1, GREASE, v2, 1.
- **Safari 27:** W, `rotated`.
  - 8, `max_idle_timeout` 300000.
  - Rotations of one cycle in 3 samples. A rotation cannot be told from another permutation.
  - Until midday 2026-10-25 Safari 27 also sent 0xff080808 = 09, last (the first h3-discovery capture, 3 of 3).
  - Every connection since leaves it out:
    - the edge captures (33);
    - the h3-discovery recapture (3);
    - fp.impersonate.pro over HTTP/3 on port 443 (`internal/harness/reports/realcheck-2026-10-25.md`).
  - Same Safari and macOS build throughout: an Apple-side switch, not modelled.
- **Safari 26:** W, `rotated`.
  - 8, no idle timeout.
  - 0xff080808 = 07, still sent, also on the internet (realcheck).

#### `quic.ackInitial`, `ackBeforeFinished`, `coalesce`

| Browser | Value |
|---|---|
| Chrome 155 | W: false [1]; false; true |
| Firefox 158 | W: true; true [2]; true |
| Safari 27 | W: true; true; false [3] |
| Safari 26 | W: as 27 |

Notes:
1. 1 of 12 full handshakes ACKed an Initial: pinned.
2. Full handshakes. Resumed ones ACK with the Finished, but for one on Linux: pinned.
3. No datagram holds two packets.

#### `quic.ackDelay`, `ackDelayConstant`

| Browser | Value |
|---|---|
| Chrome 155 | W: `actual` |
| Firefox 158 | W: `actual` |
| Safari 27 | W: `constant`, raw 27–29 [1] |
| Safari 26 | W: `constant`, 9–27 |

Notes:
1. In the h3-discovery recapture, one value per connection. The first capture's three were 19–24, so the range is narrower than the browser's.

#### `quic.ackOnlyLimit`, `ackOnlyFrame`

| Browser | Value |
|---|---|
| Chrome 155 | W: 19, `MAX_DATA` [1] |
| Firefox 158 | W: 0 [2] |
| Safari 27 | W: 0 [3] |
| Safari 26 | W: as 27 |

Notes:
1. Every ACK + MAX_DATA packet follows exactly 19 ACK-only packets, no run longer.
2. Runs up to 24, never made ack-eliciting.
3. No run that long: not measured.

#### `quic.spinBit`

| Browser | Value |
|---|---|
| Chrome 155 | W: `zero` |
| Firefox 158 | W: `random` (varies within connections) |
| Safari 27 | W: `spin` (0, then 1 against quic-go) |
| Safari 26 | W: as 27 |

#### `quic.tokens`, `zeroRtt`, `closeReply`

- **Chrome 155:** W.
  - `tokens` true. Two reconnections after idle went without one: pinned.
  - `zeroRtt` true. 9 of 9 resumed sent 0-RTT, and every resumed one of the edge captures.
  - `closeReply` false. No reply to the server's close, 3 of 3.
- **Firefox 158:** W.
  - `tokens` true.
  - `zeroRtt` true: 9 of 9, and the edge captures'.
  - `closeReply` true: CONNECTION_CLOSE NO_ERROR, frame type 0x1d, 3 of 3.
- **Safari 27:** W.
  - `tokens` false.
  - `zeroRtt` true since decision 0050, from the edge captures. 60 of 60 resumed connections sent 0-RTT:
    - 54 on `h3-early`/`h3-too-early` after an idle timeout;
    - 3 after its own 12 s close;
    - 3 after its own close on the server's GOAWAY.
  - Their first flights hold to the full handshakes' padding, packet numbers, CRYPTO cut and transport parameters.
  - `closeReply` not measured: the server never closed.
- **Safari 26:** W, as 27 (60 of 60).

#### `http3.settings`, `greaseSetting`, `greaseFrame`

| Browser | Value |
|---|---|
| Chrome 155 | W: 4 settings; GREASE setting last [1]; GREASE frame [2] |
| Firefox 158 | W: 6 settings; none; GREASE frame [3] |
| Safari 27 | W: 2 settings (table 16383); GREASE setting; none |
| Safari 26 | W: as 27 |

Notes:
1. N and value 32-bit.
2. N 32-bit, 0–3 bytes.
3. Any N, 0–7 bytes.

#### `http3.uniStreams`, `streamsWithFinished`, `earlyRequestFirst`

| Browser | Value |
|---|---|
| Chrome 155 | W: see [1]; true; false |
| Firefox 158 | W: control, encoder, decoder at the handshake; false; true |
| Safari 27 | W: control only; false; — |
| Safari 26 | W: as 27 |

Notes:
1. Control at the handshake. 6 never written (decoder, [INFERENCE]: the only type left). Encoder on the first insert.

#### `http3.requestsAfterConfirmed`

- **Chrome 155:** W, false.
  - Requests held out of 0-RTT left before the handshake was confirmed: 19 of 31, in 6 of 6 `h3-early` and `h3-too-early` samples.
  - No first request of a full handshake waited. The first came 300 ms or more after it.
- **Firefox 158:** W, false.
  - Held requests left before confirmation: 32 of 35, 6 of 6 samples.
  - The first request of a full handshake came 47–74 ms after the Finished, from the parser.
- **Safari 27:** W, true.
  - The request waiting for the connection leaves 0.7–2.4 ms after the Finished.
  - It goes with the ACK of the server's next 1-RTT packet (pn 3, its HANDSHAKE_DONE), after the control stream (3 of 3).
  - Its spin bit is 1.
- **Safari 26:** W, as 27.

#### `http3.closeOnGoaway`

Taken from every QUIC connection the server sent GOAWAY on, `h3-discovery` and `h3-broken`.

- **Chrome 155:** W, false. The server closed every one.
- **Firefox 158:** W, false. It replied to the server's close.
- **Safari 27:** W, true.
  - An application CONNECTION_CLOSE (0x1d) H3_NO_ERROR, 2.2–2.8 ms after the response and GOAWAY.
  - Sent before the server's own close a second later (3 of 3).
  - No request was open on any.
- **Safari 26:** W, as 27 (3 of 3).

#### `http3.earlyStreamsWithRequest`

A connection dialled for a 0-RTT request sends the control stream in its packet.

- **Chrome 155:** W, false. The control stream alone first, then the request (18 of 18 on `h3-early`/`h3-too-early`).
- **Firefox 158:** —. `earlyRequestFirst`: the request leads the streams in their packet.
- **Safari 27:** W, true.
  - Control stream then request in one 0-RTT packet (18 of 18 GET, HEAD, OPTIONS connections).
  - The pair's connection (a POST made with the GET) sent the control stream alone, then the GET (6 of 6: Not covered).
- **Safari 26:** W, as 27 (18 of 18; pair 6 of 6).

#### `http3.discovery.dialDelayMs`, `resumedDialDelayMs`

What is counted:

- Every delay, sorted, from the request that waits for it to a fresh or resumed attempt's first datagram.
- Edge pages only; a navigation's left out.
- Nil where every delay is within the `?t=` clock's 3 ms.
- A dial draws one plus up to 1 ms, only with `Config.BrowserDialDelay`. That is off by default (decision 0050).

Values:

- **Chrome 155:** W, none.
  - Resumed 0–3 ms (50 dials; Chrome 156 51, Windows 0–2 in 34).
  - Its fresh dials go beside a request on TCP; none waited for.
- **Firefox 158:** W.
  - Fresh none: its fresh dials are background validations no request waits for.
  - Resumed 48 delays, mean 5.1 ms: 11 of 0–3, 36 of 4–9, and one 34.
  - The 34 is on `h3-too-early` sample 1's first resumed connection.
  - The draw's mean is 5.6.
- **Safari 27:** W.
  - Fresh 30, 3–10 ms, mean 7.1 (draw 7.6).
  - Resumed 51, mean 8.8 (draw 9.3): all but one 5–11.
  - The other is 22, on `h3-too-early` sample 1's first resumed connection, as Firefox's 34.
- **Safari 26:** W.
  - Fresh 27, mean 8.9 (draw 9.4): 23 of 3–10, then 17, 18, 25, 28.
  - Resumed 51, mean 8.5 (draw 9.0): all but one 6–11, then 23.
  - Requests on open connections arrive 0–2 ms after they are made (Safari 26 up to 7), so the delay is the dial's.
  - Nothing ties the outliers to the slower Mac, so they stay, as rare as measured.
  - Checked by `TestHTTP3EdgeMatchesBrowser` (`checkDialDelays`) and `TestDialDelayAfterReconnect` (the default, the draw).

#### `http3.qpack`

| Browser | Value |
|---|---|
| Chrome 155 | W: `index_all`, inserts first, `shorter` |
| Firefox 158 | W: `new_names`, request first, `always` |
| Safari 27 | W: encoder not measured [1]; `always` |
| Safari 26 | W: as 27 |

Notes:
1. No table offered.

#### `http3.qpack.resumed`

From `h3-resume-table` (decision 0060).

- **Chrome 155:** W (Chrome for Testing 155, decision 0042), `remembered_repeat`.
  - Same as Chrome 156 (macOS, Windows), 157 (Linux) and Edge 157: of 18 resumed connections each (Linux 19), 15 set the remembered 65536 and inserted in 0-RTT.
  - The held navigation's inserted in 1-RTT.
  - All 11–12 that wrote again once the server's SETTINGS arrived set it again first.
- **Firefox 158:** W (158.0.1 from Mozilla's archive), `remembered`.
  - Same as Firefox 159 (macOS, Windows, Linux).
  - Every resumed connection opened the encoder with the remembered 65536 in 0-RTT.
  - None of the 9–12 per profile that wrote again in 1-RTT set it again.
- **Safari 27:** none. Safari never uses a table.
- **Safari 26:** as 27.

#### `http3.priorityUpdate`, `maxDataFrame`, `firstDataFrame`

| Browser | Value |
|---|---|
| Chrome 155 | W: true [1]; one DATA frame per body |
| Firefox 158 | W: false; 32768, first 32224 [2] |
| Safari 27 | W: false; not measured (no body) |
| Safari 26 | W: as 27 |

Notes:
1. 102 of 102 prioritised requests.
2. 64 KiB: 32224 + 32768 + 544, 3 of 3.

#### `http3.headers`

- **Chrome 155:** W, nil. 114 requests: HTTP/2 order and pseudo order.
- **Firefox 158:** W.
  - Pseudo `:method :scheme :authority :path`.
  - `te` dropped.
  - `alt-used` before `referer`. For fetch, before `cookie`: cookie never went over HTTP/3, so its side is assumed.
  - Seen in 129 requests of 11 kinds and the navigation. Kinds not captured over HTTP/3 have no entry (not measured).
  - `alt-used` value is `<host>:<port>` in every capture (unprivileged ports).
  - The port is left out when it is 443 (code, `altUsedValue`).
    - Seen once on the wire through a public echo: fp.impersonate.pro over HTTP/3 (`internal/harness/reports/realcheck-2026-10-25.md`).
    - No capture reaches port 443. Tested by `TestAltUsedPort`.
- **Safari 27:** W, nil (9 fetches).
- **Safari 26:** W, as 27.

#### `http3.high`, `http3.low`

- **Chrome 155:** W.
  - Low `injected_image` `i`. HTTP/2 has no image-low.
  - Every other priority header is the kind's HTTP/2 value.
- **Firefox 158:** W.
  - Low `injected_image` `u=6, i`.
  - `markup-image2.so` `u=4, i` pinned (layout-driven).
- **Safari 27:** W.
  - None.
  - `kinds.injected_image.low` `u=7, i` from its `h3-page` HTTP/2 requests.
- **Safari 26:** W, as 27.

#### `kinds.xhr` POST variant (`corsChecked`, no `http1`)

- **Chrome 155:** K.
  - The `h3-page` XHR POST over HTTP/3 (= its HTTP/2 list), 3 samples.
  - `cookie` slot placed after its predecessor (assumed).
- **Firefox 158:** K.
  - Over HTTP/3 in HTTP/2 form (`alt-used` out, `te` back last), 3 samples.
  - `*` and `cookie` slots assumed.
- **Safari 27:** W.
  - Its `h3-page` HTTP/2 requests, 3 samples.
  - `cookie` slot assumed.
- **Safari 26:** W, as 27.

#### `http3.discovery`

- **Chrome 155:** W (local roots).
  - `next_request`.
  - No TCP GOAWAY.
  - No race: 0-RTT QUIC alone after the server's GOAWAY.
- **Firefox 158:** W (local roots), `on_alt_svc`.
  - `goawayTcp` on every HTTP/2 connection QUIC replaced (3 of 3).
    - Also on one opened while the resumed attempt was pending (h3-broken sample 3).
    - mimicry sends it once the connection's request is answered, whenever the connection was established: `TestGoAwayReachesTCPConnectionEstablishedAfterQUIC`.
  - `raceAfterGoaway`, 6–9 ms.
    - QUIC only after the TCP connection is up, its request sent and its GOAWAY out.
    - The GOAWAY goes 1 ms before QUIC's first datagram (h3-broken 3 of 3). TCP first in h3-discovery 3 of 3.
    - mimicry holds that QUIC dial until the request's response on TCP (`TestRaceAfterGoAwayWaitsForTheTCPResponse`).
- **Safari 27:** W, `new_connection`.
  - QUIC only after the server's HTTP/2 GOAWAY.
  - `h3-b` stayed on HTTP/2.
  - Race not measured.
- **Safari 26:** W, as 27.

#### Not measured

Documented on `profile.QUIC` and `profile.HTTP3`:

- PMTU probing, ECN, DATAGRAM and Retry.
- Safari's QPACK with a table, request bodies and PRIORITY_UPDATE.
- What `-h3-local-roots` might change after the handshake.
- A v2 connection's NEW_TOKEN reused by a v1 one.
- Broken-alternative durations beyond a page.
- Credit growth beyond 50 MiB or at lower bandwidth.
- Key updates beyond 37,000 received packets.
- Chrome's blackhole close ("85:Network blackhole detected") after its migrated path also went quiet.
- Probes of resumed attempts: Chrome's first carries four Initials; Firefox's alternate layouts. The rules hold for fresh attempts.

### Failure, version negotiation, long transfers

- **Claims:** `quic.loss`, `keepAlive`, `ack`, `credit`, `keyUpdate`, `versionNegotiation` and `http3.discovery.fallback`.
- **Captures:** `h3-blackhole`, `h3-broken`, `h3-vn` and `h3-download`, three samples each.
  - [Report](../../internal/harness/reports/http3-edge-2026-10-25.md); `server.H3EdgeCaptures`.
- **Generator:** `h3gen` (`edge*.go`).
  - It checks every unanswered attempt, silenced connection, download and fallback request against the rule.
  - It refuses a difference not pinned in `sampleGaps`.
- **Pinned in `sampleGaps`:**
  - Firefox's sample-3 resumed attempt: no cached RTT, give-up 9.2 s.
  - Firefox's 254 ms late keep-alive.
  - Safari's irregular stream credit, six samples.
  - Safari 27's TCP 139/202 ms after a Version Negotiation.
- **Tests:** `TestRegenerateReproducesProfiles` (h3gen, hellogen). On mimicry's side, `TestHTTP3EdgeMatchesBrowser`, `TestHTTP3DownloadMatchesBrowser` and `TestQUICInterop`.
- **Not held:** how far a batching browser's ACKs advance on average.
  - It is how many packets arrive while it works through the last.
  - ACKs are 19–47 µs apart at the median, with no fixed count or interval.
  - This is machine timing. No field holds it; the download test logs it without checking.

#### `quic.loss` probes

- **Chrome 155:** W.
  - First PTO 300 ms, doubling: +300, +900, +2100 ms (6 fresh attempts).
  - Resumed: from the cached RTT, max_ack_delay counted (first probe 30–33 ms).
  - One 1250-B Initial per probe. The flight's packets' CRYPTO go in turn, re-cut.
  - One PN skipped before each probe, handshake and 1-RTT.
- **Firefox 158:** W.
  - 300 ms, doubling, up to 15% late (+19.0–19.8 s last).
  - Resumed 3 ms: cached RTT, no ack delay (1 of 3 pinned).
  - Both Initials again, CRYPTO as first sent, no skip.
- **Safari 27:** not measured. No probe before TCP won.
- **Safari 26:** as 27.

#### `quic.loss` give-up

- **Chrome 155:** W. Fresh and resumed at 4.0 s (+0–4 ms).
  - Initial CONNECTION_CLOSE NO_ERROR, quiche's "25:No recent network activity after {elapsed_us}us. Timeout:4s …".
  - Padded to 1250.
  - With a 0-RTT close when 0-RTT was sent.
  - The report's first said "no CONNECTION_CLOSE", since corrected.
- **Firefox 158:** W.
  - Fresh: silent until the idle timeout.
  - Resumed at 5.0 s: application close (0x1c APPLICATION_ERROR) in a 255-B datagram.
  - 1 of 3 at 9.2 s, pinned.
- **Safari 27:** W. Abandoned when TCP wins.
  - Fresh: nothing.
  - Resumed: one 0-RTT close.
- **Safari 26:** as 27.

#### `quic.loss.pathDegrading`

- **Chrome 155:** W. Migrate 4 unbacked-off PTOs after the request (+115–131 ms).
  - PATH_CHALLENGE padded to 1250 from a new port.
  - RETIRE_CONNECTION_ID.
  - Nothing more on the old path.
- **Firefox 158:** W, none. The connection idles out at 30 s.
- **Safari 27:** W. Close 12.0 s after the last datagram received (+0–16 ms), reason "keepalive limit reached".
- **Safari 26:** as 27.

#### `quic.keepAlive`

| Browser | Value |
|---|---|
| Chrome 155 | W: 15 s after the last ack-eliciting packet sent [1] |
| Firefox 158 | W: 15 s after the idle timer's restart [2] |
| Safari 27 | W: 2 s after the last receipt |
| Safari 26 | as 27 |

Notes:
1. 15001 ms.
2. The restart is the first ack-eliciting packet after the last receipt. 1 of 3 was 254 ms late, pinned.

#### `quic.ack`

| Browser | Value |
|---|---|
| Chrome 155 | W: every 10 packets [1] |
| Firefox 158 | W: every 2, batched [2] |
| Safari 27 | W: every 2, batched [3] |
| Safari 26 | W: every 2, batched [4] |

Notes:
1. 97% of ACKs. Every 10 after 100 at every 2nd.
2. Advances 2–7; 5.0–5.7 on average.
3. Mode 3; 4.3–4.6 on average.
4. 6.1–10.8 on average, an older Mac.

#### `quic.ack.afterRtt`

The rule: a pending ACK goes at once once a smoothed RTT has passed since the client's last. Decision 0043, amendment 2026-11-02.

How h3gen counts, over every edge capture:

- It takes the client's ACK-only packets whose only new packet is one server 1-RTT packet.
- **Late:** the server sent that packet 5 ms or more after the client's previous ACK reached it.
- **At once:** the ACK reached the server within 3 ms.

Values:

- **Chrome 155:** W, false.
  - Late: 27 of 44 at once.
  - Sooner: 249 of 323.
- **Firefox 158:** W, true.
  - Late: 70 of 70 at once (Windows 76 of 76).
  - Sooner: 72 of 215 (Windows 56 of 212). The rest at the 20 ms ACK delay.
  - All 12 macOS ones the server sent before the previous ACK reached it, but Firefox acknowledged at once, had a missing packet in their ranges.
  - That the threshold is one smoothed RTT is neqo's code (source, `RecvdPackets::ack_now`).
  - The captures are over loopback (RTT 0.04–1 ms). They cannot tell it from a fixed gap below about 0.5 ms.
  - Over every h3 capture (ACK Delay field under 3 ms for at once), the two-packet ACKs in #40 fit it too.
    - These are one response with ACK-only packets before or after it: 40 of 40 at once (Windows 122 of 122).
    - In 34 of the 40 (Windows 99 of 122) the response came 3.7 ms or more after the last ACK.
    - In the rest it came within 0.4 ms (Windows 2.3 ms, its timestamps stepping by 0.5 ms). At these RTTs the rules do not separate.
  - No capture favours counting ACK-only packets; neqo counts ack-eliciting ones (source).
  - That rule leaves 144 of macOS's 262 lone ACKs at once unexplained (Windows 195 of 426).
- **Safari 27:** W, false.
  - Late: 23 of 28 at once.
  - But 236 of 288 sooner too.
- **Safari 26:** W, false (24 of 27; 229 of 252).
- **Checked by:** `TestHTTP3EdgeMatchesBrowser` (`afterRTTRecord`) and `TestRegenerateReproducesProfiles`.
  - Each Firefox profile's late lone packets over all its pages must be acknowledged at once.
  - At most 5% (at least one) may wait, since a stall under -race can lift the smoothed RTT past 5 ms.
  - None at all fails as unexercised.
  - 38 of 38 and 37 of 37 in one run. With the rule switched off, 0 of 28, failing.

#### `quic.credit`

- **Chrome 155:** W.
  - Stream and connection every half window (6 MiB, 15 MiB). Steps within 2% above.
  - Windows fixed.
- **Firefox 158:** W. Every quarter (12 MiB, 24 MiB).
- **Safari 27:** W.
  - Stream: about half the 2 MiB window, as its application reads (irregular: pinned).
  - Connection = bytes received + 2 MiB, by its ACKs. Never more; the median raise within 3.3 KB.
  - A quarter to a third of its raises come from older counts, up to 1.7 MB lower, and in other packets than its MAX_STREAM_DATA.
- **Safari 26:** W, as 27, but:
  - Every raise after the first (two in one sample) rides with a MAX_STREAM_DATA.
  - All but two (that sample) are on the rule.
  - That sample grew the stream window to 3.2 MB.

#### `quic.keyUpdate`

| Browser | Value |
|---|---|
| Chrome 155 | W: follows only [1] |
| Firefox 158 | W: follows only |
| Safari 27 | W: follows only |
| Safari 26 | W: follows only |

Notes:
1. None started in 3,793 packets sent, 37,050 received.

#### `quic.versionNegotiation`

| Browser | Value |
|---|---|
| Chrome 155 | W: no retry; no QUIC after |
| Firefox 158 | W: retry in v2 [1] |
| Safari 27 | W: no retry |
| Safari 26 | W: no retry |

Notes:
1. Retry 1 ms after the VN. Close 0x53f8 when the server's parameters lack version_information.

#### `http3.discovery.fallback`

- **Chrome 155:** W, `race`, TCP 305–306 ms.
  - The attempt runs on. Its give-up close comes 181–194 ms after the request is on TCP (3 of 3).
  - 0-RTT holds requests 4.0 s, then fresh QUIC + TCP together.
  - Broken after 1 fresh failure.
  - Pending → TCP.
- **Firefox 158:** W, `background`.
  - `hold` behind the re-validation after a close.
  - Broken after 1 fresh failure.
  - Revalidate on a request 797–3020 ms after a failure or death.
  - Pending → TCP (a connection per request).
- **Safari 27:** W, `race`, TCP 100–129 ms.
  - Abandon on TCP: no Initial after the request is on TCP (12 of 12).
  - Broken after 2, resumed counted.
  - Pending → new QUIC 10–15 ms after the close.
- **Safari 26:** W, as 27; TCP 88–109 ms; reconnect 7–9 ms.

### Early data

- **Claim:** `http3.earlyData`.
- **Captures:** `h3-early` and `h3-too-early`, three samples each.
  - [Report](../../internal/harness/reports/h3-early-2026-10-26.md); `server.H3EdgeCaptures`.
  - On each page every request is the first on a connection resumed after the last idled out.
  - Requests: fetch POST, PUT, DELETE, an XHR POST, a GET and a POST made together, a GET, HEAD and OPTIONS fetch, a form POST navigation.
  - The `h3-too-early` server answers 425 to every request whose stream started in a 0-RTT packet.
- **Generator:** `h3gen` (`edge_early.go`), from each request made before its connection's Finished.
  - Per browser: 24 in 0-RTT and 36 held, 12 answered 425.
  - It refuses a method seen both early and held.
  - It refuses a held request that leaves both with and after the Finished, unless pinned in `sampleGaps` (Firefox, 4).
- **Validation:** required with `quic.zeroRtt`, and only then (`Validate`).
- **Tests:** `TestRegenerateReproducesProfiles` (h3gen). On mimicry's side:
  - `TestHTTP3EdgeMatchesBrowser`: each stream of a 0-RTT connection is in 0-RTT, held with or after the Finished, or sent again after a 425.
  - `TestQUICInterop` (`0rtt-too-early`): the retry on the same connection against quic-go.
- **Mutations, 2026-10-26** (each fails `TestHTTP3EdgeMatchesBrowser`):
  - Every method in 0-RTT (Chrome and Firefox, both pages).
  - No retry after 425 (both; `0rtt-too-early` too).
  - No `heldWithFinished` (Firefox, both pages).
- **Unreadable body:** a request whose body cannot be read again (no `GetBody`) returns the 425 instead of retrying.
  - Code, not captured: browsers always have the body.

#### Safari rows and dial delays

Safari's rows and Firefox's `resumedDialDelayMs` (decision 0050) are kept true by the same test. It also holds:

- each resumed connection's ClientHello to the browser's resumed ones;
- its held requests to `requestsAfterConfirmed`: after the server's answer to the Finished (`checkHeldConfirmed`);
- its dials to the profile's dial delays (`checkDialDelays`).

Dial delay checks:

- **With `BrowserDialDelay` (browser mode):**
  - None before the least sample.
  - Their median no sooner than the samples' less 3 ms.
  - Some beyond 3 ms net of stalls, once the page has enough dials that none would be a 1-in-10,000 draw.
- **By default (fast mode):** their median within 6 ms.
  - Only without the race detector, which slows mimicry's own dial setup by about the delay itself.
- **Across a profile's pages:** the browser mode's median above the fast one's by half the samples' median or more (`dialRecord`, race detector or not).
- `TestDialDelayAfterReconnect` holds the default and the draw with a fake clock.

Mutations, 2026-10-31 (each fails `TestHTTP3EdgeMatchesBrowser`):

- No close on GOAWAY: Safari 27 and 26 `h3-broken`, `closed by`.
- `earlyDataBefore` ignored: Safari `h3-early`, resumed hello order.
- Held requests released at the Finished: Safari 27 and 26 `h3-early`.
- No delay with `BrowserDialDelay`:
  - Safari 27 and 26 `h3-early`/`h3-too-early` browser mode.
  - Firefox, Safari 27 and 26 the modes' difference, 0–1 ms against 2–4.
- The delay applied by default:
  - Safari 27 and 26 and Firefox fast mode, medians 7–14 ms.
  - The modes' difference, with and without the race detector.

#### `http3.earlyData.methods`

- **Chrome 155:** W.
  - GET, HEAD, OPTIONS in 0-RTT (24 of 24).
  - POST, PUT, DELETE (fetch), XHR POST, form POST held (36 of 36).
  - The POST made with a GET held while the GET went early.
- **Firefox 158:** W, as Chrome (24, 36).
  - The connection dialled for a held request sends its control and QPACK streams in 0-RTT without it.
- **Safari 27:** W, as Chrome (24, 36).
  - With `requestsAfterConfirmed`, a held request leaves once the handshake is confirmed.
  - That is 0.8–2.3 ms after the Finished, with the ACK of the server's HANDSHAKE_DONE packet.
  - h3gen refuses one that left before: none.
- **Safari 26:** W, as 27, 0.5–2.1 ms.

#### `http3.earlyData.heldWithFinished`

- **Chrome 155:** W, false. Own datagram, 0.05–2.1 ms after the Finished (36 of 36).
- **Firefox 158:** W, true.
  - In the Finished's datagram: 31 of the 35 made before it.
  - 4 left 0.2–18.6 ms later (necko against neqo timing: pinned).
- **Safari 27:** W, false (after confirmation, 36 of 36).
- **Safari 26:** W, as 27.

#### `http3.earlyData.retryTooEarly`

- **Chrome 155:** W, true.
  - Every 425 sent again on the same connection, a new stream in 1-RTT, 0.4–0.9 ms after the Finished (12 of 12).
  - Never on a new connection.
- **Firefox 158:** W, true (12 of 12, 0.2–2.2 ms after the Finished).
  - Beside 3–4 of them per sample, a TCP connection opened and declined (pinned in `h3EdgeGaps`).
- **Safari 27:** W, true (12 of 12, 3.3–4.3 ms after the Finished).
- **Safari 26:** W, as 27 (2.1–3.2 ms).

### Stream limits

- **Claim:** `quic.streamsBlocked`.
- **Captures:** `h3-streams-blocked`, three samples each.
  - [Report](../../internal/harness/reports/h3-streams-blocked-2026-10-31.md); `server.H3EdgeCaptures`.
  - quic-go allows 2 bidirectional streams, raised by one per stream acknowledged.
  - Two bursts of 6 fetches are made together while it allows 2 more, so 4 wait.
- **Generator:** `h3gen` (`edge_blocked.go`), from each burst. It records:
  - the limit the first frame sent alone carries (checked against the streams open);
  - how many the waiting requests sent alone (one, or one each);
  - which of their streams' opening packets carried one, with which limit and where.
- **Refused:** any other STREAMS_BLOCKED, a unidirectional one, a frame sharing its packet, and a burst or sample that says otherwise.
- **Missing capture:** only this capture may be missing. A profile without it has no `streamsBlocked` and sends none (see "Not covered").
- **Tests:** `TestRegenerateReproducesProfiles` (h3gen). On mimicry's side, `TestHTTP3EdgeMatchesBrowser`.
- **Mutations, 2026-10-31** (each fails it):
  - Chrome's sent once per limit: 2 alone, the browser's 8.
  - None after a raise: Chrome's 6, 8, 12, 14 missing.
  - Firefox's without "another request still waits": 9 and 15 beyond the browser's.
  - Safari sending after a raise: 5, 7, 11, 13 beyond.
- **Lost frame:** sent again, in a packet of its own, while its limit holds and a request still waits.
  - This matches how upstream resends STREAM_DATA_BLOCKED (RFC 9000, section 13.3).
  - Not measured: no packet was lost on loopback.

#### `quic.streamsBlocked.atRequest`

- **Chrome 156 (macOS):** W, `every_request` (24 of 24).
  - One per request made at the limit, the same limit each time (4 4 4 4, 10 10 10 10).
  - Each alone in a 29-byte packet, 0–2.3 ms after the request.
- **Firefox 158:** W, `first_request` (6 of 6 bursts).
  - One for the first request made at the limit (5, 11), alone, 0–1.2 ms after it.
  - None for the three after it.
- **Safari 27:** W, `first_request` (3, 9), 0–2.1 ms after the first (6 of 6).
- **Chrome 155, Chrome 156 (Windows), Safari 26:** not measured (no capture): none.

#### `quic.streamsBlocked.afterRaise`

- **Chrome 156 (macOS):** W, `always`.
  - Right after the STREAM frame of the stream that takes the raised limit's last stream, with that limit.
  - Sent whether or not a request still waits (6, 8, 12, 14: 12 of 12 raises).
  - Also after the first of the raise's two streams, with the first MAX_STREAMS frame's limit (5, 7, 11, 13).
    - Two frames per raise is [INFERENCE] from the server packet's size.
    - Seen in 9 of 12 raises, 3 per sample.
    - mimicry's shows as its waiting request opens before or after its loop has read the second frame (both seen).
- **Firefox 158:** W, `waiting`.
  - Right before the STREAM frame of each waiting stream opened while another request still waits (6 7 8, 12 13 14).
  - None for a burst's last (6 of 6).
  - In the replay mimicry's raises go by two (see "Not covered": the spacing of requests made together). So it sends 7 and 13 by the same rule.
- **Safari 27:** W, none. Two requests still waited after each raise (6 of 6 bursts).
- **Chrome 155, Chrome 156 (Windows), Safari 26:** not measured.
