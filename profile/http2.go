package profile

// HTTP2 describes what a browser sends when it opens an HTTP/2 connection and
// its first request.
type HTTP2 struct {
	// Settings in the order sent in the first SETTINGS frame.
	Settings []Setting `json:"settings"`
	// WindowUpdate is the connection-level WINDOW_UPDATE increment sent after
	// SETTINGS. Zero sends none.
	WindowUpdate uint32 `json:"window_update,omitempty"`
	// Priorities are PRIORITY frames sent after SETTINGS, in order.
	Priorities []Priority `json:"priorities,omitempty"`
	// HeadersPriority is the priority carried on the request's HEADERS frame.
	// Nil sends none.
	HeadersPriority *PriorityParam `json:"headers_priority,omitempty"`
	// PseudoHeaderOrder is the order of :method, :authority, :scheme, :path.
	PseudoHeaderOrder []string `json:"pseudo_header_order"`
}

// Setting is one SETTINGS parameter.
type Setting struct {
	ID    uint16 `json:"id"`
	Value uint32 `json:"value"`
}

// Priority is a PRIORITY frame.
type Priority struct {
	StreamID uint32 `json:"stream_id"`
	PriorityParam
}

// PriorityParam is the priority information of a PRIORITY frame or a HEADERS
// frame.
type PriorityParam struct {
	// DependsOn is the stream this one depends on.
	DependsOn uint32 `json:"depends_on"`
	// Exclusive sets the exclusive flag.
	Exclusive bool `json:"exclusive"`
	// Weight as sent on the wire: the priority weight minus one, so 255 means
	// 256.
	Weight uint8 `json:"weight"`
}

// Header is one request header, in the browser's order. Names are lowercase.
// An empty Value only fixes where a header supplied by the caller goes; a
// non-empty Value is also sent by default.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// pseudoHeaders are the request pseudo-headers every HTTP/2 request carries.
var pseudoHeaders = []string{
	":method",
	":authority",
	":scheme",
	":path",
}
