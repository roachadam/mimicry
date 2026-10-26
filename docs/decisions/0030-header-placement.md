# 0030: Header placement: move one header, keep the browser's order

- Status: Accepted
- Date: 2026-10-26
- Commits: (where it landed)

## Problem

Some servers expect a header of the caller's own (an API key, a CSRF
token) at a fixed place among the browser's, or a header in another place
than this browser version puts it. The only way to move one header was
`HeaderOrderKey`, which replaces the order outright: every header must be
named (Host and Connection too over HTTP/1.1), a profile default it does
not name is dropped, and the list has to be kept in step with the profile
and its kinds, variants and page-set placement (0021) by hand. Codebases
coming from tls-client wrote such lists on every request
([coming from tls-client](../concepts/coming-from-tls-client.md)); in
mimicry it is an escape hatch that discards everything the profile knows
to change one line.

## Options

- **Keep `HeaderOrderKey` only.** Moving one header costs the whole list,
  and the list silently goes stale when the profile, the kind or the
  variant changes.
- **A second pseudo-header** (`HeaderPlaceKey`, values like
  `x-api-key>user-agent`). Fits `net/http` request literals, but a
  string syntax to parse, nothing typed, and it cannot be set once for
  every request without also riding in `Config.Headers`, where it would
  count as a page-set header.
- **Profile-relative positions** (an index, "first", "last"). An index
  means a different place in every kind and variant; first and last are
  rarely what a server checks.
- **Placements relative to another header, as typed config:**
  `Place{Header, After | Before}`, on `Config` for every request and on
  `Request` for one. Chosen.

For a header or anchor the request does not send:

- **Skip the placement silently.** The deviation the caller asked for
  would vanish on some requests, unseen: the opposite of 0005's rule.
- **Error, nothing sent.** Chosen: the request either goes as asked or
  not at all, like `ErrNotMeasured` (0008).

For `HeaderOrderKey` together with placements:

- **Error whenever both are present.** A `Config.HeaderPlacement` would
  then forbid `HeaderOrderKey` on every request of the Transport.
- **The complete order wins over `Config`'s placements; a request asking
  for both its own order and its own placements is an error.** Chosen.
  `Exact` is treated the same way: its order is the caller's.

## Decision

`mimicry.Place{Header, After, Before}` (exactly one of After and Before;
names in any case), `Config.HeaderPlacement []Place` and
`Request.HeaderPlacement []Place`. The connection builds the request's
header lines as before, the profile's order with page-set placement,
defaults, suppressions, `Config.Headers`, cookies, framing and (HTTP/3)
dropped headers and `alt-used` all applied, then moves each named header,
`Config`'s placements first and then the request's, in slice order, to
directly after the last line of its anchor or before the first. A header
sent more than once moves as one run.

What can be placed is what the connection writes in the profile's list:
over HTTP/1.1 Host, Connection, Content-Length and Transfer-Encoding too;
over HTTP/2 and HTTP/3 the fields after the pseudo-headers. Pseudo-headers
stay in the profile's order and cannot be placed or anchored.
`Proxy-Authorization` to a forwarding proxy goes where the browser puts
it, after placement. Connection and Host are never fields over HTTP/2 and
HTTP/3, so placing against them there fails.

Errors wrap `mimicry.ErrPlacement`:

- `NewTransport`: a `Config.HeaderPlacement` entry with both or neither of
  After and Before, an empty or invalid name, a pseudo-header, or a header
  that is its own anchor.
- `RoundTrip`, before anything is sent: the same for
  `Request.HeaderPlacement`, and request placements on an `Exact` request
  or one that sets `HeaderOrderKey` (on the request or in
  `Config.Headers`).
- `RoundTrip`, from the connection: a header or anchor the request does
  not send. The request is not sent; its CORS preflight may have been.
  Over HTTP/1.1 the connection it would have used is closed, as for any
  request that fails to write.

Scope: placements apply to the caller's requests and every hop of their
redirect chains. CORS preflights are the Transport's own and WebSocket
handshakes the browser's (`WebSocketOptions` has no placement); neither
takes any. `Exact` and `HeaderOrderKey` requests ignore
`Config.HeaderPlacement`.

## Evidence

No capture: placement is a deviation from the browser by definition.
`TestHeaderPlacement` (root package) sends each placed request and the
same request without placements over HTTP/1.1 and HTTP/2 and checks that
only the moved headers changed place, and every error path;
`TestHeaderPlacementOverHTTP3` (`internal/engine/native`) does the same
over HTTP/3 against a QUIC server that decodes the field sections
(placements in order, pseudo-headers refused). quic-go's server in
`TestQUICInterop` loses header order, so it cannot check placement. With
no placement the field lists are untouched, so the
harness's captures (`TestProfilesMatchCaptures`,
`TestKindsMatchBrowserTraces`, `TestHTTP1MatchesBrowserTrace`) still
hold.

## Consequences

Moving one header is one line of config and survives profile updates; the
deviation is in the caller's code, by name. A placement on `Config` must
hold for every request the Transport sends: an anchor only some kinds
send (`Sec-Fetch-User`, `Origin`, `Cookie`) fails the others, so such a
placement belongs on the request. `Request` is no longer comparable with
`==` (it holds a slice).

## Revisit when

- A caller needs a placement that tolerates a missing anchor (an
  `Optional` flag), or one relative to the pseudo-headers.
- WebSocket handshakes need placement (`WebSocketOptions.HeaderPlacement`).
