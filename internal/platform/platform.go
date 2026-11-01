// Package platform says which profile platforms share a browser's network
// stack, so that a profile on one may inherit captures from the other's
// (decision 0051). cmd/profilegen enforces it on the built-in profiles and
// cmd/refresh plans by it; it stays out of the public profile package,
// whose Validate checks only that an inherited capture is the same
// browser's.
package platform

// Stack is the platform whose network stack browser shares on platform,
// from whose profiles a profile of browser on platform may also inherit
// captures (profile.Profile.CapturesFrom), "" for none: macOS for Safari
// on iOS.
func Stack(browser, platform string) string {
	if browser == "safari" && platform == "ios" {
		return "macos"
	}

	return ""
}
