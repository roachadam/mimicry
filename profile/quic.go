package profile

import (
	"fmt"
	"strconv"
	"strings"
)

// QUIC is how a browser speaks QUIC version 1 (RFC 9000) for HTTP/3: how
// it builds and pads its Initial packets and cuts its ClientHello
// (TLS.QUIC) into them, its connection IDs and packet numbers, its
// transport parameters, how it acknowledges, and how it resumes and ends a
// connection. harness/cmd/h3gen derives it from the h3 captures and
// checks every value against every sample; it is never hand-edited.
//
// Not measured, for every browser: credit growth (MAX_DATA,
// MAX_STREAM_DATA, MAX_STREAMS under large downloads: the captured
// responses never needed more), key updates the browser starts, path MTU
// probing, ECN, migration, DATAGRAM frames, Retry and version negotiation.
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
}

// Extension types QUIC hellos add.
const (
	ExtEarlyData               Code = 0x002a
	ExtQUICTransportParameters Code = 0x0039
)
