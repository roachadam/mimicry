// Package mimicry is an HTTP client that reproduces real browsers' network
// fingerprints: TLS ClientHello, HTTP/2 frames and header order, HTTP/1.1
// header lines, and HTTP/3 where the browser uses it. Transport is an
// http.RoundTripper; browser profiles live in the profile and profiles
// packages.
//
// A request is a top-level navigation unless As or With says otherwise; the
// profile supplies the kind's headers, order and priority, and the page
// decides sec-fetch-site, Origin and Referer. Defaults reproduce the browser
// exactly; every deviation is the caller's, and asking for what the profile
// never measured is an error wrapping ErrNotMeasured, not a guess.
//
//	page, _ := http.NewRequest("GET", "https://example.com/", nil)
//	client.Do(page) // a navigation, as typed into the address bar
//	api, _ := http.NewRequest("GET", "https://api.example.com/v1", nil)
//	client.Do(mimicry.As(api, mimicry.Fetch)) // fetch() from the last page
package mimicry
