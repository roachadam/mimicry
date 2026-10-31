# 0049: STREAMS_BLOCKED as each browser sends it

- Status: Accepted
- Date: 2026-10-31
- Commits: the one that closes [#4](https://github.com/roachadam/mimicry/issues/4)

**Summary.** `quic.streamsBlocked` holds when a profile's browser sends
STREAMS_BLOCKED while its requests wait for a server's stream limit, as
h3gen derives it from the `h3-streams-blocked` captures: per request made
at the limit or once per limit, and what a waiting request's stream
carries once the limit is raised. Chrome's varying part (one frame or two
per raise) is modelled as a rule whose two forms come from when the
waiting request opens its stream, not by picking a count. Profiles without
the capture send none, as before.

## Problem

All three browsers send STREAMS_BLOCKED when a request waits for the
server's limit on bidirectional streams, each by its own rule
([report](../../internal/harness/reports/h3-streams-blocked-2026-10-31.md)).
mimicry sent none. Two things needed deciding: how to model Chrome's
frames after a raise, which differ between samples, and how mimicry's
QUIC layer can count requests made at the limit when HTTP/3 opens its
requests' streams one at a time (`h3.Conn.start` holds `startMu` across
`NewStream`), so at most one request ever waits inside `NewStream`.

## Options

- **Chrome after a raise: one frame per raise** (the 3 of 12 form), **or
  one per stream** (the 9 of 12). Either picks one sample's value, which
  the evidence rule forbids ([0003](0003-evidence-rule.md)).
- **A drawn count** (one or two per raise, at the samples' rate). A
  probability from 12 raises, and a rule no capture shows.
- **The frame with the stream that takes up the limit, as the stream
  opens.** A raise reaches the client as two MAX_STREAMS frames (one per
  stream the server ended: [INFERENCE] from the server packet's size,
  and quic-go's rule); a waiting request that opens its stream after the
  first frame was read and before the second takes up the first frame's
  limit and sends one too. Both of Chrome's forms come out, as how the
  request and the connection's loop are scheduled falls.
- **Counting requests: from `NewStream`.** Sees one request at a time, so
  Chrome's frame per request and Firefox's "another request still waits"
  cannot be told.
- **Counting requests: `NewStream` outside `startMu`.** Streams could
  then be numbered in another order than their HEADERS leave.
- **Counting requests: `Conn.ExpectStream`**, called by HTTP/3 as a
  request is made, before `startMu`; `NewStream` then counts it as given
  its stream.

## Decision

The profile field and the generator as in the summary; the last option
for Chrome's raise; `Conn.ExpectStream` for counting. The streams go to
requests in the order counted, so those made at the limit are the last in
line. A frame for a request made at the limit goes in a packet of its own,
as in every browser; one a waiting stream carries goes right after its
STREAM frame (Chrome) or right before it (Firefox). A lost frame is sent
again while its limit holds and a request waits, as upstream resends
STREAM_DATA_BLOCKED: no capture lost one.

The e2e comparison holds the frames sent with streams as sets where the
samples differ: each frame every sample sent last in its packet (the
limit each raise ended at) is required, and each of mimicry's must be one
some sample sent, at most as often.

## Evidence

[EVIDENCE.md, Stream limits](../../profiles/EVIDENCE.md#stream-limits):
the rule each sample shows (6 bursts per browser), h3gen's refusals,
and the mutations `TestHTTP3EdgeMatchesBrowser` catches. mimicry sent both
of Chrome's forms over runs of the test.

## Consequences

- Chrome's frames per raise vary run to run, as Chrome's did; their count
  per page is not compared, the frames are (as sets).
- Firefox's rule is right but its raises are not: mimicry acknowledges two
  responses together where Firefox acknowledged each, so quic-go raises
  its limit by two and mimicry sends one frame per two streams. Pinned in
  `h3EdgeGaps`; a line in EVIDENCE.md's Not covered.
- Profiles without the capture (Chrome 155, Chrome on Windows, Safari 26)
  have no `streamsBlocked`: they send none, as before. None may inherit
  another profile's capture ([0035](0035-version-policy-and-capture-inheritance.md)
  allows only an older version of the same browser and platform).

## Revisit when

- A capture shows Chrome's raise against a server that raises one stream
  at a time, or another server's MAX_STREAMS, so the forms can be told
  apart by what the server sent.
- A browser is seen sending STREAMS_BLOCKED for unidirectional streams,
  or resending one.
