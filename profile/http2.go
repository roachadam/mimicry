package profile

// HTTP2 describes what a browser sends on an HTTP/2 connection: how it opens
// it, its requests' frames, and how it encodes headers and returns
// flow-control credit.
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
	// ChainPriorities makes each request depend, exclusively, on the most
	// recently opened stream that is still open, as Chromium does, instead of
	// on HeadersPriority.DependsOn. Needs HeadersPriority.
	ChainPriorities bool `json:"chain_priorities,omitempty"`
	// PseudoHeaderOrder is the order of :method, :authority, :scheme, :path.
	PseudoHeaderOrder []string `json:"pseudo_header_order"`
	// FirstStreamID is the first request's stream ID. Zero means 1; Firefox
	// starts at 3.
	FirstStreamID uint32 `json:"first_stream_id,omitempty"`
	// StreamWindowUpdate is a WINDOW_UPDATE increment sent on every stream
	// right after its HEADERS, as Firefox does. Zero sends none.
	StreamWindowUpdate uint32 `json:"stream_window_update,omitempty"`
	// MaxDataFrame caps the payload of the DATA frames carrying a request
	// body. Zero uses the peer's SETTINGS_MAX_FRAME_SIZE; the smaller wins.
	MaxDataFrame uint32 `json:"max_data_frame,omitempty"`
	// StreamUpdateThreshold and ConnectionUpdateThreshold are how many
	// received bytes the reader consumes before a WINDOW_UPDATE returns them,
	// per stream and for the connection. Zero means half the window.
	StreamUpdateThreshold     uint32 `json:"stream_update_threshold,omitempty"`
	ConnectionUpdateThreshold uint32 `json:"connection_update_threshold,omitempty"`
	// SplitCookie sends each cookie as its own header field (RFC 9113,
	// 8.2.3), as every captured browser does, instead of one joined field.
	SplitCookie bool `json:"split_cookie,omitempty"`
	// HPACK is how header blocks are encoded. Nil encodes as Chromium does.
	HPACK *HPACK `json:"hpack,omitempty"`
}

// HPACK describes a browser's header compression choices (RFC 7541).
// Captures cannot record it; it is derived from connection traces and checked
// by re-encoding them byte for byte.
type HPACK struct {
	// WithoutIndexing names are sent as literals without indexing and never
	// enter the dynamic table.
	WithoutIndexing []string `json:"without_indexing,omitempty"`
	// NeverIndexed names are sent as never-indexed literals.
	NeverIndexed []string `json:"never_indexed,omitempty"`
	// LiteralOnExactMatch sends a WithoutIndexing field that matches a table
	// entry exactly as a literal naming that entry (Firefox), instead of as
	// an indexed field (Chromium, Safari).
	LiteralOnExactMatch bool `json:"literal_on_exact_match,omitempty"`
	// NameMatch chooses which entry a literal's name refers to when several
	// carry it: NameMatchStaticFirst or NameMatchHighestIndex.
	NameMatch string `json:"name_match"`
	// Huffman says when strings are Huffman-coded: HuffmanShorter or
	// HuffmanAlways.
	Huffman string `json:"huffman"`
}

// HPACK name matching and Huffman rules.
const (
	// NameMatchStaticFirst refers to the first static entry with the name,
	// else the newest dynamic one (Chromium, Safari).
	NameMatchStaticFirst = "static_first"
	// NameMatchHighestIndex refers to the entry with the highest index: the
	// oldest dynamic entry before any static one (Firefox).
	NameMatchHighestIndex = "highest_index"
	// HuffmanShorter codes a string only when that is strictly shorter
	// (Chromium, Safari).
	HuffmanShorter = "shorter"
	// HuffmanAlways codes every string, even when longer (Firefox).
	HuffmanAlways = "always"
)

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
