# 0036: Fast QUIC fallback by default, the browser's timing on request

- Status: Accepted
- Date: 2026-10-28
- Commits: `1d9e67f`

**Summary.** By default a request stops waiting on an unanswered 0-RTT
QUIC connection at the profile's race delay (about 300 ms), and on a
failed attempt at once, then goes on TCP; the browser's longer holds from
0028 stay available as `Config.BrowserQUICFallback`. It is the maintainer's
decision: the fastest behaviour that stays unfingerprintable, since the
change shows only where QUIC fails and every healthy-server behaviour is
unchanged. Requests no longer stall 4-5 s behind a dead 0-RTT attempt, but
waits on a live connection whose path died keep the browser's timing.

## Problem

Decision [0028](0028-quic-failure-model.md) made each browser's QUIC
failure behaviour profile data, timing included. Some of that timing is a
stall a user notices: Chrome holds every request behind a 0-RTT attempt the
server never answers until the attempt gives up (4.0 s, `zeroRttHolds`);
Firefox holds the requests made while its resumed re-validation after a
server's close is pending until it gives up (5 s; the requests waited
3.0–7.2 s in the `h3-broken` captures, `hold`); a request whose attempt
failed outright (Version Negotiation not retried) still waits out the race
delay before going on TCP. mimicry is used to automate ordinary user actions
([0029](0029-scope-what-gets-modelled.md)), and the maintainer's decision
was the fastest behaviour that stays unfingerprintable, with the browser's
exact timing kept as an option.

The defaults rule ([0005](0005-request-kinds-api.md), AGENTS.md: "defaults
are the browser") says every deviation is one the caller asks for. This
decision makes one deviation the default, so it has to say why it cannot
be seen. ([0037](0037-coalescing-opt-out.md) later stated the general
rule this decision follows: a costly, low-risk browser behaviour gets the
faster default and an option for the browser's.)

## Options

- **Race TCP at once on every new QUIC connection** (what some clients
  do). Fastest on every failure, but a browser on a healthy network opens
  no TCP connection beside a QUIC connection that comes up within its race
  delay: a server sees an extra TCP connection on every page. Rejected.
- **Keep the browser's timing everywhere** (decision 0028 as it was).
  Faithful on failure paths no vendor plausibly checks, at the cost of
  seconds-long stalls on networks that drop UDP after the first
  connection, or behind a server that stops answering QUIC.
- **Shorten only the holds, and only on failure paths** (chosen). Every
  rule that applies while the server answers stays as it is: each
  profile's race delay before TCP (Chrome 305 ms, Safari 88–129 ms),
  0-RTT requests sent at once, Firefox's background validation. What
  changes is only how long a request waits for a QUIC connection whose
  server has said nothing, or one that already failed.
- **Shorten the dead-path waits too.** A live connection whose path stops
  answering holds its pending requests until the browser gives up on it
  (Firefox at its 30 s idle timeout, Safari after 12 s; Chrome migrates).
  Not chosen: those requests were sent and may have reached the server,
  and the path may come back (Wi-Fi roaming, NAT rebinding), where the
  browser carries on over QUIC; resending them on TCP early would put a
  duplicate request on the server's side of a network that only
  hiccupped, and telling a dead path from a slow server needs a
  path-degrading signal mimicry models for Chrome and Safari only. The
  waits stay the browser's
  ([EVIDENCE.md, Not covered](../../profiles/EVIDENCE.md#not-covered-by-any-claim)).

## Decision

By default (`Config.BrowserQUICFallback` false):

- **0-RTT holds end at the race delay.** A request on a resumed QUIC
  connection whose handshake the server has not answered waits at most the
  profile's race delay from the connection's start (`tcpDelayMs`: Chrome
  305–306 ms), or for a profile without one its first probe timeout
  (`quic.loss.initialPtoMs`: Firefox 300 ms), then goes on TCP. A request
  already sent in 0-RTT stays on the QUIC connection as the browser leaves
  it (its answer, if one ever comes, is dropped); one held for the
  handshake (a POST) is withdrawn unsent. Requests made after that go on
  TCP at once until the handshake completes. The QUIC connection carries
  on exactly as the browser's: its probes, its give-up and close at 4.0 s
  (Chrome) or 5.0 s (Firefox), and what follows (Chrome's fresh attempt
  beside TCP, Firefox's re-validation) are unchanged.
- **A failed attempt holds nothing.** A request waiting for an attempt
  that failed before the race delay (Version Negotiation it does not
  retry, an error from the socket) goes on TCP at once.

`Config.BrowserQUICFallback` keeps decision 0028's timing for both.

## Evidence

- `TestHTTP3EdgeMatchesBrowser` runs every edge page in both modes. With
  `BrowserQUICFallback` every comparison is as before. With the default,
  the differences are exactly `h3EdgeFastGaps`, all on the pages where
  QUIC fails (`h3-broken`: the requests behind Chrome's 0-RTT attempt and
  Firefox's re-validation, and the TCP connections they open; `h3-vn`:
  Safari's TCP connection after its failed attempt), and each must appear;
  `h3-blackhole` (fresh attempts), `h3-v2`, `h3-early` and `h3-too-early`
  (healthy servers, 0-RTT accepted, rejected and answered 425) are
  identical in both modes. `TestHTTP3MatchesBrowser` and
  `TestHTTP3DownloadMatchesBrowser` (healthy pages) run with the default.
- Why a healthy connection cannot trigger it: the trigger is a resumed
  connection with no handshake reply from the server after the race
  delay. Over loopback the reply comes within about 1 ms; on a real
  network within one round trip plus the server's processing. The race
  delay is the browser's own bound for the same situation on a fresh
  connection (Chrome races TCP at 305 ms whatever the RTT), so a path
  slow enough to trigger the fast rule makes the browser open a TCP
  connection beside its fresh QUIC attempts too.
- The browsers themselves send the held request again on TCP when the
  attempt gives up (Chrome's `h3-broken` closed1.cs: on TCP 4.0 s after
  its 0-RTT copy; Firefox's closed2/closed3: 3.0–7.2 s), so a server that
  receives the 0-RTT copy late sees two copies from a browser too. The
  fast default changes when the second one comes, not whether.

## Consequences

- A request no longer stalls 4–5 s behind an unanswered 0-RTT connection
  or up to 300 ms behind a failed attempt. In the `h3-broken` replay,
  Chrome's closed1.cs goes on TCP after 307–308 ms instead of 4.0 s and
  its closed2.cs at once instead of after 1.0–2.0 s; Firefox's closed2.cs,
  made when its resumed attempt was already 300 ms unanswered, at once
  (2–6 ms) instead of after 3.0–7.2 s; Safari's request after Version
  Negotiation in `h3-vn` after 4–9 ms instead of 118–212 ms.
- On a path where the server's handshake reply takes longer than the race
  delay (round trips above about 300 ms, or the reply's first flight lost
  on a resumed connection), the request also goes on TCP, where the browser
  would have waited on QUIC: an extra TCP connection and a second copy of
  a 0-RTT request, on a path the browser's own race treats as failing.
  That is the only case where the default is visible on a network that
  works, and decision 0029 rates failure-path timing as not checked.
- Live-connection deaths (dead paths) keep the browser's timing: a request
  pending on a Firefox connection whose path died still waits up to 30 s
  ([EVIDENCE.md, Not covered](../../profiles/EVIDENCE.md#not-covered-by-any-claim)).
- What-mimicry-does lists the option. Code that measures fidelity on
  failure paths (the harness, research) sets `BrowserQUICFallback`.

## Revisit when

- A vendor is seen timing a client's fallback from QUIC to TCP, or
  flagging a TCP connection that follows a 0-RTT attempt within the race
  delay.
- A capture shows a browser bounding its 0-RTT hold itself (Chrome's
  source has a "QUIC zero-RTT job" race whose timing the captures did not
  show), or a model of dead-path detection for Firefox lands.
