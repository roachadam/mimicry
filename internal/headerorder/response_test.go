package headerorder

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// A hostile server sending ever new header names grows a connection's
// Canon no further than its bound, and every name still comes out
// canonical. StatusLine's shortcuts are what net/http's Status would be.
func TestResponseHeader(t *testing.T) {
	var c Canon

	for i := range 4 * canonMax {
		name := "x-name-" + strconv.Itoa(i)
		if got, want := c.Key(name), http.CanonicalHeaderKey(name); got != want {
			t.Fatalf("Key(%q) = %q, want %q", name, got, want)
		}
	}

	if len(c.names) > canonMax {
		t.Fatalf("cache holds %d names, want at most %d", len(c.names), canonMax)
	}

	var fresh Canon
	if long := strings.Repeat("x", canonNameMax+1); fresh.Key(long) != http.CanonicalHeaderKey(long) || len(fresh.names) != 0 {
		t.Fatal("a long name is not canonical or is cached")
	}

	for code := 100; code < 1000; code++ {
		status := strconv.Itoa(code)
		if got, want := StatusLine(status, code), status+" "+http.StatusText(code); got != want {
			t.Errorf("StatusLine(%q) = %q, want %q", status, got, want)
		}
	}

	if got := StatusLine("0200", 200); got != "0200 OK" {
		t.Errorf(`StatusLine("0200") = %q, want "0200 OK"`, got)
	}
}
