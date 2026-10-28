package native

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roachadam/mimicry/internal/altsvc"
	"github.com/roachadam/mimicry/internal/dial"
	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/internal/h3"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/profile"
)

// errTCPOnly is a request that goes again, on TCP only: its QUIC
// connection died with it pending, where the profile sends such requests
// on TCP (Fallback.PendingTCP), or its 0-RTT connection is overdue
// (errEarlyOverdue).
var errTCPOnly = fmt.Errorf("%w: it goes on TCP", errRetry)

// errEarlyOverdue is a request whose resumed connection's server has said
// nothing for as long as fast fallback lets it wait (Engine.earlyWait).
var errEarlyOverdue = fmt.Errorf("%w: the server has not answered its 0-RTT connection", errTCPOnly)

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
	// earlyUntil, with fast fallback, ends the wait of requests on it,
	// resumed, for a handshake the server has not answered
	// (Engine.earlyWait). Zero: no end but the connection's.
	earlyUntil time.Time
	// tls is the connection's TLS state once its handshake is complete,
	// which responses carry (state).
	tls atomic.Pointer[tls.ConnectionState]
}

// state is c's TLS state, kept once the handshake is complete.
func (c *quicConn) state() *tls.ConnectionState {
	if s := c.tls.Load(); s != nil {
		return s
	}

	s := handshake.State(c.qc.ConnectionState())
	if s.HandshakeComplete {
		c.tls.Store(s)
	}

	return s
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
	// port is the alternative's: the origin's host at port.
	port uint16
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
// Its alternative is in the engine's altsvc.Cache, under the same key.
type origin struct {
	// known: the engine has seen the origin's alternative, in an Alt-Svc
	// response or, from a shared or loaded cache, at a request.
	known bool
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
	// revalidate (FallbackBackground): after a failed resumed attempt or a dead
	// connection, requests go on TCP and a fresh attempt starts RevalidateMillis
	// later. Zero: none.
	revalidate time.Time
	// reconnect (Fallback.PendingQUIC): no dial starts before then, after
	// a live connection died.
	reconnect time.Time
	// token is the server's last NEW_TOKEN; rtt the last connection's
	// smoothed RTT.
	token []byte
	rtt   time.Duration
}

// quicState is the engine's HTTP/3 state: per-origin history, their
// alternatives (alts, the engine's own or one shared with others), the
// QUIC pool (route key plus " h3") and session tickets. Safe for
// concurrent use.
type quicState struct {
	mu      sync.Mutex
	origins map[string]*origin
	alts    *altsvc.Cache
	pool    *pool.Pool[*quicConn, noExclusive]
	tickets *handshake.Tickets
}

// h3Key is r's HTTP/3 pool key.
func h3Key(r route) string {
	return r.key + " h3"
}

// h3Route reports whether req on r may use HTTP/3: the engine speaks it
// for the profile and h3Refusal has nothing against it.
func (e *Engine) h3Route(req *http.Request, r route) bool {
	return e.quic != nil && e.h3Refusal(req, r) == nil
}

// h3Refusal says why req on r never uses HTTP/3, or is nil: it must be
// https; direct (browsers never use HTTP/3 through a proxy) or, with
// HTTP3OverSOCKS5, through a SOCKS5 proxy that has not refused a UDP
// association; and the profile must have measured its kind over HTTP/3.
func (e *Engine) h3Refusal(req *http.Request, r route) error {
	switch {
	case req.URL.Scheme != "https":
		return errors.New("mimicry: plain http:// never goes over HTTP/3")
	case r.proxy != nil && !e.h3SOCKS5:
		return errors.New("mimicry: requests through a proxy never go over HTTP/3 (Config.HTTP3OverSOCKS5 allows SOCKS5 proxies)")
	case r.proxy != nil && r.proxy.Scheme == dial.SchemeHTTP:
		return errors.New("mimicry: requests through an HTTP proxy never go over HTTP/3")
	case r.proxy != nil && e.dialer.UDPRefused(r.proxy):
		return errors.New("mimicry: the SOCKS5 proxy refused a UDP association: its requests stay on TCP")
	case !h3.Measured(e.profile, headerorder.PlanFrom(req.Context())):
		return fmt.Errorf("%w: profile %s has no HTTP/3 list for this request, which goes on TCP", headerorder.ErrNotMeasured, e.profile.Name)
	}

	return nil
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

// alternative returns the port of r's HTTP/3 alternative if it may be used
// now: advertised, unexpired and, with o its origin state (nil: none yet),
// not broken. e.quic.mu must be held.
func (e *Engine) alternative(o *origin, r route) (uint16, bool) {
	if o != nil && o.broken {
		return 0, false
	}

	return e.quic.alts.Get(r.alt)
}

// learnAltSvc records resp's Alt-Svc and, where the profile switches on
// Alt-Svc, starts the origin's QUIC connection in the background. Once the
// origin has used HTTP/3, requests decide instead.
func (e *Engine) learnAltSvc(req *http.Request, r route, resp *http.Response) {
	if !e.h3Route(req, r) {
		return
	}

	values := resp.Header.Values("Alt-Svc")
	if len(values) == 0 {
		return
	}

	q := e.quic
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.alts.Learn(r.alt, r.host, values) {
		o := e.originState(r)

		o.known = true
		e.startOnAltSvc(o, r)
	}
}

// startOnAltSvc starts o's QUIC connection in the background where the
// profile switches on Alt-Svc (Firefox's validation), unless o has used
// HTTP/3 or waits to revalidate. e.quic.mu must be held.
func (e *Engine) startOnAltSvc(o *origin, r route) {
	if e.profile.HTTP3.Discovery.Switch != profile.SwitchOnAltSvc || o.used || !o.revalidate.IsZero() {
		return
	}

	if port, ok := e.mayDial(o, r); ok {
		e.startAttempt(o, r, dialSpec{
			port: port,
		})
	}
}

// mayDial returns the port of o's alternative if an attempt may start for
// it: the alternative is available and o has no connection or attempt.
// e.quic.mu must be held.
func (e *Engine) mayDial(o *origin, r route) (uint16, bool) {
	port, ok := e.alternative(o, r)
	if !ok || o.attempt != nil {
		return 0, false
	}

	if _, ok := e.quic.pool.Shared(h3Key(r)); ok {
		return 0, false
	}

	return port, true
}

// tryH3 sends req over HTTP/3 if discovery and fallback say so now; handled
// is false when it goes on TCP. race, when not nil, gates the QUIC attempt
// racing req's TCP connection: the caller closes it once req is done on TCP.
func (e *Engine) tryH3(ctx context.Context, req *http.Request, r route) (resp *http.Response, handled bool, race chan struct{}, err error) {
	if !e.h3Route(req, r) || r.tcpOnly {
		return nil, false, nil, nil
	}

	c, race, err := e.quicFor(ctx, r)
	switch {
	case errors.Is(err, errTCP):
		return nil, false, race, nil
	case err != nil:
		return nil, true, nil, err
	}

	resp, handled, err = e.roundTripH3(r, c, req)

	return resp, handled, nil, err
}

// way is how quicFor sends a request to an origin whose alternative is
// available and that has no QUIC connection up.
type way int

const (
	// wayTCP: on TCP, nothing started.
	wayTCP way = iota
	// wayGoAwayRace: on TCP after the server's GOAWAY; a resumed QUIC
	// connection follows its response (RaceAfterGoAway).
	wayGoAwayRace
	// wayRevalidate: on TCP after a failure; a fresh attempt beside it
	// once RevalidateMillis have passed.
	wayRevalidate
	// wayResume: the origin used HTTP/3 before (FallbackBackground): a
	// resumed attempt holds the request, without a ticket it goes on TCP
	// and an attempt validates in the background.
	wayResume
	// wayRace: the request waits for the origin's attempt as Fallback
	// says (FallbackRace).
	wayRace
	// wayBeside: on the open TCP connection, an attempt starting beside
	// it (SwitchNextRequest).
	wayBeside
)

// pickWay picks how a request to o goes, tcpOpen when its route has an
// HTTP/2 connection that can take it, as the profile's Discovery and
// Fallback say. It changes nothing. e.quic.mu must be held.
func (e *Engine) pickWay(o *origin, tcpOpen bool) way {
	d := e.profile.HTTP3.Discovery
	fb := d.Fallback

	switch {
	case d.RaceAfterGoAway && o.last != nil && o.last.GoneAway():
		return wayGoAwayRace
	case !o.revalidate.IsZero():
		return wayRevalidate
	case fb.Mode == profile.FallbackBackground && o.used:
		return wayResume
	case fb.Mode == profile.FallbackRace && (o.used || !tcpOpen):
		return wayRace
	case d.Switch == profile.SwitchNextRequest && tcpOpen:
		return wayBeside
	}

	return wayTCP
}

// quicFor returns the QUIC connection a request on r goes on, or nil for
// TCP, as pickWay decides; race is as in tryH3.
func (e *Engine) quicFor(ctx context.Context, r route) (c *quicConn, race chan struct{}, err error) {
	if pooled, ok := e.quic.pool.Shared(h3Key(r)); ok {
		if e.overdue(pooled) {
			// Fast fallback: its server has said nothing for longer than a
			// request waits on it.
			return nil, nil, errTCP
		}

		return pooled, nil, nil
	}

	d := e.profile.HTTP3.Discovery
	fb := d.Fallback
	q := e.quic
	q.mu.Lock()

	o := e.originState(r)
	now := e.clock.Now()

	port, ok := e.alternative(o, r)
	if !ok {
		q.mu.Unlock()

		return nil, nil, errTCP
	}

	if !o.known {
		// The alternative came from a shared or loaded cache: the engine
		// learns it now, as from an Alt-Svc response.
		o.known = true
		e.startOnAltSvc(o, r)
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

	switch e.pickWay(o, tcpOpen) {
	case wayGoAwayRace:
		// On TCP; a resumed QUIC connection follows RaceDelayMillis later, never
		// before the TCP response (the order in every Firefox sample), and later
		// requests wait for it (Fallback.Hold).
		o.last = nil
		if o.attempt == nil {
			race = make(chan struct{})
			e.startAttempt(o, r, dialSpec{
				port:   port,
				delay:  drawMillis(d.RaceDelayMillis),
				after:  race,
				ticket: e.takeTicket(r),
			})
		}

	case wayRevalidate:
		// A resumed attempt failed or the last connection died: TCP, and
		// a fresh attempt beside it once RevalidateMillis have passed
		// (its middle: the captures bound the threshold, no more).
		if rv := fb.RevalidateMillis; rv != nil && !now.Before(o.revalidate.Add(millis((rv.Min+rv.Max)/2))) {
			if port, ok := e.mayDial(o, r); ok {
				e.dropTicket(r)
				e.startAttempt(o, r, dialSpec{
					port: port,
				})
			}
		}

	case wayResume:
		// The origin's next connection resumes, and the request goes on
		// it in 0-RTT (Firefox after an idle timeout); without a ticket
		// it validates in the background.
		switch {
		case o.attempt != nil && o.attempt.resumed && fb.Hold:
			a, hold = o.attempt, true
		case o.attempt == nil:
			if t := e.takeTicket(r); t != nil {
				a, hold = e.startAttempt(o, r, dialSpec{
					port:    port,
					ticket:  t,
					pending: true,
				}), true
			} else {
				e.startAttempt(o, r, dialSpec{
					port: port,
				})
			}
		}

	case wayRace:
		// The request needs the origin's QUIC connection and waits for
		// it as Fallback says.
		if a = o.attempt; a == nil {
			a = e.startAttempt(o, r, dialSpec{
				port:    port,
				ticket:  e.takeTicket(r),
				pending: true,
			})
		}

	case wayBeside:
		// Chrome: QUIC starts beside the request, which goes on the open
		// HTTP/2 connection.
		if o.attempt == nil {
			e.startAttempt(o, r, dialSpec{
				port:   port,
				ticket: e.takeTicket(r),
			})
		}

	case wayTCP:
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

// await waits for a racing attempt (FallbackRace) up to TCPDelayMillis,
// then returns nil for TCP; a failed attempt holds the request no longer
// with fast fallback, until the delay with browser timing. With
// ZeroRTTHolds a 0-RTT request waits for the handshake; with AbandonOnTCP
// the attempt is dropped.
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

			if !e.browserFallback {
				return nil, errTCP
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
// (resumesQUIC), or returns nil.
func (e *Engine) takeTicket(r route) *handshake.Ticket {
	if !e.resumesQUIC() {
		return nil
	}

	return e.quic.tickets.Take(h3Key(r))
}

// hasTicket reports whether takeTicket would return a ticket for r,
// taking none.
func (e *Engine) hasTicket(r route) bool {
	return e.resumesQUIC() && e.quic.tickets.Has(h3Key(r))
}

// resumesQUIC reports whether the profile resumes QUIC connections: with
// 0-RTT, every time (Safari never did).
func (e *Engine) resumesQUIC() bool {
	return e.plan.Resumes() && e.profile.QUIC.ZeroRTT
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

// earlyLimit is how long fast fallback lets requests wait on a resumed
// connection for its server's first answer: the profile's race delay
// (Chrome's, which ZeroRTTHolds otherwise stretches to the give-up), else
// its first probe timeout (Firefox: no race delay; it probes then). Zero:
// no limit.
func (e *Engine) earlyLimit() time.Duration {
	if d := drawMillis(e.profile.HTTP3.Discovery.Fallback.TCPDelayMillis); d > 0 {
		return d
	}

	return millis(e.profile.QUIC.Loss.InitialPTOMillis)
}

// earlyWait is how much longer a request may wait on c for a handshake its
// server has not answered: c resumed in 0-RTT with fast fallback, until
// c.earlyUntil. limited is false where the request waits as the browser's
// does: browser timing, or c's handshake is complete.
func (e *Engine) earlyWait(c *quicConn) (wait time.Duration, limited bool) {
	if c.earlyUntil.IsZero() || c.handshook.Load() {
		return 0, false
	}

	return c.earlyUntil.Sub(e.clock.Now()), true
}

// overdue reports whether requests wait on c no longer (earlyWait).
func (e *Engine) overdue(c *quicConn) bool {
	wait, limited := e.earlyWait(c)

	return limited && wait <= 0
}

// roundTripH3 sends req on c. A request the server did not process, or a
// replayable one whose connection ended before its response, goes to another
// connection and c leaves the pool.
func (e *Engine) roundTripH3(r route, c *quicConn, req *http.Request) (*http.Response, bool, error) {
	wait, limited := e.earlyWait(c)
	switch {
	case limited && wait <= 0:
		return nil, true, errEarlyOverdue
	case limited:
		return e.roundTripEarly(r, c, req, wait)
	}

	c.inFlight.Add(1)

	defer c.inFlight.Add(-1)

	resp, err := c.RoundTrip(req)

	return e.h3Result(r, c, req, resp, err)
}

// roundTripEarly is roundTripH3 on c, resumed and unanswered, with fast
// fallback: when neither the response nor the handshake has come within
// wait, req goes on TCP. A request sent in 0-RTT stays on c, as the
// browser's would until c gives up, its answer dropped; one held for the
// handshake (Conn.EarlyMethod) is withdrawn unsent.
func (e *Engine) roundTripEarly(r route, c *quicConn, req *http.Request, wait time.Duration) (*http.Response, bool, error) {
	type result struct {
		resp *http.Response
		err  error
	}

	// The copy on c outlives the caller's context once it is left there;
	// until then the caller's cancellation reaches it.
	ctx := req.Context()
	qctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, cancel)
	done := make(chan result, 1)

	c.inFlight.Add(1)

	go func() {
		resp, err := c.RoundTrip(req.WithContext(qctx)) //nolint:bodyclose // closed by roundTripEarly's caller, or below once the request has gone on TCP
		done <- result{
			resp: resp,
			err:  err,
		}
	}()

	t := e.clock.NewTimer(wait)
	defer t.Stop()

	handshake := c.handshake
	for {
		select {
		case res := <-done:
			c.inFlight.Add(-1)

			return e.h3Result(r, c, req, res.resp, res.err)

		case <-handshake:
			// Answered, or given up: the request waits for its outcome
			// on c, as any other.
			handshake = nil

			t.Stop()

		case <-t.Chan():
			stop()

			if !c.EarlyMethod(req.Method) {
				cancel()
			}

			go func() {
				res := <-done

				c.inFlight.Add(-1)
				cancel()

				if res.resp != nil {
					_ = res.resp.Body.Close()
				}

				if errors.Is(res.err, h3.ErrConnLost) || errors.Is(res.err, h3.ErrConnUnusable) {
					e.quic.pool.RemoveShared(h3Key(r), c)
				}
			}()

			return nil, true, errEarlyOverdue
		}
	}
}

// h3Result is roundTripH3's outcome of req's exchange on c.
func (e *Engine) h3Result(r route, c *quicConn, req *http.Request, resp *http.Response, err error) (*http.Response, bool, error) {
	key := h3Key(r)

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
			return nil, true, fmt.Errorf("%w: its QUIC connection died: %w", errTCPOnly, err)
		}

		return nil, true, fmt.Errorf("%w: %w", errRetry, err)
	}

	if err == nil {
		resp.TLS = c.state()
	}

	return resp, true, err
}

// diedPending handles c's death with requests pending, once, and reports
// whether they go on TCP. Only a death after the handshake (idle timeout or
// path degrading) follows Fallback.Pending; other ends use the usual rules.
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

// declineExtra opens and declines (h2.Decline) one extra TCP connection per
// pending request up to the per-host limit, as Fallback.PendingTCP says
// (Firefox, measured in the h3-broken capture).
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

	go e.runAttempt(ctx, o, r, a, spec)

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
		got, gerr := q.pool.Get(context.Background(), h3Key(r))
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
		go e.watchHandshake(o, r, c) //nolint:gosec // watched for the connection's lifetime, not the attempt's
	}

	close(a.done)
}

// goAwayIfReplaced sends GOAWAY on s, just pooled by req, if the origin's
// QUIC connection came up while s was dialling (goawayTcp). runAttempt
// covers connections pooled before; both decide under e.quic.mu, so one
// sees the other. Requests of a kind unmeasured over HTTP/3 keep s, and
// so do those that left an overdue 0-RTT connection (earlyWait): until
// its handshake completes it is not up (watchHandshake).
func (e *Engine) goAwayIfReplaced(req *http.Request, r route, s *shared) {
	if !e.h3Route(req, r) || !e.profile.HTTP3.Discovery.GoAwayTCP {
		return
	}

	q := e.quic
	q.mu.Lock()
	defer q.mu.Unlock()

	if c, ok := q.pool.Shared(h3Key(r)); ok && !e.overdue(c) {
		e.goAwayTCP(r, s)
	}
}

// goAwayTCP unpools s and sends GOAWAY once its opening request has its
// response. e.quic.mu must be held.
func (e *Engine) goAwayTCP(r route, s *shared) {
	e.pool.RemoveShared(r.key, s)

	go func() {
		<-s.sent
		s.GoAway()
	}()
}

// watchHandshake records the outcome of c's handshake, resumed in 0-RTT,
// once it completes or c ends first. One completing after its requests
// went on TCP (earlyWait) sends GOAWAY on the HTTP/2 connection they
// opened, as on any once QUIC is up (goawayTcp).
func (e *Engine) watchHandshake(o *origin, r route, c *quicConn) {
	err := c.qc.WaitHandshake(context.Background())

	e.quic.mu.Lock()

	if err == nil {
		overdue := e.overdue(c)

		c.handshook.Store(true)
		e.succeeded(o)

		if s, ok := e.pool.Shared(r.key); ok && overdue && e.profile.HTTP3.Discovery.GoAwayTCP {
			e.goAwayTCP(r, s)
		}
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

// failed records a failed attempt toward Fallback.BrokenAfter (resumed
// ones only if counted) and sets up the next attempt (ZeroRTTHolds,
// RevalidateMillis). e.quic.mu must be held.
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

// connectH3 dials r's alternative per spec and starts HTTP/3 on it. A
// Version Negotiation listing a version the profile retries in starts a
// second connection at once.
func (e *Engine) connectH3(ctx context.Context, r route, spec dialSpec) (*quicConn, error) {
	q := e.quic
	key := h3Key(r)

	q.mu.Lock()

	o := e.originState(r)
	port := spec.port
	token := o.token
	rtt := o.rtt
	q.mu.Unlock()

	start := e.clock.Now()
	dctx, cancel := context.WithTimeout(ctx, e.dialer.Timeout)

	defer cancel()

	cfg := handshake.Config{
		ServerName:       r.host,
		RootCAs:          e.rootCAs,
		VerifyConnection: e.verifyFor(r.host),
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
		// (tls.quic.versionNegotiation), offering no ticket.
		if retry, ok := e.plan.RetryQUICHello(); ok {
			cfg.Ticket = nil
			dc.Version, dc.AfterVersionNegotiation, dc.Token, dc.CachedRTT = v, true, nil, 0
			qc, err = e.dialQUIC(dctx, r, port, retry, cfg, dc)
		}
	}

	if err != nil {
		return nil, err
	}

	c, err := h3.New(qc, e.profile, h3.Options{
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
	switch {
	case !qcn.resumed:
		qcn.handshook.Store(true)
		close(qcn.handshake)

	case !e.browserFallback:
		if d := e.earlyLimit(); d > 0 {
			qcn.earlyUntil = start.Add(d)
		}
	}

	go e.watchH3(r, qc)

	return qcn, nil
}

// dialQUIC opens a UDP socket to r's origin's alternative at port and
// dials QUIC on it with hello, a fresh spec of it per connection. Through
// a proxy (a SOCKS5 one: h3Refusal) the socket is a UDP association with
// it, a new one for each socket: no datagram goes to the origin directly.
func (e *Engine) dialQUIC(ctx context.Context, r route, port uint16, hello fingerprint.Hello, cfg handshake.Config, dc quic.DialConfig) (*quic.Conn, error) {
	listen := func(ctx context.Context, host string, port uint16) (net.PacketConn, netip.AddrPort, error) {
		if r.proxy != nil {
			// Always the host, for the proxy to resolve: peer is a
			// stand-in when the host is a name.
			return e.dialer.ListenUDPVia(ctx, r.proxy, r.host, port)
		}

		return e.dialer.ListenUDP(ctx, host, port)
	}

	pc, peer, err := listen(ctx, r.host, port)
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
		pc, _, err := listen(ctx, peer.Addr().String(), peer.Port())
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
