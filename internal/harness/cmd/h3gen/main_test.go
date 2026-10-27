package main

import (
	"bytes"
	"testing"

	"github.com/roachadam/mimicry/internal/harness/internal/testprofiles"
)

const captures = "../../testdata/captures"

// The checked profiles hold the quic and http3 sections their h3 captures
// show; rerunning h3gen changes none of them.
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
				t.Errorf("regenerating %s changes it; rerun h3gen on it and review the diff", name)
			}
		})
	}
}

// A profile inheriting every h3 scenario from chrome-155-macos reads its
// captures and sampleGaps pins, and regenerates unchanged.
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
