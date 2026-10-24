package handshake

import (
	"sync"

	utls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/profile"
)

// Tickets keeps session tickets by key (host, port and connection
// partition), used as the browser uses them: a TLS 1.3 ticket once (a
// connection takes it, and the server's new tickets on that connection
// replace it); a TLS 1.2 ticket by the profile's tls12 policy. Safe for
// concurrent use.
type Tickets struct {
	// tls12 is profile.Resumption.TLS12; empty keeps no TLS 1.2 tickets.
	tls12 string

	mu sync.Mutex
	m  map[string]*utls.ClientSessionState
}

// NewTickets returns an empty store using TLS 1.2 tickets as tls12
// (profile.TLS12Reuse, profile.TLS12SingleUse, or "" for not at all).
func NewTickets(tls12 string) *Tickets {
	return &Tickets{
		tls12: tls12,
		m:     map[string]*utls.ClientSessionState{},
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

// Take returns the ticket a new connection for key offers, or nil. TLS 1.3
// tickets, and TLS 1.2 ones under single use, leave the store: concurrent
// connections then resume one at a time.
func (t *Tickets) Take(key string) *Ticket {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.m[key]
	if !ok {
		return nil
	}
	tk := &Ticket{
		state: s,
		tls13: version(s) == utls.VersionTLS13,
	}
	if tk.tls13 || t.tls12 != profile.TLS12Reuse {
		delete(t.m, key)
	}
	return tk
}

// keep stores a ticket the server sent on a connection that resumed with
// used (nil: a full handshake).
func (t *Tickets) keep(key string, s *utls.ClientSessionState, used *Ticket) {
	switch version(s) {
	case utls.VersionTLS13:
	case utls.VersionTLS12:
		if t.tls12 == "" || (t.tls12 == profile.TLS12SingleUse && used != nil) {
			return
		}
	default:
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[key] = s
}

// connCache is the session cache one connection's handshake sees: the
// ticket it took (or none) and, for new tickets, the shared store. uTLS
// asks it by server name; the connection's own key is what counts.
type connCache struct {
	ticket  *Ticket
	tickets *Tickets
	key     string
}

func (c *connCache) Get(string) (*utls.ClientSessionState, bool) {
	if c.ticket == nil {
		return nil, false
	}
	return c.ticket.state, true
}

func (c *connCache) Put(_ string, s *utls.ClientSessionState) {
	if s != nil {
		c.tickets.keep(c.key, s, c.ticket)
	}
}

// version reads a session's TLS version: the first two bytes of its
// encoding; zero when it cannot.
func version(s *utls.ClientSessionState) uint16 {
	_, state, err := s.ResumptionState()
	if err != nil || state == nil {
		return 0
	}
	b, err := state.Bytes()
	if err != nil || len(b) < 2 {
		return 0
	}
	return uint16(b[0])<<8 | uint16(b[1])
}
