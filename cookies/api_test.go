package cookies

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
)

// fill stores a jar with a host-only, a Domain, an HttpOnly session, a
// Lax and a partitioned third-party cookie, the last under top-level site
// a.example.
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
	if got := len(j.All()); got != 4 {
		t.Fatalf("filled %d cookies, want 4", got)
	}
}

// A jar written and read back sends the same cookies everywhere. JSON keeps
// every attribute, partitioned cookies included; Netscape files keep what the
// format holds (domain, host-only, path, Secure, HttpOnly, expiry, raw value)
// and leave partitioned cookies out.
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
		if strings.Contains(text, "part") {
			t.Errorf("partitioned cookie written:\n%s", text)
		}
		k := newJar(t, Config{
			Clock: clock,
		})
		if err := k.ReadNetscape(strings.NewReader(text)); err != nil {
			t.Fatal(err)
		}
		for _, u := range []string{"https://www.a.example/p", "https://x.a.example/p"} {
			if want, got := Header(j.Cookies(nav(t, u))), Header(k.Cookies(nav(t, u))); got != want {
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
		return len(others) == 4 && c.Name == "sess"
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
