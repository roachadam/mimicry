// Package capture defines a recorded capture. It uses the JSON schema of
// mimicry's profile package, with values that are random by design blanked.
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
	// Records are the TLS records that carried ClientHello, headers
	// included. Nil in captures made before they were kept.
	Records []byte
	// SampleClientHellos are every sample's records (or, without them, its
	// ClientHello) when this capture was combined from several (Combine),
	// the first being this capture's.
	SampleClientHellos [][]byte
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
	// ProxyConn numbers the client's connection to the capture proxy that
	// carried this connection (ProxyRequest.Conn), or 0 for a direct one.
	ProxyConn int
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

// RecordVersion is the record-layer version of the records that carried the
// ClientHello, zero if they were not kept.
func (c *Capture) RecordVersion() uint16 {
	if len(c.Records) < 3 {
		return 0
	}
	return uint16(c.Records[1])<<8 | uint16(c.Records[2])
}

// hello is what <name>.clienthello keeps of one sample: its records, or its
// ClientHello message if they were not kept.
func (c *Capture) hello() []byte {
	if len(c.Records) > 0 {
		return c.Records
	}
	return c.ClientHello
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

// output is one file Write stores.
type output struct {
	path   string
	encode func() ([]byte, error)
}

// Write stores c in dir and returns the path of the first file written. The
// default scenario writes <name>.json (the profile) and <name>.clienthello
// (see ReadClientHellos); other scenarios write only their traces, as
// <name>.<scenario>.*. Trace files hold every sample's trace. Write never
// overwrites: if any file exists it writes none and returns ErrExists.
func Write(dir string, c *Capture) (string, error) {
	outputs, err := outputsOf(dir, c)
	if err != nil {
		return "", err
	}
	if err := checkNew(outputs); err != nil {
		return "", err
	}
	data := make([][]byte, len(outputs))
	for i, o := range outputs {
		if data[i], err = o.encode(); err != nil {
			return "", err
		}
	}
	for i, o := range outputs {
		if err := writeNew(o.path, data[i]); err != nil {
			return "", err
		}
	}
	return outputs[0].path, nil
}

// CheckNew returns ErrExists if any file Write would store for c in dir
// exists. File names don't depend on the sample count, so it can be checked
// after the first sample.
func CheckNew(dir string, c *Capture) error {
	outputs, err := outputsOf(dir, c)
	if err != nil {
		return err
	}
	return checkNew(outputs)
}

func checkNew(outputs []output) error {
	for _, o := range outputs {
		_, err := os.Stat(o.path)
		if err == nil {
			return fmt.Errorf("%s: %w", o.path, ErrExists)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// ReadClientHellos reads a <name>.clienthello file: one sample per line, in
// hex, each the TLS records that carried the ClientHello (starting 0x16) or,
// in older captures, the ClientHello message alone (starting 0x01).
func ReadClientHellos(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var hellos [][]byte
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		b, err := hex.DecodeString(strings.TrimSpace(line))
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		if len(b) == 0 || (b[0] != recordTypeHandshake && b[0] != handshakeClientHello) {
			return nil, fmt.Errorf("%s:%d: neither TLS records nor a ClientHello message", path, i+1)
		}
		hellos = append(hellos, b)
	}
	return hellos, nil
}

// The first byte of a record carrying a handshake, and of a ClientHello
// message.
const (
	recordTypeHandshake  = 0x16
	handshakeClientHello = 0x01
)

// outputsOf lists the files Write stores for c, the first one first.
func outputsOf(dir string, c *Capture) ([]output, error) {
	if c.Profile.Name == "" {
		return nil, errors.New("capture has no name; call Identify first")
	}
	prefix := filepath.Join(dir, c.Profile.Name)
	var outputs []output
	if c.Scenario == "" || c.Scenario == DefaultScenario {
		outputs = append(outputs, output{
			path: prefix + ".json",
			encode: func() ([]byte, error) {
				return profile.Encode(&c.Profile)
			},
		}, output{
			path: prefix + ".clienthello",
			encode: func() ([]byte, error) {
				hellos := c.SampleClientHellos
				if len(hellos) == 0 {
					hellos = [][]byte{
						c.hello(),
					}
				}
				var out []byte
				for _, h := range hellos {
					out = hex.AppendEncode(out, h)
					out = append(out, '\n')
				}
				return out, nil
			},
		})
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
		outputs = append(outputs, output{
			path: trace.path,
			encode: func() ([]byte, error) {
				data, err := json.MarshalIndent(trace.samples, "", "  ")
				if err != nil {
					return nil, err
				}
				data = append(data, '\n')
				if strings.HasSuffix(trace.path, ".gz") {
					return gzipped(data)
				}
				return data, nil
			},
		})
	}
	if len(outputs) == 0 {
		return nil, fmt.Errorf("scenario %s captured no trace (protocol %q)", c.Scenario, c.Protocol)
	}
	return outputs, nil
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
