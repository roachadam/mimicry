package main

import (
	"bytes"
	"os"
	"testing"
)

// The generated file is committed; it must match what the generator produces
// from the committed data, or a profile was added without regenerating.
func TestGeneratedFileUpToDate(t *testing.T) {
	want, err := generate("../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../profiles/profiles.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("profiles/profiles.gen.go is stale; run go generate ./profiles")
	}
}
