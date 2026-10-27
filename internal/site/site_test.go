package site

import (
	"net/url"
	"strconv"
	"testing"
)

// TestCachesBounded checks that the per-host caches stay bounded however
// many hosts a long-lived client meets: unbounded, they would leak.
func TestCachesBounded(t *testing.T) {
	for i := range 3 * cacheSize {
		Of(&url.URL{
			Scheme: "https",
			Host:   "h" + strconv.Itoa(i) + ".example.com",
		})
	}

	for name, m := range map[string]interface{ Range(func(k, v any) bool) }{
		"registrables": &registrables.m,
		"sites":        &sites.m,
	} {
		n := 0
		m.Range(func(any, any) bool {
			n++
			return true
		})

		if n > cacheSize {
			t.Errorf("%s holds %d entries, more than %d", name, n, cacheSize)
		}
	}
}
