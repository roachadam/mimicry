// Package profiles holds the built-in browser profiles, one embedded JSON file
// per browser version and platform, each taken from a capture of the real
// browser. Every accessor returns a fresh copy, so changing one never affects
// another caller.
//
// Chrome, Firefox and Safari return each browser's newest version and move
// to every new version as it is added; the versioned accessors
// (Chrome155MacOS) keep one fixed. The newest five versions of each browser
// on each platform are supported (Supported); older ones stay, deprecated,
// and are no longer refreshed.
//
// The accessors are generated from data/*.json by cmd/profilegen
// (go generate ./profiles).
//
// Imports: profile and the standard library.
package profiles
