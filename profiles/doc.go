// Package profiles holds the built-in browser profiles, one embedded JSON file
// per browser version and platform, each taken from a capture of the real
// browser. Every accessor returns a fresh copy, so changing one never affects
// another caller.
//
// The accessors are generated from data/*.json by cmd/profilegen
// (go generate ./profiles).
//
// Imports: profile and the standard library.
package profiles
