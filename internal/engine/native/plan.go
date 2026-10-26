package native

import (
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/roachadam/mimicry/internal/h1"
	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/internal/h3"
	"github.com/roachadam/mimicry/internal/pool"
)

// The protocols, as a Plan names them: as responses' Proto does.
const (
	HTTP1 = "HTTP/1.1"
	HTTP2 = "HTTP/2.0"
	HTTP3 = "HTTP/3.0"
)

// errPlainHTTP is HTTP/2's head for a plain-HTTP request.
var errPlainHTTP = errors.New("mimicry: plain http:// goes over HTTP/1.1 only")

// Field is one header line as a connection writes it.
type Field struct {
	Name  string
	Value string
}

// Head is a request's head over one protocol: HTTP/1.1's request line
// (Line; empty for the others) and the header lines in order,
// pseudo-headers first over HTTP/2 and HTTP/3. Err is why the request
// cannot go over the protocol, Line and Fields empty then: what the
// connection would return before sending anything, or why the engine
// never sends it there.
type Head struct {
	Line   string
	Fields []Field
	Err    error
}

// Plan is what the engine would write for a request.
type Plan struct {
	// Protocols are the protocols the request may take now, given the
	// open connections and the origin's Alt-Svc: one when that is decided
	// before connecting, else those it is decided between when
	// connecting (ALPN, a QUIC attempt it waits for, another host's
	// HTTP/2 connection found by lookup), the likeliest first.
	Protocols []string
	HTTP2     Head
	HTTP1     Head
	// HTTP3 is nil when the engine speaks no HTTP/3 (the profile has
	// none, or Config.DisableHTTP3).
	HTTP3 *Head
}

// Plan returns what RoundTrip would write for req: each protocol's head,
// built by the code that protocol's connection writes with (h1.Writer,
// h2.Head, h3.Head) from the same route and request RoundTrip starts
// from (begin), and the protocols it may take now. It dials nothing,
// looks nothing up, and changes no connection, pool, ticket or HTTP/3
// state. Its error is one RoundTrip returns whatever the protocol (a URL
// it refuses, the proxy's).
func (e *Engine) Plan(req *http.Request) (*Plan, error) {
	r, req, _, err := e.begin(req)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		Protocols: e.protocolsNow(req, r),
	}
	tcpErr := e.tcpRefusal(req.Context())
	switch {
	case req.URL.Scheme != "https":
		p.HTTP2.Err = errPlainHTTP
	case tcpErr != nil:
		p.HTTP2.Err = tcpErr
	default:
		fields, err := h2.Head(e.profile, req)
		p.HTTP2.Err = err
		for _, f := range fields {
			p.HTTP2.Fields = append(p.HTTP2.Fields, Field(f))
		}
	}
	if tcpErr != nil {
		p.HTTP1.Err = tcpErr
	} else {
		w := h1.NewWriter(e.profile)
		if r.forward {
			w = w.Forward(e.profile.Proxy)
		}
		h, err := w.Head(req)
		p.HTTP1.Line, p.HTTP1.Err = h.Line, err
		for _, f := range h.Fields {
			p.HTTP1.Fields = append(p.HTTP1.Fields, Field(f))
		}
	}
	if e.quic != nil {
		p.HTTP3 = &Head{}
		if err := e.h3Refusal(req, r); err != nil {
			p.HTTP3.Err = err
		} else {
			fields, err := h3.Head(e.profile, r.host, e.altPort(r), req)
			p.HTTP3.Err = err
			for _, f := range fields {
				p.HTTP3.Fields = append(p.HTTP3.Fields, Field(f))
			}
		}
	}
	return p, nil
}

// protocolsNow are the protocols a request on r may take now
// (Plan.Protocols): HTTP/3 as quicFor would decide it (h3Now), and on
// TCP the pool's connection for the route, HTTP/1.1 for plain HTTP, or
// either by ALPN.
func (e *Engine) protocolsNow(req *http.Request, r route) []string {
	var out []string
	if e.h3Route(req, r) {
		quic, decided := e.h3Now(r)
		if quic || !decided {
			out = append(out, HTTP3)
		}
		if decided && quic {
			return out
		}
	}
	if req.URL.Scheme != "https" {
		return append(out, HTTP1)
	}
	switch e.pool.Peek(r.key) {
	case pool.FoundShared:
		return append(out, HTTP2)
	case pool.FoundExclusive:
		return append(out, HTTP1)
	}
	return append(out, HTTP2, HTTP1)
}

// h3Now reports whether a request on r goes over HTTP/3 now, as quicFor
// decides it, and whether that is decided before connecting: not while
// it waits for an attempt (wayRace, a resumed attempt it holds for) or
// for the origin's reconnect time. It changes nothing.
func (e *Engine) h3Now(r route) (quic, decided bool) {
	if _, ok := e.quic.pool.Shared(h3Key(r)); ok {
		return true, true
	}
	q := e.quic
	q.mu.Lock()
	defer q.mu.Unlock()
	o := q.origins[r.key]
	now := e.clock.Now()
	switch {
	case o == nil || !o.available(now):
		return false, true
	case o.reconnect.After(now):
		return false, false
	}
	_, tcpOpen := e.pool.Shared(r.key)
	switch e.pickWay(o, tcpOpen) {
	case wayRace:
		return false, false
	case wayResume:
		hold := e.profile.HTTP3.Discovery.Fallback.Hold
		if (o.attempt != nil && o.attempt.resumed && hold) || (o.attempt == nil && e.hasTicket(r)) {
			return false, false
		}
	}
	return false, true
}

// altPort is the port of r's HTTP/3 alternative, which alt-used names:
// the origin's Alt-Svc, or before one is known its own port (h3=":port"
// on the same port, as servers usually advertise it).
func (e *Engine) altPort(r route) string {
	q := e.quic
	q.mu.Lock()
	defer q.mu.Unlock()
	if o := q.origins[r.key]; o != nil && o.hasAlt {
		return strconv.Itoa(int(o.alt.port))
	}
	_, port, _ := net.SplitHostPort(r.addr)
	return port
}
