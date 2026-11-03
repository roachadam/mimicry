package main

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// release is a browser's stable version on a platform, as its feed says.
type release struct {
	browser  string
	platform string
	version  string
}

// builtin is the newest built-in profile of a browser on a platform.
type builtin struct {
	name     string
	browser  string
	platform string
	// version is the version the profile's User-Agent shows of what a
	// release changes: the major for Chrome and Firefox (Chrome's reduced
	// UA, and Firefox's, end in .0), Safari's full Version/ token
	// (27.0.1).
	version string
	// anchors are the trust anchor IDs of its trust anchors extension
	// (0xca34), in order; nil when it sends none.
	anchors [][]byte
}

// behind is a release newer than the newest profile of its browser on its
// platform.
type behind struct {
	release release
	profile builtin
}

// anchorDiff is a Chrome profile whose trust anchors differ from the Chrome
// Root Store at its platform's stable tag.
type anchorDiff struct {
	profile string
	tag     string
	// added are in the root store but not the profile; removed the other
	// way round. Both sorted.
	added   [][]byte
	removed [][]byte
}

// majorOnly says, per browser, whether only a release's major version can
// differ from what its User-Agent shows.
var majorOnly = map[string]bool{
	"chrome":  true,
	"firefox": true,
}

// compareVersions orders dotted numeric versions, a missing component
// counting as 0 ("27" equals "27.0").
func compareVersions(a, b string) (int, error) {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		var x, y int

		var err error

		if i < len(as) {
			if x, err = strconv.Atoi(as[i]); err != nil {
				return 0, fmt.Errorf("version %q: %w", a, err)
			}
		}

		if i < len(bs) {
			if y, err = strconv.Atoi(bs[i]); err != nil {
				return 0, fmt.Errorf("version %q: %w", b, err)
			}
		}

		if c := cmp.Compare(x, y); c != 0 {
			return c, nil
		}
	}

	return 0, nil
}

// findBehind lists the releases newer than the newest profile of their
// browser on their platform. A release with no profile on its platform is
// not reported: there is nothing to refresh.
func findBehind(releases []release, builtins []builtin) ([]behind, error) {
	var out []behind

	for _, r := range releases {
		i := slices.IndexFunc(builtins, func(b builtin) bool {
			return b.browser == r.browser && b.platform == r.platform
		})
		if i < 0 {
			continue
		}

		v := r.version
		if majorOnly[r.browser] {
			v, _, _ = strings.Cut(v, ".")
		}

		c, err := compareVersions(v, builtins[i].version)
		if err != nil {
			return nil, fmt.Errorf("%s on %s: %w", r.browser, r.platform, err)
		}

		if c > 0 {
			out = append(out, behind{
				release: r,
				profile: builtins[i],
			})
		}
	}

	return out, nil
}

// diffAnchors compares a profile's trust anchor IDs with the root store's.
func diffAnchors(profile, store [][]byte) (added, removed [][]byte) {
	has := func(list [][]byte, id []byte) bool {
		return slices.ContainsFunc(list, func(x []byte) bool {
			return bytes.Equal(x, id)
		})
	}

	for _, id := range store {
		if !has(profile, id) {
			added = append(added, id)
		}
	}

	for _, id := range profile {
		if !has(store, id) {
			removed = append(removed, id)
		}
	}

	slices.SortFunc(added, bytes.Compare)
	slices.SortFunc(removed, bytes.Compare)

	return added, removed
}

// chromeVersion is a full Chrome version, which names its Chromium tag.
var chromeVersion = regexp.MustCompile(`^\d+(?:\.\d+){3}$`)

// parseChrome reads chromiumdash's fetch_releases response: the newest
// stable version first. The version goes into the root store's URL and the
// issue body, so anything but a full version fails.
func parseChrome(data []byte) (string, error) {
	var releases []struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return "", fmt.Errorf("chromiumdash: %w", err)
	}

	if len(releases) == 0 || releases[0].Version == "" {
		return "", errors.New("chromiumdash: no stable release")
	}

	if !chromeVersion.MatchString(releases[0].Version) {
		return "", fmt.Errorf("chromiumdash: version %q is not a Chrome version", releases[0].Version)
	}

	return releases[0].Version, nil
}

// dottedVersion is a version of dot-separated numbers.
var dottedVersion = regexp.MustCompile(`^\d+(?:\.\d+)*$`)

// parseFirefox reads Mozilla product-details' firefox_versions.json: the
// desktop release, Windows and macOS alike.
func parseFirefox(data []byte) (string, error) {
	var versions struct {
		Latest string `json:"LATEST_FIREFOX_VERSION"` //nolint:tagliatelle // product-details' key
	}
	if err := json.Unmarshal(data, &versions); err != nil {
		return "", fmt.Errorf("product-details: %w", err)
	}

	if versions.Latest == "" {
		return "", errors.New("product-details: no LATEST_FIREFOX_VERSION")
	}

	// It goes into the issue body: anything but numbers fails.
	if !dottedVersion.MatchString(versions.Latest) {
		return "", fmt.Errorf("product-details: LATEST_FIREFOX_VERSION %q is not a version", versions.Latest)
	}

	return versions.Latest, nil
}

// safariTitle is a released version's page in Apple's Safari release notes;
// a beta's title says Beta, and does not match.
var safariTitle = regexp.MustCompile(`^Safari (\d+(?:\.\d+)*) Release Notes$`)

// parseSafari reads Apple's Safari release notes index: the highest version
// with release notes. Safari on macOS and iOS share them; a bug-fix release
// (27.0.1) has none, so a profile at one is never behind its notes'
// version.
func parseSafari(data []byte) (string, error) {
	var notes struct {
		References map[string]struct {
			Title string `json:"title"`
		} `json:"references"`
	}
	if err := json.Unmarshal(data, &notes); err != nil {
		return "", fmt.Errorf("safari release notes: %w", err)
	}

	newest := ""

	for _, ref := range notes.References {
		m := safariTitle.FindStringSubmatch(ref.Title)
		if m == nil {
			continue
		}

		if newest == "" {
			newest = m[1]
			continue
		}

		c, err := compareVersions(m[1], newest)
		if err != nil {
			return "", fmt.Errorf("safari release notes: %w", err)
		}

		if c > 0 {
			newest = m[1]
		}
	}

	if newest == "" {
		return "", errors.New("safari release notes: no released version")
	}

	return newest, nil
}

// anchorLine is a trust_anchor_id field of root_store.textproto, a quoted
// protobuf text-format string, then perhaps a comment.
var anchorLine = regexp.MustCompile(`(?m)^\s*trust_anchor_id:\s*("(?:[^"\\]|\\.)*")`)

// parseRootStore reads the trust anchor IDs of Chromium's
// net/data/ssl/chrome_root_store/root_store.textproto, in file order.
func parseRootStore(data []byte) ([][]byte, error) {
	var ids [][]byte

	for _, m := range anchorLine.FindAllSubmatch(data, -1) {
		// Chromium writes each byte as \xHH, which Go's quoting reads
		// alike; anything else it cannot read fails loudly.
		s, err := strconv.Unquote(string(m[1]))
		if err != nil {
			return nil, fmt.Errorf("root store: trust_anchor_id %s: %w", m[1], err)
		}

		if s == "" {
			return nil, errors.New("root store: empty trust_anchor_id")
		}

		ids = append(ids, []byte(s))
	}

	if len(ids) == 0 {
		return nil, errors.New("root store: no trust_anchor_id")
	}

	return ids, nil
}

// parseTrustAnchors reads the body of the trust anchors extension (0xca34):
// a two-byte length, then each ID with a one-byte length.
func parseTrustAnchors(data []byte) ([][]byte, error) {
	if len(data) < 2 || int(binary.BigEndian.Uint16(data)) != len(data)-2 {
		return nil, fmt.Errorf("trust anchors extension %x: bad length", data)
	}

	var ids [][]byte

	for rest := data[2:]; len(rest) > 0; {
		n := int(rest[0])
		if n == 0 || n > len(rest)-1 {
			return nil, fmt.Errorf("trust anchors extension %x: bad ID length", data)
		}

		ids = append(ids, rest[1:1+n])
		rest = rest[1+n:]
	}

	return ids, nil
}

// anchorName shows a trust anchor ID as the issue and Chromium do: its
// bytes in hex, then the relative OID they encode (11129.9.2).
func anchorName(id []byte) string {
	var (
		arcs []string
		arc  uint64
	)

	for _, b := range id {
		arc = arc<<7 | uint64(b&0x7f)
		if b&0x80 == 0 {
			arcs = append(arcs, strconv.FormatUint(arc, 10))
			arc = 0
		}
	}

	return "`" + hex.EncodeToString(id) + "` (" + strings.Join(arcs, ".") + ")"
}

// Display names in the report.
var (
	browserNames = map[string]string{
		"chrome":  "Chrome",
		"firefox": "Firefox",
		"safari":  "Safari",
	}
	platformNames = map[string]string{
		"macos":   "macOS",
		"windows": "Windows",
		"ios":     "iOS",
	}
)

// report is the issue body for what was found, "" when nothing was.
func report(stale []behind, diffs []anchorDiff) string {
	if len(stale) == 0 && len(diffs) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("Found by the weekly release watch (`.github/workflows/release-watch.yml`, which runs `go run ./cmd/releasewatch` in `internal/harness/`). " +
		"It never changes a profile: a refresh needs a capture of the new release (decision 0003). See `docs/refreshing-profiles.md`. " +
		"The watch rewrites this description while it finds something, and closes the issue once it finds nothing.\n")

	if len(stale) > 0 {
		b.WriteString("\n## Newer releases\n\n")
		b.WriteString("| Browser | Platform | Stable | Newest profile | Its version |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")

		for _, s := range stale {
			fmt.Fprintf(&b, "| %s | %s | %s | `%s` | %s |\n",
				browserNames[s.release.browser], platformNames[s.release.platform], s.release.version, s.profile.name, s.profile.version)
		}
	}

	if len(diffs) > 0 {
		b.WriteString("\n## Chrome Root Store trust anchors\n\n")
		b.WriteString("The `trust_anchor_id`s of `net/data/ssl/chrome_root_store/root_store.textproto` at the platform's stable Chrome tag, " +
			"against the trust anchors extension (0xca34) of the newest Chrome profile. " +
			"Added: in the root store, not the profile; removed: in the profile, not the root store.\n\n")
		b.WriteString("| Profile | Stable tag | Added | Removed |\n")
		b.WriteString("| --- | --- | --- | --- |\n")

		for _, d := range diffs {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", d.profile, d.tag, anchorList(d.added), anchorList(d.removed))
		}
	}

	return b.String()
}

// anchorList joins anchorNames, or says none.
func anchorList(ids [][]byte) string {
	if len(ids) == 0 {
		return "none"
	}

	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = anchorName(id)
	}

	return strings.Join(names, ", ")
}
