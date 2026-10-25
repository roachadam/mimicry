package native

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/h3"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/profile"
)

// brokenFor is how long an origin whose QUIC connection failed is not
// tried over HTTP/3 again: Chromium's first broken-alternative delay
// (5 minutes). Every browser captured tried once per host and page.
const brokenFor = 5 * time.Minute

// noExclusive is the HTTP/3 pool's exclusive connection type: it has
// none.
type noExclusive struct{}

func (noExclusive) Usable() bool {
	return false
}

func (noExclusive) Close() error {
	return nil
}

// origin is what the engine knows of an origin's HTTP/3, by route key.
type origin struct {
	alt    altSvc
	hasAlt bool
	// brokenUntil: its QUIC connection failed; HTTP/3 is not tried
	// before then.
	brokenUntil time.Time
	// used: it has had an HTTP/3 connection, so a request finding none
	// dials one and goes on it (a new one is resumed with 0-RTT where
	// the profile does).
	used bool
	// last is its last HTTP/3 connection, to tell why it ended.
	last *h3.Conn
	// dialing: a QUIC connection is being dialled in the background.
	dialing bool
	// token is the server's last NEW_TOKEN; rtt the last connection's
	// smoothed RTT.
	token []byte
	rtt   time.Duration
}

// quicState is the engine's HTTP/3: what each origin advertised and how
// its connections went, its connections (a pool keyed like the TCP
// pool's, plus " h3") and its QUIC session tickets. Safe for concurrent
// use.
type quicState struct {
	mu      sync.Mutex
	origins map[string]*origin
	pool    *pool.Pool[*h3.Conn, noExclusive]
	tickets *handshake.Tickets
}

// h3Key is r's HTTP/3 pool key.
func h3Key(r route) string {
	return r.key + " h3"
}

// h3Route reports whether req on r may use HTTP/3: the engine speaks it
// for the profile, the request is https and direct (browsers never use
// HTTP/3 through a proxy), and the profile measured its kind over HTTP/3.
func (e *Engine) h3Route(req *http.Request, r route) bool {
	return e.quic != nil && req.URL.Scheme == "https" && r.proxy == nil &&
		h3.Measured(e.profile, headerorder.PlanFrom(req.Context()))
}

// originState returns r's origin state; e.quic.mu must be held.
func (e *Engine) originState(r route) *origin {
	o := e.quic.origins[r.key]
	if o == nil {
		o = &origin{}
		e.quic.origins[r.key] = o
	}
	return o
}

// learnAltSvc records the Alt-Svc of a response to req on r, and starts
// the origin's QUIC connection at once where the profile does
// (SwitchOnAltSvc), until the origin has used HTTP/3: after that a
// request decides (a new connection, or Firefox's race after a GOAWAY),
// not the Alt-Svc of the response that came with the GOAWAY.
func (e *Engine) learnAltSvc(req *http.Request, r route, resp *http.Response) {
	if !e.h3Route(req, r) {
		return
	}
	values := resp.Header.Values("Alt-Svc")
	if len(values) == 0 {
		return
	}
	now := e.clock.Now()
	alt, found, cleared := parseAltSvc(values, r.host, now)
	q := e.quic
	q.mu.Lock()
	o := e.originState(r)
	switch {
	case cleared:
		o.hasAlt = false
	case found:
		o.alt, o.hasAlt = alt, true
	}
	start := found && e.profile.HTTP3.Discovery.Switch == profile.SwitchOnAltSvc && !o.used && e.mayDial(o, r)
	q.mu.Unlock()
	if start {
		e.dialH3Background(r, 0)
	}
}

// mayDial reports whether a background dial may start for o: it has an
// unexpired alternative, is not broken, and has no connection or dial.
// e.quic.mu must be held.
func (e *Engine) mayDial(o *origin, r route) bool {
	now := e.clock.Now()
	if !o.hasAlt || now.After(o.alt.expires) || now.Before(o.brokenUntil) || o.dialing {
		return false
	}
	_, ok := e.quic.pool.Shared(h3Key(r))
	return !ok
}

// tryH3 sends req over HTTP/3 if the origin's discovery rule says so now;
// handled is false when it goes on TCP instead (and may start a QUIC
// connection in the background, as the browser does).
func (e *Engine) tryH3(ctx context.Context, req *http.Request, r route) (resp *http.Response, handled bool, err error) {
	if !e.h3Route(req, r) {
		return nil, false, nil
	}
	key := h3Key(r)
	if c, ok := e.quic.pool.Shared(key); ok {
		return e.roundTripH3(key, c, req)
	}
	d := e.profile.HTTP3.Discovery
	q := e.quic
	q.mu.Lock()
	o := e.originState(r)
	now := e.clock.Now()
	if !o.hasAlt || now.After(o.alt.expires) || now.Before(o.brokenUntil) {
		q.mu.Unlock()
		return nil, false, nil
	}
	race := d.RaceAfterGoAway && o.last != nil && o.last.GoneAway()
	var sync bool
	switch {
	case race:
		// The request goes on TCP; QUIC follows it (Firefox).
		o.last = nil
	case o.used:
		// A request finding no connection after the origin's last one
		// ended dials a new one and goes on it.
		sync = true
	case d.Switch == profile.SwitchOnAltSvc:
		// Requests wait for the QUIC connection the Alt-Svc started.
		sync = true
	case d.Switch == profile.SwitchNewConnection:
		_, open := e.pool.Shared(r.key)
		sync = !open
	}
	// Chrome starts QUIC beside the request; Firefox's race follows its
	// request's TCP dial; Safari never dials in the background.
	background := !sync && (race || d.Switch == profile.SwitchNextRequest) && e.mayDial(o, r)
	q.mu.Unlock()
	if background {
		delay := time.Duration(0)
		if race && d.RaceDelayMillis != nil {
			delay = time.Duration(d.RaceDelayMillis.Min+rand.IntN(d.RaceDelayMillis.Max-d.RaceDelayMillis.Min+1)) * time.Millisecond //nolint:gosec // timing needs no secrecy
		}
		e.dialH3Background(r, delay) //nolint:contextcheck // the dial outlives the request beside it (Chrome, Firefox's race)
	}
	if !sync {
		return nil, false, nil
	}
	got, err := q.pool.Get(ctx, key)
	if err != nil {
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}
		// The dial this request waited for failed: TCP.
		return nil, false, nil
	}
	switch got.Found {
	case pool.FoundShared:
		return e.roundTripH3(key, got.Shared, req)
	case pool.FoundDial:
		c, err := e.dialH3(ctx, r, true)
		if err != nil {
			got.Dial.Fail(err)
			if ctx.Err() != nil {
				return nil, true, ctx.Err()
			}
			return nil, false, nil
		}
		return e.roundTripH3(key, got.Dial.Shared(c), req)
	}
	return nil, false, nil
}

// roundTripH3 sends req on c. A request the server did not process goes
// to another connection, and c leaves the pool; so does a replayable one
// whose connection ended before its response (the server closed it as
// the request went out), as over a reused HTTP/1.1 connection.
func (e *Engine) roundTripH3(key string, c *h3.Conn, req *http.Request) (*http.Response, bool, error) {
	resp, err := c.RoundTrip(req)
	switch {
	case errors.Is(err, h3.ErrConnUnusable):
		e.quic.pool.RemoveShared(key, c)
		return nil, true, fmt.Errorf("%w: %w", errNotSent, err)
	case errors.Is(err, h3.ErrRejected):
		// Its body was read: it goes again with a fresh copy.
		e.quic.pool.RemoveShared(key, c)
		return nil, true, fmt.Errorf("%w: %w", errRetry, err)
	case errors.Is(err, h3.ErrConnLost) && replayable(req):
		e.quic.pool.RemoveShared(key, c)
		return nil, true, fmt.Errorf("%w: %w", errRetry, err)
	}
	return resp, true, err
}

// dialH3Background dials r's origin over QUIC after delay, outside any
// request, and pools the connection for the requests after.
func (e *Engine) dialH3Background(r route, delay time.Duration) {
	q := e.quic
	q.mu.Lock()
	o := e.originState(r)
	if o.dialing {
		q.mu.Unlock()
		return
	}
	o.dialing = true
	q.mu.Unlock()
	go func() {
		defer func() {
			q.mu.Lock()
			o.dialing = false
			q.mu.Unlock()
		}()
		if delay > 0 {
			<-e.clock.After(delay)
		}
		ctx := context.Background()
		key := h3Key(r)
		got, err := q.pool.Get(ctx, key)
		if err != nil || got.Found != pool.FoundDial {
			return
		}
		c, err := e.dialH3(ctx, r, false)
		if err != nil {
			got.Dial.Fail(err)
			return
		}
		got.Dial.Shared(c)
	}()
}

// dialH3 opens a QUIC connection to r's origin's alternative and starts
// HTTP/3 on it; pending: it is for a request about to be sent. A failed
// dial marks the origin broken. Once up, the origin's HTTP/2 connection
// gets a GOAWAY where the profile does (Discovery.GoAwayTCP).
func (e *Engine) dialH3(ctx context.Context, r route, pending bool) (*h3.Conn, error) {
	q := e.quic
	key := h3Key(r)
	q.mu.Lock()
	o := e.originState(r)
	port := o.alt.port
	token := o.token
	rtt := o.rtt
	q.mu.Unlock()
	c, err := e.connectH3(ctx, r, key, port, token, rtt, pending)
	q.mu.Lock()
	defer q.mu.Unlock()
	if err != nil {
		o.brokenUntil = e.clock.Now().Add(brokenFor)
		return nil, err
	}
	o.used, o.last = true, c
	if e.profile.HTTP3.Discovery.GoAwayTCP {
		if s, ok := e.pool.Shared(r.key); ok {
			e.pool.RemoveShared(r.key, s)
			go s.GoAway()
		}
	}
	return c, nil
}

// connectH3 dials and starts HTTP/3, bounded by the dial timeout.
func (e *Engine) connectH3(ctx context.Context, r route, key string, port uint16, token []byte, rtt time.Duration, pending bool) (*h3.Conn, error) {
	pc, peer, err := e.dialer.ListenUDP(ctx, r.host, port)
	if err != nil {
		return nil, err
	}
	cfg := handshake.Config{
		ServerName: r.host,
		RootCAs:    e.rootCAs,
	}
	hello := e.plan.QUICHello()
	// Only a browser seen resuming QUIC (with 0-RTT, every time) resumes
	// it: Safari never did.
	if e.plan.Resumes() && e.profile.QUIC.ZeroRTT {
		cfg.Tickets, cfg.TicketKey = e.quic.tickets, key
		if cfg.Ticket = e.quic.tickets.Take(key); cfg.Ticket != nil {
			hello = e.plan.ResumedQUICHello()
		} else {
			rtt = 0
		}
	}
	spec, err := handshake.Spec(hello)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, e.dialer.Timeout)
	defer cancel()
	dc := quic.DialConfig{
		Profile:   e.profile.QUIC,
		Hello:     spec,
		TLS:       handshake.QUICConfig(cfg),
		CachedRTT: rtt,
		OnToken: func(t []byte) {
			e.quic.mu.Lock()
			defer e.quic.mu.Unlock()
			e.originState(r).token = t
		},
	}
	if e.profile.QUIC.Tokens {
		dc.Token = token
	}
	if cfg.Ticket != nil {
		// A v2 connection's ticket starts its resumption in v2 (Firefox).
		dc.Version = quic.TicketVersion(cfg.Ticket.Extra())
	}
	qc, err := quic.Dial(dctx, pc, peer, dc)
	if err != nil {
		return nil, err
	}
	c, err := h3.New(qc, e.profile, h3.Options{ //nolint:contextcheck // the connection serves the server's streams for its own lifetime
		AltHost: r.host,
		AltPort: strconv.Itoa(int(port)),
		Pending: pending,
	})
	if err != nil {
		return nil, err
	}
	go e.watchH3(r, qc) //nolint:contextcheck // sampled for the connection's lifetime, not the dial's
	return c, nil
}

// watchH3 keeps the connection's smoothed RTT for the origin's next
// connection (Chrome's initial_rtt), sampled until it ends.
func (e *Engine) watchH3(r route, qc *quic.Conn) {
	t := e.clock.NewTicker(time.Second)
	defer t.Stop()
	for {
		rtt := qc.SmoothedRTT()
		if rtt > 0 {
			e.quic.mu.Lock()
			e.originState(r).rtt = rtt
			e.quic.mu.Unlock()
		}
		select {
		case <-qc.Done():
			return
		case <-t.Chan():
		}
	}
}
