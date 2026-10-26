package native

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/internal/h3"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/profile"
)

// errTCPOnly is a request whose QUIC connection died with it pending,
// where the profile sends such requests on TCP (Fallback.PendingTCP): it
// goes again, on TCP only.
var errTCPOnly = fmt.Errorf("%w: its QUIC connection died", errRetry)

// errTCP is quicFor's answer for a request that goes on TCP.
var errTCP = errors.New("the request goes on TCP")

// noExclusive is the HTTP/3 pool's exclusive connection type: it has
// none.
type noExclusive struct{}

func (noExclusive) Usable() bool {
	return false
}

func (noExclusive) Close() error {
	return nil
}

// quicConn is one of the engine's HTTP/3 connections: the h3 connection,
// its QUIC connection, and what the fallback rules need to know of it.
type quicConn struct {
	*h3.Conn
	qc *quic.Conn
	// resumed: it resumed a ticket, so it may have taken requests in
	// 0-RTT before its handshake completed.
	resumed bool
	// handshake is closed once the handshake's outcome is recorded in the
	// origin's state: handshook says whether it completed before the
	// connection ended.
	handshake chan struct{}
	handshook atomic.Bool
	// inFlight counts the requests sent on it that have no response yet.
	inFlight atomic.Int32
	// died handles its death with requests pending once; toTCP is what it
	// decided (Fallback.PendingTCP).
	died  sync.Once
	toTCP bool
}

// attempt is a QUIC connection being dialled to an origin. Requests wait
// for it as the profile's Fallback says, or leave it to run in the
// background.
type attempt struct {
	// done is closed once the dial has returned: conn on success (one
	// resumed in 0-RTT is usable at once, before its handshake
	// completes), nil otherwise.
	done chan struct{}
	conn *quicConn
	// together (Fallback.ZeroRTTHolds): it follows a 0-RTT attempt that
	// gave up, beside TCP: no request waits for it.
	together bool
	// resumed: it offers a ticket.
	resumed bool
	// cancel drops the dial (Fallback.AbandonOnTCP).
	cancel context.CancelFunc
}

// dialSpec is how an attempt dials.
type dialSpec struct {
	// delay holds the dial back (Firefox's race after a GOAWAY).
	delay time.Duration
	// after, when set, holds the dial back until it is closed too: the
	// race's TCP request has its response (Firefox's race after a
	// GOAWAY).
	after <-chan struct{}
	// ticket is the session it resumes; nil for a full handshake.
	ticket *handshake.Ticket
	// pending: a request goes on the connection at once, which, in
	// 0-RTT, it sends before the control and QPACK streams where the
	// profile does (HTTP3.EarlyRequestFirst).
	pending bool
}

// origin is what the engine knows of an origin's HTTP/3, by route key.
type origin struct {
	alt    altSvc
	hasAlt bool
	// broken: Fallback.BrokenAfter attempts in a row failed. The origin's
	// requests stay on TCP for the engine's life.
	broken bool
	// failures counts the failed attempts in a row that count towards
	// BrokenAfter.
	failures int
	// used: it has had an HTTP/3 connection, so a request finding none
	// dials one, resumed with 0-RTT where the profile does, even with an
	// HTTP/2 connection open, and waits for it as Fallback says.
	used bool
	// last is its last HTTP/3 connection, to tell why it ended.
	last *quicConn
	// attempt is the QUIC connection being dialled to it; nil when none.
	attempt *attempt
	// together (Fallback.ZeroRTTHolds): its 0-RTT attempt gave up, so the
	// next attempt starts beside TCP.
	together bool
	// revalidate (FallbackBackground): when a resumed attempt failed or a
	// live connection died. Until an attempt succeeds again, requests go
	// on TCP, and one Fallback.RevalidateMillis later starts a fresh
	// attempt beside it. Zero: none.
	revalidate time.Time
	// reconnect (Fallback.PendingQUIC): no dial starts before then, after
	// a live connection died.
	reconnect time.Time
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
	pool    *pool.Pool[*quicConn, noExclusive]
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

// available reports whether o's alternative may be used now: advertised,
// unexpired and not broken. e.quic.mu must be held.
func (o *origin) available(now time.Time) bool {
	return o.hasAlt && !now.After(o.alt.expires) && !o.broken
}

// learnAltSvc records the Alt-Svc of a response to req on r, and starts
// the origin's QUIC connection at once, in the background, where the
// profile does (SwitchOnAltSvc), until the origin has used HTTP/3: after
// that a request decides (a new connection, Firefox's race after a
// GOAWAY, or its validation after a failure), not the Alt-Svc of the
// response that came with the GOAWAY.
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
	defer q.mu.Unlock()
	o := e.originState(r)
	switch {
	case cleared:
		o.hasAlt = false
	case found:
		o.alt, o.hasAlt = alt, true
	}
	if found && e.profile.HTTP3.Discovery.Switch == profile.SwitchOnAltSvc && !o.used && o.revalidate.IsZero() && e.mayDial(o, r, now) {
		e.startAttempt(o, r, dialSpec{}) //nolint:contextcheck // the attempt outlives the request
	}
}

// mayDial reports whether an attempt may start for o: its alternative is
// available and it has no connection or attempt. e.quic.mu must be held.
func (e *Engine) mayDial(o *origin, r route, now time.Time) bool {
	if !o.available(now) || o.attempt != nil {
		return false
	}
	_, ok := e.quic.pool.Shared(h3Key(r))
	return !ok
}

// tryH3 sends req over HTTP/3 if the origin's discovery and fallback rules
// say so now; handled is false when it goes on TCP instead (having maybe
// started a QUIC connection in the background, as the browser does).
// race, when not nil, is the gate of the QUIC attempt racing req's TCP
// connection: the caller closes it once req has its response on TCP, or
// failed there.
func (e *Engine) tryH3(ctx context.Context, req *http.Request, r route) (resp *http.Response, handled bool, race chan struct{}, err error) {
	if !e.h3Route(req, r) || r.tcpOnly {
		return nil, false, nil, nil
	}
	c, race, err := e.quicFor(ctx, r) //nolint:contextcheck // an attempt it starts outlives the request
	switch {
	case errors.Is(err, errTCP):
		return nil, false, race, nil
	case err != nil:
		return nil, true, nil, err
	}
	resp, handled, err = e.roundTripH3(r, c, req) //nolint:contextcheck // a dead connection's extra TCP dials outlive the request (declineExtra)
	return resp, handled, nil, err
}

// quicFor returns the QUIC connection a request on r goes on, or nil for
// TCP, as the profile's Discovery and Fallback say: the origin's pooled
// connection, or one it waits for (and how long), or none, maybe with an
// attempt started in the background; race is the gate of an attempt that
// races the request's TCP connection (tryH3).
func (e *Engine) quicFor(ctx context.Context, r route) (c *quicConn, race chan struct{}, err error) {
	if pooled, ok := e.quic.pool.Shared(h3Key(r)); ok {
		return pooled, nil, nil
	}
	d := e.profile.HTTP3.Discovery
	fb := d.Fallback
	q := e.quic
	q.mu.Lock()
	o := e.originState(r)
	now := e.clock.Now()
	if !o.available(now) {
		q.mu.Unlock()
		return nil, nil, errTCP
	}
	if wait := o.reconnect.Sub(now); wait > 0 {
		// The new connection follows the dead one's close by
		// ReconnectMillis (Safari).
		q.mu.Unlock()
		select {
		case <-e.clock.After(wait):
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		return e.quicFor(ctx, r)
	}
	_, tcpOpen := e.pool.Shared(r.key)
	var a *attempt
	hold := false
	switch {
	case d.RaceAfterGoAway && o.last != nil && o.last.GoneAway():
		// The request goes on TCP; a resumed QUIC connection follows it
		// (Firefox), which the requests after wait for (Fallback.Hold).
		// It starts RaceDelayMillis later, and never before the request
		// has its response on TCP: in every Firefox sample the TCP
		// connection is up, the request sent and Firefox's GOAWAY on it
		// (sent once its request is done) out before the QUIC
		// connection's first datagram, an order no load may change.
		o.last = nil
		if o.attempt == nil {
			race = make(chan struct{})
			e.startAttempt(o, r, dialSpec{ //nolint:contextcheck // the attempt outlives the request
				delay:  drawMillis(d.RaceDelayMillis),
				after:  race,
				ticket: e.takeTicket(r),
			})
		}
	case !o.revalidate.IsZero():
		// A resumed attempt failed or the last connection died: TCP, and
		// a fresh attempt beside it once RevalidateMillis have passed
		// (its middle: the captures bound the threshold, no more).
		if rv := fb.RevalidateMillis; rv != nil && !now.Before(o.revalidate.Add(millis((rv.Min+rv.Max)/2))) && e.mayDial(o, r, now) {
			e.dropTicket(r)
			e.startAttempt(o, r, dialSpec{}) //nolint:contextcheck // the attempt outlives the request
		}
	case fb.Mode == profile.FallbackBackground && o.used:
		// The origin's next connection resumes, and the request goes on
		// it in 0-RTT (Firefox after an idle timeout); without a ticket
		// it validates in the background.
		switch {
		case o.attempt != nil && o.attempt.resumed && fb.Hold:
			a, hold = o.attempt, true
		case o.attempt == nil:
			if t := e.takeTicket(r); t != nil {
				a, hold = e.startAttempt(o, r, dialSpec{ //nolint:contextcheck // the attempt outlives the request
					ticket:  t,
					pending: true,
				}), true
			} else {
				e.startAttempt(o, r, dialSpec{}) //nolint:contextcheck // the attempt outlives the request
			}
		}
	case fb.Mode == profile.FallbackRace && (o.used || !tcpOpen):
		// The request needs the origin's QUIC connection and waits for
		// it as Fallback says.
		if a = o.attempt; a == nil {
			a = e.startAttempt(o, r, dialSpec{ //nolint:contextcheck // the attempt outlives the request
				ticket:  e.takeTicket(r),
				pending: true,
			})
		}
	case d.Switch == profile.SwitchNextRequest && tcpOpen:
		// Chrome: QUIC starts beside the request, which goes on the open
		// HTTP/2 connection.
		if o.attempt == nil {
			e.startAttempt(o, r, dialSpec{ //nolint:contextcheck // the attempt outlives the request
				ticket: e.takeTicket(r),
			})
		}
	}
	q.mu.Unlock()
	if a == nil {
		return nil, race, errTCP
	}
	if hold {
		select {
		case <-a.done:
			if a.conn == nil {
				return nil, nil, errTCP
			}
			return a.conn, nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	c, err = e.await(ctx, a)
	return c, nil, err
}

// await waits for a, a race against TCP (FallbackRace): the attempt's
// connection if it is up before Fallback.TCPDelayMillis (none while it is
// a together attempt), else nil for TCP, even if it failed sooner. A
// connection resumed in 0-RTT is up at once, so with ZeroRTTHolds the
// request waits for its handshake there. With AbandonOnTCP the attempt
// is dropped as the request goes on TCP.
func (e *Engine) await(ctx context.Context, a *attempt) (*quicConn, error) {
	fb := e.profile.HTTP3.Discovery.Fallback
	delay := drawMillis(fb.TCPDelayMillis)
	if a.together {
		delay = 0
	}
	t := e.clock.NewTimer(delay)
	defer t.Stop()
	done := a.done
	for {
		select {
		case <-done:
			if a.conn != nil {
				return a.conn, nil
			}
			// A failed attempt: TCP once the delay has passed.
			done = nil
		case <-t.Chan():
			select {
			case <-a.done:
				if a.conn != nil {
					return a.conn, nil
				}
			default:
				if fb.AbandonOnTCP {
					a.cancel()
				}
			}
			return nil, errTCP
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// takeTicket takes r's QUIC session ticket where the profile resumes QUIC
// (with 0-RTT, every time: Safari never did), or returns nil.
func (e *Engine) takeTicket(r route) *handshake.Ticket {
	if !e.plan.Resumes() || !e.profile.QUIC.ZeroRTT {
		return nil
	}
	return e.quic.tickets.Take(h3Key(r))
}

// dropTicket forgets r's QUIC session ticket: a fresh validation follows a
// failed one (Firefox).
func (e *Engine) dropTicket(r route) {
	_ = e.quic.tickets.Take(h3Key(r))
}

// drawMillis draws a delay from r, in milliseconds; zero for nil.
func drawMillis(r *profile.Range) time.Duration {
	if r == nil {
		return 0
	}
	return millis(r.Min + rand.IntN(r.Max-r.Min+1)) //nolint:gosec // timing needs no secrecy
}

// millis is n milliseconds.
func millis(n int) time.Duration {
	return time.Duration(n) * time.Millisecond
}

// roundTripH3 sends req on c. A request the server did not process goes
// to another connection, and c leaves the pool; so does a replayable one
// whose connection ended before its response: the server closed it as
// the request went out (as over a reused HTTP/1.1 connection), its 0-RTT
// handshake gave up, or its path died, when the request goes where
// Fallback.Pending says.
func (e *Engine) roundTripH3(r route, c *quicConn, req *http.Request) (*http.Response, bool, error) {
	key := h3Key(r)
	c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
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
		if e.diedPending(r, c, req) {
			return nil, true, fmt.Errorf("%w: %w", errTCPOnly, err)
		}
		return nil, true, fmt.Errorf("%w: %w", errRetry, err)
	}
	return resp, true, err
}

// diedPending handles c's death with requests pending, once, after its
// handshake's outcome is recorded, and reports whether they go on TCP. A
// connection that died after its handshake (its idle timeout, or its
// path: QUIC.Loss.PathDegrading) sends them where Fallback.Pending says:
// to TCP, a fresh validation following RevalidateMillis later (Firefox,
// which opens a TCP connection for each and sends them all on the first),
// or to a new QUIC connection ReconnectMillis later (Safari). Any other
// end (the server's close, a 0-RTT handshake that gave up) leaves them to
// the usual rules.
func (e *Engine) diedPending(r route, c *quicConn, req *http.Request) bool {
	select {
	case <-c.qc.Done():
	default:
		return false
	}
	<-c.handshake
	err := c.qc.Err()
	if !c.handshook.Load() || !errors.Is(err, quic.ErrIdleTimeout) && !errors.Is(err, quic.ErrPathDegraded) {
		return false
	}
	c.died.Do(func() {
		fb := e.profile.HTTP3.Discovery.Fallback
		q := e.quic
		q.mu.Lock()
		o := e.originState(r)
		now := e.clock.Now()
		switch fb.Pending {
		case profile.PendingTCP:
			c.toTCP = true
			if fb.Mode == profile.FallbackBackground {
				o.revalidate = now
			}
		case profile.PendingQUIC:
			o.reconnect = now.Add(drawMillis(fb.ReconnectMillis))
		}
		q.mu.Unlock()
		if c.toTCP {
			e.declineExtra(r, req, int(c.inFlight.Load()))
		}
	})
	return c.toTCP
}

// declineExtra opens TCP connections beside the one pending requests
// share, one per request up to the profile's per-host limit, and
// declines each once its handshake is done (h2.Decline), as
// Fallback.PendingTCP says: Firefox's, when its QUIC connection idled out
// under 6 requests (h3-broken: 6 TCP connections in 3 ms, all requests on
// one, GOAWAY on the others).
func (e *Engine) declineExtra(r route, req *http.Request, pending int) {
	n := min(pending, e.profile.Connections.HTTP1PerHost) - 1
	for range n {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), e.dialer.Timeout)
			defer cancel()
			conn, proto, err := e.dial(ctx, req, r)
			if err != nil {
				return
			}
			if proto != "h2" {
				_ = conn.Close()
				return
			}
			_ = h2.Decline(conn, e.profile)
		}()
	}
}

// startAttempt starts dialling r's origin as spec says, outside any
// request; the connection goes in the pool for the requests after.
// e.quic.mu must be held.
func (e *Engine) startAttempt(o *origin, r route, spec dialSpec) *attempt {
	ctx, cancel := context.WithCancel(context.Background())
	a := &attempt{
		done:     make(chan struct{}),
		together: o.together,
		resumed:  spec.ticket != nil,
		cancel:   cancel,
	}
	o.together = false
	o.attempt = a
	go e.runAttempt(ctx, o, r, a, spec) //nolint:contextcheck // the attempt outlives the request that started it
	return a
}

// runAttempt is a's dial, which ends it: the connection is pooled, or
// the failure counts as Fallback says.
func (e *Engine) runAttempt(ctx context.Context, o *origin, r route, a *attempt, spec dialSpec) {
	defer a.cancel()
	q := e.quic
	if spec.delay > 0 {
		select {
		case <-e.clock.After(spec.delay):
		case <-ctx.Done():
		}
	}
	if spec.after != nil {
		select {
		case <-spec.after:
		case <-ctx.Done():
		}
	}
	c, err := e.connectH3(ctx, r, spec)
	ours := err == nil
	if ours {
		// Pooled before the attempt ends, so that no request starts
		// another meanwhile. Another connection pooled first (none
		// should be) is the one the waiting requests take.
		got, gerr := q.pool.Get(context.Background(), h3Key(r)) //nolint:contextcheck // pooling waits for nothing; the attempt has no request
		switch {
		case gerr != nil:
		case got.Found == pool.FoundDial:
			if pooled := got.Dial.Shared(c); pooled != c {
				c, ours = pooled, false
			}
		case got.Found == pool.FoundShared:
			_ = c.Close()
			c, ours = got.Shared, false
		}
	}
	q.mu.Lock()
	if o.attempt == a {
		o.attempt = nil
	}
	if err != nil {
		e.failed(o, a.resumed)
		q.mu.Unlock()
		close(a.done)
		return
	}
	a.conn = c
	if !ours {
		q.mu.Unlock()
		close(a.done)
		return
	}
	o.last = c
	if !c.resumed {
		e.succeeded(o)
	}
	if e.profile.HTTP3.Discovery.GoAwayTCP {
		if s, ok := e.pool.Shared(r.key); ok {
			e.goAwayTCP(r, s)
		}
	}
	q.mu.Unlock()
	if c.resumed {
		go e.watchHandshake(o, c) //nolint:gosec,contextcheck // watched for the connection's lifetime, not the attempt's
	}
	close(a.done)
}

// goAwayIfReplaced sends s, the HTTP/2 connection req just opened on r
// and pooled, GOAWAY after req if the origin's QUIC connection came up
// while it was being established (goaway_tcp). Firefox replaces every
// HTTP/2 connection to an origin with a live HTTP/3 one, whenever it was
// established: runAttempt sends GOAWAY on the one pooled when QUIC comes
// up, this on one pooled after (a request that went on TCP beside the
// attempt, its dial slower than the attempt). Each decides under
// e.quic.mu after pooling its own connection, so one of them sees the
// other's. Only for a request that may use HTTP/3: one whose kind the
// profile never measured there keeps its connection.
func (e *Engine) goAwayIfReplaced(req *http.Request, r route, s *shared) {
	if !e.h3Route(req, r) || !e.profile.HTTP3.Discovery.GoAwayTCP {
		return
	}
	q := e.quic
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.pool.Shared(h3Key(r)); ok {
		e.goAwayTCP(r, s)
	}
}

// goAwayTCP takes s, an HTTP/2 connection of r's, out of the pool and
// sends it GOAWAY once the request that opened it has its response (one
// in flight is never refused), as Firefox does to the origin's HTTP/2
// connections its HTTP/3 one replaces. e.quic.mu must be held.
func (e *Engine) goAwayTCP(r route, s *shared) {
	e.pool.RemoveShared(r.key, s)
	go func() {
		<-s.sent
		s.GoAway()
	}()
}

// watchHandshake records the outcome of c's handshake, resumed in 0-RTT,
// once it completes or c ends first.
func (e *Engine) watchHandshake(o *origin, c *quicConn) {
	err := c.qc.WaitHandshake(context.Background())
	e.quic.mu.Lock()
	if err == nil {
		c.handshook.Store(true)
		e.succeeded(o)
	} else {
		e.failed(o, true)
	}
	e.quic.mu.Unlock()
	close(c.handshake)
}

// succeeded records an attempt whose handshake completed. e.quic.mu must
// be held.
func (e *Engine) succeeded(o *origin) {
	o.used, o.failures, o.revalidate = true, 0, time.Time{}
}

// failed records an attempt that failed (resumed: it offered a ticket):
// it counts towards Fallback.BrokenAfter unless it resumed and the
// profile counts only fresh ones; after a failed resumed one, Chrome's
// next attempt starts beside TCP (ZeroRTTHolds) and Firefox validates
// afresh later (RevalidateMillis). e.quic.mu must be held.
func (e *Engine) failed(o *origin, resumed bool) {
	fb := e.profile.HTTP3.Discovery.Fallback
	if resumed && !fb.BrokenCountsResumed {
		switch {
		case fb.ZeroRTTHolds:
			o.together = true
		case fb.Mode == profile.FallbackBackground:
			o.revalidate = e.clock.Now()
		}
		return
	}
	o.failures++
	if o.failures >= fb.BrokenAfter {
		o.broken = true
	}
}

// connectH3 dials r's origin's alternative as spec says and starts
// HTTP/3 on it, bounded by the dial timeout. A Version Negotiation packet
// that lists a version the profile retries in (Firefox: v2) starts a
// second connection in it at once.
func (e *Engine) connectH3(ctx context.Context, r route, spec dialSpec) (*quicConn, error) {
	q := e.quic
	key := h3Key(r)
	q.mu.Lock()
	o := e.originState(r)
	port := o.alt.port
	token := o.token
	rtt := o.rtt
	q.mu.Unlock()
	dctx, cancel := context.WithTimeout(ctx, e.dialer.Timeout)
	defer cancel()
	cfg := handshake.Config{
		ServerName: r.host,
		RootCAs:    e.rootCAs,
	}
	hello := e.plan.QUICHello()
	if e.plan.Resumes() && e.profile.QUIC.ZeroRTT {
		cfg.Tickets, cfg.TicketKey = q.tickets, key
		if cfg.Ticket = spec.ticket; cfg.Ticket != nil {
			hello = e.plan.ResumedQUICHello()
		} else {
			rtt = 0
		}
	}
	dc := quic.DialConfig{
		Profile:   e.profile.QUIC,
		CachedRTT: rtt,
		OnToken: func(t []byte) {
			q.mu.Lock()
			defer q.mu.Unlock()
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
	qc, err := e.dialQUIC(dctx, r, port, hello, cfg, dc)
	from := dc.Version
	if from == 0 {
		from = 1
	}
	if v, ok := quic.RetryVersion(err, e.profile.QUIC, from); ok {
		// Firefox: a new connection in v at once, with its retry hello
		// (tls.quic.version_negotiation), offering no ticket.
		if retry, ok := e.plan.RetryQUICHello(); ok {
			cfg.Ticket = nil
			dc.Version, dc.AfterVersionNegotiation, dc.Token, dc.CachedRTT = v, true, nil, 0
			qc, err = e.dialQUIC(dctx, r, port, retry, cfg, dc)
		}
	}
	if err != nil {
		return nil, err
	}
	c, err := h3.New(qc, e.profile, h3.Options{ //nolint:contextcheck // the connection serves the server's streams for its own lifetime
		AltHost: r.host,
		AltPort: strconv.Itoa(int(port)),
		Pending: spec.pending,
	})
	if err != nil {
		return nil, err
	}
	qcn := &quicConn{
		Conn:      c,
		qc:        qc,
		resumed:   cfg.Ticket != nil,
		handshake: make(chan struct{}),
	}
	if !qcn.resumed {
		qcn.handshook.Store(true)
		close(qcn.handshake)
	}
	go e.watchH3(r, qc) //nolint:contextcheck // sampled for the connection's lifetime, not the dial's
	return qcn, nil
}

// dialQUIC opens a UDP socket to r's origin's alternative at port and
// dials QUIC on it with hello, a fresh spec of it per connection.
func (e *Engine) dialQUIC(ctx context.Context, r route, port uint16, hello fingerprint.Hello, cfg handshake.Config, dc quic.DialConfig) (*quic.Conn, error) {
	pc, peer, err := e.dialer.ListenUDP(ctx, r.host, port)
	if err != nil {
		return nil, err
	}
	spec, err := handshake.Spec(hello)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	dc.Hello = spec
	dc.TLS = handshake.QUICConfig(cfg)
	dc.NewPath = func(ctx context.Context) (net.PacketConn, error) {
		// The same peer from a new local port (Chrome's migration).
		pc, _, err := e.dialer.ListenUDP(ctx, peer.Addr().String(), peer.Port())
		return pc, err
	}
	return quic.Dial(ctx, pc, peer, dc)
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
