package ws

import (
	"fmt"

	"github.com/roachadam/mimicry/internal/zdeflate"
	"github.com/roachadam/mimicry/profile"
)

// Compressor compresses a socket's messages as the profile's browser does
// once the server accepted permessage-deflate, including which messages it
// sends uncompressed. It is not safe for concurrent use.
type Compressor struct {
	w         *zdeflate.Writer
	rule      string
	noContext bool
}

// NewCompressor returns the compressor of p's browser for d. It fails where
// the browser fails the socket: client_max_window_bits=8, which zlib
// refuses and only Chrome raises to 9.
func NewCompressor(p *profile.WebSocket, d *Deflate) (*Compressor, error) {
	var lib zdeflate.Library
	switch p.Compression {
	case profile.CompressionChromiumZlib6:
		lib = zdeflate.Chromium
	case profile.CompressionZlib6:
		lib = zdeflate.Zlib
	case profile.CompressionZlibRS6:
		lib = zdeflate.ZlibRS
	default:
		return nil, fmt.Errorf("ws: unknown compression %q", p.Compression)
	}
	bits := d.ClientMaxWindowBits
	if bits == 8 && p.WindowBits8 == profile.WindowBits8As9 {
		bits = 9
	}
	w, err := zdeflate.NewWriter(lib, 6, bits)
	if err != nil {
		return nil, fmt.Errorf("ws: a %d-bit compression window: %w", d.ClientMaxWindowBits, err)
	}

	return &Compressor{
		w:         w,
		rule:      p.Uncompressed,
		noContext: d.ClientNoContextTakeover,
	}, nil
}

// Message returns what goes on the wire for message p, and whether it is
// compressed (RSV1).
func (c *Compressor) Message(p []byte) ([]byte, bool) {
	if len(p) == 0 {
		if c.rule == profile.UncompressedNever {
			// Chrome writes the empty stored block itself, leaving zlib
			// alone.
			return []byte{
				0x00,
			}, true
		}

		return p, false
	}
	out := c.w.Message(p)
	if c.noContext {
		c.w.Reset()
	}
	switch {
	case c.rule == profile.UncompressedEmptyOrLonger && c.noContext && len(out) > len(p):
		return p, false
	case c.rule == profile.UncompressedEmptyOrNotShorter && len(out) >= len(p):
		// The compressor took the message in: Safari starts over.
		c.w.Reset()
		return p, false
	}

	return out, true
}
