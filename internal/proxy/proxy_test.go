package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
)

// peer runs serve on the far end of a pipe and returns the near end. serve's
// error, if any, fails the test once it is done.
func peer(t *testing.T, serve func(conn net.Conn) error) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	errs := make(chan error, 1)
	go func() {
		errs <- serve(server)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		if err := <-errs; err != nil {
			t.Error(err)
		}
	})

	return client
}

// readRequest reads an HTTP request head, through its blank line.
func readRequest(br *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return b.String(), err
		}
		b.WriteString(line)
		if line == "\r\n" {
			return b.String(), nil
		}
	}
}

// expect reads exactly want from r.
func expect(r io.Reader, want []byte) error {
	got := make([]byte, len(want))
	if _, err := io.ReadFull(r, got); err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("peer read " + string(got) + ", want " + string(want))
	}

	return nil
}

func TestConnectBoundsTheAnswerHeader(t *testing.T) {
	conn := peer(t, func(conn net.Conn) error {
		br := bufio.NewReader(conn)
		if _, err := readRequest(br); err != nil {
			return err
		}
		// Write until the client stops reading; the pipe then fails.
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Long: ")
		_, _ = io.Copy(conn, strings.NewReader(strings.Repeat("a", 1<<20)))

		return nil
	})
	_, _, err := Connect(t.Context(), conn, "example.com:443", nil)
	if !errors.Is(err, ErrAnswerTooLarge) {
		t.Fatalf("err = %v, want ErrAnswerTooLarge", err)
	}
	_ = conn.Close()
}

// socksPeer is a SOCKS5 proxy: it expects the greeting offering methods,
// picks method, runs auth if set, expects request, and answers reply.
type socksPeer struct {
	methods []byte
	pick    byte
	auth    func(conn net.Conn) error
	request []byte
	reply   []byte
}

func (p socksPeer) serve(conn net.Conn) error {
	greeting := append([]byte{5, 0}, p.methods...)
	greeting[1] = uint8(len(p.methods))
	if err := expect(conn, greeting); err != nil {
		return err
	}
	if _, err := conn.Write([]byte{5, p.pick}); err != nil {
		return err
	}
	if p.pick == 0xff {
		return nil
	}
	if p.auth != nil {
		if err := p.auth(conn); err != nil {
			return err
		}
	}
	if p.request == nil {
		return nil
	}
	if err := expect(conn, p.request); err != nil {
		return err
	}
	// A client stops reading an error reply after its code, so the write
	// may fail; the client's result is what the test checks.
	_, _ = conn.Write(p.reply)

	return nil
}

func TestSOCKS5Failures(t *testing.T) {
	tests := []struct {
		name string
		peer socksPeer
		user *url.Userinfo
		want string
	}{
		{
			name: "no acceptable method",
			peer: socksPeer{
				methods: []byte{0},
				pick:    0xff,
			},
			want: "accepts none of the authentication methods",
		},
		{
			name: "method not offered",
			peer: socksPeer{
				methods: []byte{0},
				pick:    1,
			},
			want: "picked authentication method 1",
		},
		{
			name: "password asked without one",
			peer: socksPeer{
				methods: []byte{0},
				pick:    2,
			},
			want: "the proxy URL has none",
		},
		{
			name: "credentials rejected",
			peer: socksPeer{
				methods: []byte{0},
				pick:    2,
				auth: func(conn net.Conn) error {
					if err := expect(conn, []byte("\x01\x01u\x01p")); err != nil {
						return err
					}
					_, err := conn.Write([]byte{1, 1})

					return err
				},
			},
			user: url.UserPassword("u", "p"),
			want: "rejected the username and password",
		},
		{
			name: "connection refused",
			peer: socksPeer{
				methods: []byte{0},
				pick:    0,
				request: append(append([]byte{5, 1, 0, 3, 11}, "example.com"...), 0x01, 0xbb),
				reply:   []byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0},
			},
			want: "refused the connection: connection refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := peer(t, tt.peer.serve)
			err := SOCKS5(t.Context(), conn, "example.com:443", tt.peer.methods, tt.user)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
