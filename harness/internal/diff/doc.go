// Package diff compares two captures of one connection, for example a browser
// capture and what mimicry sends: the ClientHello, the HTTP/2 preface and the
// first request's headers, reporting each difference with its path. The rest
// of a profile, derived from page traces, is not compared. Values that are
// random by design never reach a profile (GREASE is a placeholder, keys and
// server names are generated), so the comparison is exact except where the
// profile says the browser shuffles its extensions.
//
// Imports: mimicry's profile package and the standard library.
package diff
