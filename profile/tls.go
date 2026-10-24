package profile

// TLS describes a browser's ClientHello.
type TLS struct {
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
//     padding, pre_shared_key and encrypted_client_hello.
//   - A typed field: Groups for supported_groups, KeyShares for key_share, and
//     so on (see each field).
//   - Data, the exact body, for every other type including GREASE. Absent
//     Data means an empty body.
type Extension struct {
	Type Code `json:"type"`

	// Data is the exact body of an extension without a typed field.
	Data Bytes `json:"data,omitempty"`

	// Groups for supported_groups.
	Groups []Code `json:"groups,omitempty"`
	// KeyShares lists the groups a key share is sent for, for key_share. Keys
	// are generated per connection.
	KeyShares []Code `json:"key_shares,omitempty"`
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

// Extension types with special handling in profiles.
const (
	ExtServerName              Code = 0x0000
	ExtSupportedGroups         Code = 0x000a
	ExtECPointFormats          Code = 0x000b
	ExtSignatureAlgorithms     Code = 0x000d
	ExtALPN                    Code = 0x0010
	ExtPadding                 Code = 0x0015
	ExtCompressCertificate     Code = 0x001b
	ExtRecordSizeLimit         Code = 0x001c
	ExtDelegatedCredentials    Code = 0x0022
	ExtSessionTicket           Code = 0x0023
	ExtPreSharedKey            Code = 0x0029
	ExtSupportedVersions       Code = 0x002b
	ExtPSKKeyExchangeModes     Code = 0x002d
	ExtSignatureAlgorithmsCert Code = 0x0032
	ExtKeyShare                Code = 0x0033
	ExtApplicationSettingsOld  Code = 0x4469
	ExtApplicationSettings     Code = 0x44cd
	ExtEncryptedClientHello    Code = 0xfe0d
)

// generatedExtensions have bodies built per connection.
var generatedExtensions = map[Code]bool{
	ExtServerName:   true,
	ExtPadding:      true,
	ExtPreSharedKey: true,
	// Empty, or a TLS 1.2 session ticket being resumed.
	ExtSessionTicket:        true,
	ExtEncryptedClientHello: true,
}

// typedField names the JSON field that carries each typed extension's payload.
var typedField = map[Code]string{
	ExtSupportedGroups:         "groups",
	ExtKeyShare:                "key_shares",
	ExtSignatureAlgorithms:     "signature_algorithms",
	ExtSignatureAlgorithmsCert: "signature_algorithms",
	ExtDelegatedCredentials:    "signature_algorithms",
	ExtSupportedVersions:       "versions",
	ExtALPN:                    "alpn",
	ExtApplicationSettingsOld:  "alps",
	ExtApplicationSettings:     "alps",
	ExtECPointFormats:          "point_formats",
	ExtPSKKeyExchangeModes:     "psk_modes",
	ExtCompressCertificate:     "cert_compression",
	ExtRecordSizeLimit:         "record_size_limit",
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
