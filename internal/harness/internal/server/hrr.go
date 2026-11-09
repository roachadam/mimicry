package server

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/cryptobyte"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// The hrr page's hosts: the page's, served as usual, and the one whose
// every connection gets a HelloRetryRequest with a cookie.
const (
	hrrHost      = "www.hrr-a.test"
	hrrRetryHost = "www.hrr-b.test"
)

// hrrFetches is how many times the hrr page asks hrrRetryHost for a
// resource, each on a connection of its own.
const hrrFetches = 3

// HelloRetryCookieLength is the length of the cookie the hrr scenario's
// HelloRetryRequest carries.
const HelloRetryCookieLength = 32

// HRR is the hrr scenario: the page fetches from a host that answers every
// ClientHello with a HelloRetryRequest asking for a group the client
// offered without a key share and carrying a cookie, records the second
// ClientHello and closes the connection (crypto/tls cannot finish a
// handshake whose HelloRetryRequest had a cookie). Each such connection is
// a capture.Connection with its Retry.
var HRR = &Scenario{
	Name: "hrr",
	Doc:  "a HelloRetryRequest with a 32-byte cookie, asking for a group offered without a key share: where the second ClientHello puts the cookie, and how else it differs from the first",
	multi: hrrPage{
		fixedHosts: fixedHosts{
			names: []string{
				hrrHost,
				hrrRetryHost,
			},
			runFor: 15 * time.Second,
		},
	},
	helloRetryHost: hrrRetryHost,
}

// hrrPage is the hrr scenario's page: hrrFetches no-cors fetches to
// hrrRetryHost, each failing at its handshake, then /done.
type hrrPage struct {
	fixedHosts
}

func (p hrrPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	path, _, _ := strings.Cut(req.path, "?")

	switch {
	case path == "/" && host == hrrHost:
		url := "https://" + net.JoinHostPort(hrrRetryHost, port) + "/hrr/"
		script := fmt.Sprintf(`(async () => {
for (let i = 0; i < %d; i++) {
  await fetch(%q + i, {mode: 'no-cors'}).catch(() => {});
  await new Promise(d => setTimeout(d, 500));
}
fetch('/done');
})();`, hrrFetches, url)

		return htmlResponse(htmlPage("hrr", "", "<script>"+script+"</script>\n"))

	case path == "/done":
		return response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	}

	return response{status: 404, contentType: "text/plain", body: []byte("not found\n")}
}

// helloRetry answers a ClientHello (msg, parsed as first, which arrived at
// helloAt) with a HelloRetryRequest carrying a cookie, reads the second
// ClientHello, records both in log and returns: the connection then
// closes.
func helloRetry(conn net.Conn, host string, msg []byte, first profile.TLS, helloAt time.Time, log *pageLog) error {
	group, ok := retryGroup(first)
	if !ok {
		return errors.New("hrr: the ClientHello offers no group without a key share")
	}

	hrr, err := helloRetryRequest(msg, group)
	if err != nil {
		return err
	}

	if _, err := conn.Write(hrr); err != nil {
		return err
	}

	second, err := readRetryHello(conn)
	if err != nil {
		return fmt.Errorf("hrr: the second ClientHello: %w", err)
	}

	t, err := ParseClientHello(second)
	if err != nil {
		return fmt.Errorf("hrr: the second ClientHello: %w", err)
	}

	log.helloRetry(host, msg, first, helloAt, capture.Retry{
		Group:       profile.Code(group),
		Hello:       &t,
		ClientHello: hex.EncodeToString(second),
	})

	return nil
}

// retryGroup is the group the HelloRetryRequest asks for: the first the
// client lists in supported_groups without a key share.
func retryGroup(t profile.TLS) (uint16, bool) {
	var groups, shares []profile.Code

	for _, e := range t.Extensions {
		switch e.Type {
		case profile.ExtSupportedGroups:
			groups = e.Groups
		case profile.ExtKeyShare:
			shares = e.KeyShares
		default:
		}
	}

	for _, g := range groups {
		if g != profile.GREASE && !slices.Contains(shares, g) {
			return uint16(g), true
		}
	}

	return 0, false
}

// helloRetryMagic is the ServerHello random that makes it a
// HelloRetryRequest (RFC 8446, Section 4.1.3).
var helloRetryMagic = []byte{
	0xcf, 0x21, 0xad, 0x74, 0xe5, 0x9a, 0x61, 0x11, 0xbe, 0x1d, 0x8c, 0x02, 0x1e, 0x65, 0xb8, 0x91,
	0xc2, 0xa2, 0x11, 0x16, 0x7a, 0xbb, 0x8c, 0x5e, 0x07, 0x9e, 0x09, 0xe2, 0xc8, 0xa8, 0x33, 0x9c,
}

// helloRetryRequest is the record of a HelloRetryRequest answering the
// ClientHello msg: its session ID echoed, its first TLS 1.3 cipher suite,
// group and a cookie of HelloRetryCookieLength bytes.
func helloRetryRequest(msg []byte, group uint16) ([]byte, error) {
	s := cryptobyte.String(msg)

	var body, sessionID, suites cryptobyte.String

	if !s.Skip(1) || !s.ReadUint24LengthPrefixed(&body) ||
		!body.Skip(2+32) || !body.ReadUint8LengthPrefixed(&sessionID) ||
		!body.ReadUint16LengthPrefixed(&suites) {
		return nil, errMalformed
	}

	var suite uint16

	for !suites.Empty() {
		var v uint16
		if !suites.ReadUint16(&v) {
			return nil, errMalformed
		}

		if v>>8 == 0x13 {
			suite = v

			break
		}
	}

	if suite == 0 {
		return nil, errors.New("hrr: the ClientHello offers no TLS 1.3 cipher suite")
	}

	var b cryptobyte.Builder

	b.AddUint8(recordTypeHandshake)
	b.AddUint16(0x0303)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint8(2) // server_hello
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddUint16(0x0303)
			b.AddBytes(helloRetryMagic)
			b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddBytes(sessionID)
			})
			b.AddUint16(suite)
			b.AddUint8(0)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint16(uint16(profile.ExtSupportedVersions))
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
					b.AddUint16(0x0304)
				})
				b.AddUint16(uint16(profile.ExtKeyShare))
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
					b.AddUint16(group)
				})
				b.AddUint16(uint16(profile.ExtCookie))
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
					b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
						b.AddBytes(bytes.Repeat([]byte{0xc0}, HelloRetryCookieLength))
					})
				})
			})
		})
	})

	return b.Bytes()
}

// recordTypeChangeCipherSpec is the record a client may send between its
// hellos for middlebox compatibility (RFC 8446, Appendix D.4).
const recordTypeChangeCipherSpec = 20

// readRetryHello reads the second ClientHello, skipping a
// change_cipher_spec record before it.
func readRetryHello(r io.Reader) ([]byte, error) {
	var header [recordHeaderLen]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	if header[0] == recordTypeChangeCipherSpec {
		n := binary.BigEndian.Uint16(header[3:])
		if n != 1 {
			return nil, fmt.Errorf("change_cipher_spec record of %d bytes", n)
		}

		if _, err := io.ReadFull(r, make([]byte, n)); err != nil {
			return nil, err
		}

		_, msg, err := ReadClientHello(r)

		return msg, err
	}

	_, msg, err := ReadClientHello(io.MultiReader(bytes.NewReader(header[:]), r))

	return msg, err
}

// serverName is the server_name a ClientHello msg asks for, "" without
// one.
func serverName(msg []byte) string {
	s := cryptobyte.String(msg)

	var body, sessionID, suites, compression, exts cryptobyte.String

	if !s.Skip(1) || !s.ReadUint24LengthPrefixed(&body) ||
		!body.Skip(2+32) || !body.ReadUint8LengthPrefixed(&sessionID) ||
		!body.ReadUint16LengthPrefixed(&suites) || !body.ReadUint8LengthPrefixed(&compression) ||
		!body.ReadUint16LengthPrefixed(&exts) {
		return ""
	}

	for !exts.Empty() {
		var (
			typ  uint16
			data cryptobyte.String
		)

		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return ""
		}

		if typ != uint16(profile.ExtServerName) {
			continue
		}

		var list, name cryptobyte.String

		var nameType uint8

		if !data.ReadUint16LengthPrefixed(&list) || !list.ReadUint8(&nameType) || nameType != 0 ||
			!list.ReadUint16LengthPrefixed(&name) {
			return ""
		}

		return string(name)
	}

	return ""
}
