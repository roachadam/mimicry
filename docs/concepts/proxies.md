# Proxies

Set `Config.Proxy` to send requests through an HTTP or SOCKS5 proxy. It takes
the same function as `http.Transport.Proxy`:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome155MacOS(),
	Proxy:   http.ProxyURL(proxyURL), // or http.ProxyFromEnvironment
})
```

Returning a nil URL sends that request directly.

## What each side sees

The server sees exactly what it would without the proxy: the profile's
ClientHello, HTTP/2 settings and headers, tunnelled byte for byte.

The proxy sees what the profile's browser sends a proxy, from captures
([decision 0014](../decisions/0014-proxies.md)):

- `http://` proxy, `https` request: a CONNECT tunnel with the browser's
  headers (Chrome: `Host`, `Proxy-Connection`, `User-Agent`; Firefox:
  `User-Agent`, `Proxy-Connection`, `Connection`, `Host`; Safari: `Host`,
  `Proxy-Connection`, `Connection`).
- `http://` proxy, `http` request: the request itself in absolute form
  (`GET http://host/path HTTP/1.1`), Chrome's with `Proxy-Connection` in place
  of `Connection`.
- `socks5://` or `socks5h://` proxy: the browsers' SOCKS5 handshake, offering
  no authentication and sending the host name for the proxy to resolve. Both
  schemes behave alike.

## Credentials

Put them in the URL (`http://user:pass@proxy:3128`). They are used the way
the profile's browser uses them:

- Chrome and Firefox: the first request to a proxy goes without, the
  proxy's 407 is answered on the same connection, and every later request
  to that proxy sends them unasked.
- Safari: every CONNECT goes without and is answered after its 407 (on the
  same connection, in a different header order); plain-HTTP requests carry
  them from the first.

Only Basic authentication is supported. SOCKS5 credentials, which no
browser sends, are offered when the URL has them; that part is visibly
yours.

## Connections

Each proxy URL has its own connections and TLS sessions; nothing is shared
with other proxies or with direct requests. `Config.DialContext`, when set,
opens the connection to the proxy.

## Limits

- `https://` proxies (TLS to the proxy) are not supported.
- Plain `http://` requests carry an HTTPS request's headers; browsers send
  non-secure origins fewer (no `Sec-Fetch-*`, smaller `Accept-Encoding`). Not
  modelled yet, with or without a proxy.
- Chrome's speculative extra connections to the proxy are not made.
