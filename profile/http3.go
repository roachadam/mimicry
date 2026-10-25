package profile

// HTTP3 is what a browser sends at the HTTP/3 layer (RFC 9114) and when
// it moves an origin to HTTP/3: its control stream, QPACK (RFC 9204),
// priorities, request framing and header lists. harness/cmd/h3gen derives
// it from the h3 captures, Headers excepted, which harness/cmd/kindsgen
// derives; neither is hand-edited. Its requests' header lists and priority
// headers are the profile's HTTP/2 ones (the navigation's, Kinds'), as
// Headers changes them.
type HTTP3 struct {
	// Settings are the control stream's SETTINGS, in order.
	Settings []H3Setting `json:"settings"`
	// GreaseSetting is a reserved setting sent after Settings. Nil: none
	// (Firefox).
	GreaseSetting *H3Grease `json:"grease_setting,omitempty"`
	// GreaseFrame is a reserved frame sent on the control stream right
	// after SETTINGS. Nil: none (Safari).
	GreaseFrame *H3Grease `json:"grease_frame,omitempty"`
	// UniStreams are the client's unidirectional streams, in the order of
	// their stream IDs (2, 6, 10, ...). The control stream is first.
	UniStreams []H3UniStream `json:"uni_streams"`
	// StreamsWithFinished sends the streams opened at the handshake in a
	// 1-RTT packet coalesced with the client's Finished (Chrome); otherwise
	// they follow it in a datagram of their own (Firefox, Safari).
	StreamsWithFinished bool `json:"streams_with_finished,omitempty"`
	// EarlyRequestFirst: on a connection dialled for a request and
	// sending it in 0-RTT, the request's stream goes before the
	// unidirectional streams (Firefox); otherwise after them (Chrome).
	EarlyRequestFirst bool `json:"early_request_first,omitempty"`
	// RequestsAfterConfirmed: a request waiting for a new connection
	// (without 0-RTT) leaves once the handshake is confirmed, with the
	// acknowledgement of the server's HANDSHAKE_DONE, not with or just
	// after the client's Finished (Safari). Not measured for Chrome and
	// Firefox: no request of theirs was waiting for a full handshake.
	RequestsAfterConfirmed bool `json:"requests_after_confirmed,omitempty"`
	// QPACK is how field sections are encoded.
	QPACK QPACK `json:"qpack"`
	// PriorityUpdate sends, before each request that carries a priority
	// header, a PRIORITY_UPDATE frame (RFC 9218) for its stream with the
	// header's value on the control stream, in the packet that carries
	// the request (Chrome). No browser reprioritises a request later.
	PriorityUpdate bool `json:"priority_update,omitempty"`
	// MaxDataFrame caps a DATA frame carrying a request body, and
	// FirstDataFrame the first one (Firefox: 32768 and 32224, whatever the
	// HEADERS frame's length: a 64 KiB body goes as 32224, 32768 and 544
	// bytes). Zero sends the body in one DATA frame (Chrome; Safari not
	// measured).
	MaxDataFrame   int `json:"max_data_frame,omitempty"`
	FirstDataFrame int `json:"first_data_frame,omitempty"`
	// High and Low are the priority header's values (empty: none) of
	// requests with fetchpriority high and low, by kind, where only
	// HTTP/3 measured them: the kind's High and Low (Kind.High, Kind.Low)
	// were not measured over HTTP/2, so have no HTTP/2 weight. kindsgen
	// derives them, and checks every other HTTP/3 request's priority
	// header against its kind's HTTP/2 value.
	High map[string]string `json:"high,omitempty"`
	Low  map[string]string `json:"low,omitempty"`
	// Headers is how a request's header list over HTTP/3 differs from its
	// HTTP/2 list. Nil: it is the HTTP/2 list (Chrome, Safari).
	Headers *H3Headers `json:"headers,omitempty"`
	// Discovery is when the browser moves an origin to HTTP/3.
	Discovery Discovery `json:"discovery"`
}

// H3Setting is one HTTP/3 setting.
type H3Setting struct {
	ID    uint64 `json:"id"`
	Value uint64 `json:"value"`
}

// HTTP/3 settings with special handling (RFC 9204, section 5; RFC 9114,
// section 7.2.4.1).
const (
	H3SettingQPACKMaxTableCapacity = 0x01
	H3SettingMaxFieldSectionSize   = 0x06
	H3SettingQPACKBlockedStreams   = 0x07
)

// H3Grease is the shape of a reserved setting or frame: its identifier is
// 0x1f * N + 0x21 (RFC 9114, sections 7.2.4.1 and 7.2.8), N uniform below
// 2^IDBits, or with IDBits 0 over every N whose identifier fits a
// variable-length integer (Firefox's frames).
type H3Grease struct {
	IDBits int `json:"id_bits,omitempty"`
	// ValueBits: a setting's value is uniform below 2^ValueBits.
	ValueBits int `json:"value_bits,omitempty"`
	// MaxLength: a frame's payload is random bytes, its length uniform in
	// [0, MaxLength].
	MaxLength int `json:"max_length,omitempty"`
}

// H3UniStream is one unidirectional stream the client opens.
type H3UniStream struct {
	// Type is H3StreamControl, H3StreamQPACKEncoder or H3StreamQPACKDecoder.
	Type string `json:"type"`
	// Open is when it is opened: H3OpenAtHandshake, H3OpenOnFirstInsert or
	// H3OpenNever.
	Open string `json:"open"`
}

// Values of H3UniStream.Type.
const (
	H3StreamControl      = "control"
	H3StreamQPACKEncoder = "qpack_encoder"
	H3StreamQPACKDecoder = "qpack_decoder"
)

// Values of H3UniStream.Open.
const (
	// H3OpenAtHandshake opens the stream, with its type and what it has to
	// say at once (the control stream's SETTINGS; the encoder stream's
	// Set Dynamic Table Capacity when the server allows a table), in the
	// first 1-RTT (or 0-RTT) packets.
	H3OpenAtHandshake = "handshake"
	// H3OpenOnFirstInsert opens the encoder stream with the first request
	// that inserts into the dynamic table (Chrome), its Set Dynamic Table
	// Capacity first.
	H3OpenOnFirstInsert = "first_insert"
	// H3OpenNever takes the stream ID and never sends on it (Chrome's
	// decoder stream, 6: responses used no dynamic table).
	H3OpenNever = "never"
)

// QPACK is how a browser encodes field sections (RFC 9204). Without a
// dynamic table from the server, every browser sends an exact static match
// indexed, else a literal naming a static entry, else a literal.
type QPACK struct {
	// Encoder is how the dynamic table the server allows is used:
	// QPACKIndexAll or QPACKNewNames, the capacity set to the server's
	// maximum. Empty: not measured (Safari: its server offered no table).
	Encoder string `json:"encoder,omitempty"`
	// InsertsFirst sends a request's inserts on the encoder stream ahead
	// of its HEADERS, in the same packet (Chrome); otherwise the request
	// goes first and its inserts after (Firefox), so either may block the
	// server's decoding briefly within the server's blocked streams.
	InsertsFirst bool `json:"inserts_first,omitempty"`
	// Huffman is when strings are Huffman-coded: HuffmanShorter or
	// HuffmanAlways.
	Huffman HuffmanRule `json:"huffman"`
}

// Values of QPACK.Encoder.
const (
	// QPACKIndexAll sends every field as an index: an exact match of the
	// static table from it, any other from the dynamic table, inserting
	// before each request what the table lacks (naming a static or
	// dynamic entry when one has the name) (Chrome).
	QPACKIndexAll = "index_all"
	// QPACKNewNames inserts, once, each field whose name is not in the
	// static table, with the value it first has; later fields refer to
	// those entries by index when they match exactly and by name
	// otherwise, and every other field is encoded as without a table
	// (Firefox).
	QPACKNewNames = "new_names"
)

// H3Headers is how a browser's HTTP/3 header lists differ from its HTTP/2
// ones (Firefox).
type H3Headers struct {
	// PseudoHeaderOrder replaces HTTP2.PseudoHeaderOrder.
	PseudoHeaderOrder []string `json:"pseudo_header_order"`
	// Drop are headers the HTTP/2 lists carry that HTTP/3 leaves out
	// (Firefox: te).
	Drop []string `json:"drop,omitempty"`
	// AltUsed places alt-used: <host>:<port> (RFC 7838, section 5), by
	// request kind, for the navigation under AltUsedNavigation: it goes
	// before the first header the request sends whose name is the one
	// given or comes after it in the HTTP/2 list the request takes (its
	// kind's or variant's; Profile.Headers and Variants for the
	// navigation), and last if there is none. When that list lacks the
	// header given, the order of the kind's own list (Kind.Headers;
	// Profile.Headers) decides. A header no HTTP/3 request of the kind
	// sent takes the side other kinds' requests show it on; cookie, which
	// no HTTP/3 request carried, is assumed after. A kind without an
	// entry was not measured over HTTP/3.
	AltUsed map[string]string `json:"alt_used,omitempty"`
	// AltUsedNavigation is AltUsed for the navigation.
	AltUsedNavigation string `json:"alt_used_navigation,omitempty"`
}

// Discovery is when a browser moves an origin to HTTP/3 once a response
// advertised it (Alt-Svc h3), and how. Browsers never use HTTP/3 through a
// proxy. Once the origin has used HTTP/3, a request finding no QUIC
// connection to it (the last one idled out or got the server's GOAWAY)
// dials a new one, resumed with 0-RTT where QUIC.ZeroRTT, and goes on it
// at once, even with an HTTP/2 connection to the origin open (Chrome;
// Firefox after an idle timeout), unless RaceAfterGoAway says otherwise.
type Discovery struct {
	// Switch is SwitchNextRequest, SwitchOnAltSvc or SwitchNewConnection.
	Switch string `json:"switch"`
	// GoAwayTCP: once the QUIC connection is up, the browser sends GOAWAY
	// NO_ERROR on the origin's HTTP/2 connection, which takes no more
	// requests (Firefox). Otherwise that connection is left open and
	// unused (Chrome).
	GoAwayTCP bool `json:"goaway_tcp,omitempty"`
	// RaceAfterGoAway: after the server ended an HTTP/3 connection with
	// GOAWAY, the next request to its origin dials TCP first and QUIC
	// (resumed, 0-RTT) RaceDelayMillis later; the request goes on TCP, and
	// once QUIC is up the TCP connection gets a GOAWAY (Firefox).
	// Otherwise it dials QUIC alone (Chrome, Safari).
	RaceAfterGoAway bool `json:"race_after_goaway,omitempty"`
	// RaceDelayMillis is the range of the delay between the two dials.
	RaceDelayMillis *Range `json:"race_delay_ms,omitempty"`
}

// Values of Discovery.Switch.
const (
	// SwitchNextRequest (Chrome): the first request to an origin whose
	// Alt-Svc is known starts a QUIC connection and still goes on the
	// origin's HTTP/2 connection; requests made once the QUIC connection
	// is up use it.
	SwitchNextRequest = "next_request"
	// SwitchOnAltSvc (Firefox): the response that carries Alt-Svc starts
	// the QUIC connection at once, before the next request; requests made
	// once it is up use it.
	SwitchOnAltSvc = "on_alt_svc"
	// SwitchNewConnection (Safari): only a request that needs a new
	// connection to the origin uses HTTP/3; an open HTTP/2 connection
	// keeps the origin's requests whatever Alt-Svc says.
	SwitchNewConnection = "new_connection"
)
