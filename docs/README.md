# mimicry documentation

## Using mimicry

- [What mimicry does on the wire](concepts/what-mimicry-does.md)
- [How a request is built](concepts/how-a-request-is-built.md)
- [Cookies](concepts/cookies.md)
- [Proxies](concepts/proxies.md)
- [CORS preflights](concepts/cors-preflights.md)
- [WebSockets](concepts/websockets.md)
- [HTTP/3](concepts/http3.md)
- [Coming from tls-client](concepts/coming-from-tls-client.md)

## Evaluating it

- [Performance](performance.md): speed and memory compared with `net/http` and other clients.
- [Profile evidence](../profiles/EVIDENCE.md): what each built-in profile claims, and the test that checks it.
- [Licence](../README.md#licence): mimicry is MIT; code copied from other projects keeps its licence.

## Contributing

- [Refreshing profiles](refreshing-profiles.md): updating profiles for a new browser version.
- [Capturing](capturing.md): capturing browsers and the harness tools.
- [Benchmarks](../internal/bench/README.md) and [measurement reports](../internal/harness/reports/).
- [AGENTS.md](../AGENTS.md): working conventions.
- Decisions, below. New records copy [`decisions/template.md`](decisions/template.md).

## Decisions

Each record explains why mimicry works the way it does, and what would reopen the question. Read the record before changing behaviour that looks odd.

### Design and API

| # | Decision | Status |
|---|---|---|
| [0001](decisions/0001-own-http-stack-thin-utls-fork.md) | Own HTTP/1.1 and HTTP/2 stack; uTLS as a thin fork | Amended by 0027 |
| [0002](decisions/0002-net-http-types.md) | `net/http` types in the API, no forked `net/http` | Accepted |
| [0018](decisions/0018-no-public-engine.md) | No public Engine interface | Amended by 0044 |
| [0019](decisions/0019-transport-is-one-browsing-entity.md) | A Transport is one browsing entity | Amended by 0040, 0044 |
| [0020](decisions/0020-connection-pool-package.md) | The connection pool is its own package | Amended by 0040 |
| [0029](decisions/0029-scope-what-gets-modelled.md) | Model what anti-bot systems check or users notice | Amended by 0036, 0037 |
| [0034](decisions/0034-request-plans.md) | Request plans: RoundTrip's path, without sending | Accepted |
| [0041](decisions/0041-default-request-timeout.md) | `NewClient` times requests out after 30 s | Accepted |
| [0046](decisions/0046-response-decompression.md) | Responses are decoded whoever set `Accept-Encoding` | Accepted |

### Profiles and evidence

| # | Decision | Status |
|---|---|---|
| [0003](decisions/0003-evidence-rule.md) | Browser wire captures are the evidence | Amended by 0035 |
| [0004](decisions/0004-complete-profile-files.md) | One complete profile file per browser version | Amended by 0035 |
| [0008](decisions/0008-unmeasured-is-an-error.md) | Asking for what was never measured is an error | Accepted |
| [0024](decisions/0024-clienthello-shape-in-profiles.md) | The ClientHello's whole shape is profile data | Amended |
| [0035](decisions/0035-version-policy-and-capture-inheritance.md) | Newest-version accessors, five supported versions, inherited captures | Amended by 0047 |
| [0044](decisions/0044-profile-fingerprint.md) | Transports find a shared profile by a hash of its content | Accepted |
| [0047](decisions/0047-windows-profiles.md) | Windows profiles: `chrome-<major>-windows` and `ChromeWindows()` | Accepted |

### Requests and headers

| # | Decision | Status |
|---|---|---|
| [0005](decisions/0005-request-kinds-api.md) | Request kinds: defaults exact, deviations visible | Amended by 0036, 0039 |
| [0006](decisions/0006-header-order-variants.md) | Header order: a default per kind plus measured variants | Amended by 0021 |
| [0007](decisions/0007-navigation-stays-top-level.md) | The navigation stays the profile's top-level request | Accepted |
| [0015](decisions/0015-non-secure-origins.md) | Requests to non-secure origins as captured | Accepted |
| [0016](decisions/0016-literal-urls-no-https-upgrade.md) | Requests go to the URL given; no HTTPS upgrades or HSTS by default | Accepted |
| [0017](decisions/0017-cors-preflights.md) | CORS preflights as the browser sends them | Accepted |
| [0021](decisions/0021-page-headers-hash-table-order.md) | Page-set headers in the browser's hash-table order | Accepted |
| [0022](decisions/0022-redirects-as-the-browser-follows-them.md) | Redirects follow the browser's rules, inside the Transport | Amended |
| [0030](decisions/0030-header-placement.md) | Header placement: move one header, keep the browser's order | Accepted |
| [0048](decisions/0048-cookie-twins.md) | Cookie twins of the redirect scenarios measure each hop's `cookie` slot | Accepted |

### Cookies

| # | Decision | Status |
|---|---|---|
| [0009](decisions/0009-own-cookie-jar.md) | mimicry's own cookie jar, driven by the transport | Accepted |
| [0012](decisions/0012-cookie-rules-in-profiles.md) | Browser cookie rules as a profile section | Amended by 0048 |
| [0031](decisions/0031-cookies-for-any-site.md) | Seeding cookies from strings, and cookies for any site | Accepted |

### Connections and TLS

| # | Decision | Status |
|---|---|---|
| [0010](decisions/0010-dial-context.md) | `Config.DialContext` for the TCP connection | Accepted |
| [0011](decisions/0011-connection-partitioning.md) | Partition connections only where a rule is measured | Amended |
| [0013](decisions/0013-tls-resumption.md) | TLS session resumption as the browser does it | Accepted |
| [0023](decisions/0023-connection-limits-and-coalescing.md) | Per-host HTTP/1.1 limit and HTTP/2 coalescing, per browser | Amended by 0037 |
| [0032](decisions/0032-certificate-pinning.md) | Certificate pinning and VerifyConnection on every handshake | Accepted |
| [0037](decisions/0037-coalescing-opt-out.md) | Options to disable HTTP/2 coalescing and change the per-host limit | Accepted |
| [0040](decisions/0040-idle-connection-cleanup.md) | Idle connections close after a timeout | Amended |
| [0042](decisions/0042-http2-opening-records.md) | The HTTP/2 opening is grouped into TLS records as the browser does | Accepted |

### Proxies and WebSockets

| # | Decision | Status |
|---|---|---|
| [0014](decisions/0014-proxies.md) | Proxies as the browser uses them | Amended by 0038, 0039, 0040 |
| [0025](decisions/0025-websockets.md) | WebSockets as each browser opens and frames them | Amended |
| [0033](decisions/0033-proxy-errors.md) | Proxy failures are a `*ProxyError` | Amended by 0039 |
| [0038](decisions/0038-http3-over-socks5.md) | HTTP/3 through SOCKS5 on request; never a datagram around a proxy | Accepted |
| [0039](decisions/0039-proxy-credentials-up-front.md) | Proxy credentials sent up front by default | Accepted |

### HTTP/3 and QUIC

| # | Decision | Status |
|---|---|---|
| [0026](decisions/0026-http3-capture.md) | HTTP/3 is captured from the client's own packets | Accepted |
| [0027](decisions/0027-quic-base.md) | QUIC from a copy of x/net/quic; HTTP/3 and QPACK written for mimicry | Amended |
| [0028](decisions/0028-quic-failure-model.md) | QUIC and HTTP/3 fail as each browser's do | Amended by 0036 |
| [0036](decisions/0036-fast-quic-fallback.md) | Fast QUIC fallback by default, the browser's timing on request | Accepted |
| [0043](decisions/0043-http3-ack-rate-opt-in.md) | HTTP/3 keeps Chrome's ACK rate unless configured | Accepted |
| [0045](decisions/0045-udp-receive-buffer-default.md) | QUIC sockets ask for Chrome's 1 MB receive buffer by default | Accepted |
