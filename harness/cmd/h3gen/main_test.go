package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed profiles hold the quic and http3 sections their h3
// captures show; rerunning h3gen changes none of them.
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
			data, err := os.ReadFile(path) //nolint:gosec // a file in the repository
			if err != nil {
				t.Fatal(err)
			}
			got, err := regenerate(data, "../../testdata/captures")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("regenerating %s changes it; rerun h3gen on it and review the diff", path)
			}
		})
	}
}
