# 0060: QPACK on a resumed connection uses the remembered SETTINGS

- Status: Accepted; amended 2026-11-07 (a late fetch on the page; Chrome 155 and Firefox 158 measured)
- Date: 2026-11-06
- Commits: the one that closes [#90](https://github.com/roachadam/mimicry/issues/90)

**Summary.** A QUIC connection resumed with 0-RTT now starts its QPACK
encoder from the server's SETTINGS remembered with its ticket, as Chrome,
Edge and Firefox do: it sets the table's capacity and inserts in 0-RTT,
before the server's own SETTINGS arrive. Chrome and Edge then set the
capacity again once those arrive; Firefox does not. A new capture,
`h3-resume-table`, measured it on macOS, Windows and Linux, and a profile
field, `http3.qpack.resumed`, says which.

## Problem

With 0-RTT a client sends requests before the server's SETTINGS, so it
can only use a QPACK dynamic table from settings it remembered (RFC 9114,
section 7.2.4.2; RFC 9204, section 3.2.3). mimicry remembered none: its
0-RTT requests used no table, and the table filled only once the server's
SETTINGS came. One `chrome-157-linux` `h3-page` sample had shown Chrome
setting the capacity and inserting in 0-RTT, then setting it again, and 13
Firefox side connections set it in 0-RTT; that was pinned
(`qpackRepeatedCapacity`). No capture was built to show the rule.

## Options

- **Remember the SETTINGS per origin in the engine**, beside the Alt-Svc
  state. Simple, but a ticket and the settings it was issued with could
  come apart (a ticket from one connection, settings from a later one).
- **Store the SETTINGS with each ticket**, as QUICHE does (it holds a new
  ticket until the server's SETTINGS have arrived, then caches both).
  Each resumption uses exactly what the server sent on the connection
  that issued its ticket.
- **Leave it as a known gap.** The encoder stream shows it to any server.

## Decision

Store them with each ticket.

- `quic.DialConfig.RememberApplication` holds each 0-RTT ticket until
  `Conn.SetApplicationState` gives it the server's SETTINGS payload, then
  stores both in the ticket's `SessionState.Extra`, beside the remembered
  transport parameters; `Conn.RememberedApplicationState` returns them on a
  connection that resumed with 0-RTT.
- `internal/h3` gives the payload to the QUIC connection when the server's
  SETTINGS arrive, and, on a 0-RTT connection, starts the encoder from the
  remembered ones before it reads the server's.
- `internal/qpack`'s encoder queues Set Dynamic Table Capacity
  instructions instead of keeping one pending: `RepeatCapacity` adds
  Chrome's second one even when the capacity did not change, after a
  remembered one not sent yet (Chrome then sends both together).
- `http3.qpack.resumed`, derived by h3gen from `h3-resume-table`:
  `remembered` (Firefox) or `remembered_repeat` (Chrome, Edge). Empty for
  a profile without the capture: its 0-RTT requests use no table, as
  before.

## Evidence

`h3-resume-table` (2026-11-06), 3 samples per browser: the `h3-early`
schedule's resumed connections, all GETs, against a server allowing a
64 KiB table and 100 blocked streams, captured on the second Mac (Chrome
156, Firefox 159, Edge 157), the Windows PC (the same, the PC's Edge
policies lifted) and WSL Ubuntu (Chrome 157, Firefox 159). Of 18 resumed
connections with an encoder stream per profile:

| Browser | opened in 0-RTT, capacity 65536 | wrote again in 1-RTT | capacity set again there |
|---|---|---|---|
| Chrome, Edge (5 profiles) | 15 (the navigation's 3 in 1-RTT) | 6 | 6 |
| Firefox macOS, Windows, Linux | 18 | 1, 10, 15 | 0 |

Chrome repeats the capacity with the next encoder output after the
server's SETTINGS arrive: before a pair's second request, or, where its
navigation was sent after the handshake, as two instructions in a row.
`TestQPACKReproducesBrowserSections` re-encodes every `h3-discovery`,
`h3-page` and `h3-resume-table` connection with the rule, the
`chrome-157-linux` `h3-page` connection once pinned included, and
`TestHTTP3EdgeMatchesBrowser` replays the page through mimicry, comparing
each connection's encoder stream (how often it set the capacity, the
packet its first bytes went in). A first capture of the page sent its GET
navigation without the page's issue time (`?t=`, `h3Page`'s script); it
was fixed and the page recaptured before deriving.

## Consequences

- Every Chrome, Edge and Firefox profile but `chrome-155-macos` and
  `firefox-158-*` (those browsers can no longer be installed) fills its
  table in 0-RTT on a resumed connection. Safari never uses a table.
- `qpackRepeatedCapacity` is gone.
- The replay shows two differences around it, pinned: Chrome and Edge
  hold a navigation out of 0-RTT (3 of 3 samples each) where mimicry
  sends it, which `http3.earlyData` does not describe; and the replay
  makes a pair's two requests together, where the page made the second
  after the loopback handshake.
- A server that accepts 0-RTT and then sends a smaller table capacity or
  fewer blocked streams than remembered is broken (RFC 9114, section
  7.2.4.2): the 0-RTT inserts and sections may not fit them. mimicry
  closes the connection with H3_SETTINGS_ERROR, as QUICHE closes with its
  ZERO_RTT settings mismatch errors; the encoder itself ignores a lower
  capacity once it has inserted.

## Revisit when

A browser stops remembering the SETTINGS or repeats differently, a
capture holds a server whose SETTINGS change between connections, or
navigations' 0-RTT gets a rule.

## Amendment 2026-11-07: a late fetch, and the older versions

Firefox 158 on macOS put every encoder write in 0-RTT (18 of 18), so
whether it sets the capacity again was not shown (Firefox 159 on macOS:
1 of 18). The page now carries a late fetch 500 ms after two of its
resumed requests, on the same connection and after the server's SETTINGS,
with a header no request had (`x-late`, not in QPACK's static table), so
every encoder writes again there, Firefox's, which inserts only new names,
included. Every browser was captured again with it (2026-11-07), and the
older versions with it: Firefox 158.0.1 from Mozilla's archive (the
brand), and Chrome 155 as Chrome for Testing 155.0.8059.84, whose capture
feeds `qpack.resumed` alone (decision 0042's testing-build rule: QPACK is
the network stack's). Of the resumed connections with an encoder stream:

| Profile | resumed | opened in 0-RTT | wrote again in 1-RTT | capacity set again there |
|---|---|---|---|---|
| `chrome-155-macos` (Chrome for Testing) | 18 | 15 | 12 | 12 |
| `chrome-156-macos`, `edge-157-macos` | 18 | 15 | 11 | 11 |
| `chrome-156-windows`, `edge-157-windows` | 18 | 15 | 12 | 12 |
| `chrome-157-linux` | 19 | 16 | 12 | 12 |
| `firefox-158-macos`, `firefox-158-windows` | 18 | 18 | 9 | 0 |
| `firefox-159-macos` | 17 | 17 | 12 | 0 |
| `firefox-159-windows` | 18 | 18 | 9 | 0 |
| `firefox-159-linux` | 18 | 18 | 12 | 0 |

So every Chrome, Edge and Firefox profile now fills its table in 0-RTT.
The navigations Chrome and Edge held (3 per profile) opened their encoder
in 1-RTT. Firefox's short-lived side connections showed up in some samples
on every platform this time, so the replay only logs the connections they
renumber.
