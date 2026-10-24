# Plain HTTP: what Chrome 155, Firefox 158 and Safari 27/26 send to a non-secure origin, 2026-10-24

The kinds page served over plain HTTP on every host (`kinds-plain`: the
same requests as `kinds` and `kinds-http1`, `http://` instead of
`https://`), three samples per browser; Safari 26 on the second Mac. Hosts
are under `.test`, so not potentially trustworthy.

## Compared with the same requests over HTTPS (HTTP/1.1)

79 requests per browser, matched by step.

| | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| Dropped | `sec-ch-ua`, `sec-ch-ua-mobile`, `sec-ch-ua-platform`, every `Sec-Fetch-*` (`Sec-Fetch-Storage-Access` included) | every `Sec-Fetch-*` | every `Sec-Fetch-*` |
| `Accept-Encoding` | `gzip, deflate` (was `gzip, deflate, br, zstd`) | the same (font loads keep `identity`) | the same |
| Added | none | none | `Upgrade-Insecure-Requests` on frame navigations |
| Order of what remains | moves `Origin` after `User-Agent` on CORS requests (18 of 79) | unchanged | changed on most requests |

So a non-secure request is its own header list, not the secure one minus a
few names: mimicry keeps it as captured, per kind and condition
(`insecure` variants, HTTP/1.1 only, since no browser speaks HTTP/2 without
TLS).

Cookies follow RFC 6265bis: the page's `Secure` cookies are neither set
from nor sent to `http://`.

## Not modelled

- Safari tries TLS on the same host and port first (its automatic HTTPS
  upgrade), then falls back to plain HTTP: the capture server sees a
  ClientHello on the plain port before each page. mimicry sends the plain
  request directly.
- Chrome's HTTPS-Upgrades try `https://` on port 443 first for `http://`
  URLs on port 80; on other ports (as captured) it does not. Not measured.
- Loopback hosts (`localhost`, `*.localhost`, `127.0.0.0/8`, `::1`) are
  potentially trustworthy by the Secure Contexts specification, so plain
  requests to them keep the secure lists. Taken from the specification,
  not captured.
