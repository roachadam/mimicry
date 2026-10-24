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

// Decline sends the preface New sends, then GOAWAY with NO_ERROR and no
// streams, and closes: Firefox's frames on the connection it opened to a
// host another connection serves (the connections capture).
func TestDeclineSendsThePrefaceThenGoAway(t *testing.T) {
	p := profiles.Firefox158MacOS()
	client, server := net.Pipe()
	errs := make(chan error, 1)
	go func() {
		errs <- Decline(client, p)
	}()
	br := bufio.NewReader(server)
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(br, preface); err != nil || string(preface) != http2.ClientPreface {
		t.Fatalf("preface %q, %v", preface, err)
	}
	fr := http2.NewFramer(nil, br)
	var types []http2.FrameType
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			break
		}
		types = append(types, f.Header().Type)
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if n := f.NumSettings(); n != len(p.HTTP2.Settings) {
				t.Errorf("%d settings, want %d", n, len(p.HTTP2.Settings))
			}
		case *http2.WindowUpdateFrame:
			if f.Increment != p.HTTP2.WindowUpdate {
				t.Errorf("WINDOW_UPDATE %d, want %d", f.Increment, p.HTTP2.WindowUpdate)
			}
		case *http2.GoAwayFrame:
			if f.ErrCode != http2.ErrCodeNo || f.LastStreamID != 0 {
				t.Errorf("GOAWAY %v after stream %d, want NO_ERROR after 0", f.ErrCode, f.LastStreamID)
			}
		}
	}
	want := []http2.FrameType{
		http2.FrameSettings,
		http2.FrameWindowUpdate,
		http2.FrameGoAway,
	}
	if len(types) != len(want) {
		t.Fatalf("frames %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("frames %v, want %v", types, want)
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}

// Over TCP, a server that sends its SETTINGS at once still reads every
// frame Decline sent, then a clean end: Decline reads what the server
// sent before closing, so its close is no reset that could overtake its
// own frames.
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
	if err := Decline(client, profiles.Firefox158MacOS()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, io.EOF) {
		t.Fatalf("server read %v after the frames, want a clean EOF", err)
	}
}
