# 0057: URL parsing as the browsers measure it

- Status: Accepted
- Date: 2026-11-04
- Commits: the one that closes [#71](https://github.com/roachadam/mimicry/issues/71)

**Summary.** A new `urls` capture has Chrome, Firefox and Safari parse 43
awkward URLs, by fetch() and by navigation, and mimicry now sends what
they agree on: `'` in a query as `%27`, a path's `\` as `/` and its dot
segments resolved, IPv4 hosts in any form as dotted decimal. Where they
differ, a kindsgen-derived profile section `urls` says what each does (`|`
in a path; an invalid `xn--` label). Trailing-dot SNI differs too and is
left to [#88](https://github.com/roachadam/mimicry/issues/88).

## Problem

#69 made URLs take the WHATWG URL standard's form before anything is
sent, but only an uppercase host and a default port had been seen in a
browser. Three things were open: `'` in a query (the standard encodes it,
browsers had differed), paths (Go's encoding, not the standard's), and
hosts #69 did not touch (a malformed punycode label, short IPv4 forms).

## Options

- **Follow the standard everywhere.** No capture needed, but the captures
  show browsers leaving it: Chrome encodes `|` in a path, Firefox, Safari
  and Chrome send a lone `%`, Chrome and Safari 27 send `xn--a`.
- **One behaviour per browser name in code.** No runtime code branches on
  `Spec.Browser`; the profiles carry behaviour fields (as
  `context.preflightCache`), and a browser's next version may change one:
  Safari 26 refuses `xn--a`, Safari 27 sends it.
- **Measure, model what all agree on in code, and the differences as
  profile fields derived from the capture** (chosen).

## Decision

- The `urls` scenario: an `https://` page fetches, then loads in an iframe,
  each URL (query and path characters, dot segments, uppercase,
  internationalized, trailing-dot, default-port and leading-zero-port
  hosts, a malformed punycode label), then goes to a plain `http://` page
  for the IPv4 forms (an `https://` page's `http://` loopback requests
  are mixed content to Safari). Each request's path starts with its case
  (`/c/<f|n>-<case>/`). The capture tool forwards port 443 to the TLS
  port, the server answers a ClientHello without SNI (Safari, trailing
  dot), and records one it cannot serve (`capture.Connection.Refused`: a
  server name ending in a dot, which Go's TLS refuses).
- `url.go` applies what every browser does, for http(s) and ws(s): the
  query set plus `'`; the path set (the query set, `?`, `^`, `` ` ``,
  `{`, `}`), `\` as `/`, dot segments resolved; the standard's IPv4
  parser.
- `profile.URLs`, derived by kindsgen: `pathEncodes` (characters encoded
  in a path beyond the shared set: `|` for Chrome) and `checkPunycode` (an
  ASCII host's `xn--` labels through UTS #46 ToASCII: Firefox, Safari 26).
  Nil: a `|` in a path or an invalid `xn--` label is `ErrNotMeasured`;
  every other URL is one the browsers agree on.
- The request target, Referer, a WebSocket URL and cookie paths take the
  path as mimicry made it (`internal/weburl`): Go's `EscapedPath` would
  encode a `|` Firefox and Safari send as written. `internal/weburl` is
  standard-library only; `.golangci.yml` admits it to `internal/h1`,
  `internal/h2`, `internal/h3` (which write the target) and `profile`
  (whose `urls.pathEncodes` extends its path set).
- `chrome-156-windows` and `firefox-159-windows` have their own captures
  (identical to macOS on all 43 URLs), as does Safari 26, which differs
  from Safari 27 on `xn--a`; the iPhone profiles inherit their Safari
  version's. `chrome-155-macos` and `firefox-158-*` have none: inheriting
  is only from an older version, and those browsers can no longer be
  installed.
- `req.URL.Opaque` is the way to send a request target as written,
  path and query untouched (the host still canonical), in Go's two forms
  (`"/raw"`, `"//host/raw"`), `Exact` or not. Normalising is the default,
  as the browser's; a security test needs targets no browser sends
  (`/../`, `\`, invalid escapes). It is the idiom `net/http`, tls-client
  and azuretls (both on fhttp) already have; curl's equivalents are
  `--path-as-is` and `--request-target`. A `Request.RawTarget` field was
  rejected as a second spelling of it, and `Exact` covering the URL as
  making browser headers with a raw path impossible.

## Evidence

The `urls` captures of 2026-11-04, 3 samples each: Chrome 156 and
Firefox 159 on macOS and Windows, Safari 27 (macOS 27) and Safari 26
(macOS 26). Every browser: `'` in a query `%27`; in a path `?`, `^`,
`` ` ``, `{`, `}` encoded, `'` as written, `\` a separator, `.`, `..`
and `%2e` forms resolved, a lone `%` as written; `127.1`, `0x7f.1`,
`0177.0.0.1`, `2130706433` and `127.0.0.1.` sent as `127.0.0.1`. Chrome
alone encodes `|` in a path. `xn--a.urls.test`: sent by Chrome and
Safari 27, refused by Firefox and Safari 26. A trailing-dot host keeps its
dot in `Host` everywhere; its SNI keeps it in Chrome and Firefox
(recorded as refused by Go's TLS) and is absent in Safari. Ledger:
[URLs](../../profiles/EVIDENCE.md#urls).

## Consequences

- Callers' URLs go out as a browser sends them in every case measured but
  two, pinned in `urlsKnown` (`TestURLsMatchBrowser`): a lone `%` in a
  path, which Go's `url.Parse` refuses inside `http.NewRequest` (send it
  with `URL.Opaque`), and trailing-dot SNI (#88).
- `TestOpaqueTargetGoesOutAsWritten` keeps `URL.Opaque` raw over every
  protocol.
- `TestURLsMatchBrowser` fails when a case starts matching, so a pin
  cannot outlive its cause.
- A profile without `urls` errors on two inputs it could not know; the
  older profiles keep that until retired.
- IPv6 literal forms (`[0:0::1]`) are not captured and stay as written.

## Revisit when

A browser's next version changes a case (a refresh's `urls` capture shows
it, and kindsgen refuses a character it cannot model), or #88 lands.
