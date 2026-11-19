# The capture tool

## Running it

- `moon run harness:capture` launches every installed browser (Chrome, Firefox, Safari) against an in-process capture server.
- It writes new captures to `testdata/captures/`.
- It never overwrites. Delete a capture to retake it.
- Use visible windows for captures you commit. Headless Chrome reports `HeadlessChrome` in its user agent.
- On Linux with no display (a server, WSL), the tool starts Xvfb for the browsers' windows. The browsers run windowed there, not headless.
- moon skips this task when `CI=true`.

## Samples

- Each browser is sampled three times.
  - Change the count with `-samples`, or per scenario as `-scenarios page:3,http1:1`.
- Each sample runs on a fresh server and port, so no connection or TLS session carries over.
- If the samples differ only in extension order, the capture records `"extensionOrder": "shuffled"` (Chrome).
  Otherwise it records `"fixed"`.
  So the default scenario needs at least two samples to tell.
- Any other difference between samples fails the capture.
  It is randomness a profile cannot describe, and it needs investigating.

## Summary and preflight

- `-summary FILE` appends a JSON line per written scenario: profile name, user agent, samples, seconds.
- `-preflight` prints what a capture on the capture machine needs as JSON, and exits.
  It covers browser versions, console user, CA trust, resolver, aioquic and Safari's proxy.

## The capture page

The capture page loads like a site.
It first loads a stylesheet, an image and a script on one connection, then:

- a 16 MiB download
- a 200 KB upload
- a request carrying a header whose Huffman coding is longer than its value
- a navigation to `/nav` with two cookies.
  The cookies are scoped to `/nav` and deleted there, so Safari keeps nothing.

The page ends by requesting `/done`.

An HTTP/2 connection that ends before `/done` is a truncated trace.
It is skipped, and logged with its last frames.

## HTTP/2 traces

- `<name>.h2trace.json` is a JSON array with every sample's trace.
- A trace holds every frame the browser sent and each header block's HPACK instructions.
- The profile JSON still comes from the first request only.

## Chrome's start and retaken samples

Chrome starts on `about:blank` and gets the capture URL 5 s later.
The delay is for fresh profiles:

- A fresh profile rebuilds its certificate verifiers within seconds of launch.
- The rebuild retires every open connection.
- A long-running browser never shows a server that.
- On a warm Mac, rebuilds come 0.4–3 s after launch.

A cold Mac can take longer.
The tool takes a sample again, up to twice, in two cases:

- **Moved connection:** a single-host page whose finishing connection does not start with the navigation (`/`) moved connection partway.
  Retaking it avoids recording a subresource as the profile's request.
- **Early exit:** a browser exits before it is sent to the page.

## Other browser builds

### Chrome for Testing

- `-chrome-app PATH` captures another Chrome build than `/Applications/Google Chrome.app`.
- Use it for Chrome for Testing, for a version no longer installable as the brand.
- Name such a capture `<name>.<scenario>.chrome-for-testing.<suffix>` (`.pagetrace.json.gz`, `.h1trace.json.gz`).
- The tools that list a profile's captures skip it.
- Only the network stack's fields read it: `http2.openingRecords`, `http1.uploadRecords`, `tls.trailingDotSni` and `http3.qpack.resumed`.
  The code that finds it is `capture.TestingPages`.
- The reason: the testing build shares the brand's network stack but not its headers ([decision 0042](../decisions/0042-http2-opening-records.md), [0052](../decisions/0052-http1-upload-records.md)).

### Older Firefox

- `-firefox-app PATH` captures another Firefox than the installed one.
  - macOS: an application.
  - Windows: `firefox.exe`.
- Use it for an older release from Mozilla's archive.
  Keep it beside the installed one with its updates off:
  - macOS: a read-only bundle.
  - Windows: `distribution\policies.json` with `DisableAppUpdate`.
- It is the brand, so its captures are named and read as any other.

### With `cmd/refresh`

`cmd/refresh` passes both flags to every machine of its run.
So give it one machine's browsers at a time.

## Chrome's keychain flags

- Every Chrome launch gets `--use-mock-keychain` and `--password-store=basic`.
- They change only how Chrome encrypts its stored cookies and passwords, never the wire.
- They keep a testing build from asking for the keychain on screen.
  A testing build has no login-keychain item of its own.
