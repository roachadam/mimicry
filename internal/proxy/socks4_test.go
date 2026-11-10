package proxy

import (
	"encoding/hex"
	"net"
	"strings"
	"testing"
)

// SOCKS4 sends the request bytes browsers send (captures: Chrome 156
// through socks4://, Firefox 159 with socks_remote_dns off and on, Safari 27
// through macOS's SOCKS proxy setting as SOCKS4) and reports the proxy's
// answer.
func TestSOCKS4(t *testing.T) {
	tests := []struct {
		name   string
		target string
		userID string
		// hostnameIP is the SOCKS4A DSTIP; zero: Firefox's 0.0.0.1.
		hostnameIP [4]byte
		// request is the request the proxy must read, in hex; reply what
		// it answers.
		request string
		reply   []byte
		// want is the error's text; empty: no error.
		want string
	}{
		{
			name:    "IPv4 address, as Chrome and Firefox send it",
			target:  "172.66.147.243:443",
			request: "040101bbac4293f300",
			reply:   []byte{0, 0x5a, 0, 0, 0, 0, 0, 0},
		},
		{
			name:    "host name as SOCKS4A, as Firefox sends it with remote DNS",
			target:  "example.com:443",
			request: "040101bb00000001006578616d706c652e636f6d00",
			reply:   []byte{0, 0x5a, 0, 0, 0, 0, 0, 0},
		},
		{
			name:       "host name as SOCKS4A, as Safari sends it",
			target:     "example.com:443",
			hostnameIP: [4]byte{0, 0, 0, 16},
			request:    "040101bb00000010006578616d706c652e636f6d00",
			reply:      []byte{0, 0x5a, 0, 0, 0, 0, 0, 0},
		},
		{
			name:    "user ID",
			target:  "10.0.0.1:80",
			userID:  "alice",
			request: "040100500a000001616c69636500",
			reply:   []byte{0, 0x5a, 0, 0, 0, 0, 0, 0},
		},
		{
			name:    "refused",
			target:  "10.0.0.1:80",
			request: "040100500a00000100",
			reply:   []byte{0, 0x5b, 0, 0, 0, 0, 0, 0},
			want:    "refused the connection: rejected or failed",
		},
		{
			name:    "identd",
			target:  "10.0.0.1:80",
			request: "040100500a00000100",
			reply:   []byte{0, 0x5d, 0, 0, 0, 0, 0, 0},
			want:    "identd could not confirm the user ID",
		},
		{
			name:    "a reply of another version",
			target:  "10.0.0.1:80",
			request: "040100500a00000100",
			reply:   []byte{5, 0, 0, 0, 0, 0, 0, 0},
			want:    "answered version 5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := peer(t, func(conn net.Conn) error {
				want, err := hex.DecodeString(tt.request)
				if err != nil {
					return err
				}

				if err := expect(conn, want); err != nil {
					return err
				}

				_, err = conn.Write(tt.reply)

				return err
			})

			hostnameIP := tt.hostnameIP
			if hostnameIP == [4]byte{} {
				hostnameIP = [4]byte{0, 0, 0, 1}
			}

			req, err := SOCKS4Request(tt.target, tt.userID, hostnameIP)
			if err != nil {
				t.Fatal(err)
			}

			err = SOCKS4(t.Context(), conn, req)

			switch {
			case tt.want == "" && err != nil:
				t.Fatal(err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// What SOCKS4 cannot carry, or a host name without a SOCKS4A address, is
// refused while building the request, before anything is dialled.
func TestSOCKS4RequestRefuses(t *testing.T) {
	socks4a := [4]byte{0, 0, 0, 1}

	for _, tc := range []struct {
		target, userID string
		hostnameIP     [4]byte
		want           string
	}{
		{"[2001:db8::1]:443", "", socks4a, "IPv4 addresses only"},
		{"[::ffff:10.0.0.1]:443", "", socks4a, ""},
		{"10.0.0.1:443", "a\x00b", socks4a, "user ID contains a NUL"},
		{"exa\x00mple.com:443", "", socks4a, "host name contains a NUL"},
		{"example.com:443", "", [4]byte{}, "is not 0.0.0.x"},
		{"example.com:443", "", [4]byte{1, 0, 0, 1}, "is not 0.0.0.x"},
		{"10.0.0.1:443", "", [4]byte{}, ""},
	} {
		_, err := SOCKS4Request(tc.target, tc.userID, tc.hostnameIP)

		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%q: %v", tc.target, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%q: err = %v, want %q", tc.target, err, tc.want)
		}
	}
}
