// Package mimicry is an HTTP client that reproduces real browsers' network
// fingerprints: the TLS ClientHello, HTTP/2 frames and header order, and later
// HTTP/3.
//
// The core is Transport, an http.RoundTripper, so mimicry works anywhere an
// *http.Client does. Profiles describe each browser and live in the profile and
// profiles packages.
//
// Imports: the standard library, profile, and internal packages. Only
// internal/engine/native assembles the request path.
package mimicry
