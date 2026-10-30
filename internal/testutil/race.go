package testutil

import "runtime/debug"

// RaceEnabled reports whether the test binary was built with -race.
func RaceEnabled() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}

	for _, s := range info.Settings {
		if s.Key == "-race" {
			return s.Value == "true"
		}
	}

	return false
}
