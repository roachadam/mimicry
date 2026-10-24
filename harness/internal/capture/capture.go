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
	// SampleTraces are the traces of every sample this capture was combined
	// from (Combine), the first being Trace. Nil for a single connection.
	SampleTraces []*h2trace.Trace
	// H1 are the HTTP/1.1 requests of a page load, across its connections;
	// SampleH1 holds every sample's (Combine).
	H1       []H1Request
	SampleH1 [][]H1Request
	// Page is a whole page load across connections and hosts (replay
	// scenarios); SamplePages holds every sample's (Combine).
	Page        *Page
	SamplePages []*Page
	// Proxy is how the client asked the capture proxy for its connections
	// (proxy scenarios); SampleProxy holds every sample's (Combine).
	Proxy       []ProxyRequest
	SampleProxy [][]ProxyRequest
	// Finished reports that the client asked for /done: a browser ran the
	// capture page to the end. A browser's trace without it is truncated.
	Finished bool
	// Scenario names how the server answered (server.Scenario).
	Scenario string
}

// DefaultScenario names the capture page profiles come from.
const DefaultScenario = "page"

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

// Write stores c in dir. For the default scenario it writes <name>.json (the
// profile) and <name>.clienthello (the raw ClientHello as hex). Traces go to
// <name>.h2trace.json and <name>.h1trace.json, JSON arrays with every
// sample's trace (or c's alone); other scenarios write only their traces, as
// <name>.<scenario>.h2trace.json and .h1trace.json, since profiles come from
// the default page. It never overwrites: an existing file returns ErrExists.
// It returns the path of the first file written.
func Write(dir string, c *Capture) (string, error) {
	if c.Profile.Name == "" {
		return "", errors.New("capture has no name; call Identify first")
	}
	prefix := filepath.Join(dir, c.Profile.Name)
	var written []string
	if c.Scenario == "" || c.Scenario == DefaultScenario {
		data, err := profile.Encode(&c.Profile)
		if err != nil {
			return "", err
		}
		if err := writeNew(prefix+".json", data); err != nil {
			return "", err
		}
		hello := []byte(hex.EncodeToString(c.ClientHello) + "\n")
		if err := writeNew(prefix+".clienthello", hello); err != nil {
			return "", err
		}
		written = append(written, prefix+".json")
	} else {
		prefix += "." + c.Scenario
	}
	h2 := c.SampleTraces
	if len(h2) == 0 && c.Trace != nil {
		h2 = []*h2trace.Trace{
			c.Trace,
		}
	}
	h1 := c.SampleH1
	if len(h1) == 0 && len(c.H1) > 0 {
		h1 = [][]H1Request{
			c.H1,
		}
	}
	pages := c.SamplePages
	if len(pages) == 0 && c.Page != nil {
		pages = []*Page{
			c.Page,
		}
	}
	proxy := c.SampleProxy
	if len(proxy) == 0 && len(c.Proxy) > 0 {
		proxy = [][]ProxyRequest{
			c.Proxy,
		}
	}
	for _, trace := range []struct {
		path    string
		samples any
		present bool
	}{
		{prefix + ".h2trace.json", h2, len(h2) > 0},
		{prefix + ".h1trace.json", h1, len(h1) > 0},
		// Replayed pages are tens of MiB of repetitive JSON.
		{prefix + ".pagetrace.json.gz", pages, len(pages) > 0},
		{prefix + ".proxytrace.json", proxy, len(proxy) > 0},
	} {
		if !trace.present {
			continue
		}
		data, err := json.MarshalIndent(trace.samples, "", "  ")
		if err != nil {
			return "", err
		}
		data = append(data, '\n')
		if strings.HasSuffix(trace.path, ".gz") {
			if data, err = gzipped(data); err != nil {
				return "", err
			}
		}
		if err := writeNew(trace.path, data); err != nil {
			return "", err
		}
		written = append(written, trace.path)
	}
	if len(written) == 0 {
		return "", fmt.Errorf("scenario %s captured no trace (protocol %q)", c.Scenario, c.Protocol)
	}
	return written[0], nil
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
