package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/testprofiles"
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

// The checked profiles and their captures hold the shape their captured
// ClientHellos show; rerunning hellogen changes neither.
func TestRegenerateReproducesProfiles(t *testing.T) {
	names, err := testprofiles.Names()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			p, data, err := testprofiles.Load(name)
			if err != nil {
				t.Fatal(err)
			}
			captured := read(t, filepath.Join(captures, capture.Source(p, capture.DefaultScenario)+".json"))
			got, gotCapture, err := regenerate(data, captured, captures)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("regenerating %s changes it; rerun hellogen on it and review the diff", name)
			}
			if !bytes.Equal(gotCapture, captured) {
				t.Errorf("regenerating %s changes its capture; rerun hellogen on it and review the diff", name)
			}
		})
	}
}

// A profile that inherits every scenario but the default from
// chrome-155-macos (testprofiles.Inheriting) reads that profile's captures
// for them: given chrome-155-macos's default page as its own, it
// regenerates unchanged, as chrome-155-macos does.
func TestRegenerateReadsInheritedCaptures(t *testing.T) {
	const source = "chrome-155-macos"
	data, err := testprofiles.Inheriting(nil)
	if err != nil {
		t.Fatal(err)
	}
	from, err := filepath.Abs(captures)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, e := range entries {
		// Its own files are written below, never through a link into the
		// repository's captures.
		if strings.HasPrefix(e.Name(), testprofiles.InheritingName+".") {
			continue
		}
		if err := os.Symlink(filepath.Join(from, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	// The default page is never inherited: the profile's own, which is
	// chrome-155-macos's hello.
	for _, suffix := range []string{".json", ".clienthello", ".h2trace.json"} {
		if err := os.WriteFile(filepath.Join(dir, testprofiles.InheritingName+suffix), read(t, filepath.Join(from, source+suffix)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	captured := read(t, filepath.Join(dir, testprofiles.InheritingName+".json"))
	got, gotCapture, err := regenerate(data, captured, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("regenerating %s from %s's captures changes it", testprofiles.InheritingName, source)
	}
	if !bytes.Equal(gotCapture, captured) {
		t.Errorf("regenerating %s from %s's captures changes its capture", testprofiles.InheritingName, source)
	}
}
