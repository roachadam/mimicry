# 0055: Chrome's client hints opt-in

- Status: Accepted
- Date: 2026-11-04
- Commits: the one that closes [#62](https://github.com/roachadam/mimicry/issues/62)

**Summary.** A Chrome profile now answers a site's client hints opt-in as
Chrome 156 does. A navigation response's `Accept-CH` is stored per origin
(`Config.ClientHintStore`). Later requests carry the stored hints. A
`Critical-CH` hint the navigation lacked makes mimicry send it once more.
`Clear-Site-Data` clears the opt-in. The hint values describe one captured
machine: the profile's own by default, or another captured machine
(`Config.ClientHints`, `profiles.Machines`, `profiles.RandomMachine`).
Firefox and Safari ignore the opt-in, and so do their profiles.

## Problem

mimicry sent only Chrome's low-entropy hints (`sec-ch-ua`, `-mobile`,
`-platform`), whatever a site asked for. A site that opts in to
high-entropy hints, as bot-management vendors and Google do, then saw a
Chrome that ignored its `Accept-CH`. A `Critical-CH` site saw no restart.
Issue [#62](https://github.com/roachadam/mimicry/issues/62); the captures
came first, in PR #83 (`client-hints` scenario,
[capturing](../capturing.md)), and were retaken with the steps this
decision's first draft found missing.

## Evidence

The `client-hints` captures, 3 samples each. The first set (PR #83,
2026-11-03) was retaken on 2026-11-04 with steps added for what it left
out: fetches with the page's own headers, redirected navigations into an
opted-in origin, `Accept-CH` and `Critical-CH` on a redirect, a form POST
answered with `Critical-CH`, and `Clear-Site-Data` on a fetch. Every
sample sends the same names in the same order.

| Browser | Machine | Requests | Restarts |
|---|---|---|---|
| Chrome 156.0.8078.80 | macOS 27.0.1, arm | 90 | 6 |
| Chrome 156.0.8078.81 | Windows 10, x86 | 90 | 6 |
| Firefox 159.0.1 | macOS 26 | 84 | 0 |
| Safari 27.0.1 | macOS 27 | 84 | 0 |

Chrome's name orders are the same on macOS and Windows. Firefox and Safari
send no hint beyond their lists and never restart.

Two retakes were needed. Safari's first sample retried a navigation after
a connection reset, which no other sample did. Chrome on the Mac first ran
with an external DPR-1 display attached and its window on it: navigations
said DPR 2 (the main display), subresources DPR 1, a mixture no one
machine describes, which kindsgen refused. Both were taken again, on the
built-in display alone for Chrome.

What Chrome does:

- **Opt-in.** A top-level navigation's `Accept-CH` replaces its origin's
  set, a redirect's included; a response without one keeps it. A form
  POST's response opts in too. `Accept-CH` on a subresource's response is
  ignored. The opt-in applies at once to the opted-in document's own
  subresources, and to later navigations of the origin.
- **Which origins get the hints.**
  - The page's own origin, including same-origin frames, gets every hint
    its origin opted in to.
  - Another origin gets the low-entropy three only. This includes
    `sub.ch-a.test`, a subdomain of the same site.
  - A third party named in a `Permissions-Policy` allowlist, or in a
    `Delegate-CH` meta tag, gets every hint.
  - An origin left out of an explicit allowlist gets no hints at all, not
    even `sec-ch-ua`.
- **Order on documents.** A navigation's or frame's hints go as one run,
  in a fixed order, where its list has the low-entropy three: this is
  `clientHints.hints`. The order is the same over HTTP/2, HTTP/1.1 and
  HTTP/3. The run is lowercase over HTTP/1.1 too. The rule holds for the
  navigation's other lists: a redirect's hop (same-origin, and from
  another origin into the opted-in one) has the run after
  `sec-fetch-dest`, a form POST after `cache-control`, where those lists
  keep `sec-ch-ua`.
- **Order on subresources.** The hints come out in Blink's header hash
  table order, with `user-agent` among them: the table of decision 0021.
  The order of entry matters. Blink's
  `FrameFetchContext::AddClientHintsIfNecessary` adds them in another order
  than navigations send them, and with that order the table reproduces
  every captured subresource. That order is `clientHints.tableOrder`. The
  navigation order does not reproduce them: with it, the table fails the
  captures that carry all 24 hints. A fetch with a header of the page's
  own (`x-mimicry`) or a JSON body (`content-type`, the table's as-page
  name) comes out in the same table, the page's names entering first.
- **`Critical-CH`.** A hint that the response's `Accept-CH` asks for, that
  navigations send, and that the request lacked makes Chrome send the
  request again, once. A redirect is sent again too, before its hop is
  followed. The second request goes on the same connection; all 36
  restarts did, over HTTP/2, HTTP/1.1 and HTTP/3. Its hint run goes right
  after `accept` (`clientHints.restartAfter`). Chrome does not restart
  for:
  - a hint already sent;
  - `Width` or `Save-Data`;
  - an unknown name;
  - a form POST (its `Accept-CH` is kept; the POST is sent once).

  A response's own `Permissions-Policy: ch-ua-model=()` did not stop the
  restart from sending Model.
- **`Clear-Site-Data`.** `"clientHints"` or `"*"` on a navigation's
  response empties the origin's set from the next request on. On a
  fetch's response it empties it for the next navigation, but the page
  that made the fetch keeps sending its hints: a document's hints are
  fixed when it loads.
- **Values.**
  - Machine values: platform version, arch, bitness, model, wow64, form
    factors, DPR, viewport, device memory, and the `prefers-*` settings.
  - Browser value: the full version.
  - Network values: `rtt`, `downlink` and `ect`. `downlink` stays within
    ±10% of 1.6 Mbps in steps of 0.05. `rtt` takes values from 0 to 150 in
    steps of 50. Both are fixed per host within a sample, and differ
    between a host's navigations and its subresources. `ect` is `4g`.
- **Not sent.**
  - `Sec-CH-UA-Form-Factor` (singular): unknown to Chrome 156.
  - `Save-Data`: the data saver is off.
  - `Width` and `Sec-CH-Width`: sent only for an `<img>` with `sizes`.

Each fact above comes from the captures. Blink's source gave the subresource
order of entry, which the captures then confirmed, as with decision 0021's
tables.

## Options

### Where the opt-in is kept

- **Inside the Transport, like the Alt-Svc cache.** No caller could save
  or share it.
- **Inside the cookie jar.** This couples unrelated state.
- **A `Config` interface with an in-memory default.** Chosen: callers who
  persist a jar per identity can persist this too.

### Where the hint values come from

- **Error until the caller sets them (`ErrNotMeasured`).** A request would
  fail just because a site opted in.
- **Leave out the hints the caller did not set.** Chrome never does this.
- **The captured machine's values, with captured alternatives.** Chosen.
  Every preset is one real machine, so its values agree with each other.
  A Mac reporting `x86`, or Windows 10 reporting `"15.0.0"`, would give the
  client away. The maintainer asked for a collection to pick from, set or
  randomized when a client is created: `profiles.Machines` and
  `profiles.RandomMachine`. Each platform has one captured machine today,
  so both offer only that machine for now.

### Network hints

- **Derive both from mimicry's own measurements.** mimicry has no
  throughput estimate, and the captures show Chrome's `downlink` is not a
  measurement of the path.
- **Captured constants only.** `rtt` would then ignore the real network.
- **Hybrid.** Chosen. `rtt` is the last TCP connect's duration, from its
  `connect()` to the connection being up: no DNS lookup, and through a
  proxy only the connect to the proxy. `downlink` is the machine's
  captured centre (1.6 Mbps). Both get a noise factor of 0.90 to 1.10 and
  Chrome's rounding and caps: `rtt` to 50 ms, at most 3 s; `downlink` to
  0.05 Mbps, at most 10. The values are fixed per host, separately for a
  navigation and a subresource, on first use, and kept for the
  Transport's life, as the captures show them fixed per host. `ect` is the
  machine's. The maintainer wanted nothing that recomputes the value
  continually: the connect time is already known. QUIC connections do not
  update it.

### Delegation

- **Parse `Permissions-Policy` and `Delegate-CH`.** mimicry reads no HTML,
  so it cannot see a meta tag. It also knows no frame tree, which an
  allowlist needs.
- **No delegation.** A delegated third party would then get the wrong
  hints.
- **The caller says, on each request.** Chosen, as `Request.ClientHints`
  (`HintsDelegated`, `HintsWithheld`). The default gives another origin
  the low-entropy three.

### Orders the first captures did not show

The first draft of this decision placed hints on a redirected or form-POST
navigation, and on a fetch with the page's own headers, by the rules the
other requests showed, rather than refusing them (`ErrNotMeasured`) or
sending them without hints. The maintainer had the cases captured instead;
the captures confirmed both rules, so nothing is placed by a rule no
capture of its kind checks.

### Profile shape

The profile records:

- the navigation run's order (`hints`) and the order of entry into the
  header table (`tableOrder`);
- the header a restart's run follows (`restartAfter`);
- the full version;
- the machine.

kindsgen derives all of this from the captures. It refuses captures whose
samples disagree, and any captured request that the model would send
differently. That includes every subresource's table run under
`tableOrder`.

## Decision

- **`profile.ClientHints`**: Chrome's only, derived by kindsgen. It is nil
  for Firefox and Safari. It is also nil for Chrome profiles without the
  capture (`chrome-155-macos`, and iOS, which sends no hints), which keep
  ignoring the opt-in.
- **`Config.ClientHintStore`**: nil keeps one per Transport.
  `NewClientHintStore` returns a `MemoryClientHintStore`, which saves and
  loads as JSON (`WriteJSON`, `ReadJSON`), like the cookie jar and
  `AltSvcCache`.
- **`Config.ClientHints`**: a `*profile.Machine`; nil uses the profile's
  machine.
- **`Request.ClientHints`**: `HintsDefault`, `HintsDelegated` or
  `HintsWithheld`, for requests to another origin than their page's; the
  page's own origin always gets its hints. A navigation takes none.
- **Restart.** `Transport.RoundTrip` sends a `Critical-CH` restart itself,
  for GET navigations without a body only. It reads the first response's
  body (up to 64 KiB) and closes it, then sends the request again. Only
  the second response is returned. The cookie jar keeps the first
  response's cookies.
- **Responses.**
  - A navigation's response, a redirect's included, stores its
    `Accept-CH` and may restart it.
  - Any response's `Clear-Site-Data` empties its origin's set.
  - A subresource's `Accept-CH` changes nothing.
- **A page's hints.** Requests from the last navigation's page
  (`Request.From` equal to it) send the hints it loaded with
  (`Transport.lastPage`); requests from any other page, the store's.
- **Insecure URLs.** Nothing goes to a non-secure URL, as before.
- **Where the run goes.** On every list, a navigation's run goes where the
  low-entropy three are. A subresource's run is laid out by
  `headerorder.Build`'s table (`Plan.OwnRun`): the list holds the hints in
  `tableOrder`, then `user-agent`; the page's own headers enter first.

## Consequences

- Sites that opt in get Chrome's hints, values and restart.
- Saving the store with the cookie jar resumes an identity's opt-ins.
- A caller fetching from a third party that a page delegated to must say
  so with `HintsDelegated`. Otherwise that third party sees only the
  low-entropy hints, as without delegation.
- Some of what follows was not captured. Each is a line in
  [EVIDENCE.md, Not covered](../../profiles/EVIDENCE.md#not-covered-by-any-claim):
  - `Width`;
  - `Clear-Site-Data` on a redirect's response;
  - Chrome's network quality estimator.
- The macOS and Windows profiles carry the full version their captures
  show: `chrome-156-windows` says 156.0.8078.81. Chrome updates patch
  versions silently, so a refresh moves it.

## Revisit when

- A Chrome version changes its hint set, order or restart; kindsgen fails
  on its captures.
- Another machine is captured. It becomes another entry in
  `profiles.Machines` with no code change.
- A caller needs `Width`. Add a request field for the image's layout width.
- A capture shows `Clear-Site-Data` on a redirect, or a multi-display
  machine worth offering as a preset (navigations and subresources then
  disagree on DPR).
