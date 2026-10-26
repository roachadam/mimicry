// Package mimicry is an HTTP client that reproduces real browsers' network
// fingerprints: the TLS ClientHello, HTTP/2 frames and header order,
// HTTP/1.1 header lines, and HTTP/3 over QUIC where the browser moves an
// origin to it (Config.DisableHTTP3 keeps everything on TCP).
//
// The core is Transport, an http.RoundTripper, so mimicry works anywhere an
// *http.Client does. Profiles describe each browser and live in the profile and
// profiles packages.
//
// A request is a top-level navigation unless it says otherwise: browsers
// send scripts, images, fonts, fetch() calls and the rest with headers, an
// order and a priority of their own, and compute sec-fetch-site, Origin and
// Referer from the page making the request. As and With say how a request is
// made; the profile supplies the rest:
//
//	page, _ := http.NewRequest("GET", "https://example.com/", nil)
//	client.Do(page) // a navigation, as typed into the address bar
//	api, _ := http.NewRequest("GET", "https://api.example.com/v1", nil)
//	client.Do(mimicry.As(api, mimicry.Fetch)) // fetch() from the last page
//
// Defaults reproduce the browser exactly, and every change is the caller's,
// in order of precedence: the profile's defaults for the kind, then what the
// request's page decides (sec-fetch-site, Origin, Referer, credentials),
// then Config.Headers, then the request's own headers (a header set to nil
// suppresses a default; HeaderOrderKey sets the order, dropping defaults it
// does not name), then Config.HeaderPlacement and Request.HeaderPlacement,
// which move one header next to another and keep the rest of the
// browser's order. Exact bypasses the profile's headers altogether. Asking
// for what the profile never measured (a fetchpriority for a kind, a kind a
// profile lacks) is an error wrapping ErrNotMeasured, not a guess.
//
// A Transport is one browsing entity, like one browser tab: requests come
// from its last navigation unless they name their page, so parallel tasks
// use a Transport (or client) each.
//
// Transport.DialWebSocket opens a WebSocket as the profile's browser opens
// one from a page: the handshake as the profile's websocket kind, over
// HTTP/2 extended CONNECT where the server allows it or an HTTP/1.1
// connection of its own, and every frame, compression, pong and close as
// the browser sends them.
//
// Config.Pins pins hosts to keys (FetchPins and cmd/pins list a site's),
// and Config.VerifyConnection checks every connection, resumed ones
// included; responses carry their connection's TLS state. A proxy that
// fails a request returns a *ProxyError. Neither changes a byte sent.
//
// Imports: the standard library, profile, cookies, internal packages,
// clockwork and the decompressors. Only internal/engine/native assembles
// the request path.
package mimicry
