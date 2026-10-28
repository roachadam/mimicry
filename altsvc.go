package mimicry

import (
	"io"

	"github.com/roachadam/mimicry/internal/altsvc"
)

// AltSvcCache keeps the HTTP/3 alternatives servers advertise (Alt-Svc,
// RFC 7838) for the Transports given it in Config.AltSvc, as a browser keeps
// one for all its tabs and, saved to disk, across restarts (Chrome's HTTP
// server properties, Firefox's alternate services). A Transport finding an
// origin's alternative in it moves to HTTP/3 as its browser does once it
// knows one, so a new Transport's first request to an origin a previous
// Transport learnt can go over HTTP/3 (Config.AltSvc says per browser).
//
// An entry lives until its ma (max-age; 24 hours without one) or a
// "clear" from its origin. Only the alternatives are shared: which origins
// failed over QUIC, connections and session tickets stay each Transport's.
// Entries are kept per connection partition (anonymous requests and
// cross-site frames apart), not per top-level site: browsers partition
// them by top-level site too, which mimicry does not model (decision
// 0011). Create one with NewAltSvcCache; it is safe for concurrent use.
type AltSvcCache struct {
	c *altsvc.Cache
}

// NewAltSvcCache returns an empty cache.
func NewAltSvcCache() *AltSvcCache {
	return &AltSvcCache{
		c: altsvc.New(nil),
	}
}

// WriteJSON writes the unexpired alternatives as a JSON array, each with
// its origin ("https://host:port"), partition (when not the default), port
// and expiry: what ReadJSON reads back.
func (c *AltSvcCache) WriteJSON(w io.Writer) error {
	return c.c.WriteJSON(w)
}

// ReadJSON adds the alternatives of a JSON array WriteJSON wrote, replacing
// those of the same origin and partition. Expired ones are skipped; a
// malformed entry fails it before anything is added.
func (c *AltSvcCache) ReadJSON(r io.Reader) error {
	return c.c.ReadJSON(r)
}

// Clear forgets every alternative. Transports sharing the cache keep the
// QUIC connections they have.
func (c *AltSvcCache) Clear() {
	c.c.Clear()
}
