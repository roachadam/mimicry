package mimicry

import (
	"io"
	"time"

	"github.com/roachadam/mimicry/internal/engine/native"
	"github.com/roachadam/mimicry/internal/handshake"
)

// TicketCache keeps the TLS session tickets, over TCP and QUIC, of the
// Transports given it in Config.Tickets, as one browser keeps them for all
// its tabs. Save it (WriteJSON) and load it into a new process (ReadJSON)
// to carry an identity's sessions over, so its first connections resume,
// with 0-RTT where the profile's browser uses it, instead of starting with
// full handshakes.
//
// What that models depends on the browser (decision 0062). Chrome 156 and
// Firefox 159 keep tickets in memory only: relaunched, they start with
// full handshakes, so a loaded cache models one that stayed open. Safari
// 27 resumes its QUIC sessions, with 0-RTT, after a relaunch too (it never
// resumes over TCP). Share a cache only between Transports of one
// identity: a ticket links the connections that use it, as a cookie would.
//
// Tickets are kept per origin, connection partition and proxy, and used as
// the profile's browser uses them: a TLS 1.3 ticket once. A ticket saved
// and loaded into two processes would be used twice, which no browser
// does: load a saved cache into one process only. Through a proxy, a
// ticket is filed under a hash of the proxy's URL, credentials included,
// never the URL itself: it resumes only through the same proxy URL.
//
// Create one with NewTicketCache; it is safe for concurrent use.
type TicketCache struct {
	s *handshake.Store
}

// NewTicketCache returns an empty cache.
func NewTicketCache() *TicketCache {
	return &TicketCache{
		s: handshake.NewStore(),
	}
}

// WriteJSON writes the usable tickets as a JSON array, each with its
// origin ("https://host:port"), partition and proxy hash (when not the
// default), whether it is for HTTP/3, its expiry and the session itself:
// what ReadJSON reads back. The session holds the secrets that resumed
// connections use: keep the output as you would a password.
func (c *TicketCache) WriteJSON(w io.Writer) error {
	return native.WriteTickets(w, c.s, time.Now())
}

// ReadJSON adds the tickets of a JSON array WriteJSON wrote, replacing
// those of the same origin, partition, proxy and protocol. Expired ones
// are skipped; a malformed entry fails it before anything is added. A
// file written by another version of mimicry may not be read.
//
// Read only files you wrote: a resumed connection is authenticated by the
// ticket's secret alone, not the server's certificate, so whoever can
// write the file can stand in for every server it names.
func (c *TicketCache) ReadJSON(r io.Reader) error {
	return native.ReadTickets(r, c.s, time.Now())
}

// Clear forgets every ticket. Transports sharing the cache keep the
// connections they have.
func (c *TicketCache) Clear() {
	c.s.Clear()
}
