package capture

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

func TestParseUserAgent(t *testing.T) {
	for _, tc := range []struct {
		ua                         string
		browser, version, platform string
	}{
		{
			ua:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36",
			browser:  "chrome",
			version:  "155",
			platform: "macos",
		},
		{
			ua:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36 Edg/155.0.0.0",
			browser:  "edge",
			version:  "155",
			platform: "windows",
		},
		{
			ua:       "Mozilla/5.0 (X11; Linux x86_64; rv:148.0) Gecko/20100101 Firefox/148.0",
			browser:  "firefox",
			version:  "148",
			platform: "linux",
		},
		{
			ua:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.3 Safari/605.1.15",
			browser:  "safari",
			version:  "26",
			platform: "macos",
		},
		{
			ua:       "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1",
			browser:  "safari",
			version:  "18",
			platform: "ios",
		},
		{
			ua:       "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Mobile Safari/537.36",
			browser:  "chrome",
			version:  "155",
			platform: "android",
		},
		{
			ua:       "curl/8.7.1",
			browser:  "unknown",
			version:  "unknown",
			platform: "unknown",
		},
	} {
		browser, version, platform := ParseUserAgent(tc.ua)
		if browser != tc.browser || version != tc.version || platform != tc.platform {
			t.Errorf("%s:\n got %s %s %s\nwant %s %s %s", tc.ua, browser, version, platform, tc.browser, tc.version, tc.platform)
		}
	}
}

// Browsers open several connections; only the first capture of an identity is
// kept, and Write must never overwrite it.
func TestWriteNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	c := &Capture{
		Profile: profile.Profile{
			Name: "chrome-155-macos",
			Headers: []profile.Header{
				{
					Name:  "user-agent",
					Value: "first",
				},
			},
		},
		ClientHello: []byte{
			1,
			2,
		},
	}
	path, err := Write(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "chrome-155-macos.json") {
		t.Errorf("path %s", path)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := os.ReadFile(filepath.Join(dir, "chrome-155-macos.clienthello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(hello) != "0102\n" {
		t.Errorf("clienthello file %q, want hex of the raw message", hello)
	}

	c.Profile.Headers[0].Value = "second"
	if _, err := Write(dir, c); !errors.Is(err, ErrExists) {
		t.Fatalf("second Write error = %v, want ErrExists", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(first) {
		t.Error("second Write changed the existing capture")
	}
}

// Retaking a capture whose later file was left behind writes nothing, so a
// new trace never sits next to an old one; CheckNew sees the same conflict.
func TestWriteWritesNothingWhenAnyOutputExists(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "chrome-155-macos.proxy.proxytrace.json")
	if err := os.WriteFile(left, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Capture{
		Profile: profile.Profile{
			Name: "chrome-155-macos",
		},
		Scenario: "proxy",
		H1: []H1Request{
			{
				Line: "GET / HTTP/1.1",
			},
		},
		Proxy: []ProxyRequest{
			{},
		},
	}
	if err := CheckNew(dir, c); !errors.Is(err, ErrExists) {
		t.Errorf("CheckNew error = %v, want ErrExists", err)
	}
	if _, err := Write(dir, c); !errors.Is(err, ErrExists) {
		t.Fatalf("Write error = %v, want ErrExists", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "chrome-155-macos.proxy.h1trace.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("h1trace written next to the existing proxytrace (stat error %v)", err)
	}
}

// A replayed page's capture, written with every sample, loads back whole:
// what the HPACK oracle reads is what the capture tool wrote.
func TestWritePagesLoadBack(t *testing.T) {
	dir := t.TempDir()
	sample := func(host string) *Page {
		return &Page{
			H2: []*h2trace.Trace{
				{
					Host: host,
					Requests: []h2trace.Request{
						{
							Stream: 1,
							Path:   "/r1",
						},
					},
				},
			},
			H1: []H1Request{
				{
					Conn: 1,
					Host: "beacon.track-net.test",
					Line: "POST /r6 HTTP/1.1",
				},
			},
		}
	}
	c := &Capture{
		Profile: profile.Profile{
			Name: "chrome-155-macos",
		},
		Scenario: "replay-ex.com",
		SamplePages: []*Page{
			sample("www.ex-com.test"),
			sample("cdn.ex-com.test"),
		},
	}
	path, err := Write(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "chrome-155-macos.replay-ex.com.pagetrace.json.gz") {
		t.Fatalf("path %s", path)
	}
	pages, err := LoadPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pages, c.SamplePages) {
		t.Errorf("loaded %+v, want what was written", pages)
	}
}
