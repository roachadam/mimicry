package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fixtures in testdata are the feeds' responses recorded on
// 2026-11-03 (edgeupdates-products.json on 2026-11-06), Brave's version
// feed recorded on 2026-11-09 and cut to its three newest release builds
// and newest beta and nightly (brave-versions.json, the fields read), and
// an unmodified copy of Chromium's
// net/data/ssl/chrome_root_store/root_store.textproto at 152.0.7977.85
// (BSD-3-Clause, testdata/LICENSE-chromium): Chrome 152's 32 trust anchors.

// chrome156Anchors is the trust anchors extension of chrome-156-macos and
// chrome-156-windows, as captured: Chrome 153's 28 IDs.
const chrome156Anchors = "00b80582df1302010582df1302060582df13020d0582df13020e0582df13020f0582df1302120582df1302130582df13021408839a648c9b2d010708839a648c9b2d010808839a648c9b2d010908839a648c9b2d010a08839a648c9b2d010b08839a648c9b2d010c08839a648c9b2d010d08839a648c9b2d011208839a648c9b2d011304d679090104d679090404d679090504d679090604d679090704d679090804d679090a04d679090b04d679090c04d679090d04d679090f"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// TestReleasesBehind reads the recorded feeds and reports the releases
// newer than a profile: a new major for Chrome, Edge and Firefox, any newer
// version for Safari, never a release at or below the profile's.
func TestReleasesBehind(t *testing.T) {
	parse := func(name string, parse func([]byte) (string, error)) string {
		v, err := parse(readFixture(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		return v
	}

	chromeMac := parse("chromiumdash-mac.json", parseChrome)
	chromeWindows := parse("chromiumdash-windows.json", parseChrome)
	firefox := parse("firefox_versions.json", parseFirefox)
	safari := parse("safari-release-notes.json", parseSafari)
	// The feed lists an older MacOS release (152) before the current one.
	edgeMac := parse("edgeupdates-products.json", parseEdge("MacOS"))
	edgeWindows := parse("edgeupdates-products.json", parseEdge("Windows"))
	edgeLinux := parse("edgeupdates-products.json", parseEdge("Linux"))
	// The newest release build, not the newer beta and nightly.
	brave := parse("brave-versions.json", parseBrave)

	for _, c := range []struct {
		got, want string
	}{
		{
			got:  chromeMac,
			want: "156.0.8078.80",
		},
		{
			got:  chromeWindows,
			want: "156.0.8078.80",
		},
		{
			got:  firefox,
			want: "159.0.1",
		},
		{
			got:  safari,
			want: "27.2",
		},
		{
			got:  edgeMac,
			want: "157.0.4341.42",
		},
		{
			got:  edgeWindows,
			want: "157.0.4341.42",
		},
		{
			got:  edgeLinux,
			want: "157.0.4341.42",
		},
		{
			got:  brave,
			want: "157.1.99.55",
		},
	} {
		if c.got != c.want {
			t.Errorf("feed version %q, want %q", c.got, c.want)
		}
	}

	// A beta's notes are not a release.
	beta, err := parseSafari([]byte(`{"references":{"a":{"title":"Safari 27.3 Beta Release Notes"},"b":{"title":"Safari 27.2 Release Notes"},"c":{"title":"Safari 27.10 Release Notes"}}}`))
	if err != nil || beta != "27.10" {
		t.Errorf("parseSafari with a beta = %q, %v; want 27.10", beta, err)
	}

	// A version that is not one fails: Chrome's goes into the root store's
	// URL, every one into the issue body. So does a feed with no stable
	// release for the platform (Edge's Beta is not one).
	for _, c := range []struct {
		parse func([]byte) (string, error)
		data  string
	}{
		{
			parse: parseChrome,
			data:  `[{"version":"156.0.8078"}]`,
		},
		{
			parse: parseChrome,
			data:  `[{"version":"156.0.8078.80/../../main"}]`,
		},
		{
			parse: parseChrome,
			data:  `[{"version":"156.0.8078.80\n| row |"}]`,
		},
		{
			parse: parseFirefox,
			data:  `{"LATEST_FIREFOX_VERSION":"160.0b4"}`,
		},
		{
			parse: parseFirefox,
			data:  `{"LATEST_FIREFOX_VERSION":"159.0.1 | x |"}`,
		},
		{
			parse: parseEdge("MacOS"),
			data:  `[{"Product":"Stable","Releases":[{"Platform":"MacOS","ProductVersion":"157.0.4341.42 | x |"}]}]`,
		},
		{
			// Only Beta has a MacOS release.
			parse: parseEdge("MacOS"),
			data:  `[{"Product":"Beta","Releases":[{"Platform":"MacOS","ProductVersion":"158.0.4370.10"}]},{"Product":"Stable","Releases":[{"Platform":"Windows","ProductVersion":"157.0.4341.42"}]}]`,
		},
	} {
		if v, err := c.parse([]byte(c.data)); err == nil {
			t.Errorf("%s parsed as %q", c.data, v)
		}
	}

	// A response over the limit fails rather than coming back cut short.
	for _, c := range []struct {
		body string
		ok   bool
	}{
		{
			body: "1234",
			ok:   true,
		},
		{
			body: "12345",
		},
	} {
		data, err := readLimited(strings.NewReader(c.body), 4)
		if (err == nil) != c.ok || (c.ok && string(data) != c.body) {
			t.Errorf("readLimited(%q, 4) = %q, %v", c.body, data, err)
		}
	}

	releases := []release{
		{
			browser:  "chrome",
			platform: "macos",
			version:  chromeMac,
		},
		{
			browser:  "chrome",
			platform: "windows",
			version:  chromeWindows,
		},
		{
			browser:  "firefox",
			platform: "macos",
			version:  firefox,
		},
		{
			browser:  "firefox",
			platform: "windows",
			version:  firefox,
		},
		{
			browser:  "safari",
			platform: "macos",
			version:  safari,
		},
		{
			browser:  "safari",
			platform: "ios",
			version:  safari,
		},
		{
			browser:  "firefox",
			platform: "linux",
			version:  firefox,
		},
		{
			browser:  "edge",
			platform: "macos",
			version:  edgeMac,
		},
		{
			browser:  "edge",
			platform: "windows",
			version:  edgeWindows,
		},
	}

	for _, c := range []struct {
		name     string
		builtins []builtin
		want     []string
	}{
		{
			name: "2026-11-03",
			builtins: []builtin{
				{
					name:     "chrome-156-macos",
					browser:  "chrome",
					platform: "macos",
					version:  "156",
				},
				{
					name:     "chrome-155-windows",
					browser:  "chrome",
					platform: "windows",
					version:  "155",
				},
				{
					name:     "firefox-158-macos",
					browser:  "firefox",
					platform: "macos",
					version:  "158",
				},
				{
					name:     "firefox-159-windows",
					browser:  "firefox",
					platform: "windows",
					version:  "159",
				},
				{
					name:     "safari-27-macos",
					browser:  "safari",
					platform: "macos",
					version:  "27.0.1",
				},
				{
					name:     "safari-27-ios",
					browser:  "safari",
					platform: "ios",
					version:  "27.2",
				},
				{
					name:     "edge-156-macos",
					browser:  "edge",
					platform: "macos",
					version:  "156",
				},
				{
					name:     "edge-157-windows",
					browser:  "edge",
					platform: "windows",
					version:  "157",
				},
			},
			want: []string{
				"chrome 156.0.8078.80 windows chrome-155-windows",
				"firefox 159.0.1 macos firefox-158-macos",
				"safari 27.2 macos safari-27-macos",
				"edge 157.0.4341.42 macos edge-156-macos",
			},
		},
		{
			// A bug-fix release has no notes: 27.2.1 is not behind 27.2.
			name: "up to date",
			builtins: []builtin{
				{
					name:     "safari-27-macos",
					browser:  "safari",
					platform: "macos",
					version:  "27.2.1",
				},
				{
					name:     "safari-27-ios",
					browser:  "safari",
					platform: "ios",
					version:  "27.2",
				},
				{
					name:     "firefox-160-macos",
					browser:  "firefox",
					platform: "macos",
					version:  "160",
				},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			stale, err := findBehind(releases, c.builtins)
			if err != nil {
				t.Fatal(err)
			}

			var got []string
			for _, s := range stale {
				got = append(got, strings.Join([]string{s.release.browser, s.release.version, s.release.platform, s.profile.name}, " "))
			}

			if !slices.Equal(got, c.want) {
				t.Errorf("behind:\n got %q\nwant %q", got, c.want)
			}

			body := report(stale, nil)
			if (body == "") != (len(c.want) == 0) {
				t.Errorf("report %q for %d findings", body, len(c.want))
			}

			for _, s := range stale {
				row := "| " + browserNames[s.release.browser] + " | " + platformNames[s.release.platform] + " | " + s.release.version + " | `" + s.profile.name + "` | " + s.profile.version + " |"
				if !strings.Contains(body, row) {
					t.Errorf("report lacks %q:\n%s", row, body)
				}
			}
		})
	}
}

// TestTrustAnchors compares Chrome 152's root store with the trust anchors
// Chrome 153 and later send: the four IDs Chrome 153 dropped are in the
// store and not the profile.
func TestTrustAnchors(t *testing.T) {
	store, err := parseRootStore(readFixture(t, "root_store-152.0.7977.85.textproto"))
	if err != nil {
		t.Fatal(err)
	}

	data, err := hex.DecodeString(chrome156Anchors)
	if err != nil {
		t.Fatal(err)
	}

	profile, err := parseTrustAnchors(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(store) != 32 || len(profile) != 28 {
		t.Fatalf("%d IDs in the root store, %d in the profile; want 32 and 28", len(store), len(profile))
	}

	dropped := []string{
		"`d6790902` (11129.9.2)",
		"`d6790903` (11129.9.3)",
		"`d6790909` (11129.9.9)",
		"`d679090e` (11129.9.14)",
	}

	for _, c := range []struct {
		name           string
		profile, store [][]byte
		added, removed []string
	}{
		{
			name:    "store ahead",
			profile: profile,
			store:   store,
			added:   dropped,
		},
		{
			name:    "profile ahead",
			profile: store,
			store:   profile,
			removed: dropped,
		},
		{
			name:    "equal",
			profile: profile,
			store:   profile,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			added, removed := diffAnchors(c.profile, c.store)

			names := func(ids [][]byte) []string {
				out := make([]string, 0, len(ids))
				for _, id := range ids {
					out = append(out, anchorName(id))
				}

				return out
			}

			if got := names(added); !slices.Equal(got, c.added) {
				t.Errorf("added %q, want %q", got, c.added)
			}

			if got := names(removed); !slices.Equal(got, c.removed) {
				t.Errorf("removed %q, want %q", got, c.removed)
			}
		})
	}

	added, removed := diffAnchors(profile, store)
	body := report(nil, []anchorDiff{
		{
			profile: "chrome-156-macos",
			tag:     "152.0.7977.85",
			added:   added,
			removed: removed,
		},
	})

	row := "| `chrome-156-macos` | 152.0.7977.85 | " + strings.Join(dropped, ", ") + " | none |"
	if !strings.Contains(body, row) {
		t.Errorf("report lacks %q:\n%s", row, body)
	}

	for _, bad := range []string{"", "00", "0001", "0003030102", "0002000a"} {
		data, _ := hex.DecodeString(bad)
		if _, err := parseTrustAnchors(data); err == nil {
			t.Errorf("parseTrustAnchors(%s) succeeded", bad)
		}
	}
}
