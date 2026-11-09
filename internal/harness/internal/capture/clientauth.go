package capture

import (
	"github.com/roachadam/mimicry/profile"
)

// ClientAuth is one TLS connection of a client-certificate scenario
// (server.ClientCertEC and its siblings), whose server asked for a client
// certificate: what the client offered to resume, what the server asked
// for and how the client answered, as the handshake crossed the wire.
type ClientAuth struct {
	// Conn numbers the connection in the page's Connections, from 1; 0
	// for one whose handshake failed, which Connections leaves out.
	Conn int `json:"conn"`
	// Host is its server_name; Millis when its ClientHello arrived, on
	// the page's clock.
	Host   string `json:"host"`
	Millis int64  `json:"ms"`
	// HandshakeMillis is how long the handshake took from the ClientHello's
	// arrival to its end (the client's Finished read, or the failure):
	// the time a client spends choosing and signing shows here.
	HandshakeMillis int64 `json:"handshakeMs"`
	// Protocol is the ALPN protocol negotiated, "" when the handshake
	// failed.
	Protocol string `json:"protocol"`
	// Version and Suite are what the server chose.
	Version profile.Code `json:"version"`
	Suite   profile.Code `json:"suite"`
	// PSKIdentities is how many TLS 1.3 tickets the ClientHello offered;
	// Ticket reports a TLS 1.2 session ticket offered.
	PSKIdentities int  `json:"pskIdentities,omitempty"`
	Ticket        bool `json:"ticket,omitempty"`
	// Resumed reports that the server resumed the session offered: it
	// then sent no CertificateRequest.
	Resumed bool `json:"resumed,omitempty"`
	// Request is the server's CertificateRequest, nil when it sent none.
	Request *CertificateRequest `json:"certificateRequest,omitempty"`
	// Flight names the client's messages after its ClientHello, in the
	// order sent, to its Finished: "certificate", "client_key_exchange",
	// "certificate_verify", "change_cipher_spec" (a record, not a
	// handshake message), "finished".
	Flight []string `json:"flight"`
	// Certificates are the certificates the client's Certificate message
	// carried, in order; empty for an empty one (Flight says whether it
	// sent one at all).
	Certificates []ClientCertificate `json:"certificates,omitempty"`
	// CertificateVerify is the signature scheme of the client's
	// CertificateVerify, zero when it sent none.
	CertificateVerify profile.Code `json:"certificateVerify,omitempty"`
	// Requests are the paths (with their queries) of the requests the
	// connection carried, in the order they arrived.
	Requests []string `json:"requests,omitempty"`
	// Error says why the handshake failed (a client that gave up on the
	// connection) or could not be read whole; the fields hold what it
	// got to.
	Error string `json:"error,omitempty"`
}

// CertificateRequest is what a server's CertificateRequest asked for.
type CertificateRequest struct {
	// SignatureAlgorithms are the schemes a CertificateVerify may use,
	// in the server's order; SignatureAlgorithmsCert TLS 1.3's
	// signature_algorithms_cert, when sent.
	SignatureAlgorithms     []profile.Code `json:"signatureAlgorithms"`
	SignatureAlgorithmsCert []profile.Code `json:"signatureAlgorithmsCert,omitempty"`
	// CertificateTypes are TLS 1.2's certificate_types.
	CertificateTypes []uint8 `json:"certificateTypes,omitempty"`
	// Authorities are the common names of the CAs it named.
	Authorities []string `json:"authorities,omitempty"`
}

// ClientCertificate is one certificate a client sent.
type ClientCertificate struct {
	// Subject and Issuer are common names.
	Subject string `json:"subject"`
	Issuer  string `json:"issuer"`
	// Key is its public key: "ec-p256", "rsa-2048".
	Key string `json:"key"`
	// CA reports a CA certificate; a client's own is the leaf.
	CA bool `json:"ca,omitempty"`
	// Extensions is the length of its TLS 1.3 extensions (zero: none).
	Extensions int `json:"extensions,omitempty"`
}
