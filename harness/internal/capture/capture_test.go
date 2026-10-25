package capture

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// Browsers open several connections; only the first capture of an identity is
// kept, and Write must never overwrite it.
func TestWriteNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	c := &Capture{
		Profile: profile.Profile{
			Name: "chrome-155-macos",
			Headers: []profile.Header{
				{
					Name:  "user-agent",
					Value: "first",
				},
			},
		},
		ClientHello: []byte{
			1,
			2,
		},
	}
	path, err := Write(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "chrome-155-macos.json") {
		t.Errorf("path %s", path)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := os.ReadFile(filepath.Join(dir, "chrome-155-macos.clienthello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(hello) != "0102\n" {
		t.Errorf("clienthello file %q, want hex of the raw message", hello)
	}

	c.Profile.Headers[0].Value = "second"
	if _, err := Write(dir, c); !errors.Is(err, ErrExists) {
		t.Fatalf("second Write error = %v, want ErrExists", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(first) {
		t.Error("second Write changed the existing capture")
	}
}
