# Capturing browsers and deriving profiles

For contributors capturing browsers; users don't need this page.

How the `internal/harness/` module captures the real browsers, derives a profile's generated sections from those captures, and what its other tools do.

- **Routine refreshes:** to bring a profile up to a new browser version, use `cmd/refresh` ([Refreshing profiles](refreshing-profiles.md)). These pages are what it runs underneath, and what to do by hand.
- **Paths:** commands run in `internal/harness/`, and paths are relative to it, unless they say otherwise. `profiles/data/` and `cmd/profilegen` are at the repository root.
- **Why:**
  - Captures are the evidence: [decision 0003](decisions/0003-evidence-rule.md).
  - HTTP/3: [decision 0026](decisions/0026-http3-capture.md).
  - Versions and inherited captures: [decision 0035](decisions/0035-version-policy-and-capture-inheritance.md).

## Where to look

| Page | What it covers |
|---|---|
| [The capture tool](capturing/capture-tool.md) | `cmd/capture`: flags, samples, output files |
| [Browser setup](capturing/browser-setup.md) | How each desktop browser is launched, per OS |
| [Safari on an iPhone](capturing/iphone.md) | iPhone captures: Safari, Chrome and Firefox |
| [Scenarios](capturing/scenarios.md) | The scenario set; ECH, real-page, click, URL parsing and client-certificate scenarios |
| [Proxy scenarios](capturing/proxy-scenarios.md) | HTTP, SOCKS and HTTPS proxies |
| [HTTP/3 scenarios](capturing/http3-scenarios.md) | QUIC and HTTP/3 captures |
| [Deriving a profile](capturing/deriving.md) | The generators: `hellogen` → `h3gen` → `kindsgen` |
| [Other harness tools](capturing/tools.md) | `realcheck`, `pageshape`, `tracestat`, `diff`, `clientcapture`, `interop` |

## Tools

| Tool | What it does | Run by `cmd/refresh` |
|---|---|---|
| `cmd/refresh` | Brings a profile up to a new browser version, end to end | (itself) |
| `cmd/capture` | Captures the installed browsers against the capture server | yes |
| `cmd/hellogen` → `cmd/h3gen` → `cmd/kindsgen` | Derive a profile's generated sections from its captures | yes |
| `cmd/realcheck` | Compares mimicry with the real browsers through public fingerprinting services | yes (`-realcheck`) |
| `cmd/pageshape` | Records a replayed real page's shape | no; investigation |
| `cmd/tracestat` | Lists features a capture's samples disagree on | no; investigation |
| `cmd/diff` | Diffs two single-connection captures | no; investigation |
| `cmd/clientcapture` | Captures any other client | no; investigation |
| `cmd/interop` | Runs QUIC and HTTP/3 against other implementations | no; investigation |
