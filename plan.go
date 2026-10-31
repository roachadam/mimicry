package mimicry

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/engine/native"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// Plan is what RoundTrip would send for a request, worked out without
// sending it (Transport.Plan): the profile's list it takes, its header
// lines over each protocol, the protocol it would go over, its CORS
// preflight, and the headers the caller set in place of the browser's.
type Plan struct {
	// Method and URL are the request's as sent: a redirect's hop as the
	// browser sends it (a PUT answered 301 stays a PUT).
	Method string
	URL    *url.URL
	// Kind is the request's kind: Navigate by default; Preflight in
	// Plan.Preflight's plan.
	Kind Kind
	// Variant is the JSON path of the profile lists the request takes:
	// "headers", "variants[2]", "kinds.fetch.headers" or
	// "kinds.fetch.variants[4]"; empty for an Exact request.
	Variant string
	// Condition is what picks the variant, in profile.Variant's JSON
	// names: "method PUT", "site cross-site", then each flag that holds
	// ("corsChecked", "redirected", ...). Compare it with Variant to see
	// whether an exact variant was found. Empty for an Exact request.
	Condition []string
	// Protocol is the protocol the request would use now, as a response's
	// Proto ("HTTP/2.0", "HTTP/1.1", "HTTP/3.0"). Empty when only
	// connecting decides it (ALPN, a QUIC attempt, coalescing by DNS).
	Protocol string
	// HTTP2, HTTP1 and HTTP3 are the request's lines over each protocol,
	// whichever it goes over. HTTP3 is nil when the Transport speaks no
	// HTTP/3 (the profile has no http3 section, or Config.DisableHTTP3).
	HTTP2 Lines
	HTTP1 Lines
	HTTP3 *Lines
	// Preflight is the CORS preflight RoundTrip would send first, planned
	// as a request of its own; nil when the request needs none or a
	// cached answer allows it. Whether its answer allows the request
	// only the server can say.
	Preflight *Plan
	// Overrides are the headers the request or Config.Header set in
	// place of a value the profile or mimicry would have sent: a default,
	// a computed header (Referer, Origin, sec-fetch-*), or the jar's
	// cookies. A Connection: close (or req.Close) is one over HTTP/1.1
	// only: HTTP/2 and HTTP/3 send no Connection.
	Overrides []Override
	// Dropped are the headers the profile would have sent that the
	// request's HeaderOrderKey does not name, so are not sent; lowercase.
	Dropped []string

	// protocols are the protocols the request may take now: Protocol, or
	// those decided between when connecting, the likeliest first.
	protocols []string
}

// Lines is a request's head over one protocol, as it would go on the
// wire.
type Lines struct {
	// RequestLine is HTTP/1.1's ("GET /path HTTP/1.1"); empty over
	// HTTP/2 and HTTP/3, whose pseudo-headers carry it.
	RequestLine string
	// Headers are the header lines in order: pseudo-headers first and
	// lowercase over HTTP/2 and HTTP/3; over HTTP/1.1 as the browser
	// spells them, Host, Connection and framing included. Cookie is the
	// jar's current value.
	Headers []Header
	// Err is why the request cannot use this protocol (Headers is then
	// empty): the error RoundTrip would return, or why it never goes
	// there (plain http:// over HTTP/2 and HTTP/3, proxies over HTTP/3).
	Err error
}

// Header is one header line.
type Header struct {
	Name  string
	Value string
}

// Override is a header the caller set in place of mimicry's value.
type Override struct {
	// Name is lowercase.
	Name string
	// Mimicry is the value the profile or mimicry would have sent (for
	// Cookie, the jar's line).
	Mimicry string
	// Caller are the values sent instead; none when the caller suppressed
	// the header (a key with no values).
	Caller []string
}

// planner is an engine that plans requests: the pure-Go engine.
type planner interface {
	Plan(req *http.Request) (*native.Plan, error)
}

// Plan returns what RoundTrip would send for req now, without sending
// anything, opening connections, resolving names, or changing any
// Transport state. It takes RoundTrip's own path, so the lines match
// what RoundTrip writes as long as nothing changes in between. Pass req
// as RoundTrip gets it (As, With or Exact; a redirect hop with its
// Response); its body is never read.
//
// Plan returns the errors RoundTrip would return before sending
// (ErrNotMeasured, ErrPlacement, ...); per-protocol errors go in Lines.
// It never returns a *CORSError, which needs the server's answer.
func (t *Transport) Plan(req *http.Request) (*Plan, error) {
	e, ok := t.engine.(planner)
	if !ok {
		return nil, errors.New("mimicry: the transport's engine plans nothing")
	}

	r, exact := madeAs(req)

	req, o, err := t.outgoing(req, r, exact, toPlan)
	if err != nil {
		return nil, err
	}

	var pre *Plan
	if pf := o.preflight; pf != nil {
		pre = t.plan(Request{
			Kind: Preflight,
		}, false, pf.req, pf.s)
		if err := pre.fill(e, pf.req); err != nil {
			return nil, err
		}
	}

	p := t.plan(r, exact, req, o.s)
	if err := p.fill(e, t.ready(req, o, toPlan)); err != nil {
		return nil, err
	}

	p.Preflight = pre

	return p, nil
}

// plan starts the plan of req, made as r (or exact) and resolved as s:
// its lists, condition and overrides. Call it before ready adds the
// jar's cookies to s's headers.
func (t *Transport) plan(r Request, exact bool, req *http.Request, s *sending) *Plan {
	p := &Plan{
		Kind: r.Kind,
	}
	if exact {
		p.Overrides = t.connectionOverride(req, exact, s, nil)
		p.Overrides = t.cookieOverride(s, p.Overrides)

		return p
	}

	prefix := ""
	if r.Kind != Navigate {
		prefix = "kinds." + r.Kind.String() + "."
	}

	if s.variant < 0 {
		p.Variant = prefix + "headers"
	} else {
		p.Variant = prefix + "variants[" + strconv.Itoa(s.variant) + "]"
	}

	p.Condition = conditionOf(s.cond)

	var lists [][]profile.Header

	if s.plan != nil {
		lists = append(lists, s.plan.Headers, s.plan.HTTP1)
	} else {
		lists = append(lists, t.profile.Headers)
		if t.profile.HTTP1 != nil {
			lists = append(lists, t.profile.HTTP1.Headers)
		}
	}

	// The connection builds framing from the request, never the header map.
	header := headerorder.WithoutFraming(s.header)
	for _, list := range lists {
		replaced, dropped := headerorder.Replaced(list, header)
		for _, rp := range replaced {
			if !slices.ContainsFunc(p.Overrides, func(o Override) bool {
				return o.Name == rp.Name
			}) {
				p.Overrides = append(p.Overrides, Override{
					Name:    rp.Name,
					Mimicry: rp.List,
					Caller:  rp.Values,
				})
			}
		}

		for _, name := range dropped {
			if !slices.Contains(p.Dropped, name) {
				p.Dropped = append(p.Dropped, name)
			}
		}
	}

	p.Overrides = t.connectionOverride(req, exact, s, p.Overrides)
	p.Overrides = t.cookieOverride(s, p.Overrides)

	return p
}

// connectionOverride adds a close (a caller's Connection: close, or
// req.Close) that HTTP/1.1 sends in place of the list's value: the
// profile's Connection slot, or keep-alive where no HTTP/1.1 list
// applies (as h1 writes it). A list without the slot sends neither.
func (t *Transport) connectionOverride(req *http.Request, exact bool, s *sending, overrides []Override) []Override {
	var caller []string

	for k, v := range s.header {
		if strings.EqualFold(k, "connection") && httpguts.HeaderValuesContainsToken(v, "close") {
			caller = v
			break
		}
	}

	if caller == nil && req.Close {
		caller = []string{"close"}
	}

	if caller == nil {
		return overrides
	}

	var h1 []profile.Header

	switch {
	case exact:
	case s.plan != nil:
		h1 = s.plan.HTTP1
	case t.profile.HTTP1 != nil:
		h1 = t.profile.HTTP1.Headers
	}

	mimicry := "keep-alive"
	if len(h1) > 0 {
		i := slices.IndexFunc(h1, func(hd profile.Header) bool {
			return strings.EqualFold(hd.Name, "connection")
		})
		if i < 0 {
			return overrides
		}

		mimicry = h1[i].Value
	}

	return append(overrides, Override{
		Name:    "connection",
		Mimicry: mimicry,
		Caller:  caller,
	})
}

// cookieOverride adds to overrides the request's own Cookie header where
// it replaces cookies the jar would send.
func (t *Transport) cookieOverride(s *sending, overrides []Override) []Override {
	if t.jar == nil || !s.credentialed || !hasHeader(s.header, "Cookie") || slices.ContainsFunc(overrides, func(o Override) bool {
		return o.Name == "cookie"
	}) {
		return overrides
	}

	jar := cookies.Header(t.jar.Peek(s.cookies))

	var caller []string

	for k, v := range s.header {
		if strings.EqualFold(k, "Cookie") {
			caller = append(caller, v...)
		}
	}

	if jar == "" || slices.Equal(caller, []string{jar}) {
		return overrides
	}

	return append(overrides, Override{
		Name:    "cookie",
		Mimicry: jar,
		Caller:  caller,
	})
}

// conditionOf lists a variant condition as Plan.Condition does.
func conditionOf(v profile.Variant) []string {
	method, site := v.Method, v.Site
	if method == "" {
		method = http.MethodGet
	}

	if site == profile.SiteSameOrigin {
		site = "same-origin"
	}

	out := []string{"method " + method, "site " + site}
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"corsChecked", v.CORSChecked},
		{"pageSet", v.PageSet},
		{"redirected", v.Redirected},
		{"crossOriginRedirect", v.CrossOriginRedirect},
		{"crossSiteRedirect", v.CrossSiteRedirect},
		{"originBeforeRedirect", v.OriginBeforeRedirect},
		{"methodChangedByRedirect", v.MethodChangedByRedirect},
		{"insecureBeforeRedirect", v.InsecureBeforeRedirect},
		{"insecure", v.Insecure},
	} {
		if f.set {
			out = append(out, f.name)
		}
	}

	return out
}

// fill sets p's lines and protocol from the engine's plan of req, ready
// as RoundTrip hands it to the engine, and returns the error RoundTrip
// would: when none of the protocols req may take now can carry it, the
// likeliest's.
func (p *Plan) fill(e planner, req *http.Request) error {
	ep, err := e.Plan(req)
	if err != nil {
		return publicError(err)
	}

	p.Method, p.URL = methodOf(req), req.URL
	p.protocols = ep.Protocols

	if len(ep.Protocols) == 1 {
		p.Protocol = ep.Protocols[0]
	}

	p.HTTP2, p.HTTP1 = linesOf(ep.HTTP2), linesOf(ep.HTTP1)
	if ep.HTTP3 != nil {
		l := linesOf(*ep.HTTP3)

		p.HTTP3 = &l
	}

	var first error
	for i, name := range p.protocols {
		l := p.lines(name)
		if l == nil {
			continue
		}

		if l.Err == nil {
			return nil
		}

		if i == 0 {
			first = l.Err
		}
	}

	return first
}

// linesOf is a Lines from the engine's head.
func linesOf(h native.Head) Lines {
	l := Lines{
		RequestLine: h.Line,
		Err:         h.Err,
	}
	for _, f := range h.Fields {
		l.Headers = append(l.Headers, Header(f))
	}

	return l
}

// lines returns p's Lines for protocol name, or nil.
func (p *Plan) lines(name string) *Lines {
	switch name {
	case native.HTTP2:
		return &p.HTTP2
	case native.HTTP1:
		return &p.HTTP1
	case native.HTTP3:
		return p.HTTP3
	}

	return nil
}

// String returns p as lines for a log: the request, its kind, variant
// and condition, protocol, overrides and dropped headers, its preflight,
// then each protocol's lines. Values are as sent, credentials and
// cookies included.
func (p *Plan) String() string {
	var b strings.Builder
	p.write(&b, "")

	return b.String()
}

// write writes p (String) with each line indented by indent.
func (p *Plan) write(w io.Writer, indent string) {
	line := func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, indent+format+"\n", args...)
	}
	target := ""

	if p.URL != nil {
		target = p.URL.Redacted()
	}

	line("%s %s", p.Method, target)
	line("kind: %s", p.Kind)

	if p.Variant == "" {
		line("variant: none (Exact)")
	} else {
		line("variant: %s (%s)", p.Variant, strings.Join(p.Condition, ", "))
	}

	if p.Protocol != "" {
		line("protocol: %s", p.Protocol)
	} else {
		line("protocol: decided when connecting (%s)", strings.Join(p.protocols, " or "))
	}

	for _, o := range p.Overrides {
		caller := "suppressed"
		if len(o.Caller) > 0 {
			caller = strconv.Quote(strings.Join(o.Caller, ", "))
		}

		line("override: %s: %q replaced by %s", o.Name, o.Mimicry, caller)
	}

	if len(p.Dropped) > 0 {
		line("dropped by %s: %s", HeaderOrderKey, strings.Join(p.Dropped, ", "))
	}

	if p.Preflight != nil {
		line("preflight:")
		p.Preflight.write(w, indent+"  ")
	}

	protocols := []struct {
		name string
		l    *Lines
	}{
		{native.HTTP2, &p.HTTP2},
		{native.HTTP1, &p.HTTP1},
		{native.HTTP3, p.HTTP3},
	}
	for _, pr := range protocols {
		if pr.l == nil {
			continue
		}

		if pr.l.Err != nil {
			line("%s: %v", pr.name, pr.l.Err)

			continue
		}

		line("%s:", pr.name)

		if pr.l.RequestLine != "" {
			line("  %s", pr.l.RequestLine)
		}

		for _, h := range pr.l.Headers {
			line("  %s: %s", h.Name, h.Value)
		}
	}
}
