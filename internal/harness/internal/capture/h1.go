package capture

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
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
	// Driver is how the capture tool sent Safari on an iPhone to the
	// page (browser.IOSDriver: devicectl, manual or safaridriver) in an
	// HTTP/1.1 trace's requests; empty for other browsers and in a page.
	Driver string `json:"driver,omitempty"`
	// Upload is how the request went out in TLS records, in scenarios
	// that record it (TLS 1.3 only); nil otherwise.
	Upload *H1Upload `json:"upload,omitempty"`
}

// H1Upload is how a client cut one HTTP/1.1 request, its head and body,
// into TLS records.
type H1Upload struct {
	// Offset is how many bytes of application data the client had sent
	// on the connection before the request; Head and Body are the
	// request head's and body's lengths.
	Offset int `json:"offset"`
	Head   int `json:"head"`
	Body   int `json:"body"`
	// Lead is how much of the first record came before the request, and
	// Trail how much of the last came after it.
	Lead  int `json:"lead,omitempty"`
	Trail int `json:"trail,omitempty"`
	// Records are the records that carried the request, from the one
	// holding its first byte to the one holding its last, in order, as
	// runs of records of one content length.
	Records []RecordRun `json:"records"`
}

// RecordRun is a run of consecutive TLS records of one content length.
type RecordRun struct {
	Size  int `json:"size"`
	Count int `json:"count"`
	// Millis is when the run's last record arrived at the server, from
	// the request's first record.
	Millis float64 `json:"ms"`
}

// Page is a whole page load across every connection and host it used:
// HTTP/2 traces, HTTP/1.1 requests, TLS handshakes, WebSockets and QUIC
// connections.
type Page struct {
	H2          []*h2trace.Trace `json:"h2"`
	H1          []H1Request      `json:"h1,omitempty"`
	Connections []Connection     `json:"connections,omitempty"`
	WebSockets  []WebSocket      `json:"websockets,omitempty"`
	WSResults   []WSResult       `json:"wsResults,omitempty"`
	// QUIC are the QUIC connections, in the order their first datagrams
	// arrived; their handshakes are not in Connections, which are TCP's.
	QUIC []*quictrace.Conn `json:"quic,omitempty"`
	// Conditions are browser settings the capture tool changed from their
	// defaults, each saying why (cmd/capture's -h3-local-roots).
	Conditions []string `json:"conditions,omitempty"`
	// Driver is how the capture tool sent Safari on an iPhone to the page
	// (browser.IOSDriver): devicectl and manual are the phone's own
	// Safari, safaridriver its automation window. Empty for other
	// browsers and in captures made before it was kept.
	Driver string `json:"driver,omitempty"`
	// DNS are the browser's lookups of the page's hosts, in the order
	// they arrived (the ech scenario, whose hosts the browser looks up
	// instead of having them mapped).
	DNS []DNSQuery `json:"dns,omitempty"`
	// Start is the zero of the page's millisecond times, on the recording
	// process's clock. Not stored: zero in a capture read from a file.
	Start time.Time `json:"-"`
}

// Connection is one connection of a page, in the order their handshakes
// completed.
type Connection struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol"`
	// Millis is when its ClientHello arrived, on Page.QUIC's clock, so a
	// TCP and a QUIC connection racing for one host show as such. Zero in
	// older captures.
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
	PSKIdentities int    `json:"pskIdentities,omitempty"`
	PSKIdentity   string `json:"pskIdentity,omitempty"`
	// Ticket is a short hash of the TLS 1.2 session ticket offered.
	Ticket string `json:"ticket,omitempty"`
	// Requests is how many requests the connection carried; zero for one
	// a browser opened speculatively and never used.
	Requests int `json:"requests,omitempty"`
	// Hello is the connection's ClientHello, and ClientHello it as sent,
	// hex (its key shares' bytes are what the parsed hello leaves out);
	// absent in captures made before it was kept.
	Hello       *profile.TLS `json:"hello,omitempty"`
	ClientHello string       `json:"clientHello,omitempty"`
	// RecordVersion is the record-layer version of the records that
	// carried it; zero in captures made before it was kept.
	RecordVersion uint16 `json:"recordVersion,omitempty"`
	// Retry is the second ClientHello, after a HelloRetryRequest with a
	// cookie (the hrr scenario), whose handshake the server then dropped:
	// Protocol is empty.
	Retry *Retry `json:"retry,omitempty"`
	// Refused says why the server's TLS refused the ClientHello, ending
	// the handshake there (Protocol is empty, Host the server_name as
	// sent): a server name ending in a dot, which Go's crypto/tls
	// rejects (the urls scenario).
	Refused string `json:"refused,omitempty"`
	// ECH is the encrypted_client_hello the ClientHello carried for one of
	// the ech scenario's configs; nil for none, and for GREASE.
	ECH *ECH `json:"ech,omitempty"`
}

// DNSQuery is a DNS lookup a browser made during a page.
type DNSQuery struct {
	// Millis is when it arrived, on the page's clock (Connection.Millis).
	Millis int64 `json:"ms"`
	// Name is the name looked up, without the final dot.
	Name string `json:"name"`
	// Type is the record type: A, AAAA, HTTPS, ...
	Type string `json:"type"`
	// Via is how it came: "system", through the system's resolver to the
	// capture tool's DNS stub, or "doh", to its DNS-over-HTTPS endpoint.
	Via string `json:"via"`
}

// ECH is a real encrypted_client_hello: the ECH config a ClientHelloOuter
// encrypted its ClientHelloInner to, and, when the server held its key, the
// inner hello.
type ECH struct {
	// ConfigID is the config's ID, KDF and AEAD the HPKE suite the client
	// chose from it.
	ConfigID uint8        `json:"configId"`
	KDF      profile.Code `json:"kdf"`
	AEAD     profile.Code `json:"aead"`
	// Accepted reports that the server decrypted it and went on with the
	// inner hello; otherwise it finished the handshake with the outer one,
	// sending its retry configs if it had any (Retry).
	Accepted bool `json:"accepted"`
	// Retry reports that the server's EncryptedExtensions carried
	// retry_configs.
	Retry bool `json:"retry,omitempty"`
	// Inner is the EncodedClientHelloInner, as the client encoded it
	// before padding, parsed: ech_outer_extensions (0xfd00) where it
	// stands, its list of compressed extension types as Data. Nil when the
	// server held no key for the config.
	Inner *profile.TLS `json:"inner,omitempty"`
	// InnerHello is the EncodedClientHelloInner as decrypted, hex: the
	// ClientHello body without its handshake header, then the padding.
	InnerHello string `json:"innerHello,omitempty"`
	// Padding is how many zero bytes followed the encoded inner hello.
	Padding int `json:"padding,omitempty"`
	// OuterExtensions are the types ech_outer_extensions copies from the
	// outer hello, in its order.
	OuterExtensions []profile.Code `json:"outerExtensions,omitempty"`
}

// ClientHelloBytes is ClientHello decoded; nil where it was not kept.
func (c *Connection) ClientHelloBytes() []byte {
	b, err := hex.DecodeString(c.ClientHello)
	if err != nil || len(b) == 0 {
		return nil
	}

	return b
}

// LoadPages reads a replayed page capture written by Write
// (<name>.<scenario>.pagetrace.json.gz): every sample's page, its QUIC
// connections rederived from their wire data (quictrace.Conn.Rederive).
func LoadPages(path string) ([]*Page, error) {
	f, err := os.Open(path)
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

// ReadTrace reads a trace file written by Write, gunzipped when its name
// ends in .gz.
func ReadTrace(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(path, ".gz") {
		return data, err
	}

	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return out, nil
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
