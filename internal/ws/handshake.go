package ws

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // RFC 6455 defines the accept key with SHA-1
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/profile"
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

var errExtension = errors.New("mimicry: the server's Sec-WebSocket-Extensions")

// ParseExtensions reads the server's Sec-WebSocket-Extensions answer to our
// offer: nil for none, the accepted permessage-deflate, or an error for
// anything not offered (RFC 7692, section 7.1). An unoffered
// client_max_window_bits is accepted, as every browser accepts it.
func ParseExtensions(offer, answer string) (*Deflate, error) {
	if strings.TrimSpace(answer) == "" {
		return nil, nil //nolint:nilnil // no extension accepted
	}

	offered := false
	for e := range strings.SplitSeq(offer, ",") {
		name, _, _ := strings.Cut(e, ";")

		offered = offered || strings.TrimSpace(name) == "permessage-deflate"
	}

	var d *Deflate
	for e := range strings.SplitSeq(answer, ",") {
		params := strings.Split(e, ";")
		if strings.TrimSpace(params[0]) != "permessage-deflate" || !offered {
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
				if err != nil || !hasValue {
					return nil, fmt.Errorf("%w %q has a bad %s", errExtension, answer, name)
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

// Target checks a WebSocket URL as the WebSocket constructor
// does (ws or wss, no fragment) and returns the http or https URL its
// handshake requests, which cookies, sites and connections use.
func Target(u *url.URL) (*url.URL, error) {
	target := *u
	switch u.Scheme {
	case "wss":
		target.Scheme = "https"
	case "ws":
		target.Scheme = "http"
	default:
		return nil, fmt.Errorf("mimicry: a WebSocket URL is ws:// or wss://, not %q", u.Scheme)
	}

	switch {
	case u.Host == "" || !httpguts.ValidHostHeader(u.Host):
		return nil, fmt.Errorf("mimicry: WebSocket URL %s has no valid host", u.Redacted())
	case u.Fragment != "" || u.RawFragment != "":
		return nil, fmt.Errorf("mimicry: WebSocket URL %s has a fragment", u.Redacted())
	case u.User != nil:
		return nil, fmt.Errorf("mimicry: WebSocket URL %s has credentials", u.Redacted())
	}

	return &target, nil
}

// CheckProtocols checks subprotocols as the WebSocket constructor does:
// tokens (RFC 7230), none twice.
func CheckProtocols(protocols []string) error {
	for i, pr := range protocols {
		if pr == "" || !httpguts.ValidHeaderFieldName(pr) {
			return fmt.Errorf("mimicry: WebSocket subprotocol %q is not a token", pr)
		}

		if slices.Contains(protocols[:i], pr) {
			return fmt.Errorf("mimicry: WebSocket subprotocol %q given twice", pr)
		}
	}

	return nil
}

// PlacesProtocols reports whether a handshake's lists, over HTTP/2 and
// HTTP/1.1 (its plan's Headers and HTTP1), both have a place for
// Sec-WebSocket-Protocol. An empty list, one never sent over, has.
func PlacesProtocols(headers, http1 []profile.Header) bool {
	places := func(list []profile.Header) bool {
		return len(list) == 0 || slices.ContainsFunc(list, func(h profile.Header) bool {
			return strings.EqualFold(h.Name, "sec-websocket-protocol")
		})
	}

	return places(headers) && places(http1)
}

// CheckHandshake checks the server's answer resp to a handshake with key,
// offer and protocols, over HTTP/2 when h2 (RFC 6455, section 4.1; RFC
// 8441, section 5), and returns the permessage-deflate it accepted, if
// any.
func CheckHandshake(resp *http.Response, h2 bool, key, offer string, protocols []string) (*Deflate, error) {
	if h2 {
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("mimicry: WebSocket handshake answered %s", resp.Status)
		}
	} else {
		switch {
		case resp.StatusCode != http.StatusSwitchingProtocols:
			return nil, fmt.Errorf("mimicry: WebSocket handshake answered %s", resp.Status)
		case !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket"):
			return nil, errors.New("mimicry: WebSocket handshake answered without Upgrade: websocket")
		case !httpguts.HeaderValuesContainsToken(resp.Header.Values("Connection"), "upgrade"):
			return nil, errors.New("mimicry: WebSocket handshake answered without Connection: Upgrade")
		case resp.Header.Get("Sec-WebSocket-Accept") != AcceptKey(key):
			return nil, errors.New("mimicry: WebSocket handshake answered with a wrong Sec-WebSocket-Accept")
		}
	}

	if got := resp.Header.Values("Sec-WebSocket-Protocol"); len(got) > 0 {
		if len(got) > 1 || !slices.Contains(protocols, got[0]) {
			return nil, fmt.Errorf("mimicry: WebSocket handshake answered subprotocol %q, not one asked for", strings.Join(got, ", "))
		}
	}

	return ParseExtensions(offer, strings.Join(resp.Header.Values("Sec-WebSocket-Extensions"), ", "))
}

// failedBody is how much of a failed handshake's response body is kept.
const failedBody = 4 << 10

// FailedResponse returns resp with the start of its body read into
// memory and the transport behind it closed.
func FailedResponse(resp *http.Response) *http.Response {
	if resp.Body == nil || resp.Body == http.NoBody {
		return resp
	}

	b, _ := io.ReadAll(io.LimitReader(resp.Body, failedBody))

	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))

	return resp
}
