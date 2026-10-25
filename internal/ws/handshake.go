package ws

import (
	"crypto/sha1" //nolint:gosec // RFC 6455 defines the accept key with SHA-1
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// guid is RFC 6455's key suffix (section 1.3).
const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// NewKey returns a Sec-WebSocket-Key: 16 bytes from rand, in base64.
func NewKey(rand io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand, b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

// AcceptKey is the Sec-WebSocket-Accept a server answers key with.
func AcceptKey(key string) string {
	h := sha1.Sum([]byte(key + guid)) //nolint:gosec // RFC 6455, section 4.2.2
	return base64.StdEncoding.EncodeToString(h[:])
}

// Deflate is permessage-deflate as the server accepted it (RFC 7692,
// section 7.1).
type Deflate struct {
	// ServerNoContextTakeover: each server message is compressed on its
	// own. ClientNoContextTakeover: the server asks ours to be.
	ServerNoContextTakeover bool
	ClientNoContextTakeover bool
	// ClientMaxWindowBits is the LZ77 window our messages may use: 15
	// unless the server limited it.
	ClientMaxWindowBits int
}

var errExtension = errors.New("ws: the server's Sec-WebSocket-Extensions")

// ParseExtensions reads the server's Sec-WebSocket-Extensions answer to
// offer, ours: nil for none, the accepted permessage-deflate, or an error
// for anything not offered (RFC 6455, section 9.1; RFC 7692, section 7.1).
func ParseExtensions(offer, answer string) (*Deflate, error) {
	if strings.TrimSpace(answer) == "" {
		return nil, nil //nolint:nilnil // no extension accepted
	}
	offered := map[string]bool{}
	for _, e := range strings.Split(offer, ",") {
		params := strings.Split(e, ";")
		if strings.TrimSpace(params[0]) != "permessage-deflate" {
			continue
		}
		offered["permessage-deflate"] = true
		for _, p := range params[1:] {
			name, _, _ := strings.Cut(p, "=")
			offered[strings.TrimSpace(name)] = true
		}
	}
	var d *Deflate
	for _, e := range strings.Split(answer, ",") {
		params := strings.Split(e, ";")
		if name := strings.TrimSpace(params[0]); name != "permessage-deflate" || !offered[name] {
			return nil, fmt.Errorf("%w %q accepts an extension not offered", errExtension, answer)
		}
		if d != nil {
			return nil, fmt.Errorf("%w %q accepts permessage-deflate twice", errExtension, answer)
		}
		d = &Deflate{
			ClientMaxWindowBits: 15,
		}
		seen := map[string]bool{}
		for _, p := range params[1:] {
			name, value, hasValue := strings.Cut(p, "=")
			name, value = strings.TrimSpace(name), strings.Trim(strings.TrimSpace(value), `"`)
			if seen[name] {
				return nil, fmt.Errorf("%w %q repeats %s", errExtension, answer, name)
			}
			seen[name] = true
			switch name {
			case "server_no_context_takeover":
				d.ServerNoContextTakeover = true
			case "client_no_context_takeover":
				d.ClientNoContextTakeover = true
			case "server_max_window_bits":
				// Our inflater takes any window.
				if _, err := windowBits(value); err != nil || !hasValue {
					return nil, fmt.Errorf("%w %q has a bad %s", errExtension, answer, name)
				}
			case "client_max_window_bits":
				bits, err := windowBits(value)
				if err != nil || !hasValue || !offered[name] {
					return nil, fmt.Errorf("%w %q has a bad or unoffered %s", errExtension, answer, name)
				}
				d.ClientMaxWindowBits = bits
			default:
				return nil, fmt.Errorf("%w %q has an unknown parameter %s", errExtension, answer, name)
			}
			if hasValue != (name == "server_max_window_bits" || name == "client_max_window_bits") {
				return nil, fmt.Errorf("%w %q: %s with a value it does not take", errExtension, answer, name)
			}
		}
	}
	return d, nil
}

// windowBits parses a window_bits value: 8 to 15.
func windowBits(v string) (int, error) {
	bits, err := strconv.Atoi(v)
	if err != nil || bits < 8 || bits > 15 || strconv.Itoa(bits) != v {
		return 0, fmt.Errorf("bad window bits %q", v)
	}
	return bits, nil
}
