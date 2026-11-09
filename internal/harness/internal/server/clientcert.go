package server

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/tlsflight"
	"github.com/roachadam/mimicry/profile"
)

// Values of Scenario.ClientCert: the key of the identity the capture tool
// gives the browser.
const (
	ClientKeyEC  = "ec"
	ClientKeyRSA = "rsa"
)

// The client-certificate scenarios: the resume page (new connections to
// three hosts, sequential and parallel, HTTP/2 and HTTP/1.1, each after
// tickets arrived) from a server that asks for a client certificate on
// every connection, naming the run's test client CA, while the browser
// holds one identity issued by it. Each connection's handshake is recorded
// (capture.ClientAuth): what the client offered to resume, whether it sent
// its certificate, its chain, its CertificateVerify scheme.
var (
	ClientCertEC = &Scenario{
		Name:       "client-cert-ec",
		Doc:        "a server asking for a client certificate, the browser holding an EC P-256 identity: whether and when it sends it, its chain and CertificateVerify scheme, and resumption, on the resume page",
		clientCert: ClientKeyEC,
		multi: resumePage{
			fixedHosts: clientCertHosts,
		},
	}
	ClientCertRSA = &Scenario{
		Name:       "client-cert-rsa",
		Doc:        "client-cert-ec with an RSA-2048 identity",
		clientCert: ClientKeyRSA,
		multi: resumePage{
			fixedHosts: clientCertHosts,
		},
	}
	ClientCertECTLS12 = &Scenario{
		Name:       "client-cert-ec-tls12",
		Doc:        "client-cert-ec from a TLS 1.2-only server",
		clientCert: ClientKeyEC,
		multi: resumePage{
			fixedHosts: clientCertHosts,
		},
		maxVersion: tls.VersionTLS12,
	}
	ClientCertRSATLS12 = &Scenario{
		Name:       "client-cert-rsa-tls12",
		Doc:        "client-cert-rsa from a TLS 1.2-only server",
		clientCert: ClientKeyRSA,
		multi: resumePage{
			fixedHosts: clientCertHosts,
		},
		maxVersion: tls.VersionTLS12,
	}
)

// clientCertHosts are the resume page's hosts, given longer to run: on the
// Windows PC, Firefox 159 took 7 to 11 s over each handshake it answered
// with the identity from the user's store (2026-11-08), about 90 s over
// the page.
var clientCertHosts = fixedHosts{
	names:  resumeHosts.names,
	http1:  resumeHosts.http1,
	runFor: 3 * time.Minute,
}

// ClientCertHosts are the hosts of every client-certificate scenario's
// page, where the browser may send its identity.
func ClientCertHosts() []string {
	return slices.Clone(clientCertHosts.names)
}

// ClientCert is the key of the identity sc's browser must hold
// (ClientKeyEC, ClientKeyRSA), "" for a scenario asking for none.
func (sc *Scenario) ClientCert() string {
	return sc.clientCert
}

// noSafariClientCert is why Safari takes no client-certificate scenario.
const noSafariClientCert = "Safari 27 did not send an identity it could use: from a temporary keychain, with an identity preference for the host and the client CA trusted, it sent an empty Certificate; from the login keychain the page stalled, probably on a prompt (2026-11-08)"

// flightTap keeps the bytes each side sent through a connection until
// stopped: the client's after its ClientHello (read by the TLS server) and
// the server's.
type flightTap struct {
	net.Conn

	mu      sync.Mutex
	in, out []byte
	stopped bool
}

// flightLimit bounds what a flightTap keeps of each side: a handshake with
// a few certificates is a few kilobytes.
const flightLimit = 1 << 18

func (t *flightTap) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)

	t.mu.Lock()

	if !t.stopped && len(t.in) < flightLimit {
		t.in = append(t.in, p[:n]...)
	}

	t.mu.Unlock()

	return n, err
}

func (t *flightTap) Write(p []byte) (int, error) {
	t.mu.Lock()

	if !t.stopped && len(t.out) < flightLimit {
		t.out = append(t.out, p...)
	}

	t.mu.Unlock()

	return t.Conn.Write(p)
}

// stop ends the keeping and returns what each side sent.
func (t *flightTap) stop() (in, out []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stopped = true

	return t.in, t.out
}

// clientAuth reads a connection's handshake from what each side sent (the
// client's from the records that carried its ClientHello) and describes
// it, as far as it went: hs is nil, and the description says why, when it
// could not be read whole.
func clientAuth(client, server []byte, keys *tlsflight.KeyLog, hello []byte) (capture.ClientAuth, *tlsflight.Handshake) {
	o := resumptionOffer(hello)
	a := capture.ClientAuth{
		PSKIdentities: o.pskIdentities,
		Ticket:        o.ticket != "",
		Flight:        []string{},
	}

	hs, err := tlsflight.Read(client, server, keys)
	if hs != nil {
		a.Version = profile.Code(hs.Version)
		a.Suite = profile.Code(hs.Suite)

		if derr := describeFlight(&a, hs); derr != nil {
			err = errors.Join(err, derr)
		}
	}

	if err != nil {
		a.Error = err.Error()

		return a, nil
	}

	return a, hs
}

// describeFlight fills a's request and flight from hs.
func describeFlight(a *capture.ClientAuth, hs *tlsflight.Handshake) error {
	if m, ok := tlsflight.Find(hs.Server, tlsflight.TypeCertificateRequest); ok {
		req, err := tlsflight.ParseCertificateRequest(hs.Version, m.Body)
		if err != nil {
			return fmt.Errorf("server's CertificateRequest: %w", err)
		}

		a.Request = &capture.CertificateRequest{
			SignatureAlgorithms:     schemeCodes(req.SignatureAlgorithms),
			SignatureAlgorithmsCert: schemeCodes(req.SignatureAlgorithmsCert),
			CertificateTypes:        req.CertificateTypes,
		}

		for _, der := range req.Authorities {
			a.Request.Authorities = append(a.Request.Authorities, commonName(der))
		}
	}

	for _, m := range hs.Client {
		a.Flight = append(a.Flight, m.Name())

		switch m.Type {
		case tlsflight.TypeCertificate:
			entries, err := tlsflight.ParseCertificate(hs.Version, m.Body)
			if err != nil {
				return fmt.Errorf("client's Certificate: %w", err)
			}

			for _, e := range entries {
				c, err := describeCertificate(e)
				if err != nil {
					return fmt.Errorf("client's Certificate: %w", err)
				}

				a.Certificates = append(a.Certificates, c)
			}

		case tlsflight.TypeCertificateVerify:
			scheme, err := tlsflight.ParseCertificateVerify(m.Body)
			if err != nil {
				return fmt.Errorf("client's CertificateVerify: %w", err)
			}

			a.CertificateVerify = profile.Code(scheme)
		}
	}

	return nil
}

// describeCertificate names a certificate a client sent.
func describeCertificate(e tlsflight.CertificateEntry) (capture.ClientCertificate, error) {
	c, err := x509.ParseCertificate(e.DER)
	if err != nil {
		return capture.ClientCertificate{}, err
	}

	d := capture.ClientCertificate{
		Subject:    c.Subject.CommonName,
		Issuer:     c.Issuer.CommonName,
		CA:         c.IsCA,
		Extensions: len(e.Extensions),
	}

	switch k := c.PublicKey.(type) {
	case *ecdsa.PublicKey:
		d.Key = "ec-" + map[int]string{256: "p256", 384: "p384", 521: "p521"}[k.Curve.Params().BitSize]
	case *rsa.PublicKey:
		d.Key = "rsa-" + strconv.Itoa(k.N.BitLen())
	default:
		d.Key = c.PublicKeyAlgorithm.String()
	}

	return d, nil
}

// commonName is the common name of a distinguished name in DER, or the
// whole name as text when it has none or does not parse.
func commonName(der []byte) string {
	var rdns pkix.RDNSequence
	if rest, err := asn1.Unmarshal(der, &rdns); err != nil || len(rest) > 0 {
		return fmt.Sprintf("%x", der)
	}

	var name pkix.Name

	name.FillFromRDNSequence(&rdns)

	if name.CommonName == "" {
		return rdns.String()
	}

	return name.CommonName
}

func schemeCodes(v []uint16) []profile.Code {
	if len(v) == 0 {
		return nil
	}

	out := make([]profile.Code, len(v))
	for i, c := range v {
		out[i] = profile.Code(c)
	}

	return out
}

// recordClientAuth records the handshake of a client-certificate
// scenario's connection, c, whose ClientHello (in the records raw) arrived
// at helloAt: read from tap, with what the server made of it (state), or,
// when the handshake failed (a client giving up on the connection after
// the CertificateRequest), why. It then keeps rlog's record count right:
// the client's flight took hs.ClientEncrypted records, not the one
// Finished rlog assumes.
func recordClientAuth(log *pageLog, c *capture.Capture, tap *flightTap, raw []byte, keys *tlsflight.KeyLog, state tls.ConnectionState, failed error, rlog *recordLog, helloAt time.Time) {
	in, out := tap.stop()

	a, hs := clientAuth(append(slices.Clip(raw), in...), out, keys, c.ClientHello)

	a.Millis = helloAt.Sub(log.start).Milliseconds()
	a.HandshakeMillis = time.Since(helloAt).Milliseconds()

	if failed != nil {
		a.Host = serverName(c.ClientHello)
		a.Error = strings.TrimSuffix("the handshake failed: "+failed.Error()+"; "+a.Error, "; ")
	} else {
		a.Host = state.ServerName
		a.Protocol = state.NegotiatedProtocol
		a.Resumed = state.DidResume
	}

	if rlog != nil && hs != nil && hs.Version == tlsflight.VersionTLS13 {
		rlog.handshakeRecords(hs.ClientEncrypted)
	}

	log.clientAuth(c, a)
}
