package capture

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// H1Request is one HTTP/1.1 request as the client sent it, casing and order
// intact.
type H1Request struct {
	// Conn numbers the connection it came on, from 1 in order of arrival:
	// browsers spread an HTTP/1.1 page over several.
	Conn int `json:"conn"`
	// Host is the connection's server name, recorded when a page spans
	// several hosts (replay scenarios).
	Host string `json:"host,omitempty"`
	// Line is the request line ("GET / HTTP/1.1").
	Line string `json:"line"`
	// Headers are the header lines, verbatim ("User-Agent: ...").
	Headers []string `json:"headers"`
}

// Page is a whole page load across every connection and host it used:
// each HTTP/2 connection's trace, every HTTP/1.1 request, every
// connection's handshake, the WebSockets the page opened with what its
// script saw of each (the websocket scenarios), and every QUIC connection
// with its HTTP/3 (the h3 scenarios).
type Page struct {
	H2          []*h2trace.Trace `json:"h2"`
	H1          []H1Request      `json:"h1,omitempty"`
	Connections []Connection     `json:"connections,omitempty"`
	WebSockets  []WebSocket      `json:"websockets,omitempty"`
	WSResults   []WSResult       `json:"ws_results,omitempty"`
	// QUIC are the QUIC connections, in the order their first datagrams
	// arrived; their handshakes are not in Connections, which are TCP's.
	QUIC []*quictrace.Conn `json:"quic,omitempty"`
	// Conditions are browser settings the capture tool changed for this
	// capture beyond its defaults, each saying why (see cmd/capture's
	// -h3-local-roots).
	Conditions []string `json:"conditions,omitempty"`
	// Start is when the server started serving the page, the zero of its
	// times in ms, on the recording process's clock: a test lines the page
	// up with its own by it. Not stored, so zero in a capture read from a
	// file.
	Start time.Time `json:"-"`
}

// Connection is one connection of a page, in the order their handshakes
// completed.
type Connection struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol"`
	// Millis is when its ClientHello arrived, since the server started
	// serving the page (the clock of Page.QUIC), so a TCP connection and
	// a QUIC one raced for the same host show as such. Zero in captures
	// made before it was kept.
	Millis int64 `json:"ms,omitempty"`
	// Resumed reports a ClientHello offering an earlier session (TLS 1.3
	// pre_shared_key, or a TLS 1.2 session ticket) instead of a full
	// handshake.
	Resumed bool `json:"resumed,omitempty"`
	// Accepted reports that the server resumed the session the client
	// offered.
	Accepted bool `json:"accepted,omitempty"`
	// PSKIdentities is how many tickets the ClientHello offered, and
	// PSKIdentity a short hash of the first, to tell one ticket used twice
	// from fresh ones.
	PSKIdentities int    `json:"psk_identities,omitempty"`
	PSKIdentity   string `json:"psk_identity,omitempty"`
	// Ticket is a short hash of the TLS 1.2 session ticket offered.
	Ticket string `json:"ticket,omitempty"`
	// Requests is how many requests the connection carried; zero for one
	// a browser opened speculatively and never used.
	Requests int `json:"requests,omitempty"`
	// Hello is the connection's ClientHello.
	Hello *profile.TLS `json:"hello,omitempty"`
	// RecordVersion is the record-layer version of the records that
	// carried it; zero in captures made before it was kept.
	RecordVersion uint16 `json:"record_version,omitempty"`
}

// LoadPages reads a replayed page capture written by Write
// (<name>.<scenario>.pagetrace.json.gz): every sample's page, its QUIC
// connections rederived from their wire data (quictrace.Conn.Rederive).
func LoadPages(path string) ([]*Page, error) {
	f, err := os.Open(path) //nolint:gosec // a capture file the caller names
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()
	r, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var pages []*Page
	if err := json.NewDecoder(r).Decode(&pages); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, p := range pages {
		for _, c := range p.QUIC {
			c.Rederive()
		}
	}
	return pages, nil
}

func gzipped(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
