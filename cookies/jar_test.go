package cookies

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/profile"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// nav is a typed-URL navigation to u.
func nav(t *testing.T, u string) Request {
	t.Helper()
	return Request{
		URL:        mustURL(t, u),
		Method:     "GET",
		Navigation: true,
	}
}

// RFC 6265bis rules every browser shares: what a jar must refuse to
// store, so that it never sends a cookie the browser would have dropped.
func TestSetRejects(t *testing.T) {
	for _, tt := range []struct {
		url, line, why string
	}{
		{"https://www.example.com/", "a=1; Domain=other.com", "does not match"},
		{"https://www.example.com/", "a=1; Domain=com", "public suffix"},
		{"https://www.example.co.uk/", "a=1; Domain=co.uk", "public suffix"},
		{"http://www.example.com/", "a=1; Secure", "insecure URL"},
		{"https://www.example.com/", "__Secure-a=1", "__Secure-"},
		{"https://www.example.com/", "__Host-a=1; Secure; Domain=example.com", "__Host-"},
		{"https://www.example.com/", "__Host-a=1; Secure; Path=/x", "__Host-"},
		{"https://www.example.com/", "__host-a=1; Secure; Path=/x", "__Host-"},
		{"https://www.example.com/", "a=1; SameSite=None", "without Secure"},
		{"https://www.example.com/", "a=1; Partitioned", "without Secure"},
		{"https://www.example.com/", "a=" + strings.Repeat("x", 4096), "4096"},
		{"https://www.example.com/", "a=1\x00", "control"},
		{"https://www.example.com/", "=", "empty"},
	} {
		var got Event
		j := New(Config{
			OnSetCookie: func(e Event) {
				got = e
			},
		})
		j.SetCookies(nav(t, tt.url), []string{tt.line})
		if got.Cookie != nil || !strings.Contains(got.Rejected, tt.why) {
			t.Errorf("%s from %s: stored %v, rejected %q; want rejected for %q", tt.line, tt.url, got.Cookie != nil, got.Rejected, tt.why)
		}
	}
}

// What each request gets: domain and path matching, Secure only over
// HTTPS, host-only cookies on their host only.
func TestCookiesMatch(t *testing.T) {
	j := New(Config{})
	j.SetCookies(nav(t, "https://www.example.com/a/b"), []string{
		"host=1", // host-only, default path /a
		"dom=1; Domain=example.com; Path=/",
		"sec=1; Secure; Path=/",
		"deep=1; Path=/a/b/c",
	})
	for _, tt := range []struct {
		url  string
		want string
	}{
		{"https://www.example.com/a/b", "host=1; dom=1; sec=1"},
		{"https://www.example.com/a", "host=1; dom=1; sec=1"},
		{"https://www.example.com/ab", "dom=1; sec=1"},
		{"https://www.example.com/a/b/c/d", "deep=1; host=1; dom=1; sec=1"},
		{"https://static.example.com/a", "dom=1"},
		{"http://www.example.com/a", "host=1; dom=1"},
		{"https://example.org/", ""},
	} {
		if got := Header(j.Cookies(nav(t, tt.url))); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.url, got, tt.want)
		}
	}
}

// Expiry: Max-Age wins over Expires; an expired line deletes; cookies
// lapse as the clock passes their expiry; a replacement keeps its place.
func TestExpiryAndReplacement(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	j := New(Config{
		Clock: clock,
	})
	r := nav(t, "https://example.com/")
	j.SetCookies(r, []string{
		"a=1",
		"b=1; Max-Age=60; Expires=Wed, 21 Oct 2099 07:28:00 GMT",
		"c=1; Expires=Wed, 21 Oct 2099 07:28:00 GMT",
		"d=1; expires=Thu, 01-Jan-1970 00:00:00 GMT",
	})
	if got := Header(j.Cookies(r)); got != "a=1; b=1; c=1" {
		t.Fatalf("stored %q", got)
	}
	clock.Advance(61 * time.Second)
	j.SetCookies(r, []string{"a=2", "c=; Max-Age=0"})
	if got := Header(j.Cookies(r)); got != "a=2" {
		t.Errorf("after expiry, replacement and deletion: %q", got)
	}
	j.SetCookies(r, []string{"z=1", "a=3"})
	if got := Header(j.Cookies(r)); got != "a=3; z=1" {
		t.Errorf("a replaced cookie keeps its creation order: %q", got)
	}
}

// An insecure URL cannot set a cookie that would shadow a Secure one.
func TestInsecureCannotShadowSecure(t *testing.T) {
	j := New(Config{})
	j.SetCookies(nav(t, "https://example.com/"), []string{"id=secure; Secure"})
	j.SetCookies(nav(t, "http://example.com/"), []string{"id=plain"})
	if got := Header(j.Cookies(nav(t, "https://example.com/"))); got != "id=secure" {
		t.Errorf("got %q", got)
	}
}

// Cookie dates parse as RFC 6265bis says, including the forms servers
// actually send.
func TestParseDate(t *testing.T) {
	want := time.Date(2026, 10, 21, 7, 28, 0, 0, time.UTC)
	for _, s := range []string{
		"Wed, 21 Oct 2026 07:28:00 GMT",
		"Wednesday, 21-Oct-26 07:28:00 GMT",
		"Wed Oct 21 07:28:00 2026",
		"21 Oct 2026 07:28:00",
	} {
		if got, ok := parseDate(s); !ok || !got.Equal(want) {
			t.Errorf("%q: %v %v, want %v", s, got, ok, want)
		}
	}
	for _, s := range []string{"31 Feb 2026 00:00:00", "Oct 2026 07:28:00", "21 Oct 1600 00:00:00", "garbage"} {
		if _, ok := parseDate(s); ok {
			t.Errorf("%q parsed", s)
		}
	}
}

// Without a profile section the jar follows RFC 6265bis: an unset
// SameSite is Lax, so a cross-site subresource does not get it.
func TestDefaultRulesAreLaxByDefault(t *testing.T) {
	j := New(Config{})
	j.SetCookies(nav(t, "https://a.example/"), []string{"unset=1; Secure", "none=1; Secure; SameSite=None"})
	r := Request{
		URL:    mustURL(t, "https://a.example/img"),
		Method: "GET",
		Page:   mustURL(t, "https://b.example/"),
	}
	if got := Header(j.Cookies(r)); got != "none=1" {
		t.Errorf("cross-site subresource: %q", got)
	}
	if j.rules.UnsetSameSite != profile.SameSiteLax {
		t.Errorf("default rules: %+v", j.rules)
	}
}
