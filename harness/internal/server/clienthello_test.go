package server

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	utls "github.com/roachadam/utls"
)

// chromeHello returns a realistic ClientHello handshake message, built by
// uTLS's Chrome profile without a network.
func chromeHello(t *testing.T) []byte {
	t.Helper()
	u := utls.UClient(&net.TCPConn{}, &utls.Config{
		ServerName: "example.com",
	}, utls.HelloChrome_155)
	if err := u.BuildHandshakeState(); err != nil {
		t.Fatal(err)
	}
	return u.HandshakeState.Hello.Raw
}

// records wraps payload in handshake records of at most size bytes each.
func records(payload []byte, size int) []byte {
	var out []byte
	for len(payload) > 0 {
		n := min(size, len(payload))
		out = append(out, recordTypeHandshake, 3, 1, byte(n>>8), byte(n)) //nolint:gosec // n <= len(payload), well under 64 KiB
		out = append(out, payload[:n]...)
		payload = payload[n:]
	}
	return out
}

// Clients may split a ClientHello across records; the reader must reassemble
// it, return exactly the bytes it consumed for replay, and leave later bytes
// unread.
func TestReadClientHelloAcrossRecords(t *testing.T) {
	msg := chromeHello(t)
	for _, size := range []int{
		len(msg),
		100,
		7,
	} {
		wire := records(msg, size)
		r := bytes.NewReader(append(wire, "next"...))
		raw, got, err := readClientHello(r)
		if err != nil {
			t.Fatalf("records of %d bytes: %v", size, err)
		}
		if !bytes.Equal(got, msg) {
			t.Errorf("records of %d bytes: reassembled message differs", size)
		}
		if !bytes.Equal(raw, wire) {
			t.Errorf("records of %d bytes: raw bytes differ from what was read", size)
		}
		if rest, _ := io.ReadAll(r); string(rest) != "next" {
			t.Errorf("records of %d bytes: consumed past the ClientHello, %q left", size, rest)
		}
	}
}

func TestReadClientHelloRejects(t *testing.T) {
	msg := chromeHello(t)
	notHello := append([]byte{2}, msg[1:]...)
	alert := records(msg, len(msg))
	alert[0] = 21
	for _, tc := range []struct {
		name string
		in   []byte
		want string
	}{
		{
			name: "non-handshake record",
			in:   alert,
			want: "is not a handshake",
		},
		{
			name: "not a ClientHello",
			in:   records(notHello, len(notHello)),
			want: "is not a ClientHello",
		},
		{
			name: "truncated",
			in:   records(msg, len(msg))[:len(msg)/2],
			want: io.ErrUnexpectedEOF.Error(),
		},
		{
			name: "plain HTTP",
			in:   []byte("GET / HTTP/1.1\r\n\r\n"),
			want: "is not a handshake",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := readClientHello(bytes.NewReader(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestParseClientHelloRejectsMalformed(t *testing.T) {
	msg := chromeHello(t)
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{
			name: "truncated",
			in:   msg[:len(msg)-3],
		},
		{
			name: "trailing byte",
			in:   append(append([]byte(nil), msg...), 0),
		},
		{
			name: "wrong handshake type",
			in:   append([]byte{2}, msg[1:]...),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseClientHello(tc.in); err == nil {
				t.Fatal("accepted a malformed ClientHello")
			} else if !errors.Is(err, errMalformed) {
				t.Fatalf("error = %v, want errMalformed", err)
			}
		})
	}
}
