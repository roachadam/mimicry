package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Every built-in profile is what kindsgen derives from the committed
// captures: no hand edits, no generator drift.
func TestRegenerateReproducesProfiles(t *testing.T) {
	paths, err := filepath.Glob("../../../profiles/data/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no profiles found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path) //nolint:gosec // a profile in the repository
			if err != nil {
				t.Fatal(err)
			}
			got, err := regenerate(data, "../../testdata/captures")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("regenerating %s changes it; rerun kindsgen on it and review the diff", path)
			}
		})
	}
}
