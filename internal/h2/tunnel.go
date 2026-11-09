package h2

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"

	"github.com/roachadam/mimicry/internal/hpack"
	"github.com/roachadam/mimicry/profile"
)

// Tunnel opens a CONNECT stream (RFC 9113, section 8.5) to authority
// ("host:port") on c, a connection to an https:// proxy: :method and
// :authority in the profile's pseudo-header order, then fields, its
// HEADERS frame at weight. It returns the proxy's response once its
// headers arrive and, for a 2xx, the tunnel: a net.Conn over the stream's
// DATA frames both ways, which outlives ctx and whose Close resets the
// stream with the error code named reset. Any other response comes
// without a tunnel, its body the caller's to read and close. Errors are
// those of RoundTrip: ErrConnUnusable means nothing was sent.
func (c *Conn) Tunnel(ctx context.Context, authority string, fields []profile.Header, weight uint8, reset string) (*http.Response, net.Conn, error) {
	if !httpguts.ValidHostHeader(authority) {
		return nil, nil, fmt.Errorf("h2: invalid authority %q", authority)
	}

	head := requestHead{
		fields: make([]hpack.Field, 0, 2+len(fields)),
		weight: weight,
	}

	for _, name := range c.h2.PseudoHeaderOrder {
		switch name {
		case ":method":
			head.fields = append(head.fields, hpack.Field{
				Name:  name,
				Value: http.MethodConnect,
			})

		case ":authority":
			head.fields = append(head.fields, hpack.Field{
				Name:  name,
				Value: authority,
			})
		}
	}

	if len(head.fields) != 2 {
		return nil, nil, fmt.Errorf("h2: the profile's pseudo-header order %v lacks :method or :authority", c.h2.PseudoHeaderOrder)
	}

	for _, f := range fields {
		if !httpguts.ValidHeaderFieldName(f.Name) {
			return nil, nil, fmt.Errorf("h2: invalid header field name %q", f.Name)
		}

		if !httpguts.ValidHeaderFieldValue(f.Value) {
			// The value is not quoted: it may be a secret.
			return nil, nil, fmt.Errorf("h2: invalid value for header field %q", f.Name)
		}

		head.fields = append(head.fields, hpack.Field{
			Name:  f.Name,
			Value: f.Value,
		})
	}

	s, err := c.newStream(ctx)
	if err != nil {
		return nil, nil, err
	}

	if err := c.writeHeaders(s, head); err != nil {
		c.endStream(s, ending{
			err: err,
		})

		return nil, nil, s.response().err
	}

	r := s.response()
	if r.err != nil {
		return nil, nil, r.err
	}

	if r.resp.StatusCode < 200 || r.resp.StatusCode > 299 {
		return r.resp, nil, nil
	}

	// From here on, only the tunnel's own end ends the stream.
	c.mu.Lock()
	s.stopCancel()
	c.mu.Unlock()

	// The tunnel is the body: nobody reads it as a response's.
	r.resp.Body = http.NoBody

	wctx, wcancel := context.WithCancel(context.Background())

	return r.resp, &tunnel{
		c:       c,
		s:       s,
		reset:   errCode(reset),
		wctx:    wctx,
		wcancel: wcancel,
	}, nil
}

// tunnel is a CONNECT stream as a net.Conn: reads take the stream's DATA
// (Conn.consumed returning credit as for any body), writes send DATA
// within the flow-control windows, and deadlines interrupt both.
type tunnel struct {
	c     *Conn
	s     *stream
	reset http2.ErrCode

	// mu guards the write deadline: wctx ends when it passes, failing the
	// writes under way with it, and a later deadline starts another.
	mu      sync.Mutex
	wctx    context.Context //nolint:containedctx // the write deadline's, set by SetWriteDeadline
	wcancel context.CancelFunc
	wtimer  clockwork.Timer
}

// Read reads what the proxy relays: os.ErrDeadlineExceeded once the read
// deadline passes with nothing buffered, as a socket's.
func (t *tunnel) Read(b []byte) (int, error) {
	return t.s.body.Read(b)
}

// Write sends b in DATA frames, waiting for flow-control credit until the
// write deadline.
func (t *tunnel) Write(b []byte) (int, error) {
	t.mu.Lock()

	ctx := t.wctx
	t.mu.Unlock()

	if err := t.c.sendData(ctx, t.s, b, false); err != nil {
		if ctx.Err() != nil {
			return 0, os.ErrDeadlineExceeded
		}

		return 0, err
	}

	return len(b), nil
}

// Close resets the stream with the tunnel's error code, unless it has
// ended already (the proxy ended or reset it).
func (t *tunnel) Close() error {
	t.c.reset(t.s, t.reset, net.ErrClosed)
	t.s.body.setDeadline(time.Time{}, t.c.clock)

	t.mu.Lock()

	if t.wtimer != nil {
		t.wtimer.Stop()
	}

	t.wcancel()
	t.mu.Unlock()

	return nil
}

func (t *tunnel) LocalAddr() net.Addr {
	return t.c.conn.LocalAddr()
}

func (t *tunnel) RemoteAddr() net.Addr {
	return t.c.conn.RemoteAddr()
}

func (t *tunnel) SetDeadline(d time.Time) error {
	_ = t.SetReadDeadline(d)

	return t.SetWriteDeadline(d)
}

func (t *tunnel) SetReadDeadline(d time.Time) error {
	t.s.body.setDeadline(d, t.c.clock)

	return nil
}

func (t *tunnel) SetWriteDeadline(d time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.wtimer != nil {
		t.wtimer.Stop()

		t.wtimer = nil
	}

	if t.wctx.Err() != nil {
		// The last deadline passed: writes from now on wait again.
		t.wctx, t.wcancel = context.WithCancel(context.Background())
	}

	switch left := d.Sub(t.c.clock.Now()); {
	case d.IsZero():
	case left > 0:
		t.wtimer = t.c.clock.AfterFunc(left, t.wcancel)
	default:
		t.wcancel()
	}

	return nil
}

// deadline is a pipe's read deadline, set on a tunnel's: expired once it
// passed. gen tells a timer set for an earlier deadline from the current.
type deadline struct {
	expired bool
	gen     uint64
	timer   clockwork.Timer
}

// setDeadline sets p's read deadline: zero clears it, a past one expires
// it at once, a later one when clock reaches it.
func (p *pipe) setDeadline(d time.Time, clock clockwork.Clock) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dl == nil {
		if d.IsZero() {
			return
		}

		p.dl = &deadline{}
	}

	dl := p.dl
	if dl.timer != nil {
		dl.timer.Stop()

		dl.timer = nil
	}

	dl.gen++

	dl.expired = false

	if d.IsZero() {
		return
	}

	left := d.Sub(clock.Now())
	if left <= 0 {
		dl.expired = true

		p.cond.Broadcast()

		return
	}

	gen := dl.gen

	dl.timer = clock.AfterFunc(left, func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		if dl.gen == gen {
			dl.expired = true

			p.cond.Broadcast()
		}
	})
}

// expired reports whether p's read deadline has passed. p.mu must be held.
func (p *pipe) expired() bool {
	return p.dl != nil && p.dl.expired
}
