// Package h1 speaks HTTP/1.1 over an established connection: Host and
// Connection first, then headers in the profile's order. Header names are
// written in canonical form; browsers' exact HTTP/1.1 casing is not in the
// profile schema yet.
//
// Imports: profile, headerorder and the standard library.
package h1
