package cookies

import (
	"cmp"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/internal/site"
	"github.com/roachadam/mimicry/profile"
)

// Config configures a Jar.
type Config struct {
	// Rules are the browser's cookie rules; nil uses
	// profile.DefaultCookies (RFC 6265bis).
	Rules *profile.Cookies
	// Clock tells the time; nil uses the real clock.
	Clock clockwork.Clock
	// OnSetCookie, when set, is told about every Set-Cookie line the jar
	// receives: stored, or rejected and why. It is called with the jar's
	// lock released, in the order lines arrive.
	OnSetCookie func(Event)
}

// Event reports one Set-Cookie line.
type Event struct {
	URL  *url.URL
	Line string
	// Cookie is the cookie stored, or nil when Rejected says why not. A
	// line that deletes a cookie (expired, Max-Age=0) has neither.
	Cookie   *Cookie
	Rejected string
	Deleted  bool
}

// Request is a request as cookies see it.
type Request struct {
	URL    *url.URL
	Method string
	// Navigation marks a top-level navigation: the request becomes the
	// page.
	Navigation bool
	// Page is the page the request is made from: the page loading a
	// subresource or frame, or the page a navigation leaves. Nil: none (a
	// typed URL).
	Page *url.URL
	// Redirects are the URLs that redirected to URL, first first.
	Redirects []*url.URL
	// WebSocket marks a WebSocket's opening handshake, made from Page: a
	// credentialed subresource, unless the rules say otherwise
	// (profile.Cookies.WebSocketUnpartitioned). A ws:// or wss:// URL
	// counts as http:// or https:// (RFC 6455, section 4.1).
	WebSocket bool
}

// httpSchemes returns r with a ws:// or wss:// URL as http:// or https://.
func httpSchemes(r Request) Request {
	scheme, ok := map[string]string{
		"ws":  "http",
		"wss": "https",
	}[r.URL.Scheme]
	if !ok {
		return r
	}
	u := *r.URL
	u.Scheme = scheme
	r.URL = &u

	return r
}

// Jar keeps cookies. It is safe for concurrent use.
type Jar struct {
	rules       profile.Cookies
	clock       clockwork.Clock
	onSetCookie func(Event)

	mu sync.Mutex
	// domains holds the cookies by the registrable domain of their Domain
	// (domainKey), so that a request looks only at its host's.
	domains map[string][]entry
	// seq is the next entry's sequence number.
	seq uint64
}

// entry is a stored cookie and its sequence number: the order it was
// stored in, which orders cookies created at the same time.
type entry struct {
	c   *Cookie
	seq uint64
}

// compareCreated orders entries by creation: creation time, then the
// order they were stored in.
func compareCreated(a, b entry) int {
	if d := a.c.Created.Compare(b.c.Created); d != 0 {
		return d
	}

	return cmp.Compare(a.seq, b.seq)
}

// domainKey is the bucket for a cookie domain or request host: its
// registrable domain, since a cookie never reaches hosts outside it.
func domainKey(domain string) string {
	if domain == AnySite {
		return AnySite
	}

	return site.Registrable(domain)
}

// New returns an empty jar. Its rules, when given, must be valid
// (profile.Cookies.Validate).
func New(cfg Config) (*Jar, error) {
	j := &Jar{
		clock:       cfg.Clock,
		onSetCookie: cfg.OnSetCookie,
		domains:     map[string][]entry{},
	}
	if cfg.Rules != nil {
		if err := cfg.Rules.Validate(); err != nil {
			return nil, err
		}
		j.rules = *cfg.Rules
	} else {
		j.rules = *profile.DefaultCookies()
	}
	if j.clock == nil {
		j.clock = clockwork.NewRealClock()
	}

	return j, nil
}

// context is how a request relates to its top-level page.
type context struct {
	topSite string
	// crossSite: the request is not same-site with its page, for SameSite
	// (the redirect chain counts where the browser counts it).
	crossSite bool
	// thirdParty: a subresource or frame whose own site is not its
	// top-level page's, for partitions and third-party policies.
	thirdParty bool
	// blocked: the browser sends and keeps nothing for this request.
	blocked bool
}

func (j *Jar) contextOf(r Request) context {
	ref := r.Page
	// The top-level site: a navigation's own, else its page's.
	c := context{
		topSite: site.Of(r.URL),
	}
	if !r.Navigation && r.Page != nil {
		c.topSite = site.Of(r.Page)
	}
	if ref == nil {
		return c
	}
	c.thirdParty = !r.Navigation && !site.Same(r.URL, ref)
	urls := []*url.URL{r.URL}
	if j.rules.Redirects == profile.RedirectsChain {
		urls = append(urls, r.Redirects...)
	}
	for _, u := range urls {
		if !j.sameSite(u, ref) {
			c.crossSite = true
		}
	}
	if j.rules.Redirects == profile.RedirectsBlockAfterCrossSite {
		for _, u := range r.Redirects {
			if !j.sameSite(u, ref) {
				c.blocked = true
			}
		}
	}

	return c
}

// sameSite reports whether a and b are same-site for SameSite: the same
// schemeful site, or only the same registrable domain where the browser
// leaves the scheme out (Firefox, Safari).
func (j *Jar) sameSite(a, b *url.URL) bool {
	if j.rules.SameSiteScheme == profile.SameSiteSchemeless {
		return site.Registrable(a.Hostname()) == site.Registrable(b.Hostname())
	}

	return site.Same(a, b)
}

// secure reports whether u counts as secure for Secure cookies: https, or
// plain http to a loopback host where the browser treats it as secure.
func (j *Jar) secure(u *url.URL) bool {
	return u.Scheme == "https" || (j.rules.SecureLoopback && site.Loopback(u.Hostname()))
}

// effective is a cookie's SameSite with Unset resolved by the rules.
func (j *Jar) effective(s SameSite) SameSite {
	if s != SameSiteUnset {
		return s
	}
	if j.rules.UnsetSameSite == profile.SameSiteNone {
		return SameSiteNone
	}

	return SameSiteLax
}

// SetCookies stores the cookies of a response to r: its raw Set-Cookie
// lines, as received.
func (j *Jar) SetCookies(r Request, lines []string) {
	j.setCookies(r, lines)
}

// setCookies is SetCookies, returning an event per line (in line order)
// after telling OnSetCookie about them.
func (j *Jar) setCookies(r Request, lines []string) []Event {
	r = httpSchemes(r)
	ctx := j.contextOf(r)
	now := j.clock.Now()
	events := make([]Event, 0, len(lines))
	j.mu.Lock()
	for _, line := range lines {
		events = append(events, j.set(setRequest{
			Request: r,
			ctx:     ctx,
			now:     now,
		}, line))
	}
	j.mu.Unlock()
	if j.onSetCookie != nil {
		for _, e := range events {
			j.onSetCookie(e)
		}
	}

	return events
}

// setRequest is a request whose response sets cookies, with what every
// one of its lines shares.
type setRequest struct {
	Request
	ctx context
	now time.Time
}

func (j *Jar) set(r setRequest, line string) Event {
	e := Event{
		URL:  r.URL,
		Line: line,
	}
	reject := func(why string) Event {
		e.Rejected = why
		return e
	}
	now := r.now
	p, err := parse(line, &j.rules, now)
	if err != nil {
		return reject(err.Error())
	}
	if r.ctx.blocked {
		return reject("set after a cross-site redirect, which this browser blocks")
	}
	host := strings.ToLower(r.URL.Hostname())
	secureURL := j.secure(r.URL)
	c := &Cookie{
		Name:        p.name,
		Value:       p.value,
		Path:        p.path,
		Expires:     p.expires,
		Secure:      p.secure,
		HTTPOnly:    p.httpOnly,
		SameSite:    p.sameSite,
		Partitioned: p.partitioned,
		Created:     now,
		LastAccess:  now,
		Raw:         line,
	}
	if j.rules.Latin1Values {
		c.Value = latin1(c.Value)
	}
	switch {
	case p.domain == "":
		c.Domain, c.HostOnly = host, true
	case site.IsPublicSuffix(p.domain) && p.domain != host:
		return reject("Domain is a public suffix")
	case site.IsPublicSuffix(p.domain):
		c.Domain, c.HostOnly = host, true
	case !domainMatch(host, p.domain):
		return reject("Domain does not match the host")
	default:
		c.Domain = p.domain
	}
	if c.Path == "" {
		c.Path = defaultPath(r.URL.EscapedPath())
	}
	if c.Secure && !secureURL {
		return reject("Secure from an insecure URL")
	}
	if c.Name == "" && hiddenPrefix(c.Value) {
		return reject("a nameless cookie whose value starts with __Secure- or __Host-")
	}
	// A server would read the pair as a name (every browser refuses it).
	if c.Name == "" && strings.Contains(c.Value, "=") {
		return reject("a nameless cookie whose value holds \"=\"")
	}
	if hasPrefixFold(c.Name, "__Secure-") && !c.Secure {
		return reject("__Secure- prefix without Secure")
	}
	// __Host- needs Path=/: as the attribute sent (RFC 6265bis), or as
	// stored, the default path counting (Safari).
	hostPath := p.pathAttr
	if j.rules.HostPrefixPath == profile.HostPrefixPathStored {
		hostPath = c.Path
	}
	if hasPrefixFold(c.Name, "__Host-") && (!c.Secure || !c.HostOnly || hostPath != "/") {
		return reject("__Host- prefix needs Secure, no Domain and Path=/")
	}
	// __Host-Http- also meets __Host-'s rules, above.
	if j.rules.HTTPPrefix && (hasPrefixFold(c.Name, "__Http-") || hasPrefixFold(c.Name, "__Host-Http-")) && (!c.Secure || !c.HTTPOnly) {
		return reject("__Http- prefix needs Secure and HttpOnly")
	}
	if c.SameSite == SameSiteNone && !c.Secure && j.rules.NoneRequiresSecure {
		return reject("SameSite=None without Secure")
	}
	if c.Partitioned && !c.Secure {
		return reject("Partitioned without Secure")
	}
	if !r.Navigation && r.ctx.crossSite && j.effective(c.SameSite) != SameSiteNone {
		return reject("SameSite=" + j.effective(c.SameSite).String() + " from a cross-site response")
	}
	switch {
	case r.ctx.thirdParty && j.rules.ThirdParty == profile.ThirdPartyPartitionAll:
		c.Partition = r.ctx.topSite
	case r.ctx.thirdParty && j.rules.ThirdParty == profile.ThirdPartyPartitionedOnly && !c.Partitioned:
		return reject("an unpartitioned third-party cookie, which this browser blocks")
	case c.Partitioned:
		c.Partition = r.ctx.topSite
	}
	key := domainKey(c.Domain)
	bucket := j.domains[key]
	if !secureURL {
		// An insecure URL cannot shadow a Secure cookie (RFC 6265bis 5.7).
		for _, old := range bucket {
			if old.c.Secure && old.c.Name == c.Name && (domainMatch(c.Domain, old.c.Domain) || domainMatch(old.c.Domain, c.Domain)) && pathMatch(c.Path, old.c.Path) {
				return reject("would shadow a Secure cookie from an insecure URL")
			}
		}
	}
	i := slices.IndexFunc(bucket, func(old entry) bool {
		return sameKey(old.c, c)
	})
	if !c.Expires.IsZero() && !c.Expires.After(now) {
		if i >= 0 {
			j.store(key, slices.Delete(bucket, i, i+1))
		}
		e.Deleted = true

		return e
	}
	switch {
	case i >= 0 && j.rules.NewValueRenews && bucket[i].c.Value != c.Value:
		// A new value makes a new cookie, the newest (Chrome).
		j.domains[key] = append(slices.Delete(bucket, i, i+1), j.newEntry(c))
	case i >= 0:
		// A replacement keeps the old cookie's creation time and place.
		c.Created = bucket[i].c.Created
		bucket[i].c = c
	default:
		j.domains[key] = append(bucket, j.newEntry(c))
		j.evict(key, c.Partition, now)
	}
	cp := *c
	e.Cookie = &cp

	return e
}

// newEntry numbers c as the jar's newest cookie. Call with the lock held.
func (j *Jar) newEntry(c *Cookie) entry {
	j.seq++
	return entry{
		c:   c,
		seq: j.seq,
	}
}

// evict keeps the browser's per-site limit on the cookies of partition kept
// under key: once they number more than MaxPerSite, the expired ones go,
// then the least recently used (oldest first among equals) until EvictTo
// remain. Call with the lock held.
func (j *Jar) evict(key, partition string, now time.Time) {
	if j.rules.MaxPerSite == 0 {
		return
	}
	bucket := j.domains[key]
	count := func() int {
		n := 0
		for _, e := range bucket {
			if e.c.Partition == partition {
				n++
			}
		}

		return n
	}
	if count() <= j.rules.MaxPerSite {
		return
	}
	bucket = slices.DeleteFunc(bucket, func(e entry) bool {
		return !e.c.Expires.IsZero() && !e.c.Expires.After(now)
	})
	if excess := count() - j.rules.EvictTo; excess > 0 {
		var lru []entry
		for _, e := range bucket {
			if e.c.Partition == partition {
				lru = append(lru, e)
			}
		}
		slices.SortFunc(lru, func(a, b entry) int {
			if d := a.c.LastAccess.Compare(b.c.LastAccess); d != 0 {
				return d
			}

			return cmp.Compare(a.seq, b.seq)
		})
		evicted := make(map[uint64]bool, excess)
		for _, e := range lru[:excess] {
			evicted[e.seq] = true
		}
		bucket = slices.DeleteFunc(bucket, func(e entry) bool {
			return evicted[e.seq]
		})
	}
	j.store(key, bucket)
}

// store sets the cookies kept under key, dropping the key when there are
// none. Call with the lock held.
func (j *Jar) store(key string, bucket []entry) {
	if len(bucket) == 0 {
		delete(j.domains, key)
		return
	}
	j.domains[key] = bucket
}

// Cookies returns the cookies the browser would send with r, in its order.
// It updates their last-access times.
func (j *Jar) Cookies(r Request) []*Cookie {
	return j.cookies(r, true)
}

// Peek returns the cookies Cookies would, changing nothing: last-access
// times stay, and expired cookies stay stored.
func (j *Jar) Peek(r Request) []*Cookie {
	return j.cookies(r, false)
}

// cookies is Cookies, updating last-access times and dropping expired
// cookies only with touch.
func (j *Jar) cookies(r Request, touch bool) []*Cookie {
	r = httpSchemes(r)
	ctx := j.contextOf(r)
	if ctx.blocked {
		return nil
	}
	now := j.clock.Now()
	host := strings.ToLower(r.URL.Hostname())
	secureURL := j.secure(r.URL)
	safe := r.Method == "" || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || r.Method == http.MethodTrace
	reqPath := r.URL.EscapedPath()
	key := domainKey(host)
	j.mu.Lock()
	defer j.mu.Unlock()
	live := j.live
	if !touch {
		live = j.unexpired
	}
	bucket := live(key, now)
	if anySite := live(AnySite, now); key != AnySite && len(anySite) > 0 {
		// Every site's request also looks at the AnySite cookies.
		bucket = slices.Concat(bucket, anySite)
	}
	// socket: a WebSocket handshake that also carries its host's
	// unpartitioned Lax and None cookies cross-site (Safari 26).
	socket := r.WebSocket && j.rules.WebSocketUnpartitioned
	var matched []entry
	for _, e := range bucket {
		c := e.c
		switch {
		case c.HostOnly && host != c.Domain:
			continue
		case !c.HostOnly && c.Domain != AnySite && !domainMatch(host, c.Domain):
			continue
		case !pathMatch(reqPath, c.Path):
			continue
		case c.Secure && !secureURL:
			continue
		}
		if c.Partition != "" {
			if c.Partition != ctx.topSite {
				continue
			}
		} else if ctx.thirdParty && j.rules.ThirdParty != profile.ThirdPartySameSite && !socket {
			continue // Firefox and Safari keep the unpartitioned jar first-party
		}
		if ctx.crossSite {
			switch j.effective(c.SameSite) {
			case SameSiteStrict:
				continue
			case SameSiteLax:
				unsafeOK := c.SameSite == SameSiteUnset && j.rules.LaxAllowsUnsafeSeconds > 0 &&
					now.Sub(c.Created) < time.Duration(j.rules.LaxAllowsUnsafeSeconds)*time.Second
				socketOK := socket && c.Partition == ""
				if !socketOK && (!r.Navigation || (!safe && !unsafeOK)) {
					continue
				}
			case SameSiteNone, SameSiteUnset:
				// None is sent cross-site; effective never returns Unset.
			}
		}
		if touch {
			c.LastAccess = now
		}
		matched = append(matched, e)
	}
	j.sortForRequest(matched)
	out := make([]*Cookie, len(matched))
	for i, e := range matched {
		cp := *e.c
		out[i] = &cp
	}

	return out
}

// live drops the expired cookies kept under key and returns the rest.
// Call with the lock held.
func (j *Jar) live(key string, now time.Time) []entry {
	bucket := slices.DeleteFunc(j.domains[key], func(e entry) bool {
		return e.expired(now)
	})
	j.store(key, bucket)

	return bucket
}

// unexpired returns the cookies kept under key that have not expired,
// leaving the jar as it is. Call with the lock held.
func (j *Jar) unexpired(key string, now time.Time) []entry {
	var out []entry
	for _, e := range j.domains[key] {
		if !e.expired(now) {
			out = append(out, e)
		}
	}

	return out
}

// expired reports whether e's cookie has expired at now.
func (e entry) expired(now time.Time) bool {
	return !e.c.Expires.IsZero() && !e.c.Expires.After(now)
}

// sortForRequest puts cookies in the browser's order.
func (j *Jar) sortForRequest(es []entry) {
	slices.SortFunc(es, func(a, b entry) int {
		if j.rules.PartitionedFirst && (a.c.Partition == "") != (b.c.Partition == "") {
			if a.c.Partition != "" {
				return -1
			}

			return 1
		}
		if d := len(b.c.Path) - len(a.c.Path); d != 0 {
			return d
		}
		if j.rules.Order == profile.OrderDomainName {
			if d := strings.Compare(storedDomain(a.c), storedDomain(b.c)); d != 0 {
				return d
			}
			if d := strings.Compare(a.c.Name, b.c.Name); d != 0 {
				return d
			}
		}

		return compareCreated(a, b)
	})
}

// storedDomain is the domain as a browser stores it: a leading dot for a
// Domain cookie.
func storedDomain(c *Cookie) string {
	if c.HostOnly {
		return c.Domain
	}

	return "." + c.Domain
}

// Header returns the Cookie header value for cs, in their order: "a=1;
// b=2", a nameless cookie as its value alone.
func Header(cs []*Cookie) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.pair()
	}

	return strings.Join(parts, "; ")
}

// domainMatch reports whether host domain-matches domain (RFC 6265bis
// 5.1.3).
func domainMatch(host, domain string) bool {
	if host == domain {
		return true
	}
	if net.ParseIP(host) != nil {
		return false
	}

	return strings.HasSuffix(host, "."+domain)
}

// pathMatch reports whether a request path matches a cookie path (RFC
// 6265bis 5.1.4).
func pathMatch(reqPath, cookiePath string) bool {
	if reqPath == "" {
		reqPath = "/"
	}
	if reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}

	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

// defaultPath is a cookie's path when it sets none: the directory of the
// request's escaped path (RFC 6265bis 5.1.4).
func defaultPath(p string) string {
	if p == "" || p[0] != '/' {
		return "/"
	}
	i := strings.LastIndex(p, "/")
	if i == 0 {
		return "/"
	}

	return p[:i]
}

// hasPrefixFold reports whether s starts with prefix, ASCII case-blind
// (as browsers match cookie prefixes; no Unicode folding).
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	lower := func(b byte) byte {
		if 'A' <= b && b <= 'Z' {
			return b + 'a' - 'A'
		}

		return b
	}
	for i := range len(prefix) {
		if lower(s[i]) != lower(prefix[i]) {
			return false
		}
	}

	return true
}

// hiddenPrefix reports whether a nameless cookie's value starts, after
// leading whitespace, with a cookie prefix: a server would read it as a
// prefixed name, so browsers refuse it (RFC 6265bis 5.7).
func hiddenPrefix(value string) bool {
	v := strings.TrimLeft(value, " \t")
	return hasPrefixFold(v, "__Secure-") || hasPrefixFold(v, "__Host-")
}

// latin1 re-encodes each byte of v at or above 0x80 as the UTF-8 of the
// Latin-1 character with that code, as Safari does.
func latin1(v string) string {
	ascii := true
	for i := range len(v) {
		if v[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return v
	}
	var b strings.Builder
	for i := range len(v) {
		if v[i] < 0x80 {
			b.WriteByte(v[i])
		} else {
			b.WriteRune(rune(v[i]))
		}
	}

	return b.String()
}
