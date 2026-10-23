package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestGenerateRefuses(t *testing.T) {
	valid, err := os.ReadFile("../../profiles/data/chrome-155-macos.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		file string
		data []byte
		want string
	}{
		{
			name: "invalid profile",
			file: "chrome-155-macos.json",
			data: bytes.Replace(valid, []byte(`"extension_order": "shuffled"`), []byte(`"extension_order": "sometimes"`), 1),
			want: "extension_order",
		},
		{
			name: "file name does not match the profile",
			file: "chrome-999-macos.json",
			data: valid,
			want: "must be chrome-155-macos.json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "data"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "data", tc.file), tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := generate(dir); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("generate error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestFuncName(t *testing.T) {
	for in, want := range map[string]string{
		"chrome-155-macos":    "Chrome155MacOS",
		"safari-18-ios":       "Safari18IOS",
		"firefox-158-windows": "Firefox158Windows",
	} {
		if got := funcName(in); got != want {
			t.Errorf("funcName(%q) = %q, want %q", in, got, want)
		}
	}
}
