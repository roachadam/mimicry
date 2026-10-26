package profile

import (
	"fmt"
	"strconv"
	"strings"
)

// QUIC is how a browser speaks QUIC version 1 (RFC 9000) for HTTP/3: how
// it builds and pads its Initial packets and cuts its ClientHello
// (TLS.QUIC) into them, its connection IDs and packet numbers, its
// transport parameters, how it acknowledges, recovers loss, returns
// credit and answers Version Negotiation, and how it resumes and ends a
// connection. harness/cmd/h3gen derives it from the h3 captures and
// checks every value against every sample; it is never hand-edited.
//
// Not measured, for every browser: credit growth beyond 50 MiB or at
// lower bandwidth, MAX_STREAMS, key updates beyond 37,000 received
// packets, path MTU probing, ECN, migration by Firefox or Safari (none
// seen), DATAGRAM frames and Retry from the browser's view.
type QUIC struct {
	// Version is the QUIC version of every packet: 1.
	Version uint32 `json:"version"`
	// DatagramSize is the length every datagram carrying an Initial packet
	// is padded to, and the longest datagram the browser sends (Chrome
	// 1250, Firefox 1252, Safari 1200).
	DatagramSize int `json:"datagram_size"`
	// Padding is where a datagram carrying an Initial packet is padded:
	// PaddingScattered, PaddingTrailing or PaddingEnd.
	Padding string `json:"padding"`
	// Crypto is how the ClientHello is cut into CRYPTO frames and Initial
	// packets.
	Crypto QUICCrypto `json:"crypto"`
	// PacketNumbers is how packet numbers are assigned:
	// PacketNumbersShared, PacketNumbersRandomInitial or
	// PacketNumbersPerSpace.
	PacketNumbers string `json:"packet_numbers"`
	// DCIDLength is the length of the random Destination Connection ID of
	// the first Initial packet. With DCIDLengthRule DCIDLengthBiased it is
	// the shortest of the lengths that rule picks.
	DCIDLength int `json:"dcid_length"`
	// DCIDLengthRule is how DCIDLength is picked per connection: empty is
	// always DCIDLength; DCIDLengthBiased.
	DCIDLengthRule string `json:"dcid_length_rule,omitempty"`
	// SCIDLength is the length of the client's own connection IDs: its
	// Source Connection ID and those it issues (Chrome, Safari: 0, so they
	// issue none).
	SCIDLength int `json:"scid_length"`
	// NewConnectionIDs is how many connection IDs the client issues with
	// NEW_CONNECTION_ID frames (sequence numbers from 1, retire_prior_to
	// 0) on a connection to a server whose active_connection_id_limit is
	// 4, the captured server's (Firefox: 4, three with its Finished and
	// one once the handshake is done; a resumed connection sends the first
	// in 0-RTT). Zero with SCIDLength 0.
	NewConnectionIDs int `json:"new_connection_ids,omitempty"`
	// TransportParameters are the quic_transport_parameters the
	// ClientHello carries (RFC 9000, section 18), in the order of one
	// observed connection's, permuted as TransportParameterOrder says.
	TransportParameters []TransportParameter `json:"transport_parameters"`
	// TransportParameterOrder is TransportParameterOrderFixed,
	// TransportParameterOrderShuffled or TransportParameterOrderRotated.
	TransportParameterOrder string `json:"transport_parameter_order"`
	// AckInitial: the client acknowledges the server's Initial packets
	// (Firefox, Safari). Chrome does not: it drops its Initial keys when
	// it sends its first Handshake packet, which carries its first
	// acknowledgement.
	AckInitial bool `json:"ack_initial,omitempty"`
	// AckBeforeFinished: the client acknowledges the server's first flight
	// in datagrams of their own before the one carrying its Finished
	// (Firefox: the Initial and Handshake ACKs coalesced; Safari: one
	// datagram each). Otherwise its Handshake ACK goes with its Finished
	// (Chrome).
	AckBeforeFinished bool `json:"ack_before_finished,omitempty"`
	// Coalesce: packets of different packet number spaces that are ready
	// together share a datagram (RFC 9000, section 12.2): Chrome's
	// Handshake Finished with its first 1-RTT packet, Firefox's Initial and
	// Handshake ACKs, its Finished with 1-RTT, its last Initial with 0-RTT.
	// Otherwise every packet is a datagram of its own (Safari).
	Coalesce bool `json:"coalesce,omitempty"`
	// AckDelay is what an ACK frame's ACK Delay field says: AckDelayActual
	// or AckDelayConstant. Delayed acknowledgements wait at most the
	// max_ack_delay transport parameter (default 25 ms).
	AckDelay string `json:"ack_delay"`
	// AckDelayConstant is the range a connection's constant ACK Delay
	// field is drawn from, as sent (before ack_delay_exponent scales it;
	// every browser uses the default exponent 3). With AckDelayConstant.
	AckDelayConstant *Range `json:"ack_delay_constant,omitempty"`
	// AckOnlyLimit: after this many consecutive ACK-only 1-RTT packets the
	// next one is made ack-eliciting with an AckOnlyFrame (Chrome: 19,
	// then ACK and MAX_DATA with its current limit, quiche's
	// OnAckNeedsRetransmittableFrame). Zero: ACK-only packets stay so
	// (Firefox: runs of 24; Safari: no run that long was captured).
	AckOnlyLimit int    `json:"ack_only_limit,omitempty"`
	AckOnlyFrame string `json:"ack_only_frame,omitempty"`
	// SpinBit is how the latency spin bit of 1-RTT packets is set:
	// SpinBitZero, SpinBitRandom or SpinBitSpin.
	SpinBit string `json:"spin_bit"`
	// Tokens: a new connection to a server carries the token of the last
	// NEW_TOKEN frame the server sent (Chrome, Firefox; Chrome left it out
	// on some reconnections after an idle timeout, a known gap).
	Tokens bool `json:"tokens,omitempty"`
	// ZeroRTT: a resumed connection (TLS.Resumption) sends its first
	// requests in 0-RTT packets, its hello offering early_data. False
	// with TLS.Resumption: not measured.
	ZeroRTT bool `json:"zero_rtt,omitempty"`
	// CloseReply: the client answers the server's CONNECTION_CLOSE with a
	// transport CONNECTION_CLOSE of its own, NO_ERROR, frame type 0x1d,
	// with an ACK in the same packet (Firefox). Otherwise it sends nothing
	// (Chrome; Safari not measured). A connection the browser stops using
	// ends silently by the idle timeout (max_idle_timeout).
	CloseReply bool `json:"close_reply,omitempty"`
	// Loss is how the client probes an unanswered handshake, when it
	// gives up, and what it does when a live connection's path stops
	// answering (h3-blackhole, h3-broken).
	Loss QUICLoss `json:"loss"`
	// KeepAlive is the PING the client sends while a request waits on a
	// connection that has gone quiet (h3-broken).
	KeepAlive QUICKeepAlive `json:"keep_alive"`
	// Ack is how often the client acknowledges a bulk receive
	// (h3-download). No browser sends ACK_FREQUENCY or IMMEDIATE_ACK.
	Ack QUICAck `json:"ack"`
	// Credit is how the client returns flow-control credit while it
	// receives (h3-download).
	Credit QUICCredit `json:"credit"`
	// KeyUpdate is how the client takes part in 1-RTT key updates (RFC
	// 9001, section 6; h3-download).
	KeyUpdate QUICKeyUpdate `json:"key_update"`
	// VersionNegotiation is what a Version Negotiation packet makes the
	// client do (h3-vn).
	VersionNegotiation QUICVersionNegotiation `json:"version_negotiation"`
}

// QUICLoss is how a client recovers from loss where it differs from RFC
// 9002's defaults: the handshake's probe timeouts (PTOs), the probes
// themselves, the handshake's give-up, and path degrading. A probe
// timeout doubles after each probe that goes unanswered, for every
// browser.
type QUICLoss struct {
	// InitialPTOMillis is the first probe timeout of a connection with
	// no RTT sample (Chrome, Firefox: 300; probes at +300, +900, +2100
	// ms...). Zero: not measured (Safari: TCP won every race before its
	// first probe was due).
	InitialPTOMillis int `json:"initial_pto_ms,omitempty"`
	// CachedRTT: a resumed connection starts from the RTT measured on
	// the connection whose ticket it resumes (RFC 9002, section 6.2.2);
	// a fresh one from InitialPTOMillis even to a server it has an RTT
	// for (Chrome, Firefox).
	CachedRTT bool `json:"cached_rtt,omitempty"`
	// PTOAckDelay counts the server's max_ack_delay in the probe
	// timeout before the handshake completes too, where RFC 9002 counts
	// it only in 1-RTT (Chrome: a resumed attempt's first probe after
	// 30–33 ms on a ~1 ms RTT; Firefox: 3 ms).
	PTOAckDelay bool `json:"pto_ack_delay,omitempty"`
	// Probe is what a handshake probe sends: ProbeFlight or
	// ProbeOnePacket. Empty: not measured (Safari).
	Probe string `json:"probe,omitempty"`
	// SkipPacketNumber skips one packet number before every probe, in
	// the handshake and in 1-RTT (Chrome: Initial packets 1, 2, 4, 6, 8),
	// but not before a close.
	SkipPacketNumber bool `json:"skip_packet_number,omitempty"`
	// Fresh and Resumed are when a handshake without and with a resumed
	// ticket gives up, and how it ends.
	Fresh   QUICGiveUp `json:"fresh"`
	Resumed QUICGiveUp `json:"resumed"`
	// AbandonClose is how the client ends an attempt it drops itself
	// because TCP won the race (HTTP3.Discovery.Fallback.AbandonOnTCP):
	// CloseZeroRTT, or empty for nothing (Safari, fresh attempts).
	AbandonClose string `json:"abandon_close,omitempty"`
	// PathDegrading is when the client decides a live connection's path
	// has stopped answering, and what it does. Nil: it never does; the
	// connection ends at its idle timeout, still probing and taking new
	// requests (Firefox).
	PathDegrading *QUICPathDegrading `json:"path_degrading,omitempty"`
}

// Values of QUICLoss.Probe.
const (
	// ProbeFlight sends every Initial packet of the first flight again,
	// with its CRYPTO frames as they were and new packet numbers
	// (Firefox).
	ProbeFlight = "flight"
	// ProbeOnePacket sends one Initial packet, padded to DatagramSize,
	// carrying the CRYPTO data of the first flight's packets in turn (the
	// first's, then the second's, ...), cut afresh as Crypto.Split says
	// (Chrome).
	ProbeOnePacket = "one_packet"
)

// QUICGiveUp is when an unanswered handshake ends and what the client
// sends then.
type QUICGiveUp struct {
	// AfterMillis ends the attempt this long after it started if the
	// handshake has not completed (Chrome: 4000, quiche's idle timeout
	// before the handshake completes, fresh and resumed; Firefox
	// resumed: 5000, necko's). Zero: it lasts until max_idle_timeout
	// (Firefox fresh: probes up to +19.0–19.8 s, none at the ~39 s one
	// would be due) or is not measured (Safari: TCP always won first).
	AfterMillis int `json:"after_ms,omitempty"`
	// Close is what the client sends at AfterMillis: CloseTransport,
	// CloseApplication, or empty for nothing (Firefox fresh).
	Close string `json:"close,omitempty"`
	// Reason is CloseTransport's reason phrase, "{elapsed_us}" replaced
	// by the microseconds since the attempt started (Chrome: quiche's
	// "25:No recent network activity after {elapsed_us}us. Timeout:4s
	// num_undecryptable_packets: 0 {}").
	Reason string `json:"reason,omitempty"`
	// DatagramSize is the size the close's datagram is padded to: an
	// Initial packet padded inside (Chrome: 1250) or, with Close
	// CloseApplication, trailing zeros after its packets (Firefox: 255).
	DatagramSize int `json:"datagram_size,omitempty"`
}

// Values of QUICGiveUp.Close and QUICLoss.AbandonClose.
const (
	// CloseTransport is CONNECTION_CLOSE 0x1c NO_ERROR, frame type 0,
	// with a reason, in an Initial packet, and in a 0-RTT packet
	// coalesced after it if the attempt sent 0-RTT (Chrome).
	CloseTransport = "transport"
	// CloseApplication is an application close in an Initial packet,
	// which RFC 9000 (section 10.2.3) turns into CONNECTION_CLOSE 0x1c
	// APPLICATION_ERROR (0x0c) without a reason, followed by a 0-RTT
	// packet with the 0x1d close if the attempt sent 0-RTT (Firefox).
	CloseApplication = "application"
	// CloseZeroRTT is an application CONNECTION_CLOSE (0x1d,
	// H3_NO_ERROR) in one 0-RTT packet of its own, if the attempt sent
	// 0-RTT; nothing otherwise (Safari).
	CloseZeroRTT = "zero_rtt"
)

// QUICPathDegrading is a client's path-degrading rule. Exactly one of
// AfterPTOs and AfterMillis is set.
type QUICPathDegrading struct {
	// AfterPTOs: the path is degrading once the oldest unacknowledged
	// ack-eliciting packet is this many probe timeouts old, counted
	// without back-off (Chrome: 4, +115–131 ms on its ~30 ms PTO).
	AfterPTOs int `json:"after_ptos,omitempty"`
	// AfterMillis: the path is dead once no packet has arrived for this
	// long while a request waits (Safari: 12000, its keep-alive limit).
	AfterMillis int `json:"after_ms,omitempty"`
	// Action is PathMigrate or PathClose.
	Action string `json:"action"`
	// CloseReason is PathClose's CONNECTION_CLOSE reason phrase (Safari:
	// "keepalive limit reached").
	CloseReason string `json:"close_reason,omitempty"`
}

// Values of QUICPathDegrading.Action.
const (
	// PathMigrate moves the connection to a new UDP socket (a new local
	// port) and the server's next connection ID: a PATH_CHALLENGE padded
	// to DatagramSize, then RETIRE_CONNECTION_ID for the old ID, the
	// unacknowledged data again, and every later packet on the new path
	// (Chrome). Its PATH_RESPONSE is padded to DatagramSize too.
	PathMigrate = "migrate"
	// PathClose sends CONNECTION_CLOSE (0x1c, NO_ERROR, CloseReason) and
	// moves the pending requests to a new connection
	// (HTTP3.Discovery.Fallback.Pending) (Safari).
	PathClose = "close"
)

// QUICKeepAlive is the PING a client sends while a request waits for its
// response and nothing arrives: PeriodMillis after the moment From names,
// then every PeriodMillis. An idle connection sends none.
type QUICKeepAlive struct {
	// PeriodMillis: Chrome, Firefox 15000 (half their 30 s idle
	// timeout); Safari 2000.
	PeriodMillis int `json:"period_ms"`
	// From is KeepAliveFromSent, KeepAliveFromIdle or
	// KeepAliveFromReceived.
	From string `json:"from"`
}

// Values of QUICKeepAlive.From.
const (
	// KeepAliveFromSent counts from the last ack-eliciting packet the
	// client sent (Chrome: quiche's ping alarm).
	KeepAliveFromSent = "sent"
	// KeepAliveFromIdle counts from when the idle timer last restarted
	// (RFC 9000, section 10.1): the first ack-eliciting packet sent after
	// the last one received (Firefox).
	KeepAliveFromIdle = "idle"
	// KeepAliveFromReceived counts from the last packet received
	// (Safari).
	KeepAliveFromReceived = "received"
)

// QUICAck is how often a client acknowledges during a bulk receive. The
// ACK delay and ACK-only rules are QUIC's AckDelay and AckOnlyLimit.
type QUICAck struct {
	// Every: the client acknowledges every Every-th ack-eliciting packet
	// (Chrome 10, quiche's ack decimation; Firefox, Safari 2), or when
	// max_ack_delay expires.
	Every int `json:"every"`
	// WarmupPackets: until this many packets have arrived, it
	// acknowledges every WarmupEvery-th instead (Chrome: 100, every 2nd).
	WarmupPackets int `json:"warmup_packets,omitempty"`
	WarmupEvery   int `json:"warmup_every,omitempty"`
	// Batched: the acknowledgement waits until the packets that have
	// arrived are all read, so on a burst one ACK covers Every or more
	// (Firefox: 2–7, most often 2–5; Safari 27 most often 3). Otherwise
	// it covers exactly Every (Chrome: 97% of its ACKs).
	Batched bool `json:"batched,omitempty"`
}

// QUICCredit is how a client returns flow-control credit. Its windows are
// its initial transport parameters (initial_max_stream_data_bidi_local,
// initial_max_data) and never grow: an update sets the limit to the bytes
// received plus the window.
type QUICCredit struct {
	// StreamDivisor: MAX_STREAM_DATA once the bytes received on the
	// stream since its last update reach window / StreamDivisor (Chrome
	// 2, Firefox 4; Safari about 2: it updates as its application
	// reads, at irregular points, 62 B to 2.1 MB apart, median 0.9–1.1
	// MB).
	StreamDivisor int `json:"stream_divisor"`
	// ConnectionDivisor: MAX_DATA likewise for the connection (Chrome 2,
	// Firefox 4). Zero with ConnectionStreamWindow.
	ConnectionDivisor int `json:"connection_divisor,omitempty"`
	// ConnectionStreamWindow: the connection's limit is the bytes
	// received on it plus the stream window, sent once that passes the
	// initial limit and then with every MAX_STREAM_DATA (Safari: the
	// first at 15.2–15.9 MB received; Safari 27 sends a quarter to a third
	// of its raises in other packets, some from older counts).
	ConnectionStreamWindow bool `json:"connection_stream_window,omitempty"`
}

// QUICKeyUpdate is how a client takes part in key updates.
type QUICKeyUpdate struct {
	// Initiate: the client starts key updates of its own. No browser
	// does: each only follows the server's, switching its own keys in
	// its next packet (none in 3,476–8,619 packets sent and 37,000
	// received).
	Initiate bool `json:"initiate"`
}

// QUICVersionNegotiation is what a Version Negotiation packet makes a
// client do. Without Retry the attempt fails at once, as an unanswered
// one would (HTTP3.Discovery.Fallback), and no other version is tried
// (Chrome, Safari).
type QUICVersionNegotiation struct {
	// Retry: when the packet lists a version of version_information's
	// available ones other than the one used, the client starts a new
	// connection in it at once (Firefox: version 2, 1 ms later), its
	// version_information choosing that version.
	Retry bool `json:"retry"`
	// DowngradeError: the retried connection closes with
	// CONNECTION_CLOSE (0x1c) and this error code, in its Handshake and
	// 1-RTT packets, once the server's transport parameters arrive
	// without version_information (RFC 9368's downgrade check; Firefox:
	// 0x53f8, the draft's VERSION_NEGOTIATION_ERROR, where RFC 9368 has
	// 0x11). Zero with Retry: no check.
	DowngradeError QUICErrorCode `json:"downgrade_error,omitempty"`
}

// QUICErrorCode is a QUIC transport error code. In JSON it is a hex
// string such as "0x53f8".
type QUICErrorCode uint64

// String returns the code in hex.
func (c QUICErrorCode) String() string {
	return "0x" + strconv.FormatUint(uint64(c), 16)
}

// MarshalText implements encoding.TextMarshaler.
func (c QUICErrorCode) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

// UnmarshalText accepts "0x" followed by hex digits.
func (c *QUICErrorCode) UnmarshalText(text []byte) error {
	s, ok := strings.CutPrefix(string(text), "0x")
	if !ok || s == "" {
		return fmt.Errorf("QUIC error code %q: want 0x and hex digits", text)
	}
	v, err := strconv.ParseUint(s, 16, 62)
	if err != nil {
		return fmt.Errorf("QUIC error code %q: %w", text, err)
	}
	*c = QUICErrorCode(v)
	return nil
}

// QUICCrypto is how the ClientHello is cut into CRYPTO frames across the
// client's Initial packets.
type QUICCrypto struct {
	// Split is CryptoSplitShuffle, CryptoSplitSNICut or CryptoSplitOrdered.
	Split string `json:"split"`
	// FirstPacket is how many ClientHello bytes the first Initial packet
	// carries with CryptoSplitOrdered (Safari 999).
	FirstPacket int `json:"first_packet,omitempty"`
	// Head is the range of the length of the ClientHello's head that
	// stays in the first packet with CryptoSplitShuffle.
	Head *Range `json:"head,omitempty"`
	// AddedFrames is the range of how many times a packet's CRYPTO data is
	// split further with CryptoSplitShuffle, while padding lasts.
	AddedFrames *Range `json:"added_frames,omitempty"`
	// Pings is the range of how many PING frames a packet gets with
	// CryptoSplitShuffle, while padding lasts.
	Pings *Range `json:"pings,omitempty"`
}

// Range is an inclusive range of integers.
type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Contains reports whether v is in r.
func (r Range) Contains(v int) bool {
	return v >= r.Min && v <= r.Max
}

// Values of QUIC.Padding.
const (
	// PaddingScattered pads with PADDING frames spread between the
	// packet's other frames (Chrome; see CryptoSplitShuffle).
	PaddingScattered = "scattered"
	// PaddingTrailing pads the datagram, not the packet: zero bytes after
	// its last packet. A datagram that also carries a 1-RTT packet pads
	// that packet with PADDING frames instead (Firefox).
	PaddingTrailing = "trailing"
	// PaddingEnd pads with one run of PADDING frames ending the packet
	// (Safari).
	PaddingEnd = "end"
)

// Values of QUICCrypto.Split.
const (
	// CryptoSplitShuffle (Chrome): two Initial packets; the first carries
	// the ClientHello's head [0, h), h in Head, and its tail, the second the
	// middle, so that the first holds floor((len-4)/2) bytes. In each
	// packet the CRYPTO data is split AddedFrames times at random points,
	// Pings PING frames are added, the padding is spread in random runs
	// between the frames, and the frames are shuffled (quiche's
	// QuicChaosProtector); with no padding left, no splits or PINGs are
	// added.
	CryptoSplitShuffle = "shuffle"
	// CryptoSplitSNICut (Firefox): the ClientHello is cut in two at the
	// middle of its server_name host (6 bytes into the 13-byte names
	// captured). With n = ceil(len/DatagramSize) packets and limit = len/n, the
	// longer piece is shortened to make the two fit limit (the head piece
	// keeps its start, the tail piece its end); the first packet carries
	// the tail piece as planned, then as much of the head piece as fits
	// after it (a resumed hello's token leaves less room than limit), and
	// the rest follows in order in as many packets as needed (neqo's SNI
	// slicing).
	CryptoSplitSNICut = "sni_cut"
	// CryptoSplitOrdered (Safari): one CRYPTO frame per packet, in order,
	// FirstPacket bytes in the first.
	CryptoSplitOrdered = "ordered"
)

// Values of QUIC.PacketNumbers.
const (
	// PacketNumbersShared numbers every packet from one counter starting
	// at 1, across the Initial, 0-RTT, Handshake and 1-RTT spaces
	// (Chrome). The second Initial packet's number is encoded in 2 bytes
	// when the connection carries no token, otherwise in 1.
	PacketNumbersShared = "shared"
	// PacketNumbersRandomInitial numbers each space from 0, except the
	// Initial space, which starts at 1 + (a & 0x1f) + (max(b, 224) - 224) *
	// 32 for two random bytes a and b: mostly below 33, one time in eight
	// up to 1024 (Firefox; neqo). 1-RTT starts at 1 after 0-RTT, which
	// shares its space.
	PacketNumbersRandomInitial = "random_initial"
	// PacketNumbersPerSpace numbers each space from 0 (Safari).
	PacketNumbersPerSpace = "per_space"
)

// DCIDLengthBiased picks max(8, 5 + (v & (v >> 4))) for a random byte v:
// 8 more than half the time, up to 20 (Firefox; neqo).
const DCIDLengthBiased = "biased"

// AckOnlyMaxData is the frame Chrome adds to an ACK-only packet after
// QUIC.AckOnlyLimit of them: MAX_DATA with the connection's current limit
// (a value of QUIC.AckOnlyFrame).
const AckOnlyMaxData = "MAX_DATA"

// Values of QUIC.TransportParameterOrder.
const (
	// TransportParameterOrderFixed sends TransportParameters in order
	// (Firefox).
	TransportParameterOrderFixed = "fixed"
	// TransportParameterOrderShuffled sends them in a uniform random order
	// per connection (Chrome).
	TransportParameterOrderShuffled = "shuffled"
	// TransportParameterOrderRotated sends a rotation of them, starting at
	// a random one, with those marked Last kept last in order (Safari: the
	// captures show rotations of one cycle; three samples per version
	// cannot tell a rotation from another permutation).
	TransportParameterOrderRotated = "rotated"
)

// Values of QUIC.AckDelay.
const (
	// AckDelayActual reports the time since the largest acknowledged
	// packet arrived (Chrome, Firefox).
	AckDelayActual = "actual"
	// AckDelayConstant reports one value per connection, drawn from
	// AckDelayConstant, whatever the actual delay (Safari).
	AckDelayConstant = "constant"
)

// Values of QUIC.SpinBit.
const (
	// SpinBitZero always sends 0 (Chrome).
	SpinBitZero = "zero"
	// SpinBitRandom sends a random value per packet (Firefox: RFC 9000,
	// section 17.4, for an endpoint that does not spin).
	SpinBitRandom = "random"
	// SpinBitSpin spins: 0 until the handshake is confirmed, then the
	// inverse of the spin bit of the server's largest-numbered 1-RTT
	// packet since (the one carrying HANDSHAKE_DONE included) (Safari: its
	// first 1-RTT packet sends 0 although the server's 0.5-RTT data has
	// arrived).
	SpinBitSpin = "spin"
)

// TransportParameter is one QUIC transport parameter. Its value is given
// in one of these ways, by ID:
//
//   - initial_source_connection_id: none; it is the client's Source
//     Connection ID (SCIDLength bytes).
//   - initial_rtt (0x3127): none; Resumed, and the RTT measured on the
//     connection whose ticket is resumed, in microseconds (Chrome).
//   - version_information (0x11): Versions.
//   - GREASE: MaxLength.
//   - Value for an integer parameter, Data for any other (absent: empty).
type TransportParameter struct {
	ID TransportParameterID `json:"id"`
	// Value is a parameter's value that is one variable-length integer.
	Value *uint64 `json:"value,omitempty"`
	// Data is the exact value of a parameter that is not an integer.
	Data Bytes `json:"data,omitempty"`
	// Versions of version_information (RFC 9368): the chosen version,
	// then the available ones. GREASEVersion stands for a reserved
	// version 0x?a?a?a?a, random per connection.
	Versions []QUICVersion `json:"versions,omitempty"`
	// ShuffleVersions sends the available versions in a random order per
	// connection (Chrome).
	ShuffleVersions bool `json:"shuffle_versions,omitempty"`
	// MaxLength is the longest random value of a GREASE parameter, whose
	// length is uniform in [0, MaxLength] (Chrome: 15).
	MaxLength int `json:"max_length,omitempty"`
	// Resumed sends the parameter only on a resumed connection.
	Resumed bool `json:"resumed,omitempty"`
	// Last keeps the parameter last, in order, whatever
	// TransportParameterOrder permutes (Safari's 0xff080808).
	Last bool `json:"last,omitempty"`
}

// Transport parameter IDs with special handling in profiles.
const (
	TPMaxIdleTimeout                TransportParameterID = 0x01
	TPMaxUDPPayloadSize             TransportParameterID = 0x03
	TPInitialMaxData                TransportParameterID = 0x04
	TPInitialMaxStreamDataBidiLocal TransportParameterID = 0x05
	TPInitialMaxStreamDataBidiRem   TransportParameterID = 0x06
	TPInitialMaxStreamDataUni       TransportParameterID = 0x07
	TPInitialMaxStreamsBidi         TransportParameterID = 0x08
	TPInitialMaxStreamsUni          TransportParameterID = 0x09
	TPAckDelayExponent              TransportParameterID = 0x0a
	TPMaxAckDelay                   TransportParameterID = 0x0b
	TPActiveConnectionIDLimit       TransportParameterID = 0x0e
	TPInitialSourceConnectionID     TransportParameterID = 0x0f
	TPVersionInformation            TransportParameterID = 0x11
	TPInitialRTT                    TransportParameterID = 0x3127
)

// TransportParameterID is a transport parameter's ID. In JSON it is a hex
// string such as "0x0f", or "GREASE" for a reserved ID (31 * N + 27, RFC
// 9000, section 18.1) picked at random per connection.
type TransportParameterID uint64

// GREASETransportParameter stands for any reserved transport parameter
// ID: the lowest, 27.
const GREASETransportParameter TransportParameterID = 27

// IsGREASETransportParameter reports a reserved transport parameter ID.
func IsGREASETransportParameter(id uint64) bool {
	return id >= 27 && (id-27)%31 == 0
}

// String returns "GREASE" or the ID in hex, at least two digits.
func (id TransportParameterID) String() string {
	if id == GREASETransportParameter {
		return "GREASE"
	}
	return fmt.Sprintf("0x%02x", uint64(id))
}

// MarshalText implements encoding.TextMarshaler.
func (id TransportParameterID) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

// UnmarshalText accepts "GREASE" or "0x" followed by hex digits. Unlike
// Code, a reserved ID in hex stays itself: browsers send real parameters
// in the reserved space (Firefox's min_ack_delay, 0xff02de1a).
func (id *TransportParameterID) UnmarshalText(text []byte) error {
	s := string(text)
	if s == "GREASE" {
		*id = GREASETransportParameter
		return nil
	}
	digits, ok := strings.CutPrefix(s, "0x")
	if !ok || digits == "" || len(digits) > 16 {
		return fmt.Errorf("invalid transport parameter %q: want \"GREASE\" or 0x followed by hex digits", s)
	}
	v, err := strconv.ParseUint(digits, 16, 62)
	if err != nil {
		return fmt.Errorf("invalid transport parameter %q: %w", s, err)
	}
	*id = TransportParameterID(v)
	return nil
}

// QUICVersion is a QUIC version. In JSON it is 8 hex digits with 0x, such
// as "0x00000001", or "GREASE" for a reserved version 0x?a?a?a?a (RFC
// 9000, section 15) picked at random per connection.
type QUICVersion uint32

// GREASEVersion stands for any reserved version: the lowest, 0x0a0a0a0a.
const GREASEVersion QUICVersion = 0x0a0a0a0a

// IsGREASEVersion reports a reserved QUIC version.
func IsGREASEVersion(v uint32) bool {
	return v&0x0f0f0f0f == 0x0a0a0a0a
}

// String returns "GREASE" or the version in hex.
func (v QUICVersion) String() string {
	if v == GREASEVersion {
		return "GREASE"
	}
	return fmt.Sprintf("0x%08x", uint32(v))
}

// MarshalText implements encoding.TextMarshaler.
func (v QUICVersion) MarshalText() ([]byte, error) {
	return []byte(v.String()), nil
}

// UnmarshalText accepts "GREASE" or "0x" followed by 1-8 hex digits. Any
// reserved version is stored as GREASEVersion.
func (v *QUICVersion) UnmarshalText(text []byte) error {
	s := string(text)
	if s == "GREASE" {
		*v = GREASEVersion
		return nil
	}
	digits, ok := strings.CutPrefix(s, "0x")
	if !ok || digits == "" || len(digits) > 8 {
		return fmt.Errorf("invalid QUIC version %q: want \"GREASE\" or 0x followed by 1-8 hex digits", s)
	}
	n, err := strconv.ParseUint(digits, 16, 32)
	if err != nil {
		return fmt.Errorf("invalid QUIC version %q: %w", s, err)
	}
	if IsGREASEVersion(uint32(n)) {
		*v = GREASEVersion
		return nil
	}
	*v = QUICVersion(n)
	return nil
}

// QUICHello is how the ClientHello a browser sends over QUIC differs from
// its TLS one (Profile.TLS): RFC 9001 forbids a session ID and TLS below
// 1.3, and browsers change more. The hello is TLS's extensions without
// Drop, those in Replace taking its bodies, with quic_transport_parameters
// (0x0039, its body QUIC.TransportParameters) added, in the order
// ExtensionOrder says; a resumed one (TLS.Resumption) also without
// TLS.Resumption.Drop, with early_data (0x002a) when QUIC.ZeroRTT, GREASE
// ECH's payload lengths ResumedECHPayloadLengths and pre_shared_key last.
// Required with Profile.QUIC; harness/cmd/hellogen derives it.
type QUICHello struct {
	// CipherSuites replaces TLS.CipherSuites. Required.
	CipherSuites []Code `json:"cipher_suites"`
	// SessionIDLength is legacy_session_id's length: 0 in every browser
	// (RFC 9001, section 8.4).
	SessionIDLength uint8 `json:"session_id_length"`
	// ExtensionOrder is ExtensionOrderFixed or ExtensionOrderShuffled,
	// whatever TLS's is: Firefox shuffles over QUIC only.
	ExtensionOrder string `json:"extension_order"`
	// Last are extensions a shuffled hello keeps last, in this order,
	// before pre_shared_key (Firefox: quic_transport_parameters, then
	// encrypted_client_hello).
	Last []Code `json:"last,omitempty"`
	// Drop are TLS's extensions the hello leaves out; GREASE drops both
	// GREASE extensions (Chrome).
	Drop []Code `json:"drop,omitempty"`
	// Replace are extensions whose bodies differ from TLS's, each
	// replacing the one of its type in place (Chrome: groups and key
	// shares without GREASE, its signature algorithms, TLS 1.3 only, ALPN
	// and ALPS h3).
	Replace []Extension `json:"replace,omitempty"`
	// TransportParametersBefore is the extension quic_transport_parameters
	// goes before in a fixed order (Safari: compress_certificate).
	// Required with ExtensionOrderFixed; a shuffled hello places it with
	// the others.
	TransportParametersBefore *Code `json:"transport_parameters_before,omitempty"`
	// ResumedECHPayloadLengths are GREASE ECH's payload lengths in a
	// resumed hello (Firefox: 432, where its TLS one has 400). Required
	// with TLS.Resumption and encrypted_client_hello.
	ResumedECHPayloadLengths []uint16 `json:"resumed_ech_payload_lengths,omitempty"`
	// VersionNegotiation is how the hello of a connection retried after
	// Version Negotiation (QUIC.VersionNegotiation.Retry) differs from a
	// full one (Firefox: no X25519MLKEM768 group or key share, no
	// compress_certificate, no GREASE ECH, padded to 512 bytes). Nil:
	// the profile does not retry.
	VersionNegotiation *QUICHelloRetry `json:"version_negotiation,omitempty"`
}

// QUICHelloRetry is how a retried QUIC hello differs from a full one.
type QUICHelloRetry struct {
	// Drop are the full hello's extensions it leaves out.
	Drop []Code `json:"drop,omitempty"`
	// Replace are extensions whose bodies differ, each replacing the one
	// of its type in place.
	Replace []Extension `json:"replace,omitempty"`
	// Padding adds padding (0x0015) last, sized as NSS and BoringSSL
	// size it: a hello of 256 to 511 bytes is padded to 512.
	Padding bool `json:"padding,omitempty"`
}

// Extension types QUIC hellos add.
const (
	ExtEarlyData               Code = 0x002a
	ExtQUICTransportParameters Code = 0x0039
)
