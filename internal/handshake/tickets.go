package handshake

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	utls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/profile"
)

// Store keeps session tickets by key (host, port, partition, proxy; QUIC
// apart) for the Tickets views given it: one engine's, or every engine
// sharing a store (Config.Tickets). It belongs to one browser once Bind or
// Import names it. Safe for concurrent use.
type Store struct {
	mu sync.Mutex
	m  map[string]ticket
	// sweepAt is the store size at which keep next drops expired tickets.
	sweepAt int
	// browser is the browser (profile.Spec.Browser) the store's tickets
	// belong to; empty until Bind or Import names one.
	browser string
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		m:       map[string]ticket{},
		sweepAt: minSweep,
	}
}

// Bind makes s browser's, unless it already belongs to another: a ticket
// links the connections that use it, so two browsers sharing a store
// would show as one identity.
func (s *Store) Bind(browser string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.bind(browser)
}

// bind is Bind with s.mu held. An empty browser binds nothing.
func (s *Store) bind(browser string) error {
	switch {
	case browser == "" || s.browser == browser:
		return nil
	case s.browser != "":
		return fmt.Errorf("the tickets belong to %s, not %s: one identity's tickets are one browser's", s.browser, browser)
	}

	s.browser = browser

	return nil
}

// Browser is the browser s belongs to, or "".
func (s *Store) Browser() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.browser
}

// Tickets is a view of a Store that uses its tickets as the browser does:
// a TLS 1.3 ticket once, TLS 1.2 tickets by the profile's tls12 policy. A
// ticket uTLS would decline (expired lifetime or certificate) is never
// handed out, so a hello never looks resumed without resuming. Safe for
// concurrent use. See decision 0013.
type Tickets struct {
	s *Store
	// tls12 is profile.Resumption.TLS12; empty keeps no TLS 1.2 tickets.
	tls12 string
	clock clockwork.Clock
}

// ticket is a stored session and when it stops being usable.
type ticket struct {
	state *utls.ClientSessionState
	tls13 bool
	// expires is the TLS 1.3 ticket's lifetime end or the server
	// certificate's expiry, whichever comes first; zero if unknown.
	expires time.Time
}

// minSweep is the store size below which keep does not sweep.
const minSweep = 64

// NewTickets returns a view of a new store using TLS 1.2 tickets as tls12
// (profile.TLS12Reuse, profile.TLS12SingleUse, or "" for not at all), with
// clock telling expired tickets.
func NewTickets(tls12 string, clock clockwork.Clock) *Tickets {
	return NewStore().Tickets(tls12, clock)
}

// Tickets returns a view of s using TLS 1.2 tickets as tls12, with clock
// telling expired tickets.
func (s *Store) Tickets(tls12 string, clock clockwork.Clock) *Tickets {
	return &Tickets{
		s:     s,
		tls12: tls12,
		clock: clock,
	}
}

// Ticket is a session a connection resumes, taken from a Tickets.
type Ticket struct {
	state *utls.ClientSessionState
	tls13 bool
}

// TLS13 reports a TLS 1.3 ticket, which the ClientHello offers in
// pre_shared_key (fingerprint.Plan.ResumedHello); a TLS 1.2 one goes in
// session_ticket of the full ClientHello.
func (t *Ticket) TLS13() bool {
	return t.tls13
}

// Extra is the session's SessionState.Extra: what the connection that
// received the ticket stored with it (the QUIC version and transport
// parameters, quic.TicketVersion).
func (t *Ticket) Extra() [][]byte {
	_, s, err := t.state.ResumptionState()
	if err != nil || s == nil {
		return nil
	}

	return s.Extra
}

// EarlyData reports whether the ticket allows 0-RTT: its
// max_early_data_size is above 0. A browser offers early_data only then
// (fingerprint.Plan.ResumedQUICHello).
func (t *Ticket) EarlyData() bool {
	_, s, err := t.state.ResumptionState()
	if err != nil || s == nil {
		return false
	}

	return s.EarlyData
}

// Take returns the ticket a new connection for key offers, or nil. TLS 1.3
// tickets, and TLS 1.2 ones under single use, leave the store: concurrent
// connections then resume one at a time. Expired tickets are dropped. A
// view keeping no TLS 1.2 tickets (QUIC's) never hands one out.
func (t *Tickets) Take(key string) *Ticket {
	s := t.s

	s.mu.Lock()
	defer s.mu.Unlock()

	tk, ok := s.m[key]
	if !ok {
		return nil
	}

	if tk.expired(t.clock.Now()) {
		delete(s.m, key)

		return nil
	}

	if !tk.tls13 && t.tls12 == "" {
		return nil
	}

	if tk.tls13 || t.tls12 != profile.TLS12Reuse {
		delete(s.m, key)
	}

	return &Ticket{
		state: tk.state,
		tls13: tk.tls13,
	}
}

// Has reports whether Take would return a ticket for key, without taking
// it or dropping an expired one.
func (t *Tickets) Has(key string) bool {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()

	tk, ok := t.s.m[key]

	return ok && !tk.expired(t.clock.Now()) && (tk.tls13 || t.tls12 != "")
}

// Drop forgets key's ticket, if any.
func (t *Tickets) Drop(key string) {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()

	delete(t.s.m, key)
}

// expired reports whether uTLS would decline the ticket at now.
func (s ticket) expired(now time.Time) bool {
	return !s.expires.IsZero() && now.After(s.expires)
}

// keep stores a ticket the server sent on a connection that resumed with
// used (nil: a full handshake) and whose certificate expires at notAfter.
func (t *Tickets) keep(key string, s *utls.ClientSessionState, used *Ticket, notAfter time.Time) {
	_, state, err := s.ResumptionState()
	if err != nil || state == nil {
		return
	}

	tk := ticket{
		state:   s,
		expires: notAfter,
	}

	switch state.Version() {
	case utls.VersionTLS13:
		tk.tls13 = true
		if useBy := state.UseBy(); !useBy.IsZero() && (tk.expires.IsZero() || useBy.Before(tk.expires)) {
			tk.expires = useBy
		}

	case utls.VersionTLS12:
		// The TLS 1.2 ticket lifetime hint is not used: how long the
		// browser keeps such a ticket is not measured.
		if t.tls12 == "" || (t.tls12 == profile.TLS12SingleUse && used != nil) {
			return
		}

	default:
		return
	}

	t.s.put(key, tk, t.clock.Now())
}

// put stores tk under key, dropping expired tickets at now once the store
// has grown to sweepAt.
func (s *Store) put(key string, tk ticket, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.putLocked(key, tk, now)
}

// putLocked is put with s.mu held.
func (s *Store) putLocked(key string, tk ticket, now time.Time) {
	s.m[key] = tk
	if len(s.m) >= s.sweepAt {
		for k, other := range s.m {
			if other.expired(now) {
				delete(s.m, k)
			}
		}

		s.sweepAt = max(minSweep, 2*len(s.m))
	}
}

// Saved is a stored ticket as Export gives it and Import takes it: its
// key, when it stops being usable (zero: unknown), and the session as
// uTLS encodes it (ClientSessionState.ResumptionState: the ticket, and the
// SessionState's Bytes, the secrets included).
type Saved struct {
	Key     string
	Expires time.Time
	Ticket  []byte
	State   []byte
}

// Export returns the tickets usable at now.
func (s *Store) Export(now time.Time) ([]Saved, error) {
	type entry struct {
		key string
		tk  ticket
	}

	// Stored sessions are never changed: encode them after unlocking, so
	// that handshakes do not wait for it.
	s.mu.Lock()

	entries := make([]entry, 0, len(s.m))
	for key, tk := range s.m {
		if !tk.expired(now) {
			entries = append(entries, entry{key, tk})
		}
	}

	s.mu.Unlock()

	saved := make([]Saved, 0, len(entries))
	for _, e := range entries {
		ticket, state, err := e.tk.state.ResumptionState()
		if err != nil {
			return nil, err
		}

		b, err := state.Bytes()
		if err != nil {
			return nil, err
		}

		saved = append(saved, Saved{
			Key:     e.key,
			Expires: e.tk.expires,
			Ticket:  ticket,
			State:   b,
		})
	}

	return saved, nil
}

// Import adds the tickets of saved, which belong to browser (empty:
// unknown), still usable at now, replacing any under the same key. A
// session that does not parse or is neither TLS 1.2 nor 1.3, or a store
// belonging to another browser, fails it before anything is added.
func (s *Store) Import(saved []Saved, browser string, now time.Time) error {
	tks := make([]ticket, len(saved))
	for i, sv := range saved {
		tk, err := parseSaved(sv)
		if err != nil {
			return fmt.Errorf("ticket %d: %w", i, err)
		}

		tks[i] = tk
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.bind(browser); err != nil {
		return err
	}

	for i, tk := range tks {
		if !tk.expired(now) {
			s.putLocked(saved[i].Key, tk, now)
		}
	}

	return nil
}

// parseSaved is sv's ticket.
func parseSaved(sv Saved) (ticket, error) {
	if len(sv.Ticket) == 0 {
		return ticket{}, errors.New("no ticket")
	}

	state, err := utls.ParseSessionState(sv.State)
	if err != nil {
		return ticket{}, err
	}

	tk := ticket{
		expires: sv.Expires,
	}

	switch state.Version() {
	case utls.VersionTLS13:
		tk.tls13 = true
		// As keep: never past the ticket's own lifetime, whatever the
		// file says, or uTLS would decline a ticket the hello offers.
		if useBy := state.UseBy(); !useBy.IsZero() && (tk.expires.IsZero() || useBy.Before(tk.expires)) {
			tk.expires = useBy
		}

	case utls.VersionTLS12:
	default:
		return ticket{}, fmt.Errorf("session version %#04x is neither TLS 1.2 nor 1.3", state.Version())
	}

	// The ticket is copied: NewResumptionState keeps the slice.
	if tk.state, err = utls.NewResumptionState(append([]byte(nil), sv.Ticket...), state); err != nil {
		return ticket{}, err
	}

	return tk, nil
}

// Clear forgets every ticket. The store still belongs to its browser.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.m)

	s.sweepAt = minSweep
}

// connCache is the session cache one connection's handshake sees: the
// ticket it took (or none) and, for new tickets, the shared store. uTLS
// asks it by server name; the connection's own key is what counts.
type connCache struct {
	ticket  *Ticket
	tickets *Tickets
	key     string
	// notAfter is the server certificate's expiry, set when the handshake
	// verifies it, before any ticket arrives.
	notAfter time.Time
	// noKeep: the connection sent a client certificate, and the browser
	// resumes no such session (profile.ClientAuth.Resume false), so its
	// tickets are not kept. Set during the handshake, before any ticket.
	noKeep bool
}

func (c *connCache) Get(string) (*utls.ClientSessionState, bool) {
	if c.ticket == nil {
		return nil, false
	}

	return c.ticket.state, true
}

func (c *connCache) Put(_ string, s *utls.ClientSessionState) {
	if s != nil && !c.noKeep {
		c.tickets.keep(c.key, s, c.ticket, c.notAfter)
	}
}
