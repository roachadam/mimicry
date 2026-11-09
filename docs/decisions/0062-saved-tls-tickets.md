# 0062: TLS session tickets can be shared and saved (TicketCache)

- Status: Accepted
- Date: 2026-11-08
- Commits: (this change)

**Summary.** `Config.Tickets` takes a `TicketCache`, which keeps the TLS
and QUIC session tickets of every Transport given it and saves and loads
them as JSON. A process that loads one resumes its first connections where
it would otherwise make full handshakes. Chrome 156 and Firefox 159 keep
tickets in memory only, so for them a loaded cache models a browser that
stayed open; Safari 27 resumes its QUIC sessions after a relaunch too.
Without the field nothing changes: each Transport keeps its own tickets.

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
  a view with its profile's policy; a view keeping no TLS 1.2 tickets,
  QUIC's, never hands one out).
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
  the process that received it. A loaded TLS 1.3 ticket never outlives its
  own lifetime, whatever the file says. The encoding may change between
  uTLS versions; a file that no longer parses fails `ReadJSON`.

Two rules for a shared cache, chosen by the maintainer (2026-11-08) after
review:

- **A swept proxy's tickets leave it** (decision 0040), as they leave a
  Transport's own store. Keeping them until they expire, since another
  Transport might still use the proxy, grows without bound under rotation:
  at 0040's rotation rate (a new proxy every 120 ms) and about 1.4 KB a
  ticket, some 84 MB at a 2-hour ticket lifetime and 7 GB at 7 days, TLS
  1.2 tickets until their certificate expires. Counting each proxy's users
  per cache, or a size cap, were the alternatives; dropping costs another
  Transport still using the proxy one resumption at most.
- **One browser per cache.** A ticket links the connections that use it,
  so a cache shared by a Chrome and a Firefox Transport would join two
  identities. The first Transport, or the first file read, binds the cache
  to its browser (`profile.Spec.Browser`); `NewTransport` refuses a
  profile of another, `ReadJSON` another browser's tickets (each saved
  entry names it). Other versions of the same browser may share it, as an
  identity moves to a new release; one exact profile per cache would make
  every upgrade start over.

## Decision

`TicketCache` and `Config.Tickets` as above, with those two rules. The
docs say what a loaded cache models per browser, that a cache is loaded
into one process at a time, and that its file must be kept secret and
read only by its writer.

## Evidence

- **Browsers across a relaunch** (2026-11-08, this Mac, macOS 27.0.1), with
  throwaway servers logging each ClientHello's `pre_shared_key` and
  `early_data` and whether the handshake resumed, TCP and (quic-go) HTTP/3
  on one port; the page reloaded every 4 s and the server closed idle
  connections after 2 s, so each run made new connections. Each browser
  was launched twice on one persistent profile and quit gracefully in
  between.
  - Chrome 156.0.8078.80 (TCP, twice; and with HTTP/3 on `tk.test`): within
    a run the later connections resumed; the second run's first TCP hellos
    and its one QUIC hello offered no PSK.
  - Firefox 159.0 (TCP; then HTTP/3): within a run every later QUIC
    connection resumed with 0-RTT; after the relaunch it went to HTTP/3 at
    once (Alt-Svc kept) but its first QUIC hello offered no PSK.
  - Safari 27 (HTTP/3, the user's own Safari quit with AppleScript, four
    relaunches over two sessions): every relaunch's first QUIC connection
    resumed with 0-RTT; no TCP hello ever offered a PSK, as decision 0013
    found. No Safari process was left running; the WebKit networking
    service (an XPC service started days earlier, parent launchd) was.
    [INFERENCE] Safari's tickets live there, and a logout or reboot
    clears them.
- `TestSavedTicketsResume` (`internal/engine/native`): an engine's store
  written and read into another engine's resumes the second engine's
  first connection; it fails if `Import` adds nothing.
- `TestSavedTicketsThroughProxy`: through an HTTP proxy with credentials,
  the saved file holds neither password, user nor proxy host; loaded into
  another engine, the ticket resumes through the same proxy URL and not
  with another password. It fails if a call site keys tickets by the raw
  route key.
- `TestSavedTicketKeys`: a ticket in a frame partition, through a proxy,
  over HTTP/3, loads under its own key only.
- `TestSavedTicketVersionsAndExpiry`: TLS 1.3 and TLS 1.2 (reuse, single
  use) tickets round-trip; a TLS 1.2 one never reaches QUIC's view;
  expired tickets are neither written nor loaded.
- `TestReadTicketsRefusesMalformed`: a bad origin, proxy, ticket or state
  fails `ReadTickets` with nothing added.
- `TestTicketStoreIsOneBrowsers`: a Chrome 156 store takes a Chrome 155
  engine and refuses a Firefox one; Chrome's saved file is refused by a
  Firefox store, and binds an unbound store to Chrome.
- `TestSweptProxiesAreForgotten`, run with a shared store as well: a
  forgotten proxy's next connection is a full handshake either way.
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
- A resumed connection is authenticated by the ticket's secret, not the
  server's certificate: whoever can write a file `ReadJSON` reads can
  stand in for every server it names. Checking the saved certificates
  would not help (the certificates are public; the secret is what counts),
  so the docs say to read only files you wrote.
- Tickets through a proxy are keyed by a hash: two URLs naming the same
  proxy differently (a password changed, a port written out) do not share
  tickets.
- A loaded file used by two processes would resume a ticket twice; only
  the documentation prevents it.

## Revisit when

Callers need a store shared between processes with single-use enforced,
or a capture shows Chrome or Firefox keeping tickets across a relaunch.
