# QUIC and HTTP/3

`quic` and `http3`, from the `h3-discovery` and `h3-page` captures, three
samples per browser and scenario, unforced and (Chrome, Firefox) with
`-h3-local-roots` ([report](../../internal/harness/reports/http3-2026-10-25.md)):
Chrome and Firefox 33 QUIC connections each (21 completed handshakes, 12
full and 9 resumed; the 12 unforced ones refused the capture server's CA and show the first
flight only), Safari 27 and 26 3 each (`h3-discovery` only: Safari opened
no QUIC connection in `h3-page`). Generated, never hand-edited: `h3gen`
derives `quic` and `http3` (but `headers`, `high`, `low`), `kindsgen` those
three, `hellogen` `tls.quic`; each checks its values against every
connection of every sample and refuses a difference not pinned with a
reason (`h3gen`'s `sampleGaps`: Chrome's one Initial ACK and two tokenless
reconnections; `kindsgen`'s `h3Unmatched`: the XHR POST, which no HTTP/2
list covers; `h3PriorityGaps`: Firefox's second-page image). Their
`TestRegenerateReproducesProfiles` keep the files equal to what the
captures give. `TestHTTP3MatchesBrowser` replays `H3DiscoverySteps` and
`H3PageSteps` through mimicry against every sample, holds what each
browser draws per connection (packet numbers, DCID length, CRYPTO cuts,
PINGs, parameter order, GREASE and ECH lengths, ACK delays, spin bit) to
the rule below for mimicry's connections and every captured one, and
`TestQPACKReproducesBrowserSections` re-encodes every captured section.
Mutations, 2026-10-25 (`-overlay`, each fails `TestHTTP3MatchesBrowser`):
no PRIORITY_UPDATE (Chrome), no QUIC dial on Alt-Svc (Firefox), HTTP/3
on Safari's next request, Firefox's HTTP/2 pseudo-header order over
HTTP/3, the SNI cut keeping the head piece whole on a resumed hello,
Safari's request before HANDSHAKE_DONE, a dial on the Alt-Svc that came
with a GOAWAY (Firefox), Chrome's transport parameters unshuffled.

| Claim | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `quic.version`, `datagramSize`, `padding` | W: 1; 1250; `scattered` | W: 1; 1252; `trailing` (zero bytes after the packets; PADDING only in a coalesced 1-RTT packet) | W: 1; 1200; `end` | W: as 27 |
| `quic.crypto` | W: `shuffle`: head 55–86, 2–10 added CRYPTO frames and 2–10 PINGs per padded packet, the first packet holding floor((len-4)/2) bytes, in all 66 hello packets (quiche's chaos protection) | W: `sni_cut` at the host's middle (6 of 13 bytes), tail then head, the longer piece cut to len/n, in all 33 (fresh: 2 packets, middle 948 bytes; resumed: 3, the tail piece as planned and the head piece what fits after it beside the 90-byte token, so a host early in the hello stays whole: cut inside it in 4 of 9) | W: `ordered`, `firstPacket` 999 | W: as 27 |
| `quic.packetNumbers` | W: `shared` from 1; 2-byte second Initial PN iff no token (33 of 33) | W: `random_initial` (first Initial PN 2–32, four of 33 at 497–813: neqo's formula, 1–1024); 1-RTT after 0-RTT; before an ACK, a PN in the fewest bytes holding PN + 1, not RFC 9000 A.2's twice that: 127–254 in 1 byte (28 of 28 Initials where the rules differ, Firefox 158 and 159 on every platform; [#91](https://github.com/roachadam/mimicry/issues/91)) | W: `per_space` | W: as 27 |
| `quic.dcidLength`, `dcidLengthRule`, `scidLength`, `newConnectionIds` | W: 8; fixed; 0; 0 | W: 8 with `biased` (23 of 33 at 8, others 10–17, neqo's max(8, 5 + (v & v>>4))); 3; 4 in all 21 completed, retire_prior_to 0, against a server limit of 4 | W: 8; 0; 0 | W: as 27 |
| `quic.transportParameters`, `transportParameterOrder` | W: 12 parameters + GREASE (0–15 bytes) + `initial_rtt` when resumed; `version_information` 1, then {1, GREASE} shuffled; `shuffled` | W: 14, incl. 0x1d empty and `min_ack_delay` 0xff02de1a = 1000; `version_information` 1, GREASE, v2, 1; `fixed` | W: 8, `max_idle_timeout` 300000; `rotated` (rotations of one cycle in 3 samples: a rotation cannot be told from another permutation). Until midday 2026-10-25 Safari 27 also sent 0xff080808 = 09, last (the first h3-discovery capture, 3 of 3); every connection since leaves it out: the edge captures (33), the h3-discovery recapture (3), and fp.impersonate.pro over HTTP/3 on port 443 (`internal/harness/reports/realcheck-2026-10-25.md`). Same Safari and macOS build throughout: an Apple-side switch, not modelled | W: 8, no idle timeout, 0xff080808 = 07 (still sent, also on the internet: realcheck); `rotated` |
| `quic.ackInitial`, `ackBeforeFinished`, `coalesce` | W: false (1 of 12 full handshakes ACKed an Initial: pinned); false; true | W: true; true (full handshakes; resumed ones ACK with the Finished, but for one on Linux: pinned); true | W: true; true; false (no datagram holds two packets) | W: as 27 |
| `quic.ackDelay`, `ackDelayConstant` | W: `actual` | W: `actual` | W: `constant`, raw 27–29 in the h3-discovery recapture (one value per connection; the first capture's three were 19–24, so the range is narrower than the browser's) | W: `constant`, 9–27 |
| `quic.ackOnlyLimit`, `ackOnlyFrame` | W: 19, `MAX_DATA` (every ACK + MAX_DATA packet follows exactly 19 ACK-only packets, no run longer) | W: 0 (runs up to 24, never made ack-eliciting) | W: 0 (no run that long: not measured) | W: as 27 |
| `quic.spinBit` | W: `zero` | W: `random` (varies within connections) | W: `spin` (0, then 1 against quic-go) | W: as 27 |
| `quic.tokens`, `zeroRtt`, `closeReply` | W: true (two reconnections after idle without one: pinned); true (9 of 9 resumed sent 0-RTT, and every resumed one of the edge captures); false (no reply to the server's close, 3 of 3) | W: true; true (9 of 9, and the edge captures'); true (CONNECTION_CLOSE NO_ERROR, frame type 0x1d, 3 of 3) | W: false; true (since decision 0050, from the edge captures: 60 of 60 resumed connections sent 0-RTT, 54 on `h3-early`/`h3-too-early` after an idle timeout, 3 after its own 12 s close, 3 after its own close on the server's GOAWAY; their first flights hold to the full handshakes' padding, packet numbers, CRYPTO cut and transport parameters); not measured (the server never closed) | W: as 27 (60 of 60) |
| `http3.settings`, `greaseSetting`, `greaseFrame` | W: 4 settings; a GREASE setting last (N and value 32-bit); GREASE frame (N 32-bit, 0–3 bytes) | W: 6 settings; none; GREASE frame (any N, 0–7 bytes) | W: 2 settings (table 16383); GREASE setting; none | W: as 27 |
| `http3.uniStreams`, `streamsWithFinished`, `earlyRequestFirst` | W: control at the handshake, 6 never written (decoder, [INFERENCE]: the only type left), encoder on the first insert; true; false | W: control, encoder, decoder at the handshake; false; true | W: control only; false; — | W: as 27 |
| `http3.requestsAfterConfirmed` | W: false: requests held out of 0-RTT left before the handshake was confirmed (19 of 31, in 6 of 6 `h3-early` and `h3-too-early` samples); no first request of a full handshake waited (the first came 300 ms or more after it) | W: false: held requests left before confirmation (32 of 35, 6 of 6 samples); the first request of a full handshake came 47–74 ms after the Finished, from the parser | W: true: the request waiting for the connection leaves 0.7–2.4 ms after the Finished with the ACK of the server's next 1-RTT packet (pn 3, its HANDSHAKE_DONE), after the control stream (3 of 3); its spin bit 1 | W: as 27 |
| `http3.closeOnGoaway` (from every QUIC connection the server sent GOAWAY on, `h3-discovery` and `h3-broken`) | W: false (the server closed every one) | W: false (it replied to the server's close) | W: true: an application CONNECTION_CLOSE (0x1d) H3_NO_ERROR 2.2–2.8 ms after the response and GOAWAY, before the server's own close a second later (3 of 3); no request was open on any | W: as 27 (3 of 3) |
| `http3.earlyStreamsWithRequest` (a connection dialled for a 0-RTT request sends the control stream in its packet) | W: false: the control stream alone first, then the request (18 of 18 on `h3-early`/`h3-too-early`) | — (`earlyRequestFirst`: the request leads the streams in their packet) | W: true: control stream then request in one 0-RTT packet (18 of 18 GET, HEAD, OPTIONS connections); the pair's connection (a POST made with the GET) sent the control stream alone, then the GET (6 of 6: Not covered) | W: as 27 (18 of 18; pair 6 of 6) |
| `http3.discovery.dialDelayMs`, `resumedDialDelayMs` (every delay, sorted, from the request that waits for it to a fresh or resumed attempt's first datagram, edge pages, a navigation's left out; nil where every delay is within the `?t=` clock's 3 ms; a dial draws one plus up to 1 ms, only with `Config.BrowserDialDelay`: off by default, decision 0050) | W: none: resumed 0–3 ms (50 dials; Chrome 156 51, Windows 0–2 in 34); its fresh dials go beside a request on TCP, none waited for | W: fresh none (its fresh dials are background validations no request waits for); resumed 48 delays, mean 5.1 ms: 11 of 0–3, 36 of 4–9, one 34 (on `h3-too-early` sample 1's first resumed connection); the draw's mean 5.6 | W: fresh 30, 3–10 ms, mean 7.1 (draw 7.6); resumed 51, mean 8.8 (draw 9.3): all but one 5–11, the 22 on `h3-too-early` sample 1's first resumed connection, as Firefox's 34 | W: fresh 27, mean 8.9 (draw 9.4): 23 of 3–10, then 17, 18, 25, 28; resumed 51, mean 8.5 (draw 9.0): all but one 6–11, then 23. Requests on open connections arrive 0–2 ms after they are made (Safari 26 up to 7), so the delay is the dial's; nothing ties the outliers to the slower Mac, so they stay, as rare as measured. Checked by `TestHTTP3EdgeMatchesBrowser` (`checkDialDelays`) and `TestDialDelayAfterReconnect` (the default, the draw) |
| `http3.qpack` | W: `index_all`, inserts first, `shorter` | W: `new_names`, request first, `always` | W: encoder not measured (no table offered); `always` | W: as 27 |
| `http3.qpack.resumed` (`h3-resume-table`, decision 0060) | W (Chrome for Testing 155, decision 0042): `remembered_repeat`, as Chrome 156 (macOS, Windows), 157 (Linux) and Edge 157: of 18 resumed connections each (Linux 19), 15 set the remembered 65536 and inserted in 0-RTT (the held navigation's in 1-RTT), and all 11–12 that wrote again once the server's SETTINGS arrived set it again first | W (158.0.1 from Mozilla's archive): `remembered`, as Firefox 159 (macOS, Windows, Linux): every resumed connection opened the encoder with the remembered 65536 in 0-RTT, and none of the 9–12 per profile that wrote again in 1-RTT set it again | none: Safari never uses a table | as 27 |
| `http3.priorityUpdate`, `maxDataFrame`, `firstDataFrame` | W: true (102 of 102 prioritised requests); one DATA frame per body | W: false; 32768, first 32224 (64 KiB: 32224 + 32768 + 544, 3 of 3) | W: false; not measured (no body) | W: as 27 |
| `http3.headers` | W: nil (114 requests: HTTP/2 order and pseudo order) | W: pseudo `:method :scheme :authority :path`, `te` dropped, `alt-used` before `referer` (before `cookie` for fetch: cookie never went over HTTP/3, its side is assumed) in 129 requests of 11 kinds and the navigation; kinds not captured over HTTP/3 have no entry (not measured). `alt-used` value `<host>:<port>` (every capture, unprivileged ports), the port left out when it is 443 (code, `altUsedValue`): seen once on the wire through a public echo (fp.impersonate.pro over HTTP/3, `internal/harness/reports/realcheck-2026-10-25.md`), no capture reaches port 443; `TestAltUsedPort` | W: nil (9 fetches) | W: as 27 |
| `http3.high`, `http3.low` | W: low `injected_image` `i` (HTTP/2 has no image-low); every other priority header the kind's HTTP/2 value | W: low `injected_image` `u=6, i`; `markup-image2.so` `u=4, i` pinned (layout-driven) | W: none; `kinds.injected_image.low` `u=7, i` from its `h3-page` HTTP/2 requests | W: as 27 |
| `kinds.xhr` POST variant (`corsChecked`, no `http1`) | K: the `h3-page` XHR POST over HTTP/3 (= its HTTP/2 list), 3 samples; `cookie` slot placed after its predecessor (assumed) | K: over HTTP/3 in HTTP/2 form (`alt-used` out, `te` back last), 3 samples; `*` and `cookie` slots assumed | W: its `h3-page` HTTP/2 requests, 3 samples; `cookie` slot assumed | W: as 27 |
| `http3.discovery` | W (local roots): `next_request`; no TCP GOAWAY; no race (0-RTT QUIC alone after the server's GOAWAY) | W (local roots): `on_alt_svc`; `goawayTcp` (every HTTP/2 connection QUIC replaced, 3 of 3; also one opened while the resumed attempt was pending, h3-broken sample 3; mimicry sends it once the connection's request is answered, whenever the connection was established: `TestGoAwayReachesTCPConnectionEstablishedAfterQUIC`); `raceAfterGoaway`, 6–9 ms, QUIC only after the TCP connection is up, its request sent and its GOAWAY out (1 ms before QUIC's first datagram, h3-broken 3 of 3; TCP first in h3-discovery 3 of 3): mimicry holds that QUIC dial until the request's response on TCP (`TestRaceAfterGoAwayWaitsForTheTCPResponse`) | W: `new_connection` (QUIC only after the server's HTTP/2 GOAWAY; `h3-b` stayed on HTTP/2); race not measured | W: as 27 |

Not measured, documented on `profile.QUIC` and `profile.HTTP3`: PMTU
probing, ECN, DATAGRAM and Retry; Safari's QPACK with a table, request
bodies and PRIORITY_UPDATE; what
`-h3-local-roots` might change after the handshake; a v2 connection's
NEW_TOKEN reused by a v1 one; broken-alternative durations beyond a page;
credit growth beyond 50 MiB or at lower bandwidth; key updates beyond
37,000 received packets; Chrome's blackhole close ("85:Network blackhole
detected") after its migrated path also went quiet; probes of resumed
attempts (Chrome's first carries four Initials, Firefox's alternate
layouts: the rules hold for fresh attempts).

### Failure, version negotiation, long transfers

`quic.loss`, `keepAlive`, `ack`, `credit`, `keyUpdate`,
`versionNegotiation` and `http3.discovery.fallback`, from the
`h3-blackhole`, `h3-broken`, `h3-vn` and `h3-download` captures
([report](../../internal/harness/reports/http3-edge-2026-10-25.md);
`server.H3EdgeCaptures`), three samples each. Generated by `h3gen`
(`edge*.go`), which checks every unanswered attempt, silenced connection,
download and fallback request against the rule and refuses a difference
not pinned in `sampleGaps` (Firefox's sample-3 resumed attempt: no cached
RTT, give-up 9.2 s; Firefox's 254 ms late keep-alive; Safari's irregular
stream credit, six samples; Safari 27's TCP 139/202 ms after a Version
Negotiation). Kept true by `TestRegenerateReproducesProfiles` (h3gen,
hellogen), and on mimicry's side by `TestHTTP3EdgeMatchesBrowser`,
`TestHTTP3DownloadMatchesBrowser` and `TestQUICInterop`. How far a batching browser's ACKs advance on average
is how many packets arrive while it works through the last (ACKs 19–47 µs
apart at the median, no fixed count or interval): machine timing, which no
field holds and the download test logs without checking.

| Claim | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `quic.loss` probes | W: first PTO 300 ms, doubling (+300, +900, +2100 ms, 6 fresh attempts); resumed from the cached RTT, max_ack_delay counted (first probe 30–33 ms); one 1250-B Initial per probe, the flight's packets' CRYPTO in turn, re-cut; one PN skipped before each probe, handshake and 1-RTT | W: 300 ms, doubling up to 15% late (+19.0–19.8 s last); resumed 3 ms (cached RTT, no ack delay; 1 of 3 pinned); both Initials again, CRYPTO as first sent, no skip | not measured: no probe before TCP won | as 27 |
| `quic.loss` give-up | W: fresh and resumed at 4.0 s (+0–4 ms): Initial CONNECTION_CLOSE NO_ERROR, quiche's "25:No recent network activity after {elapsed_us}us. Timeout:4s …", padded to 1250, with a 0-RTT close when 0-RTT was sent (the report's first "no CONNECTION_CLOSE", since corrected) | W: fresh silent until the idle timeout; resumed at 5.0 s: application close (0x1c APPLICATION_ERROR) in a 255-B datagram (1 of 3 at 9.2 s, pinned) | W: abandoned when TCP wins: nothing (fresh), one 0-RTT close (resumed) | as 27 |
| `quic.loss.pathDegrading` | W: migrate 4 unbacked-off PTOs after the request (+115–131 ms): PATH_CHALLENGE padded to 1250 from a new port, RETIRE_CONNECTION_ID, nothing more on the old path | W: none; the connection idles out at 30 s | W: close 12.0 s after the last datagram received (+0–16 ms), reason "keepalive limit reached" | as 27 |
| `quic.keepAlive` | W: 15 s after the last ack-eliciting packet sent (15001 ms) | W: 15 s after the idle timer's restart (first ack-eliciting packet after the last receipt; 1 of 3 254 ms late, pinned) | W: 2 s after the last receipt | as 27 |
| `quic.ack` | W: every 10 packets (97% of ACKs) after 100 at every 2nd | W: every 2, batched (advances 2–7; 5.0–5.7 on average) | W: every 2, batched (mode 3; 4.3–4.6 on average) | W: every 2, batched (6.1–10.8 on average, an older Mac) |
| `quic.ack.afterRtt` (a pending ACK goes at once once a smoothed RTT has passed since the client's last; h3gen counts, over every edge capture, the client's ACK-only packets whose only new packet is one server 1-RTT packet: "late" if the server sent that packet 5 ms or more after the client's previous ACK reached it, "at once" if the ACK reached the server within 3 ms; decision 0043, amendment 2026-11-02) | W: false (late: 27 of 44 at once; sooner: 249 of 323) | W: true. Late: 70 of 70 at once (Windows 76 of 76); sooner: 72 of 215 (Windows 56 of 212), the rest at the 20 ms ACK delay, and all 12 macOS ones the server sent before the previous ACK reached it but Firefox acknowledged at once had a missing packet in their ranges. That the threshold is one smoothed RTT is neqo's code (source, `RecvdPackets::ack_now`); the captures are over loopback (RTT 0.04–1 ms) and cannot tell it from a fixed gap below about 0.5 ms. Over every h3 capture (ACK Delay field under 3 ms for at once), the two-packet ACKs in #40 (one response with ACK-only packets before or after it: 40 of 40 at once, Windows 122 of 122) fit it too: in 34 of the 40 (Windows 99 of 122) the response came 3.7 ms or more after the last ACK, in the rest within 0.4 ms (Windows 2.3 ms, its timestamps stepping by 0.5 ms), where at these RTTs the rules do not separate. No capture favours counting ACK-only packets (neqo counts ack-eliciting ones, source), and that rule leaves 144 of macOS's 262 lone ACKs at once unexplained (Windows 195 of 426) | W: false (late: 23 of 28 at once, but 236 of 288 sooner too) | W: false (24 of 27; 229 of 252) | `TestHTTP3EdgeMatchesBrowser` (`afterRTTRecord`: each Firefox profile's late lone packets over all its pages acknowledged at once, at most 5% (at least one) waiting since a stall under -race can lift the smoothed RTT past 5 ms, and none at all failing as unexercised; 38 of 38 and 37 of 37 in one run, and with the rule switched off 0 of 28, failing), `TestRegenerateReproducesProfiles` |
| `quic.credit` | W: stream and connection every half window (6 MiB, 15 MiB; steps within 2% above), windows fixed | W: every quarter (12 MiB, 24 MiB) | W: stream about half the 2 MiB window as its application reads (irregular: pinned); connection = bytes received + 2 MiB (by its ACKs: never more, the median raise within 3.3 KB; a quarter to a third of its raises from older counts, up to 1.7 MB lower, and in other packets than its MAX_STREAM_DATA) | W: as 27, but every raise after the first (two in one sample) rides with a MAX_STREAM_DATA and all but two (that sample) are on the rule; that sample grew the stream window to 3.2 MB |
| `quic.keyUpdate` | W: follows only (none started in 3,793 packets sent, 37,050 received) | W: follows only | W: follows only | W: follows only |
| `quic.versionNegotiation` | W: no retry; no QUIC after | W: retry in v2 1 ms after the VN; close 0x53f8 when the server's parameters lack version_information | W: no retry | W: no retry |
| `http3.discovery.fallback` | W: `race`, TCP 305–306 ms, the attempt running on (its give-up close 181–194 ms after the request is on TCP, 3 of 3); 0-RTT holds requests 4.0 s, then fresh QUIC + TCP together; broken after 1 fresh failure; pending → TCP | W: `background`; `hold` behind the re-validation after a close; broken after 1 fresh failure; revalidate on a request 797–3020 ms after a failure or death; pending → TCP (a connection per request) | W: `race`, TCP 100–129 ms; abandon on TCP (no Initial after the request is on TCP, 12 of 12); broken after 2, resumed counted; pending → new QUIC 10–15 ms after the close | W: as 27; TCP 88–109 ms; reconnect 7–9 ms |

### Early data

`http3.earlyData`, from the `h3-early` and `h3-too-early` captures
([report](../../internal/harness/reports/h3-early-2026-10-26.md);
`server.H3EdgeCaptures`), three samples each: on each page every request
(fetch POST, PUT, DELETE, an XHR POST, a GET and a POST made together, a
GET, HEAD and OPTIONS fetch, a form POST navigation) is the first on a
connection resumed after the last idled out; the `h3-too-early` server
answers every request whose stream started in a 0-RTT packet with 425.
Generated by `h3gen` (`edge_early.go`) from each request made before its
connection's Finished (24 in 0-RTT and 36 held per browser, 12 answered
425), refusing a method seen both early and held and a held request that
leaves both with and after the Finished unless pinned in `sampleGaps`
(Firefox, 4). Required with `quic.zeroRtt`, and only then (`Validate`).
Kept true by `TestRegenerateReproducesProfiles` (h3gen) and, on
mimicry's side, by `TestHTTP3EdgeMatchesBrowser` (each stream of a 0-RTT
connection: in 0-RTT, held with or after the Finished, sent again after
a 425) and `TestQUICInterop` (`0rtt-too-early`: the retry on the same
connection against quic-go). Mutations, 2026-10-26 (each fails
`TestHTTP3EdgeMatchesBrowser`): every method in 0-RTT (Chrome and
Firefox, both pages); no retry after 425 (both; `0rtt-too-early` too);
no `heldWithFinished` (Firefox, both pages). A request whose body
cannot be read again (no `GetBody`) returns the 425 instead of retrying:
code, not captured (browsers always have the body).

Safari's rows and Firefox's `resumedDialDelayMs` (decision 0050) are kept
true by the same test, which also holds each resumed connection's
ClientHello to the browser's resumed ones, its held requests to
`requestsAfterConfirmed` (after the server's answer to the Finished:
`checkHeldConfirmed`) and its dials to the profile's dial delays
(`checkDialDelays`): with `BrowserDialDelay` (browser mode) none before
the least sample, their median no sooner than the samples' less 3 ms,
and some beyond 3 ms net of stalls once the page has enough dials that
none would be a 1-in-10,000 draw; by default (fast mode) their median
within 6 ms (without the race detector, which slows mimicry's own dial
setup by about the delay itself); and, across a profile's pages, the
browser mode's median above the fast one's by half the samples' median
or more (`dialRecord`, race detector or not). `TestDialDelayAfterReconnect`
holds the default and the draw with a fake clock. Mutations, 2026-10-31
(each fails `TestHTTP3EdgeMatchesBrowser`): no close on GOAWAY (Safari 27
and 26 `h3-broken`, `closed by`); `earlyDataBefore` ignored (Safari
`h3-early`, resumed hello order); held requests released at the Finished
(Safari 27 and 26 `h3-early`); no delay with `BrowserDialDelay` (Safari
27 and 26 `h3-early`/`h3-too-early` browser mode; Firefox, Safari 27 and
26 the modes' difference, 0–1 ms against 2–4); the delay applied by
default (Safari 27 and 26 and Firefox fast mode, medians 7–14 ms; the
modes' difference, with and without the race detector).

| Claim | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `http3.earlyData.methods` | W: GET, HEAD, OPTIONS in 0-RTT (24 of 24); POST, PUT, DELETE (fetch), XHR POST, form POST held (36 of 36), the POST made with a GET held while the GET went early | W: as Chrome (24, 36); the connection dialled for a held request sends its control and QPACK streams in 0-RTT without it | W: as Chrome (24, 36); with `requestsAfterConfirmed` a held request leaves once the handshake is confirmed, 0.8–2.3 ms after the Finished with the ACK of the server's HANDSHAKE_DONE packet (h3gen refuses one that left before: none) | W: as 27, 0.5–2.1 ms |
| `http3.earlyData.heldWithFinished` | W: false: own datagram, 0.05–2.1 ms after the Finished (36 of 36) | W: true: in the Finished's datagram, 31 of the 35 made before it; 4 left 0.2–18.6 ms later (necko against neqo timing: pinned) | W: false (after confirmation, 36 of 36) | W: as 27 |
| `http3.earlyData.retryTooEarly` | W: true: every 425 sent again on the same connection, a new stream in 1-RTT, 0.4–0.9 ms after the Finished (12 of 12); never on a new connection | W: true (12 of 12, 0.2–2.2 ms after the Finished); beside 3–4 of them per sample a TCP connection opened and declined (pinned in `h3EdgeGaps`) | W: true (12 of 12, 3.3–4.3 ms after the Finished) | W: as 27 (2.1–3.2 ms) |

### Stream limits

`quic.streamsBlocked`, from the `h3-streams-blocked` captures
([report](../../internal/harness/reports/h3-streams-blocked-2026-10-31.md);
`server.H3EdgeCaptures`), three samples each: quic-go allowing 2
bidirectional streams, raised by one per stream acknowledged, and two
bursts of 6 fetches made together while it allows 2 more, so 4 wait.
Generated by `h3gen` (`edge_blocked.go`) from each burst: the limit the
first frame sent alone carries (checked against the streams open), how
many the waiting requests sent alone (one, or one each), and which of
their streams' opening packets carried one, with which limit and where;
it refuses any other STREAMS_BLOCKED, a unidirectional one, a frame
sharing its packet, and a burst or sample that says otherwise. Only this
capture may be missing: a profile without it has no `streamsBlocked`
and sends none (see "Not covered"). Kept true by
`TestRegenerateReproducesProfiles` (h3gen) and, on mimicry's side, by
`TestHTTP3EdgeMatchesBrowser`. Mutations, 2026-10-31 (each fails it):
Chrome's sent once per limit (2 alone, the browser's 8); none after a
raise (Chrome: 6, 8, 12, 14 missing); Firefox's without "another request
still waits" (9 and 15 beyond the browser's); Safari sending after a
raise (5, 7, 11, 13 beyond). A lost frame is sent again, in a packet of
its own, while its limit holds and a request still waits, as upstream
resends STREAM_DATA_BLOCKED (RFC 9000, section 13.3): not measured, no
packet was lost on loopback.

| Claim | Chrome 156 (macOS) | Firefox 158 | Safari 27 | Chrome 155, Chrome 156 (Windows), Safari 26 |
|---|---|---|---|---|
| `quic.streamsBlocked.atRequest` | W: `every_request`: one per request made at the limit, the same limit each time (4 4 4 4, 10 10 10 10), each alone in a 29-byte packet, 0–2.3 ms after the request (24 of 24) | W: `first_request`: one for the first request made at the limit (5, 11), alone, 0–1.2 ms after it; none for the three after it (6 of 6 bursts) | W: `first_request` (3, 9), 0–2.1 ms after the first (6 of 6) | not measured (no capture): none |
| `quic.streamsBlocked.afterRaise` | W: `always`: right after the STREAM frame of the stream that takes the raised limit's last stream, with that limit, whether or not a request still waits (6, 8, 12, 14: 12 of 12 raises); also after the first of the raise's two streams, with the first MAX_STREAMS frame's limit (5, 7, 11, 13; two frames per raise is [INFERENCE] from the server packet's size), in 9 of 12 raises, 3 per sample: mimicry's shows as its waiting request opens before or after its loop has read the second frame (both seen) | W: `waiting`: right before the STREAM frame of each waiting stream opened while another request still waits (6 7 8, 12 13 14), none for a burst's last (6 of 6); in the replay mimicry's raises go by two (see "Not covered": the spacing of requests made together), so it sends 7 and 13 by the same rule | W: none: two requests still waited after each raise (6 of 6 bursts) | not measured |
