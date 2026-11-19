# Scenarios

Scenarios (`internal/server/scenario.go`) capture the same browser under other server behaviour.

- Choose them with `-scenarios`. The default is all but replays.
- The behaviours: server-set cookies, unusual SETTINGS, a slow download, an idle connection, and HTTP/1.1 only.
- Add a scenario when a behaviour might depend on the server.
- Reuse the default page wherever possible.

## What a scenario writes

- Each scenario writes `<name>.<scenario>.h2trace.json`.
- Its first request is compared with the default page's.
- Any change is logged as a finding.

## HTTP/1.1 only

The `http1` scenario offers only `http/1.1` in ALPN.
It writes `<name>.http1.h1trace.json` instead.
Per sample, that file holds:

- every request of the page load, across the browser's connections
- each request's request line and header lines, verbatim
- the connection each request came on

The HTTP/1.1 recorder rejects a line that does not end in CRLF.
It does not record such a line as if it did.

## Upload scenarios

The upload scenarios (`http1-upload`, `h2-upload`) keep every TLS record the client sends, not only the first 16:

- **HTTP/1.1**: per request, its head and body lengths, and the records that carried it. The records appear as runs of one size, with their arrival times.
- **HTTP/2**: per connection, every record, with the frames starting in it.

Their traces are gzipped (`.h1trace.json.gz`, `.h2trace.json.gz`).
See [decision 0052](../decisions/0052-http1-upload-records.md).

## HelloRetryRequest

The `hrr` scenario's page fetches three times from `www.hrr-b.test`.

- The server answers every connection to that host with a HelloRetryRequest.
- It builds the HelloRetryRequest raw, since crypto/tls sends no cookie.
- The HelloRetryRequest names the first group the hello lists without a key share, and carries a 32-byte cookie.
- The server records the second ClientHello as the connection's `retry`, then closes the connection.
- So each fetch fails, and browsers retry on fresh connections.
- Firefox also retries with fallback hellos (no `compress_certificate`).
- Every one of these connections counts.

## ECH scenarios

`ech` and `ech-h3` (`internal/server/ech.go`) are both quick scenarios.

### DNS and ports

They are the only scenarios whose hosts the browser looks up in DNS (`Scenario.ResolvesHosts`).
For every other scenario, the browser maps the hosts itself.
So the browser sees their HTTPS records.

Two resolvers answer:

- **DNS stub**: the capture tool's, over UDP on port 15353.
- **DoH endpoint**: `dnsstub.DoH`, named `doh.mimicry.test`. The browser maps that name to the server's address.

Both answer:

- every `*.test` name with the server's address
- the hosts below with an HTTPS record: alpn `h2` and an ECH config list (`server.ECHRecord`)

The keys are derived from each config's ID.
So every run, and the e2e tests' DNS stub, publish the same configs.

Both pages are on https://'s default port.

- The tool forwards TCP 443 to the TLS port.
- On Linux, port 443 (TCP, and UDP for `ech-h3`) needs `net.ipv4.ip_unprivileged_port_start` at 443 or below.
- `cmd/refresh` lowers it in WSL, as for every scenario on the default port.

### How each browser looks the hosts up

`browser.Browser.UsesDoH` decides.

- **Chrome, Edge and Brave** use secure DNS.
  - Setting: `dns_over_https` mode `secure` with the DoH endpoint's template, written to the throwaway profile's Local State. It is recorded as a capture condition.
  - Reason: Chrome on macOS with `/etc/resolver/test` resolves through the system's getaddrinfo, which asks for no HTTPS record (Chrome 157: A and AAAA only, then GREASE ECH). With secure DNS, Chrome asks its DoH server and uses the configs.
  - On Windows, a `DnsOverHttpsMode` policy overrides this ([Refreshing profiles](../refreshing-profiles.md#the-windows-machine)).
- **Firefox on macOS** asks the system's resolver.
  - `/etc/resolver/test` sends the queries to the DNS stub.
  - The tool refuses to start without that file (`dnsstub.ResolverFile`).
- **Firefox on Windows and Linux** resolves through its own DoH client alone, since nothing there sends `*.test` to the stub. These prefs are set in the throwaway profile and recorded as a capture condition:
  - `network.trr.mode` 3
  - `network.trr.uri`: the DoH endpoint, without `{?dns}` (Firefox posts its queries)
  - `network.trr.bootstrapAddr`: the server's address
  - `network.trr.allow-rfc1918` (for its loopback answers)
  - `network.trr.confirmationNS` `skip`
- **Safari** asks the system's resolver, as for every scenario.

### What is recorded

- **DNS queries**: the page records every query it answers (`dns`): time, name, type, and `via` (`system` or `doh`).
- **ECH**: the server decrypts each ClientHelloOuter for a config it holds the key of (crypto/tls's `EncryptedClientHelloKeys`). It records the connection's `ech`:
  - config ID
  - HPKE suite
  - whether it accepted
  - whether it sent retry configs
  - the decrypted inner hello, parsed, with its padding and the extensions `ech_outer_extensions` copies

### The ech page

`ech` fetches the hosts below, one after another.
Each response closes its connection, so the next request opens a new one.

- `www.ech.test`: a config the server decrypts. The second connection resumes inside ECH.
- `www.ech-retry.test`, twice: a config the server holds no key for, rejected with retry configs. This shows what follows, and whether the next connection keeps them.
- `www.ech-noretry.test`: rejected by a public name that sends no retry configs.
- `www.ech-bad.test`: a list that does not parse.
- `www.ech-suite.test`: a P-256 config ahead of an X25519 one that lists AES-256-GCM first.

### The ech-h3 page

`ech-h3` is `www.ech.test` alone, five fetches.
It shows whether the browser moves a host whose ECH config it uses to HTTP/3.

- Every response advertises `Alt-Svc: h3=":443"`.
- A QUIC server on UDP 443 holds the same keys.
- Why 443: Chrome 157 took no `Alt-Svc` naming the TLS port's high-numbered UDP twin for an origin on 443.

How each browser takes it:

- **Chrome and Firefox** take it with `-h3-local-roots` only.
- **Safari** does not take it. It ignores ECH configs, and its `Alt-Svc` cache for :443 outlives a sample.

### How the captures are used

- hellogen derives `tls.realEch` from `ech`, and its `altSvcIgnored` from `ech-h3` ([decision 0068](../decisions/0068-real-ech.md)).
- `TestECHMatchesBrowser` and `TestECHH3MatchesBrowser` replay them.

## Real-page replays

Replay scenarios load the shape of real pages with synthetic content.
They check what the small capture page cannot:

- hundreds of requests of every kind
- dozens of hosts and connections
- kilobytes of cookies

### Recording a page

1. Record a page with a throwaway Chrome profile (`--log-net-log=FILE --net-log-capture-mode=IncludeSensitive`). Spend 5 s on `about:blank` first, as the capture tool does.
2. Run `go run ./cmd/pageshape -site nike.com -out testdata/pages/nike.com.json FILE`.
3. Delete the log afterwards. It holds real cookies.

Only lengths and character classes leave the log.

### Running replays

- `-scenarios replay-<site>` replays one page.
- `replay` replays all of `testdata/pages`.
- Replays are not part of the default run: each sample takes the page's length, up to 35 s.

### How a page is served

- Each host is served under `.test`, with its site kept apart (`www.nike.com` → `www.nike-com.test`, `server.ReplayHost`).
- Each host has its own certificate from the capture CA.
- Generated documents (the top one and every iframe's) schedule the recorded requests.
- Each request comes from the element or API giving its `sec-fetch-dest` and mode.
- Cookies of the recorded shapes are set, then deleted again at the end.
- HTTP/3 parts of a real page are replayed over HTTP/2.

### The capture

The capture is `<name>.replay-<site>.pagetrace.json.gz`.
It is gzipped: otherwise it would be tens of MiB of JSON.
Per sample, it holds:

- every HTTP/2 connection's trace, with its host
- every HTTP/1.1 request

Its profile keeps only the ClientHello and user agent.
The first request on the connection that ends the page varies with timing.

## The click scenario

The click scenario (`nav-redirect-click`) has the tool click as a user does.
So each navigation has user activation (`Sec-Fetch-User`).
A script's `click()` has none.

- A page waiting for a click takes the title `mimicry-click`.
- It fills its window with its link or submit button.
- The tool clicks its middle once per URL.
- It clicks again after 6 s if the page is still there.

How each browser is clicked:

- **Chrome and Edge**: over the DevTools protocol (`Input.dispatchMouseEvent`), on their throwaway profiles.
- **Firefox**: over WebDriver BiDi (`input.performActions`, with `remote.prefs.recommended` off), on its throwaway profile.
- **Safari**: through the system's mouse events (`CGEventPost`), after its window is brought to the front.
  - This needs Accessibility access for the app running the tool.
  - An ssh session has none. So a Mac driven over ssh (such as the second Mac) cannot take this scenario.
  - The mouse moves to Safari's window: leave the machine alone while it runs.

## URL parsing

`urls` has the browser parse 43 URLs.
Each is fetched (`no-cors`), then loaded in an iframe, 5 s at most each.

### The cases

- **Characters**: a query and a path with each of space, `"`, `'`, `<`, `>`, `` ` ``, `{`, `}`, `|`, `\`, `^`, `é`, a lone `%` and `%41`.
- **Dot segments**: also written as `%2e`.
- **Hosts**:
  - uppercase
  - internationalized (`Bücher.urls.test`)
  - ending in a dot: `dot.urls.test.` (named only so) and `www.urls.test.` (which a browser may pool with `www.urls.test`)
  - on an explicit `:443`
  - on the page's port with a leading zero
  - a malformed punycode label (`xn--a.urls.test`)

### How the page runs

- Each request's path starts with its case: `/c/f-<case>/` or `/c/n-<case>/`.
- The server answers any of them. So whatever the browser makes of the rest arrives as it was sent.
- The page then goes to `http://` on the plain port for the IPv4 forms (`127.1`, `0x7f.1`, `0177.0.0.1`, `2130706433`, `127.0.0.1.`).
- It does so because, from an `https://` page, Safari blocks even loopback `http://`.
- Each half reports the cases that failed at `/report`. This is a hint: the wire is the evidence.
- After that report, the second half asks for `/done`.

### Server side

- The capture tool forwards port 443 to the TLS port for the scenario.
- macOS lets a user bind 443 only on every address. So the tool then lets only loopback peers through.
- Safari sends a ClientHello without SNI for a host ending in a dot. The server answers it with a certificate for both dotted hosts.
- Go's TLS refuses a ClientHello naming a host with its dot, before anything else. The server records one as a connection with `refused` set (`capture.Connection.Refused`).

### How the capture is used

- kindsgen derives `urls` from it ([decision 0057](../decisions/0057-url-parsing-measured.md)).
- `TestURLsMatchBrowser` sends every URL through mimicry and compares.
- Inheriting works only from older versions. So a browser's newest profiles need their own capture.

## Client-certificate scenarios

There are four: `client-cert-ec`, `client-cert-rsa`, `client-cert-ec-tls12` and `client-cert-rsa-tls12`.

- They serve the resume page: new connections to three hosts, sequential and parallel, HTTP/2 and HTTP/1.1.
- The server asks every connection for a client certificate (`RequestClientCert`, naming the run's test client CA).
- The server uses TLS 1.3, or TLS 1.2 at most in the `-tls12` pair.
- The browser holds one identity: an EC P-256 or an RSA-2048 key.
- There is one key per scenario, so nothing is left to choose.

### What each writes

- **The page**: `.pagetrace.json.gz`. Its `connections` leave out failed handshakes.
- **The handshakes**: `<name>.<scenario>.clientauth.json`. Per sample, it lists every TLS connection in handshake order (`capture.ClientAuth`).

Each connection in the handshakes file records:

- its page connection number (0 for a handshake that failed: a client giving up on it)
- its host
- when its ClientHello arrived
- how long the handshake took (`handshakeMs`, where choosing and signing shows)
- version and suite
- what the ClientHello offered to resume (`pskIdentities`, `ticket`), and whether the server resumed it
- the server's CertificateRequest: its `signature_algorithms`, `signature_algorithms_cert` and CAs, as sent
- the client's messages after its ClientHello, in order (`flight`). An empty Certificate is `certificate` with no `certificates`.
- each certificate sent: subject, issuer, key, whether a CA
- the CertificateVerify scheme
- the paths of the requests the connection carried

How the server sees the flights:

- The server reads its own TLS 1.3 key log to decrypt both sides' handshake flights (`internal/tlsflight`).
- TLS 1.2's flights are in the clear up to ChangeCipherSpec.
- A resumed handshake has no CertificateRequest. The server restores the certificate from the ticket.

### The client CA

The identities come from a client CA made for the run (`internal/clientcert`).

- It lives in memory and is forgotten with the run.
- Its name is "mimicry capture client CA (test)".
- It is constrained to client authentication, and to no DNS name or IP address.
- It is never installed as a trust anchor.

### Where the identity goes

The tool puts the scenario's identity where the browser reads identities.
It uses a PKCS#12 file holding the identity and the untrusted CA.
The tool takes the identity out after the scenario, however it ends.

- **Chrome, Edge and Brave on macOS**:
  - A temporary keychain in the user's search list (`mimicry-client-cert-*.keychain-db` in the temporary directory).
  - Its key is open to the browser's code-signing team (`set-key-partition-list`).
  - `AutoSelectCertificateForUrls` entries for the page's hosts go in the browser's policy domain (`defaults write com.google.Chrome`, `com.microsoft.Edge`, `com.brave.Browser`). They are filtered on the CA's name.
  - Without the policy, Chromium asks which identity to send even when one fits.
  - Entries of the user's own stay.
  - Over ssh (the second Mac), these commands run in the logged-in user's session through one-shot launchd jobs.
  - Reason: a keychain unlocked from ssh stays locked to the browser, which then asks for its password on screen (2026-11-08, Edge 157 on macOS 26).
- **Chrome, Edge, Brave and Firefox on Windows**:
  - The user's store (`Cert:\CurrentUser\My`), with the CA among the user's intermediate CAs.
  - For the Chromium browsers, the policy entries (`AutoSelectCertificateForUrls`) under `HKCU\Software\Policies\Google\Chrome`, `Microsoft\Edge` and `BraveSoftware\Brave`.
  - Firefox reads the store through its `osclientcerts` module (`security.osclientcerts.autoload`, its default there).
  - On the PC measured, each handshake Firefox answered with the identity took 7 to 11 s. The page's wait allows for it.
- **Firefox on macOS and Linux**:
  - Its throwaway profile's own NSS database (`pk12util`). So NSS's tools are needed on macOS too: `brew install nss`.
  - `osclientcerts` is off, so no identity of the user's keychain is offered.
- **Chrome, Edge and Brave on Linux**:
  - The throwaway profile's NSS database (HOME is the profile).
  - The policy goes in a managed policy file only root may write: `/etc/opt/chrome/policies/managed/mimicry-client-cert.json`, `/etc/opt/edge/...`, `/etc/brave/policies/managed/...`.
  - The tool refuses the scenarios without that file, and prints it.
  - `cmd/refresh` writes it as root in WSL around the capture step, and removes it after.

Firefox gets `security.default_personal_cert` "Select Automatically": the first matching identity, no dialog.

### Cleanup

The tool takes the identity out when it is interrupted, terminated or loses its ssh session (SIGHUP).
SIGPIPE is ignored, so that writes to a gone terminal do not end it first.

`capture -cleanup` removes whatever a killed run left.
Each step runs whatever the others do.

- **Everywhere**: the PKCS#12 files' temporary directories.
- **macOS**:
  - the one-shot launchd jobs still loaded (`org.mimicry.capture.*`, booted out)
  - the keychains, in the search list or still only in the temporary directory
  - the policy entries
- **Windows**: the identities, the CA and the policy entries. It finds them by the CA's name.
- **Linux**: it names a policy file left for root to remove.

`cmd/refresh` runs it on every Mac a plan captured a client-certificate scenario on, however the run ends.

### Safari

Safari (on macOS and the iPhone) is refused (`Scenario.UnsupportedBy`, `UnsupportedOnIOS`).
Observed on 2026-11-08:

- Safari 27 sent an empty Certificate with the identity in a temporary keychain, an identity preference for the host, and the CA trusted.
- With the identity in the login keychain, it stalled, probably on a hidden prompt.
