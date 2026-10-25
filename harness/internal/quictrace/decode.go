package quictrace

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/cryptobyte"
)

// Received is one UDP datagram as the server's socket read it.
type Received struct {
	At   time.Time
	Data []byte
}

// Decode turns the datagrams one client address sent, in order of
// arrival, into its connections: usually one; more if the client sent a
// new connection's Initial from the same address. start is when the
// server started serving the page; cidLen the length of the connection IDs
// the server issues, which short headers carry without a length; keyLog
// the server's key log, for every packet type but Initial. Packets it
// cannot open keep their header and say why.
func Decode(start time.Time, datagrams []Received, cidLen int, keyLog *KeyLog) []*Conn {
	var conns []*Conn
	var c *connState
	for _, r := range datagrams {
		if next := c.newConnection(r.Data); next != nil {
			c = next
			c.keyLog = keyLog
			c.cidLen = cidLen
			c.first = r.At
			c.conn.Millis = r.At.Sub(start).Milliseconds()
			conns = append(conns, c.conn)
		}
		if c == nil {
			// Datagrams before any Initial: nothing to attribute them to.
			continue
		}
		c.datagram(r)
	}
	return conns
}

// connState is the decoding state of one connection.
type connState struct {
	conn   *Conn
	first  time.Time
	cidLen int
	keyLog *KeyLog
	// version is the first packet's.
	version uint32
	// Keys by packet type; app are the 1-RTT keys of the latest key phase.
	initial, handshake, early, app *keys
	// largest is the largest packet number opened in each packet number
	// space (Initial, Handshake, application data), -1 for none.
	largest [3]int64
	// hello reassembles the Initial packets' CRYPTO stream until the
	// ClientHello is complete; helloMsg is it, random its client random
	// and pskIdentity the first ticket it offers.
	hello       cryptoStream
	helloMsg    []byte
	random      []byte
	pskIdentity []byte
}

// Packet number spaces.
const (
	spaceInitial = iota
	spaceHandshake
	spaceApp
)

// newConnection returns the state of a new connection if datagram data
// starts one: an Initial that the current connection's keys (if any)
// cannot open but keys from its own Destination Connection ID can.
func (c *connState) newConnection(data []byte) *connState {
	h, err := parseLongHeader(data)
	if err != nil || h.typ != PacketInitial {
		return nil
	}
	if c != nil && c.initial != nil {
		if _, err := c.initial.unprotect(data[:h.end], h.pnOffset, false, c.largest[spaceInitial]); err == nil {
			return nil
		}
	}
	k, err := clientInitialKeys(h.version, h.dcid)
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
		version: h.version,
		initial: k,
		largest: [3]int64{-1, -1, -1},
	}
}

// datagram decodes one datagram's packets into the connection.
func (c *connState) datagram(r Received) {
	d := Datagram{
		Micros: r.At.Sub(c.first).Microseconds(),
		Size:   len(r.Data),
	}
	rest := r.Data
	for len(rest) > 0 {
		// A packet must have its fixed bit set; zeros after the
		// datagram's packets are padding.
		if len(d.Packets) > 0 && rest[0]&0x40 == 0 {
			d.Trailing = len(rest)
			break
		}
		p, n, err := c.packet(rest)
		if err != nil {
			d.Trailing = len(rest)
			break
		}
		d.Packets = append(d.Packets, p)
		rest = rest[n:]
	}
	c.conn.Datagrams = append(c.conn.Datagrams, d)
}

// longHeader is a long header packet's header fields.
type longHeader struct {
	version       uint32
	typ           string
	dcid          []byte
	scidLen       int
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
	h.length = new(int(length)) //nolint:gosec // at most the datagram's length
	h.pnOffset = len(b) - len(r.b)
	h.end = h.pnOffset + int(length) //nolint:gosec // at most the datagram's length
	return h, nil
}

// packet decodes the packet at the start of b and returns it with its
// length.
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
		c.open(&p, b, 1+c.cidLen, true)
		return p, len(b), nil
	}
	h, err := parseLongHeader(b)
	if err != nil {
		return Packet{}, 0, err
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
	switch h.typ {
	case PacketInitial, PacketHandshake, Packet0RTT:
		c.open(&p, b[:h.end], h.pnOffset, false)
	default:
		p.Error = "not a packet a client protects"
	}
	return p, h.end, nil
}

// open removes the packet's protection with the keys of its type and
// reads its frames into p.
func (c *connState) open(p *Packet, b []byte, pnOffset int, short bool) {
	space, k := c.keysFor(p.Type, b, pnOffset, short)
	if k == nil {
		p.FirstByte = 0
		if short {
			p.FirstByte = b[0] & 0xe0
		} else {
			p.FirstByte = b[0] & 0xf0
		}
		p.Error = "no keys"
		if c.random == nil {
			p.Error = "no keys: the ClientHello was not complete"
		}
		return
	}
	o, err := k.unprotect(b, pnOffset, short, c.largest[space])
	if err != nil && short {
		// A new key phase (RFC 9001, section 6).
		if next, nerr := k.next(); nerr == nil {
			if no, oerr := next.unprotect(b, pnOffset, short, c.largest[space]); oerr == nil {
				c.app = next
				o, err = no, nil
			}
		}
	}
	if err != nil {
		p.FirstByte = 0
		p.Error = "cannot open: " + err.Error()
		return
	}
	p.FirstByte, p.PNLength, p.PN = o.firstByte, o.pnLen, o.pn
	c.largest[space] = max(c.largest[space], int64(o.pn)) //nolint:gosec // packet numbers are below 2^62
	frames, crypto, err := parseFrames(o.payload)
	p.Frames = frames
	if err != nil {
		p.Error = "frames: " + err.Error()
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

// keysFor returns the packet number space and keys of a packet type,
// deriving them from the key log the first time.
func (c *connState) keysFor(typ string, b []byte, pnOffset int, short bool) (int, *keys) {
	switch typ {
	case PacketInitial:
		return spaceInitial, c.initial
	case PacketHandshake:
		if c.handshake == nil {
			c.handshake = c.derive(labelClientHandshake, b, pnOffset, short, spaceHandshake)
		}
		return spaceHandshake, c.handshake
	case Packet0RTT:
		if c.early == nil {
			c.early = c.derive(labelClientEarly, b, pnOffset, short, spaceApp)
		}
		return spaceApp, c.early
	}
	if c.app == nil {
		c.app = c.derive(labelClientTraffic, b, pnOffset, short, spaceApp)
	}
	return spaceApp, c.app
}

func (c *connState) derive(label string, b []byte, pnOffset int, short bool, space int) *keys {
	secret := c.keyLog.secret(c.random, label)
	if secret == nil && label == labelClientEarly {
		secret = c.keyLog.earlySecret(c.pskIdentity, c.helloMsg)
	}
	if secret == nil {
		return nil
	}
	return keysForSecret(c.version, secret, b, pnOffset, short, c.largest[space])
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
	var typ uint8
	var body, sessionID, suites, compression, exts cryptobyte.String
	var h hello
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
		var typ uint16
		var data cryptobyte.String
		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return hello{}, errors.New("malformed ClientHello extensions")
		}
		switch typ {
		case extServerName:
			var list, name cryptobyte.String
			var nameType uint8
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
