package cookies

import (
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
}

// Jar keeps cookies. It is safe for concurrent use.
type Jar struct {
	rules       profile.Cookies
	clock       clockwork.Clock
	onSetCookie func(Event)

	mu sync.Mutex
	// cookies are in creation order.
	cookies []*Cookie
}

// New returns an empty jar.
func New(cfg Config) *Jar {
	j := &Jar{
		clock:       cfg.Clock,
		onSetCookie: cfg.OnSetCookie,
	}
	if cfg.Rules != nil {
		j.rules = *cfg.Rules
	} else {
		j.rules = *profile.DefaultCookies()
	}
	if j.clock == nil {
		j.clock = clockwork.NewRealClock()
	}
	return j
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
		if !site.Same(u, ref) {
			c.crossSite = true
		}
	}
	if j.rules.Redirects == profile.RedirectsBlockAfterCrossSite {
		for _, u := range r.Redirects {
			if !site.Same(u, ref) {
				c.blocked = true
			}
		}
	}
	return c
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
	events := make([]Event, 0, len(lines))
	j.mu.Lock()
	for _, line := range lines {
		events = append(events, j.set(r, line))
	}
	j.mu.Unlock()
	if j.onSetCookie != nil {
		for _, e := range events {
			j.onSetCookie(e)
		}
	}
}

func (j *Jar) set(r Request, line string) Event {
	e := Event{
		URL:  r.URL,
		Line: line,
	}
	reject := func(why string) Event {
		e.Rejected = why
		return e
	}
	now := j.clock.Now()
	p, err := parse(line, j.rules.Nameless, now)
	if err != nil {
		return reject(err.Error())
	}
	ctx := j.contextOf(r)
	if ctx.blocked {
		return reject("set after a cross-site redirect, which this browser blocks")
	}
	host := strings.ToLower(r.URL.Hostname())
	secureURL := r.URL.Scheme == "https"
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
		c.Path = defaultPath(r.URL.Path)
	}
	if c.Secure && !secureURL {
		return reject("Secure from an insecure URL")
	}
	lower := strings.ToLower(c.Name)
	if strings.HasPrefix(lower, "__secure-") && !c.Secure {
		return reject("__Secure- prefix without Secure")
	}
	if strings.HasPrefix(lower, "__host-") && (!c.Secure || !c.HostOnly || c.Path != "/") {
		return reject("__Host- prefix needs Secure, no Domain and Path=/")
	}
	if c.SameSite == SameSiteNone && !c.Secure && j.rules.NoneRequiresSecure {
		return reject("SameSite=None without Secure")
	}
	if c.Partitioned && !c.Secure {
		return reject("Partitioned without Secure")
	}
	if !r.Navigation && ctx.crossSite && j.effective(c.SameSite) != SameSiteNone {
		return reject("SameSite=" + j.effective(c.SameSite).String() + " from a cross-site response")
	}
	switch {
	case ctx.thirdParty && j.rules.ThirdParty == profile.ThirdPartyPartitionAll:
		c.Partition = ctx.topSite
	case ctx.thirdParty && j.rules.ThirdParty == profile.ThirdPartyPartitionedOnly && !c.Partitioned:
		return reject("an unpartitioned third-party cookie, which this browser blocks")
	case c.Partitioned:
		c.Partition = ctx.topSite
	}
	if !secureURL {
		// An insecure URL cannot shadow a Secure cookie (RFC 6265bis 5.7).
		for _, old := range j.cookies {
			if old.Secure && old.Name == c.Name && (domainMatch(c.Domain, old.Domain) || domainMatch(old.Domain, c.Domain)) && pathMatch(c.Path, old.Path) {
				return reject("would shadow a Secure cookie from an insecure URL")
			}
		}
	}
	i := slices.IndexFunc(j.cookies, func(old *Cookie) bool {
		return sameKey(old, c)
	})
	if !c.Expires.IsZero() && !c.Expires.After(now) {
		if i >= 0 {
			j.cookies = slices.Delete(j.cookies, i, i+1)
		}
		e.Deleted = true
		return e
	}
	if i >= 0 {
		// A replacement keeps the old cookie's creation time and place.
		c.Created = j.cookies[i].Created
		j.cookies[i] = c
	} else {
		j.cookies = append(j.cookies, c)
	}
	cp := *c
	e.Cookie = &cp
	return e
}

// Cookies returns the cookies the browser would send with r, in its order.
// It updates their last-access times.
func (j *Jar) Cookies(r Request) []*Cookie {
	ctx := j.contextOf(r)
	if ctx.blocked {
		return nil
	}
	now := j.clock.Now()
	host := strings.ToLower(r.URL.Hostname())
	secureURL := r.URL.Scheme == "https"
	safe := r.Method == "" || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || r.Method == http.MethodTrace
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cookies = slices.DeleteFunc(j.cookies, func(c *Cookie) bool {
		return !c.Expires.IsZero() && !c.Expires.After(now)
	})
	var out []*Cookie
	for _, c := range j.cookies {
		switch {
		case c.HostOnly && host != c.Domain:
			continue
		case !c.HostOnly && !domainMatch(host, c.Domain):
			continue
		case !pathMatch(r.URL.Path, c.Path):
			continue
		case c.Secure && !secureURL:
			continue
		}
		if c.Partition != "" {
			if c.Partition != ctx.topSite {
				continue
			}
		} else if ctx.thirdParty && j.rules.ThirdParty != profile.ThirdPartySameSite {
			continue // Firefox and Safari keep the unpartitioned jar first-party
		}
		if ctx.crossSite {
			switch j.effective(c.SameSite) {
			case SameSiteStrict:
				continue
			case SameSiteLax:
				unsafeOK := c.SameSite == SameSiteUnset && j.rules.LaxAllowsUnsafeSeconds > 0 &&
					now.Sub(c.Created) < time.Duration(j.rules.LaxAllowsUnsafeSeconds)*time.Second
				if !r.Navigation || (!safe && !unsafeOK) {
					continue
				}
			}
		}
		c.LastAccess = now
		cp := *c
		out = append(out, &cp)
	}
	j.sortForRequest(out)
	return out
}

// sortForRequest puts cookies in the browser's order. The input is in
// creation order, which the stable sort keeps among equals.
func (j *Jar) sortForRequest(cs []*Cookie) {
	slices.SortStableFunc(cs, func(a, b *Cookie) int {
		if d := len(b.Path) - len(a.Path); d != 0 {
			return d
		}
		if j.rules.Order != profile.OrderDomainName {
			return a.Created.Compare(b.Created)
		}
		if d := strings.Compare(storedDomain(a), storedDomain(b)); d != 0 {
			return d
		}
		return strings.Compare(a.Name, b.Name)
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

// defaultPath is a cookie's path when it sets none: the request path's
// directory (RFC 6265bis 5.1.4).
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
