package native

import (
	"errors"
	"net"
	"net/http"
	"strconv"

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

// Head is a request's head over one protocol: the HTTP/1.1 request line
// (Line; empty otherwise) and header lines in order. Err, when set, is why
// the request cannot go over the protocol; Line and Fields are then empty.
type Head struct {
	Line   string
	Fields []Field
	Err    error
}

// Plan is what the engine would write for a request.
type Plan struct {
	// Protocols are the protocols the request may take now, likeliest
	// first; several when the choice is made while connecting.
	Protocols []string
	HTTP2     Head
	HTTP1     Head
	// HTTP3 is nil when the engine speaks no HTTP/3 (the profile has
	// none, or Config.DisableHTTP3).
	HTTP3 *Head
}

// Plan returns what RoundTrip would write for req over each protocol it
// may take now, built by the same code the connections use. It dials,
// looks up and changes nothing; its error is one RoundTrip would return
// for any protocol. See decision 0034.
func (e *Engine) Plan(req *http.Request) (*Plan, error) {
	r, err := e.begin(req)
	if err != nil {
		return nil, err
	}

	req, _ = e.withUnasked(req, r)

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
		w := e.h1
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

// protocolsNow is Plan.Protocols for a request on r.
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
	case pool.FoundDial:
		// A new connection: ALPN picks between them.
	}

	return append(out, HTTP2, HTTP1)
}

// h3Now reports whether a request on r goes over HTTP/3 as quicFor would
// decide, and whether that is decided before connecting. It changes nothing.
func (e *Engine) h3Now(r route) (quic, decided bool) {
	if c, ok := e.quic.pool.Shared(h3Key(r)); ok {
		return !e.overdue(c), true
	}

	q := e.quic
	q.mu.Lock()
	defer q.mu.Unlock()

	o := q.origins[r.key]
	if _, ok := e.alternative(o, r); !ok {
		return false, true
	}

	if o == nil {
		// Known from a shared or loaded cache only: the request finds
		// what the origin's first one would.
		var none origin

		o = &none
	}

	if o.reconnect.After(e.clock.Now()) {
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

	case wayTCP, wayGoAwayRace, wayRevalidate, wayBeside:
		// The request goes on TCP whatever QUIC does beside it.
	}

	return false, true
}

// altPort is the port of r's HTTP/3 alternative, which alt-used names:
// the origin's Alt-Svc, or before one is known its own port (h3=":port"
// on the same port, as servers usually advertise it).
func (e *Engine) altPort(r route) string {
	if port, ok := e.quic.alts.Get(r.alt); ok {
		return strconv.Itoa(int(port))
	}

	_, port, _ := net.SplitHostPort(r.addr)

	return port
}
