package quictrace

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"golang.org/x/crypto/cryptobyte"
)

// Received is one UDP datagram as the server's socket read or wrote it.
type Received struct {
	At   time.Time
	Data []byte
	// Server is set on a datagram the server sent to the client address.
	Server bool
	// Addr is the client address it came from or went to; one value (""
	// for all) when the caller decodes one address.
	Addr string
}

// Decode turns the client's and server's datagrams, merged in time order,
// into the client's connections: usually one per address, more if the
// client started a new connection from the same address. A client datagram
// from an unknown address joins the connection whose 1-RTT keys open it
// (migration, RFC 9000 section 9); one no connection opens is dropped.
// cidLen is the length of server-issued connection IDs; keyLog opens every
// packet type but Initial. Packets it cannot open keep their header and
// say why.
func Decode(start time.Time, datagrams []Received, cidLen int, keyLog *KeyLog) []*Conn {
	var (
		conns  []*Conn
		states []*connState
	)

	byAddr := map[string]*connState{}

	for _, r := range datagrams {
		c := byAddr[r.Addr]
		if r.Server {
			if c != nil {
				c.conn.Server = append(c.conn.Server, c.datagram(r, c.serverPacket))
			}

			continue
		}

		if next := c.newConnection(r.Data); next != nil {
			c = next
			c.keyLog = keyLog
			c.cidLen = cidLen
			c.first = r.At
			c.addrs = []string{
				r.Addr,
			}
			c.conn.Addr = r.Addr
			c.conn.Millis = r.At.Sub(start).Milliseconds()
			c.conn.Start = r.At
			byAddr[r.Addr] = c
			states = append(states, c)
			conns = append(conns, c.conn)
		}

		if c == nil {
			c = migrated(states, r.Data)
			if c == nil {
				// Nothing to attribute it to.
				continue
			}

			c.addrs = append(c.addrs, r.Addr)
			byAddr[r.Addr] = c
		}

		c.conn.Datagrams = append(c.conn.Datagrams, c.datagram(r, c.packet))
	}

	return conns
}

// migrated returns the connection, the most recent first, whose current
// 1-RTT keys (either key phase) open data, a short header packet from an
// address none of them uses yet; nil if none does.
func migrated(states []*connState, data []byte) *connState {
	if len(data) == 0 || data[0]&0x80 != 0 {
		return nil
	}

	for _, c := range slices.Backward(states) {
		pnOffset := 1 + c.cidLen
		if len(data) < pnOffset {
			continue
		}

		version := c.appVersion()
		k := c.trafficKeys(c.app, labelClientTraffic, version, data, pnOffset, true, c.largest[spaceApp])

		if k == nil {
			continue
		}

		if _, err := k.unprotect(data, pnOffset, true, c.largest[spaceApp]); err == nil {
			return c
		}

		if next, err := k.next(); err == nil {
			if _, err := next.unprotect(data, pnOffset, true, c.largest[spaceApp]); err == nil {
				return c
			}
		}
	}

	return nil
}

// connState is the decoding state of one connection.
type connState struct {
	conn   *Conn
	first  time.Time
	cidLen int
	keyLog *KeyLog
	// addrs are the client addresses it used, in order of first use.
	addrs []string
	// version is the first packet's; odcid its Destination Connection ID,
	// from which every version's Initial keys derive; scidLen the length
	// of its Source Connection ID, which the server's short headers carry.
	version uint32
	odcid   []byte
	scidLen int
	// initialVersion and handshakeVersion are the versions of the client's
	// latest Initial and Handshake packets (0 for none): its 1-RTT packets
	// are in the latter's, or before any the former's.
	initialVersion, handshakeVersion uint32
	// vnSent reports that the server sent a Version Negotiation packet.
	vnSent bool
	// The client's keys by packet type and version; app are the 1-RTT keys
	// of the latest key phase.
	initial, handshake, early, app map[uint32]*keys
	// The server's keys by packet type and version; of serverApp only the
	// header protection key is used, which key updates keep.
	serverInitial, serverHandshake, serverApp map[uint32]*keys
	// largest is the largest packet number the client sent that was opened
	// in each packet number space (Initial, Handshake, application data),
	// -1 for none; serverLargest the server's.
	largest, serverLargest [3]int64
	// hello reassembles the Initial packets' CRYPTO stream until the
	// ClientHello is complete; helloMsg is it, random its client random
	// and pskIdentity the first ticket it offers.
	hello       cryptoStream
	helloMsg    []byte
	random      []byte
	pskIdentity []byte
	// serverCrypto reassembles the server's Handshake CRYPTO stream until
	// its first message, EncryptedExtensions, is complete (serverDone).
	serverCrypto cryptoStream
	serverDone   bool
}

// Packet number spaces.
const (
	spaceInitial = iota
	spaceHandshake
	spaceApp
)

// newConnection returns a new connection's state if data starts one: an
// Initial the current connection's keys cannot open but its own DCID's
// can, or, after Version Negotiation, one in another version (RFC 9000
// section 6.2).
func (c *connState) newConnection(data []byte) *connState {
	h, err := parseLongHeader(data)
	if err != nil || h.typ != PacketInitial {
		return nil
	}

	if c != nil && (!c.vnSent || h.version == c.version) {
		if k := c.initialKeys(h.version, false); k != nil {
			if _, err := k.unprotect(data[:h.end], h.pnOffset, false, c.largest[spaceInitial]); err == nil {
				return nil
			}
		}
	}

	k, err := initialKeys(h.version, h.dcid, false)
	if err != nil {
		return nil
	}

	if _, err := k.unprotect(data[:h.end], h.pnOffset, false, -1); err != nil {
		return nil
	}

	return &connState{
		conn: &Conn{
			Version:   versionHex(h.version),
			Datagrams: []Datagram{},
		},
		version:        h.version,
		odcid:          append([]byte(nil), h.dcid...),
		scidLen:        h.scidLen,
		initialVersion: h.version,
		initial: map[uint32]*keys{
			h.version: k,
		},
		handshake:       map[uint32]*keys{},
		early:           map[uint32]*keys{},
		app:             map[uint32]*keys{},
		serverInitial:   map[uint32]*keys{},
		serverHandshake: map[uint32]*keys{},
		serverApp:       map[uint32]*keys{},
		largest:         [3]int64{-1, -1, -1},
		serverLargest:   [3]int64{-1, -1, -1},
	}
}

// initialKeys returns the client's Initial keys in a version, or with
// server the server's, derived from the connection's original Destination
// Connection ID the first time; nil for a version without them.
func (c *connState) initialKeys(version uint32, server bool) *keys {
	m := c.initial
	if server {
		m = c.serverInitial
	}

	if k, ok := m[version]; ok {
		return k
	}

	k, err := initialKeys(version, c.odcid, server)
	if err != nil {
		k = nil
	}

	m[version] = k

	return k
}

// datagram decodes one datagram's packets with packet, the client's or the
// server's packet decoder.
func (c *connState) datagram(r Received, packet func([]byte) (Packet, int, error)) Datagram {
	d := Datagram{
		Micros: r.At.Sub(c.first).Microseconds(),
		Size:   len(r.Data),
		Path:   slices.Index(c.addrs, r.Addr),
	}
	rest := r.Data

	for len(rest) > 0 {
		// A packet must have its fixed bit set; zeros after the
		// datagram's packets are padding.
		if len(d.Packets) > 0 && rest[0]&0x40 == 0 {
			d.Trailing = len(rest)
			break
		}

		p, n, err := packet(rest)
		if err != nil {
			d.Trailing = len(rest)
			break
		}

		d.Packets = append(d.Packets, p)
		rest = rest[n:]
	}

	return d
}

// longHeader is a long header packet's header fields; idsEnd is where its
// connection IDs end.
type longHeader struct {
	version       uint32
	typ           string
	dcid          []byte
	scidLen       int
	idsEnd        int
	tokenLen      *int
	length        *int
	pnOffset, end int
}

var errHeader = errors.New("malformed packet header")

// longTypes maps the long header's type bits to packet types, by version
// (RFC 9000, section 17.2; RFC 9369, section 3.2).
var longTypes = map[uint32][4]string{
	Version1: {PacketInitial, Packet0RTT, PacketHandshake, PacketRetry},
	Version2: {PacketRetry, PacketInitial, Packet0RTT, PacketHandshake},
}

// parseLongHeader reads the long header at the start of b.
func parseLongHeader(b []byte) (longHeader, error) {
	if len(b) < 7 || b[0]&0x80 == 0 {
		return longHeader{}, errHeader
	}

	h := longHeader{
		version: binary.BigEndian.Uint32(b[1:5]),
	}
	r := &reader{
		b: b[5:],
	}

	h.dcid = r.bytes(uint64(r.byte()))
	h.scidLen = len(r.bytes(uint64(r.byte())))

	if r.err != nil {
		return longHeader{}, errHeader
	}

	h.idsEnd = len(b) - len(r.b)
	if h.version == 0 {
		h.typ, h.end = PacketVN, len(b)
		return h, nil
	}

	types, ok := longTypes[h.version]
	if !ok {
		// Its type bits mean nothing to us; the rest of the datagram is
		// the packet.
		h.typ, h.end = "unknown", len(b)
		return h, nil
	}

	h.typ = types[(b[0]>>4)&0x03]
	if h.typ == PacketRetry {
		h.end = len(b)
		return h, nil
	}

	if h.typ == PacketInitial {
		h.tokenLen = new(len(r.bytes(r.varint())))
	}

	length := r.varint()
	if r.err != nil || length > uint64(len(r.b)) {
		return longHeader{}, errHeader
	}

	h.length = new(int(length))
	h.pnOffset = len(b) - len(r.b)
	h.end = h.pnOffset + int(length)

	return h, nil
}

// packet decodes the client's packet at the start of b and returns it
// with its length.
func (c *connState) packet(b []byte) (Packet, int, error) {
	if b[0]&0x80 == 0 {
		if len(b) < 1+c.cidLen {
			return Packet{}, 0, errHeader
		}

		p := Packet{
			Type:       Packet1RTT,
			DCIDLength: c.cidLen,
			Size:       len(b),
		}
		c.open(&p, b, 1+c.cidLen, true, c.appVersion())

		return p, len(b), nil
	}

	h, p, err := longPacket(b)
	if err != nil {
		return Packet{}, 0, err
	}

	switch h.typ {
	case PacketInitial:
		c.initialVersion = h.version
	case PacketHandshake:
		c.handshakeVersion = h.version
		c.conn.NegotiatedVersion = ""

		if h.version != c.version {
			c.conn.NegotiatedVersion = versionHex(h.version)
		}
	}

	switch h.typ {
	case PacketInitial, PacketHandshake, Packet0RTT:
		c.open(&p, b[:h.end], h.pnOffset, false, h.version)
	default:
		p.Error = "not a packet a client protects"
	}

	return p, h.end, nil
}

// serverPacket decodes the server's packet at the start of b and returns
// it with its length. 1-RTT packets are decoded to the header only.
func (c *connState) serverPacket(b []byte) (Packet, int, error) {
	if b[0]&0x80 == 0 {
		pnOffset := 1 + c.scidLen
		if len(b) < pnOffset {
			return Packet{}, 0, errHeader
		}

		p := Packet{
			Type:       Packet1RTT,
			FirstByte:  b[0] & 0xe0,
			DCIDLength: c.scidLen,
			Size:       len(b),
		}
		if k := c.serverAppKeys(c.appVersion(), b, pnOffset); k != nil {
			if _, o, err := k.unmask(b, pnOffset, true, c.serverLargest[spaceApp]); err == nil {
				p.FirstByte, p.PNLength, p.PN = o.firstByte, o.pnLen, o.pn
				c.serverLargest[spaceApp] = max(c.serverLargest[spaceApp], int64(o.pn))
			}
		}

		return p, len(b), nil
	}

	h, p, err := longPacket(b)
	if err != nil {
		return Packet{}, 0, err
	}

	switch h.typ {
	case PacketInitial, PacketHandshake:
		c.openServer(&p, b[:h.end], h.pnOffset, h.version)
	case PacketVN:
		c.vnSent = true

		list := b[h.idsEnd:h.end]

		for ; len(list) >= 4; list = list[4:] {
			p.Versions = append(p.Versions, versionHex(binary.BigEndian.Uint32(list)))
		}

		if len(list) > 0 {
			p.Error = "version list not a multiple of 4 bytes"
		}

	case PacketRetry:
		p.TokenLength = new(max(0, h.end-h.idsEnd-16))
		if err := verifyRetry(h.version, b[:h.end], c.odcid); err != nil {
			p.Error = err.Error()
		}

	default:
		p.Error = "not a packet a server protects"
	}

	return p, h.end, nil
}

// longPacket reads the long header at the start of b into a Packet.
func longPacket(b []byte) (longHeader, Packet, error) {
	h, err := parseLongHeader(b)
	if err != nil {
		return longHeader{}, Packet{}, err
	}

	p := Packet{
		Type:        h.typ,
		FirstByte:   b[0],
		Version:     versionHex(h.version),
		DCIDLength:  len(h.dcid),
		SCIDLength:  new(h.scidLen),
		TokenLength: h.tokenLen,
		Length:      h.length,
		Size:        h.end,
	}

	return h, p, nil
}

// open removes the protection of the client's packet with the keys of its
// type and version and reads its frames into p.
func (c *connState) open(p *Packet, b []byte, pnOffset int, short bool, version uint32) {
	space, k := c.keysFor(p.Type, version, b, pnOffset, short)
	if k == nil {
		c.noKeys(p, b, short)

		return
	}

	o, err := k.unprotect(b, pnOffset, short, c.largest[space])
	if err != nil && short {
		// A new key phase (RFC 9001, section 6).
		if next, nerr := k.next(); nerr == nil {
			if no, oerr := next.unprotect(b, pnOffset, short, c.largest[space]); oerr == nil {
				c.app[version] = next
				o, err = no, nil
			}
		}
	}

	crypto, ok := readOpened(p, o, err, &c.largest[space])
	if !ok {
		return
	}

	if p.Type == Packet0RTT {
		c.conn.EarlyData = true
	}

	if p.Type == PacketInitial {
		for _, d := range crypto {
			c.hello.add(d)
		}

		c.helloDone()
	}
}

// openServer opens the server's Initial or Handshake packet into p, and
// reads its Handshake CRYPTO stream up to the transport parameters.
func (c *connState) openServer(p *Packet, b []byte, pnOffset int, version uint32) {
	space, k := spaceInitial, c.initialKeys(version, true)
	if p.Type == PacketHandshake {
		space = spaceHandshake
		k = c.trafficKeys(c.serverHandshake, labelServerHandshake, version, b, pnOffset, false, c.serverLargest[space])
	}

	if k == nil {
		c.noKeys(p, b, false)

		return
	}

	o, err := k.unprotect(b, pnOffset, false, c.serverLargest[space])
	crypto, ok := readOpened(p, o, err, &c.serverLargest[space])

	if !ok || p.Type != PacketHandshake || c.serverDone {
		return
	}

	for _, d := range crypto {
		c.serverCrypto.add(d)
	}

	c.encryptedExtensions(p)
}

// noKeys records in p that there are no keys to open packet b: only the
// first byte's unprotected bits.
func (c *connState) noKeys(p *Packet, b []byte, short bool) {
	p.FirstByte = b[0] & 0xf0
	if short {
		p.FirstByte = b[0] & 0xe0
	}

	p.Error = "no keys"
	if c.random == nil {
		p.Error = "no keys: the ClientHello was not complete"
	}
}

// readOpened records an opened packet in p and raises largest to its
// packet number; it returns the CRYPTO data, and false if err is set.
func readOpened(p *Packet, o opened, err error, largest *int64) ([]cryptoData, bool) {
	if err != nil {
		p.FirstByte = 0
		p.Error = "cannot open: " + err.Error()

		return nil, false
	}

	p.FirstByte, p.PNLength, p.PN = o.firstByte, o.pnLen, o.pn
	*largest = max(*largest, int64(o.pn))

	frames, crypto, err := parseFrames(o.payload)

	p.Frames = frames

	if err != nil {
		p.Error = "frames: " + err.Error()
	}

	return crypto, true
}

// appVersion is the version of 1-RTT packets: of the client's latest
// Handshake packet, or before any its latest Initial's.
func (c *connState) appVersion() uint32 {
	if c.handshakeVersion != 0 {
		return c.handshakeVersion
	}

	return c.initialVersion
}

// serverAppKeys returns the server's 1-RTT keys from SERVER_TRAFFIC_SECRET_0,
// in the connection's cipher suite or, before one is known, the suite that
// authenticates b. Only the header protection key is used; key updates
// keep it.
func (c *connState) serverAppKeys(version uint32, b []byte, pnOffset int) *keys {
	if k := c.serverApp[version]; k != nil {
		return k
	}

	secret := c.keyLog.secret(c.random, labelServerTraffic)
	if secret == nil {
		return nil
	}

	var k *keys
	if s, ok := c.suite(); ok {
		var err error
		if k, err = newKeys(s, version, secret); err != nil {
			return nil
		}
	} else if k = keysForSecret(version, secret, b, pnOffset, true, c.serverLargest[spaceApp]); k == nil {
		return nil
	}

	c.serverApp[version] = k

	return k
}

// suite is the connection's cipher suite, from any keys derived from its
// key log secrets.
func (c *connState) suite() (suite, bool) {
	for _, m := range []map[uint32]*keys{c.serverHandshake, c.handshake, c.app} {
		for _, k := range m {
			if k != nil {
				return k.suite, true
			}
		}
	}

	return suite{}, false
}

// keysFor returns the packet number space and keys of a client packet's
// type and version, deriving them the first time.
func (c *connState) keysFor(typ string, version uint32, b []byte, pnOffset int, short bool) (int, *keys) {
	switch typ {
	case PacketInitial:
		return spaceInitial, c.initialKeys(version, false)
	case PacketHandshake:
		return spaceHandshake, c.trafficKeys(c.handshake, labelClientHandshake, version, b, pnOffset, short, c.largest[spaceHandshake])
	case Packet0RTT:
		return spaceApp, c.trafficKeys(c.early, labelClientEarly, version, b, pnOffset, short, c.largest[spaceApp])
	}

	return spaceApp, c.trafficKeys(c.app, labelClientTraffic, version, b, pnOffset, short, c.largest[spaceApp])
}

// trafficKeys returns the keys of m in a version, deriving them the first
// time from the key log's secret under label: those of its suites whose
// AEAD authenticates packet b (read as unprotect does).
func (c *connState) trafficKeys(m map[uint32]*keys, label string, version uint32, b []byte, pnOffset int, short bool, largest int64) *keys {
	if k := m[version]; k != nil {
		return k
	}

	secret := c.keyLog.secret(c.random, label)
	if secret == nil && label == labelClientEarly {
		secret = c.keyLog.earlySecret(c.pskIdentity, c.helloMsg)
	}

	if secret == nil {
		return nil
	}

	k := keysForSecret(version, secret, b, pnOffset, short, largest)
	if k != nil {
		m[version] = k
	}

	return k
}

// helloDone fills in the connection's ClientHello once the CRYPTO stream
// holds all of it.
func (c *connState) helloDone() {
	if c.random != nil {
		return
	}

	msg := c.hello.message()
	if msg == nil {
		return
	}

	c.conn.ClientHello = hex.EncodeToString(msg)

	h, err := readHello(msg)
	if err != nil {
		c.conn.HelloError = err.Error()
		return
	}

	c.random, c.helloMsg, c.pskIdentity = h.random, msg, h.pskIdentity
	c.conn.Host = h.serverName
	c.conn.Resumed = h.pskIdentity != nil

	if h.transportParameters != nil {
		if c.conn.TransportParameters, err = ParseTransportParameters(h.transportParameters); err != nil {
			c.conn.HelloError = err.Error()
		}
	}
}

// encryptedExtensions reads the server's transport parameters once its
// EncryptedExtensions is complete; failures go to p.
func (c *connState) encryptedExtensions(p *Packet) {
	msg := c.serverCrypto.message()
	if msg == nil {
		return
	}

	c.serverDone = true
	c.serverCrypto = cryptoStream{}

	params, err := readEncryptedExtensions(msg)
	if err == nil {
		c.conn.ServerTransportParameters, err = ParseTransportParameters(params)
	}

	if err != nil && p.Error == "" {
		p.Error = "EncryptedExtensions: " + err.Error()
	}
}

// ClientHelloBytes returns the connection's reassembled ClientHello
// message, or nil.
func (c *Conn) ClientHelloBytes() []byte {
	b, err := hex.DecodeString(c.ClientHello)
	if err != nil || len(b) == 0 {
		return nil
	}

	return b
}

// cryptoStream reassembles a CRYPTO stream from offset 0.
type cryptoStream struct {
	data    []byte
	covered []bool
}

// maxHello bounds the reassembly buffer; ClientHellos are a few KiB.
const maxHello = 1 << 16

func (s *cryptoStream) add(d cryptoData) {
	end := d.offset + uint64(len(d.data))
	if end > maxHello {
		return
	}

	if int(end) > len(s.data) {
		s.data = append(s.data, make([]byte, int(end)-len(s.data))...)
		s.covered = append(s.covered, make([]bool, int(end)-len(s.covered))...)
	}

	copy(s.data[d.offset:], d.data)

	for i := d.offset; i < end; i++ {
		s.covered[i] = true
	}
}

// message returns the first handshake message once all of it arrived.
func (s *cryptoStream) message() []byte {
	if len(s.data) < 4 || !s.covered[0] || !s.covered[1] || !s.covered[2] || !s.covered[3] {
		return nil
	}

	n := 4 + (int(s.data[1])<<16 | int(s.data[2])<<8 | int(s.data[3]))
	if len(s.data) < n {
		return nil
	}

	for _, ok := range s.covered[:n] {
		if !ok {
			return nil
		}
	}

	return append([]byte(nil), s.data[:n]...)
}

// hello is what Decode reads from a ClientHello itself.
type hello struct {
	random              []byte
	serverName          string
	pskIdentity         []byte
	transportParameters []byte
}

// TLS extensions read here.
const (
	extServerName          = 0x0000
	extPreSharedKey        = 0x0029
	extTransportParameters = 0x0039
)

// readHello reads a ClientHello message's random and the extensions
// Decode needs.
func readHello(msg []byte) (hello, error) {
	s := cryptobyte.String(msg)

	var (
		typ                                        uint8
		body, sessionID, suites, compression, exts cryptobyte.String
		h                                          hello
	)

	if !s.ReadUint8(&typ) || typ != 1 || !s.ReadUint24LengthPrefixed(&body) ||
		!body.Skip(2) || !body.ReadBytes(&h.random, 32) ||
		!body.ReadUint8LengthPrefixed(&sessionID) ||
		!body.ReadUint16LengthPrefixed(&suites) ||
		!body.ReadUint8LengthPrefixed(&compression) ||
		!body.ReadUint16LengthPrefixed(&exts) {
		return hello{}, errors.New("malformed ClientHello")
	}

	h.random = append([]byte(nil), h.random...)
	for !exts.Empty() {
		var (
			typ  uint16
			data cryptobyte.String
		)

		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return hello{}, errors.New("malformed ClientHello extensions")
		}

		switch typ {
		case extServerName:
			var (
				list, name cryptobyte.String
				nameType   uint8
			)

			if !data.ReadUint16LengthPrefixed(&list) || !list.ReadUint8(&nameType) || !list.ReadUint16LengthPrefixed(&name) {
				return hello{}, fmt.Errorf("malformed server_name")
			}

			h.serverName = string(name)

		case extPreSharedKey:
			var identities, identity cryptobyte.String
			if !data.ReadUint16LengthPrefixed(&identities) || !identities.ReadUint16LengthPrefixed(&identity) {
				return hello{}, fmt.Errorf("malformed pre_shared_key")
			}

			h.pskIdentity = append([]byte{}, identity...)

		case extTransportParameters:
			h.transportParameters = append([]byte{}, data...)
		}
	}

	return h, nil
}

// readEncryptedExtensions returns the body of an EncryptedExtensions
// message's quic_transport_parameters extension (RFC 9001, section 8.2).
func readEncryptedExtensions(msg []byte) ([]byte, error) {
	s := cryptobyte.String(msg)

	var (
		typ        uint8
		body, exts cryptobyte.String
	)

	if !s.ReadUint8(&typ) {
		return nil, errors.New("empty handshake message")
	}

	if typ != 8 {
		return nil, fmt.Errorf("the first handshake message is of type %d, not EncryptedExtensions", typ)
	}

	if !s.ReadUint24LengthPrefixed(&body) || !body.ReadUint16LengthPrefixed(&exts) {
		return nil, errors.New("malformed EncryptedExtensions")
	}

	for !exts.Empty() {
		var (
			ext  uint16
			data cryptobyte.String
		)

		if !exts.ReadUint16(&ext) || !exts.ReadUint16LengthPrefixed(&data) {
			return nil, errors.New("malformed EncryptedExtensions extensions")
		}

		if ext == extTransportParameters {
			return append([]byte(nil), data...), nil
		}
	}

	return nil, errors.New("no quic_transport_parameters in EncryptedExtensions")
}
