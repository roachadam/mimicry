// Package altsvc keeps origins' HTTP/3 alternatives, learnt from Alt-Svc
// response headers (RFC 7838): one engine's alone, or shared by every
// engine given the same Cache, as a browser's tabs share one, and saved
// and loaded as JSON, as browsers keep it across restarts.
//
// Keys are the engine's pool keys of direct https routes: "https://" then
// host:port, then a space and the connection partition if any. Only
// same-host alternatives are kept (h3=":port"), and an entry lives until
// its ma (max-age) or a "clear" from its origin.
package altsvc

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

// defaultMaxAge is an entry's lifetime without ma (RFC 7838, section 3.1).
const defaultMaxAge = 24 * time.Hour

// alt is an origin's HTTP/3 alternative: the same host, at port.
type alt struct {
	port    uint16
	expires time.Time
}

// Cache keeps alternatives by key. It is safe for concurrent use.
type Cache struct {
	clock clockwork.Clock

	mu   sync.Mutex
	alts map[string]alt
}

// New returns an empty cache timed by clock (nil: the real clock).
func New(clock clockwork.Clock) *Cache {
	if clock == nil {
		clock = clockwork.NewRealClock()
	}

	return &Cache{
		clock: clock,
		alts:  map[string]alt{},
	}
}

// Get returns key's alternative port, if it has one that has not expired.
func (c *Cache) Get(key string) (port uint16, ok bool) {
	now := c.clock.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	a, ok := c.alts[key]
	if !ok {
		return 0, false
	}

	if now.After(a.expires) {
		delete(c.alts, key)

		return 0, false
	}

	return a.port, true
}

// Learn records the first same-host h3 alternative in the Alt-Svc values
// of a response from host, under key, or forgets key's on "clear". It
// reports whether it found an alternative.
func (c *Cache) Learn(key, host string, values []string) bool {
	now := c.clock.Now()
	a, found, cleared := parse(values, host, now)

	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case cleared:
		delete(c.alts, key)
	case found:
		c.alts[key] = a
	}

	return found
}

// Clear forgets every alternative.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	clear(c.alts)
}

// entry is an alternative as WriteJSON writes it.
type entry struct {
	// Origin is "https://host:port".
	Origin string `json:"origin"`
	// Partition is the connection partition it was learnt in, if any.
	Partition string    `json:"partition,omitempty"`
	Port      uint16    `json:"port"`
	Expires   time.Time `json:"expires"`
}

// WriteJSON writes the unexpired alternatives as a JSON array, by origin
// and partition.
func (c *Cache) WriteJSON(w io.Writer) error {
	now := c.clock.Now()

	c.mu.Lock()

	entries := make([]entry, 0, len(c.alts))
	for key, a := range c.alts {
		if now.After(a.expires) {
			continue
		}

		origin, partition, _ := strings.Cut(key, " ")

		entries = append(entries, entry{
			Origin:    origin,
			Partition: partition,
			Port:      a.port,
			Expires:   a.expires,
		})
	}

	c.mu.Unlock()

	slices.SortFunc(entries, func(a, b entry) int {
		return cmp.Or(cmp.Compare(a.Origin, b.Origin), cmp.Compare(a.Partition, b.Partition))
	})

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(entries)
}

// ReadJSON adds the alternatives of a JSON array WriteJSON wrote, replacing
// those of the same origin and partition; expired ones are skipped. A
// malformed entry fails it before anything is added.
func (c *Cache) ReadJSON(r io.Reader) error {
	var entries []entry
	if err := json.NewDecoder(r).Decode(&entries); err != nil {
		return fmt.Errorf("altsvc: %w", err)
	}

	for i, e := range entries {
		if err := e.check(); err != nil {
			return fmt.Errorf("altsvc: entry %d: %w", i, err)
		}
	}

	now := c.clock.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range entries {
		if now.After(e.Expires) {
			continue
		}

		key := e.Origin
		if e.Partition != "" {
			key += " " + e.Partition
		}

		c.alts[key] = alt{
			port:    e.Port,
			expires: e.Expires,
		}
	}

	return nil
}

// check reports what makes e unusable: an origin that is not
// "https://host:port", or no port.
func (e entry) check() error {
	hostPort, ok := strings.CutPrefix(e.Origin, "https://")
	if !ok {
		return fmt.Errorf("origin %q is not https://host:port", e.Origin)
	}

	host, port, err := net.SplitHostPort(hostPort)
	if err != nil || host == "" || port == "" || strings.ContainsAny(hostPort, " /") {
		return fmt.Errorf("origin %q is not https://host:port", e.Origin)
	}

	if e.Port == 0 {
		return errors.New("no port")
	}

	return nil
}

// parse returns the first same-host h3 alternative in Alt-Svc values, its
// expiry counted from now, and whether the value is "clear".
func parse(values []string, host string, now time.Time) (a alt, found, cleared bool) {
	for _, v := range values {
		for _, field := range splitQuoted(v, ',') {
			field = strings.TrimSpace(field)
			if field == "clear" {
				return alt{}, false, true
			}

			params := splitQuoted(field, ';')
			proto, authority, ok := strings.Cut(strings.TrimSpace(params[0]), "=")

			if !ok || strings.TrimSpace(proto) != "h3" {
				continue
			}

			authority = strings.Trim(strings.TrimSpace(authority), `"`)

			h, p, err := net.SplitHostPort(authority)

			if err != nil || (h != "" && !strings.EqualFold(h, host)) {
				continue
			}

			port, err := strconv.ParseUint(p, 10, 16)
			if err != nil || port == 0 {
				continue
			}

			maxAge := defaultMaxAge
			for _, param := range params[1:] {
				name, value, _ := strings.Cut(strings.TrimSpace(param), "=")
				if strings.TrimSpace(name) != "ma" {
					continue
				}

				if s, err := strconv.ParseUint(strings.Trim(strings.TrimSpace(value), `"`), 10, 32); err == nil {
					maxAge = time.Duration(s) * time.Second
				}
			}

			return alt{
				port:    uint16(port),
				expires: now.Add(maxAge),
			}, true, false
		}
	}

	return alt{}, false, false
}

// splitQuoted splits s at each sep outside double quotes.
func splitQuoted(s string, sep byte) []string {
	var out []string

	start, quoted := 0, false

	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			quoted = !quoted
		case s[i] == '\\' && quoted:
			// The escaped byte is skipped.
			i++
		case s[i] == sep && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}

	return append(out, s[start:])
}
