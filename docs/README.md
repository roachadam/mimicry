# mimicry documentation

Working documentation, written as the code is built and polished before
release. Three kinds:

- [`decisions/`](decisions/): why mimicry is built the way it is. One record
  per decision: the problem, the options, the choice, the evidence, and what
  would make us revisit it. Read these before "fixing" something that looks
  odd.
- [`concepts/`](concepts/): how it works, for users and contributors.
- `guides/` (later): how to do things with it.

Related:

- [`backlog.md`](backlog.md): every known gap and deferred item, with its
  detection relevance and what closing it takes
  ([decision 0029](decisions/0029-scope-what-gets-modelled.md) says what
  gets modelled).
- [`profiles/EVIDENCE.md`](../profiles/EVIDENCE.md): every claim a built-in
  profile makes, its evidence and the test that keeps it true.
- [`harness/reports/`](../harness/reports/): dated measurement reports.
- [`AGENTS.md`](../AGENTS.md): working conventions.
- [`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md): code bundled from
  other projects and the Go dependencies, with their licences (mimicry
  itself is MIT, [`LICENSE`](../LICENSE)).

## Decisions

| # | Decision | Status |
|---|---|---|
| [0001](decisions/0001-own-http-stack-thin-utls-fork.md) | Own HTTP/1.1 and HTTP/2 stack; uTLS as a thin fork | Accepted |
| [0002](decisions/0002-net-http-types.md) | `net/http` types in the API, no forked `net/http` | Accepted |
| [0003](decisions/0003-evidence-rule.md) | Browser wire captures are the evidence | Accepted |
| [0004](decisions/0004-complete-profile-files.md) | One complete profile file per browser version, no inheritance | Accepted; captures inherited by 0035 |
| [0005](decisions/0005-request-kinds-api.md) | Request kinds: defaults exact, deviations visible | Accepted |
| [0006](decisions/0006-header-order-variants.md) | Header order: a default per kind plus measured variants | Accepted |
| [0007](decisions/0007-navigation-stays-top-level.md) | The navigation stays the profile's top-level request | Accepted |
| [0008](decisions/0008-unmeasured-is-an-error.md) | Asking for what was never measured is an error | Accepted |
| [0009](decisions/0009-own-cookie-jar.md) | Our own cookie jar, driven by the transport | Accepted |
| [0010](decisions/0010-dial-context.md) | `Config.DialContext` for the TCP connection | Accepted |
| [0011](decisions/0011-connection-partitioning.md) | Model connection partitioning only where a rule is measured | Accepted |
| [0012](decisions/0012-cookie-rules-in-profiles.md) | Browser cookie rules as a profile section | Accepted |
| [0013](decisions/0013-tls-resumption.md) | TLS session resumption as the browser does it | Accepted |
| [0014](decisions/0014-proxies.md) | Proxies as the browser uses them | Accepted |
| [0015](decisions/0015-non-secure-origins.md) | Requests to non-secure origins as captured, per kind | Accepted |
| [0016](decisions/0016-literal-urls-no-https-upgrade.md) | Requests go to the URL given; no HTTPS upgrades or HSTS by default | Accepted |
| [0017](decisions/0017-cors-preflights.md) | CORS preflights as the browser sends them, refusal included | Accepted |
| [0018](decisions/0018-no-public-engine.md) | No public Engine interface | Accepted |
| [0019](decisions/0019-transport-is-one-browsing-entity.md) | A Transport is one browsing entity | Accepted |
| [0020](decisions/0020-connection-pool-package.md) | The connection pool is its own package | Accepted |
| [0021](decisions/0021-page-headers-hash-table-order.md) | Page-set headers in the browser's hash-table order | Accepted |
| [0022](decisions/0022-redirects-as-the-browser-follows-them.md) | Redirects follow the browser's rules, inside the Transport | Accepted |
| [0023](decisions/0023-connection-limits-and-coalescing.md) | Per-host HTTP/1.1 limit and HTTP/2 coalescing, per browser | Accepted |
| [0024](decisions/0024-clienthello-shape-in-profiles.md) | The ClientHello's whole shape is profile data | Accepted |
| [0025](decisions/0025-websockets.md) | WebSockets as each browser opens and frames them | Accepted |
| [0026](decisions/0026-http3-capture.md) | HTTP/3 is captured from the client's own packets | Accepted |
| [0027](decisions/0027-quic-base.md) | QUIC from a copy of x/net/quic; HTTP/3 and QPACK our own | Accepted |
| [0028](decisions/0028-quic-failure-model.md) | QUIC and HTTP/3 fail as each browser's do | Accepted |
| [0029](decisions/0029-scope-what-gets-modelled.md) | Model what anti-bot systems check or users notice | Accepted |
| [0030](decisions/0030-header-placement.md) | Header placement: move one header, keep the browser's order | Accepted |
| [0031](decisions/0031-cookies-for-any-site.md) | Seeding cookies from strings, and cookies for any site | Accepted |
| [0032](decisions/0032-certificate-pinning.md) | Certificate pinning and VerifyConnection on every handshake | Accepted |
| [0033](decisions/0033-proxy-errors.md) | Proxy failures are a `*ProxyError` | Accepted |
| [0034](decisions/0034-request-plans.md) | Request plans: RoundTrip's path, without sending | Accepted |
| [0035](decisions/0035-version-policy-and-capture-inheritance.md) | Newest-version accessors, five supported versions, inherited captures | Accepted |

New records copy [`decisions/template.md`](decisions/template.md).

## Concepts

- [What mimicry does on the wire](concepts/what-mimicry-does.md)
- [How a request is built](concepts/how-a-request-is-built.md)
- [Cookies](concepts/cookies.md)
- [Proxies](concepts/proxies.md)
- [CORS preflights](concepts/cors-preflights.md)
- [WebSockets](concepts/websockets.md)
- [HTTP/3](concepts/http3.md)
- [Coming from tls-client](concepts/coming-from-tls-client.md)
- [Refreshing profiles](concepts/refreshing-profiles.md)
