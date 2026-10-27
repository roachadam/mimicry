package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// Deriving a committed profile from itself and its own default and http1
// captures, as a refresh in place does, reproduces it byte for byte: what
// fromCapture takes from a capture (the hello, the HTTP/2 preface, both
// navigations with their slots) is exactly what the profiles hold.
func TestDeriveReproducesProfiles(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("../..", profilesDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no profiles found")
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			prev, err := profile.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if prev.CapturesFrom["http1"] != "" {
				t.Skip("inherits its http1 capture")
			}
			captured, err := os.ReadFile(filepath.Join("../..", capturesDir, name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			c, err := profile.Decode(captured)
			if err != nil {
				t.Fatal(err)
			}
			h1, err := http1Navigation(filepath.Join("../..", capturesDir, name+".http1.h1trace.json"))
			if err != nil {
				t.Fatal(err)
			}
			p, err := fromCapture(prev, prev.Name, prev.Version, c, h1, prev.CapturesFrom)
			if err != nil {
				t.Fatal(err)
			}
			got, err := profile.Encode(p)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("deriving %s from its own captures changes it:\n%s", name, firstDifference(data, got))
			}
		})
	}
}

// firstDifference shows the first line where a and b differ.
func firstDifference(a, b []byte) string {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return "line " + strconv.Itoa(i+1) + ":\n  want " + al[i] + "\n  got  " + bl[i]
		}
	}

	return "lengths differ"
}
