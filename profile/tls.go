package profile

// TLS describes a browser's ClientHello.
type TLS struct {
	// RecordVersion is the record-layer version of the TLS records that
	// carry the ClientHello: 0x0301 for every browser captured (RFC 8446,
	// section 5.1, allows 0x0301 for the initial ClientHello). Zero in a
	// capture whose records were not kept.
	RecordVersion Code `json:"record_version,omitempty"`
	// LegacyVersion is the ClientHello's legacy_version: 0x0303 in a TLS 1.3
	// ClientHello (RFC 8446, section 4.1.2).
	LegacyVersion Code `json:"legacy_version"`
	// SessionIDLength is the length of legacy_session_id, whose bytes are
	// random per connection: 32 in TLS 1.3's middlebox compatibility mode
	// (RFC 8446, appendix D.4).
	SessionIDLength uint8 `json:"session_id_length"`
	// CipherSuites in the order the browser offers them.
	CipherSuites []Code `json:"cipher_suites"`
	// CompressionMethods, normally [0] (null).
	CompressionMethods ByteList `json:"compression_methods"`
	// ExtensionOrder says whether the browser sends Extensions in this exact
	// order (ExtensionOrderFixed) or permutes them per connection
	// (ExtensionOrderShuffled, Chrome since 110). Pinned extensions keep their
	// position either way; see Extension.Pinned.
	ExtensionOrder string `json:"extension_order"`
	// Extensions in the order of one observed ClientHello: a full
	// handshake's.
	Extensions []Extension `json:"extensions"`
	// Resumption is how the browser resumes a TLS 1.3 session on a new
	// connection to a server it holds a ticket for. Nil: it does not
	// (Safari), and every connection is a full handshake.
	Resumption *Resumption `json:"resumption,omitempty"`
	// WebSocketH1 is how the hello of a WebSocket's own HTTP/1.1
	// connection differs from this one. Required with Profile.WebSocket;
	// hellogen derives it.
	WebSocketH1 *WebSocketHello `json:"websocket_h1,omitempty"`
	// QUIC is how the hello sent over QUIC differs from this one.
	// Required with Profile.QUIC; hellogen derives it.
	QUIC *QUICHello `json:"quic,omitempty"`
}

// Resumption describes how a browser resumes sessions. With a TLS 1.3
// ticket, the resumed ClientHello is the full one, in the same order or
// shuffled alike, minus Drop, with pre_shared_key last; each ticket is used
// once (Chrome, Firefox), so concurrent new connections resume one at a
// time. With a TLS 1.2 ticket, the ClientHello is the full one with the
// ticket in session_ticket, used as TLS12 says.
type Resumption struct {
	// Drop are extensions a TLS 1.3 resumed ClientHello leaves out
	// (Firefox: session_ticket).
	Drop []Code `json:"drop,omitempty"`
	// TLS12 is how TLS 1.2 session tickets are used: TLS12Reuse or
	// TLS12SingleUse. Empty: TLS 1.2 sessions are not resumed.
	TLS12 string `json:"tls12,omitempty"`
	// ECHPayloadLengths are the GREASE ECH payload lengths a TLS 1.3
	// resumed ClientHello picks from, one per connection, in place of the
	// full hello's ech.payload_lengths: the inner hello it pads for carries
	// the ticket (Firefox: 400 bytes instead of 240). Required when the
	// ClientHello has encrypted_client_hello.
	ECHPayloadLengths []uint16 `json:"ech_payload_lengths,omitempty"`
}

// Values of Resumption.TLS12.
const (
	// TLS12Reuse resumes every new connection with the newest ticket,
	// concurrent ones included, until a newer one replaces it (Chrome).
	TLS12Reuse = "reuse"
	// TLS12SingleUse uses a ticket once, and keeps none a resumed
	// connection receives, so connections alternate between full and
	// resumed handshakes (Firefox).
	TLS12SingleUse = "single_use"
)

// Values of TLS.ExtensionOrder.
const (
	ExtensionOrderFixed    = "fixed"
	ExtensionOrderShuffled = "shuffled"
)

// Pinned reports whether e keeps its position when a browser shuffles its
// extensions: GREASE brackets the list, padding sizes the record, and
// pre_shared_key must be last (RFC 8446, section 4.2.11).
func (e *Extension) Pinned() bool {
	return e.Type == GREASE || e.Type == ExtPadding || e.Type == ExtPreSharedKey
}

// Extension is one ClientHello extension. Type is required. The payload is
// given in one of three ways, depending on Type:
//
//   - Generated per connection, with no payload in the profile: server_name,
//     padding, pre_shared_key and session_ticket.
//   - Typed fields: Groups for supported_groups, KeyShares and
//     KeyShareLengths for key_share, ECH for encrypted_client_hello, and so
//     on (see each field).
//   - Data, the exact body, for every other type including GREASE. Absent
//     Data means an empty body. uTLS writes some bodies itself, so Data must
//     be the one it sends: empty for the first GREASE extension (of at most
//     two), 00 for the second; empty for extended_master_secret and
//     signed_certificate_timestamp; 0100000000 for status_request; 00 for
//     renegotiation_info.
type Extension struct {
	Type Code `json:"type"`

	// Data is the exact body of an extension without a typed field.
	Data Bytes `json:"data,omitempty"`

	// Groups for supported_groups.
	Groups []Code `json:"groups,omitempty"`
	// KeyShares lists the groups a key share is sent for, for key_share. Keys
	// are generated per connection.
	KeyShares []Code `json:"key_shares,omitempty"`
	// KeyShareLengths are the lengths of the keys in KeyShares, in the same
	// order, for key_share. A group fixes its key's length; a GREASE share's
	// is the browser's choice (BoringSSL: one byte).
	KeyShareLengths []uint16 `json:"key_share_lengths,omitempty"`
	// ECH for encrypted_client_hello: the GREASE extension's shape.
	ECH *ECH `json:"ech,omitempty"`
	// SignatureAlgorithms for signature_algorithms, signature_algorithms_cert
	// and delegated_credentials.
	SignatureAlgorithms []Code `json:"signature_algorithms,omitempty"`
	// Versions for supported_versions.
	Versions []Code `json:"versions,omitempty"`
	// ALPN protocols for application_layer_protocol_negotiation.
	ALPN []string `json:"alpn,omitempty"`
	// ALPS protocols for application_settings (either codepoint).
	ALPS []string `json:"alps,omitempty"`
	// PointFormats for ec_point_formats.
	PointFormats ByteList `json:"point_formats,omitempty"`
	// PSKModes for psk_key_exchange_modes.
	PSKModes ByteList `json:"psk_modes,omitempty"`
	// CertCompression algorithms for compress_certificate.
	CertCompression []Code `json:"cert_compression,omitempty"`
	// RecordSizeLimit for record_size_limit.
	RecordSizeLimit uint16 `json:"record_size_limit,omitempty"`
}

// ECH is the shape of the GREASE encrypted_client_hello extension a browser
// sends to a server it holds no ECH configuration for: an outer ClientHello's
// extension with an HPKE cipher suite, a config_id, an encapsulated key and
// a payload, all but their lengths random. The config_id is a random byte
// per connection; the cipher suite and the payload length are picked per
// connection from CipherSuites and PayloadLengths, which list every value
// the captures show (one each when the browser does not vary it).
type ECH struct {
	// CipherSuites are the HPKE symmetric cipher suites, sorted.
	CipherSuites []HPKESuite `json:"cipher_suites"`
	// EncLength is the encapsulated key's length: 32 for X25519.
	EncLength uint16 `json:"enc_length"`
	// PayloadLengths are the payload's lengths, the AEAD tag included,
	// sorted.
	PayloadLengths []uint16 `json:"payload_lengths"`
}

// HPKESuite is an HPKE symmetric cipher suite (RFC 9180, section 7).
type HPKESuite struct {
	KDF  Code `json:"kdf"`
	AEAD Code `json:"aead"`
}

// Compare orders suites by KDF, then AEAD.
func (s HPKESuite) Compare(o HPKESuite) int {
	if s.KDF != o.KDF {
		return int(s.KDF) - int(o.KDF)
	}
	return int(s.AEAD) - int(o.AEAD)
}

// Extension types with special handling in profiles.
const (
	ExtServerName                 Code = 0x0000
	ExtStatusRequest              Code = 0x0005
	ExtSupportedGroups            Code = 0x000a
	ExtECPointFormats             Code = 0x000b
	ExtSignatureAlgorithms        Code = 0x000d
	ExtALPN                       Code = 0x0010
	ExtSignedCertificateTimestamp Code = 0x0012
	ExtPadding                    Code = 0x0015
	ExtExtendedMasterSecret       Code = 0x0017
	ExtCompressCertificate        Code = 0x001b
	ExtRecordSizeLimit            Code = 0x001c
	ExtDelegatedCredentials       Code = 0x0022
	ExtSessionTicket              Code = 0x0023
	ExtPreSharedKey               Code = 0x0029
	ExtSupportedVersions          Code = 0x002b
	ExtPSKKeyExchangeModes        Code = 0x002d
	ExtSignatureAlgorithmsCert    Code = 0x0032
	ExtKeyShare                   Code = 0x0033
	ExtApplicationSettingsOld     Code = 0x4469
	ExtApplicationSettings        Code = 0x44cd
	ExtEncryptedClientHello       Code = 0xfe0d
	ExtRenegotiationInfo          Code = 0xff01
)

// generatedExtensions have bodies built per connection.
var generatedExtensions = map[Code]bool{
	ExtServerName:   true,
	ExtPadding:      true,
	ExtPreSharedKey: true,
	// Empty, or a TLS 1.2 session ticket being resumed.
	ExtSessionTicket: true,
}

// typedFields names the JSON fields that carry each typed extension's
// payload; all are required.
var typedFields = map[Code][]string{
	ExtSupportedGroups: {
		"groups",
	},
	ExtKeyShare: {
		"key_shares",
		"key_share_lengths",
	},
	ExtEncryptedClientHello: {
		"ech",
	},
	ExtSignatureAlgorithms: {
		"signature_algorithms",
	},
	ExtSignatureAlgorithmsCert: {
		"signature_algorithms",
	},
	ExtDelegatedCredentials: {
		"signature_algorithms",
	},
	ExtSupportedVersions: {
		"versions",
	},
	ExtALPN: {
		"alpn",
	},
	ExtApplicationSettingsOld: {
		"alps",
	},
	ExtApplicationSettings: {
		"alps",
	},
	ExtECPointFormats: {
		"point_formats",
	},
	ExtPSKKeyExchangeModes: {
		"psk_modes",
	},
	ExtCompressCertificate: {
		"cert_compression",
	},
	ExtRecordSizeLimit: {
		"record_size_limit",
	},
}

// setFields returns the JSON names of the payload fields set on e.
func (e *Extension) setFields() []string {
	var fields []string
	if len(e.Data) > 0 {
		fields = append(fields, "data")
	}
	if len(e.Groups) > 0 {
		fields = append(fields, "groups")
	}
	if len(e.KeyShares) > 0 {
		fields = append(fields, "key_shares")
	}
	if len(e.KeyShareLengths) > 0 {
		fields = append(fields, "key_share_lengths")
	}
	if e.ECH != nil {
		fields = append(fields, "ech")
	}
	if len(e.SignatureAlgorithms) > 0 {
		fields = append(fields, "signature_algorithms")
	}
	if len(e.Versions) > 0 {
		fields = append(fields, "versions")
	}
	if len(e.ALPN) > 0 {
		fields = append(fields, "alpn")
	}
	if len(e.ALPS) > 0 {
		fields = append(fields, "alps")
	}
	if len(e.PointFormats) > 0 {
		fields = append(fields, "point_formats")
	}
	if len(e.PSKModes) > 0 {
		fields = append(fields, "psk_modes")
	}
	if len(e.CertCompression) > 0 {
		fields = append(fields, "cert_compression")
	}
	if e.RecordSizeLimit != 0 {
		fields = append(fields, "record_size_limit")
	}
	return fields
}
