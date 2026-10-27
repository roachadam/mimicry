package main

import (
	"bytes"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/testprofiles"
)

const captures = "../../testdata/captures"

// Every checked profile is what kindsgen derives from the committed
// captures: no hand edits, no generator drift.
func TestRegenerateReproducesProfiles(t *testing.T) {
	names, err := testprofiles.Names()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			_, data, err := testprofiles.Load(name)
			if err != nil {
				t.Fatal(err)
			}

			got, err := regenerate(data, captures)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(got, data) {
				t.Errorf("regenerating %s changes it; rerun kindsgen on it and review the diff", name)
			}
		})
	}
}

// An inheriting profile (testprofiles.Inheriting) must regenerate
// unchanged from chrome-155-macos captures rewritten to its version.
func TestRegenerateReadsInheritedCaptures(t *testing.T) {
	data, err := testprofiles.Inheriting(nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := regenerate(data, captures)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("regenerating %s from chrome-155-macos's captures changes it", testprofiles.InheritingName)
	}
}
