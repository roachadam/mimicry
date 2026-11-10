package capture

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// Browsers open several connections; only the first capture of an identity is
// kept, and Write must never overwrite it.
func TestWriteNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	c := &Capture{
		Profile: profile.Spec{
			Name: "chrome-155-macos",
			Headers: []profile.Header{
				{
					Name:  "user-agent",
					Value: "first",
				},
			},
		},
		ClientHello: []byte{
			1,
			2,
		},
	}

	path, err := Write(dir, c)
	if err != nil {
		t.Fatal(err)
	}

	if path != filepath.Join(dir, "chrome-155-macos.json") {
		t.Errorf("path %s", path)
	}

	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	hello, err := os.ReadFile(filepath.Join(dir, "chrome-155-macos.clienthello"))
	if err != nil {
		t.Fatal(err)
	}

	if string(hello) != "0102\n" {
		t.Errorf("clienthello file %q, want hex of the raw message", hello)
	}

	c.Profile.Headers[0].Value = "second"
	if _, err := Write(dir, c); !errors.Is(err, ErrExists) {
		t.Fatalf("second Write error = %v, want ErrExists", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(after) != string(first) {
		t.Error("second Write changed the existing capture")
	}
}

// A capture is named from its user agent: Safari on an iPhone must not take
// the macOS profile's name, whose Safari version it shares, and Chrome and
// Firefox on an iPhone, which say CriOS/ and FxiOS/ and no Chrome/ or
// Firefox/, must be recognised.
func TestParseUserAgent(t *testing.T) {
	for _, tc := range []struct {
		ua                         string
		browser, version, platform string
	}{
		{
			// Safari 27.2 on iOS 27.2, captured 2026-11-01: iOS keeps
			// saying 18_7 in the user agent.
			ua:       "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Mobile/15E148 Safari/604.1",
			browser:  "safari",
			version:  "27",
			platform: "ios",
		},
		{
			ua:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.0.1 Safari/605.1.15",
			browser:  "safari",
			version:  "27",
			platform: "macos",
		},
		{
			// Chrome 157.0.8103.24 on iOS 27.2, captured through devicectl.
			ua:       "Mozilla/5.0 (iPhone; CPU iPhone OS 27_2_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/157.0.8103.24 Mobile/15E148 Safari/604.1",
			browser:  "chrome",
			version:  "157",
			platform: "ios",
		},
		{
			// Firefox 159.1 on iOS 27.2, captured through devicectl: Safari's
			// frozen 18_7.
			ua:       "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/159.1 Mobile/15E148 Safari/604.1",
			browser:  "firefox",
			version:  "159",
			platform: "ios",
		},
	} {
		c := &Capture{
			Profile: profile.Spec{
				Headers: []profile.Header{
					{
						Name:  "user-agent",
						Value: tc.ua,
					},
				},
			},
		}
		c.Identify("")

		want := tc.browser + "-" + tc.version + "-" + tc.platform
		if c.Profile.Browser != tc.browser || c.Profile.Version != tc.version || c.Profile.Platform != tc.platform || c.Profile.Name != want {
			t.Errorf("%s: identified as %s (%s, %s, %s), want %s", tc.ua, c.Profile.Name, c.Profile.Browser, c.Profile.Version, c.Profile.Platform, want)
		}
	}
}
