package server

import (
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/net/dns/dnsmessage"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// The ech page's hosts. Each publishes an HTTPS record (ECHRecord) whose
// ECH config list sets up one case: one the server decrypts; one it holds
// no key for, rejected with retry configs; one it holds no key for,
// rejected without; one that does not parse; and a list whose first config
// has a KEM other than X25519, then one listing AES-256-GCM first.
const (
	echHost        = "www.ech.test"
	echRetryHost   = "www.ech-retry.test"
	echNoRetryHost = "www.ech-noretry.test"
	echBadHost     = "www.ech-bad.test"
	echSuiteHost   = "www.ech-suite.test"
)

// The ECH configs' public names: what an outer ClientHello names, and
// what the server answers a rejected one as. A rejection from
// echNoRetryName carries no retry configs.
const (
	echPublicName  = "public.ech.test"
	echNoRetryName = "noretry.ech.test"
)

// echConfig is one of the ech scenario's ECH configs (RFC 9849, Section 4),
// with a key derived from its ID, so every run, and the e2e tests, publish
// the same configs.
type echConfig struct {
	id         uint8
	publicName string
	// p256 makes its KEM DHKEM(P-256, HKDF-SHA256); otherwise it is
	// DHKEM(X25519, HKDF-SHA256).
	p256 bool
	// maxNameLength is maximum_name_length, which sets how a client pads
	// its inner hello (RFC 9849, Section 6.1.3).
	maxNameLength uint8
	// suites are its HPKE suites (KDF, AEAD), in its order.
	suites [][2]uint16
	// served says the server holds its key; retry that it sends it as a
	// retry config.
	served, retry bool
}

// The ech scenario's configs. Those the server decrypts list their suites
// in different orders, so a client following the config's order shows
// apart from one with an order of its own.
var (
	echPublished = echConfig{
		id:         0x11,
		publicName: echPublicName,
		suites: [][2]uint16{
			{hpkeKDFSHA256, hpkeAES128},
			{hpkeKDFSHA256, hpkeAES256},
			{hpkeKDFSHA256, hpkeChaCha},
		},
		served: true,
	}
	echStale = echConfig{
		id:         0x22,
		publicName: echPublicName,
		suites:     echPublished.suites,
	}
	echRetry = echConfig{
		id:            0x33,
		publicName:    echPublicName,
		maxNameLength: 64,
		suites: [][2]uint16{
			{hpkeKDFSHA256, hpkeChaCha},
			{hpkeKDFSHA256, hpkeAES256},
			{hpkeKDFSHA256, hpkeAES128},
		},
		served: true,
		retry:  true,
	}
	echStaleNoRetry = echConfig{
		id:         0x44,
		publicName: echNoRetryName,
		suites:     echPublished.suites,
	}
	echSuite = echConfig{
		id:            0x55,
		publicName:    echPublicName,
		maxNameLength: 32,
		suites: [][2]uint16{
			{hpkeKDFSHA256, hpkeAES256},
			{hpkeKDFSHA256, hpkeAES128},
			{hpkeKDFSHA256, hpkeChaCha},
		},
		served: true,
	}
	echP256 = echConfig{
		id:         0x66,
		publicName: echPublicName,
		p256:       true,
		suites:     echPublished.suites,
		served:     true,
	}
	echConfigs = []echConfig{
		echPublished,
		echStale,
		echRetry,
		echStaleNoRetry,
		echSuite,
		echP256,
	}
)

// HPKE identifiers (RFC 9180, Section 7).
const (
	hpkeKEMP256   = 0x0010
	hpkeKEMX25519 = 0x0020
	hpkeKDFSHA256 = 0x0001
	hpkeAES128    = 0x0001
	hpkeAES256    = 0x0002
	hpkeChaCha    = 0x0003
)

// echVersion is the ECHConfig version of RFC 9849 and the
// encrypted_client_hello extension's code point.
const echVersion = 0xfe0d

// echMalformed is echBadHost's ECH config list: its framing is sound, but
// its one ECHConfig ends after a byte of its contents.
var echMalformed = []byte{0x00, 0x05, 0xfe, 0x0d, 0x00, 0x01, 0x07}

// key is the config's private key.
func (c echConfig) key() *ecdh.PrivateKey {
	seed := sha256.Sum256([]byte("mimicry ech capture key " + strconv.Itoa(int(c.id))))

	curve := ecdh.X25519()
	if c.p256 {
		curve = ecdh.P256()
	}

	k, err := curve.NewPrivateKey(seed[:])
	if err != nil {
		panic(err) // a fixed seed: any 32 bytes for X25519, and below P-256's order
	}

	return k
}

// kem is the config's HPKE KEM.
func (c echConfig) kem() uint16 {
	if c.p256 {
		return hpkeKEMP256
	}

	return hpkeKEMX25519
}

// marshal is the config as an ECHConfig.
func (c echConfig) marshal() []byte {
	var b cryptobyte.Builder

	b.AddUint16(echVersion)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint8(c.id)
		b.AddUint16(c.kem())
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(c.key().PublicKey().Bytes())
		})
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			for _, s := range c.suites {
				b.AddUint16(s[0])
				b.AddUint16(s[1])
			}
		})
		b.AddUint8(c.maxNameLength)
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes([]byte(c.publicName))
		})
		b.AddUint16(0) // no extensions
	})

	return b.BytesOrPanic()
}

// echConfigList is an ECHConfigList of the given configs.
func echConfigList(configs ...echConfig) []byte {
	var b cryptobyte.Builder

	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		for _, c := range configs {
			b.AddBytes(c.marshal())
		}
	})

	return b.BytesOrPanic()
}

// echLists are the config lists the ech page's hosts publish, but
// echBadHost's, which does not parse (echMalformed).
var echLists = map[string][]echConfig{
	echHost: {
		echPublished,
	},
	echRetryHost: {
		echStale,
	},
	echNoRetryHost: {
		echStaleNoRetry,
	},
	echSuiteHost: {
		echP256,
		echSuite,
	},
}

// ECHConfigList is the ECH config list the HTTPS record of host carries
// (ECHRecord); ok is false for a host without one.
func ECHConfigList(host string) (list []byte, ok bool) {
	host = strings.ToLower(host)
	if host == echBadHost {
		return slices.Clone(echMalformed), true
	}

	configs, ok := echLists[host]
	if !ok {
		return nil, false
	}

	return echConfigList(configs...), true
}

// ECHConfigInfo is what the generators know of one of the ech page's
// configs.
type ECHConfigInfo struct {
	ID  uint8
	KEM profile.Code
	// Suites are its HPKE suites, in its order.
	Suites        []profile.HPKESuite
	MaxNameLength int
	// Retry says the server sends it as a retry config.
	Retry bool
}

// ECHHostConfigs returns the configs host's list holds, in its order;
// malformed reports the list that does not parse (echBadHost's); ok is
// false for a host that publishes none.
func ECHHostConfigs(host string) (configs []ECHConfigInfo, malformed, ok bool) {
	if host == echBadHost {
		return nil, true, true
	}

	list, ok := echLists[host]
	if !ok {
		return nil, false, false
	}

	for _, c := range list {
		configs = append(configs, c.info())
	}

	return configs, false, true
}

// ECHConfigByID is the ech page's config with this ID.
func ECHConfigByID(id uint8) (ECHConfigInfo, bool) {
	i := slices.IndexFunc(echConfigs, func(c echConfig) bool {
		return c.id == id
	})
	if i < 0 {
		return ECHConfigInfo{}, false
	}

	return echConfigs[i].info(), true
}

func (c echConfig) info() ECHConfigInfo {
	suites := make([]profile.HPKESuite, len(c.suites))
	for i, s := range c.suites {
		suites[i] = profile.HPKESuite{
			KDF:  profile.Code(s[0]),
			AEAD: profile.Code(s[1]),
		}
	}

	return ECHConfigInfo{
		ID:            c.id,
		KEM:           profile.Code(c.kem()),
		Suites:        suites,
		MaxNameLength: int(c.maxNameLength),
		Retry:         c.retry,
	}
}

// ECHRecord is the HTTPS record (RFC 9460) of name, a host of the ech
// pages, as the capture tool's DNS stub publishes it (dnsstub.Config.HTTPS):
// alpn "h2" and the host's ECH config list; ok is false for other names.
// It names no h3: the ech page stays on TCP, and the ech-h3 page offers
// HTTP/3 through Alt-Svc only.
func ECHRecord(name string) (params []dnsmessage.SVCParam, ok bool) {
	list, ok := ECHConfigList(httpsHost(name))
	if !ok {
		return nil, false
	}

	return []dnsmessage.SVCParam{
		{
			Key:   dnsmessage.SVCParamALPN,
			Value: []byte("\x02h2"),
		},
		{
			Key:   dnsmessage.SVCParamECH,
			Value: list,
		},
	}, true
}

// httpsHost is the host an HTTPS record's name is for: an https:// origin
// on another port than 443 is looked up as _<port>._https.<host> (RFC
// 9460, section 9.1), as the e2e tests' are.
func httpsHost(name string) string {
	if rest, ok := strings.CutPrefix(name, "_"); ok {
		if _, host, ok := strings.Cut(rest, "._https."); ok {
			return host
		}
	}

	return name
}

// echServerKeys are the keys the ech page's server decrypts with, for a
// ClientHelloOuter naming hello.ServerName: those with retry set are sent
// as retry configs, except from echNoRetryName.
func echServerKeys(hello *tls.ClientHelloInfo) ([]tls.EncryptedClientHelloKey, error) {
	var keys []tls.EncryptedClientHelloKey

	for _, c := range echConfigs {
		if !c.served {
			continue
		}

		keys = append(keys, tls.EncryptedClientHelloKey{
			Config:      c.marshal(),
			PrivateKey:  c.key().Bytes(),
			SendAsRetry: c.retry && hello.ServerName != echNoRetryName,
		})
	}

	return keys, nil
}

// echSendsRetry reports whether the server answers a rejected ECH offer
// to the public name with retry configs.
func echSendsRetry(publicName string) bool {
	return publicName != echNoRetryName
}

// ECH is the ech scenario: a page whose hosts the browser looks up in DNS
// (the capture tool's DNS stub, or its DNS-over-HTTPS endpoint), each with
// an HTTPS record carrying an ECH config list (ECHRecord). The server
// decrypts each ClientHelloOuter for a config it holds the key of and
// records the inner hello (capture.ECH).
var ECH = &Scenario{
	Name: "ech",
	Doc:  "Encrypted Client Hello from DNS HTTPS records: the lookups, the outer and inner hellos, resumption inside ECH, a rejection with retry configs and one without, and a config list that does not parse",
	multi: echPage{
		fixedHosts: fixedHosts{
			names: []string{
				echHost,
				echRetryHost,
				echNoRetryHost,
				echBadHost,
				echSuiteHost,
			},
			runFor: 20 * time.Second,
		},
		steps: ECHSteps,
	},
	defaultPort: true,
	ech:         true,
}

// ECHH3 is the ech-h3 scenario: the ech page's own host (ECHRecord: alpn
// h2 and an ECH config) whose every response also advertises HTTP/3 on
// the QUIC socket's port through Alt-Svc (the capture tool serves it on
// UDP 443), by a QUIC server holding the same ECH keys: whether a browser
// moves a host whose ECH config it uses to HTTP/3. Each response closes
// its connection (a GOAWAY over HTTP/3), so every request starts a new
// one. Chrome 157 and Firefox 159 sent no QUIC packet; with the same
// record without its ECH config both moved to HTTP/3 (2026-11-09).
var ECHH3 = &Scenario{
	Name: "ech-h3",
	Doc:  "HTTP/3 for a host with an ECH config: its HTTPS record (alpn h2, an ECH config) and Alt-Svc h3 on every response; whether the browser moves to QUIC, and what its QUIC ClientHello carries if it does",
	multi: echPage{
		fixedHosts: fixedHosts{
			names: []string{
				echHost,
			},
			runFor: 15 * time.Second,
		},
		steps:  ECHH3Steps,
		altSvc: true,
	},
	defaultPort: true,
	ech:         true,
	h3:          &h3Server{},
}

// ECHH3Steps are the ech-h3 page's requests after its document, all to
// its one host.
var ECHH3Steps = []string{
	"r1.ech",
	"r2.ech",
	"r3.ech",
	"r4.ech",
	"r5.ech",
}

// ECHH3Captures is H3ProfileCaptures for ech-h3, which hellogen reads for
// tls.realEch.altSvcIgnored.
func ECHH3Captures(dir string, p *profile.Spec) ([]capture.File, error) {
	return existingCaptures(dir, p, ECHH3)
}

// ECHSteps are the ech page's requests after its document, in order, by
// label (path /k/<label>); the suffix names the host: ech the page's own,
// retry, noretry, bad and suite the others. Each response closes its
// connection, so the next request starts a new one: r1.ech resumes inside
// ECH, r2.retry shows whether the retry configs of r1.retry were kept.
var ECHSteps = []string{
	"r1.ech",
	"r1.retry",
	"r2.retry",
	"r1.noretry",
	"r1.bad",
	"r1.suite",
}

// echPage is the ech scenarios' page: a document that requests steps one
// after another, then /done. With altSvc every response advertises
// HTTP/3 on the QUIC socket's port (portPair.quic).
type echPage struct {
	fixedHosts
	steps  []string
	altSvc bool
}

// certNames gives the public names certificates too: a rejected ECH offer
// is answered as the public name, whose certificate the client checks.
func (p echPage) certNames(host string) ([]string, bool) {
	if host == echPublicName || host == echNoRetryName {
		return []string{
			host,
		}, true
	}

	return p.fixedHosts.certNames(host)
}

func (p echPage) answer(req request) response {
	host, port := splitAuthority(req.host)
	path, _, _ := strings.Cut(req.path, "?")
	r := response{status: 404, contentType: "text/plain", body: []byte("not found\n")}

	switch {
	case host == echHost && path == "/":
		r = response{status: 200, contentType: "text/html; charset=utf-8", body: p.document(originOn("https://", port)), closeAfter: true}
	case path == "/done":
		r = response{status: 200, contentType: "text/plain", body: []byte("ok\n")}
	case strings.HasPrefix(path, "/k/"):
		r = response{status: 200, contentType: "text/plain", body: []byte("ok\n"), closeAfter: true}
	}

	if p.altSvc {
		r.headers = append(r.headers, altSvc(req.ports.quic))
	}

	return r
}

// document requests the page's steps 300 ms apart, so each connection's
// tickets have arrived before the next starts. The requests are no-cors
// with credentials, so every host's connections share the page's socket
// pool; a failed one moves on.
func (p echPage) document(origin func(string) string) []byte {
	hostOf := map[string]string{
		"ech":     echHost,
		"retry":   echRetryHost,
		"noretry": echNoRetryHost,
		"bad":     echBadHost,
		"suite":   echSuiteHost,
	}
	urls := make([]string, 0, len(p.steps))

	for _, label := range p.steps {
		_, suffix, _ := strings.Cut(label, ".")

		urls = append(urls, strconv.Quote(origin(hostOf[suffix])+"/k/"+label))
	}

	return htmlPage("ech", "", "<script>(async () => {\n"+
		"const pause = () => new Promise(d => setTimeout(d, 300));\n"+
		"for (const u of ["+strings.Join(urls, ", ")+"]) { await pause(); await fetch(u, {mode: \"no-cors\", credentials: \"include\"}).catch(() => {}); }\n"+
		"fetch(\"/done\");\n})();</script>\n")
}

// IsECHHost reports whether name is a host or public name of the ech page.
func IsECHHost(name string) bool {
	switch name {
	case echHost, echRetryHost, echNoRetryHost, echBadHost, echSuiteHost, echPublicName, echNoRetryName:
		return true
	}

	return false
}

// rawExtension is one extension of a ClientHello: its type, body, and the
// body's offset in the handshake message.
type rawExtension struct {
	typ  uint16
	body []byte
	at   int
}

// rawExtensions are the extensions of a ClientHello handshake message.
func rawExtensions(msg []byte) ([]rawExtension, error) {
	s := cryptobyte.String(msg)

	var body, sid, suites, compression, list cryptobyte.String

	if !s.Skip(1) || !s.ReadUint24LengthPrefixed(&body) ||
		!body.Skip(2+32) || !body.ReadUint8LengthPrefixed(&sid) ||
		!body.ReadUint16LengthPrefixed(&suites) || !body.ReadUint8LengthPrefixed(&compression) ||
		!body.ReadUint16LengthPrefixed(&list) {
		return nil, errMalformed
	}

	var exts []rawExtension

	for !list.Empty() {
		var (
			typ  uint16
			data cryptobyte.String
		)

		if !list.ReadUint16(&typ) || !list.ReadUint16LengthPrefixed(&data) {
			return nil, errMalformed
		}

		// The extensions end the message, so list ends where msg does.
		exts = append(exts, rawExtension{
			typ:  typ,
			body: data,
			at:   len(msg) - len(list) - len(data),
		})
	}

	return exts, nil
}

// openECH reads the encrypted_client_hello of msg, a ClientHelloOuter. It
// returns nil for a hello without one, and for one that is not for the ech
// scenario's configs: a GREASE extension (a public name the page's configs
// do not have) included. With the key of the config it names it decrypts
// the inner hello.
func openECH(msg []byte) (*capture.ECH, error) {
	sni := serverName(msg)
	if sni != echPublicName && sni != echNoRetryName {
		return nil, nil //nolint:nilnil // nil: no real ECH for the page's configs
	}

	exts, err := rawExtensions(msg)
	if err != nil {
		return nil, err
	}

	i := slices.IndexFunc(exts, func(e rawExtension) bool {
		return e.typ == echVersion
	})
	if i < 0 {
		return nil, nil //nolint:nilnil // nil: no encrypted_client_hello
	}

	ext := exts[i]
	s := cryptobyte.String(ext.body)

	var (
		typ, configID uint8
		kdf, aead     uint16
		enc, payload  cryptobyte.String
	)

	if !s.ReadUint8(&typ) || typ != echOuter || !s.ReadUint16(&kdf) || !s.ReadUint16(&aead) ||
		!s.ReadUint8(&configID) || !s.ReadUint16LengthPrefixed(&enc) ||
		!s.ReadUint16LengthPrefixed(&payload) || !s.Empty() {
		return nil, errors.New("ech: a malformed outer encrypted_client_hello")
	}

	ech := &capture.ECH{
		ConfigID: configID,
		KDF:      profile.Code(kdf),
		AEAD:     profile.Code(aead),
	}

	i = slices.IndexFunc(echConfigs, func(c echConfig) bool {
		return c.id == configID && c.publicName == sni
	})
	if i < 0 {
		return nil, fmt.Errorf("ech: config %#02x is not one of %s's", configID, sni)
	}

	config := echConfigs[i]

	if !config.served {
		return ech, nil
	}

	inner, err := decryptECH(msg, ext.at+len(ext.body)-len(payload), config, kdf, aead, enc, payload)
	if err != nil {
		return nil, err
	}

	return ech, readInner(ech, inner)
}

// decryptECH opens payload, which starts at offset payloadAt of msg, the
// ClientHelloOuter, for config (RFC 9849, Section 7.1).
func decryptECH(msg []byte, payloadAt int, config echConfig, kdf, aead uint16, enc, payload []byte) ([]byte, error) {
	k, err := hpke.NewDHKEMPrivateKey(config.key())
	if err != nil {
		return nil, err
	}

	h, err := hpke.NewKDF(kdf)
	if err != nil {
		return nil, err
	}

	a, err := hpke.NewAEAD(aead)
	if err != nil {
		return nil, err
	}

	info := append([]byte("tls ech\x00"), config.marshal()...)

	r, err := hpke.NewRecipient(enc, k, h, a, info)
	if err != nil {
		return nil, err
	}

	// ClientHelloOuterAAD: the outer hello's body with the payload zeroed.
	aad := slices.Clone(msg[4:])
	clear(aad[payloadAt-4 : payloadAt-4+len(payload)])

	inner, err := r.Open(aad, payload)
	if err != nil {
		return nil, fmt.Errorf("ech: config %#02x: %w", config.id, err)
	}

	return inner, nil
}

// echInnerMarker is the body of an inner hello's encrypted_client_hello
// (ECHClientHelloType inner).
const echInnerMarker = 1

// extECHOuterExtensions is ech_outer_extensions, the inner hello's list of
// extensions copied from the outer one (RFC 9849, Section 5.1).
const extECHOuterExtensions = 0xfd00

// readInner fills ech in from encoded, a decrypted EncodedClientHelloInner.
func readInner(ech *capture.ECH, encoded []byte) error {
	ech.InnerHello = hex.EncodeToString(encoded)

	s := cryptobyte.String(encoded)

	var sid, suites, compression, exts cryptobyte.String
	if !s.Skip(2+32) || !s.ReadUint8LengthPrefixed(&sid) ||
		!s.ReadUint16LengthPrefixed(&suites) || !s.ReadUint8LengthPrefixed(&compression) ||
		!s.ReadUint16LengthPrefixed(&exts) {
		return errors.New("ech: a malformed EncodedClientHelloInner")
	}

	for _, b := range s {
		if b != 0 {
			return errors.New("ech: padding that is not zeros")
		}
	}

	ech.Padding = len(s)

	body := encoded[:len(encoded)-len(s)]

	n := len(body)
	msg := append([]byte{handshakeClientHello, byte(n >> 16), byte(n >> 8), byte(n)}, body...)

	t, err := ParseClientHello(msg)
	if err != nil {
		return fmt.Errorf("ech: the inner hello: %w", err)
	}

	ech.Inner = &t

	for _, e := range t.Extensions {
		if e.Type != extECHOuterExtensions {
			continue
		}

		list := cryptobyte.String(e.Data)

		var types cryptobyte.String
		if !list.ReadUint8LengthPrefixed(&types) || !list.Empty() {
			return errors.New("ech: a malformed ech_outer_extensions")
		}

		if ech.OuterExtensions, err = readCodes(&types); err != nil {
			return err
		}
	}

	return nil
}

// echRequired reports the alert a client sends after a rejected ECH offer
// once it has checked the public name's certificate (RFC 9849, Section
// 6.1.6): the connection then ends, as the protocol wants.
func echRequired(err error) bool {
	var op *net.OpError

	return errors.As(err, &op) && op.Op == "remote error" && strings.Contains(op.Err.Error(), "encrypted client hello required")
}
