package cookies

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// All returns copies of every cookie, expired ones included until a request
// to their domain or ClearExpired removes them, in creation order.
func (j *Jar) All() []*Cookie {
	j.mu.Lock()
	es := j.entries()
	out := make([]*Cookie, len(es))
	for i, e := range es {
		cp := *e.c
		out[i] = &cp
	}
	j.mu.Unlock()
	return out
}

// entries returns every entry in creation order. Call with the lock held.
func (j *Jar) entries() []entry {
	var n int
	for _, bucket := range j.domains {
		n += len(bucket)
	}
	es := make([]entry, 0, n)
	for _, bucket := range j.domains {
		es = append(es, bucket...)
	}
	slices.SortFunc(es, compareCreated)
	return es
}

// Set stores c as given, without a URL or a browser's rules: for restoring
// a session or seeding a jar. Name and value must hold no control
// characters or ";", and a nameless cookie's value must neither start with
// __Secure- or __Host- nor hold "="; Domain is required; an empty Path is
// "/"; a zero Created is now. It replaces a cookie with the same name,
// domain, path and partition.
func (j *Jar) Set(c Cookie) error {
	if strings.ContainsAny(c.Name, ";=") || strings.ContainsFunc(c.Name+c.Value, isControl) || strings.Contains(c.Value, ";") {
		return errors.New("cookies: name or value holds a control character, \";\" or \"=\" in the name")
	}
	if c.Name == "" && hiddenPrefix(c.Value) {
		return errors.New("cookies: a nameless cookie's value starts with __Secure- or __Host-")
	}
	if c.Name == "" && strings.Contains(c.Value, "=") {
		return errors.New("cookies: a nameless cookie's value holds \"=\"")
	}
	if c.Name == "" && c.Value == "" {
		return errors.New("cookies: empty name and value")
	}
	if c.Domain == "" {
		return errors.New("cookies: Domain is required")
	}
	c.Domain = strings.ToLower(strings.TrimPrefix(c.Domain, "."))
	if c.Path == "" {
		c.Path = "/"
	}
	now := j.clock.Now()
	if c.Created.IsZero() {
		c.Created = now
	}
	if c.LastAccess.IsZero() {
		c.LastAccess = c.Created
	}
	key := domainKey(c.Domain)
	j.mu.Lock()
	defer j.mu.Unlock()
	bucket := j.domains[key]
	if i := slices.IndexFunc(bucket, func(old entry) bool {
		return sameKey(old.c, &c)
	}); i >= 0 {
		bucket[i].c = &c
		return nil
	}
	// Among cookies created at the same time, the new one goes last.
	j.domains[key] = append(bucket, j.newEntry(&c))
	return nil
}

func isControl(r rune) bool {
	return (r < 0x20 && r != '\t') || r == 0x7f
}

// Delete removes every cookie match reports true for, and returns how many.
// match gets a copy of each cookie, with the jar unlocked: it may use the
// jar. A cookie replaced while match runs stays.
func (j *Jar) Delete(match func(*Cookie) bool) int {
	j.mu.Lock()
	es := j.entries()
	copies := make([]Cookie, len(es))
	for i, e := range es {
		copies[i] = *e.c
	}
	j.mu.Unlock()
	doomed := map[*Cookie]bool{}
	for i, e := range es {
		if match(&copies[i]) {
			doomed[e.c] = true
		}
	}
	if len(doomed) == 0 {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for key, bucket := range j.domains {
		j.store(key, slices.DeleteFunc(bucket, func(e entry) bool {
			if doomed[e.c] {
				n++
				return true
			}
			return false
		}))
	}
	return n
}

// Clear removes every cookie.
func (j *Jar) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.domains = map[string][]entry{}
}

// ClearExpired removes expired cookies, and returns how many.
func (j *Jar) ClearExpired() int {
	now := j.clock.Now()
	return j.Delete(func(c *Cookie) bool {
		return !c.Expires.IsZero() && !c.Expires.After(now)
	})
}

// ClearSession removes session cookies, as closing a browser does, and
// returns how many.
func (j *Jar) ClearSession() int {
	return j.Delete(func(c *Cookie) bool {
		return c.Expires.IsZero()
	})
}

// Clone returns an independent jar with the same rules, clock, hook and
// cookies.
func (j *Jar) Clone() *Jar {
	j.mu.Lock()
	defer j.mu.Unlock()
	c := &Jar{
		rules:       j.rules,
		clock:       j.clock,
		onSetCookie: j.onSetCookie,
		domains:     make(map[string][]entry, len(j.domains)),
		seq:         j.seq,
	}
	for key, bucket := range j.domains {
		es := make([]entry, len(bucket))
		for i, e := range bucket {
			cp := *e.c
			es[i] = entry{
				c:   &cp,
				seq: e.seq,
			}
		}
		c.domains[key] = es
	}
	return c
}

// WriteJSON writes every cookie as a JSON array, every attribute kept: the
// lossless way to save a jar.
func (j *Jar) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(j.All())
}

// ReadJSON adds the cookies of a JSON array WriteJSON wrote, through Set.
func (j *Jar) ReadJSON(r io.Reader) error {
	var cs []Cookie
	if err := json.NewDecoder(r).Decode(&cs); err != nil {
		return fmt.Errorf("cookies: %w", err)
	}
	for i, c := range cs {
		if err := j.Set(c); err != nil {
			return fmt.Errorf("cookie %d: %w", i, err)
		}
	}
	return nil
}

// WriteNetscape writes the cookies in the Netscape cookies.txt format curl
// and wget read. The format has no SameSite, partition or creation time
// and separates fields with tabs: partitioned cookies and cookies with a
// tab in a field are left out, and the rest lose those attributes.
func (j *Jar) WriteNetscape(w io.Writer) error {
	bw := bufio.NewWriter(w)
	_, _ = bw.WriteString("# Netscape HTTP Cookie File\n")
	for _, c := range j.All() {
		if c.Partition != "" || strings.Contains(c.Name+c.Value+c.Path+c.Domain, "\t") {
			continue
		}
		domain := c.Domain
		if !c.HostOnly {
			domain = "." + domain
		}
		if c.HTTPOnly {
			domain = "#HttpOnly_" + domain
		}
		var expires int64
		if !c.Expires.IsZero() {
			expires = c.Expires.Unix()
		}
		if _, err := fmt.Fprintf(bw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			domain, upperBool(!c.HostOnly), c.Path, upperBool(c.Secure), expires, c.Name, c.Value); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// ReadNetscape adds the cookies of a Netscape cookies.txt file, through Set;
// lines are created in file order.
func (j *Jar) ReadNetscape(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20)
	now := j.clock.Now()
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		httpOnly := false
		if rest, ok := strings.CutPrefix(line, "#HttpOnly_"); ok {
			line, httpOnly = rest, true
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return fmt.Errorf("cookies: line %d: %d fields, want 7", n, len(f))
		}
		expires, err := strconv.ParseInt(f[4], 10, 64)
		if err != nil {
			return fmt.Errorf("cookies: line %d: expiry %q", n, f[4])
		}
		c := Cookie{
			Name:     f[5],
			Value:    f[6],
			Domain:   f[0],
			HostOnly: !strings.HasPrefix(f[0], ".") && f[1] != "TRUE",
			Path:     f[2],
			Secure:   f[3] == "TRUE",
			HTTPOnly: httpOnly,
			Created:  now.Add(time.Duration(n) * time.Nanosecond),
		}
		if expires > 0 {
			c.Expires = time.Unix(expires, 0)
		}
		if err := j.Set(c); err != nil {
			return fmt.Errorf("cookies: line %d: %w", n, err)
		}
	}
	return sc.Err()
}

func upperBool(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

// CookieJar returns the jar as an http.CookieJar, for an http.Client or
// other code that wants one. It is lossy, which is why mimicry's transport
// does not use it: net/http hands it only the cookies it can parse (values
// with backslashes, UTF-8 or inner quotes are dropped before the jar sees
// them; the jar stores the raw lines of the rest), sends values with
// spaces or commas re-quoted, and asks for cookies with only a URL, so
// every request is treated as a navigation from nowhere (no SameSite or
// third-party rules apply).
func (j *Jar) CookieJar() http.CookieJar {
	return adapter{
		j: j,
	}
}

type adapter struct {
	j *Jar
}

func (a adapter) SetCookies(u *url.URL, cs []*http.Cookie) {
	lines := make([]string, 0, len(cs))
	for _, c := range cs {
		// Raw is the Set-Cookie line as received; String re-serializes,
		// re-quoting values, for a cookie built by hand.
		line := c.Raw
		if line == "" {
			line = c.String()
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	a.j.SetCookies(Request{
		URL:        u,
		Method:     http.MethodGet,
		Navigation: true,
	}, lines)
}

func (a adapter) Cookies(u *url.URL) []*http.Cookie {
	cs := a.j.Cookies(Request{
		URL:        u,
		Method:     http.MethodGet,
		Navigation: true,
	})
	out := make([]*http.Cookie, len(cs))
	for i, c := range cs {
		// net/http drops '"' from a value and quotes it again when told
		// it was quoted.
		value, quoted := c.Value, false
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value, quoted = value[1:len(value)-1], true
		}
		out[i] = &http.Cookie{ //nolint:gosec // a request cookie: only name and value are sent
			Name:   c.Name,
			Value:  value,
			Quoted: quoted,
		}
	}
	return out
}
