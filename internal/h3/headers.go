package h3

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/qpack"
	"github.com/roachadam/mimicry/profile"
)

// hopByHop are the headers HTTP/3 never carries (RFC 9114, 4.2).
var hopByHop = map[string]bool{
	"connection":        true,
	"proxy-connection":  true,
	"keep-alive":        true,
	"transfer-encoding": true,
	"upgrade":           true,
	"host":              true,
}

// Measured reports whether p covers a request planned as plan over HTTP/3:
// HTTP3 and QUIC sections and, where its HTTP/3 lists add alt-used, an entry
// for the request's kind (H3Headers.AltUsed). Other requests go on TCP.
func Measured(p *profile.Profile, plan *headerorder.Plan) bool {
	if p.HTTP3 == nil || p.QUIC == nil {
		return false
	}
	hd := p.HTTP3.Headers
	if !sendsAltUsed(hd) || exact(plan) {
		return true
	}
	if plan == nil || plan.Kind == "" {
		return hd.AltUsedNavigation != ""
	}
	_, ok := hd.AltUsed[plan.Kind]

	return ok
}

// sendsAltUsed reports HTTP/3 lists that carry alt-used (Firefox).
func sendsAltUsed(hd *profile.H3Headers) bool {
	return hd != nil && (len(hd.AltUsed) > 0 || hd.AltUsedNavigation != "")
}

// exact reports a request sent with only its own headers (mimicry.Exact).
func exact(plan *headerorder.Plan) bool {
	return plan != nil && plan.Kind == "" && len(plan.Headers) == 0 && len(plan.HTTP1) == 0
}

// head is a request's field list, in the order it is sent, and its
// priority header's value ("" without one).
type head struct {
	fields   []qpack.Field
	priority string
}

// Head returns the field list a connection for p, to the alternative
// service at altHost and altPort (Options), sends for req (RoundTrip),
// pseudo-headers first, built as RoundTrip builds it. An error is one
// RoundTrip returns before sending anything.
func Head(p *profile.Profile, altHost, altPort string, req *http.Request) ([]qpack.Field, error) {
	hd, err := buildHead(p, altHost, altPort, req, bodyLength(req))
	return hd.fields, err
}

// bodyLength is the length of req's body as RoundTrip sends it: 0 for
// none, -1 when unknown (as in net/http, a body with ContentLength 0).
func bodyLength(req *http.Request) int64 {
	if req.Body == nil || req.Body == http.NoBody {
		return 0
	}
	if req.ContentLength <= 0 {
		return -1
	}

	return req.ContentLength
}

// buildHead builds req's field list for p on a connection to the alternative
// service at altHost and altPort: pseudo-headers in the profile's HTTP/3
// order, then the HTTP/2 list as HTTP3.Headers changes it. length is the
// body's (0 for none, -1 unknown). A field invalid on the wire is an error.
func buildHead(p *profile.Profile, altHost, altPort string, req *http.Request, length int64) (head, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	if !httpguts.ValidHeaderFieldName(method) || method == http.MethodConnect {
		return head{}, fmt.Errorf("h3: invalid method %q", method)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	host = strings.TrimSuffix(host, ":")
	if !httpguts.ValidHostHeader(host) {
		return head{}, fmt.Errorf("h3: invalid host %q", host)
	}
	path := req.URL.RequestURI()
	if (!strings.HasPrefix(path, "/") && path != "*") || !httpguts.ValidHeaderFieldValue(path) {
		return head{}, fmt.Errorf("h3: invalid path %q", path)
	}
	pseudo := map[string]string{
		":method":    method,
		":authority": host,
		":scheme":    req.URL.Scheme,
		":path":      path,
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	plan := headerorder.PlanFrom(req.Context())
	if length != 0 {
		for name := range h {
			if strings.EqualFold(name, "Content-Length") {
				delete(h, name)
			}
		}
		if length > 0 && (plan == nil || !plan.NoContentLength) {
			h.Set("Content-Length", strconv.FormatInt(length, 10))
		} else {
			h["Content-Length"] = nil
		}
	}
	list := p.Headers
	var table *profile.HeaderTable
	if plan != nil {
		list, table = plan.Headers, plan.Table
	}
	ordered := headerorder.Build(list, h, func(name string) bool {
		return hopByHop[name]
	}, table)
	hd := p.HTTP3.Headers
	if hd != nil && !exact(plan) {
		ordered = slices.DeleteFunc(ordered, func(f headerorder.Field) bool {
			return slices.Contains(hd.Drop, f.Name)
		})
		if sendsAltUsed(hd) {
			ordered = placeAltUsed(p, altUsedValue(altHost, altPort), ordered, plan)
		}
	}
	ordered, err := headerorder.Place(ordered, headerorder.PlacementsFrom(req.Context()))
	if err != nil {
		return head{}, err
	}
	order := p.HTTP2.PseudoHeaderOrder
	if hd != nil {
		order = hd.PseudoHeaderOrder
	}
	out := head{
		fields: make([]qpack.Field, 0, len(order)+len(ordered)),
	}
	for _, name := range order {
		out.fields = append(out.fields, qpack.Field{
			Name:  name,
			Value: pseudo[name],
		})
	}
	for _, f := range ordered {
		if !httpguts.ValidHeaderFieldName(f.Name) {
			return head{}, fmt.Errorf("h3: invalid header field name %q", f.Name)
		}
		if !httpguts.ValidHeaderFieldValue(f.Value) {
			// The value is not quoted: it may be a secret.
			return head{}, fmt.Errorf("h3: invalid value for header field %q", f.Name)
		}
		if f.Name == "priority" && out.priority == "" {
			out.priority = f.Value
		}
		if f.Name == "cookie" {
			crumbs := headerorder.Crumbs(f.Value)
			if p.HTTP2.SplitCookie {
				for _, crumb := range crumbs {
					out.fields = append(out.fields, qpack.Field{
						Name:  f.Name,
						Value: crumb,
					})
				}

				continue
			}
			f.Value = strings.Join(crumbs, "; ")
		}
		out.fields = append(out.fields, qpack.Field{
			Name:  f.Name,
			Value: f.Value,
		})
	}

	return out, nil
}

// placeAltUsed inserts alt-used with value alt, the connection's
// alternative service (altUsedValue), where p's H3Headers.AltUsed says:
// before the first field named the kind's anchor or a name after it in
// the list the request takes (the kind's own where that lacks the
// anchor), else last.
func placeAltUsed(p *profile.Profile, alt string, fields []headerorder.Field, plan *headerorder.Plan) []headerorder.Field {
	hd := p.HTTP3.Headers
	anchor, own := hd.AltUsedNavigation, p.Headers
	if plan != nil && plan.Kind != "" {
		anchor = hd.AltUsed[plan.Kind]
		if k := p.Kinds[plan.Kind]; k != nil {
			own = k.Headers
		}
	}
	// The list the request takes (its variant's), or the kind's own when
	// that lacks the anchor.
	list := own
	if plan != nil && slices.ContainsFunc(plan.Headers, func(h profile.Header) bool {
		return h.Name == anchor
	}) {
		list = plan.Headers
	}
	after := map[string]bool{
		anchor: true,
	}
	if i := slices.IndexFunc(list, func(h profile.Header) bool {
		return h.Name == anchor
	}); i >= 0 {
		for _, h := range list[i:] {
			after[strings.ToLower(h.Name)] = true
		}
	}
	field := headerorder.Field{
		Name:  "alt-used",
		Value: alt,
	}
	fields = slices.DeleteFunc(fields, func(f headerorder.Field) bool {
		return f.Name == "alt-used"
	})
	at := slices.IndexFunc(fields, func(f headerorder.Field) bool {
		return after[f.Name]
	})
	if at < 0 {
		at = len(fields)
	}

	return slices.Insert(fields, at, field)
}

// altUsedValue is alt-used's value as Firefox writes it
// (nsHttpChannel::BeginConnect): the alternative's host, and ":<port>"
// unless the port is the default, 443.
func altUsedValue(host, port string) string {
	if port == "443" {
		return host
	}

	return net.JoinHostPort(host, port)
}
