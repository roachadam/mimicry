package handshake

import (
	"sync"

	utls "github.com/roachadam/utls"
)

// Tickets keeps TLS 1.3 session tickets by key (host, port and connection
// partition). Each ticket is used once, as Chrome and Firefox use them: a
// connection takes the latest ticket for its key, and the server's new
// tickets on that connection replace it. Safe for concurrent use.
type Tickets struct {
	mu sync.Mutex
	m  map[string]*utls.ClientSessionState
}

// NewTickets returns an empty store.
func NewTickets() *Tickets {
	return &Tickets{
		m: map[string]*utls.ClientSessionState{},
	}
}

// Ticket is a session a connection resumes, taken from a Tickets.
type Ticket struct {
	state *utls.ClientSessionState
}

// Take removes and returns the ticket for key, or nil when there is none:
// concurrent connections to one server resume one at a time.
func (t *Tickets) Take(key string) *Ticket {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.m[key]
	if !ok {
		return nil
	}
	delete(t.m, key)
	return &Ticket{
		state: s,
	}
}

func (t *Tickets) put(key string, s *utls.ClientSessionState) {
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

// Put keeps TLS 1.3 tickets only: the resumed ClientHello is built for TLS
// 1.3 (pre_shared_key; Firefox's leaves session_ticket out), so a TLS 1.2
// session could not be resumed the browser's way.
func (c *connCache) Put(_ string, s *utls.ClientSessionState) {
	if s == nil || !isTLS13(s) {
		return
	}
	c.tickets.put(c.key, s)
}

// isTLS13 reads a session's version: the first two bytes of its encoding.
func isTLS13(s *utls.ClientSessionState) bool {
	_, state, err := s.ResumptionState()
	if err != nil || state == nil {
		return false
	}
	b, err := state.Bytes()
	if err != nil || len(b) < 2 {
		return false
	}
	return uint16(b[0])<<8|uint16(b[1]) == utls.VersionTLS13
}
