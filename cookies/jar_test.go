package cookies

import (
	"fmt"
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

// sub is a GET subresource of page.
func sub(t *testing.T, u, page string) Request {
	t.Helper()
	return Request{
		URL:    mustURL(t, u),
		Method: "GET",
		Page:   mustURL(t, page),
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

// The prefix rules leave cookies that meet them alone.
func TestPrefixesAccepted(t *testing.T) {
	for _, line := range []string{
		"__Host-a=1; Secure; Path=/",
		"__Secure-a=1; Secure; Domain=example.com",
		"=x__Secure-y",
		"a=__Host-x",
	} {
		if got := setOne(newJar(t, Config{}), nav(t, "https://www.example.com/d/e"), line); got.Cookie == nil {
			t.Errorf("%s rejected: %s", line, got.Rejected)
		}
	}
}

// What each request gets: domain and path matching, Secure only over
// HTTPS, host-only cookies on their host only.
func TestCookiesMatch(t *testing.T) {
	j := newJar(t, Config{})
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
		{"https://example.com/a", "dom=1"},
		{"http://www.example.com/a", "host=1; dom=1"},
		{"https://example.org/", ""},
	} {
		if got := Header(j.Cookies(nav(t, tt.url))); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.url, got, tt.want)
		}
	}
}

// Paths match as the URL is written, escaped: a cookie path with %XX
// matches the same escapes, and an escaped "/" does not split the default
// path.
func TestEscapedPath(t *testing.T) {
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
}

// The last Path and Domain attributes win, even when invalid: an invalid
// Path is the default path, an empty Domain makes the cookie host-only.
func TestLastAttributeWins(t *testing.T) {
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

// Max-Age is "-" and digits, or digits; anything else is ignored.
func TestMaxAgeSyntax(t *testing.T) {
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

// Without rules the jar follows RFC 6265bis: an unset SameSite is Lax, so
// a cross-site subresource does not get it.
func TestDefaultRulesAreLaxByDefault(t *testing.T) {
	j := newJar(t, Config{})
	j.SetCookies(nav(t, "https://a.example/"), []string{"unset=1; Secure", "none=1; Secure; SameSite=None"})
	if got := Header(j.Cookies(sub(t, "https://a.example/img", "https://b.example/"))); got != "none=1" {
		t.Errorf("cross-site subresource: %q", got)
	}
}

// New refuses rules a profile would not pass.
func TestNewValidatesRules(t *testing.T) {
	for _, r := range []*profile.Cookies{
		{
			UnsetSameSite: profile.SameSiteNone,
		},
		rules(func(r *profile.Cookies) {
			r.ThirdParty = "everything"
		}),
	} {
		if j, err := New(Config{
			Rules: r,
		}); err == nil || j != nil {
			t.Errorf("New(%+v) = %v, %v; want an error", r, j, err)
		}
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

// Third-party policies: what a cross-site subresource stores and gets.
func TestThirdPartyPolicies(t *testing.T) {
	const (
		page  = "https://www.a.example/"
		other = "https://www.c.example/"
		cdn   = "https://cdn.b.example/i"
	)
	lines := []string{
		"none=1; Secure; SameSite=None; Path=/",
		"part=1; Secure; SameSite=None; Partitioned; Path=/",
	}
	for _, tt := range []struct {
		policy string
		// stored: what the third-party response stored.
		stored []string
		// fromPage, fromOther: what cdn gets under page and under other;
		// firstParty: what a navigation to cdn gets.
		fromPage, fromOther, firstParty string
	}{
		{profile.ThirdPartySameSite, []string{"none", "part"}, "first=1; none=1; part=1", "first=1; none=1", "first=1; none=1"},
		{profile.ThirdPartyPartitionAll, []string{"none", "part"}, "none=1; part=1", "", "first=1"},
		{profile.ThirdPartyPartitionedOnly, []string{"part"}, "part=1", "", "first=1"},
	} {
		t.Run(tt.policy, func(t *testing.T) {
			j := newJar(t, Config{
				Rules: rules(func(r *profile.Cookies) {
					r.ThirdParty = tt.policy
				}),
			})
			j.SetCookies(nav(t, "https://cdn.b.example/"), []string{"first=1; Secure; SameSite=None"})
			var stored []string
			j.onSetCookie = func(e Event) {
				if e.Cookie != nil {
					stored = append(stored, e.Cookie.Name)
				}
			}
			j.SetCookies(sub(t, cdn, page), lines)
			if strings.Join(stored, ",") != strings.Join(tt.stored, ",") {
				t.Errorf("stored %v, want %v", stored, tt.stored)
			}
			if got := Header(j.Cookies(sub(t, cdn, page))); got != tt.fromPage {
				t.Errorf("under %s: %q, want %q", page, got, tt.fromPage)
			}
			if got := Header(j.Cookies(sub(t, cdn, other))); got != tt.fromOther {
				t.Errorf("under %s: %q, want %q", other, got, tt.fromOther)
			}
			if got := Header(j.Cookies(nav(t, cdn))); got != tt.firstParty {
				t.Errorf("first-party: %q, want %q", got, tt.firstParty)
			}
		})
	}
}

// Redirect policies: a same-site subresource reached through a cross-site
// hop.
func TestRedirectPolicies(t *testing.T) {
	r := Request{
		URL:       mustURL(t, "https://a.example/img"),
		Method:    "GET",
		Page:      mustURL(t, "https://a.example/"),
		Redirects: []*url.URL{mustURL(t, "https://b.example/r")},
	}
	for _, tt := range []struct {
		policy string
		want   string
		stored bool
	}{
		{profile.RedirectsIgnore, "lax=1; none=1", true},
		{profile.RedirectsChain, "none=1", false},
		{profile.RedirectsBlockAfterCrossSite, "", false},
	} {
		t.Run(tt.policy, func(t *testing.T) {
			j := newJar(t, Config{
				Rules: rules(func(r *profile.Cookies) {
					r.Redirects = tt.policy
				}),
			})
			j.SetCookies(nav(t, "https://a.example/"), []string{"lax=1; SameSite=Lax", "none=1; Secure; SameSite=None"})
			if got := Header(j.Cookies(r)); got != tt.want {
				t.Errorf("sent %q, want %q", got, tt.want)
			}
			if got := setOne(j, r, "new=1; SameSite=Lax"); (got.Cookie != nil) != tt.stored {
				t.Errorf("Lax cookie from the response stored %v, want %v (%s)", got.Cookie != nil, tt.stored, got.Rejected)
			}
		})
	}
}

// A pair without "=": a value (Chrome), a name (Firefox), or nothing
// (Safari).
func TestNamelessPolicies(t *testing.T) {
	for _, tt := range []struct {
		policy string
		want   string
	}{
		{profile.NamelessValue, "knoeq"},
		{profile.NamelessName, "knoeq="},
		{profile.NamelessDrop, ""},
	} {
		j := newJar(t, Config{
			Rules: rules(func(r *profile.Cookies) {
				r.Nameless = tt.policy
			}),
		})
		j.SetCookies(nav(t, "https://example.com/"), []string{"knoeq"})
		if got := Header(j.Cookies(nav(t, "https://example.com/"))); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.policy, got, tt.want)
		}
	}
	// As a value, a pair without "=" must not pass for a prefixed name.
	got := setOne(newJar(t, Config{}), nav(t, "https://example.com/"), "__Host-x; Secure; Path=/")
	if got.Cookie != nil {
		t.Errorf("nameless __Host- value stored")
	}
}

// Safari re-encodes value bytes above 0x7f as Latin-1 characters.
func TestLatin1Values(t *testing.T) {
	for _, latin1 := range []bool{false, true} {
		j := newJar(t, Config{
			Rules: rules(func(r *profile.Cookies) {
				r.Latin1Values = latin1
			}),
		})
		want := "a=café"
		if latin1 {
			want = "a=cafÃ©"
		}
		j.SetCookies(nav(t, "https://example.com/"), []string{"a=café"})
		if got := Header(j.Cookies(nav(t, "https://example.com/"))); got != want {
			t.Errorf("latin1 %v: %q, want %q", latin1, got, want)
		}
	}
}

// Header order: longer paths first, then creation (RFC) or Domain
// cookies first and by name (Safari).
func TestOrderPolicies(t *testing.T) {
	for _, tt := range []struct {
		order string
		want  string
	}{
		{profile.OrderRFC, "deep=1; z=1; y=1; a=1"},
		{profile.OrderDomainName, "deep=1; y=1; a=1; z=1"},
	} {
		j := newJar(t, Config{
			Rules: rules(func(r *profile.Cookies) {
				r.Order = tt.order
			}),
		})
		j.SetCookies(nav(t, "https://www.example.com/"), []string{
			"z=1",
			"y=1; Domain=example.com",
			"a=1",
			"deep=1; Path=/x",
		})
		if got := Header(j.Cookies(nav(t, "https://www.example.com/x/y"))); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.order, got, tt.want)
		}
	}
}

// Cookies of different domains, created at the same time, stay in the
// order they were stored: in All, in a clone, and in the header.
func TestCreationOrderAcrossDomains(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	j := newJar(t, Config{
		Clock: clock,
	})
	var want []string
	for i := range 20 {
		host := []string{"a.example", "b.example", "c.example.co.uk", "127.0.0.1"}[i%4]
		name := "c" + strings.Repeat("x", i)
		j.SetCookies(nav(t, "http://"+host+"/"), []string{name + "=1"})
		want = append(want, host+"/"+name)
	}
	if err := j.Set(Cookie{
		Name:    "old",
		Value:   "1",
		Domain:  "a.example",
		Created: clock.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	want = append([]string{"a.example/old"}, want...)
	for _, jar := range []*Jar{j, j.Clone()} {
		var got []string
		for _, c := range jar.All() {
			got = append(got, c.Domain+"/"+c.Name)
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("All:\n%v\nwant\n%v", got, want)
		}
	}
	if got := Header(j.Cookies(nav(t, "http://a.example/"))); !strings.HasPrefix(got, "old=1; c=1; cxxxx=1") {
		t.Errorf("header %q", got)
	}
}

// Safari ignores the __Http- prefixes and checks __Host-'s Path=/ on the
// path stored, so a cookie without Path whose default path is "/" is kept;
// RFC 6265bis (Chrome, Firefox) refuses both.
func TestPrefixPolicies(t *testing.T) {
	safari := rules(func(r *profile.Cookies) {
		r.HTTPPrefix = false
		r.HostPrefixPath = profile.HostPrefixPathStored
	})
	for _, tt := range []struct {
		url, line   string
		rfc, safari bool
	}{
		{"https://www.example.com/", "__Http-a=1; Secure; HttpOnly; Path=/", true, true},
		{"https://www.example.com/", "__Http-a=1; Secure; Path=/", false, true},
		{"https://www.example.com/", "__Host-Http-a=1; Secure; HttpOnly; Path=/", true, true},
		{"https://www.example.com/", "__Host-Http-a=1; Secure; Path=/", false, true},
		{"https://www.example.com/", "__Host-a=1; Secure", false, true},
		{"https://www.example.com/d/e", "__Host-a=1; Secure", false, false},
		{"https://www.example.com/", "__Host-a=1; Secure; Path=/x", false, false},
	} {
		for _, r := range []struct {
			name  string
			rules *profile.Cookies
			want  bool
		}{
			{"rfc", nil, tt.rfc},
			{"safari", safari, tt.safari},
		} {
			got := setOne(newJar(t, Config{Rules: r.rules}), nav(t, tt.url), tt.line)
			if (got.Cookie != nil) != r.want {
				t.Errorf("%s: %s from %s: stored %v (%s), want %v", r.name, tt.line, tt.url, got.Cookie != nil, got.Rejected, r.want)
			}
		}
	}
}

// Chrome gives a cookie a new value's creation time, which moves it after
// the cookies set since; the same value again keeps its place. RFC 6265bis
// (Firefox) keeps the creation time either way.
func TestNewValueRenews(t *testing.T) {
	for _, tt := range []struct {
		renews bool
		want   string
	}{
		{false, "a=2; b=1; c=1"},
		{true, "b=1; c=1; a=2"},
	} {
		clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
		j := newJar(t, Config{
			Rules: rules(func(r *profile.Cookies) {
				r.NewValueRenews = tt.renews
			}),
			Clock: clock,
		})
		page := nav(t, "https://example.com/")
		for _, line := range []string{"a=1", "b=1", "c=1", "a=2", "b=1"} {
			j.SetCookies(page, []string{line})
			clock.Advance(time.Second)
		}
		if got := Header(j.Cookies(page)); got != tt.want {
			t.Errorf("renews %v: %q, want %q", tt.renews, got, tt.want)
		}
	}
}

// Chrome judges SameSite on schemeful sites: a navigation between https://
// and http:// of one host is cross-site, so Strict cookies stay behind.
// Firefox leaves the scheme out.
func TestSameSiteScheme(t *testing.T) {
	for _, tt := range []struct {
		scheme string
		toHTTP string
		toBack string
	}{
		{profile.SameSiteSchemeful, "lax=1; unset=1", "lax=1; unset=1; secure=1"},
		{profile.SameSiteSchemeless, "strict=1; lax=1; unset=1", "strict=1; lax=1; unset=1; secure=1"},
	} {
		j := newJar(t, Config{
			Rules: rules(func(r *profile.Cookies) {
				r.SameSiteScheme = tt.scheme
			}),
		})
		j.SetCookies(nav(t, "https://www.example.com/"), []string{
			"strict=1; SameSite=Strict",
			"lax=1; SameSite=Lax",
			"unset=1",
			"secure=1; Secure; SameSite=Lax",
		})
		toHTTP := nav(t, "http://www.example.com/e")
		toHTTP.Page = mustURL(t, "https://www.example.com/")
		if got := Header(j.Cookies(toHTTP)); got != tt.toHTTP {
			t.Errorf("%s: to http: %q, want %q", tt.scheme, got, tt.toHTTP)
		}
		back := nav(t, "https://www.example.com/e")
		back.Page = toHTTP.URL
		if got := Header(j.Cookies(back)); got != tt.toBack {
			t.Errorf("%s: back to https: %q, want %q", tt.scheme, got, tt.toBack)
		}
		// Same-origin on the http:// page: same-site either way.
		if got := Header(j.Cookies(sub(t, "http://www.example.com/p", "http://www.example.com/e"))); got != "strict=1; lax=1; unset=1" {
			t.Errorf("%s: http probe: %q", tt.scheme, got)
		}
	}
}

// Chrome, Firefox and Safari 27 keep a Secure cookie set by
// http://localhost and send it back there; Safari 26 refuses it.
func TestSecureLoopback(t *testing.T) {
	for _, loopback := range []bool{false, true} {
		for _, host := range []string{"localhost", "a.localhost", "127.0.0.1", "[::1]"} {
			j := newJar(t, Config{
				Rules: rules(func(r *profile.Cookies) {
					r.SecureLoopback = loopback
				}),
			})
			page := nav(t, "http://"+host+"/")
			j.SetCookies(page, []string{"s=1; Secure", "p=1"})
			want := "p=1"
			if loopback {
				want = "s=1; p=1"
			}
			if got := Header(j.Cookies(page)); got != want {
				t.Errorf("loopback %v, %s: %q, want %q", loopback, host, got, want)
			}
		}
	}
	// Other hosts stay insecure over http.
	j := newJar(t, Config{})
	if got := setOne(j, nav(t, "http://example.com/"), "s=1; Secure"); got.Cookie != nil {
		t.Error("Secure cookie stored from http://example.com")
	}
}

// Chrome and Firefox keep 180 cookies per site: the 181st evicts the least
// recently used down to 150, so 200 cookies set at once leave the newest
// 169. Without a limit (Safari) all 200 stay.
func TestSiteLimit(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("m%03d=1", i)
	}
	for _, tt := range []struct {
		max, evictTo int
		n            int
		first        string
	}{
		{0, 0, 200, "m000=1"},
		{180, 150, 169, "m031=1"},
	} {
		j := newJar(t, Config{
			Rules: rules(func(r *profile.Cookies) {
				r.MaxPerSite, r.EvictTo = tt.max, tt.evictTo
			}),
		})
		page := nav(t, "https://www.example.com/")
		j.SetCookies(page, lines)
		got := j.Cookies(page)
		if len(got) != tt.n {
			t.Fatalf("limit %d: %d cookies, want %d", tt.max, len(got), tt.n)
		}
		if got[0].pair() != tt.first || got[len(got)-1].Name != "m199" {
			t.Errorf("limit %d: %s first, %s last", tt.max, got[0].pair(), got[len(got)-1].Name)
		}
		// Another site's cookies do not count.
		j.SetCookies(nav(t, "https://other.example/"), []string{"o=1"})
		if all := len(j.All()); all != tt.n+1 {
			t.Errorf("limit %d: %d cookies in all", tt.max, all)
		}
	}
}

// Eviction takes the least recently used: a cookie just sent outlives
// older ones set after it.
func TestSiteLimitEvictsLeastRecentlyUsed(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	j := newJar(t, Config{
		Rules: rules(func(r *profile.Cookies) {
			r.MaxPerSite, r.EvictTo = 4, 2
		}),
		Clock: clock,
	})
	page := nav(t, "https://www.example.com/")
	j.SetCookies(page, []string{"used=1; Path=/u", "a=1; Path=/a", "b=1; Path=/a", "c=1; Path=/a"})
	clock.Advance(time.Second)
	j.Cookies(nav(t, "https://www.example.com/u"))
	clock.Advance(time.Second)
	j.SetCookies(page, []string{"d=1; Path=/a"})
	all := j.All()
	names := make([]string, 0, len(all))
	for _, c := range all {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, " "); got != "used d" {
		t.Errorf("kept %q, want %q", got, "used d")
	}
}
