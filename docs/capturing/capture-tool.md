# The capture tool

`moon run harness:capture` launches every installed browser (Chrome,
Firefox, Safari) against an in-process capture server and writes new
captures to `testdata/captures/`. It never overwrites; delete a
capture to retake it. Use visible windows for captures you commit: headless
Chrome reports `HeadlessChrome` in its user agent. On Linux with no
display (a server, WSL), the tool starts Xvfb for the browsers' windows:
they run windowed there, not headless. moon skips this task
when `CI=true`.

Each browser is sampled three times (`-samples`, or per scenario as
`-scenarios page:3,http1:1`), each on a fresh server and port so no
connection or TLS session carries over. If the samples differ only in
extension order, the capture records `"extensionOrder": "shuffled"`
(Chrome), otherwise `"fixed"` (so the default scenario needs at least two
samples to tell). Any other difference between samples fails the capture:
it is randomness a profile cannot describe and needs investigating.
`-summary FILE` appends a JSON line per written scenario (profile name,
user agent, samples, seconds); `-preflight` prints what a capture on the
capture machine needs (browser versions, console user, CA trust, resolver, aioquic,
Safari's proxy) as JSON and exits.

The capture page loads like a site: a stylesheet, an image and a script on
one connection, then a 16 MiB download, a 200 KB upload, a request carrying
a header whose Huffman coding is longer than its value, and a navigation to
`/nav` with two cookies (scoped to `/nav` and deleted there, so Safari keeps
nothing). The page ends by requesting `/done`; an HTTP/2 connection that
ends before that is a truncated trace and is skipped, logged with its last
frames. `<name>.h2trace.json` is a JSON array with every sample's trace:
every frame the browser sent and each header block's HPACK instructions.
The profile JSON still comes from the first request only.

Chrome starts on `about:blank` and gets the capture URL 5 s later: a fresh
profile rebuilds its certificate verifiers within seconds of launch and
retires every open connection, which a long-running browser never shows a
server (on a warm Mac, rebuilds come 0.4–3 s after launch). A cold
Mac can take longer: a single-host page whose finishing connection does not
start with the navigation (`/`) moved connection partway, and the tool
takes that sample again, up to twice, rather than record a subresource as
the profile's request. It does the same for a browser that exits before
it is sent to the page.

`-chrome-app PATH` captures another Chrome build than
`/Applications/Google Chrome.app`: Chrome for Testing, for a version no
longer installable as the brand. Name such a capture
`<name>.<scenario>.chrome-for-testing.<suffix>`
(`.pagetrace.json.gz`, `.h1trace.json.gz`). The tools that list a
profile's captures skip it; only the network stack's fields read it
(`http2.openingRecords`, `http1.uploadRecords`, `tls.trailingDotSni`,
`http3.qpack.resumed`; `capture.TestingPages`), since the testing build
shares the brand's network stack but not its headers
([decision 0042](../decisions/0042-http2-opening-records.md),
[0052](../decisions/0052-http1-upload-records.md)). `-firefox-app PATH`
captures another Firefox than the installed one, an application on macOS
or `firefox.exe` on Windows: an older release from Mozilla's archive, kept
beside the installed one with its updates off (a read-only bundle on
macOS, `distribution\policies.json` with `DisableAppUpdate` on Windows).
It is the brand, so its captures are named and read as any other.
`cmd/refresh` passes both flags to every machine of its run, so give it one
machine's browsers at a time. Every Chrome launch gets
`--use-mock-keychain` and `--password-store=basic`: they change only how
Chrome encrypts its stored cookies and passwords, never the wire, and keep
a testing build (which has no login-keychain item of its own) from asking
for the keychain on screen.
