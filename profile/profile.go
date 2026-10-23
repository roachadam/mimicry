package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Profile describes one browser version on one platform.
type Profile struct {
	// Name identifies the profile, for example "chrome-155-macos".
	Name     string `json:"name"`
	Browser  string `json:"browser"`
	Version  string `json:"version"`
	Platform string `json:"platform"`

	TLS TLS `json:"tls"`
	// HTTP2 is required when the profile offers h2 in ALPN.
	HTTP2 *HTTP2 `json:"http2,omitempty"`
	// Headers of a top-level navigation request, in order.
	Headers []Header `json:"headers"`
}

// Parse decodes a profile from JSON and validates it. Unknown fields and
// trailing data are errors, so a typo cannot silently drop a setting.
func Parse(data []byte) (*Profile, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Profile
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("profile: trailing data after the profile")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Encode writes p as indented JSON with a trailing newline, the format of the
// built-in profiles and of harness captures. It does not validate p, so that a
// capture of unusual browser behaviour can still be recorded.
func Encode(p *Profile) ([]byte, error) {
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	return append(out, '\n'), nil
}
