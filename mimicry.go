// Package mimicry is an HTTP client that reproduces real browsers' network
// fingerprints: the TLS ClientHello, HTTP/2 frames and header order, and later
// HTTP/3.
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
// suppresses a default; HeaderOrderKey reorders). Exact bypasses the profile's
// headers altogether. Asking for what the profile never measured (a
// fetchpriority for a kind, a kind a profile lacks) is an error, not a guess.
//
// Imports: the standard library, profile, and internal packages. Only
// internal/engine/native assembles the request path.
package mimicry
