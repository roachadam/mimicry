# 0016: Requests go to the URL given; no HTTPS upgrades or HSTS by default

- Status: Accepted
- Date: 2026-10-24
- Commits: `63fc039`

## Problem

Browsers often never send a plain-HTTP request for an `http://` URL:

- Safari first opens TLS to the same host and port (seen in the
  `kinds-plain` captures, [report](../../harness/reports/plain-http-2026-10-24.md)),
  falling back to plain HTTP when it fails.
- Chrome's HTTPS-Upgrades try `https://` first for navigations to `http://`
  on port 80 (not on other ports, as captured). [INFERENCE] Safari and
  recent Firefox likely do the same on port 80; not measured.
- HSTS: once a site sends `Strict-Transport-Security`, a browser sends it
  no plain-HTTP request again.

mimicry sends what it is asked for. A plain request to a site that also
serves HTTPS is one a modern browser would rarely make.

## Options

- Upgrade `http://` navigations by default, falling back as the browser
  does, with an opt-out. Matches the browser, but silently changes the URL
  fetched (different content, or failure where `http://` would work), which
  no HTTP client does; and the behaviour is not measured on ports 80 and 443.
- Keep requests literal; document it; offer browser policy (upgrades, HSTS)
  later as an opt-in layer. Chosen.
- Refuse `http://` URLs a browser would upgrade: cannot be known without
  trying HTTPS first.

## Decision

A request goes to its URL's scheme, host and port, nothing else. Writing
`http://` is the caller's visible choice (the deviation rule of decision
0005), and what mimicry owes is that the request it then sends is the
browser's own for that URL, which non-secure variants provide
([0015](0015-non-secure-origins.md)).

HTTPS upgrades and HSTS belong together, as browser policy that decides a
request before it exists. When added, they are an option in `Config`,
measured first per browser: which requests upgrade, on which ports, the
fallback and its timing.

None of the other clients compared (tls-client, httpcloak,
curl-impersonate, checked in their source on 2026-10-24) upgrades either,
and all three send `http://` requests with their HTTPS headers.

## Evidence

The `kinds-plain` captures (Safari's TLS attempts on the plain port, no
upgrade from Chrome or Firefox on a non-default port); the other clients'
source.

## Consequences

- Callers should use `https://` URLs for sites that serve HTTPS, as they
  would with any client.
- A plain request to such a site differs from a browser's visit: the browser
  would most likely have gone straight to HTTPS.

## Revisit when

A caller needs browser-like navigation of `http://` URLs (crawling links as a
browser follows them), HSTS behaviour matters to a target, or the port-80
upgrade behaviour is measured.
