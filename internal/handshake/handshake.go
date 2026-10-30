// Package handshake performs the TLS handshake and ALPN from a fingerprint
// plan and keeps session tickets for resumption. Config.VerifyConnection
// runs in every handshake, resumed ones included.
package handshake

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"

	utls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/profile"
)

// Config is what a handshake needs besides the ClientHello.
type Config struct {
	// ServerName is sent in server_name and verified against the certificate.
	ServerName string
	// RootCAs verifies the server certificate; nil uses the system roots.
	RootCAs *x509.CertPool
	// Tickets, when set, keeps the server's TLS 1.3 tickets under
	// TicketKey for later connections. Nil: no resumption.
	Tickets   *Tickets
	TicketKey string
	// Ticket is the session this connection resumes; the hello must then
	// carry pre_shared_key (fingerprint.Plan.ResumedHello).
	Ticket *Ticket
	// VerifyConnection, when set, runs after certificate verification on
	// every handshake, resumed ones included, before any received ticket is
	// kept; an error fails the handshake.
	VerifyConnection func(tls.ConnectionState) error
}

// Client performs the TLS handshake on conn with hello and returns the TLS
// connection and the negotiated ALPN protocol. It honours ctx; on error conn
// is closed.
func Client(ctx context.Context, conn net.Conn, hello fingerprint.Hello, cfg Config) (net.Conn, string, error) {
	spec, err := Spec(hello)
	if err != nil {
		_ = conn.Close()
		return nil, "", err
	}

	tlsConn := utls.UClient(conn, TLSConfig(cfg), utls.HelloCustom)
	if err := tlsConn.ApplyPreset(spec); err != nil {
		_ = conn.Close()
		return nil, "", fmt.Errorf("handshake: applying ClientHello: %w", err)
	}

	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, "", err
	}

	return tlsConn, tlsConn.ConnectionState().NegotiatedProtocol, nil
}

// ConnState is the TLS state of conn, a connection Client returned, as
// crypto/tls describes it; nil for any other connection.
func ConnState(conn net.Conn) *tls.ConnectionState {
	tc, ok := conn.(*utls.UConn)
	if !ok {
		return nil
	}

	return State(tc.ConnectionState())
}

// State is cs as crypto/tls describes it. ExportKeyingMaterial is not
// carried over: it fails on the result.
func State(cs utls.ConnectionState) *tls.ConnectionState {
	return &tls.ConnectionState{
		Version:                     cs.Version,
		HandshakeComplete:           cs.HandshakeComplete,
		DidResume:                   cs.DidResume,
		CipherSuite:                 cs.CipherSuite,
		CurveID:                     tls.CurveID(cs.CurveID),
		NegotiatedProtocol:          cs.NegotiatedProtocol,
		ServerName:                  cs.ServerName,
		PeerCertificates:            cs.PeerCertificates,
		VerifiedChains:              cs.VerifiedChains,
		SignedCertificateTimestamps: cs.SignedCertificateTimestamps,
		OCSPResponse:                cs.OCSPResponse,
		TLSUnique:                   cs.TLSUnique,
		ECHAccepted:                 cs.ECHAccepted,
		HelloRetryRequest:           cs.HelloRetryRequest,
		LocalCertificate:            cs.LocalCertificate,
	}
}

// TLSConfig is the uTLS configuration for cfg: its server name, roots and
// ticket cache, which QUIC uses for its 0-RTT tickets too. Both TCP and
// QUIC handshakes use it.
func TLSConfig(cfg Config) *utls.Config {
	config := &utls.Config{
		ServerName: cfg.ServerName,
		RootCAs:    cfg.RootCAs,
		// Without a ticket to resume, the hello has no pre_shared_key.
		PreferSkipResumptionOnNilExtension: true,
	}

	var cache *connCache

	if cfg.Tickets != nil {
		cache = &connCache{
			ticket:  cfg.Ticket,
			tickets: cfg.Tickets,
			key:     cfg.TicketKey,
		}
		config.ClientSessionCache = cache
		// A ticket uTLS will not use (another name) leaves pre_shared_key
		// empty: leave it out and do a full handshake.
		config.OmitEmptyPsk = true
	}

	if cache == nil && cfg.VerifyConnection == nil {
		return config
	}

	// It runs on resumed handshakes too (VerifyPeerCertificate does not),
	// once verification has passed, and before any ticket is stored: a
	// ticket comes only from a connection it accepted.
	config.VerifyConnection = func(cs utls.ConnectionState) error {
		if cfg.VerifyConnection != nil {
			if err := cfg.VerifyConnection(*State(cs)); err != nil {
				return err
			}
		}

		// Tickets keep their server certificate's expiry, past which
		// uTLS declines them.
		if cache != nil && len(cs.PeerCertificates) > 0 {
			cache.notAfter = cs.PeerCertificates[0].NotAfter
		}

		return nil
	}

	return config
}

// Spec turns a connection's ClientHello layout into a uTLS spec. GREASE
// placeholders, empty key shares and GREASE ECH's random fields are filled
// in by uTLS per connection, so a spec serves one connection.
func Spec(h fingerprint.Hello) (*utls.ClientHelloSpec, error) {
	// uTLS sends the first hello in TLS 1.0 records, caps legacy_version at
	// TLS 1.2 and sends a 32-byte session ID, none over QUIC (RFC 9001,
	// section 8.4); profile.Validate allows nothing else.
	sid := sessionIDLength
	if slices.ContainsFunc(h.Extensions, func(e profile.Extension) bool {
		return e.Type == profile.ExtQUICTransportParameters
	}) {
		sid = 0
	}

	if h.LegacyVersion != legacyVersion || h.SessionIDLength != sid {
		return nil, fmt.Errorf("handshake: legacyVersion %s with a %d-byte session ID; uTLS sends %s with %d bytes",
			h.LegacyVersion, h.SessionIDLength, legacyVersion, sid)
	}

	spec := &utls.ClientHelloSpec{
		CipherSuites:       codes16(h.CipherSuites),
		CompressionMethods: append([]uint8(nil), h.CompressionMethods...),
	}
	for i := range h.Extensions {
		e := &h.Extensions[i]

		ext, err := extension(e)
		if err != nil {
			return nil, fmt.Errorf("handshake: extension %s: %w", e.Type, err)
		}

		if ks, ok := ext.(*utls.KeyShareExtension); ok && h.X25519Reuse {
			if err := reuseX25519(ks); err != nil {
				return nil, fmt.Errorf("handshake: extension %s: %w", e.Type, err)
			}
		}

		spec.Extensions = append(spec.Extensions, ext)
		if e.Type == profile.ExtSupportedVersions {
			for _, v := range e.Versions {
				if v == profile.GREASE {
					continue
				}

				if spec.TLSVersMin == 0 || uint16(v) < spec.TLSVersMin {
					spec.TLSVersMin = uint16(v)
				}

				spec.TLSVersMax = max(spec.TLSVersMax, uint16(v))
			}
		}
	}

	if spec.TLSVersMax == 0 {
		return nil, errors.New("handshake: profile has no supported_versions; TLS 1.2-only profiles are not supported")
	}

	return spec, nil
}

// reuseX25519 has uTLS send X25519MLKEM768's X25519 key as the x25519 share
// too, which it supports for the x25519 share after the hybrid one.
func reuseX25519(ks *utls.KeyShareExtension) error {
	hybrid := slices.IndexFunc(ks.KeyShares, func(s utls.KeyShare) bool { return s.Group == utls.X25519MLKEM768 })
	x25519 := slices.IndexFunc(ks.KeyShares, func(s utls.KeyShare) bool { return s.Group == utls.X25519 })

	if hybrid < 0 || x25519 < hybrid {
		return errors.New("X25519 key reuse needs X25519MLKEM768 and then x25519 key shares")
	}

	shares := utls.ReuseHybridAndClassicalKeyShares(ks.KeyShares[hybrid], ks.KeyShares[x25519])

	ks.KeyShares[hybrid], ks.KeyShares[x25519] = shares[0], shares[1]

	return nil
}

// extension maps one profile extension to uTLS. Extensions whose effect uTLS
// must know about (EMS, session tickets, certificate compression, key shares,
// ALPN, ...) use uTLS's own types; anything else is sent as its exact bytes.
func extension(e *profile.Extension) (utls.TLSExtension, error) {
	switch e.Type {
	case profile.GREASE:
		// uTLS gives the second GREASE extension its one-byte body itself.
		return &utls.UtlsGREASEExtension{}, nil
	case profile.ExtServerName:
		return &utls.SNIExtension{}, nil
	case profile.ExtPadding:
		return &utls.UtlsPaddingExtension{
			GetPaddingLen: utls.BoringPaddingStyle,
		}, nil

	case profile.ExtEncryptedClientHello:
		return greaseECH(e.ECH)
	case profile.ExtPreSharedKey:
		// Filled from the connection's ticket by uTLS.
		return &utls.UtlsPreSharedKeyExtension{}, nil
	case profile.ExtSessionTicket:
		// Empty, or the TLS 1.2 ticket being resumed, filled by uTLS.
		return &utls.SessionTicketExtension{}, nil
	case profile.ExtQUICTransportParameters:
		// The QUIC connection's transport parameters, which it sets.
		return &utls.QUICTransportParametersExtension{}, nil
	case profile.ExtSupportedGroups:
		curves := make([]utls.CurveID, len(e.Groups))
		for i, g := range e.Groups {
			curves[i] = utls.CurveID(g)
		}

		return &utls.SupportedCurvesExtension{
			Curves: curves,
		}, nil

	case profile.ExtKeyShare:
		if len(e.KeyShareLengths) != len(e.KeyShares) {
			return nil, fmt.Errorf("%d key share lengths for %d key shares", len(e.KeyShareLengths), len(e.KeyShares))
		}

		shares := make([]utls.KeyShare, len(e.KeyShares))
		for i, g := range e.KeyShares {
			shares[i] = utls.KeyShare{
				Group: utls.CurveID(g),
			}
			if g == profile.GREASE {
				// uTLS sends a GREASE share's key as given: zeros, as
				// BoringSSL does, of the profile's length. Other keys are
				// generated, their lengths fixed by the group.
				shares[i].Data = make([]byte, e.KeyShareLengths[i])
			}
		}

		return &utls.KeyShareExtension{
			KeyShares: shares,
		}, nil

	case profile.ExtSignatureAlgorithms:
		return &utls.SignatureAlgorithmsExtension{
			SupportedSignatureAlgorithms: schemes(e.SignatureAlgorithms),
		}, nil

	case profile.ExtSignatureAlgorithmsCert:
		return &utls.SignatureAlgorithmsCertExtension{
			SupportedSignatureAlgorithms: schemes(e.SignatureAlgorithms),
		}, nil

	case profile.ExtDelegatedCredentials:
		return &utls.DelegatedCredentialsExtension{
			SupportedSignatureAlgorithms: schemes(e.SignatureAlgorithms),
		}, nil

	case profile.ExtSupportedVersions:
		return &utls.SupportedVersionsExtension{
			Versions: codes16(e.Versions),
		}, nil

	case profile.ExtALPN:
		return &utls.ALPNExtension{
			AlpnProtocols: append([]string(nil), e.ALPN...),
		}, nil

	case profile.ExtApplicationSettings:
		return &utls.ApplicationSettingsExtensionNew{
			SupportedProtocols: append([]string(nil), e.ALPS...),
		}, nil

	case profile.ExtApplicationSettingsOld:
		return &utls.ApplicationSettingsExtension{
			SupportedProtocols: append([]string(nil), e.ALPS...),
		}, nil

	case profile.ExtECPointFormats:
		return &utls.SupportedPointsExtension{
			SupportedPoints: append([]uint8(nil), e.PointFormats...),
		}, nil

	case profile.ExtPSKKeyExchangeModes:
		return &utls.PSKKeyExchangeModesExtension{
			Modes: append([]uint8(nil), e.PSKModes...),
		}, nil

	case profile.ExtCompressCertificate:
		algs := make([]utls.CertCompressionAlgo, len(e.CertCompression))
		for i, a := range e.CertCompression {
			algs[i] = utls.CertCompressionAlgo(a)
		}

		return &utls.UtlsCompressCertExtension{
			Algorithms: algs,
		}, nil

	case profile.ExtRecordSizeLimit:
		return &utls.FakeRecordSizeLimitExtension{
			Limit: e.RecordSizeLimit,
		}, nil

	default:
		// The profile holds the rest as raw bytes.
		return dataExtension(e)
	}
}

// What uTLS sends regardless of the profile: legacy_version, the session ID
// length, GREASE ECH's X25519 key and the HPKE AEAD tag.
const (
	legacyVersion   profile.Code = 0x0303
	sessionIDLength uint8        = 32
	echEncLength    uint16       = 32
	hpkeTagLength   uint16       = 16
)

// greaseECH builds a GREASE encrypted_client_hello extension in the
// profile's shape. uTLS picks the cipher suite and the payload length per
// connection from the candidates, and a random config_id.
func greaseECH(ech *profile.ECH) (utls.TLSExtension, error) {
	if ech == nil {
		return nil, errors.New("no GREASE ECH shape")
	}

	if ech.EncLength != echEncLength {
		return nil, fmt.Errorf("encapsulated key of %d bytes; uTLS sends %d", ech.EncLength, echEncLength)
	}

	ext := &utls.GREASEEncryptedClientHelloExtension{
		CandidateCipherSuites: make([]utls.HPKESymmetricCipherSuite, len(ech.CipherSuites)),
		CandidatePayloadLens:  make([]uint16, len(ech.PayloadLengths)),
	}
	for i, s := range ech.CipherSuites {
		// AES-128-GCM, AES-256-GCM and ChaCha20Poly1305: uTLS panics on
		// any other.
		if s.AEAD < 1 || s.AEAD > 3 {
			return nil, fmt.Errorf("HPKE AEAD %s is not one uTLS can size", s.AEAD)
		}

		ext.CandidateCipherSuites[i] = utls.HPKESymmetricCipherSuite{
			KdfId:  uint16(s.KDF),
			AeadId: uint16(s.AEAD),
		}
	}

	for i, n := range ech.PayloadLengths {
		if n <= hpkeTagLength {
			return nil, fmt.Errorf("payload of %d bytes is no longer than the AEAD tag", n)
		}

		// uTLS takes the plaintext's length and adds the tag.
		ext.CandidatePayloadLens[i] = n - hpkeTagLength
	}

	// HPKE's enc for DHKEM(X25519) is the sender's ephemeral X25519 public
	// key. Given one, uTLS skips setting up an HPKE sender to a dummy key
	// (an extra X25519, the key schedule, an AEAD) only to discard all but
	// enc. A real key pair, never random bytes: an X25519 public key's top
	// bit is always clear.
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("GREASE ECH key: %w", err)
	}

	ext.EncapsulatedKey = k.PublicKey().Bytes()

	return ext, nil
}

// Raw-data extensions uTLS has state for.
const (
	extStatusRequest        = 0x0005
	extSCT                  = 0x0012
	extExtendedMasterSecret = 0x0017
	extRenegotiationInfo    = 0xff01
)

// dataExtension handles extensions the profile stores as raw bytes. Those the
// handshake depends on map to uTLS types, after checking the bytes are the
// ones uTLS writes; the rest are sent verbatim.
func dataExtension(e *profile.Extension) (utls.TLSExtension, error) {
	want := func(body []byte, ext utls.TLSExtension) (utls.TLSExtension, error) {
		if string(e.Data) != string(body) {
			return nil, fmt.Errorf("body %x is not the %x uTLS sends for this extension", []byte(e.Data), body)
		}

		return ext, nil
	}
	switch e.Type {
	case extExtendedMasterSecret:
		return want(nil, &utls.ExtendedMasterSecretExtension{})
	case extSCT:
		return want(nil, &utls.SCTExtension{})
	case extStatusRequest:
		return want([]byte{1, 0, 0, 0, 0}, &utls.StatusRequestExtension{})
	case extRenegotiationInfo:
		return want([]byte{0}, &utls.RenegotiationInfoExtension{
			Renegotiation: utls.RenegotiateOnceAsClient,
		})

	default:
		return &utls.GenericExtension{
			Id:   uint16(e.Type),
			Data: append([]byte(nil), e.Data...),
		}, nil
	}
}

func codes16(codes []profile.Code) []uint16 {
	out := make([]uint16, len(codes))
	for i, c := range codes {
		out[i] = uint16(c)
	}

	return out
}

func schemes(codes []profile.Code) []utls.SignatureScheme {
	out := make([]utls.SignatureScheme, len(codes))
	for i, c := range codes {
		out[i] = utls.SignatureScheme(c)
	}

	return out
}
