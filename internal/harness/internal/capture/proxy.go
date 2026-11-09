package capture

import (
	"encoding/json"
	"fmt"

	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

// LoadProxyRequests reads a proxy scenario's trace written by Write
// (<name>.<scenario>.proxytrace.json): every sample's requests, in order.
func LoadProxyRequests(path string) ([][]ProxyRequest, error) {
	return loadSamples[ProxyRequest](path)
}

// LoadProxyTLS reads an https:// proxy scenario's connections to the
// proxy written by Write (<name>.<scenario>.proxytls.json.gz): every
// sample's, in the order their handshakes ended.
func LoadProxyTLS(path string) ([][]ProxyTLS, error) {
	return loadSamples[ProxyTLS](path)
}

// loadSamples reads a JSON array of samples, gzipped if path says so.
func loadSamples[T any](path string) ([][]T, error) {
	data, err := ReadTrace(path)
	if err != nil {
		return nil, err
	}

	var samples [][]T
	if err := json.Unmarshal(data, &samples); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return samples, nil
}

// ProxyRequest is how a client asked its proxy for one connection.
type ProxyRequest struct {
	// Kind is ProxyConnect or ProxySOCKS5.
	Kind string `json:"kind"`
	// Conn numbers the client's connection to the proxy, from 1: a
	// CONNECT retried after a 407 on the same connection shares it.
	Conn int `json:"conn"`
	// Stream is the HTTP/2 stream of a CONNECT sent to a TLS proxy over
	// HTTP/2 (ProxyTLS.Protocol "h2"); 0 over HTTP/1.1.
	Stream uint32 `json:"stream,omitempty"`
	// Status is the proxy's answer to a CONNECT (200, 407, 502).
	Status int `json:"status,omitempty"`
	// Target is the host:port asked for.
	Target string `json:"target"`
	// Lines are a CONNECT request's request line and header lines,
	// verbatim; over HTTP/2, its decoded header fields in order as
	// "name: value", pseudo-header fields included (the encoding is in
	// the connection's ProxyTLS.H2).
	Lines []string `json:"lines,omitempty"`
	// Greeting and Request are SOCKS5's method selection and request
	// messages, hex encoded.
	Greeting string `json:"greeting,omitempty"`
	Request  string `json:"request,omitempty"`
	// ClientHello is, for a tunnel through a TLS proxy, the TLS records
	// carrying the first ClientHello the client sent through it (to the
	// site), hex, as a .clienthello line.
	ClientHello string `json:"clientHello,omitempty"`
	// Driver is how the capture tool sent Safari on an iPhone to the
	// page (browser.IOSDriver); empty for other browsers.
	Driver string `json:"driver,omitempty"`
}

// Values of ProxyRequest.Kind.
const (
	ProxyConnect = "connect"
	ProxySOCKS5  = "socks5"
)

// ProxyTLS is one client connection to a TLS (https://) proxy, written by
// Write (<name>.<scenario>.proxytls.json.gz): its handshake as a page's
// Connection records one (Host is the server name sent, Protocol the
// ALPN protocol the proxy picked, Resumed and Accepted the session
// offered and taken, Requests the CONNECTs to capture hosts it carried;
// ClientHello is empty, Records holds it), and what the client sent over
// it. Every connection is kept, those carrying only the browser's own
// traffic (refused, and not in the proxy trace) included.
type ProxyTLS struct {
	// Conn is the connection's ProxyRequest.Conn.
	Conn int `json:"conn"`
	Connection
	// Records are the TLS records that carried the ClientHello, hex, as
	// a .clienthello line.
	Records string `json:"records"`
	// Failed says why the handshake did not complete; empty when it did.
	Failed string `json:"failed,omitempty"`
	// H2 is every frame the client sent over HTTP/2 (Protocol "h2"),
	// with each CONNECT's header block, and Settings its first SETTINGS
	// frame's settings in order; nil over HTTP/1.1.
	H2       *h2trace.Trace    `json:"h2,omitempty"`
	Settings []profile.Setting `json:"settings,omitempty"`
}
