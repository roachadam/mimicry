package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/profile"
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

// The generator refuses a set of profiles that breaks the version policy
// (decision 0035), so a refresh cannot leave too many versions supported or
// inherit captures that are not there.
func TestCheckPolicyRefuses(t *testing.T) {
	versions := func(n, deprecated int) []*profile.Spec {
		out := make([]*profile.Spec, 0, n)
		for i := range n {
			out = append(out, &profile.Spec{
				Name:       fmt.Sprintf("chrome-%d-macos", 150+i),
				Browser:    "chrome",
				Version:    strconv.Itoa(150 + i),
				Platform:   "macos",
				Deprecated: i < deprecated,
			})
		}

		return out
	}
	safari := func(version int, platform string) *profile.Spec {
		return &profile.Spec{
			Name:     fmt.Sprintf("safari-%d-%s", version, platform),
			Browser:  "safari",
			Version:  strconv.Itoa(version),
			Platform: platform,
		}
	}

	for _, tc := range []struct {
		name     string
		profiles func() []*profile.Spec
		want     string
	}{
		{
			name: "six supported versions",
			profiles: func() []*profile.Spec {
				return versions(6, 0)
			},
			want: "chrome-150-macos must be deprecated",
		},
		{
			name: "newest version deprecated",
			profiles: func() []*profile.Spec {
				ps := versions(2, 0)

				ps[1].Deprecated = true

				return ps
			},
			want: "chrome-151-macos is deprecated but among the newest 5",
		},
		{
			name: "inherited from a profile that is not built in",
			profiles: func() []*profile.Spec {
				ps := versions(2, 0)

				ps[1].CapturesFrom = map[string]string{
					"cookies": "chrome-149-macos",
				}

				return ps
			},
			want: `capturesFrom.cookies: "chrome-149-macos" is not a built-in profile`,
		},
		{
			name: "inherited from a newer version",
			profiles: func() []*profile.Spec {
				ps := versions(2, 0)

				ps[0].CapturesFrom = map[string]string{
					"cookies": "chrome-151-macos",
				}

				return ps
			},
			want: "chrome-151-macos is not an older version",
		},
		{
			name: "inherited from a profile that inherits it too",
			profiles: func() []*profile.Spec {
				ps := versions(3, 0)

				ps[1].CapturesFrom = map[string]string{
					"cookies": "chrome-150-macos",
				}
				ps[2].CapturesFrom = map[string]string{
					"cookies": "chrome-151-macos",
				}

				return ps
			},
			want: "chrome-151-macos inherits that scenario itself; name chrome-150-macos",
		},
		{
			name: "inherited from another platform",
			profiles: func() []*profile.Spec {
				ps := append(versions(1, 0), onWindows(versions(2, 0))...)

				ps[2].CapturesFrom = map[string]string{
					"cookies": "chrome-150-macos",
				}

				return ps
			},
			want: "chrome-151-windows: capturesFrom.cookies: chrome-150-macos is not chrome on windows",
		},
		{
			name: "inherited from a newer version of the platform sharing the stack",
			profiles: func() []*profile.Spec {
				ps := []*profile.Spec{
					safari(27, "ios"),
					safari(28, "macos"),
				}

				ps[0].CapturesFrom = map[string]string{
					"cookies": "safari-28-macos",
				}

				return ps
			},
			want: "safari-27-ios: capturesFrom.cookies: safari-28-macos is a newer version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPolicy(tc.profiles())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("checkPolicy() = %v, want an error containing %q", err, tc.want)
			}
		})
	}

	if err := checkPolicy(versions(7, 2)); err != nil {
		t.Fatalf("checkPolicy(five supported, two deprecated) = %v", err)
	}

	// The newest five are counted per platform: five on each are all
	// supported.
	if err := checkPolicy(append(versions(5, 0), onWindows(versions(5, 0))...)); err != nil {
		t.Fatalf("checkPolicy(five on macOS, five on Windows) = %v", err)
	}

	// Safari on iOS inherits from macOS Safari of its own version, whose
	// network stack it shares (decision 0051).
	ios := []*profile.Spec{
		safari(27, "macos"),
		safari(27, "ios"),
	}

	ios[1].CapturesFrom = map[string]string{
		"cookies": "safari-27-macos",
	}
	if err := checkPolicy(ios); err != nil {
		t.Fatalf("checkPolicy(safari-27-ios inheriting from safari-27-macos) = %v", err)
	}
}

// onWindows moves macOS profiles to Windows.
func onWindows(ps []*profile.Spec) []*profile.Spec {
	for _, p := range ps {
		p.Name = strings.TrimSuffix(p.Name, "-macos") + "-windows"
		p.Platform = "windows"
	}

	return ps
}

// A Windows profile gets its own newest-version accessor and versioned one;
// the unqualified accessor stays on macOS (decision 0047).
func TestGenerateWindows(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile("../../profiles/data/chrome-155-macos.json")
	if err != nil {
		t.Fatal(err)
	}

	mac, err := profile.Decode(b)
	if err != nil {
		t.Fatal(err)
	}

	for _, version := range []string{"155", "156"} {
		p := *mac

		p.Name, p.Version, p.Platform = "chrome-"+version+"-windows", version, "windows"
		p.CapturesFrom = nil

		write(t, filepath.Join(dir, "data", p.Name+".json"), &p)
	}

	write(t, filepath.Join(dir, "data", "chrome-155-macos.json"), mac)

	src, err := generate(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"func Chrome() profile.Profile {\n\treturn load(\"chrome-155-macos\")\n}",
		"func ChromeWindows() profile.Profile {\n\treturn load(\"chrome-156-windows\")\n}",
		"func Chrome155Windows() profile.Profile {\n\treturn load(\"chrome-155-windows\")\n}",
		"func Chrome156Windows() profile.Profile {\n\treturn load(\"chrome-156-windows\")\n}",
	} {
		if !bytes.Contains(src, []byte(want)) {
			t.Errorf("generated source lacks\n%s\n\ngot:\n%s", want, src)
		}
	}

	if bytes.Contains(src, []byte("func ChromeMacOS()")) {
		t.Error("generated a platform accessor for the default platform")
	}

	// A name that does not say the profile's platform is refused.
	p := *mac

	p.Name = "chrome-157-windows"
	p.Version = "157"
	p.CapturesFrom = nil

	write(t, filepath.Join(dir, "data", p.Name+".json"), &p)

	if _, err := generate(dir); err == nil || !strings.Contains(err.Error(), "it must be named chrome-157-macos") {
		t.Fatalf("generate(a windows-named macOS profile) = %v", err)
	}
}

func write(t *testing.T, path string, p *profile.Spec) {
	t.Helper()

	b, err := profile.Encode(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, b, 0o600); err != nil { //nolint:gosec // test path under t.TempDir() with a generated file name
		t.Fatal(err)
	}
}
