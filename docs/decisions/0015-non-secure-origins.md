# 0015: Requests to non-secure origins as captured, per kind

- Status: Accepted
- Date: 2026-10-24
- Commits: `ae57d13`

## Problem

mimicry sent `http://` requests with the headers browsers send only to
secure origins: client hints, `Sec-Fetch-*`, Brotli and Zstandard in
`Accept-Encoding`. Any server, or a proxy that forwards plain requests, sees
the mismatch at once.

## Options

- Strip a fixed set of headers and rewrite `Accept-Encoding` for `http://`
  requests: wrong for Chrome, which also reorders (`Origin` moves after
  `User-Agent`), and for Safari, which reorders most requests and adds
  `Upgrade-Insecure-Requests` to frame navigations.
- Capture the kinds page over plain HTTP and keep each kind's non-secure
  lists as measured. Chosen.

## Decision

- `Variant.Insecure`: a header list for requests to URLs that are not
  potentially trustworthy, HTTP/1.1 only. `kindsgen` derives them from the
  `kinds-plain` capture for every kind and the navigation (typed GET and
  form POST), never folding them into the secure order, with values from the
  plain requests themselves.
- At request time a non-secure request takes its exact non-secure variant,
  else the kind's non-secure default; with neither it is an error (decision
  0008), never the secure lists.
- Potentially trustworthy is the Secure Contexts specification's: `https`,
  `wss`, and loopback hosts (`localhost`, `*.localhost`, `127.0.0.0/8`,
  `::1`). [INFERENCE] The loopback exception is from the specification; the
  captures use `.test` hosts only.

## Evidence

[`internal/harness/reports/plain-http-2026-10-24.md`](../../internal/harness/reports/plain-http-2026-10-24.md),
captures `<profile>.kinds-plain.pagetrace.json.gz` for all five profiles.
`TestKindsMatchBrowserTraces` (`kinds-plain`: every request and the page's
navigation, line for line), `TestProxyMatchesBrowser` (forwarded plain
requests line for line).

## Consequences

- Plain-HTTP requests, direct or through a proxy, are the browser's.
- A custom profile without non-secure variants cannot send `http://`
  requests (to anything but loopback) until it has them.
- Safari's HTTPS upgrade attempt before a plain request, and Chrome's
  HTTPS-Upgrades on port 80, are not made: requests go to the URL given
  ([0016](0016-literal-urls-no-https-upgrade.md)).

## Revisit when

A capture to a loopback `http://` origin shows a browser treating it as
non-secure, or the HTTPS upgrade attempts are needed.
