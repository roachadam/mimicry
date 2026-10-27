package main

import (
	"bytes"
	"fmt"
	"os"
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
	versions := func(n, deprecated int) []*profile.Profile {
		out := make([]*profile.Profile, 0, n)
		for i := range n {
			out = append(out, &profile.Profile{
				Name:       fmt.Sprintf("chrome-%d-macos", 150+i),
				Browser:    "chrome",
				Version:    strconv.Itoa(150 + i),
				Platform:   "macos",
				Deprecated: i < deprecated,
			})
		}

		return out
	}
	for _, tc := range []struct {
		name     string
		profiles func() []*profile.Profile
		want     string
	}{
		{
			name: "six supported versions",
			profiles: func() []*profile.Profile {
				return versions(6, 0)
			},
			want: "chrome-150-macos must be deprecated",
		},
		{
			name: "newest version deprecated",
			profiles: func() []*profile.Profile {
				ps := versions(2, 0)

				ps[1].Deprecated = true

				return ps
			},
			want: "chrome-151-macos is deprecated but among the newest 5",
		},
		{
			name: "inherited from a profile that is not built in",
			profiles: func() []*profile.Profile {
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
			profiles: func() []*profile.Profile {
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
			profiles: func() []*profile.Profile {
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
}
