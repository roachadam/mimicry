# mimicry documentation

Working documentation, written as the code is built. Two kinds:

- [`decisions/`](decisions/): why mimicry is built the way it is. One record
  per decision: the problem, the options, the choice, the evidence, and what
  would make us revisit it. Read these before "fixing" something that looks
  odd.
- [`concepts/`](concepts/): how it works, for users and contributors.

Related:

- [`backlog.md`](backlog.md): every known gap and deferred item, with its
  detection relevance and what closing it takes
  ([decision 0029](decisions/0029-scope-what-gets-modelled.md) says what
  gets modelled).
- [`profiles/EVIDENCE.md`](../profiles/EVIDENCE.md): every claim a built-in
  profile makes, its evidence and the test that keeps it true.
- [`internal/harness/reports/`](../internal/harness/reports/): dated measurement reports.
- [`internal/bench/`](../internal/bench/README.md): benchmarks against `net/http`
  and other impersonating clients, with dated reports in its `reports/`;
  [`performance.md`](performance.md) sums up the latest run, with charts.
- [`capturing.md`](capturing.md): capturing the browsers, deriving a
  profile's generated sections, and the harness's other tools.
- [`AGENTS.md`](../AGENTS.md): working conventions.
- [Licence](../README.md#licence): mimicry is MIT; the code copied from
  other projects and its licences.

## Decisions

| # | Decision | Status |
|---|---|---|
| [0001](decisions/0001-own-http-stack-thin-utls-fork.md) | Own HTTP/1.1 and HTTP/2 stack; uTLS as a thin fork | Accepted; amended by 0027 (`internal/quic` imports uTLS too) |
| [0002](decisions/0002-net-http-types.md) | `net/http` types in the API, no forked `net/http` | Accepted |
| [0003](decisions/0003-evidence-rule.md) | Browser wire captures are the evidence | Accepted; amended by 0035 (evidence class inherited) |
| [0004](decisions/0004-complete-profile-files.md) | One complete profile file per browser version, no inheritance | Accepted; amended by 0035 (values never inherited; a scenario's captures can be) |
| [0005](decisions/0005-request-kinds-api.md) | Request kinds: defaults exact, deviations visible | Accepted; amended by 0036 and 0039 (two default deviations) |
| [0006](decisions/0006-header-order-variants.md) | Header order: a default per kind plus measured variants | Accepted; amended by 0021 (page-set headers placed by name) |
| [0007](decisions/0007-navigation-stays-top-level.md) | The navigation stays the profile's top-level request | Accepted |
| [0008](decisions/0008-unmeasured-is-an-error.md) | Asking for what was never measured is an error | Accepted |
| [0009](decisions/0009-own-cookie-jar.md) | Our own cookie jar, driven by the transport | Accepted |
| [0010](decisions/0010-dial-context.md) | `Config.DialContext` for the TCP connection | Accepted |
| [0011](decisions/0011-connection-partitioning.md) | Model connection partitioning only where a rule is measured | Accepted; amended: top-level-site partitioning measured, deferred by 0029; Alt-Svc cache not partitioned by site |
| [0012](decisions/0012-cookie-rules-in-profiles.md) | Browser cookie rules as a profile section | Accepted |
| [0013](decisions/0013-tls-resumption.md) | TLS session resumption as the browser does it | Accepted |
| [0014](decisions/0014-proxies.md) | Proxies as the browser uses them | Accepted; amended: WebSockets through proxies, Firefox's tunnels; HTTP/3 through SOCKS5 on request by 0038; credentials up front by default by 0039; unused proxies' connections close, `MaxIdleProxies`, by 0040 |
| [0015](decisions/0015-non-secure-origins.md) | Requests to non-secure origins as captured, per kind | Accepted |
| [0016](decisions/0016-literal-urls-no-https-upgrade.md) | Requests go to the URL given; no HTTPS upgrades or HSTS by default | Accepted |
| [0017](decisions/0017-cors-preflights.md) | CORS preflights as the browser sends them, refusal included | Accepted |
| [0018](decisions/0018-no-public-engine.md) | No public Engine interface | Accepted; amended by 0044 (one validated copy shared between equal profiles) |
| [0019](decisions/0019-transport-is-one-browsing-entity.md) | A Transport is one browsing entity | Accepted; amended by 0040 (rotating proxies within one Transport) and 0044 (cheap clients) |
| [0020](decisions/0020-connection-pool-package.md) | The connection pool is its own package | Accepted; amended by 0040 (idle timeout for every protocol, `Config.IdleConnTimeout`, a separate `KeyTimeout`, `Shared.OnClose`) |
| [0021](decisions/0021-page-headers-hash-table-order.md) | Page-set headers in the browser's hash-table order | Accepted |
| [0022](decisions/0022-redirects-as-the-browser-follows-them.md) | Redirects follow the browser's rules, inside the Transport | Accepted; amended: `navigationOrigin`, user navigations' `Sec-Fetch-User` |
| [0023](decisions/0023-connection-limits-and-coalescing.md) | Per-host HTTP/1.1 limit and HTTP/2 coalescing, per browser | Accepted; amended by 0037 (options to disable coalescing and change the limit) |
| [0024](decisions/0024-clienthello-shape-in-profiles.md) | The ClientHello's whole shape is profile data | Accepted; amended: `x25519Reuse` added, then checked on every hello with both shares |
| [0025](decisions/0025-websockets.md) | WebSockets as each browser opens and frames them | Accepted; amended: `windowBits8`; WebSockets through proxies measured |
| [0026](decisions/0026-http3-capture.md) | HTTP/3 is captured from the client's own packets | Accepted |
| [0027](decisions/0027-quic-base.md) | QUIC from a copy of x/net/quic; HTTP/3 and QPACK our own | Accepted; amended: fix our copy, don't wait for upstream |
| [0028](decisions/0028-quic-failure-model.md) | QUIC and HTTP/3 fail as each browser's do | Accepted; amended by 0036 (holds shortened by default); `brokenAfter` counts only attempts that sent a datagram |
| [0029](decisions/0029-scope-what-gets-modelled.md) | Model what anti-bot systems check or users notice | Accepted; amended by 0036 (fast failure-path timing by default) and 0037 (faster defaults for costly, low-risk behaviours) |
| [0030](decisions/0030-header-placement.md) | Header placement: move one header, keep the browser's order | Accepted |
| [0031](decisions/0031-cookies-for-any-site.md) | Seeding cookies from strings, and cookies for any site | Accepted |
| [0032](decisions/0032-certificate-pinning.md) | Certificate pinning and VerifyConnection on every handshake | Accepted |
| [0033](decisions/0033-proxy-errors.md) | Proxy failures are a `*ProxyError` | Accepted; amended: `DialWebSocket` errors through proxies; credentials up front, a 407 final at once, by 0039 |
| [0034](decisions/0034-request-plans.md) | Request plans: RoundTrip's path, without sending | Accepted |
| [0035](decisions/0035-version-policy-and-capture-inheritance.md) | Newest-version accessors, five supported versions, inherited captures | Accepted |
| [0036](decisions/0036-fast-quic-fallback.md) | Fast QUIC fallback by default, the browser's timing on request | Accepted |
| [0037](decisions/0037-coalescing-opt-out.md) | HTTP/2 coalescing stays the browser's; `DisableCoalescing` and `MaxConnsPerHost` for speed | Accepted |
| [0038](decisions/0038-http3-over-socks5.md) | HTTP/3 through SOCKS5 proxies on request (UDP ASSOCIATE), never a datagram around the proxy | Accepted |
| [0039](decisions/0039-proxy-credentials-up-front.md) | Proxy credentials from the first request by default, the browser's challenge on request | Accepted |
| [0040](decisions/0040-idle-connection-cleanup.md) | Idle HTTP/2 and HTTP/3 connections close; unused proxies and dropped Transports let theirs go | Accepted; amended: `Config.IdleConnTimeout` and `pool.Shared.OnClose`; `Config.MaxIdleProxies` |
| [0041](decisions/0041-default-request-timeout.md) | `NewClient` times requests out after 30 s | Accepted |
| [0042](decisions/0042-http2-opening-records.md) | The HTTP/2 opening is cut into TLS records as the browser cuts it (`http2.openingRecords`) | Accepted |
| [0043](decisions/0043-http3-ack-rate-opt-in.md) | An opt-in lower HTTP/3 ACK rate: `Config.HTTP3ACKEvery` (Chrome's every 10 by default) | Accepted |
| [0044](decisions/0044-profile-fingerprint.md) | Transports find their shared profile by a fingerprint of its content, not its JSON | Accepted |
| [0045](decisions/0045-udp-receive-buffer-default.md) | QUIC sockets ask for Chrome's 1 MB receive buffer by default (`Config.UDPReadBufferSize`; negative keeps the OS's) | Accepted |

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
