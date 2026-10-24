package server

import (
	"sync"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// pageLog collects one page load across the server's connections: browsers
// spread a page over several, the more so over HTTP/1.1 and across hosts.
type pageLog struct {
	mu       sync.Mutex
	conns    int
	requests []capture.H1Request
	h2       []*h2conn
	// connections are every connection's host, protocol, resumption and
	// parsed ClientHello; firstHello and firstRecords are the first one's
	// raw ClientHello and the records that carried it: on a fresh server it
	// cannot resume a session, so it is the browser's full handshake.
	connections  []capture.Connection
	firstHello   []byte
	firstRecords []byte
	// index finds a connection's entry in connections by its capture.
	index map[*capture.Capture]int
}

// connection records a connection whose handshake completed: its
// ClientHello, and whether it offered and got a resumed session.
func (l *pageLog) connection(c *capture.Capture, host string, accepted bool) {
	o := resumptionOffer(c.ClientHello)
	hello := c.Profile.TLS
	l.mu.Lock()
	defer l.mu.Unlock()
	l.connections = append(l.connections, capture.Connection{
		Host:          host,
		Protocol:      c.Protocol,
		Resumed:       o.pskIdentities > 0 || o.ticket != "",
		Accepted:      accepted,
		PSKIdentities: o.pskIdentities,
		PSKIdentity:   o.pskIdentity,
		Ticket:        o.ticket,
		Hello:         &hello,
		RecordVersion: c.RecordVersion(),
	})
	if l.index == nil {
		l.index = map[*capture.Capture]int{}
	}
	l.index[c] = len(l.connections) - 1
	if len(l.connections) == 1 {
		l.firstHello = c.ClientHello
		l.firstRecords = c.Records
	}
}

// newConn numbers an HTTP/1.1 connection, from 1.
func (l *pageLog) newConn() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.conns++
	return l.conns
}

// request counts a request on the connection c captures: a connection
// with none was opened speculatively and never used.
func (l *pageLog) request(c *capture.Capture) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i, ok := l.index[c]; ok {
		l.connections[i].Requests++
	}
}

func (l *pageLog) add(r capture.H1Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, r)
}

func (l *pageLog) snapshot() []capture.H1Request {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]capture.H1Request(nil), l.requests...)
}

func (l *pageLog) addH2(s *h2conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.h2 = append(l.h2, s)
}

// page stops tracing every HTTP/2 connection and returns the whole page
// load: each connection's trace and every HTTP/1.1 request.
func (l *pageLog) page() *capture.Page {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := &capture.Page{
		H1:          append([]capture.H1Request(nil), l.requests...),
		Connections: append([]capture.Connection(nil), l.connections...),
	}
	for _, s := range l.h2 {
		p.H2 = append(p.H2, s.stopTracing())
	}
	return p
}

// pageProfile trims a page's capture to what is the same in every sample:
// the ClientHello of the page's first connection and the user agent that
// names the browser. The connection that asked for /done may have resumed
// a session, and its first request depends on timing and carries ports and
// cookies; the page's data is its traces.
func (l *pageLog) pageProfile(c *capture.Capture) {
	l.mu.Lock()
	if len(l.connections) > 0 {
		c.ClientHello = l.firstHello
		c.Records = l.firstRecords
		c.Profile.TLS = *l.connections[0].Hello
	}
	l.mu.Unlock()
	ua := c.UserAgent()
	c.Profile.HTTP2 = nil
	c.Profile.Headers = []profile.Header{
		{
			Name:  "user-agent",
			Value: ua,
		},
	}
}
