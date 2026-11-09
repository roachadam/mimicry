# 0062: TLS session tickets can be shared and saved (TicketCache)

- Status: Accepted
- Date: 2026-11-08
- Commits: (this change)

**Summary.** `Config.Tickets` takes a `TicketCache`, which keeps the TLS
and QUIC session tickets of every Transport given it and saves and loads
them as JSON. A process that loads one resumes its first connections where
it would otherwise make full handshakes, as one browser that stayed open
does. Chrome 156 and Firefox 159 keep tickets in memory only, so a saved
cache does not model a relaunched browser. Without the field nothing
changes: each Transport keeps its own tickets.

## Problem

Cookies (`Jar.WriteJSON`), Alt-Svc entries (`AltSvcCache`) and client hint
opt-ins (`ClientHintStore`) could be saved; tickets lived in each engine's
memory. A process that restarted, or handed an identity to another worker,
made full handshakes on every first connection
([#103](https://github.com/roachadam/mimicry/issues/103)). httpcloak saves
tickets with its sessions (`Session.Save`, `LoadSession`) and has a shared
ticket cache (`SessionCacheBackend`).

## Options

- **Save inside each Transport** (a method on Transport): no sharing between
  a browser's tabs, and an API unlike the other stores.
- **A shared store in `Config`, shaped like `AltSvcCache`** (chosen):
  `NewTicketCache`, `WriteJSON`, `ReadJSON`, `Clear`. Transports given the
  same cache share tickets, as a browser's tabs do; the TLS 1.2 policy
  stays each profile's (`handshake.Store` holds the tickets, each engine
  a view with its profile's policy).
- **A pluggable backend interface** (httpcloak): a remote cache shared by
  many processes would hand one single-use ticket to several, which no
  browser does. Not now.

The saved form needed two choices:

- **Proxy keys.** Tickets are keyed per proxy (decision 0013), by the
  proxy's URL, credentials included. Writing that to a file would leak the
  credentials, so a proxy is named by the first 16 bytes of the SHA-256 of
  its URL, in memory and on disk alike: tickets stay apart per proxy, and
  a loaded ticket resumes only through the same proxy URL.
- **The session encoding.** uTLS's `SessionState.Bytes` with the ticket:
  it keeps `SessionState.Extra`, where the QUIC copy stores the server's
  transport parameters, version and HTTP/3 SETTINGS for 0-RTT (decision
  0060), so a loaded QUIC ticket resumes with 0-RTT as it would have in
  the process that received it. The encoding may change between uTLS
  versions; a file that no longer parses fails `ReadJSON`.

A swept proxy (decision 0040) drops its tickets from a Transport's own
store; from a shared cache it does not, since another Transport may still
use the proxy. They expire there (7 days at most).

## Decision

`TicketCache` and `Config.Tickets` as above. The docs say what a loaded
cache models (a browser that stayed open), that a cache is loaded into one
process at a time, and that its file is kept like a password.

## Evidence

- **Browsers across a relaunch** (2026-11-08, this Mac, macOS 27.0.1): a
  TLS 1.3 server logging each ClientHello's `pre_shared_key` and whether
  the handshake resumed; each browser launched twice on one persistent
  profile, quit gracefully (SIGTERM) in between. Chrome 156.0.8078.80:
  in each run the first connections were full and a later one resumed;
  the second run's first hellos offered no PSK. Firefox 159.0: the same.
  Neither keeps tickets across a relaunch. Safari not checked (it runs
  only as the user's own Safari, which the check would quit). The check
  was a throwaway server, not a harness scenario.
- `TestSavedTicketsResume` (`internal/engine/native`): an engine's store
  written and read into another engine's resumes the second engine's
  first connection; it fails if `Import` adds nothing.
- `TestSavedTicketKeys`: a ticket received through a proxy with
  credentials, in a frame partition, over HTTP/3, saves without the proxy's
  URL or credentials, and loads under its own key only (not another
  password's, protocol's, partition's).
- `ExampleTicketCache`: over HTTP/2, the restored cache's first request
  resumes.
- Against cloudflare-quic.com (2026-11-08): a process made five requests
  (two over HTTP/2, then HTTP/3) and saved its tickets and Alt-Svc cache
  (one TCP ticket, one QUIC ticket); a second process loaded both and its
  first request went over HTTP/3, resumed (`resp.TLS.DidResume`). The QUIC
  ticket carried the remembered transport parameters, which the QUIC copy
  stores only with tickets allowing 0-RTT.

## Consequences

- A worker can take over an identity with its sessions; nothing changes
  for a caller who doesn't set the field.
- Tickets through a proxy are keyed by a hash: two URLs naming the same
  proxy differently (a password changed, a port written out) do not share
  tickets.
- A loaded file used by two processes would resume a ticket twice; only
  the documentation prevents it.

## Revisit when

A capture shows a browser keeping tickets across a relaunch (Safari is
unchecked), or callers need a store shared between processes with
single-use enforced.
