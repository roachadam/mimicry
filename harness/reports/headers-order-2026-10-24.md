# Headers a page sets: where browsers put them (2026-10-24)

Capture: `headers-order` (`server.HeadersSteps`), 3 samples per browser, all
complete (60 GETs + preflights each, no step failed or upgraded). All three
samples of every browser are identical, names, order and values.

## The rules

| Browser | Rule | Explains |
|---|---|---|
| Chrome 155 | Blink's `HTTPHeaderMap` iteration order. The page's headers enter the table first (fetch(): sorted, the `FetchHeaderList` is a sorted multimap; XHR: `setRequestHeader` order), then `user-agent`, `sec-ch-ua`, `sec-ch-ua-mobile`, `sec-ch-ua-platform`; `content-type` enters with the page's. Hash: rapidhash of the case-folded name as UTF-16, low 24 bits; 8 buckets, doubled when half full, triangular probing. The run goes where the list has its first table name (first after `Host`/`Connection` over HTTP/1.1); everything else (`accept`, `origin`, `sec-fetch-*`, ...) keeps the list's order. | every sample, both protocols, all 3 targets, incl. `kinds`, `kinds-http1`, `kinds-plain`, `cors` fetches |
| Safari 27, 26 | CFNetwork's header `CFDictionary` iteration order: CFString hash of the lowercased name, prime bucket counts (3, 7, 13, 23, 41, 71, ...), linear probing, grown before an insert past capacity. The browser's own headers (`accept`, `origin`, `referer`, `sec-fetch-*`, `user-agent`, `content-type`) sit in the table as the list without page headers orders them; the page's enter after; `content-length` after those. `accept-language`, `priority`, `accept-encoding` (and `Connection` over HTTP/1.1) follow outside the table. | every non-redirected sample (also plain-HTTP `fetch-custom`, where the table is 3–7 buckets); not redirects (below) |
| Firefox 158 | One contiguous run at a fixed place per list: fetch() after `referer` (cs: before `origin`); XHR before `referer`/`origin`. Inside: fetch() with a `Headers` object sorted by name; XHR in `setRequestHeader` order (and the cors page's object-literal `headers: {X-B, X-A}` keeps X-B, X-A). | every sample |

Same-origin vs cross-site vs HTTP/1.1 target change only which own headers
are in the list (Origin), so the table's size and the run; the rule is the
same.

Case, duplicates, forbidden names (every browser, every target):
- HTTP/2: all lowercase.
- HTTP/1.1: fetch() sends the page's names lowercase; XHR keeps the page's
  spelling (`X-MiXeD`, `x-lower`, first spelling `X-Dup`).
- A name set twice (`X-Dup: 1`, `x-dup: 2`) is one line `1, 2`, at the first
  one's place and spelling.
- `sec-a` is dropped by all four; preflights list
  `Access-Control-Request-Headers` lowercase, sorted, comma without space,
  without it (mimicry already does).

Redirects: Safari's redirected requests reorder every header, page's and
`accept-language`/`priority` included (cors-edge PUT/DELETE hops, cors
`redirect-target.cs`); Chrome's cross-site redirected fetch with X-Mimicry is
not its base order plus the table either (Origin sits differently from the
captured base hop). So neither table is marked `redirected`; those requests
keep their captured `page_set` variants, others are `ErrNotMeasured`.

## Implemented

- `profile.HeaderTable` in `context.header_table` (`hash`, `own`,
  `page_first`, `as_page`, `late`, `redirected`), checked by
  `checkHeaderTable` (from `checkKinds`): known hash, names once, and in every
  fetch/XHR list without `*` the table's names are one run.
- `internal/headerorder/table.go`: `TableOrder`, the Blink and CF tables and
  hashes. `Build` takes the plan's table: with page headers and no `*`, the
  run in table order at the list's first table name. `Plan.Table`,
  `Plan.LowerOwn` (fetch(): page names lowercase over HTTP/1.1; `h1/conn.go`).
- `kinds.go`: fetch/XHR page-set requests with a table take the list of their
  condition without page headers; page headers set twice combine
  (`combinePageHeaders`); redirected ones without a captured variant are
  `ErrNotMeasured` unless `redirected`.
- kindsgen: reads `headers-order` and `cors-edge`; `deriveTable` finds Own
  (browser names between two page names), extra roles (as-page, own, late,
  first that explains), the shape (blink/cf, page first or not) and
  `redirected`; page-set groups the table places get no variant and never
  fold `*` into a table profile's lists. XHR captures are checked in their
  `setRequestHeader` order. Consecutive page names collapse into one `*`.

Result: Chrome's and Safari's `*`/`page_set` lists are gone except the
redirected ones; Firefox's XHR lists gained the `*` slot.

Tests: `TestHashes`, `TestTableOrderMatchesCaptures`, `TestTableOrderRefuses`,
`TestBuildTable`, `TestRequestHeadPageHeaderCase`, `TestHeaderTablePlans`,
`TestPageHeadersCombine`, `TestDeriveTableFindsTheBrowsers`,
`TestDeriveTableNoneForOneRun`, `TestTablePlaces`, and e2e
`TestHeadersOrderMatchesBrowser` (every step and preflight, every profile).

## Not reproduced / unmeasured

- The page's set order: an `http.Header` has none, so mimicry takes page
  headers by name (exact for fetch() with a `Headers` object). XHRs whose page
  set them otherwise differ where the browser follows the page's order
  (`headersOrderGaps`: Chrome 6, Safari 7 each, Firefox 12 steps), and
  Firefox's fetch() with an object-literal init keeps insertion order too.
  Alternative: a `Request` field for the page's order (API change, not made).
- Chrome's `content-type` role: as-page and own explain every capture alike
  (no capture has it with another page header); kindsgen picks as-page, which
  Blink's source agrees with.
- Safari page headers that are WebKit common header names (Authorization,
  Cache-Control, ...) may enter the table among the browser's own; only
  `content-type` was captured.
- CF tables past 1732 names and CFString's >96-character hash path are from
  source only.
- mimicry still sends a forbidden name (`Sec-*`) a caller sets.
