# 0021: Page-set headers in the browser's hash-table order

- Status: Accepted
- Date: 2026-10-24
- Commits: (the wave-3 commit)

## Problem

A header the page sets itself (`fetch()` headers, `setRequestHeader`) had
one place in each kind's list: the `*` slot, measured from `X-Mimicry`.
The `cors` capture showed Chrome and Safari placing `x-a` and `x-b`
elsewhere than `x-mimicry`, so one slot was wrong for most names
(0006, "revisit when").

## Evidence

The `headers-order` capture ([report](../../harness/reports/headers-order-2026-10-24.md)):
ten header sets (single names, pairs in both orders, a to z and reversed,
names sorting around every browser header, case and duplicates), by
`fetch()` and XHR, same-origin, cross-site and HTTP/1.1. Three samples per
browser, all identical:
- **Chrome:** the page's headers, then `user-agent` and the three
  `sec-ch-ua*`, come out as one run in the iteration order of Blink's
  header hash map (rapidhash of the lowercased name, 8 buckets doubling
  at half full, triangular probing).
- **Safari:** Safari's own headers sit in CFNetwork's header dictionary,
  the page's go in after them, then `content-length`. The run comes out in
  the dictionary's order: CFString hash, prime bucket counts, linear
  probing.
- **Firefox:** one run in a fixed place. fetch() with a `Headers` object
  is sorted by name; XHR keeps the page's order.

Both hash tables were reproduced from the browsers' source and then
checked against every captured request. The source gave the mechanism;
the wire is the evidence.

## Options

- **Keep one slot.** Wrong for most names in two browsers.
- **A slot per name**, learned from captures. It cannot cover names never
  captured, and a table's order depends on which other names are in it.
- **Model the tables.** The profile says which hash, which names enter the
  table and in what order, and which stay outside.

## Decision

Profile `context.headerTable` (`hash`, `own`, `pageFirst`, `asPage`,
`late`, `redirected`), derived by kindsgen. Firefox has none: its lists
keep the `*` slot. `internal/headerorder` orders the run.

Over HTTP/1.1, fetch() names go out lowercase and XHR keeps the page's
spelling. A name set twice becomes one line, at the first one's place and
spelling.

A redirected request with page-set headers and no captured variant is
not measured (0008): Safari reorders every header on a redirect, and
Chrome's captured redirected fetch does not follow the table.

## Consequences

- Any page-set name lands where the browser puts it, captured or not.
- Two gaps remain. A page's XHR order cannot be told to mimicry: there is
  no `Request` field for it, so page headers go in name order, which is
  exact for fetch() with a `Headers` object. Safari page headers with
  WebKit's standard names (Authorization, Cache-Control) were not captured.

## Revisit when

- A browser version changes its header container or hash.
- A caller needs XHR order: add a `Request` field for the page's order.
