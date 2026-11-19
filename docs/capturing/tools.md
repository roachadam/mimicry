# Other harness tools

- `go run ./cmd/tracestat FILE.h2trace.json...` lists the features a
  capture's samples disagree on (request order, HPACK shapes, priorities,
  frame order exactly; largest DATA frame and median WINDOW_UPDATE within
  2%). A varying feature is either modelled or recorded as a known gap;
  never pick one sample's value.
- `go run ./cmd/diff WANT.json GOT.json` lists every difference in what a
  single-connection capture records (the ClientHello, the HTTP/2 preface and
  the first request's headers), comparing extension order as WANT's
  `extensionOrder` says, and exits 1 if there are any. The rest of a
  profile (`http1`, kinds, variants, context, cookies, proxy, hpack, flow
  control) is not compared.
- `go run ./cmd/clientcapture -scenario NAME -out DIR -name LABEL -- COMMAND {url}...`
  captures any other client: it runs the command three times, each against
  a fresh server, keeps each run's first connection that carried a
  request, and writes the combined capture (logging, not failing, when the
  client varies where browsers do not). `reports/` holds
  comparisons made with it; date them, name the client versions, and keep
  the captures in `testdata/clients/`.
- `go run ./cmd/interop -local` or `-public` (opt-in, never part of
  `go test`) runs every supported profile's QUIC and HTTP/3 against other
  implementations: `-local` starts the QUIC interop runner's server images
  in Docker standalone (handshake, transfer, multiplexing, Retry,
  resumption, 0-RTT, ChaCha20, key update, loss, corruption, blackholes,
  discovery, blocked UDP); `-public` goes to real HTTP/3 sites with
  ordinary homepage GETs (Alt-Svc discovery through the public API, then a
  fresh and a resumed QUIC connection). `-servers`, `-cases`, `-sites` and
  `-profiles` narrow a run; `-addr PORT -setup DIR` reruns the cases
  against a container started by hand. Output goes to a temporary
  directory; copy it to `reports/quic-interop-<date>.md` to keep a run.
- `go run ./cmd/realcheck` (opt-in, never part of `go test`; needs the
  network) compares mimicry with the real browsers as public
  fingerprinting services see them: every supported profile (or those
  `-profiles` names) visits echo endpoints (tls.peet.ws,
  tls.browserleaks.com, fp.impersonate.pro over HTTP/2 and HTTP/3) as a
  navigation, then a few homepages behind bot-management vendors
  (Cloudflare, Akamai, DataDome, HUMAN, Amazon) once each, a few seconds
  apart, with Go's net/http client last for contrast.
  `-browsers chrome,firefox,safari` drives the installed browsers through
  the same visits first; this **takes over the screen** (Chrome and Firefox
  on throwaway profiles over DevTools and WebDriver BiDi, Safari in a new
  window of the user's own, through AppleScript), so keep it to one short
  batch. Answers go to `-out` (default `$TMPDIR/mimicry-realcheck`; it holds
  this machine's public IP, never commit it), and each run compares
  everything there, field by field, into `compare.md` and
  `compare-full.md`: a browser `browser-<name>-<major>-<platform>` (the
  platform realcheck runs on) against the profile
  `<name>-<major>-<platform>`, with per-connection randomness the profile models
  (shuffled extension and transport parameter order, GREASE ECH lengths and
  suites, random packet numbers) told apart from differences.
  `-echo=false -sites=false -profiles none` only compares. Copy
  `compare.md` to `reports/realcheck-<date>.md` to keep a run.
