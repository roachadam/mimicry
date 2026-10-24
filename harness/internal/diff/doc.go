// Package diff compares two captures of one connection, for example a browser
// capture and what mimicry sends: the ClientHello, the HTTP/2 preface and the
// first request's headers, reporting each difference with its path. The rest
// of a profile, derived from page traces, is not compared. Values that are
// random by design never reach a profile (GREASE is a placeholder, keys,
// session IDs, ECH's config_id and payload are kept as lengths only, server
// names are generated), so the comparison is exact except where the profile
// says the browser shuffles its extensions, and for GREASE ECH's cipher
// suites and payload lengths, sets the browser picks from per connection.
//
// Imports: mimicry's profile package and the standard library.
package diff
