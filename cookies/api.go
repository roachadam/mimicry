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

// Set stores c as given, without a URL or a browser's rules: for restoring
// a session or seeding a jar. Name and value must hold no control
// characters or ";", and a nameless cookie's value must neither start with
// __Secure- or __Host- nor hold "="; Domain is required (AnySite for a
// cookie sent to every site, with HostOnly false); an empty Path is "/"; a
// zero Created is now. It replaces a cookie with the same name, domain,
// path and partition. SetString seeds a site's cookies from a Cookie
// header string.
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

// AnySite stands for every site as SetString's or String's site: a
// cookie stored for AnySite is sent to every host of every site, where
// the request's context would carry that site's own cookie (SameSite and
// third-party rules apply as to any cookie). No browser has such a cookie;
// it is a deliberate deviation for the caller who wants one value
// everywhere, and it leaks: every site the jar talks to receives it,
// trackers, CDNs and unrelated sites included, and a site that sets a
// cookie of the same name gets both. Stored, it is a Cookie whose Domain
// is AnySite (HostOnly false); WriteJSON and ReadJSON keep it,
// WriteNetscape leaves it out, and it counts towards no site's limit.
const AnySite = "*"

// SetString stores the pairs of a Cookie header string ("a=1; b=2"), as
// another tool, a harvested browser or a saved session hands them over,
// as cookies of site: "shop.example", any of its hosts
// ("www.shop.example") or a URL ("https://www.shop.example/cart") give the
// same registrable domain, shop.example. Each pair becomes a Domain
// cookie of that domain (sent to every host of the site and no other),
// with path /, no expiry, not Secure and no SameSite, so the profile's
// default applies; it replaces a cookie of the same name the site had
// that way. A pair without "=" is a nameless cookie, as Header writes it;
// spaces around pairs, a missing space after ";" and empty pairs are
// tolerated. Site AnySite stores cookies for every site (see AnySite).
//
// It stores nothing and returns an error when the site is not a host name,
// IP address or URL, is a public suffix ("com", "co.uk"; a loopback name
// such as localhost is its own site), or a pair is one Set refuses.
func (j *Jar) SetString(site, cookies string) error {
	domain, err := siteDomain(site)
	if err != nil {
		return err
	}
	var cs []Cookie
	for pair := range strings.SplitSeq(cookies, ";") {
		pair = trimWSP(pair)
		if pair == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			name, value = "", pair
		}
		name, value = trimWSP(name), trimWSP(value)
		if err := checkPair(name, value); err != nil {
			return fmt.Errorf("cookies: cookie %q: %w", name, err)
		}
		cs = append(cs, Cookie{
			Name:   name,
			Value:  value,
			Domain: domain,
			Path:   "/",
		})
	}
	for _, c := range cs {
		if err := j.Set(c); err != nil {
			return err
		}
	}
	return nil
}

// String returns site's cookies as a Cookie header string ("a=1; b=2"),
// for SetString or another tool: every unexpired, unpartitioned cookie of
// the site's registrable domain (site as SetString takes it), Domain or
// host-only, of any host and path, in creation order; for AnySite, the
// AnySite cookies. Hosts, paths and attributes are lost; WriteJSON keeps
// them. It is empty for a site SetString would refuse.
func (j *Jar) String(site string) string {
	domain, err := siteDomain(site)
	if err != nil {
		return ""
	}
	now := j.clock.Now()
	j.mu.Lock()
	var es []entry
	for _, e := range j.domains[domainKey(domain)] {
		c := e.c
		expired := !c.Expires.IsZero() && !c.Expires.After(now)
		if expired || c.Partition != "" || (domain == AnySite && (c.Domain != AnySite || c.HostOnly)) {
			continue
		}
		es = append(es, e)
	}
	slices.SortFunc(es, compareCreated)
	pairs := make([]string, len(es))
	for i, e := range es {
		pairs[i] = e.c.pair()
	}
	j.mu.Unlock()
	return strings.Join(pairs, "; ")
}

// FromString returns a new jar (New(cfg)) holding site's cookies from a
// Cookie header string (SetString).
func FromString(site, cookies string, cfg Config) (*Jar, error) {
	j, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if err := j.SetString(site, cookies); err != nil {
		return nil, err
	}
	return j, nil
}

// siteDomain is the domain SetString keeps site's cookies under: the
// registrable domain of a host name, IP address or URL, or AnySite.
func siteDomain(s string) (string, error) {
	if s == AnySite {
		return AnySite, nil
	}
	host := s
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("cookies: site: %w", err)
		}
		host = u.Hostname()
	} else if h, _, err := net.SplitHostPort(s); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "" || (net.ParseIP(host) == nil && strings.ContainsFunc(host, notHostChar)) {
		return "", fmt.Errorf("cookies: site %q is not a host name, IP address or URL", s)
	}
	domain := site.Registrable(host)
	if site.IsPublicSuffix(domain) && !site.Loopback(domain) {
		return "", fmt.Errorf("cookies: site %q is a public suffix, not a site", s)
	}
	return domain, nil
}

// notHostChar reports a character no (ASCII) host name holds.
func notHostChar(r rune) bool {
	return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' && r != '_'
}

// SetHTTPCookies stores cs as if a top-level navigation to u had received
// them as Set-Cookie lines, through the profile's rules: for cookies a
// solver or another library hands over as []*http.Cookie. Each is written
// from its fields (Raw is ignored); an http.Cookie has no host-only flag,
// so a Domain makes a Domain cookie and none a host-only cookie for u's
// host, as in a Set-Cookie line. It stores every cookie it can and returns
// an error naming each one refused and why (also told to OnSetCookie,
// unless its name, value, Path or Domain could not be written as a line).
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

// setCookieLine writes c as a Set-Cookie line. Unlike http.Cookie.String,
// it keeps the value as given (the jar's rules judge it) and refuses what
// would change the line's meaning: ";" or a control character in the name,
// value, Path or Domain, or "=" in the name.
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
		b.WriteString(`"` + c.Value + `"`)
	} else {
		b.WriteString(c.Value)
	}
	if c.Path != "" {
		b.WriteString("; Path=" + c.Path)
	}
	if c.Domain != "" {
		b.WriteString("; Domain=" + c.Domain)
	}
	if !c.Expires.IsZero() {
		b.WriteString("; Expires=" + c.Expires.UTC().Format(http.TimeFormat))
	}
	switch {
	case c.MaxAge > 0:
		b.WriteString("; Max-Age=" + strconv.Itoa(c.MaxAge))
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
// and wget read. The format has no SameSite, partition, creation time or
// way to name every site, and separates fields with tabs: partitioned and
// AnySite cookies and cookies with a tab in a field are left out, and the
// rest lose those attributes.
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
