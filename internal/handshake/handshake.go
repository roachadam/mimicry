package handshake

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"

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
	config := &utls.Config{
		ServerName: cfg.ServerName,
		RootCAs:    cfg.RootCAs,
		// Without a ticket to resume, the hello has no pre_shared_key.
		PreferSkipResumptionOnNilExtension: true,
	}
	if cfg.Tickets != nil {
		cache := &connCache{
			ticket:  cfg.Ticket,
			tickets: cfg.Tickets,
			key:     cfg.TicketKey,
		}
		config.ClientSessionCache = cache
		// Tickets keep their server certificate's expiry, past which uTLS
		// declines them. It runs on resumed handshakes too, and before any
		// ticket is stored.
		config.VerifyConnection = func(cs utls.ConnectionState) error {
			if len(cs.PeerCertificates) > 0 {
				cache.notAfter = cs.PeerCertificates[0].NotAfter
			}
			return nil
		}
		// A ticket uTLS will not use (another name) leaves pre_shared_key
		// empty: leave it out and do a full handshake.
		config.OmitEmptyPsk = true
	}
	tlsConn := utls.UClient(conn, config, utls.HelloCustom)
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

// Spec turns a connection's ClientHello layout into a uTLS spec. GREASE
// placeholders and empty key shares are filled in by uTLS per connection.
func Spec(h fingerprint.Hello) (*utls.ClientHelloSpec, error) {
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
		// GREASE ECH. The profile does not yet record a browser's ECH
		// parameters, so every profile uses BoringSSL's (Chrome's).
		return utls.BoringGREASEECH(), nil
	case profile.ExtPreSharedKey:
		// Filled from the connection's ticket by uTLS.
		return &utls.UtlsPreSharedKeyExtension{}, nil
	case profile.ExtSessionTicket:
		// Empty, or the TLS 1.2 ticket being resumed, filled by uTLS.
		return &utls.SessionTicketExtension{}, nil
	case profile.ExtSupportedGroups:
		curves := make([]utls.CurveID, len(e.Groups))
		for i, g := range e.Groups {
			curves[i] = utls.CurveID(g)
		}
		return &utls.SupportedCurvesExtension{
			Curves: curves,
		}, nil
	case profile.ExtKeyShare:
		shares := make([]utls.KeyShare, len(e.KeyShares))
		for i, g := range e.KeyShares {
			shares[i] = utls.KeyShare{
				Group: utls.CurveID(g),
			}
			if g == profile.GREASE {
				// A GREASE key share carries a one-byte key, as BoringSSL sends.
				shares[i].Data = []byte{
					0,
				}
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
	}
	return dataExtension(e)
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
	}
	return &utls.GenericExtension{
		Id:   uint16(e.Type),
		Data: append([]byte(nil), e.Data...),
	}, nil
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
