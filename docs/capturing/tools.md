# Other harness tools

## tracestat

- **Run:** `go run ./cmd/tracestat FILE.h2trace.json...`
- **Does:** lists the features a capture's samples disagree on.
  - Exact: request order, HPACK shapes, priorities, frame order.
  - Within 2%: largest DATA frame and median WINDOW_UPDATE.
- **Use:** a varying feature is either modelled or recorded as a known gap.
  Never pick one sample's value.

## diff

- **Run:** `go run ./cmd/diff WANT.json GOT.json`
- **Does:** lists every difference in what a single-connection capture records.
  That is the ClientHello, the HTTP/2 preface and the first request's headers.
  - It compares extension order as WANT's `extensionOrder` says.
  - It exits 1 if there are any differences.
- **Not compared:** the rest of a profile (`http1`, kinds, variants, context, cookies, proxy, hpack, flow control).

## clientcapture

- **Run:** `go run ./cmd/clientcapture -scenario NAME -out DIR -name LABEL -- COMMAND {url}...`
- **Does:** captures any other client.
  - It runs the command three times, each against a fresh server.
  - It keeps each run's first connection that carried a request.
  - It writes the combined capture.
  - When the client varies where browsers do not, it logs that and does not fail.
- **Output:** `reports/` holds comparisons made with it.
  Date them, and name the client versions.
  Keep the captures in `testdata/clients/`.

## interop

- **Run:** `go run ./cmd/interop -local` or `-public`.
  It is opt-in, never part of `go test`.
- **Does:** runs every supported profile's QUIC and HTTP/3 against other implementations.
  - **Local:** `-local` starts the QUIC interop runner's server images in Docker standalone.
    Its cases: handshake, transfer, multiplexing, Retry, resumption, 0-RTT, ChaCha20, key update, loss, corruption, blackholes, discovery, blocked UDP.
  - **Public:** `-public` goes to real HTTP/3 sites with ordinary homepage GETs.
    It does Alt-Svc discovery through the public API, then a fresh and a resumed QUIC connection.
- **Narrowing:** `-servers`, `-cases`, `-sites` and `-profiles` narrow a run.
  `-addr PORT -setup DIR` reruns the cases against a container started by hand.
- **Output:** goes to a temporary directory.
  Copy it to `reports/quic-interop-<date>.md` to keep a run.

## realcheck

- **Run:** `go run ./cmd/realcheck`.
  It is opt-in, never part of `go test`.
  It needs the network.
- **Does:** compares mimicry with the real browsers as public fingerprinting services see them.
  - Every supported profile (or those `-profiles` names) visits echo endpoints as a navigation: tls.peet.ws, tls.browserleaks.com, fp.impersonate.pro over HTTP/2 and HTTP/3.
  - Then it visits a few homepages behind bot-management vendors (Cloudflare, Akamai, DataDome, HUMAN, Amazon), once each, a few seconds apart.
  - Go's net/http client goes last, for contrast.
- **Browsers:** `-browsers chrome,firefox,safari` drives the installed browsers through the same visits first.
  - This takes over the screen, so keep it to one short batch.
  - Chrome and Firefox run on throwaway profiles, over DevTools and WebDriver BiDi.
  - Safari runs in a new window of the user's own, through AppleScript.
- **Output:** answers go to `-out` (default `$TMPDIR/mimicry-realcheck`).
  It holds this machine's public IP, so never commit it.
- **Comparison:** each run compares everything in `-out`, field by field, into `compare.md` and `compare-full.md`.
  - A browser `browser-<name>-<major>-<platform>` is compared against the profile `<name>-<major>-<platform>`.
    The platform is the one realcheck runs on.
  - Per-connection randomness the profile models is told apart from differences.
    That is shuffled extension and transport parameter order, GREASE ECH lengths and suites, and random packet numbers.
  - `-echo=false -sites=false -profiles none` only compares.
- **Keeping a run:** copy `compare.md` to `reports/realcheck-<date>.md`.
