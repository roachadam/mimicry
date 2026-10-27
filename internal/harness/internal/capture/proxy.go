package capture

// ProxyRequest is how a client asked its proxy for one connection.
type ProxyRequest struct {
	// Kind is ProxyConnect or ProxySOCKS5.
	Kind string `json:"kind"`
	// Conn numbers the client's connection to the proxy, from 1: a
	// CONNECT retried after a 407 on the same connection shares it.
	Conn int `json:"conn"`
	// Status is the proxy's answer to a CONNECT (200, 407, 502).
	Status int `json:"status,omitempty"`
	// Target is the host:port asked for.
	Target string `json:"target"`
	// Lines are a CONNECT request's request line and header lines,
	// verbatim.
	Lines []string `json:"lines,omitempty"`
	// Greeting and Request are SOCKS5's method selection and request
	// messages, hex encoded.
	Greeting string `json:"greeting,omitempty"`
	Request  string `json:"request,omitempty"`
}

// Values of ProxyRequest.Kind.
const (
	ProxyConnect = "connect"
	ProxySOCKS5  = "socks5"
)
