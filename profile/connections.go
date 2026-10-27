package profile

// Connections are how the browser shares connections between requests:
// HTTP/1.1 connections per host, and HTTP/2 coalescing. Required (decision
// 0008).
type Connections struct {
	// HTTP1PerHost is the most HTTP/1.1 connections open at once to one
	// host (scheme, host, port, partition, proxy), dials included. Busy
	// requests wait in order. At least 1.
	HTTP1PerHost int `json:"http1PerHost"`
	// Coalesce is when a request takes another host's HTTP/2 connection:
	// CoalesceOnLookup or CoalesceOnConnect. The connection must be in the
	// same partition, same port, unproxied, with a certificate covering
	// the host.
	Coalesce Coalescing `json:"coalesce"`
}

// Coalescing is when a request takes another host's HTTP/2 connection: a
// value of Connections.Coalesce.
type Coalescing string

// Values of Coalescing.
const (
	// CoalesceOnLookup looks the host's addresses up before connecting
	// and sends the request on a connection to one of them, opening none
	// (Chrome, Safari).
	CoalesceOnLookup Coalescing = "lookup"
	// CoalesceOnConnect completes the TLS handshake first; if a connection
	// to the reached address exists, the new one sends its HTTP/2 preface
	// and a GOAWAY, closes, and the request uses the existing one (Firefox).
	CoalesceOnConnect Coalescing = "connect"
)

// checkConnections checks the connections section: required, a cap of at
// least one connection, and a coalescing rule mimicry implements.
func (c *checker) checkConnections(conns *Connections) {
	if conns == nil {
		c.add("connections", "is required")
		return
	}
	if conns.HTTP1PerHost < 1 {
		c.add("connections.http1PerHost", "%d must be at least 1", conns.HTTP1PerHost)
	}
	if conns.Coalesce != CoalesceOnLookup && conns.Coalesce != CoalesceOnConnect {
		c.add("connections.coalesce", "%q must be %q or %q", conns.Coalesce, CoalesceOnLookup, CoalesceOnConnect)
	}
}
