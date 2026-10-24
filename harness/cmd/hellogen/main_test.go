package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const captures = "../../testdata/captures"

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a file in the repository
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The committed profiles and captures hold the shape their captured
// ClientHellos show; rerunning hellogen changes neither.
func TestRegenerateReproducesProfiles(t *testing.T) {
	paths, err := filepath.Glob("../../../profiles/data/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no profiles found")
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			data := read(t, path)
			captured := read(t, filepath.Join(captures, name+".json"))
			got, gotCapture, err := regenerate(data, captured, captures, name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("regenerating %s changes it; rerun hellogen on it and review the diff", path)
			}
			if !bytes.Equal(gotCapture, captured) {
				t.Errorf("regenerating %s changes its capture; rerun hellogen on it and review the diff", path)
			}
		})
	}
}
