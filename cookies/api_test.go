package cookies

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
)

// fill stores a jar with a host-only, a Domain, an HttpOnly session, a
// Lax, a partitioned third-party cookie, the last under top-level site
// a.example, and an AnySite cookie.
func fill(t *testing.T, j *Jar) {
	t.Helper()
	j.SetCookies(nav(t, "https://www.a.example/"), []string{
		`host="x y"; Max-Age=3600`,
		"dom=1; Domain=a.example; Path=/p; Secure; SameSite=Lax",
		"sess=1; HttpOnly",
	})
	j.SetCookies(Request{
		URL:    mustURL(t, "https://cdn.b.example/i"),
		Method: "GET",
		Page:   mustURL(t, "https://www.a.example/"),
	}, []string{"part=1; Secure; SameSite=None; Partitioned; Path=/"})
	if err := j.SetString(AnySite, "everywhere=1"); err != nil {
		t.Fatal(err)
	}
	if got := len(j.All()); got != 5 {
		t.Fatalf("filled %d cookies, want 5", got)
	}
}

// A jar written and read back sends the same cookies everywhere. JSON keeps
// every attribute, partitioned and AnySite cookies included; Netscape files
// keep what the format holds (domain, host-only, path, Secure, HttpOnly,
// expiry, raw value) and leave partitioned and AnySite cookies out.
func TestExportRoundTrip(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
		j := newJar(t, Config{
			Clock: clock,
		})
		fill(t, j)
		var buf bytes.Buffer
		if err := j.WriteJSON(&buf); err != nil {
			t.Fatal(err)
		}
		k := newJar(t, Config{
			Clock: clock,
		})
		if err := k.ReadJSON(&buf); err != nil {
			t.Fatal(err)
		}
		var again bytes.Buffer
		if err := k.WriteJSON(&again); err != nil {
			t.Fatal(err)
		}
		var first bytes.Buffer
		if err := j.WriteJSON(&first); err != nil {
			t.Fatal(err)
		}
		if first.String() != again.String() {
			t.Errorf("round trip changed cookies:\n%s\n%s", first.String(), again.String())
		}
		if !strings.Contains(first.String(), `"partition": "https://a.example"`) || !strings.Contains(first.String(), `"same_site": "Lax"`) {
			t.Errorf("JSON lacks attributes:\n%s", first.String())
		}
	})
	t.Run("netscape", func(t *testing.T) {
		clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
		j := newJar(t, Config{
			Clock: clock,
		})
		fill(t, j)
		var buf bytes.Buffer
		if err := j.WriteNetscape(&buf); err != nil {
			t.Fatal(err)
		}
		text := buf.String()
		for _, want := range []string{
			"www.a.example\tFALSE\t/\tFALSE\t1792846800\thost\t\"x y\"\n",
			".a.example\tTRUE\t/p\tTRUE\t0\tdom\t1\n",
			"#HttpOnly_www.a.example\tFALSE\t/\tFALSE\t0\tsess\t1\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in:\n%s", want, text)
			}
		}
		if strings.Contains(text, "part") || strings.Contains(text, "everywhere") {
			t.Errorf("partitioned or AnySite cookie written:\n%s", text)
		}
		k := newJar(t, Config{
			Clock: clock,
		})
		if err := k.ReadNetscape(strings.NewReader(text)); err != nil {
			t.Fatal(err)
		}
		for _, u := range []string{"https://www.a.example/p", "https://x.a.example/p"} {
			sent := slices.DeleteFunc(j.Cookies(nav(t, u)), func(c *Cookie) bool {
				return c.Domain == AnySite
			})
			if want, got := Header(sent), Header(k.Cookies(nav(t, u))); got != want {
				t.Errorf("%s: read back sends %q, want %q", u, got, want)
			}
		}
	})
}

// Delete's match runs with the jar unlocked, so it may read the jar, and
// gets copies: changing one changes nothing stored.
func TestDeleteMatchMayUseJar(t *testing.T) {
	j := newJar(t, Config{})
	fill(t, j)
	n := j.Delete(func(c *Cookie) bool {
		others := j.All()
		c.Value = "changed"
		return len(others) == 5 && c.Name == "sess"
	})
	if n != 1 {
		t.Errorf("Delete removed %d, want 1", n)
	}
	for _, c := range j.All() {
		if c.Name == "sess" || c.Value == "changed" {
			t.Errorf("after Delete: %+v", c)
		}
	}
}

// SetString scopes a Cookie header string to a site: every host of its
// registrable domain gets the cookies, no other site does, a host or URL
// reduces to the registrable domain, and AnySite reaches every host of
// every site. Parsing tolerates sloppy strings; input that is not a site
// or a pair Set refuses stores nothing. String gives a site's cookies back
// (any host and path, not partitioned ones), and SetString of that string
// gives the same String: checked for every site of every case. The jar's
// own Cookies is the observer.
func TestSetString(t *testing.T) {
	type set struct {
		site, cookies, err string
	}
	type response struct {
		url   string
		lines []string
	}
	for _, tt := range []struct {
		name string
		// lines are Set-Cookie lines from a navigation to a URL, stored
		// first, in order.
		lines []response
		set   []set
		// sends maps a URL to the Cookie header a navigation to it sends.
		sends map[string]string
		// strs maps a site to its String.
		strs map[string]string
	}{
		{
			name: "a site's cookies go to every host of the site, nowhere else",
			set: []set{
				{"shop.test", "a=1; b=2", ""},
			},
			sends: map[string]string{
				"https://shop.test/":            "a=1; b=2",
				"https://www.shop.test/deep/x":  "a=1; b=2",
				"http://api.eu.shop.test:8080/": "a=1; b=2",
				"https://shop.test.evil/":       "",
				"https://outlet.test/":          "",
				"https://shop.example/":         "",
			},
			strs: map[string]string{
				"shop.test":   "a=1; b=2",
				"outlet.test": "",
			},
		},
		{
			name: "a host or URL is its registrable domain",
			set: []set{
				{"https://WWW.Shop.test:8443/cart?x=1#y", "a=1", ""},
				{"www.shop.test", "b=2", ""},
				{"shop.test.", "c=3", ""},
				{"api.shop.test:443", "d=4", ""},
				{"https://www.shop.co.uk/", "e=5", ""},
			},
			sends: map[string]string{
				"https://cdn.shop.test/": "a=1; b=2; c=3; d=4",
				"https://m.shop.co.uk/":  "e=5",
				"https://other.co.uk/":   "",
			},
			strs: map[string]string{
				"shop.test":                   "a=1; b=2; c=3; d=4",
				"https://img.shop.test/x.png": "a=1; b=2; c=3; d=4",
				"shop.co.uk":                  "e=5",
			},
		},
		{
			name: "sloppy strings parse; a later value replaces",
			set: []set{
				{"shop.test", "  a=0;b=2 ;; c = 3 ; knoeq;", ""},
				{"shop.test", "a=1", ""},
			},
			sends: map[string]string{
				"https://www.shop.test/": "a=1; b=2; c=3; knoeq",
			},
			strs: map[string]string{
				"shop.test": "a=1; b=2; c=3; knoeq",
			},
		},
		{
			name: "loopback names and IP addresses are sites of their own",
			set: []set{
				{"localhost", "a=1", ""},
				{"http://127.0.0.1:8080/", "b=2", ""},
				{"[::1]", "c=3", ""},
			},
			sends: map[string]string{
				"http://localhost:9000/": "a=1",
				"http://a.localhost/":    "",
				"http://127.0.0.1/":      "b=2",
				"http://127.0.0.2/":      "",
				"http://[::1]/":          "c=3",
			},
			strs: map[string]string{
				"localhost": "a=1",
				"127.0.0.1": "b=2",
			},
		},
		{
			name: "AnySite cookies go to every host of every site",
			set: []set{
				{AnySite, "k=v", ""},
				{"shop.test", "a=1", ""},
			},
			sends: map[string]string{
				"https://www.shop.test/": "k=v; a=1",
				"https://outlet.test/x":  "k=v",
				"http://10.0.0.1/":       "k=v",
				"http://localhost/":      "k=v",
			},
			strs: map[string]string{
				AnySite:       "k=v",
				"shop.test":   "a=1",
				"outlet.test": "",
			},
		},
		{
			name: "an AnySite cookie and a site's own of the same name both go",
			set: []set{
				{AnySite, "id=any", ""},
				{"shop.test", "id=site", ""},
			},
			sends: map[string]string{
				"https://www.shop.test/": "id=any; id=site",
				"https://outlet.test/":   "id=any",
			},
			strs: map[string]string{
				AnySite:     "id=any",
				"shop.test": "id=site",
			},
		},
		{
			name: "String has every host and path of the site, not its partitioned cookies",
			lines: []response{
				{"https://www.shop.test/x/", []string{"h=1; Path=/x"}},
				{"https://api.shop.test/", []string{"d=2; Domain=shop.test"}},
			},
			set: []set{
				{"shop.test", "s=3", ""},
			},
			sends: map[string]string{
				"https://www.shop.test/x/y": "h=1; d=2; s=3",
				"https://www.shop.test/":    "d=2; s=3",
			},
			strs: map[string]string{
				"shop.test":     "h=1; d=2; s=3",
				"www.shop.test": "h=1; d=2; s=3",
			},
		},
		{
			name: "input that is not a site, or a pair Set refuses, stores nothing",
			set: []set{
				{"com", "a=1", "public suffix"},
				{"https://co.uk/", "a=1", "public suffix"},
				{"", "a=1", "not a host"},
				{"shop.test/x", "a=1", "not a host"},
				{"https://[bad/", "a=1", "site"},
				{"https://user@/", "a=1", "not a host"},
				{"shop.test", "ok=1; b\x01=2", "control character"},
				{"shop.test", "ok=1; =__Secure-x", "__Secure-"},
				{"shop.test", "ok=1; =", "empty name and value"},
			},
			sends: map[string]string{
				"https://www.shop.test/": "",
			},
			strs: map[string]string{
				"shop.test": "",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Clock: clockwork.NewFakeClockAt(time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)),
			}
			j := newJar(t, cfg)
			for _, r := range tt.lines {
				j.SetCookies(nav(t, r.url), r.lines)
			}
			// The partitioned cookie of shop.test under outlet.test,
			// which String leaves out.
			j.SetCookies(Request{
				URL:    mustURL(t, "https://cdn.shop.test/"),
				Method: "GET",
				Page:   mustURL(t, "https://outlet.test/"),
			}, []string{"p=1; Secure; SameSite=None; Partitioned; Path=/"})
			for _, s := range tt.set {
				err := j.SetString(s.site, s.cookies)
				if (err == nil) != (s.err == "") || (err != nil && !strings.Contains(err.Error(), s.err)) {
					t.Errorf("SetString(%q, %q) = %v, want error %q", s.site, s.cookies, err, s.err)
				}
			}
			for u, want := range tt.sends {
				if got := Header(j.Cookies(nav(t, u))); got != want {
					t.Errorf("%s sends %q, want %q", u, got, want)
				}
			}
			for s, want := range tt.strs {
				got := j.String(s)
				if got != want {
					t.Errorf("String(%q) = %q, want %q", s, got, want)
				}
				k, err := FromString(s, got, cfg)
				if err != nil {
					t.Errorf("FromString(%q, %q): %v", s, got, err)
					continue
				}
				if again := k.String(s); again != got {
					t.Errorf("String(%q) through SetString = %q, want %q", s, again, got)
				}
			}
		})
	}
}

// SetHTTPCookies stores http.Cookies through the browser's rules, as Set-
// Cookie lines from a navigation to the URL: each attribute applies, every
// cookie the rules refuse (or that would not be one line) is named in the
// error, and the rest are stored.
//
//nolint:gosec // the jar's rules judge the cookies' attributes under test
func TestSetHTTPCookies(t *testing.T) {
	for _, tt := range []struct {
		name    string
		url     string
		cookies []*http.Cookie
		// errs are what the error must hold; none: no error.
		errs []string
		// told is how many lines OnSetCookie is told about.
		told  int
		sends map[string]string
	}{
		{
			name: "host-only, Domain and Path as a Set-Cookie line sets them",
			url:  "https://www.shop.test/login",
			cookies: []*http.Cookie{
				{
					Name:  "s",
					Value: "1",
				},
				{
					Name:   "d",
					Value:  "2",
					Domain: ".shop.test",
				},
				{
					Name:  "p",
					Value: "3",
					Path:  "/account",
				},
			},
			told: 3,
			sends: map[string]string{
				"https://www.shop.test/":          "s=1; d=2",
				"https://www.shop.test/account/x": "p=3; s=1; d=2",
				"https://api.shop.test/":          "d=2",
				"https://outlet.test/":            "",
			},
		},
		{
			name: "Secure, quoting, SameSite and expiry apply",
			url:  "https://www.shop.test/",
			cookies: []*http.Cookie{
				{
					Name:     "q",
					Value:    "a b",
					Quoted:   true,
					Secure:   true,
					SameSite: http.SameSiteStrictMode,
				},
				{
					Name:   "old",
					Value:  "1",
					MaxAge: -1,
				},
				{
					Name:    "past",
					Value:   "1",
					Expires: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:   "day",
					Value:  "1",
					MaxAge: 86400,
				},
			},
			told: 4,
			sends: map[string]string{
				"https://www.shop.test/": `q="a b"; day=1`,
				"http://www.shop.test/":  "day=1",
			},
		},
		{
			name: "the browser's rules refuse some; the rest are stored",
			url:  "https://www.shop.test/",
			cookies: []*http.Cookie{
				{
					Name:  "__Host-x",
					Value: "1",
				},
				{
					Name:   "o",
					Value:  "1",
					Domain: "outlet.test",
				},
				{
					Name:   "c",
					Value:  "1",
					Domain: "test",
				},
				{
					Name:  "ok",
					Value: "1",
				},
			},
			errs: []string{
				`"__Host-x": __Host- prefix`,
				`"o": Domain does not match`,
				`"c": Domain is a public suffix`,
			},
			told: 4,
			sends: map[string]string{
				"https://www.shop.test/": "ok=1",
				"https://outlet.test/":   "",
			},
		},
		{
			name: "what would change the line's meaning is refused, not written",
			url:  "https://www.shop.test/",
			cookies: []*http.Cookie{
				{
					Name:  "a",
					Value: "1; Domain=shop.test",
				},
				{
					Name:  "b",
					Value: "1",
					Path:  "/\r\nSet-Cookie: c=1",
				},
				{
					Name:   "e",
					Value:  "1",
					Domain: "shop.test; Secure",
				},
				{
					Name:  "f=g",
					Value: "1",
				},
			},
			errs: []string{
				`"a": name or value`,
				`"b": the Path or Domain`,
				`"e": the Path or Domain`,
				`"f=g": name or value`,
			},
			told: 0,
			sends: map[string]string{
				"https://www.shop.test/": "",
				"https://api.shop.test/": "",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var events int
			j := newJar(t, Config{
				Clock: clockwork.NewFakeClockAt(time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)),
				OnSetCookie: func(Event) {
					events++
				},
			})
			err := j.SetHTTPCookies(mustURL(t, tt.url), tt.cookies)
			if (err == nil) != (len(tt.errs) == 0) {
				t.Fatalf("error %v, want one holding %q", err, tt.errs)
			}
			for _, want := range tt.errs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			for u, want := range tt.sends {
				if got := Header(j.Cookies(nav(t, u))); got != want {
					t.Errorf("%s sends %q, want %q", u, got, want)
				}
			}
			if events != tt.told {
				t.Errorf("OnSetCookie told %d times, want %d", events, tt.told)
			}
		})
	}
	if err := newJar(t, Config{}).SetHTTPCookies(nil, nil); err == nil {
		t.Error("SetHTTPCookies(nil URL) succeeded")
	}
}
