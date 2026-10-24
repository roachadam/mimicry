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
// or ClearExpired removes them, in creation order.
func (j *Jar) All() []*Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]*Cookie, len(j.cookies))
	for i, c := range j.cookies {
		cp := *c
		out[i] = &cp
	}
	return out
}

// Set stores c as given, without a URL or a browser's rules: for restoring
// a session or seeding a jar. Name and value must hold no control
// characters or ";"; Domain is required; an empty Path is "/"; a zero
// Created is now. It replaces a cookie with the same name, domain, path and
// partition.
func (j *Jar) Set(c Cookie) error {
	if strings.ContainsAny(c.Name, ";=") || strings.ContainsFunc(c.Name+c.Value, isControl) || strings.Contains(c.Value, ";") {
		return errors.New("cookies: name or value holds a control character, \";\" or \"=\" in the name")
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
	j.mu.Lock()
	defer j.mu.Unlock()
	if i := slices.IndexFunc(j.cookies, func(old *Cookie) bool {
		return sameKey(old, &c)
	}); i >= 0 {
		j.cookies[i] = &c
		return nil
	}
	// Keep creation order, which RFC ordering of equal paths relies on: a
	// cookie goes after those created at the same time or earlier.
	i, _ := slices.BinarySearchFunc(j.cookies, &c, func(a, b *Cookie) int {
		if a.Created.After(b.Created) {
			return 1
		}
		return -1
	})
	j.cookies = slices.Insert(j.cookies, i, &c)
	return nil
}

func isControl(r rune) bool {
	return (r < 0x20 && r != '\t') || r == 0x7f
}

// Delete removes every cookie match reports true for, and returns how many.
func (j *Jar) Delete(match func(*Cookie) bool) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	before := len(j.cookies)
	j.cookies = slices.DeleteFunc(j.cookies, match)
	return before - len(j.cookies)
}

// Clear removes every cookie.
func (j *Jar) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cookies = nil
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
		cookies:     make([]*Cookie, len(j.cookies)),
	}
	for i, ck := range j.cookies {
		cp := *ck
		c.cookies[i] = &cp
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
// and wget read. The format has no SameSite, partition or creation time:
// partitioned cookies are left out, and the rest lose those attributes.
func (j *Jar) WriteNetscape(w io.Writer) error {
	bw := bufio.NewWriter(w)
	_, _ = bw.WriteString("# Netscape HTTP Cookie File\n")
	for _, c := range j.All() {
		if c.Partition != "" {
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
// does not use it: net/http hands it cookies already parsed (values with
// spaces or commas re-quoted, values with backslashes, UTF-8 or quotes
// dropped, before the jar sees them), and asks for cookies with only a URL,
// so every request is treated as a navigation from nowhere (no SameSite or
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
		if line := c.String(); line != "" {
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
		out[i] = &http.Cookie{ //nolint:gosec // a request cookie: only name and value are sent
			Name:  c.Name,
			Value: c.Value,
		}
	}
	return out
}
