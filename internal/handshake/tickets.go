package handshake

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	utls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/profile"
)

// Tickets keeps session tickets by key (host, port and connection
// partition), used as the browser uses them: a TLS 1.3 ticket once (a
// connection takes it, and the server's new tickets on that connection
// replace it); a TLS 1.2 ticket by the profile's tls12 policy. A ticket
// uTLS would decline is never handed out, so the ClientHello never takes the
// resumed shape without resuming: a TLS 1.3 ticket past its lifetime, or any
// ticket whose server certificate has expired. Safe for concurrent use.
type Tickets struct {
	// tls12 is profile.Resumption.TLS12; empty keeps no TLS 1.2 tickets.
	tls12 string
	clock clockwork.Clock

	mu sync.Mutex
	m  map[string]ticket
	// sweepAt is the store size at which keep next drops expired tickets.
	sweepAt int
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

// NewTickets returns an empty store using TLS 1.2 tickets as tls12
// (profile.TLS12Reuse, profile.TLS12SingleUse, or "" for not at all), with
// clock telling expired tickets.
func NewTickets(tls12 string, clock clockwork.Clock) *Tickets {
	return &Tickets{
		tls12:   tls12,
		clock:   clock,
		m:       map[string]ticket{},
		sweepAt: minSweep,
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

// Take returns the ticket a new connection for key offers, or nil. TLS 1.3
// tickets, and TLS 1.2 ones under single use, leave the store: concurrent
// connections then resume one at a time. Expired tickets are dropped.
func (t *Tickets) Take(key string) *Ticket {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.m[key]
	if !ok {
		return nil
	}
	if s.expired(t.clock.Now()) {
		delete(t.m, key)
		return nil
	}
	if s.tls13 || t.tls12 != profile.TLS12Reuse {
		delete(t.m, key)
	}
	return &Ticket{
		state: s.state,
		tls13: s.tls13,
	}
}

// expired reports whether uTLS would decline the ticket at now.
func (s ticket) expired(now time.Time) bool {
	return !s.expires.IsZero() && now.After(s.expires)
}

// keep stores a ticket the server sent on a connection that resumed with
// used (nil: a full handshake) and whose certificate expires at notAfter.
func (t *Tickets) keep(key string, s *utls.ClientSessionState, used *Ticket, notAfter time.Time) {
	enc := encoding(s)
	tk := ticket{
		state:   s,
		expires: notAfter,
	}
	switch version(enc) {
	case utls.VersionTLS13:
		tk.tls13 = true
		if useBy, ok := useBy(enc); ok && (tk.expires.IsZero() || useBy.Before(tk.expires)) {
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
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[key] = tk
	if len(t.m) >= t.sweepAt {
		now := t.clock.Now()
		for k, other := range t.m {
			if other.expired(now) {
				delete(t.m, k)
			}
		}
		t.sweepAt = max(minSweep, 2*len(t.m))
	}
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
}

func (c *connCache) Get(string) (*utls.ClientSessionState, bool) {
	if c.ticket == nil {
		return nil, false
	}
	return c.ticket.state, true
}

func (c *connCache) Put(_ string, s *utls.ClientSessionState) {
	if s != nil {
		c.tickets.keep(c.key, s, c.ticket, c.notAfter)
	}
}

// encoding is a session's encoding (utls.SessionState.Bytes), or nil.
func encoding(s *utls.ClientSessionState) []byte {
	_, state, err := s.ResumptionState()
	if err != nil || state == nil {
		return nil
	}
	b, err := state.Bytes()
	if err != nil {
		return nil
	}
	return b
}

// version reads a session's TLS version: the first two bytes of its
// encoding; zero when it has none.
func version(enc []byte) uint16 {
	if len(enc) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(enc)
}

// useBy reads a client TLS 1.3 session's use_by time (seconds since the
// epoch), which a client's encoding ends with, followed by the 4-byte
// age_add.
func useBy(enc []byte) (time.Time, bool) {
	if len(enc) < 12 {
		return time.Time{}, false
	}
	secs := binary.BigEndian.Uint64(enc[len(enc)-12:])
	return time.Unix(int64(secs), 0), true //nolint:gosec // seconds since the epoch fit in int64
}
