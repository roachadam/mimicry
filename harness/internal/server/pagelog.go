package server

import (
	"slices"
	"sync"
	"time"

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
	// start is when the server started serving the page; websockets are
	// the sockets the page opened, and results what its script reported
	// of each (the websocket scenarios).
	start      time.Time
	websockets []*capture.WebSocket
	results    []capture.WSResult
	// quic is what the server's UDP socket received (the h3 scenarios):
	// every client address's datagrams, the HTTP/3 connections served,
	// and the key log that opens their packets.
	quic quicLog
}

// connection records a connection whose handshake completed: its
// ClientHello, which arrived at helloAt, and whether it offered and got a
// resumed session.
func (l *pageLog) connection(c *capture.Capture, host string, accepted bool, helloAt time.Time) {
	o := resumptionOffer(c.ClientHello)
	hello := c.Profile.TLS
	l.mu.Lock()
	defer l.mu.Unlock()
	l.connections = append(l.connections, capture.Connection{
		Host:          host,
		Protocol:      c.Protocol,
		Millis:        helloAt.Sub(l.start).Milliseconds(),
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

// add appends an HTTP/1.1 request and returns its index.
func (l *pageLog) add(r capture.H1Request) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, r)
	return len(l.requests) - 1
}

func (l *pageLog) snapshot() []capture.H1Request {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]capture.H1Request(nil), l.requests...)
}

// addH2 adds an HTTP/2 connection and returns its trace's index in the
// page's H2.
func (l *pageLog) addH2(s *h2conn) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.h2 = append(l.h2, s)
	return len(l.h2) - 1
}

// connectionNumber numbers the connection c captures in the page's
// Connections, from 1; 0 for one without a TLS handshake.
func (l *pageLog) connectionNumber(c *capture.Capture) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i, ok := l.index[c]; ok {
		return i + 1
	}
	return 0
}

// openWebSocket records a socket whose handshake the server accepted
// and returns the recorder of its frames.
func (l *pageLog) openWebSocket(ws capture.WebSocket) *wsRecorder {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	ws.Millis = now.Sub(l.start).Milliseconds()
	ws.Frames = []capture.WSFrame{}
	r := &wsRecorder{
		log:   l,
		ws:    &ws,
		start: now,
	}
	l.websockets = append(l.websockets, r.ws)
	return r
}

// result records what the page's script reported of a WebSocket step.
func (l *pageLog) result(r capture.WSResult) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.results = append(l.results, r)
}

// page stops tracing every HTTP/2 connection and returns the whole page
// load: each connection's trace, every HTTP/1.1 request, the WebSockets
// and the QUIC connections as recorded so far.
func (l *pageLog) page() *capture.Page {
	quic := l.quicConns()
	l.mu.Lock()
	defer l.mu.Unlock()
	p := &capture.Page{
		H1:          append([]capture.H1Request(nil), l.requests...),
		Connections: append([]capture.Connection(nil), l.connections...),
		WSResults:   append([]capture.WSResult(nil), l.results...),
		QUIC:        quic,
	}
	for _, s := range l.h2 {
		p.H2 = append(p.H2, s.stopTracing())
	}
	for _, ws := range l.websockets {
		w := *ws
		w.Frames = slices.Clone(ws.Frames)
		p.WebSockets = append(p.WebSockets, w)
	}
	return p
}

// wsRecorder records one WebSocket's frames and how it ended into the
// page log.
type wsRecorder struct {
	log *pageLog
	ws  *capture.WebSocket
	// start is when the handshake arrived.
	start time.Time
}

// frame records f, timed from the handshake.
func (r *wsRecorder) frame(f capture.WSFrame) {
	f.Millis = time.Since(r.start).Milliseconds()
	r.log.mu.Lock()
	defer r.log.mu.Unlock()
	r.ws.Frames = append(r.ws.Frames, f)
}

// closed records that by ("client" or "server") ended the transport, if
// neither side had yet.
func (r *wsRecorder) closed(by string) {
	ms := time.Since(r.start).Milliseconds()
	r.log.mu.Lock()
	defer r.log.mu.Unlock()
	if r.ws.ClosedBy == "" {
		r.ws.ClosedBy = by
		r.ws.ClosedMillis = ms
	}
}

// streamEnd records how the client ended its h2 stream.
func (r *wsRecorder) streamEnd(how string) {
	r.log.mu.Lock()
	defer r.log.mu.Unlock()
	if r.ws.StreamEnd == "" {
		r.ws.StreamEnd = how
	}
}

// fail records why the server stopped reading the socket.
func (r *wsRecorder) fail(err error) {
	r.log.mu.Lock()
	defer r.log.mu.Unlock()
	r.ws.Error = err.Error()
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
