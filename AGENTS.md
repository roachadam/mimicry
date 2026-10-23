# Repository working conventions

## What this is

mimicry is a Go HTTP client that reproduces real browsers' network fingerprints. Accuracy first, then speed. The library module is the repo root (`github.com/roachadam/mimicry`); `harness/` is a separate module that captures real browsers and diffs mimicry's profiles against them.

## Commands

Tool versions are pinned in `.prototools` (Go, moon). golangci-lint is pinned in `.moon/tasks/go.yml` and run with `go run`, so it is built with the pinned Go; run it through moon rather than a system install.

```sh
moon run :test :vet :lint   # every project
moon run mimicry:test       # the library only
moon run harness:test       # the harness only
```

## Package boundaries

The public surface is small: the root package (`Transport`, `NewClient`, `Config`, `Engine`, `HeaderOrderKey`, and request kinds: `Kind`, `Request`, `As`, `With`, `Exact`), `profile` and `profiles`. Everything else is in `internal/`, flat, one package per concern. Each package's `doc.go` states what it may import; keep to it.

- Only `internal/handshake` imports `github.com/roachadam/utls`.
- Only `internal/engine/native` assembles the request path (pool → dial → handshake → h1/h2).
- `profile` imports the standard library only.
- Public packages never import from `harness/`.
- Use `net/http` types in the public API. Header order comes from the profile (the navigation, or the request's kind and its variant), with `HeaderOrderKey` as a per-request override; pseudo-header order comes from the profile only. Do not fork `net/http`.
- One dial path (`internal/dial`), and every network step honours the context and its deadline.
- Never hold a lock across network I/O.
- Configuration is a struct (`Config`), not functional options or boolean parameters.

## The uTLS fork

mimicry depends on `github.com/roachadam/utls` at a pseudo-version of its `main` branch. To move to a newer fork commit, merge it there and run `go get github.com/roachadam/utls@main`. To try an unmerged fork change, use a local `go.work` that includes `../utls`; it is gitignored and must never be committed.

## Go clocks in tests

Use `github.com/jonboulle/clockwork` for code that depends on the current time. Inject a `clockwork.Clock`, use `clockwork.NewRealClock` in production and `clockwork.NewFakeClockAt` in tests. Advance the fake clock instead of sleeping.

## Go formatting

Write Go function and method bodies on multiple lines, including simple accessors and test stubs. Do not use one-line declarations such as `func (p *Profile) Name() string { return p.name }`.

Write struct declarations and populated struct literals on multiple lines, with each field on its own line and the closing brace on a separate line. This includes single-field structs, constructor return values, and structs passed as function arguments. Empty `struct{}` types and empty literals may stay compact.

```go
type Plan struct {
    hello *utls.ClientHelloSpec
}

cfg := mimicry.Config{
    Profile: profiles.Chrome155Windows,
    Timeout: 30 * time.Second,
}
```

## Tests

Use the standard `testing` package with hand-written fakes; no assertion libraries. Tests must be deterministic: inject randomness and clocks rather than depending on them.

Profile accuracy is proven by the harness against real browser captures, not by unit tests that restate a profile's values. `harness/e2e` checks every built-in profile four ways:

- `TestProfilesMatchCaptures`: mimicry's request to the capture server has no differences from the browser's capture (TLS, HTTP/2 preface and first request, headers).
- `TestHPACKReproducesBrowserBlocks`: re-encoding the browser's traced requests with the profile's `http2.hpack` reproduces every header block byte for byte, in every sample of every scenario and on every connection of every replayed real page. `http2.hpack` cannot be captured; this is what keeps it honest. A failure names the first instruction that differs.
- `TestConnectionMatchesBrowserTrace`: on one connection, mimicry's first header block and largest upload DATA frame match every sample of the browser's trace, and every WINDOW_UPDATE credit increment it sends lies within the range the browser's own increments span (± one frame). Ranges, not single values, because credit timing varies between a browser's own connections.
- `TestCookiesMatchBrowserTrace`: cookies set by the server come back from mimicry, through a real cookie jar, in the browser's order and representation.
- `TestHTTP1MatchesBrowserTrace`: over HTTP/1.1, mimicry's two navigations (`/`, then `/nav` with a referer and cookies) are the browser's header lines exactly, casing, order and values, in every sample of the `http1` scenario (authority normalised).

Behaviour that depends on time (Chrome's 5 s credit return, its idle PING) is tested in `internal/h2` with a fake clock against a raw HTTP/2 peer; the captures show it, the unit tests pin it.

## Built-in profiles

Built-in profiles are JSON files in `profiles/data/`, one per browser version and platform; the typed Go variables are generated from them. Every built-in profile must pass `profile.Parse`, enforced twice: the generator refuses to generate from an invalid file, and a test in `profiles` parses every embedded file. A profile without a matching capture in `harness/testdata/captures/` is not finished.

Each profile file is complete and self-contained: no inheritance between versions or platforms, so every value is visible in one file and checked against that version's own capture. A new version starts as a copy of the previous one; replace every captured field from the new capture, and treat everything copied that a capture cannot show (`http2.hpack`, header slots, `cookie_order`, `http1` slots) as unverified until the e2e tests pass against the new traces. Planned: `profilegen derive` to automate this copy and list the copied fields.

`profiles/EVIDENCE.md` lists every claim the profiles make, its evidence (wire, fitted to the wire, measured on another request kind, or copied) and the test that keeps it true. A profile change that adds, removes or re-sources a claim updates its row in the same commit.

A profile's `kinds`, `context` and navigation `variants` are generated, never hand-edited: `go run ./cmd/kindsgen -profile ../profiles/data/<name>.json` (in `harness/`) derives them from `<name>.kinds.pagetrace.json.gz` and `<name>.kinds-http1.pagetrace.json.gz`, and refuses captures it cannot reconcile. Rerun it after recapturing kinds; `TestKindsMatchBrowserTraces` checks the result against the same traces.

## Capturing browsers

`moon run harness:capture` launches every installed browser (Chrome, Firefox, Safari) against an in-process capture server and writes new captures to `harness/testdata/captures/`. It never overwrites; delete a capture to retake it. Use visible windows for captures you commit: headless Chrome reports `HeadlessChrome` in its user agent. moon skips this task when `CI=true`.

Each browser is sampled three times (`-samples`), each on a fresh server and port so no connection or TLS session carries over. If the samples differ only in extension order, the capture records `"extension_order": "shuffled"` (Chrome), otherwise `"fixed"`. Any other difference between samples fails the capture: it is randomness a profile cannot describe and needs investigating.

The capture page loads like a site: a stylesheet, an image and a script on one connection, then a 16 MiB download, a 200 KB upload, a request carrying a header whose Huffman coding is longer than its value, and a navigation to `/nav` with two cookies (scoped to `/nav` and deleted there, so Safari keeps nothing). The page ends by requesting `/done`; an HTTP/2 connection that ends before that is a truncated trace and is skipped, logged with its last frames. `<name>.h2trace.json` is a JSON array with every sample's trace: every frame the browser sent and each header block's HPACK instructions. The profile JSON still comes from the first request only.

Scenarios (`internal/server/scenario.go`, `-scenarios`, default all but replays) capture the same browser under other server behaviour: server-set cookies, unusual SETTINGS, a slow download, an idle connection, and HTTP/1.1 only. Each writes `<name>.<scenario>.h2trace.json`; its first request is compared with the default page's and any change is logged as a finding. The `http1` scenario offers only `http/1.1` in ALPN and writes `<name>.http1.h1trace.json` instead: per sample, every request of the page load across the browser's connections, request line and header lines verbatim, with the connection each came on. Add a scenario when a behaviour might depend on the server; reuse the default page wherever possible.

Replay scenarios load the shape of real pages with synthetic content, to check what the small capture page cannot: hundreds of requests of every kind, dozens of hosts and connections, kilobytes of cookies. Record a page with a throwaway Chrome profile (`--log-net-log=FILE --net-log-capture-mode=IncludeSensitive`, 5 s on `about:blank` first, as the capture tool does), then `go run ./cmd/pageshape -site nike.com -out testdata/pages/nike.com.json FILE` (in `harness/`); delete the log afterwards, it holds real cookies. Only lengths and character classes leave it. `-scenarios replay-<site>` replays one page, `replay` all of `testdata/pages` (not part of the default run: each sample takes the page's length, up to 35 s). Each host is served under `.localhost` with its site kept apart (`www.nike.com` → `www.nike-com.localhost`) and its own certificate from the capture CA; generated documents (top and every iframe's) schedule the recorded requests with the element or API giving their `sec-fetch-dest` and mode; cookies of the recorded shapes are set and deleted again at the end. The capture is `<name>.replay-<site>.pagetrace.json.gz` (gzipped: tens of MiB of JSON otherwise): per sample, every HTTP/2 connection's trace (with its host) and every HTTP/1.1 request. Its profile keeps only the ClientHello and user agent, since the first request on the connection that ends the page varies with timing. HTTP/3 parts of a real page are replayed over HTTP/2.

Chrome starts on `about:blank` and gets the capture URL 5 s later: a fresh profile rebuilds its certificate verifiers within seconds of launch and retires every open connection, which a long-running browser never shows a server.

`go run ./cmd/tracestat FILE.h2trace.json...` (in `harness/`) lists the features a capture's samples disagree on (request order, HPACK shapes, priorities, frame order exactly; largest DATA frame and median WINDOW_UPDATE within 2%). A varying feature is either modelled or recorded as a known gap; never pick one sample's value.

`go run ./cmd/diff WANT.json GOT.json` (in `harness/`) lists every difference between two captures or profiles, comparing extension order as WANT's `extension_order` says, and exits 1 if there are any.

`go run ./cmd/clientcapture -scenario NAME -out DIR -name LABEL -- COMMAND {url}...` (in `harness/`) captures any other client: it runs the command three times, each against a fresh server, and writes the combined capture (logging, not failing, when the client varies where browsers do not). `reports/` holds comparisons made with it; date them, name the client versions, and keep the captures in `testdata/clients/`.

- Chrome and Firefox run with throwaway profiles (no extensions, default settings) and resolve the capture hosts to 127.0.0.1 themselves; Chrome ignores certificate errors and Firefox gets a certificate exception (or, for pages across several hosts, trusts the system's roots). Nothing on the system changes.
- Safari cannot be isolated: it opens a tab in the user's own window (closed afterwards). It must have **no content blocker or other extension enabled** while capturing (they block requests and strip cookies) and default privacy settings. It resolves through the system, so the capture tool runs a DNS stub answering `*.test` with 127.0.0.1 (`internal/dnsstub`), reached through `/etc/resolver/test` (created once: `sudo mkdir -p /etc/resolver && printf 'nameserver 127.0.0.1\nport 15353\n' | sudo tee /etc/resolver/test`). Capture names are under `.test`, never `.localhost`: WebKit treats every `*.localhost` host as a site of its own, which turns same-site requests cross-site. Safari must also trust the capture CA. The CA lives in `<user config dir>/mimicry/capture-ca/`, outside the repo, and is name-constrained to `test` and `localhost` with IP addresses excluded, so it cannot vouch for any real site. Trust it once; the capture command prints this when needed:

  ```sh
  security add-trusted-cert -r trustRoot -p ssl -k ~/Library/Keychains/login.keychain-db \
    ~/Library/Application\ Support/mimicry/capture-ca/ca.pem
  ```

  To undo: `security remove-trusted-cert` with the same file, then delete the directory.
