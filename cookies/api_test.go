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
		if !strings.Contains(first.String(), `"partition": "https://a.example"`) || !strings.Contains(first.String(), `"sameSite": "Lax"`) {
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

// SetString, SetHostString and String store and return cookies for exactly
// the names given, apply prefix rules, and round-trip; observed via Cookies.
func TestSetString(t *testing.T) {
	type set struct {
		method, name, cookies, err string
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
		// strs maps a name to its String.
		strs map[string]string
	}{
		{
			name: "a domain's cookies go to it and every name under it, nowhere else",
			set: []set{
				{"SetString", "amazon.com", "a=1; b=2", ""},
			},
			sends: map[string]string{
				"https://amazon.com/":               "a=1; b=2",
				"https://www.amazon.com/deep/x":     "a=1; b=2",
				"http://smile.api.amazon.com:8080/": "a=1; b=2",
				"https://amazon.com.evil/":          "",
				"https://walmart.com/":              "",
				"https://www.walmart.com/":          "",
				"https://amazon.co.uk/":             "",
			},
			strs: map[string]string{
				"amazon.com":     "a=1; b=2",
				"www.amazon.com": "",
				"walmart.com":    "",
			},
		},
		{
			name: "a subdomain's cookies go to it and names under it, not its parent or siblings",
			set: []set{
				{"SetString", "www.amazon.com", "w=1", ""},
			},
			sends: map[string]string{
				"https://www.amazon.com/":   "w=1",
				"https://m.www.amazon.com/": "w=1",
				"https://amazon.com/":       "",
				"https://api.amazon.com/":   "",
				"https://walmart.com/":      "",
			},
			strs: map[string]string{
				"www.amazon.com": "w=1",
				"amazon.com":     "w=1",
				"api.amazon.com": "",
			},
		},
		{
			name: "a URL or host:port is read as its host, never widened",
			set: []set{
				{"SetString", "https://WWW.Amazon.com:8443/cart?x=1#y", "a=1", ""},
				{"SetString", "www.amazon.com.", "b=2", ""},
				{"SetString", "api.amazon.com:443", "c=3", ""},
				{"SetString", "https://www.amazon.co.uk/", "d=4", ""},
			},
			sends: map[string]string{
				"https://www.amazon.com/":   "a=1; b=2",
				"https://api.amazon.com/":   "c=3",
				"https://amazon.com/":       "",
				"https://www.amazon.co.uk/": "d=4",
				"https://amazon.co.uk/":     "",
			},
			strs: map[string]string{
				"amazon.com":               "a=1; b=2; c=3",
				"https://www.amazon.com/x": "a=1; b=2",
				"amazon.co.uk":             "d=4",
			},
		},
		{
			name: "host-only cookies go to their host alone",
			set: []set{
				{"SetHostString", "amazon.com", "h=1", ""},
				{"SetHostString", "https://www.amazon.com/x", "w=2", ""},
				{"SetHostString", "http://127.0.0.1:8080/", "ip=3", ""},
				{"SetHostString", "[::1]", "v6=4", ""},
				{"SetHostString", "localhost", "l=5", ""},
			},
			sends: map[string]string{
				"https://amazon.com/":       "h=1",
				"https://www.amazon.com/":   "w=2",
				"https://m.www.amazon.com/": "",
				"https://api.amazon.com/":   "",
				"https://walmart.com/":      "",
				"http://127.0.0.1/":         "ip=3",
				"http://127.0.0.2/":         "",
				"http://[::1]/":             "v6=4",
				"http://localhost:9000/":    "l=5",
				"http://a.localhost/":       "",
			},
			strs: map[string]string{
				"amazon.com":     "h=1; w=2",
				"www.amazon.com": "w=2",
				"api.amazon.com": "",
				"walmart.com":    "",
				"127.0.0.1":      "ip=3",
				"[::1]":          "v6=4",
				"localhost":      "l=5",
			},
		},
		{
			name: "prefixes: __Host- host-only on the name given and Secure, __Secure- and __Http- Secure",
			set: []set{
				{"SetString", "amazon.com", "__Host-id=1; __Secure-s=2; plain=3; __Http-h=4", ""},
				{"SetString", "www.amazon.com", "__host-w=5", ""},
				{"SetHostString", "http://api.amazon.com/", "__Secure-x=6", ""},
			},
			sends: map[string]string{
				"https://amazon.com/":       "__Host-id=1; __Secure-s=2; plain=3; __Http-h=4",
				"http://amazon.com/":        "plain=3",
				"https://www.amazon.com/":   "__Secure-s=2; plain=3; __Http-h=4; __host-w=5",
				"http://www.amazon.com/":    "plain=3",
				"https://m.www.amazon.com/": "__Secure-s=2; plain=3; __Http-h=4",
				"https://api.amazon.com/":   "__Secure-s=2; plain=3; __Http-h=4; __Secure-x=6",
				"http://api.amazon.com/":    "plain=3",
			},
			strs: map[string]string{
				"amazon.com":     "__Host-id=1; __Secure-s=2; plain=3; __Http-h=4; __host-w=5; __Secure-x=6",
				"www.amazon.com": "__host-w=5",
			},
		},
		{
			name: "sloppy strings parse; a later value replaces",
			set: []set{
				{"SetString", "amazon.com", "  a=0;b=2 ;; c = 3 ; knoeq;", ""},
				{"SetString", "amazon.com", "a=1", ""},
			},
			sends: map[string]string{
				"https://www.amazon.com/": "a=1; b=2; c=3; knoeq",
			},
			strs: map[string]string{
				"amazon.com": "a=1; b=2; c=3; knoeq",
			},
		},
		{
			name: "AnySite cookies go to every host of every site",
			set: []set{
				{"SetString", AnySite, "k=v; __Secure-g=1", ""},
				{"SetString", "amazon.com", "a=1", ""},
			},
			sends: map[string]string{
				"https://www.amazon.com/": "k=v; __Secure-g=1; a=1",
				"https://walmart.com/x":   "k=v; __Secure-g=1",
				"http://walmart.com/x":    "k=v",
				"http://10.0.0.1/":        "k=v",
				"http://localhost/":       "k=v; __Secure-g=1",
			},
			strs: map[string]string{
				AnySite:       "k=v; __Secure-g=1",
				"amazon.com":  "a=1",
				"walmart.com": "",
			},
		},
		{
			name: "an AnySite cookie and a site's own of the same name both go",
			set: []set{
				{"SetString", AnySite, "id=any", ""},
				{"SetString", "amazon.com", "id=site", ""},
			},
			sends: map[string]string{
				"https://www.amazon.com/": "id=any; id=site",
				"https://walmart.com/":    "id=any",
			},
			strs: map[string]string{
				AnySite:      "id=any",
				"amazon.com": "id=site",
			},
		},
		{
			name: "String has every cookie for the name and names under it, of every path, not partitioned ones",
			lines: []response{
				{"https://www.amazon.com/x/", []string{"h=1; Path=/x"}},
				{"https://api.amazon.com/", []string{"d=2; Domain=amazon.com"}},
			},
			set: []set{
				{"SetString", "amazon.com", "s=3", ""},
			},
			sends: map[string]string{
				"https://www.amazon.com/x/y": "h=1; d=2; s=3",
				"https://www.amazon.com/":    "d=2; s=3",
			},
			strs: map[string]string{
				"amazon.com":     "h=1; d=2; s=3",
				"www.amazon.com": "h=1",
				"api.amazon.com": "",
			},
		},
		{
			name: "an IP address or public suffix as a domain, input that is not a host, or a pair Set refuses stores nothing",
			set: []set{
				{"SetString", "com", "a=1", "public suffix"},
				{"SetString", "https://co.uk/", "a=1", "public suffix"},
				{"SetString", "localhost", "a=1", "public suffix"},
				{"SetString", "127.0.0.1", "a=1", "use SetHostString"},
				{"SetString", "[::1]:443", "a=1", "use SetHostString"},
				{"SetString", "", "a=1", "not a host"},
				{"SetString", "amazon.com/x", "a=1", "not a host"},
				{"SetString", "https://[bad/", "a=1", "missing ']'"},
				{"SetString", "https://user@/", "a=1", "not a host"},
				{"SetString", "amazon.com", "ok=1; b\x01=2", "control character"},
				{"SetString", "amazon.com", "ok=1; =__Secure-x", "__Secure-"},
				{"SetString", "amazon.com", "ok=1; =", "empty name and value"},
				{"SetString", AnySite, "ok=1; __Host-id=1", "__Host- cookie belongs to one host"},
				{"SetHostString", AnySite, "a=1", "not a host"},
				{"SetHostString", "", "a=1", "not a host"},
			},
			sends: map[string]string{
				"https://amazon.com/":     "",
				"https://www.amazon.com/": "",
				"https://walmart.com/":    "",
				"http://127.0.0.1/":       "",
				"http://localhost/":       "",
			},
			strs: map[string]string{
				"amazon.com": "",
				AnySite:      "",
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
			// The partitioned cookie of amazon.com under walmart.com,
			// which String leaves out.
			j.SetCookies(Request{
				URL:    mustURL(t, "https://cdn.amazon.com/"),
				Method: "GET",
				Page:   mustURL(t, "https://walmart.com/"),
			}, []string{"part=1; Secure; SameSite=None; Partitioned; Path=/"})
			for _, s := range tt.set {
				store := j.SetString
				if s.method == "SetHostString" {
					store = j.SetHostString
				}
				err := store(s.name, s.cookies)
				if (err == nil) != (s.err == "") || (err != nil && !strings.Contains(err.Error(), s.err)) {
					t.Errorf("%s(%q, %q) = %v, want error %q", s.method, s.name, s.cookies, err, s.err)
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
					// An IP address or localhost: its own cookies.
					k = newJar(t, cfg)
					if err := k.SetHostString(s, got); err != nil {
						t.Errorf("SetString and SetHostString(%q, %q): %v", s, got, err)
						continue
					}
				}
				if again := k.String(s); again != got {
					t.Errorf("String(%q) stored again = %q, want %q", s, again, got)
				}
			}
		})
	}
}

// SetHTTPCookies applies each attribute through the browser's rules, names
// every refused cookie in the error, and stores the rest.
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
