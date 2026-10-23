package capture

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

// Capture is what the capture server recorded from one connection.
type Capture struct {
	// Profile holds the ClientHello and, once a request arrived, HTTP/2 and
	// headers. Name, Browser, Version and Platform are filled in by Identify.
	Profile profile.Profile
	// ClientHello is the raw ClientHello handshake message, kept for debugging
	// anything the profile schema does not describe.
	ClientHello []byte
	// Protocol is the negotiated ALPN protocol, empty if the handshake failed.
	Protocol string
	// Trace is the HTTP/2 connection's frames and header blocks, nil for
	// HTTP/1.1.
	Trace *h2trace.Trace
}

// Complete reports whether a request was recorded, not only the ClientHello.
func (c *Capture) Complete() bool {
	return len(c.Profile.Headers) > 0
}

// HasExtension reports whether the ClientHello carried an extension of typ.
func (c *Capture) HasExtension(typ profile.Code) bool {
	for _, e := range c.Profile.TLS.Extensions {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// UserAgent returns the request's user-agent header, or "".
func (c *Capture) UserAgent() string {
	for _, h := range c.Profile.Headers {
		if h.Name == "user-agent" {
			return h.Value
		}
	}
	return ""
}

// Identify fills in the profile's name, browser, major version and platform
// from the user agent. Unrecognised values become "unknown".
func (c *Capture) Identify() {
	browser, version, platform := ParseUserAgent(c.UserAgent())
	c.Profile.Browser = browser
	c.Profile.Version = version
	c.Profile.Platform = platform
	c.Profile.Name = browser + "-" + version + "-" + platform
}

// ParseUserAgent extracts the browser, its major version and the platform from
// a user agent string.
func ParseUserAgent(ua string) (browser, version, platform string) {
	browser, version = "unknown", "unknown"
	switch {
	case strings.Contains(ua, "Firefox/"):
		browser, version = "firefox", majorAfter(ua, "Firefox/")
	case strings.Contains(ua, "Edg/"):
		browser, version = "edge", majorAfter(ua, "Edg/")
	case strings.Contains(ua, "Chrome/"):
		browser, version = "chrome", majorAfter(ua, "Chrome/")
	case strings.Contains(ua, "Safari/") && strings.Contains(ua, "Version/"):
		browser, version = "safari", majorAfter(ua, "Version/")
	}
	platform = "unknown"
	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		platform = "ios"
	case strings.Contains(ua, "Android"):
		platform = "android"
	case strings.Contains(ua, "Macintosh"):
		platform = "macos"
	case strings.Contains(ua, "Windows"):
		platform = "windows"
	case strings.Contains(ua, "CrOS"):
		platform = "chromeos"
	case strings.Contains(ua, "Linux"), strings.Contains(ua, "X11"):
		platform = "linux"
	}
	return browser, version, platform
}

// majorAfter returns the digits that follow marker in ua, up to the first dot.
func majorAfter(ua, marker string) string {
	_, rest, _ := strings.Cut(ua, marker)
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return "unknown"
	}
	return rest[:end]
}

// ErrExists is returned by Write when a capture with the same name exists.
var ErrExists = errors.New("capture already exists")

// Write stores c in dir as <name>.json (the profile), <name>.clienthello (the
// raw ClientHello as hex) and, for HTTP/2, <name>.h2trace.json (the
// connection trace). It never overwrites: an existing capture returns
// ErrExists. It returns the path of the profile file.
func Write(dir string, c *Capture) (string, error) {
	if c.Profile.Name == "" {
		return "", errors.New("capture has no name; call Identify first")
	}
	data, err := profile.Encode(&c.Profile)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, c.Profile.Name+".json")
	if err := writeNew(path, data); err != nil {
		return "", err
	}
	hello := []byte(hex.EncodeToString(c.ClientHello) + "\n")
	if err := writeNew(filepath.Join(dir, c.Profile.Name+".clienthello"), hello); err != nil {
		return "", err
	}
	if c.Trace != nil {
		trace, err := json.MarshalIndent(c.Trace, "", "  ")
		if err != nil {
			return "", err
		}
		if err := writeNew(filepath.Join(dir, c.Profile.Name+".h2trace.json"), append(trace, '\n')); err != nil {
			return "", err
		}
	}
	return path, nil
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s: %w", path, ErrExists)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
