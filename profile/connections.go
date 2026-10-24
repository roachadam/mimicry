package profile

// Connections are how the browser shares connections between requests:
// how many HTTP/1.1 connections it opens to one host, and when a request
// to one host goes on another host's HTTP/2 connection (coalescing).
// Required: every request needs them (decision 0008).
type Connections struct {
	// HTTP1PerHost is the most HTTP/1.1 connections open at once to one
	// host (scheme, host and port, in one partition, through one proxy),
	// dials in progress included. A request finding them all busy waits
	// for one, in the order the requests were made. At least 1.
	HTTP1PerHost int `json:"http1_per_host"`
	// Coalesce is when a request to a host with no HTTP/2 connection of
	// its own takes one to another host: CoalesceOnLookup or
	// CoalesceOnConnect. Either way the connection taken is in the same
	// partition, to the same port, not through a proxy, and its server's
	// certificate covers the request's host.
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
	// CoalesceOnConnect connects to the host and completes the TLS
	// handshake first; if a connection to the address reached exists, the
	// new one sends its HTTP/2 preface (SETTINGS, WINDOW_UPDATE) and a
	// GOAWAY and closes, and the request goes on the existing one
	// (Firefox).
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
		c.add("connections.http1_per_host", "%d must be at least 1", conns.HTTP1PerHost)
	}
	if conns.Coalesce != CoalesceOnLookup && conns.Coalesce != CoalesceOnConnect {
		c.add("connections.coalesce", "%q must be %q or %q", conns.Coalesce, CoalesceOnLookup, CoalesceOnConnect)
	}
}
