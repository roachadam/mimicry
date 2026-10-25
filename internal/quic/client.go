package quic

// mimicry: client connections that build their packets as a browser does
// (profile.QUIC). Not in upstream; see UPSTREAM.md.

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	tls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/internal/quic/quicwire"
	"github.com/roachadam/mimicry/profile"
)

// DialConfig is what a client connection needs besides its peer's address.
type DialConfig struct {
	// Profile is the browser's QUIC section: how the connection builds,
	// numbers, pads and acknowledges its packets, and its transport
	// parameters. Required.
	Profile *profile.QUIC
	// Hello is the connection's ClientHello (handshake.Spec of the plan's
	// QUIC hello). It must carry quic_transport_parameters, whose body the
	// connection fills in, and pre_shared_key exactly when TLS's session
	// cache holds the ticket it resumes. A spec serves one connection.
	Hello *tls.ClientHelloSpec
	// TLS has the server name, the roots and the session cache
	// (handshake.QUICConfig). Required, with ServerName set.
	TLS *tls.Config
	// Token is the token of the last NEW_TOKEN frame the server sent on an
	// earlier connection, sent in this one's Initial packets when the
	// profile uses tokens; nil for none.
	Token []byte
	// OnToken, if set, is called with each NEW_TOKEN token the server sends
	// (a copy), on the connection's goroutine: it must not block.
	OnToken func([]byte)
	// CachedRTT is the smoothed RTT of the connection whose ticket Hello
	// resumes (Conn.SmoothedRTT), for a profile that sends it when resuming
	// (Chrome's initial_rtt); zero leaves that parameter out.
	CachedRTT time.Duration
}

// ErrIdleTimeout is the error of a connection closed by its idle timeout
// (RFC 9000, section 10.1): Conn.Wait and Conn.Err return it.
var ErrIdleTimeout = errIdleTimeout

// Dial opens a client connection to peer over pc, which it owns: one
// connection per socket, as browsers do (a zero-length connection ID
// leaves nothing else to demultiplex on). pc is closed when the
// connection ends.
//
// The connection is returned corked (Conn.Cork): the application's first
// writes leave together, as the browser sends them, at the first Uncork.
// When Hello resumes a ticket that allows 0-RTT, Dial returns at once,
// before anything is sent: streams written before Uncork go out in 0-RTT
// packets with the Initial flight. Otherwise it returns when the handshake
// completes, with the client's Finished held until Uncork, so the first
// writes go with it. The caller must Uncork once it has written them.
func Dial(ctx context.Context, pc net.PacketConn, peer netip.AddrPort, cfg DialConfig) (*Conn, error) {
	cc, config, err := newClientConfig(cfg)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	e, err := newSoleEndpoint(pc) //nolint:contextcheck // the endpoint runs for the connection's lifetime, not Dial's
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	peer = netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port())
	config.client = cc
	c, err := e.newConn(time.Now(), config, clientSide, newServerConnIDs{}, cfg.TLS.ServerName, peer) //nolint:contextcheck // as newSoleEndpoint
	if err != nil {
		_ = e.Close(ctx)
		return nil, err
	}
	if c.client.early {
		return c, nil
	}
	if err := c.waitReady(ctx); err != nil {
		c.Abort(nil)
		return nil, err
	}
	return c, nil
}

// Cork holds the connection's packets: what the application writes and
// flushes until the matching Uncork leaves together, its STREAM frames in
// the order the streams were flushed. Corks nest. A handshake in progress
// is not held, apart from the client's Finished once it is ready.
func (c *Conn) Cork() {
	c.cork.Add(1)
}

// Uncork releases a Cork; at the last one the held packets are sent.
func (c *Conn) Uncork() {
	if c.cork.Add(-1) == 0 {
		c.wake()
	}
}

// EarlyData reports whether the connection sends 0-RTT data: it resumes a
// ticket that allows it, and the server has not rejected it.
func (c *Conn) EarlyData() bool {
	return c.client != nil && c.client.earlyOK.Load()
}

// Done is closed when the connection has ended; Err then says why.
func (c *Conn) Done() <-chan struct{} {
	return c.lifetime.donec
}

// Err is why the connection ended (nil before Done is closed): nil for a
// close with NO_ERROR, ErrIdleTimeout, a *ConnectionCloseError for the
// peer's application close, or another error.
func (c *Conn) Err() error {
	return c.finalError()
}

// SmoothedRTT is the connection's smoothed round-trip time (RFC 9002,
// section 5.3).
func (c *Conn) SmoothedRTT() time.Duration {
	ch := make(chan time.Duration, 1)
	if err := c.runOnLoop(context.Background(), func(_ time.Time, c *Conn) {
		ch <- c.loss.rtt.smoothedRTT
	}); err != nil {
		return 0
	}
	return <-ch
}

// Confirmed is closed once the handshake is confirmed: the server's
// HANDSHAKE_DONE arrived (RFC 9001, section 4.1.2).
func (c *Conn) Confirmed() <-chan struct{} {
	return c.client.confirmed
}

// clientKeepAlive is the longest a client connection with a request
// outstanding stays silent: Chrome's ping alarm with open streams
// (quiche's 15 s ping timeout); the keep-alive comes sooner, at half the
// idle timeout, when that is shorter (neqo's stream keep-alive).
const clientKeepAlive = 15 * time.Second

// KeepAlive turns keep-alives on (while the application has a request
// outstanding) or off: a PING once the connection has been silent for
// clientKeepAlive or half its idle timeout. Without it a client waiting
// for a response is silent, and a server that cannot reach it (its path
// to the client changed, the probe of the new path lost) never learns of
// it before the idle timeout. An idle connection without requests still
// ends silently.
func (c *Conn) KeepAlive(on bool) {
	c.client.keepAlive.Store(on)
}

// keepAlivePeriod is the client's keep-alive period: zero while off.
func (cc *clientState) keepAlivePeriod() time.Duration {
	if !cc.keepAlive.Load() {
		return 0
	}
	return clientKeepAlive
}

// clientState is a client connection's browser behaviour: the profile's
// rules with this connection's draws made.
type clientState struct {
	p     *profile.QUIC
	hello *tls.ClientHelloSpec
	// token is sent in Initial packets.
	token   []byte
	onToken func([]byte)
	// scid and dcid are the client's Source Connection ID and the first
	// Initial's Destination Connection ID.
	scid []byte
	dcid []byte
	// params is the quic_transport_parameters body; local is it parsed.
	params []byte
	local  transportParameters
	// resumed: the hello offers a ticket.
	resumed bool
	// initialPN is the Initial space's first packet number.
	initialPN packetNumber
	// ackDelay is the constant ACK Delay field of 1-RTT ACKs; -1 reports
	// the actual delay.
	ackDelay unscaledAckDelay
	rng      *rand.Rand

	// early: the ClientHello offered 0-RTT and its keys are set. earlyOK
	// is early until the server rejects it.
	early   bool
	earlyOK atomic.Bool
	// keys0RTT protects 0-RTT packets.
	keys0RTT fixedKeys
	// nextPN is the next packet number with PacketNumbersShared.
	nextPN packetNumber
	// flight is the planned first flight still to send; planned once it
	// is laid out.
	flight  []plannedPacket
	planned bool
	// ackFirst: send the pending Initial and Handshake ACKs in datagrams
	// of their own before the Finished.
	ackFirst bool
	// resumeOffered: the hello offers a ticket uTLS took (its
	// QUICResumeSession event). handshakeComplete: the TLS handshake is.
	resumeOffered     bool
	handshakeComplete bool
	// spinRecv is the spin bit of the server's 1-RTT packet with the
	// largest number; spinSeen once one arrived.
	spinRecv bool
	spinSeen bool
	// confirmed is closed once the handshake is confirmed.
	confirmed chan struct{}
	// peerParams is the server's quic_transport_parameters body, stored
	// with its tickets for 0-RTT.
	peerParams []byte
	// keepAlive: the application has a request outstanding (KeepAlive).
	keepAlive atomic.Bool
}

// newClientConfig validates cfg and makes the connection's draws.
func newClientConfig(cfg DialConfig) (*clientState, *Config, error) {
	p := cfg.Profile
	switch {
	case p == nil:
		return nil, nil, errors.New("quic: no QUIC profile")
	case cfg.Hello == nil:
		return nil, nil, errors.New("quic: no ClientHello")
	case cfg.TLS == nil || cfg.TLS.ServerName == "":
		return nil, nil, errors.New("quic: TLS config without a server name")
	case p.Version != quicVersion1:
		return nil, nil, fmt.Errorf("quic: version %#x; only version 1 is supported", p.Version)
	case p.DatagramSize < smallestMaxDatagramSize || p.DatagramSize > maxUDPPayloadSize:
		return nil, nil, fmt.Errorf("quic: datagram size %d outside [%d, %d]", p.DatagramSize, smallestMaxDatagramSize, maxUDPPayloadSize)
	case p.SCIDLength > maxConnIDLen || p.DCIDLength < 8 || p.DCIDLength > maxConnIDLen:
		return nil, nil, fmt.Errorf("quic: connection ID lengths %d/%d", p.DCIDLength, p.SCIDLength)
	}
	var seed [32]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		return nil, nil, err
	}
	rng := rand.New(rand.NewChaCha8(seed)) //nolint:gosec // per-connection draws, seeded from crypto/rand
	cc := &clientState{
		p:         p,
		hello:     cfg.Hello,
		onToken:   cfg.OnToken,
		rng:       rng,
		ackDelay:  -1,
		confirmed: make(chan struct{}),
	}
	if p.Tokens && len(cfg.Token) > 0 {
		cc.token = cloneBytes(cfg.Token)
	}
	for _, e := range cfg.Hello.Extensions {
		if _, ok := e.(*tls.UtlsPreSharedKeyExtension); ok {
			cc.resumed = true
		}
	}
	dcidLen := p.DCIDLength
	if p.DCIDLengthRule == profile.DCIDLengthBiased {
		v := rng.IntN(256)
		dcidLen = max(p.DCIDLength, 5+(v&(v>>4)))
	}
	cc.dcid = randomBytes(rng, dcidLen)
	cc.scid = randomBytes(rng, p.SCIDLength)
	switch p.PacketNumbers {
	case profile.PacketNumbersShared:
		cc.nextPN = 1
		cc.initialPN = 1
	case profile.PacketNumbersRandomInitial:
		a, b := rng.IntN(256), rng.IntN(256)
		cc.initialPN = packetNumber(1 + (a & 0x1f) + (max(b, 224)-224)*32)
	case profile.PacketNumbersPerSpace:
	default:
		return nil, nil, fmt.Errorf("quic: packet numbers %q", p.PacketNumbers)
	}
	if p.AckDelay == profile.AckDelayConstant {
		if p.AckDelayConstant == nil {
			return nil, nil, errors.New("quic: constant ACK delay without a range")
		}
		cc.ackDelay = unscaledAckDelay(drawRange(rng, *p.AckDelayConstant))
	}
	params, err := marshalProfileParams(p, cc.scid, cc.resumed, cfg.CachedRTT, rng)
	if err != nil {
		return nil, nil, err
	}
	cc.params = params
	if cc.local, err = unmarshalTransportParams(params); err != nil {
		return nil, nil, fmt.Errorf("quic: profile transport parameters: %w", err)
	}
	config := &Config{
		TLSConfig:            cfg.TLS,
		MaxBidiRemoteStreams: limitOrNegative(cc.local.initialMaxStreamsBidi),
		MaxUniRemoteStreams:  limitOrNegative(cc.local.initialMaxStreamsUni),
		// The connection-level window; streams' windows are the
		// per-type parameters (clientState.streamWindow).
		MaxConnReadBufferSize:   limitOrNegative(cc.local.initialMaxData),
		MaxStreamReadBufferSize: limitOrNegative(cc.local.initialMaxStreamDataBidiLocal),
		MaxIdleTimeout:          cc.local.maxIdleTimeout,
	}
	if config.MaxIdleTimeout == 0 {
		// No max_idle_timeout: no local limit (the peer's applies).
		config.MaxIdleTimeout = -1
	}
	return cc, config, nil
}

// streamWindow is the receive window of a stream of type typ, opened by
// this side when local: the transport parameter the client sent for it.
func (cc *clientState) streamWindow(typ streamType, local bool) int64 {
	switch {
	case typ == uniStream:
		return cc.local.initialMaxStreamDataUni
	case local:
		return cc.local.initialMaxStreamDataBidiLocal
	default:
		return cc.local.initialMaxStreamDataBidiRemote
	}
}

// maxAckDelay is how long the client delays an acknowledgement: the
// max_ack_delay it sent.
func (cc *clientState) maxAckDelay() time.Duration {
	return cc.local.maxAckDelay
}

// limitOrNegative maps a transport parameter's absent (zero) value to a
// Config's negative "zero" setting.
func limitOrNegative(v int64) int64 {
	if v == 0 {
		return -1
	}
	return v
}

func randomBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32()) //nolint:gosec // a random byte: truncation is the point
	}
	return b
}

// drawRange returns a uniform integer in r.
func drawRange(rng *rand.Rand, r profile.Range) int {
	return r.Min + rng.IntN(r.Max-r.Min+1)
}

// localConnIDLen is the length of the connection IDs the peer sends to.
func (c *Conn) localConnIDLen() int {
	if c.client != nil {
		return len(c.client.scid)
	}
	return connIDLen
}

// spinBitMask is the latency spin bit of a 1-RTT packet's first byte (RFC
// 9000, section 17.4).
const spinBitMask = 0x20

// closeReply is the CONNECTION_CLOSE a client sends in reply to the
// peer's (QUIC.CloseReply): NO_ERROR, frameType the peer's frame.
type closeReply struct {
	frameType uint64
}

func (e closeReply) Error() string {
	return "quic: reply to the peer's CONNECTION_CLOSE"
}

// clientPeerClosed answers the peer's CONNECTION_CLOSE, a frame of
// frameType, as the profile does: a close of its own, or nothing.
func (c *Conn) clientPeerClosed(now time.Time, frameType uint64) {
	if c.client.p.CloseReply {
		c.enterClosing(now, closeReply{
			frameType: frameType,
		})
		return
	}
	c.enterDraining(now)
}

// Frame types of extensions a profile's transport parameters announce.
const (
	frameTypeImmediateAck     = 0x1f // draft-ietf-quic-ack-frequency
	frameTypeDatagram         = 0x30 // RFC 9221
	frameTypeDatagramWithLen  = 0x31
	frameTypeAckFrequency     = 0xaf // draft-ietf-quic-ack-frequency
	paramMaxDatagramFrameSize = 0x20
)

// minAckDelayParams are min_ack_delay's codepoints: the draft's
// (Firefox's 0xff02de1a) and later ones.
var minAckDelayParams = []profile.TransportParameterID{
	0xff02de1a,
	0xff04de1a,
	0xff04de1b,
}

// handleExtensionFrame consumes a frame of an extension the client
// announced, and returns its length; -1 for any other frame type. The
// client sends no DATAGRAM frames and keeps its own ACK timing, so it drops
// DATAGRAM frames and ignores ACK_FREQUENCY and IMMEDIATE_ACK (what a
// browser does with them is not measured).
func (c *Conn) handleExtensionFrame(_ time.Time, ptype packetType, b []byte) int {
	cc := c.client
	if cc == nil || ptype&(packetType0RTT|packetType1RTT) == 0 {
		return -1
	}
	typ, n := quicwire.ConsumeVarint(b)
	if n < 0 {
		return -1
	}
	switch {
	case (typ == frameTypeDatagram || typ == frameTypeDatagramWithLen) && cc.announces(paramMaxDatagramFrameSize):
		if typ == frameTypeDatagram {
			return len(b)
		}
		_, m := quicwire.ConsumeVarintBytes(b[n:])
		if m < 0 {
			return -1
		}
		return n + m
	case typ == frameTypeImmediateAck && cc.announcesAny(minAckDelayParams):
		return n
	case typ == frameTypeAckFrequency && cc.announcesAny(minAckDelayParams):
		// Sequence number, threshold, requested max ack delay, reordering
		// threshold.
		for range 4 {
			_, m := quicwire.ConsumeVarint(b[n:])
			if m < 0 {
				return -1
			}
			n += m
		}
		return n
	}
	return -1
}

// announces reports whether the client sent transport parameter id.
func (cc *clientState) announces(id profile.TransportParameterID) bool {
	for _, tp := range cc.p.TransportParameters {
		if tp.ID == id {
			return tp.Value == nil || *tp.Value != 0
		}
	}
	return false
}

func (cc *clientState) announcesAny(ids []profile.TransportParameterID) bool {
	for _, id := range ids {
		if cc.announces(id) {
			return true
		}
	}
	return false
}

// localActiveConnIDLimit is the active_connection_id_limit the connection
// sent: how many of the peer's connection IDs it keeps.
func (c *Conn) localActiveConnIDLimit() int {
	if c.client != nil {
		return int(c.client.local.activeConnIDLimit)
	}
	return activeConnIDLimit
}

// newSoleEndpoint is NewEndpoint for one client connection (Endpoint.sole):
// no listening, and no stateless resets.
func newSoleEndpoint(conn net.PacketConn) (*Endpoint, error) {
	var pc packetConn
	var err error
	switch conn := conn.(type) {
	case *net.UDPConn:
		pc, err = newNetUDPConn(conn)
	default:
		pc, err = newNetPacketConn(conn)
	}
	if err != nil {
		return nil, err
	}
	e := &Endpoint{
		packetConn:  pc,
		conns:       make(map[*Conn]struct{}),
		acceptQueue: newQueue[*Conn](),
		closec:      make(chan struct{}),
		sole:        true,
	}
	e.resetGen.init([32]byte{})
	e.connsMap.init()
	go e.listen()
	return e, nil
}
