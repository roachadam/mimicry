package profile

// HTTP2 describes what a browser sends on an HTTP/2 connection: how it opens
// it, its requests' frames, and how it encodes headers and returns
// flow-control credit.
type HTTP2 struct {
	// Settings in the order sent in the first SETTINGS frame.
	Settings []Setting `json:"settings"`
	// WindowUpdate is the connection-level WINDOW_UPDATE increment sent after
	// SETTINGS. Zero sends none.
	WindowUpdate uint32 `json:"windowUpdate,omitempty"`
	// Priorities are PRIORITY frames sent after SETTINGS, in order.
	Priorities []Priority `json:"priorities,omitempty"`
	// OpeningRecords is how the browser cuts a new connection's first
	// writes into TLS records when the first request is waiting as the
	// connection opens: one entry per record, each listing what it
	// carries in order, OpeningPreface and frame types (OpeningSettings,
	// OpeningWindowUpdate, OpeningPriority), through the first request's
	// OpeningHeaders and its stream WINDOW_UPDATE. A request's own frames
	// are always one record, the last; they may share the opening's last.
	// Nil: unmeasured, and every frame goes in a record of its own.
	// hellogen derives it from the captured connections' records.
	OpeningRecords [][]string `json:"openingRecords,omitempty"`
	// HeadersPriority is the priority carried on the request's HEADERS frame.
	// Nil sends none.
	HeadersPriority *PriorityParam `json:"headersPriority,omitempty"`
	// ChainPriorities makes each request depend exclusively on another
	// stream as Chromium chains them, instead of on HeadersPriority.DependsOn:
	// the parent is the newest open stream at the nearest weight level at or
	// above the request's (0 when none). Needs HeadersPriority.
	ChainPriorities bool `json:"chainPriorities,omitempty"`
	// PseudoHeaderOrder is the order of :method, :authority, :scheme, :path.
	PseudoHeaderOrder []string `json:"pseudoHeaderOrder"`
	// FirstStreamID is the first request's stream ID. Zero means 1; Firefox
	// starts at 3.
	FirstStreamID uint32 `json:"firstStreamId,omitempty"`
	// StreamWindowUpdate is a WINDOW_UPDATE increment sent on every stream
	// right after its HEADERS, as Firefox does. Zero sends none.
	StreamWindowUpdate uint32 `json:"streamWindowUpdate,omitempty"`
	// MaxDataFrame caps the payload of the DATA frames carrying a request
	// body. Zero uses the peer's SETTINGS_MAX_FRAME_SIZE; the smaller wins.
	MaxDataFrame uint32 `json:"maxDataFrame,omitempty"`
	// StreamUpdateThreshold and ConnectionUpdateThreshold are how many
	// received bytes the reader consumes before a WINDOW_UPDATE returns them,
	// per stream and for the connection. Zero means half the window.
	StreamUpdateThreshold     uint32 `json:"streamUpdateThreshold,omitempty"`
	ConnectionUpdateThreshold uint32 `json:"connectionUpdateThreshold,omitempty"`
	// WindowUpdateIntervalMillis also returns credit, however little, once
	// this long has passed since the last WINDOW_UPDATE (Chromium: 5000).
	// Zero: thresholds only.
	WindowUpdateIntervalMillis uint32 `json:"windowUpdateIntervalMs,omitempty"`
	// IdlePingMillis: a request starting after this long with nothing
	// received sends a PING after its HEADERS (Chromium: 10000); one
	// unanswered as long again closes the connection. Zero sends none.
	IdlePingMillis uint32 `json:"idlePingMs,omitempty"`
	// GoAwayReply is the error code, by its RFC 9113 name ("NO_ERROR"), of
	// the GOAWAY sent at once when the server's GOAWAY arrives, as Firefox
	// does. Empty sends none. hellogen derives it from the connections the
	// capture server sent GOAWAY on.
	GoAwayReply string `json:"goawayReply,omitempty"`
	// SplitCookie sends each cookie as its own header field (RFC 9113,
	// 8.2.3), as every captured browser does, instead of one joined field.
	SplitCookie bool `json:"splitCookie,omitempty"`
	// HPACK is how header blocks are encoded. Required.
	HPACK *HPACK `json:"hpack,omitempty"`
}

// What HTTP2.OpeningRecords lists: the connection preface, and frames by
// their RFC 9113 type names.
const (
	OpeningPreface      = "preface"
	OpeningSettings     = "SETTINGS"
	OpeningWindowUpdate = "WINDOW_UPDATE"
	OpeningPriority     = "PRIORITY"
	OpeningHeaders      = "HEADERS"
)

// errorCodes are RFC 9113's error code names (section 7), in code order.
var errorCodes = []string{
	"NO_ERROR",
	"PROTOCOL_ERROR",
	"INTERNAL_ERROR",
	"FLOW_CONTROL_ERROR",
	"SETTINGS_TIMEOUT",
	"STREAM_CLOSED",
	"FRAME_SIZE_ERROR",
	"REFUSED_STREAM",
	"CANCEL",
	"COMPRESSION_ERROR",
	"CONNECT_ERROR",
	"ENHANCE_YOUR_CALM",
	"INADEQUATE_SECURITY",
	"HTTP_1_1_REQUIRED",
}

// Opening is what a new connection sends first, in order, as
// OpeningRecords lists it: the preface, SETTINGS, the connection
// WINDOW_UPDATE and PRIORITY frames, then the first request's HEADERS and
// its stream WINDOW_UPDATE.
func (h *HTTP2) Opening() []string {
	out := []string{
		OpeningPreface,
		OpeningSettings,
	}
	if h.WindowUpdate > 0 {
		out = append(out, OpeningWindowUpdate)
	}

	for range h.Priorities {
		out = append(out, OpeningPriority)
	}

	out = append(out, OpeningHeaders)
	if h.StreamWindowUpdate > 0 {
		out = append(out, OpeningWindowUpdate)
	}

	return out
}

// HPACK describes a browser's header compression choices (RFC 7541).
// Captures cannot record it; it is derived from connection traces and checked
// by re-encoding them byte for byte.
type HPACK struct {
	// WithoutIndexing names are sent as literals without indexing and never
	// enter the dynamic table.
	WithoutIndexing []string `json:"withoutIndexing,omitempty"`
	// NeverIndexed names are sent as never-indexed literals.
	NeverIndexed []string `json:"neverIndexed,omitempty"`
	// NeverIndexBelow sends fields with these names never-indexed when their
	// value is shorter than the given number of bytes: Firefox and Safari
	// never index cookie crumbs under 20 bytes (19 never, 20 indexed).
	NeverIndexBelow map[string]int `json:"neverIndexBelow,omitempty"`
	// LiteralOnExactMatch sends a WithoutIndexing field that matches a table
	// entry exactly as a literal naming that entry (Firefox), instead of as
	// an indexed field (Chromium, Safari).
	LiteralOnExactMatch bool `json:"literalOnExactMatch,omitempty"`
	// NameMatch chooses which entry a literal's name refers to when several
	// carry it: NameMatchStaticFirst or NameMatchHighestIndex.
	NameMatch NameMatchRule `json:"nameMatch"`
	// Huffman says when strings are Huffman-coded: HuffmanShorter or
	// HuffmanAlways.
	Huffman HuffmanRule `json:"huffman"`
	// MaxTableSize caps the dynamic table the encoder announces and uses,
	// whatever the server allows (Safari: 4096). Zero: the server's size.
	MaxTableSize uint32 `json:"maxTableSize,omitempty"`
	// SizeUpdates says when a table size the server sets is signalled:
	// SizeUpdatesOnChange or SizeUpdatesAlways.
	SizeUpdates SizeUpdateRule `json:"sizeUpdates"`
}

// NameMatchRule is a value of HPACK.NameMatch.
type NameMatchRule string

// HuffmanRule is a value of HPACK.Huffman.
type HuffmanRule string

// SizeUpdateRule is a value of HPACK.SizeUpdates.
type SizeUpdateRule string

// HPACK name matching, Huffman and size update rules.
const (
	// NameMatchStaticFirst refers to the first static entry with the name,
	// else the newest dynamic one (Chromium, Safari).
	NameMatchStaticFirst NameMatchRule = "static_first"
	// NameMatchHighestIndex refers to the entry with the highest index: the
	// oldest dynamic entry before any static one (Firefox).
	NameMatchHighestIndex NameMatchRule = "highest_index"
	// HuffmanShorter codes a string only when that is strictly shorter
	// (Chromium, Safari).
	HuffmanShorter HuffmanRule = "shorter"
	// HuffmanAlways codes every string, even when longer (Firefox).
	HuffmanAlways HuffmanRule = "always"
	// SizeUpdatesOnChange signals a table size only when it changes
	// (Chromium).
	SizeUpdatesOnChange SizeUpdateRule = "on_change"
	// SizeUpdatesAlways signals every table size the server sends, even
	// the unchanged default 4096 (Firefox, Safari).
	SizeUpdatesAlways SizeUpdateRule = "always"
)

// Setting is one SETTINGS parameter.
type Setting struct {
	ID    uint16 `json:"id"`
	Value uint32 `json:"value"`
}

// Priority is a PRIORITY frame.
type Priority struct {
	StreamID uint32 `json:"streamId"`
	PriorityParam
}

// PriorityParam is the priority information of a PRIORITY frame or a HEADERS
// frame.
type PriorityParam struct {
	// DependsOn is the stream this one depends on.
	DependsOn uint32 `json:"dependsOn"`
	// Exclusive sets the exclusive flag.
	Exclusive bool `json:"exclusive"`
	// Weight as sent on the wire: the priority weight minus one, so 255 means
	// 256.
	Weight uint8 `json:"weight"`
}

// Header is one request header, in the browser's order. Names are tokens
// (RFC 9110, section 5.6.2): lowercase in HTTP/2 lists, as sent in HTTP/1.1
// and proxy lists. Values hold no CR, LF or NUL. An empty Value only fixes
// where a header supplied by the caller goes; a non-empty Value is also sent
// by default.
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

// extendedPseudoHeaders are pseudo-headers only some requests carry:
// `:protocol`, on an extended CONNECT (RFC 8441, WebSockets over HTTP/2).
// They may appear in field lists such as the HPACK policy's, but not in
// pseudoHeaderOrder, which lists those every request carries.
var extendedPseudoHeaders = []string{
	":protocol",
}
