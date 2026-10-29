# 0042: The HTTP/2 opening is cut into TLS records as the browser cuts it

- Status: Accepted
- Date: 2026-10-28
- Commits: (where it landed)

## Problem

`internal/h2` wrote the client preface, SETTINGS, the connection
WINDOW_UPDATE, PRIORITY frames and the first HEADERS each with its own
write, so each went out in a TLS record of its own: four or five small
records where browsers send one or two. Record sizes at connection start
are visible to anyone on the path, without decryption, and cost a record
(seal, 22 bytes, usually a syscall) each. Firefox's HEADERS and stream
WINDOW_UPDATE were two records on every request.

The capture server could not see records at all: crypto/tls hides them.

## Options

- Keep one record per frame: no evidence for it, and unlike every
  browser measured.
- One fixed rule for every browser (opening in one record, HEADERS
  apart): wrong for Safari 26, which puts the first HEADERS in the
  opening's record.
- **A profile field, `http2.openingRecords`, generated from captures**:
  per record, what it carries. Chosen.
- A field per scenario (connections the browser opened ahead of the
  request, or not): mimicry never opens a connection ahead of a request,
  so only the "request waiting" cut is ever sent; the other is a backlog
  gap (preconnect).

## Decision

- The capture server reads the client's record stream under TLS
  (`server.recordLog`, TLS 1.3: content length = record length − 17) and
  records, per HTTP/2 trace, its first 16 application data records with
  the frames starting in each (`h2trace.Trace.Records`).
- `h2trace.Opening` turns a trace into the cut: per record, `preface` and
  frame types, through the record carrying the first HEADERS and its
  stream WINDOW_UPDATE. A connection whose SETTINGS ACK went before the
  first HEADERS was opened ahead of its request (a round trip passed) and
  is left out.
- `hellogen` writes `http2.openingRecords` from every such connection of
  the profile's captures (`capture.OpeningRecords`) and refuses
  disagreement. `profile.Validate` requires it to list the opening in
  order (`HTTP2.Opening`) with the first request's frames in the last
  record.
- `internal/h2` gathers the opening in a per-connection buffer
  (`connWriter`, 512 bytes inside `Conn`) and writes each record with one
  write. When the first request joins the last record (Safari), the
  record waits for `writeHeaders`; a SETTINGS ACK the read loop writes
  meanwhile follows it, so the loopback race cannot reorder them; any
  other frame (GOAWAY) sends the opening first. A request's HEADERS,
  CONTINUATION and stream WINDOW_UPDATE always go out in one write.
- Without the field, each frame keeps its own record, as before; every
  supported profile has it.
- A version no longer installable as the brand may take this field, and
  only this field, from its testing build (Chrome for Testing, through
  `cmd/capture -chrome-app`): its capture is named
  `<name>.<scenario>.chrome-for-testing.pagetrace.json.gz`, which
  `capture.Files` and `ScenarioOf` skip, so only `capture.OpeningRecords`
  reads it. The record cut is the network stack's, which the testing build
  shares with the brand; its headers (brands, user agent) are not the
  brand's, so nothing else may come from it.

## Evidence

`resume` captures, 2 samples each, 2026-10-28 (Mac mini; [EVIDENCE.md,
HTTP/2 connection](../../profiles/EVIDENCE.md#http2-connection)):
- Chrome 156: 14 of 14 connections `[preface SETTINGS WINDOW_UPDATE]
  [HEADERS]`, fresh and resumed.
- Chrome 155: Chrome for Testing 155.0.8059.84 (the mini, `resume`, 2
  samples): 14 of 14 the same as Chrome 156. Regenerating chrome-155
  changed `http2.openingRecords` alone.
- Firefox 158: 7 connections `[preface SETTINGS WINDOW_UPDATE] [HEADERS
  WINDOW_UPDATE]`; 5 opened ahead of the request, the same with the ACK in
  between; 2 cut off by the trace's end.
- Safari 26.2: 12 connections `[preface SETTINGS WINDOW_UPDATE HEADERS]`;
  the tab's first connection (2) opened ahead: the opening alone, the
  ACK, then HEADERS. Staged `kinds` and `nav-redirect-page` captures
  (not committed) show navigations and subresources on new connections
  joined, only the tab's first apart.
- Safari 27.0.1 (this Mac, `resume`, 2 samples): the same as 26, 12
  connections joined, the tab's first 2 apart.

`TestConnectionMatchesBrowserTrace` compares mimicry's cut; with the cut
disabled it fails for Chrome 156, Firefox 158 and Safari 26, and with the
request frames written apart it fails for Firefox.

`BenchmarkOpen` (`internal/h2`, New and one GET against a scripted peer,
M3 Pro): writes per new connection Chrome 5 → 3, Firefox 6 → 3, Safari 26
5 → 2 (the SETTINGS ACK included); allocations 48/48/47 → 47/47/46;
+~600 B per connection (the buffer).

## Consequences

Two to four fewer records per new connection, each a seal, a syscall and
22 bytes saved. Each HTTP/2 connection holds 512 more bytes. A Safari
connection with no request yet holds its preface back until the request
or a GOAWAY.

## Revisit when

A capture shows a browser cutting the opening by timing rather than by
structure.
