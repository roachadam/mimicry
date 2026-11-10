# 0068: Real ECH from DNS HTTPS records, as each browser sends it

- Status: Accepted
- Date: 2026-11-10
- Commits: the merge of PR #124, filled in on main

**Summary.** A profile with `tls.realEch` looks up the HTTPS record (RFC
9460) of every host it connects to directly over TLS, and to a host whose
record carries an ECH config list it sends real Encrypted Client Hello
(RFC 9849) as its browser does: the config and HPKE suite the browser
picks, the browser's own inner hello, and the browser's reconnection when
the server rejects it. hellogen derives the section from an `ech` capture
whose hosts publish ECH configs through the capture tool's DNS. Chrome,
Edge and Brave 157 and Firefox 159 profiles send real ECH; Safari 27's
records that Safari ignores the configs, so it looks nothing up. Every
other profile, and every QUIC, proxied and WebSocket-only connection,
keeps the GREASE extension as before. This depends on the uTLS fork's
`fork/ech-inner-spec` branch, which is not yet merged to the fork's `main`.

## Problem

Every profile sent the GREASE ECH extension to every server. Chrome and
Firefox send real ECH to a server whose DNS HTTPS record carries an ECH
config, and the server, which holds the config's key, sees which it got:
a client calling itself Chrome that sends GREASE to a host publishing a
config is one Chrome would not be. EVIDENCE.md listed it as a known gap
(detection medium). Closing it needs what mimicry never did: DNS lookups
of its own (net.Resolver cannot ask for type 65), and a ClientHelloInner
shaped like each browser's, which no capture had shown, since the inner
hello is encrypted.

## Options

- **Always GREASE** (what mimicry did): no DNS traffic, nothing to
  measure, but every server publishing a config sees a browser that
  ignores it.
- **ECH with the TLS library's defaults** (httpcloak's way, read as a
  lead: v1.7.3 asks 8.8.8.8, 1.1.1.1 and 9.9.9.9 for the record with a
  500 ms timeout and hands the list to uTLS's
  `EncryptedClientHelloConfigList`): the inner hello is uTLS's own (its
  crypto/tls construction), not the browser's, so the server that decrypts it sees a hello no browser
  sends; the lookups go to public resolvers whatever the machine uses;
  and every profile behaves alike, though Safari sends no ECH at all.
- **Measured per profile** (chosen): capture each browser against a
  server that publishes ECH configs and decrypts what it gets, derive
  what it does into the profile, and send exactly that.

## Decision

- **The profile section `tls.realEch`** (`profile.RealECH`), derived by
  hellogen from the `ech` capture (and `altSvcIgnored` from `ech-h3`),
  never hand-edited: `ignore` (the browser sends its usual hello, the
  configs unused), `kems` and `skippedKems`, `cipherSuites` (HPKE), the
  inner hello (`inner`: its cipher suites, its own extensions and their
  order, the outer extensions it leaves out), `outerPsk`, `malformed`,
  `retryDrop`, `retryResume`, `retryPlain` and `altSvcIgnored`. Nil is
  not measured: nothing is looked up and every server gets the
  ClientHello as it is, the GREASE extension for a browser that sends
  one.
- **The lookup** (`internal/httpsrr`): for an `https://` request on a
  direct route to a host name (not an IP literal), beside the TCP dial,
  as the browsers ask beside their address lookups; the handshake waits
  for it. One type 65 query for the host, or `_<port>._https.<host>` for
  a port other than 443 (RFC 9460, section 9.1), over UDP and again over
  TCP when truncated, through `Config.Resolver`'s `Dial` when it has one,
  to the system's nameservers (`/etc/resolv.conf`, or the adapters' on
  Windows) otherwise. The ServiceMode record of lowest priority whose
  target is the host itself is used; an AliasMode set and records naming
  another target are not. Answers are cached per Transport by host and
  port for their smallest TTL (a day at most), no record for the SOA's
  negative TTL, a failed lookup for 30 s. Concurrent lookups share one
  query. A lookup that fails or times out (5 s) is no record: the
  connection goes as to any server.
- **The config** (`handshake.SelectECH`): the first config of the list,
  in its order, whose KEM the browser used (configs of another version,
  with a mandatory extension, or with a KEM the browser was seen to pass
  over are skipped), and that config's first HPKE suite. A list with no
  usable config is no ECH. `ErrNotMeasured`, with nothing sent: a config
  ahead of the one picked whose KEM the browser was never seen to use or
  pass over (Chrome: anything but X25519 and P-256; Firefox: anything but
  P-256 and X25519), and a picked config whose first suite the browser
  never used (Firefox: AES-256-GCM; either: a KDF other than
  HKDF-SHA256).
- **A list that does not parse**: as the browser, by `tls.realEch.
  malformed`: Chrome, Edge and Brave make no connection, and the request
  fails with an error wrapping `handshake.ErrMalformedECH`; Firefox sends
  its usual hello with the GREASE extension.
- **The hellos**: the outer hello is the profile's, `server_name` the
  config's public name, the GREASE extension replaced by the real one;
  the inner hello is the browser's (`fingerprint.Plan.ECHHello`, built by
  the handshake as a uTLS `ECHInnerSpec`), padded as RFC 9849, section
  6.1.3 says. A resumed inner hello offers the ticket; the outer one then
  carries no `pre_shared_key` (Chrome) or a GREASE one (Firefox).
- **Rejection**: a server that rejects the config and proves itself the
  public name gets one more connection (`echRetry` in
  `internal/engine/native`): with its retry configs when it sent some,
  otherwise the usual hello with the GREASE extension and the real
  `server_name`, both without `retryDrop`'s extensions (Firefox:
  `compress_certificate`). Later connections use the DNS config again. A
  session from a connection with retry configs is offered later only
  where the browser did (`retryResume`, Chrome); with `retryPlain`
  (Firefox on Windows and Linux) the next connection to that host is the
  usual resumed hello with GREASE. A second rejection fails the request.
- **HTTP/3**: where `altSvcIgnored` is measured (Chrome and Firefox on
  macOS), a response over a connection whose ECH the server accepted
  teaches no `Alt-Svc`, so the host stays on TCP with ECH. HTTP/3 is not
  discovered from HTTPS records (their `alpn` is read and unused), and no
  QUIC hello carries real ECH: a host already on HTTP/3 (an `Alt-Svc`
  learnt from a response without ECH, or loaded through `Config.AltSvc`)
  gets the QUIC hello with GREASE, as before.
- **No lookup through a proxy**: no capture shows whether a browser looks
  HTTPS records up or sends ECH through one (behind an HTTP or `socks5h`
  proxy it does not resolve the name itself [INFERENCE]), so mimicry
  looks nothing up through any proxy and sends GREASE as
  before. The TLS to an `https://` proxy itself, and a WebSocket's own
  HTTP/1.1 connection (`tls.websocketH1`), use the profile's hello with
  GREASE too; a WebSocket over an existing HTTP/2 connection rides that
  connection, ECH and all.
- **The uTLS fork**: the inner hello's shape, the outer hello around it,
  a HelloRetryRequest accepting ECH and the rejection's checks need four
  fork commits on branch `fork/ech-inner-spec` (`f6aff4d`, `00ede91`,
  `d88ff1a` "Let an ECHInnerSpec shape ClientHelloInner", `c977278`),
  which `go.mod` names by
  pseudo-version. It is not merged to the fork's `main`; merge it there
  and `go get github.com/roachadam/utls@main` before this lands, as
  AGENTS.md requires.

## Evidence

The `ech` scenario (`internal/harness/internal/server/ech.go`, a quick
scenario, three samples): five hosts whose HTTPS records (alpn `h2` and an
ECH config list) the capture tool's DNS stub and DoH endpoint publish:
`www.ech.test` (a config the server decrypts, then a resumed connection
inside ECH), `www.ech-retry.test` (a config it holds no key for, rejected
with retry configs, then the next connection), `www.ech-noretry.test`
(rejected without), `www.ech-bad.test` (a list that does not parse) and
`www.ech-suite.test` (a P-256 config first, then an X25519 one listing
AES-256-GCM first). The server decrypts with crypto/tls and records each
connection's ECH (`capture.ECH`: config, suite, acceptance, the inner
hello) and the page's DNS queries (`Page.DNS`).

Captured 2026-11-09, three samples each: Chrome 157 (macOS, Linux), Edge
157 (macOS, Windows), Brave 157 (macOS, Windows, Linux), Firefox 159
(macOS, Windows, Linux) and Safari 27 (macOS).

- **How the browsers got the records.** Chrome on macOS with
  `/etc/resolver/test` (the stub, as Safari uses it) resolved through the
  system's getaddrinfo, which asks for no HTTPS record: A and AAAA only,
  then GREASE. With secure DNS (`dns_over_https` mode `secure` at the
  capture tool's DoH endpoint, in the throwaway profile's Local State) it
  asked for the record and used it. DoH is therefore a capture condition
  for every Chromium browser, recorded in each capture's `conditions`.
  Firefox on macOS asked the system's resolver (via `/etc/resolver/test`)
  and got the records; off macOS nothing sends `*.test` to the stub, so
  it resolves through its own DoH client alone (`network.trr.mode` 3),
  also a recorded condition. Safari asks the system.
- **DNS queries.** Chrome and its kin: A and HTTPS in parallel, no AAAA.
  Firefox: HTTPS and A in parallel (the harness's profile disables IPv6).
  Safari 27: HTTPS, A and AAAA in parallel in the first trial run; its
  committed capture recorded one AAAA query over three samples (the
  system's cache answered the rest [INFERENCE]).
- **Chrome, Edge, Brave** (30 connections per capture, 27 with ECH;
  Chrome on Linux 33 and 30, with a speculative connection per sample):
  X25519 used, the P-256 config at the head of `www.ech-suite.test`'s
  list passed over; the first suite of the config used every time (AES-
  128-GCM, ChaCha20Poly1305 for the retry config, AES-256-GCM for the
  suite host's); inner hello: the ECH marker, `server_name` and
  `supported_versions` (GREASE, TLS 1.3), shuffled per connection, then
  `ech_outer_extensions` naming the rest of the outer hello in its order,
  GREASE included, but `extended_master_secret`, `renegotiation_info`,
  `ec_point_formats` and `session_ticket`; inner cipher suites GREASE and
  TLS 1.3's. A resumed inner hello left the outer one without
  `pre_shared_key`. Rejected with retry configs: one more connection with
  them, accepted; the next connection to the host offered the DNS config
  again with the retry connection's session (`retryResume`). Rejected
  without: one more connection with GREASE and the real name. The list
  that does not parse: no connection at all.
- **Firefox** (33 or 34 connections per capture, 24 to 28 with ECH):
  P-256 and X25519 both used (the P-256 config at the head of the suite
  host's list taken, so AES-256-GCM was never a used config's first
  suite); a fixed inner order, the inner cipher suites its full list; a
  GREASE `pre_shared_key` on the outer hello of a resumed inner one; the
  reconnection after a rejection without `compress_certificate`; the list
  that does not parse: the usual hello with GREASE. After the connection
  with retry configs, on macOS (the system's resolver) the DNS config
  offered again without a session; on Windows and Linux (its own DoH) the
  usual resumed hello with GREASE (`retryPlain`), in every sample. An
  uncommitted trial on macOS through Firefox's DoH showed both in one run
  (EVIDENCE.md, Not covered). One sample on macOS raced two connections
  with the rejected config (pinned in hellogen's `echGaps`).
- **Safari 27** (24 connections): no `encrypted_client_hello` at all on
  connections to hosts with a config (`ignore`).
- **`ech-h3`** (Chrome 157 and Firefox 159 on macOS, `-h3-local-roots`,
  three samples): `www.ech.test`'s record (alpn `h2`, an ECH config) and
  `Alt-Svc: h3=":443"` on every response, QUIC on UDP 443: all 21
  connections each on HTTP/2 with ECH accepted, no QUIC packet. Trials
  not committed: the same record without its ECH config moved both to
  HTTP/3; a record with alpn `h3,h2` and the config sent both straight
  to QUIC with real ECH (outer `server_name` the public name), which the
  capture tool could not decrypt (its keys are logged under the inner
  hello's random), so the QUIC inner hello is not measured.

The e2e tests replay both pages: `TestECHMatchesBrowser` (every
connection's host, config, suite, acceptance, outer and inner hellos and
session offers, and an HTTPS lookup before each host's first connection)
and `TestECHH3MatchesBrowser`. `TestSelectECH` covers the refusals no
capture reaches, and `internal/httpsrr`'s tests the DNS client against a
fake nameserver.

## Consequences

- A profile with `tls.realEch` sends one HTTPS query per new host and
  port (per Transport, then by TTL), through `Config.Resolver`, beside
  its own A and AAAA lookups (Go's resolver asks both, where Chrome asked
  A and HTTPS and Firefox HTTPS and A): only the resolver sees the
  difference. The query is plain DNS to the system's nameservers, where a
  browser set to secure DNS would use DoH. `Config.DialContext` does not
  carry it; `Config.Resolver.Dial` does. With `DialContext` set and no
  `Resolver.Dial`, nothing is looked up and every server gets GREASE, as
  through a proxy: a lookup that bypassed the caller's dialer could reveal
  the hosts over another path.
- The handshake waits for the lookup: a slow resolver delays each new
  host's first connection up to 5 s, once per 30 s for a failing one.
- There is no option to turn it off: a browser has none. A caller who
  wants no lookups uses a profile without the section
  (`p.With(func(s *profile.Spec) { s.TLS.RealECH = nil })`), which sends
  GREASE everywhere.
- Hosts whose record names HTTP/3 (`cloudflare-ech.com`'s, 2026-11-10:
  alpn `h3,h2` and an `ech` config) get TCP with ECH from mimicry where
  the browsers use QUIC with ECH; listed in EVIDENCE.md's Not covered.
- Real Chrome sends ECH only when its resolver path returns HTTPS
  records (secure DNS in the captures); on getaddrinfo it sends GREASE.
  mimicry always looks up: what Chrome with secure DNS does.
- Profiles without an `ech` capture (`chrome-156-*`, `chrome-155-macos`,
  Firefox 158, Safari 26, the iPhone profiles) keep GREASE; refreshing
  them through `ech` derives their section.
- Decision 0026's "Revisit when" named browsers discovering HTTP/3
  through HTTPS records; the trial above shows they do with an `h3`
  alpn. That is a capture of HTTP/3 discovery, not of what 0026 decided
  about recording QUIC, and is left unmodelled here.

## Revisit when

- A vendor is seen checking QUIC with ECH, or callers notice sites served
  over TCP where the browsers use HTTP/3: HTTP/3 discovery from HTTPS
  records, and the QUIC inner hello (which needs `quictrace` to find the
  keys of an ECH-accepted connection), would then be captured and
  modelled.
- A capture through a proxy shows a browser looking up HTTPS records and
  sending ECH there.
- Firefox's choice between `retryPlain` and offering the config again is
  explained by more samples.
- A browser changes its KEMs, suites, inner hello or retry behaviour: the
  next quick refresh's `ech` capture shows it, and hellogen refuses what
  the section cannot describe.
