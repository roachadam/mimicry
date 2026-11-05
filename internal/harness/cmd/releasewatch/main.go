// Command releasewatch reports what the built-in profiles are behind on:
// a stable browser release newer than the newest profile of its browser on
// its platform (Safari's minor version included), and a Chrome Root Store
// whose trust anchor IDs differ from the trust anchors extension (0xca34)
// of the newest Chrome profiles. It never changes a profile.
//
//	go run ./cmd/releasewatch   # in internal/harness/
//
// It reads chromiumdash (Chrome on macOS, Windows and iOS), Mozilla's
// product-details (Firefox on macOS and Windows; it has no Firefox for iOS
// version), Apple's Safari release notes (Safari on macOS and iOS) and
// Chromium's root_store.textproto at each platform's stable tag. It writes
// the findings as an issue body in Markdown to stdout, nothing when there
// are none, and what it read to stderr; it fails when a feed does.
// .github/workflows/release-watch.yml runs it weekly and keeps one issue
// up to date with its output.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// extTrustAnchors is the trust anchors extension's type (Chrome's
// codepoint).
const extTrustAnchors profile.Code = 0xca34

// rootStoreURL is Chromium's root store at a tag, on GitHub's mirror.
const rootStoreURL = "https://raw.githubusercontent.com/chromium/chromium/%s/net/data/ssl/chrome_root_store/root_store.textproto"

// feed is where one browser's stable version on some platforms is read.
type feed struct {
	browser   string
	platforms []string
	url       string
	parse     func([]byte) (string, error)
}

var feeds = []feed{
	{
		browser:   "chrome",
		platforms: []string{"macos"},
		url:       "https://chromiumdash.appspot.com/fetch_releases?channel=Stable&platform=Mac&num=1",
		parse:     parseChrome,
	},
	{
		browser:   "chrome",
		platforms: []string{"windows"},
		url:       "https://chromiumdash.appspot.com/fetch_releases?channel=Stable&platform=Windows&num=1",
		parse:     parseChrome,
	},
	{
		browser:   "chrome",
		platforms: []string{"linux"},
		url:       "https://chromiumdash.appspot.com/fetch_releases?channel=Stable&platform=Linux&num=1",
		parse:     parseChrome,
	},
	{
		browser:   "chrome",
		platforms: []string{"ios"},
		url:       "https://chromiumdash.appspot.com/fetch_releases?channel=Stable&platform=iOS&num=1",
		parse:     parseChrome,
	},
	{
		browser:   "firefox",
		platforms: []string{"macos", "windows", "linux"},
		url:       "https://product-details.mozilla.org/1.0/firefox_versions.json",
		parse:     parseFirefox,
	},
	{
		browser:   "safari",
		platforms: []string{"macos", "ios"},
		url:       "https://developer.apple.com/tutorials/data/documentation/safari-release-notes.json",
		parse:     parseSafari,
	},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "releasewatch:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	builtins, err := newestBuiltins()
	if err != nil {
		return err
	}

	var releases []release

	for _, f := range feeds {
		data, err := fetch(ctx, client, f.url)
		if err != nil {
			return err
		}

		v, err := f.parse(data)
		if err != nil {
			return err
		}

		for _, p := range f.platforms {
			fmt.Fprintf(os.Stderr, "%s on %s: stable %s\n", f.browser, p, v)

			releases = append(releases, release{
				browser:  f.browser,
				platform: p,
				version:  v,
			})
		}
	}

	stale, err := findBehind(releases, builtins)
	if err != nil {
		return err
	}

	diffs, err := checkAnchors(ctx, client, releases, builtins)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "%d release(s) newer than a profile, %d Chrome profile(s) with other trust anchors than the root store\n", len(stale), len(diffs))
	fmt.Print(report(stale, diffs))

	return nil
}

// checkAnchors compares each newest Chrome profile sending trust anchors
// with the root store at its platform's stable tag.
func checkAnchors(ctx context.Context, client *http.Client, releases []release, builtins []builtin) ([]anchorDiff, error) {
	stores := map[string][][]byte{}

	var diffs []anchorDiff

	for _, b := range builtins {
		if b.browser != "chrome" || b.anchors == nil {
			continue
		}

		tag := ""

		for _, r := range releases {
			if r.browser == b.browser && r.platform == b.platform {
				tag = r.version
			}
		}

		if tag == "" {
			continue
		}

		store, ok := stores[tag]
		if !ok {
			data, err := fetch(ctx, client, fmt.Sprintf(rootStoreURL, tag))
			if err != nil {
				return nil, err
			}

			if store, err = parseRootStore(data); err != nil {
				return nil, fmt.Errorf("chrome %s: %w", tag, err)
			}

			stores[tag] = store
		}

		fmt.Fprintf(os.Stderr, "%s: %d trust anchors; Chrome %s's root store: %d\n", b.name, len(b.anchors), tag, len(store))

		added, removed := diffAnchors(b.anchors, store)
		if len(added) > 0 || len(removed) > 0 {
			diffs = append(diffs, anchorDiff{
				profile: b.name,
				tag:     tag,
				added:   added,
				removed: removed,
			})
		}
	}

	return diffs, nil
}

// safariVersion is the version Safari's User-Agent shows.
var safariVersion = regexp.MustCompile(`Version/(\d+(?:\.\d+)*)`)

// newestBuiltins is the newest supported built-in profile of each browser
// on each platform.
func newestBuiltins() ([]builtin, error) {
	type key struct {
		browser  string
		platform string
	}

	newest := map[key]*profile.Spec{}
	order := []key{}

	for _, name := range profiles.Supported() {
		p, err := profiles.ByName(name)
		if err != nil {
			return nil, err
		}

		s := p.Spec()

		v, err := strconv.Atoi(s.Version)
		if err != nil {
			return nil, fmt.Errorf("%s: version %q: %w", name, s.Version, err)
		}

		k := key{
			browser:  s.Browser,
			platform: s.Platform,
		}

		have, ok := newest[k]
		if !ok {
			order = append(order, k)
		} else if hv, _ := strconv.Atoi(have.Version); hv >= v {
			continue
		}

		newest[k] = s
	}

	out := make([]builtin, 0, len(order))

	for _, k := range order {
		s := newest[k]
		b := builtin{
			name:     s.Name,
			browser:  s.Browser,
			platform: s.Platform,
			version:  s.Version,
		}

		if s.Browser == "safari" {
			p, _ := profiles.ByName(s.Name)

			m := safariVersion.FindStringSubmatch(p.UserAgent())
			if m == nil {
				return nil, fmt.Errorf("%s: no Version/ in its User-Agent %q", s.Name, p.UserAgent())
			}

			b.version = m[1]
		}

		for _, e := range s.TLS.Extensions {
			if e.Type != extTrustAnchors {
				continue
			}

			ids, err := parseTrustAnchors(e.Data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", s.Name, err)
			}

			b.anchors = ids
		}

		out = append(out, b)
	}

	return out, nil
}

// fetch GETs url, failing on any status but 200.
func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	data, err := readLimited(resp.Body, maxResponse)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}

	return data, nil
}

// maxResponse bounds a feed's response; root_store.textproto, the largest,
// is about 70 KB.
const maxResponse = 16 << 20

// readLimited reads r to its end, failing when it holds more than limit
// bytes rather than returning a truncated body (a root store cut short
// would report anchors as removed).
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}

	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response over %d bytes", limit)
	}

	return data, nil
}
