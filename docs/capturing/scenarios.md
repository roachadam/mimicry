# Scenarios

Scenarios (`internal/server/scenario.go`, `-scenarios`, default all but
replays) capture the same browser under other server behaviour:
server-set cookies, unusual SETTINGS, a slow download, an idle connection,
and HTTP/1.1 only. Each writes `<name>.<scenario>.h2trace.json`; its first
request is compared with the default page's and any change is logged as a
finding. The `http1` scenario offers only `http/1.1` in ALPN and writes
`<name>.http1.h1trace.json` instead: per sample, every request of the page
load across the browser's connections, request line and header lines
verbatim, with the connection each came on. The HTTP/1.1 recorder rejects a
line that does not end in CRLF instead of recording it as if it did. The
upload scenarios (`http1-upload`, `h2-upload`) keep every TLS record the
client sends, not only the first 16: per HTTP/1.1 request, its head and
body lengths and the records that carried it as runs of one size with
their arrival times; per HTTP/2 connection, every record with the frames
starting in it. Their traces are gzipped (`.h1trace.json.gz`,
`.h2trace.json.gz`; [decision 0052](../decisions/0052-http1-upload-records.md)).
The `hrr` scenario's page fetches three times from `www.hrr-b.test`,
whose every connection the server answers with a HelloRetryRequest
(raw, since crypto/tls sends no cookie): the first group the hello lists
without a key share, and a 32-byte cookie. It records the second
ClientHello as the connection's `retry` and closes it, so each fetch fails
and browsers retry on fresh connections, Firefox with fallback hellos
(no `compress_certificate`) too; every one of them counts.
Add a scenario when a behaviour might depend on the server; reuse the
default page wherever possible.

### ECH scenarios

`ech` and `ech-h3` (`internal/server/ech.go`, both quick scenarios) are
the only scenarios whose hosts the browser looks up in DNS instead of
mapping them itself (`Scenario.ResolvesHosts`), so it sees their HTTPS
records. The capture tool's DNS stub (UDP, port 15353) and its
DNS-over-HTTPS endpoint (`dnsstub.DoH`, named `doh.mimicry.test`, which
the browser maps to the server's address) answer every `*.test` name
with that address and the hosts below with an HTTPS record: alpn `h2`
and an ECH config list (`server.ECHRecord`). The keys are derived from
each config's ID, so every run, and the e2e tests' DNS stub, publish the
same configs. Both pages are on https://'s default port (the tool
forwards TCP 443 to the TLS port).

How each browser looks the hosts up, as `browser.Browser.UsesDoH`
decides:

- **Chrome, Edge and Brave** use secure DNS: `dns_over_https` mode
  `secure` with the DoH endpoint's template, written to the throwaway
  profile's Local State, recorded as a capture condition. Chrome on
  macOS with `/etc/resolver/test` resolves through the system's
  getaddrinfo, which asks for no HTTPS record (Chrome 157: A and AAAA
  only, then GREASE ECH); with secure DNS it asks its DoH server and uses
  the configs. On Windows, a `DnsOverHttpsMode` policy overrides this
  ([Refreshing profiles](../refreshing-profiles.md#the-windows-machine)).
- **Firefox on macOS** asks the system's resolver, which
  `/etc/resolver/test` sends to the DNS stub; the tool refuses to start
  without it (`dnsstub.ResolverFile`).
- **Firefox on Windows and Linux** resolves through its own DoH client
  alone, since nothing there sends `*.test` to the stub:
  `network.trr.mode` 3, `network.trr.uri` the DoH endpoint (without
  `{?dns}`: Firefox posts its queries), `network.trr.bootstrapAddr` the
  server's address, `network.trr.allow-rfc1918` (its loopback answers)
  and `network.trr.confirmationNS` `skip`, in the throwaway profile and
  recorded as a capture condition.
- **Safari** asks the system's resolver, as for every scenario.

The page records every query it answers (`dns`: time, name, type, and
`via`, `system` or `doh`), and the server decrypts each ClientHelloOuter
for a config it holds the key of (crypto/tls's
`EncryptedClientHelloKeys`) and records the connection's `ech`: config
ID, HPKE suite, whether it accepted, whether it sent retry configs, and
the decrypted inner hello parsed, with its padding and the extensions
`ech_outer_extensions` copies.

`ech` fetches, one after another, each response closing its connection
so the next request opens a new one: `www.ech.test` (a config the server
decrypts; the second connection resumes inside ECH),
`www.ech-retry.test` twice (a config it holds no key for, rejected with
retry configs: what follows, and whether the next connection keeps
them), `www.ech-noretry.test` (rejected by a public name that sends no
retry configs), `www.ech-bad.test` (a list that does not parse) and
`www.ech-suite.test` (a P-256 config ahead of an X25519 one that lists
AES-256-GCM first). `ech-h3` is `www.ech.test` alone, five fetches, every
response advertising `Alt-Svc: h3=":443"` from a QUIC server on UDP 443
holding the same keys (Chrome 157 took no `Alt-Svc` naming the TLS
port's high-numbered UDP twin for an origin on 443): whether the browser
moves a host whose ECH config it uses to HTTP/3. Chrome and Firefox take
it with `-h3-local-roots` only; Safari does not take it (it ignores ECH
configs, and its `Alt-Svc` cache for :443 outlives a sample). On Linux,
port 443 (TCP, and UDP for `ech-h3`) needs
`net.ipv4.ip_unprivileged_port_start` at 443 or below, which `cmd/refresh`
lowers in WSL as for every scenario on the default port.

hellogen derives `tls.realEch` from `ech` and its `altSvcIgnored` from
`ech-h3` ([decision 0068](../decisions/0068-real-ech.md));
`TestECHMatchesBrowser` and `TestECHH3MatchesBrowser` replay them.

### Real-page replays

Replay scenarios load the shape of real pages with synthetic content, to
check what the small capture page cannot: hundreds of requests of every
kind, dozens of hosts and connections, kilobytes of cookies. Record a page
with a throwaway Chrome profile (`--log-net-log=FILE
--net-log-capture-mode=IncludeSensitive`, 5 s on `about:blank` first, as
the capture tool does), then
`go run ./cmd/pageshape -site nike.com -out testdata/pages/nike.com.json FILE`;
delete the log afterwards, it holds real cookies. Only lengths and
character classes leave it. `-scenarios replay-<site>` replays one page,
`replay` all of `testdata/pages` (not part of the default run: each sample
takes the page's length, up to 35 s).

Each host is served under `.test` with its site kept apart
(`www.nike.com` → `www.nike-com.test`, `server.ReplayHost`) and its own
certificate from the capture CA; generated documents (top and every
iframe's) schedule the recorded requests with the element or API giving
their `sec-fetch-dest` and mode; cookies of the recorded shapes are set and
deleted again at the end. The capture is
`<name>.replay-<site>.pagetrace.json.gz` (gzipped: tens of MiB of JSON
otherwise): per sample, every HTTP/2 connection's trace (with its host) and
every HTTP/1.1 request. Its profile keeps only the ClientHello and user
agent, since the first request on the connection that ends the page varies
with timing. HTTP/3 parts of a real page are replayed over HTTP/2.

### The click scenario

The click scenario (`nav-redirect-click`) has the tool click as a user
does, so each navigation has user activation (`Sec-Fetch-User`; a script's
`click()` has none): a page waiting for a click takes the title
`mimicry-click` and fills its window with its link or submit button, and
the tool clicks its middle once per URL (again after 6 s if it is still
there). Chrome and Edge are clicked over the DevTools protocol
(`Input.dispatchMouseEvent`) and Firefox over WebDriver BiDi
(`input.performActions`, with `remote.prefs.recommended` off), both on
their throwaway profiles. Safari is clicked through the system's mouse
events (`CGEventPost`, after its window is brought to the front), which
needs Accessibility access for the app running the tool; an ssh session has
none, so a Mac driven over ssh (such as the second Mac) cannot take this scenario. The mouse moves to
Safari's window: leave the machine alone while it runs.

### URL parsing

`urls` has the browser parse 43 URLs, each fetched (`no-cors`), then each
loaded in an iframe, 5 s at most each: a query and a path with each of
space, `"`, `'`, `<`, `>`, `` ` ``, `{`, `}`, `|`, `\`, `^`, `é`, a lone
`%` and `%41`; dot segments, as `%2e` too; hosts uppercase,
internationalized (`Bücher.urls.test`), ending in a dot (`dot.urls.test.`,
named only so, and `www.urls.test.`, which a browser may pool with
`www.urls.test`), on an explicit `:443` and on the page's port with a
leading zero, and a malformed punycode label (`xn--a.urls.test`). Each
request's path starts with its case, `/c/f-<case>/` or `/c/n-<case>/`,
and the server answers any of them, so whatever the browser makes of the
rest arrives as it was sent. The page then goes to `http://` on the plain
port for the IPv4 forms (`127.1`, `0x7f.1`, `0177.0.0.1`, `2130706433`,
`127.0.0.1.`): from an `https://` page Safari blocks even loopback
`http://`. Each half reports the cases that failed at `/report` (a hint:
the wire is the evidence) before the second asks for `/done`.

The capture tool forwards port 443 to the TLS port for the scenario
(macOS lets a user bind 443 only on every address; it then lets only
loopback peers through). The server answers a ClientHello without SNI,
which Safari sends for a host ending in a dot, with a certificate for
both dotted hosts, and records one naming a host with its dot, which Go's
TLS refuses before anything else, as a connection with `refused` set
(`capture.Connection.Refused`). kindsgen derives `urls` from it
([decision 0057](../decisions/0057-url-parsing-measured.md)), and
`TestURLsMatchBrowser` sends every URL through mimicry and compares.
Inheriting works only from older versions, so a browser's newest
profiles need their own capture.

### Client-certificate scenarios

`client-cert-ec`, `client-cert-rsa`, `client-cert-ec-tls12` and
`client-cert-rsa-tls12` serve the resume page (new connections to three
hosts, sequential and parallel, HTTP/2 and HTTP/1.1) from a server that
asks every connection for a client certificate (`RequestClientCert`,
naming the run's test client CA), TLS 1.3 or, in the `-tls12` pair, TLS
1.2 at most, while the browser holds one identity: an EC P-256 or an
RSA-2048 key, one per scenario so nothing is left to choose. Besides the
page (`.pagetrace.json.gz`, whose `connections` leave out failed
handshakes), each writes `<name>.<scenario>.clientauth.json`: per sample,
every TLS connection in handshake order (`capture.ClientAuth`): its page
connection number (0 for a handshake that failed: a client giving up on
it), host, when its ClientHello arrived and how long the handshake took
(`handshakeMs`, where choosing and signing shows), version and suite,
what the ClientHello offered to resume (`pskIdentities`, `ticket`) and
whether the server resumed it, the server's CertificateRequest (its
`signature_algorithms`, `signature_algorithms_cert` and CAs, as sent),
the client's messages after its ClientHello in order (`flight`: an empty
Certificate is `certificate` with no `certificates`), each certificate
sent (subject, issuer, key, whether a CA), the CertificateVerify scheme,
and the paths of the requests the connection carried. The server reads
its own TLS 1.3 key log to decrypt both sides' handshake flights
(`internal/tlsflight`); TLS 1.2's are in the clear up to
ChangeCipherSpec. A resumed handshake has no CertificateRequest: the
server restores the certificate from the ticket.

The identities come from a client CA made for the run, in memory, and
forgotten with it (`internal/clientcert`): "mimicry capture client CA
(test)", constrained to client authentication and to no DNS name or IP
address, never installed as a trust anchor. The tool puts the scenario's
identity (a PKCS#12 file holding it and the untrusted CA) where the
browser reads identities, and takes it out after the scenario, however
it ends:

- **Chrome, Edge and Brave on macOS**: a temporary keychain in the user's
  search list (`mimicry-client-cert-*.keychain-db` in the temporary
  directory), its key open to the browser's code-signing team
  (`set-key-partition-list`), plus `AutoSelectCertificateForUrls`
  entries in the browser's policy domain (`defaults write
  com.google.Chrome`, `com.microsoft.Edge`, `com.brave.Browser`) for the page's hosts,
  filtered on the CA's name; without the policy Chromium asks which
  identity to send even when one fits. Entries of the user's own stay.
  Over ssh (the second Mac) these commands run in the logged-in user's
  session through one-shot launchd jobs: a keychain unlocked from ssh
  stays locked to the browser, which then asks for its password on
  screen (2026-11-08, Edge 157 on macOS 26).
- **Chrome, Edge, Brave and Firefox on Windows**: the user's store
  (`Cert:\CurrentUser\My`, the CA among the user's intermediate CAs),
  plus the policy entries for the Chromium browsers under
  `HKCU\Software\Policies\Google\Chrome`, `Microsoft\Edge` and
  `BraveSoftware\Brave`
  (`AutoSelectCertificateForUrls`); Firefox reads the store through its
  `osclientcerts` module (`security.osclientcerts.autoload`, its
  default there). On the PC measured, each handshake Firefox answered
  with the identity took 7 to 11 s; the page's wait allows for it.
- **Firefox on macOS and Linux**: its throwaway profile's own NSS
  database (`pk12util`, so NSS's tools are needed on macOS too: `brew
  install nss`), with `osclientcerts` off so no identity of the user's
  keychain is offered.
- **Chrome, Edge and Brave on Linux**: the throwaway profile's NSS database
  (HOME is the profile), and the policy in a managed policy file only
  root may write (`/etc/opt/chrome/policies/managed/mimicry-client-cert.json`,
  `/etc/opt/edge/...`, `/etc/brave/policies/managed/...`): the tool refuses the scenarios without it and
  prints it; `cmd/refresh` writes it as root in WSL around the capture
  step and removes it after.

Firefox gets `security.default_personal_cert` "Select Automatically": the
first matching identity, no dialog. The tool takes the identity out when
it is interrupted, terminated or loses its ssh session (SIGHUP; SIGPIPE
is ignored so that writes to a gone terminal do not end it first).
`capture -cleanup` removes whatever a killed run left, each step
whatever the others do: everywhere the PKCS#12 files' temporary
directories; on macOS the one-shot launchd jobs still loaded
(`org.mimicry.capture.*`, booted out), the keychains (in the search list,
or still only in the temporary directory) and the policy entries; on
Windows the identities, the CA and the policy entries (it finds them by
the CA's name); on Linux it names a policy file left for root to remove.
`cmd/refresh` runs it on every Mac a plan captured a client-certificate
scenario on, however the run ends. Safari
(on macOS and the iPhone) is refused (`Scenario.UnsupportedBy`,
`UnsupportedOnIOS`): Safari 27 sent an empty Certificate with the
identity in a temporary keychain, an identity preference for the host and
the CA trusted, and stalled with it in the login keychain, probably on a
hidden prompt (2026-11-08).
