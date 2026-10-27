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

func newJar(t *testing.T, cfg Config) *Jar {
	t.Helper()
	j, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// rules are RFC 6265bis's rules changed by edit, for one browser policy.
func rules(edit func(*profile.Cookies)) *profile.Cookies {
	r := profile.DefaultCookies()
	edit(r)
	return r
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

// setOne stores line from r and returns its event.
func setOne(j *Jar, r Request, line string) Event {
	var got Event
	hook := j.onSetCookie
	j.onSetCookie = func(e Event) {
		got = e
	}
	j.SetCookies(r, []string{line})
	j.onSetCookie = hook
	return got
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
		{"https://www.example.com/", "__Host-a=1; Secure; Domain=example.com; Path=/", "__Host-"},
		{"https://www.example.com/", "__Host-a=1; Secure; Path=/x", "__Host-"},
		{"https://www.example.com/", "__host-a=1; Secure; Path=/x", "__Host-"},
		// __Host- needs the Path attribute, not a default path of "/".
		{"https://www.example.com/", "__Host-a=1; Secure", "__Host-"},
		{"https://www.example.com/", "__Host-a=1; Secure; Path=x", "__Host-"},
		{"https://www.example.com/", "__Host-a=1; Secure; Path=/; Path=x", "__Host-"},
		// A nameless cookie must not pass for a prefixed one.
		{"https://www.example.com/", "=__Secure-y=1; Secure", "nameless"},
		{"https://www.example.com/", "= \t__host-y=1; Secure; Path=/", "nameless"},
		{"http://www.example.com/", "=__SECURE-y=1", "nameless"},
		// Every browser refuses a nameless pair whose value holds "=".
		{"https://www.example.com/", "=a=1; Secure", "holds \"=\""},
		// __Http- and __Host-Http- need Secure and HttpOnly.
		{"https://www.example.com/", "__Http-a=1; Secure; Path=/", "__Http-"},
		{"https://www.example.com/", "__http-a=1; HttpOnly; Path=/", "__Http-"},
		{"https://www.example.com/", "__Host-Http-a=1; Secure; Path=/", "__Http-"},
		{"https://www.example.com/", "__Host-Http-a=1; Secure; HttpOnly", "__Host-"},
		{"https://www.example.com/", "a=1; SameSite=None", "without Secure"},
		{"https://www.example.com/", "a=1; Partitioned", "without Secure"},
		{"https://www.example.com/", "a=" + strings.Repeat("x", 4096), "4096"},
		{"https://www.example.com/", "a=1\x00", "control"},
		{"https://www.example.com/", "=", "empty"},
	} {
		got := setOne(newJar(t, Config{}), nav(t, tt.url), tt.line)
		if got.Cookie != nil || !strings.Contains(got.Rejected, tt.why) {
			t.Errorf("%s from %s: stored %v, rejected %q; want rejected for %q", tt.line, tt.url, got.Cookie != nil, got.Rejected, tt.why)
		}
	}
}

// Parsing edges: escaped paths, last Path/Domain wins even when invalid,
// Max-Age syntax, and RFC 6265bis dates in the forms servers send.
func TestParseEdges(t *testing.T) {
	t.Run("escaped path", func(t *testing.T) {
		j := newJar(t, Config{})
		j.SetCookies(nav(t, "https://example.com/"), []string{
			"cafe=1; Path=/caf%C3%A9",
			"space=1; Path=/a%20b",
		})
		j.SetCookies(nav(t, "https://example.com/x%2Fy/z"), []string{"slash=1"})
		for _, tt := range []struct {
			url  string
			want string
		}{
			{"https://example.com/caf%C3%A9/x", "cafe=1"},
			{"https://example.com/a%20b", "space=1"},
			{"https://example.com/x%2Fy/q", "slash=1"},
			{"https://example.com/x/y/q", ""},
		} {
			if got := Header(j.Cookies(nav(t, tt.url))); got != tt.want {
				t.Errorf("%s: %q, want %q", tt.url, got, tt.want)
			}
		}
	})
	t.Run("last attribute wins", func(t *testing.T) {
		for _, tt := range []struct {
			line     string
			path     string
			hostOnly bool
		}{
			{"a=1; Path=/p; Path=bad", "/d", true},
			{"a=1; Path=bad; Path=/p", "/p", true},
			{"a=1; Domain=example.com; Domain=", "/d", true},
			{"a=1; Domain=; Domain=example.com", "/d", false},
		} {
			got := setOne(newJar(t, Config{}), nav(t, "https://www.example.com/d/e"), tt.line)
			if got.Cookie == nil {
				t.Errorf("%s rejected: %s", tt.line, got.Rejected)
				continue
			}
			if got.Cookie.Path != tt.path || got.Cookie.HostOnly != tt.hostOnly {
				t.Errorf("%s: path %q host-only %v, want %q %v", tt.line, got.Cookie.Path, got.Cookie.HostOnly, tt.path, tt.hostOnly)
			}
		}
	})
	t.Run("max-age syntax", func(t *testing.T) {
		for _, tt := range []struct {
			line    string
			deleted bool
		}{
			{"a=1; Max-Age=-", false},
			{"a=1; Max-Age=-x1", false},
			{"a=1; Max-Age=--1", false},
			{"a=1; Max-Age=1x", false},
			{"a=1; Max-Age=-1", true},
			{"a=1; Max-Age=0", true},
			{"a=1; Max-Age=-99999999999999999999", true},
		} {
			got := setOne(newJar(t, Config{}), nav(t, "https://example.com/"), tt.line)
			if got.Deleted != tt.deleted {
				t.Errorf("%s: deleted %v, want %v (%+v)", tt.line, got.Deleted, tt.deleted, got)
			}
			if !tt.deleted && (got.Cookie == nil || !got.Cookie.Expires.IsZero()) {
				t.Errorf("%s: want a session cookie, got %+v", tt.line, got)
			}
		}
	})
	t.Run("dates", func(t *testing.T) {
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
	})
}

// Expiry: Max-Age wins over Expires; an expired line deletes; cookies
// lapse as the clock passes their expiry; a replacement keeps its place.
func TestExpiryAndReplacement(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	j := newJar(t, Config{
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

// Expires and Max-Age both expire at most 400 days after creation.
func TestExpiryCappedAt400Days(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	limit := now.Add(400 * 24 * time.Hour)
	for _, tt := range []struct {
		line string
		want time.Time
	}{
		{"e=1; Expires=Wed, 21 Oct 2099 07:28:00 GMT", limit},
		{"m=1; Max-Age=99999999999999999999", limit},
		{"m=1; Max-Age=34560001", limit},
		{"e=1; Expires=Sat, 24 Oct 2026 13:00:00 GMT", now.Add(time.Hour)},
	} {
		j := newJar(t, Config{
			Clock: clockwork.NewFakeClockAt(now),
		})
		got := setOne(j, nav(t, "https://example.com/"), tt.line)
		if got.Cookie == nil || !got.Cookie.Expires.Equal(tt.want) {
			t.Errorf("%s: %+v, want expiry %v", tt.line, got, tt.want)
		}
	}
}

// An insecure URL cannot set a cookie that would shadow a Secure one.
func TestInsecureCannotShadowSecure(t *testing.T) {
	j := newJar(t, Config{})
	j.SetCookies(nav(t, "https://example.com/"), []string{"id=secure; Secure"})
	j.SetCookies(nav(t, "http://example.com/"), []string{"id=plain"})
	if got := Header(j.Cookies(nav(t, "https://example.com/"))); got != "id=secure" {
		t.Errorf("got %q", got)
	}
}

// Chrome: an unset-SameSite cookie goes on a cross-site top-level POST
// for its first 2 minutes; an explicit Lax one never does.
func TestLaxAllowsUnsafeWindow(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	j := newJar(t, Config{
		Rules: rules(func(r *profile.Cookies) {
			r.LaxAllowsUnsafeSeconds = 120
		}),
		Clock: clock,
	})
	j.SetCookies(nav(t, "https://a.example/"), []string{"unset=1", "lax=1; SameSite=Lax"})
	post := Request{
		URL:        mustURL(t, "https://a.example/form"),
		Method:     "POST",
		Navigation: true,
		Page:       mustURL(t, "https://b.example/"),
	}
	clock.Advance(119 * time.Second)
	if got := Header(j.Cookies(post)); got != "unset=1" {
		t.Errorf("at 119 s: %q, want unset=1", got)
	}
	clock.Advance(time.Second)
	if got := Header(j.Cookies(post)); got != "" {
		t.Errorf("at 120 s: %q, want none", got)
	}
	get := post
	get.Method = "GET"
	if got := Header(j.Cookies(get)); got != "unset=1; lax=1" {
		t.Errorf("cross-site GET navigation: %q", got)
	}
}
