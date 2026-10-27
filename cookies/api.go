package cookies

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/internal/site"
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

// Set stores c as given, bypassing URL and browser rules, for restoring a
// session or seeding a jar. Name and value must hold no control characters
// or ";"; Domain is required (AnySite for every site); an empty Path is "/"
// and a zero Created is now. It replaces a cookie with the same name,
// domain, path and partition.
func (j *Jar) Set(c Cookie) error {
	if err := checkPair(c.Name, c.Value); err != nil {
		return fmt.Errorf("cookies: %w", err)
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

// checkPair checks a name and value as Set does: what a Cookie header
// could not carry as one pair.
func checkPair(name, value string) error {
	if strings.ContainsAny(name, ";=") || strings.ContainsFunc(name+value, isControl) || strings.Contains(value, ";") {
		return errors.New("name or value holds a control character, \";\" or \"=\" in the name")
	}
	if name == "" && hiddenPrefix(value) {
		return errors.New("a nameless cookie's value starts with __Secure- or __Host-")
	}
	if name == "" && strings.Contains(value, "=") {
		return errors.New("a nameless cookie's value holds \"=\"")
	}
	if name == "" && value == "" {
		return errors.New("empty name and value")
	}

	return nil
}

func isControl(r rune) bool {
	return (r < 0x20 && r != '\t') || r == 0x7f
}

// AnySite as a domain stores a cookie sent to every host of every site. No
// browser has such a cookie, and it leaks to every site the jar talks to.
// WriteJSON and ReadJSON keep AnySite cookies, WriteNetscape omits them,
// and they count towards no site's limit. A __Host- cookie cannot be one.
const AnySite = "*"

// SetString stores the pairs of a Cookie header string ("a=1; b=2") as
// Domain cookies of domain, sent to domain and every name under it.
// domain is a host name (a port is ignored) or a URL whose host is taken;
// it is never widened to the whole site. SetString(AnySite, ...) stores
// cookies for every site.
//
// Each pair becomes a cookie with path /, no expiry, not Secure and no
// SameSite, replacing any cookie with the same name, Domain and path. A
// pair without "=" is a nameless cookie. Name prefixes are honoured
// case-insensitively: __Host- is stored host-only on domain itself, Secure,
// path /; __Secure- is stored Secure; __Http- and __Host-Http- are Secure
// and HttpOnly where the profile enforces them.
//
// It stores nothing and returns an error when domain is not a host name or
// URL, is an IP address (use SetHostString), is a public suffix, a pair is
// one Set refuses, or a __Host- cookie is given for AnySite.
func (j *Jar) SetString(domain, cookies string) error {
	if domain == AnySite {
		return j.setString(AnySite, false, cookies)
	}
	name, err := hostName(domain)
	if err != nil {
		return err
	}
	switch {
	case net.ParseIP(name) != nil:
		return fmt.Errorf("cookies: %q is an IP address, which no Domain cookie may name: use SetHostString for its cookies", domain)
	case site.IsPublicSuffix(name):
		return fmt.Errorf("cookies: %q is a public suffix, which no Domain cookie may name (SetHostString stores cookies for that one host)", domain)
	}

	return j.setString(name, false, cookies)
}

// SetHostString stores the pairs of a Cookie header string as host-only
// cookies of host, as a browser keeps a cookie set without a Domain
// attribute. host is a host name, IP address or URL. Pairs are read as
// SetString reads them; on error nothing is stored.
func (j *Jar) SetHostString(host, cookies string) error {
	name, err := hostName(host)
	if err != nil {
		return err
	}

	return j.setString(name, true, cookies)
}

// setString stores the pairs of cookies for name, host-only or as Domain
// cookies, with the rules of their prefixes; nothing if any is refused.
func (j *Jar) setString(name string, hostOnly bool, cookies string) error {
	var cs []Cookie
	for pair := range strings.SplitSeq(cookies, ";") {
		pair = trimWSP(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			k, v = "", pair
		}
		k, v = trimWSP(k), trimWSP(v)
		if err := checkPair(k, v); err != nil {
			return fmt.Errorf("cookies: cookie %q: %w", k, err)
		}
		c := Cookie{
			Name:     k,
			Value:    v,
			Domain:   name,
			HostOnly: hostOnly,
			Path:     "/",
		}
		if hasPrefixFold(k, "__Host-") {
			if name == AnySite {
				return fmt.Errorf("cookies: cookie %q: a __Host- cookie belongs to one host, not to every site", k)
			}
			c.HostOnly, c.Secure = true, true
		}
		if hasPrefixFold(k, "__Secure-") {
			c.Secure = true
		}
		if j.rules.HTTPPrefix && (hasPrefixFold(k, "__Http-") || hasPrefixFold(k, "__Host-Http-")) {
			c.Secure, c.HTTPOnly = true, true
		}
		cs = append(cs, c)
	}
	for _, c := range cs {
		if err := j.Set(c); err != nil {
			return err
		}
	}

	return nil
}

// String returns, as a Cookie header string, every unexpired,
// unpartitioned cookie whose domain is name or ends in "."+name, in
// creation order. Domain cookies of a parent domain are not included.
// name is read as SetString reads it. Hosts, paths and attributes are
// lost; WriteJSON keeps them. It is empty for an invalid name.
func (j *Jar) String(name string) string {
	if name != AnySite {
		var err error
		if name, err = hostName(name); err != nil {
			return ""
		}
	}
	now := j.clock.Now()
	j.mu.Lock()
	var es []entry
	for key, bucket := range j.domains {
		if (key == AnySite) != (name == AnySite) {
			continue
		}
		for _, e := range bucket {
			c := e.c
			expired := !c.Expires.IsZero() && !c.Expires.After(now)
			if expired || c.Partition != "" || (name == AnySite && c.HostOnly) || !domainMatch(c.Domain, name) {
				continue
			}
			es = append(es, e)
		}
	}
	slices.SortFunc(es, compareCreated)
	pairs := make([]string, len(es))
	for i, e := range es {
		pairs[i] = e.c.pair()
	}
	j.mu.Unlock()

	return strings.Join(pairs, "; ")
}

// FromString returns a new jar (New(cfg)) holding domain's cookies from a
// Cookie header string, stored as SetString stores them; for host-only
// cookies, call SetHostString on a New jar.
func FromString(domain, cookies string, cfg Config) (*Jar, error) {
	j, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if err := j.SetString(domain, cookies); err != nil {
		return nil, err
	}

	return j, nil
}

// hostName is the host SetString, SetHostString and String read s as: a
// host name or IP address (a port allowed), or a URL's host; lower case,
// without brackets or a trailing dot.
func hostName(s string) (string, error) {
	host := s
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("cookies: %q: %w", s, err)
		}
		host = u.Hostname()
	} else if h, _, err := net.SplitHostPort(s); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "" || (net.ParseIP(host) == nil && strings.ContainsFunc(host, notHostChar)) {
		return "", fmt.Errorf("cookies: %q is not a host name, IP address or URL", s)
	}

	return host, nil
}

// notHostChar reports a character no (ASCII) host name holds.
func notHostChar(r rune) bool {
	return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' && r != '_'
}

// SetHTTPCookies stores cs as if a top-level navigation to u had received
// them as Set-Cookie lines, through the profile's rules. Raw is ignored; a
// Domain makes a Domain cookie and none a host-only cookie. It stores every
// cookie it can and returns an error naming each one refused.
func (j *Jar) SetHTTPCookies(u *url.URL, cs []*http.Cookie) error {
	if u == nil || u.Hostname() == "" {
		return errors.New("cookies: SetHTTPCookies needs a URL with a host")
	}
	var errs []error
	names := make([]string, 0, len(cs))
	lines := make([]string, 0, len(cs))
	for _, c := range cs {
		line, err := setCookieLine(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("cookies: cookie %q: %w", c.Name, err))
			continue
		}
		names = append(names, c.Name)
		lines = append(lines, line)
	}
	for i, e := range j.setCookies(Request{
		URL:        u,
		Method:     http.MethodGet,
		Navigation: true,
	}, lines) {
		if e.Rejected != "" {
			errs = append(errs, fmt.Errorf("cookies: cookie %q: %s", names[i], e.Rejected))
		}
	}

	return errors.Join(errs...)
}

// setCookieLine writes c as a Set-Cookie line. Unlike http.Cookie.String, it
// keeps the value as given and refuses what would change the line's meaning.
func setCookieLine(c *http.Cookie) (string, error) {
	if err := checkPair(c.Name, c.Value); err != nil {
		return "", err
	}
	if strings.ContainsFunc(c.Path+c.Domain, isControl) || strings.Contains(c.Path+c.Domain, ";") {
		return "", errors.New("the Path or Domain holds a control character or \";\"")
	}
	var b strings.Builder
	b.WriteString(c.Name)
	b.WriteByte('=')
	if c.Quoted {
		b.WriteString(`"`)
		b.WriteString(c.Value)
		b.WriteString(`"`)
	} else {
		b.WriteString(c.Value)
	}
	if c.Path != "" {
		b.WriteString("; Path=")
		b.WriteString(c.Path)
	}
	if c.Domain != "" {
		b.WriteString("; Domain=")
		b.WriteString(c.Domain)
	}
	if !c.Expires.IsZero() {
		b.WriteString("; Expires=")
		b.WriteString(c.Expires.UTC().Format(http.TimeFormat))
	}
	switch {
	case c.MaxAge > 0:
		b.WriteString("; Max-Age=")
		b.WriteString(strconv.Itoa(c.MaxAge))
	case c.MaxAge < 0:
		b.WriteString("; Max-Age=0")
	}
	if c.Secure {
		b.WriteString("; Secure")
	}
	if c.HttpOnly {
		b.WriteString("; HttpOnly")
	}
	switch c.SameSite {
	case http.SameSiteLaxMode:
		b.WriteString("; SameSite=Lax")
	case http.SameSiteStrictMode:
		b.WriteString("; SameSite=Strict")
	case http.SameSiteNoneMode:
		b.WriteString("; SameSite=None")
	case http.SameSiteDefaultMode:
		// No attribute: the cookie was set without one.
	}
	if c.Partitioned {
		b.WriteString("; Partitioned")
	}

	return b.String(), nil
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
// and wget read. Partitioned and AnySite cookies and cookies with a tab in
// a field are left out; the rest lose SameSite and creation time.
func (j *Jar) WriteNetscape(w io.Writer) error {
	bw := bufio.NewWriter(w)
	_, _ = bw.WriteString("# Netscape HTTP Cookie File\n")
	for _, c := range j.All() {
		if c.Partition != "" || (c.Domain == AnySite && !c.HostOnly) || strings.Contains(c.Name+c.Value+c.Path+c.Domain, "\t") {
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

// CookieJar returns the jar as an http.CookieJar. It is lossy, which is why
// mimicry's transport does not use it: net/http drops values it cannot
// parse, re-quotes others, and asks for cookies with only a URL, so every
// request is treated as a navigation from nowhere.
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
