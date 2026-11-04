package profiles

import (
	"math/rand/v2"
	"reflect"
	"sync"

	"github.com/roachadam/mimicry/profile"
)

// machines are the built-in profiles' captured machines by platform, each
// once, in the order of the profiles' names.
var machines = sync.OnceValue(func() map[string][]profile.Machine {
	out := map[string][]profile.Machine{}

	for _, name := range names {
		spec := load(name).Spec()
		if spec.ClientHints == nil {
			continue
		}

		m := spec.ClientHints.Machine
		known := out[spec.Platform]

		dup := false
		for _, k := range known {
			if reflect.DeepEqual(k, m) {
				dup = true

				break
			}
		}

		if !dup {
			out[spec.Platform] = append(known, m)
		}
	}

	return out
})

// Machines returns the machines the built-in profiles were captured on
// for platform ("macos", "windows"), for Config.ClientHints: the values a
// browser on each sends once a site opts in to client hints. Each is one
// real machine's, so its values agree with each other; mixing values from
// several is a tell. Empty for a platform whose profiles send no opted-in
// hints. The slice is a copy.
func Machines(platform string) []profile.Machine {
	known := machines()[platform]
	out := make([]profile.Machine, len(known))

	for i, m := range known {
		m.FormFactors = append([]string(nil), m.FormFactors...)
		out[i] = m
	}

	return out
}

// RandomMachine returns one of Machines(platform), picked at random, for a
// client that should not share its profile's captured machine; ok is
// false when there is none. Pick once per client, as a browser keeps its
// machine: a machine that changes between requests is a tell.
func RandomMachine(platform string) (m profile.Machine, ok bool) {
	known := Machines(platform)
	if len(known) == 0 {
		return profile.Machine{}, false
	}

	return known[rand.IntN(len(known))], true //nolint:gosec // picking a machine needs no secrecy
}
