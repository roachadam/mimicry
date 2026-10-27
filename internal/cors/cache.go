package cors

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultPreflightAge is how long a preflight answered without
// Access-Control-Max-Age is remembered: the Fetch standard's 5 seconds.
// Every browser's cors capture agrees: the same request at once is not
// preflighted, after 6 seconds it is.
const defaultPreflightAge = 5 * time.Second

// cacheKey is what a preflight cache entry is for: the Fetch
// standard's key (the request's origin, URL and credentials mode).
type cacheKey struct {
	origin, url  string
	credentialed bool
}

// Cache remembers successful preflights: per key, until when each
// method ("m:PUT") and header name ("h:x-a") is allowed. The zero Cache
// is empty and ready to use.
type Cache struct {
	mu      sync.Mutex
	entries map[cacheKey]map[string]time.Time
	// pruneAt is the number of entries at which Store next drops what has
	// expired.
	pruneAt int
}

// minPrune is the fewest entries pruning waits for.
const minPrune = 64

// Allows reports whether a cached answer allows need's request at now.
func (c *Cache) Allows(need *Need, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	items := c.entries[need.key]
	has := func(item string) bool {
		until, ok := items[item]
		return ok && now.Before(until)
	}
	cred := need.key.credentialed

	if need.forced && !slices.ContainsFunc(slices.Collect(maps.Keys(items)), has) {
		// Nothing in the method and headers needs allowing: only an
		// earlier answer for the URL, still live, spares this one.
		return false
	}

	if !SafelistedMethod(need.Method) && !has("m:"+need.Method) && (cred || !has("m:*")) {
		return false
	}

	for _, name := range need.Headers {
		if !has("h:"+name) && (cred || name == "authorization" || !has("h:*")) {
			return false
		}
	}

	return true
}

// Store remembers what a successful preflight's response h allows, for its
// Access-Control-Max-Age (else defaultPreflightAge): replacing the key's
// entry (Chrome, Safari) or adding to it (Firefox).
func (c *Cache) Store(need *Need, h http.Header, replace bool, now time.Time) {
	age := defaultPreflightAge
	if v := strings.TrimSpace(h.Get("Access-Control-Max-Age")); v != "" {
		if secs, err := strconv.ParseUint(v, 10, 32); err == nil {
			age = time.Duration(secs) * time.Second
		}
	}

	until := now.Add(age)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = map[cacheKey]map[string]time.Time{}
	}

	// Drop what has expired once the cache has doubled since it last did,
	// so it stays within twice its live entries at an amortised constant
	// cost per store. (Browsers cap their caches too; by how much is not
	// measured.)
	if len(c.entries) >= c.pruneAt {
		for key, items := range c.entries {
			for item, at := range items {
				if !now.Before(at) {
					delete(items, item)
				}
			}

			if len(items) == 0 {
				delete(c.entries, key)
			}
		}

		c.pruneAt = max(2*len(c.entries), minPrune)
	}

	items := c.entries[need.key]
	if items == nil || replace {
		items = map[string]time.Time{}
	}

	for _, m := range headerList(h, "Access-Control-Allow-Methods") {
		items["m:"+m] = until
	}

	for _, name := range headerList(h, "Access-Control-Allow-Headers") {
		items["h:"+strings.ToLower(name)] = until
	}

	// Max-Age 0 stores items already expired: under replace, the answer
	// still displaces the URL's earlier entry.
	c.entries[need.key] = items
}
