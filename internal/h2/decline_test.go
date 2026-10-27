package h2

import (
	"bufio"
	"errors"
	"io"
	"net"
	"testing"

	"golang.org/x/net/http2"

	"github.com/roachadam/mimicry/profiles"
)

// Over TCP, a server that sends its SETTINGS at once still reads every
// frame Decline sent, then a clean end rather than a reset.
func TestDeclineClosesWithoutReset(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = ln.Close()
	})

	settingsSent := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			result <- err
			return
		}

		defer func() {
			_ = conn.Close()
		}()

		if err := http2.NewFramer(conn, nil).WriteSettings(); err != nil {
			result <- err
			return
		}

		close(settingsSent)

		br := bufio.NewReader(conn)
		if _, err := io.ReadFull(br, make([]byte, len(http2.ClientPreface))); err != nil {
			result <- err
			return
		}

		fr := http2.NewFramer(nil, br)
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				result <- err
				return
			}

			if _, ok := f.(*http2.GoAwayFrame); ok {
				// Everything arrived; the end must be clean too.
				_, err := br.ReadByte()
				result <- err

				return
			}
		}
	}()

	client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	<-settingsSent

	if err := Decline(client, profiles.Firefox()); err != nil {
		t.Fatal(err)
	}

	if err := <-result; !errors.Is(err, io.EOF) {
		t.Fatalf("server read %v after the frames, want a clean EOF", err)
	}
}
