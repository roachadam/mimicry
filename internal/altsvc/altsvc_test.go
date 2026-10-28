package altsvc

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
)

// A saved cache loads back as it was, every entry expiring at its ma; a
// "clear" forgets its origin's; expired entries are neither used, written
// nor loaded; a malformed file adds nothing.
func TestCacheRoundTrip(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	c := New(clock)

	const (
		www    = "https://www.example.test:443"
		cdn    = "https://cdn.example.test:443"
		anon   = "https://cdn.example.test:443 anonymous"
		gone   = "https://gone.example.test:443"
		other  = "https://other.example.test:8443"
		hourMa = `h3=":443"; ma=3600`
	)

	learn := map[string][]string{
		www:   {`h2=":443", h3=":8443"; ma=60`},
		cdn:   {hourMa},
		anon:  {hourMa},
		gone:  {hourMa},
		other: {`h3="elsewhere.test:443"`},
	}
	for key, values := range learn {
		hostPort := strings.TrimPrefix(strings.Fields(key)[0], "https://")
		host, _, _ := strings.Cut(hostPort, ":")
		c.Learn(key, host, values)
	}

	if c.Learn(gone, "gone.example.test", []string{"clear"}) {
		t.Fatal("clear reported an alternative")
	}

	var saved bytes.Buffer
	if err := c.WriteJSON(&saved); err != nil {
		t.Fatal(err)
	}

	loaded := New(clock)
	if err := loaded.ReadJSON(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}

	want := map[string]uint16{
		www:   8443,
		cdn:   443,
		anon:  443,
		gone:  0,
		other: 0,
	}
	for key, port := range want {
		got, ok := loaded.Get(key)
		if got != port || ok != (port != 0) {
			t.Errorf("%s after loading: port %d, %v; want %d", key, got, ok, port)
		}
	}

	var again bytes.Buffer
	if err := loaded.WriteJSON(&again); err != nil {
		t.Fatal(err)
	}

	if again.String() != saved.String() {
		t.Errorf("saved again:\n%s\nfirst saved:\n%s", &again, &saved)
	}

	// www's ma=60 runs out: it is gone from the cache and from what it
	// writes, and loading the first save skips it.
	clock.Advance(61 * time.Second)

	if _, ok := loaded.Get(www); ok {
		t.Error("an expired alternative is still used")
	}

	again.Reset()

	if err := loaded.WriteJSON(&again); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(again.String(), "www.example.test") {
		t.Errorf("an expired alternative was written:\n%s", &again)
	}

	late := New(clock)
	if err := late.ReadJSON(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}

	if _, ok := late.Get(www); ok {
		t.Error("an expired alternative was loaded")
	}

	if _, ok := late.Get(cdn); !ok {
		t.Error("an unexpired alternative was not loaded")
	}

	late.Clear()

	if _, ok := late.Get(cdn); ok {
		t.Error("Clear kept an alternative")
	}

	for _, bad := range []string{
		`[{"origin":"http://www.example.test:443","port":443,"expires":"2030-01-01T00:00:00Z"}]`,
		`[{"origin":"https://www.example.test","port":443,"expires":"2030-01-01T00:00:00Z"}]`,
		`[{"origin":"https://www.example.test:443","expires":"2030-01-01T00:00:00Z"}]`,
		`{"origin":"https://www.example.test:443"}`,
	} {
		fresh := New(clock)
		if err := fresh.ReadJSON(strings.NewReader(`[{"origin":"https://ok.test:443","port":443,"expires":"2030-01-01T00:00:00Z"},` + strings.TrimPrefix(bad, "["))); err == nil {
			t.Errorf("loaded %s", bad)
		}

		if _, ok := fresh.Get("https://ok.test:443"); ok {
			t.Errorf("a failed load of %s added an entry", bad)
		}
	}
}
