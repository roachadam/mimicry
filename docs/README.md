# mimicry documentation

Working documentation, written as the code is built and polished before
release. Three kinds:

- [`decisions/`](decisions/): why mimicry is built the way it is. One record
  per decision: the problem, the options, the choice, the evidence, and what
  would make us revisit it. Read these before "fixing" something that looks
  odd.
- [`concepts/`](concepts/): how it works, for users and contributors.
- `guides/` (later): how to do things with it.

Related, elsewhere in the repository:

- [`profiles/EVIDENCE.md`](../profiles/EVIDENCE.md): every claim a built-in
  profile makes, its evidence and the test that keeps it true.
- [`harness/reports/`](../harness/reports/): dated measurement reports.
- [`AGENTS.md`](../AGENTS.md): working conventions.

## Decisions

| # | Decision | Status |
|---|---|---|
| [0001](decisions/0001-own-http-stack-thin-utls-fork.md) | Own HTTP/1.1 and HTTP/2 stack; uTLS as a thin fork | Accepted |
| [0002](decisions/0002-net-http-types.md) | `net/http` types in the API, no forked `net/http` | Accepted |
| [0003](decisions/0003-evidence-rule.md) | Browser wire captures are the evidence | Accepted |
| [0004](decisions/0004-complete-profile-files.md) | One complete profile file per browser version, no inheritance | Accepted |
| [0005](decisions/0005-request-kinds-api.md) | Request kinds: defaults exact, deviations visible | Accepted |
| [0006](decisions/0006-header-order-variants.md) | Header order: a default per kind plus measured variants | Accepted |
| [0007](decisions/0007-navigation-stays-top-level.md) | The navigation stays the profile's top-level request | Accepted |
| [0008](decisions/0008-unmeasured-is-an-error.md) | Asking for what was never measured is an error | Accepted |
| [0009](decisions/0009-own-cookie-jar.md) | Our own cookie jar, driven by the transport | Accepted |
| [0010](decisions/0010-dial-context.md) | `Config.DialContext` for the TCP connection | Accepted |
| [0011](decisions/0011-connection-partitioning.md) | Model connection partitioning only where a rule is measured | Accepted |
| [0012](decisions/0012-cookie-rules-in-profiles.md) | Browser cookie rules as a profile section | Accepted |
| [0013](decisions/0013-tls-resumption.md) | TLS 1.3 session resumption as the browser does it | Accepted |

New records copy [`decisions/template.md`](decisions/template.md).

## Concepts

- [How a request is built](concepts/how-a-request-is-built.md)
- [Cookies](concepts/cookies.md)
