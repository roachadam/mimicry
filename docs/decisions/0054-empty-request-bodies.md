# 0054: Requests without a body: content-length: 0 from a measured rule

- Status: Accepted
- Date: 2026-11-03
- Commits: the commit fixing #56

**Summary.** How a POST or PUT with no body goes out over HTTP/2 and HTTP/3
(`content-length: 0`, where, and how the stream ends) is a profile field,
`emptyBody`, on the navigation and on each kind, which kindsgen derives from
the `bodiless` capture. A profile without that capture has no rule and sends
such a request as before (no `content-length`, END_STREAM on HEADERS) rather
than failing with `ErrNotMeasured`.

## Problem

Issue #56: a bodiless fetch() POST or PUT went out without the
`content-length: 0` Chrome and Firefox send. Firefox also places it last
(before `te`), not where its lists place a body's `content-length`, and ends
the HTTP/2 stream with an empty DATA frame; its bodiless form POST keeps the
list's place but sends the DATA frame too; PATCH sends none in either
browser, where Safari sends `content-length: 0` on a bodiless PATCH too.
The methods, the place and the framing differ by browser and by kind, so
neither the header lists alone nor a rule in code can say them.

## Options

- **A rule in the connections** (POST and PUT send `content-length: 0` in the
  list's place): wrong for Firefox's place and framing, and a guess for every
  browser not measured.
- **A variant condition "no body"** in the kinds' lists: every kind and
  condition would need its bodiless variant measured, and a request without
  one would fall back to the kind's GET order, worse than today.
- **`emptyBody`** (chosen): per kind, the methods that send
  `content-length: 0`, whether it goes `last` (before a final `te`) rather
  than in the list's place, and whether an empty DATA frame ends the HTTP/2
  stream (`data`). The lists keep placing everything else.

For profiles without the capture: failing with `ErrNotMeasured` (decision
0008) would make every bodiless POST and PUT fail on the Windows, iOS and
older profiles until each is recaptured; sending as before keeps them working
and records the difference in EVIDENCE.md's Not covered.

## Decision

`profile.EmptyBody` on `Spec` (the navigation: a form POST) and on `Kind`;
the root resolves it into the request's plan (`ZeroContentLength`,
`ZeroContentLengthLast`, `EmptyData`) for a request with no body whose method
it lists and whose list places `content-length` (or `last`). HTTP/2 and
HTTP/3 send the field through `headerorder.Set` like a body's length; HTTP/2
writes the empty DATA frame after the HEADERS and the profile's per-stream
WINDOW_UPDATE, in one write. Over HTTP/3 the HEADERS frame ends the stream
either way, as Firefox's did. kindsgen derives the field from the bodiless
capture only, and refuses samples, methods or protocols that disagree. A
profile without it keeps the behaviour from before, deliberately not an
`ErrNotMeasured`.

## Evidence

`firefox-158-macos.bodiless.local-roots.pagetrace.json.gz`, 3 samples, all
identical: fetch() POST and PUT `content-length: 0` after `priority`, before
`te`, HEADERS without END_STREAM then the stream's WINDOW_UPDATE and DATA(0)
with END_STREAM; PATCH none, END_STREAM on HEADERS; the form POST
`content-length: 0` after `content-type` (its list's place) with DATA(0).
Over HTTP/3, the same lists (`te` dropped, so `content-length` last), one
HEADERS frame and FIN.

`chrome-156-macos.bodiless.local-roots.pagetrace.json.gz`, 3 samples, all
identical: fetch() POST and PUT and the form POST `content-length: 0` right
after the pseudo-headers (its lists' place), END_STREAM on HEADERS; PATCH
none; the same over HTTP/3, HEADERS and FIN.

`safari-27-macos.bodiless.pagetrace.json.gz`, 3 samples, all identical,
every request over HTTP/2 (Safari stayed on TCP): fetch() POST, PUT and
PATCH and the form POST `content-length: 0` before `accept-language` (its
lists' place), END_STREAM on HEADERS. Its bodiless fetch() lists start
`sec-fetch-site, accept, origin` where its lists measured with a body start
`accept, origin, sec-fetch-site`: its header table without `content-type`,
pinned in `bodilessGaps` (EVIDENCE.md, Not covered).

The `cors` captures show every profile's bodiless cross-site PUT (`put.cs`)
with `content-length: 0` too (EVIDENCE.md, Not covered), not yet read for
this field.

## Consequences

`TestBodilessMatchesBrowser` replays the capture. Each profile gains the rule
once a `bodiless` capture of its browser exists; until then its bodiless
POST and PUT differ from the browser's (Not covered).

## Revisit when

A browser shows a rule `emptyBody` cannot say (another place, a DATA frame
over HTTP/3, a method sending it in some requests only), or a vendor is seen
checking bodiless requests of a profile without the capture.
