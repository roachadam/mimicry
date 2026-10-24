// Package profile defines a browser profile, split by concern: TLS, HTTP/2,
// HTTP/1.1, headers by request kind, cookies, proxies, redirects and
// connection sharing. It loads and validates profiles from JSON, the same
// format the harness writes browser captures in.
//
// Imports: the standard library only, so users can load profiles without the rest
// of mimicry.
package profile
