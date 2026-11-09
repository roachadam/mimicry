# Saving an identity

An identity is what one browser profile would accumulate: cookies, the
HTTP/3 servers sites advertised, each site's client hints opt-in, and TLS
session tickets. Each lives in a store you can share between the
Transports of one identity, like a browser's tabs, and save to carry the
identity to another run or another process.

| Store | `Config` field | Save and load | What a browser keeps across a restart |
|---|---|---|---|
| `*cookies.Jar` | `Cookies` | `WriteJSON` / `ReadJSON` (also Netscape) | Persistent cookies; session cookies go (`ClearSession`) |
| `*AltSvcCache` | `AltSvc` | `WriteJSON` / `ReadJSON` | Kept ([HTTP/3](http3.md)) |
| `ClientHintStore` | `ClientHintStore` | `WriteJSON` / `ReadJSON` (`MemoryClientHintStore`) | Kept ([client hints](client-hints.md)) |
| `*TicketCache` | `Tickets` | `WriteJSON` / `ReadJSON` | Chrome 156 and Firefox 159: nothing. Safari 27: its QUIC sessions ([0062](../decisions/0062-saved-tls-tickets.md)) |

```go
chrome := profiles.ChromeMacOS()

jar, err := cookies.New(cookies.Config{
	Rules: chrome.Spec().Cookies,
})
if err != nil {
	log.Fatal(err)
}

alts, hints, tickets := mimicry.NewAltSvcCache(), mimicry.NewClientHintStore(), mimicry.NewTicketCache()

client, err := mimicry.NewClient(mimicry.Config{
	Profile:         chrome,
	Cookies:         jar,
	AltSvc:          alts,
	ClientHintStore: hints,
	Tickets:         tickets,
})

// Later, when the work moves elsewhere: write each store to its own file
// (jar.WriteJSON, alts.WriteJSON, hints.WriteJSON, tickets.WriteJSON),
// and read them into the new process's stores before its first request.
```

## Tickets

A loaded `TicketCache` lets the new process resume its first connections,
over TCP and HTTP/3 (with 0-RTT where the profile's browser sends it),
where it would otherwise make full handshakes. For Chrome and Firefox
that is what one browser that stayed open does, so use it to move a live
identity between processes or workers; to look like a relaunched Chrome
or Firefox, load the other stores and leave the tickets out. A relaunched
Safari does resume its QUIC sessions, with 0-RTT, so for Safari profiles
a loaded cache fits either. (Safari never resumes over TCP.)

- **One process at a time.** A TLS 1.3 ticket is used once, as the browser
  uses it. A file loaded into two processes would resume one ticket twice,
  which no browser does and a server can see.
- **Keep the file like a password, and read only files you wrote.** A
  ticket's state holds the secret a resumed connection is keyed from, and
  that secret, not the server's certificate, is what authenticates the
  server on resumption. Whoever can read the file can decrypt those
  connections; whoever can write it can stand in for every server it
  names.
- **Proxies.** A ticket received through a proxy is filed under a hash of
  the proxy's URL, credentials included, never the URL itself. It resumes
  only through the same proxy URL. A weak proxy password can be guessed
  from the hash by someone who also knows the proxy's host and user name.
- **One identity per cache.** A ticket links the connections that use it,
  as a cookie does. A cache belongs to one browser: `NewTransport` refuses
  a profile of another browser, and `ReadJSON` another browser's file;
  newer versions of the same browser may use it. Don't share a cache
  between identities.
- **Rotating proxies.** A proxy mimicry has forgotten (see
  [proxies](proxies.md), `Config.MaxIdleProxies`) loses its tickets from
  the cache too, so a cache stays bounded however many proxies pass
  through it.
- **Expiry.** TLS 1.3 tickets expire with their lifetime (7 days at most)
  or their server certificate, whichever is first; TLS 1.2 tickets with
  the certificate. Expired ones are neither written nor loaded.
- **Client certificates.** A ticket from a session that sent a client
  certificate is marked so (`clientCert`): a connection resuming it,
  in this process or the next, carries that certificate's identity and
  takes no other host's requests ([client certificates](client-certificates.md)).
- **Versions.** The session encoding is uTLS's and may change between
  mimicry versions; `ReadJSON` then fails, and the identity starts with
  full handshakes.

## What is not saved

Open connections, CORS preflight results, the last navigation (the page
later requests come from), which origins failed over QUIC, QUIC address
validation tokens and round-trip estimates. A new Transport starts these
afresh, as a browser does after a restart.

Background: decisions [0013](../decisions/0013-tls-resumption.md),
[0062](../decisions/0062-saved-tls-tickets.md).
