// Package diff compares two profiles, for example a browser capture and what
// mimicry sends, and reports every difference with its path. Values that are
// random by design never reach a profile (GREASE is a placeholder, keys and
// server names are generated), so the comparison is exact except where the
// profile says the browser shuffles its extensions.
//
// Imports: mimicry's profile package and the standard library.
package diff
