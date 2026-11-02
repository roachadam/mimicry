// Package checked hands the root package the Spec inside a profile.Profile,
// which package profile keeps unexported so that no caller can change a
// profile clients were built from. Package profile sets Spec as it is
// initialised; this package imports nothing, so that profile, otherwise
// standard library only, may import it.
package checked

// Spec is a func(profile.Profile) (*profile.Spec, error): the validated,
// read-only Spec a profile holds, or the problems profile.Profile.With
// found in it, or an error for the zero Profile. It is an any because
// this package cannot import profile, which imports it, to name the type.
var Spec any
