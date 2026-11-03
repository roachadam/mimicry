// Package platform says which profiles share a browser's network stack, so
// that a profile may inherit captures from the other's: Safari on iOS from
// Safari on macOS (decision 0051), Chrome on iOS from Safari of its WebKit
// (decision 0053). cmd/profilegen enforces it on the built-in profiles and
// cmd/refresh plans by it; it stays out of the public profile package,
// whose Validate checks only that an inherited capture is the same
// browser's, or Safari's for Chrome on iOS.
package platform

import "regexp"

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

// Engine is the browser whose engine and network stack browser uses on
// platform, from whose profiles a profile of browser on platform may also
// inherit captures, "" for none: Safari for Chrome on iOS, which loads
// pages through WebKit and Apple's network stack. Such a profile inherits
// only from the engine's profiles of its WebKit generation
// (WebKitVersion), on its own platform or the one the engine's network
// stack is shared with (Stack).
func Engine(browser, platform string) string {
	if browser == "chrome" && platform == "ios" {
		return "safari"
	}

	return ""
}

// iosVersion is the iOS major version in an iPhone or iPad user agent.
var iosVersion = regexp.MustCompile(`\bCPU (?:iPhone )?OS ([0-9]+)_`)

// WebKitVersion is the Safari version whose WebKit a browser with Engine
// "safari" runs, from its user agent: the iOS major version, which the
// system's WebKit and Safari share ("27" for "iPhone OS 27_2_0"). "" when
// the user agent names no iOS version.
func WebKitVersion(userAgent string) string {
	m := iosVersion.FindStringSubmatch(userAgent)
	if m == nil {
		return ""
	}

	return m[1]
}
