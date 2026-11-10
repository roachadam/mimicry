package h3

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/qpack"
	"github.com/roachadam/mimicry/internal/quic/quicwire"
	"github.com/roachadam/mimicry/profile"
)

// CONNECT-UDP (RFC 9298) through an HTTP/3 proxy: an extended CONNECT
// (RFC 9220) whose UDP payloads travel as HTTP/3 datagrams (RFC 9297) in
// QUIC DATAGRAM frames, or, from the proxy, in DATAGRAM capsules on the
// stream. No browser sends it (decision 0066): nothing here is measured.

// Settings CONNECT-UDP needs from the proxy.
const (
	settingEnableConnectProtocol = 0x08 // RFC 9220
	settingH3Datagram            = 0x33 // RFC 9297
)

const (
	// capsuleDatagram is the DATAGRAM capsule's type (RFC 9297, section
	// 3.5).
	capsuleDatagram = 0x00
	// maxCapsule bounds a DATAGRAM capsule read whole; larger ones, and
	// capsules of other types, are skipped.
	maxCapsule = 1 << 16
	// udpInbox is how many datagrams wait for a UDPConn's reads; more are
	// dropped, as a full socket drops them.
	udpInbox = 256
)

// ErrNoConnectUDP is a proxy whose connection cannot carry CONNECT-UDP:
// its SETTINGS allow no extended CONNECT or no HTTP/3 datagrams, or its
// QUIC takes no DATAGRAM frames.
var ErrNoConnectUDP = errors.New("h3: the proxy offers no CONNECT-UDP (extended CONNECT with HTTP/3 datagrams)")

// ErrUDPEnded is the error of a UDPConn's reads and writes once the proxy
// has ended its stream or its connection.
var ErrUDPEnded = errors.New("h3: the proxy ended the CONNECT-UDP stream")

// ConnectUDP opens a CONNECT-UDP stream through the proxy the connection
// reaches, once its SETTINGS have arrived: :method CONNECT, :protocol
// connect-udp, :scheme https, :authority authority (the proxy) and :path
// path (the target in the proxy's URI template), the pseudo-header fields
// in the profile's HTTP/3 order with :protocol after them, then fields. It
// returns the proxy's response once its headers arrive and, for a 2xx, the
// stream as a UDPConn whose reads report peer as their source; the stream
// then outlives ctx. Any other response comes without one, its body the
// caller's to read and close. A proxy that cannot carry it, or whose
// datagrams are too small for payloads of minPayload bytes (the
// connection's packets it carries), fails with ErrNoConnectUDP before
// anything is sent.
func (c *Conn) ConnectUDP(ctx context.Context, authority, path string, fields []profile.Header, peer netip.AddrPort, minPayload int) (*http.Response, *UDPConn, error) {
	hd, err := c.connectUDPHead(authority, path, fields)
	if err != nil {
		return nil, nil, err
	}

	select {
	case <-c.settings:
	case <-c.done:
		return nil, nil, ErrConnUnusable
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	c.mu.Lock()

	ok := c.connectUDP
	c.mu.Unlock()

	// The quarter stream ID (one byte for the first streams) and the
	// context ID go in front of each payload.
	if !ok || c.qc.MaxDatagramSize() < minPayload+2 {
		return nil, nil, ErrNoConnectUDP
	}

	if !c.reserve() {
		return nil, nil, ErrConnUnusable
	}

	s, _, err := c.start(ctx, hd, false, false)
	if err != nil {
		c.release()

		return nil, nil, err
	}

	id := uint64(s.ID())
	r := &requestStream{
		c:  c,
		s:  s,
		id: id,
	}
	c.mu.Lock()

	c.requests[id] = r
	c.mu.Unlock()

	r.stop = context.AfterFunc(ctx, r.cancel)

	req := &http.Request{
		Method: http.MethodConnect,
		Proto:  "connect-udp",
		URL: &url.URL{
			Scheme: "https",
			Host:   authority,
			Path:   path,
		},
		Host:   authority,
		Header: http.Header{},
	}

	resp, err := r.readResponse(ctx, req)
	if err != nil {
		r.finish(err)

		return nil, nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, nil, nil
	}

	if !r.stop() {
		// ctx ended as the answer came: the stream is cancelled.
		_ = resp.Body.Close()

		return nil, nil, ctx.Err()
	}

	// From here on, only the UDPConn's own end ends the stream.
	s.SetReadContext(context.Background())
	s.SetWriteContext(context.Background())

	b := resp.Body.(*body) //nolint:forcetypeassert // readResponse's

	b.ctx = context.Background()
	resp.Body = http.NoBody

	u := &UDPConn{
		c:      c,
		body:   b,
		qsid:   id / 4,
		peer:   net.UDPAddrFromAddrPort(peer),
		prefix: append(quicwire.AppendVarint(nil, id/4), 0), // context ID 0: UDP payloads (RFC 9298, section 4)
		in:     make(chan []byte, udpInbox),
		done:   make(chan struct{}),
		dlWake: make(chan struct{}),
	}

	c.udpMu.Lock()

	if c.udp == nil {
		c.udp = map[uint64]*UDPConn{}
	}

	c.udp[u.qsid] = u
	c.udpMu.Unlock()

	c.udpOnce.Do(func() {
		go c.readDatagrams()
	})

	go u.readCapsules()

	return resp, u, nil
}

// connectUDPHead is a CONNECT-UDP request's field list.
func (c *Conn) connectUDPHead(authority, path string, fields []profile.Header) (head, error) {
	if !httpguts.ValidHostHeader(authority) {
		return head{}, fmt.Errorf("h3: invalid authority %q", authority)
	}

	if len(path) == 0 || path[0] != '/' || !httpguts.ValidHeaderFieldValue(path) {
		return head{}, fmt.Errorf("h3: invalid path %q", path)
	}

	pseudo := map[string]string{
		":method":    http.MethodConnect,
		":authority": authority,
		":scheme":    "https",
		":path":      path,
	}

	order := c.p.HTTP2.PseudoHeaderOrder
	if c.h3.Headers != nil {
		order = c.h3.Headers.PseudoHeaderOrder
	}

	hd := head{
		fields: make([]qpack.Field, 0, len(order)+1+len(fields)),
	}

	for _, name := range order {
		hd.fields = append(hd.fields, qpack.Field{
			Name:  name,
			Value: pseudo[name],
		})
	}

	hd.fields = append(hd.fields, qpack.Field{
		Name:  ":protocol",
		Value: "connect-udp",
	})

	for _, f := range fields {
		if !httpguts.ValidHeaderFieldName(f.Name) {
			return head{}, fmt.Errorf("h3: invalid header field name %q", f.Name)
		}

		if !httpguts.ValidHeaderFieldValue(f.Value) {
			// The value is not quoted: it may be a secret.
			return head{}, fmt.Errorf("h3: invalid value for header field %q", f.Name)
		}

		hd.fields = append(hd.fields, qpack.Field{
			Name:  f.Name,
			Value: f.Value,
		})
	}

	return hd, nil
}

// readDatagrams hands each HTTP/3 datagram to the CONNECT-UDP stream its
// quarter stream ID names, until the connection ends. Datagrams for no
// open stream, and those a full inbox has no room for, are dropped.
func (c *Conn) readDatagrams() {
	for {
		b, err := c.qc.ReceiveDatagram(context.Background())
		if err != nil {
			return
		}

		qsid, n := quicwire.ConsumeVarint(b)
		if n < 0 {
			continue
		}

		c.udpMu.Lock()

		u := c.udp[qsid]
		c.udpMu.Unlock()

		if u != nil {
			u.deliver(b[n:])
		}
	}
}

// UDPConn is a CONNECT-UDP stream as a net.PacketConn: a write sends its
// bytes to the proxy's target, whatever address it names, in one HTTP/3
// datagram; a read returns a UDP payload from the target, reported from
// the peer given to ConnectUDP. Datagrams with a context ID other than 0
// are dropped. Once the proxy ends the stream or the connection, reads and
// writes fail with ErrUDPEnded. Close ends the stream, not the connection.
type UDPConn struct {
	c    *Conn
	body *body
	qsid uint64
	peer *net.UDPAddr
	// prefix is each datagram's quarter stream ID and context ID.
	prefix []byte
	in     chan []byte

	// done is closed once err is set: the stream ended.
	errMu sync.Mutex
	err   error
	done  chan struct{}

	// wmu guards wbuf, a write's datagram.
	wmu  sync.Mutex
	wbuf []byte

	// dlMu guards the read deadline; dlWake is closed when it changes.
	dlMu     sync.Mutex
	deadline time.Time
	dlWake   chan struct{}
}

// deliver queues an HTTP/3 datagram's payload for ReadFrom.
func (u *UDPConn) deliver(b []byte) {
	select {
	case u.in <- b:
	default:
	}
}

// readCapsules reads the capsules on the stream (RFC 9297, section 3.2)
// until it ends: DATAGRAM capsules are datagrams as those in DATAGRAM
// frames, others are skipped. The stream's end ends the UDPConn.
func (u *UDPConn) readCapsules() {
	br := bufio.NewReader(u.body)

	for {
		typ, err := readVarint(br)
		if err != nil {
			u.fail(fmt.Errorf("%w: %w", ErrUDPEnded, err))

			return
		}

		length, err := readVarint(br)
		if err != nil {
			u.fail(fmt.Errorf("%w: %w", ErrUDPEnded, unexpectedEOF(err)))

			return
		}

		if typ != capsuleDatagram || length > maxCapsule {
			if err := skip(br, length); err != nil {
				u.fail(fmt.Errorf("%w: %w", ErrUDPEnded, err))

				return
			}

			continue
		}

		b := make([]byte, length)
		if _, err := io.ReadFull(br, b); err != nil {
			u.fail(fmt.Errorf("%w: %w", ErrUDPEnded, unexpectedEOF(err)))

			return
		}

		u.deliver(b)
	}
}

// fail ends the UDPConn with err, once.
func (u *UDPConn) fail(err error) {
	u.errMu.Lock()
	defer u.errMu.Unlock()

	if u.err != nil {
		return
	}

	u.err = err
	close(u.done)
}

// failed is the UDPConn's error once it has ended, else nil.
func (u *UDPConn) failed() error {
	u.errMu.Lock()
	defer u.errMu.Unlock()

	return u.err
}

// ReadFrom reads the next UDP payload from the target into p, waiting
// until one arrives, the stream ends or the read deadline passes.
func (u *UDPConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		u.dlMu.Lock()

		deadline, wake := u.deadline, u.dlWake
		u.dlMu.Unlock()

		var (
			expired <-chan time.Time
			timer   clockwork.Timer
		)

		if !deadline.IsZero() {
			left := deadline.Sub(u.c.clock.Now())
			if left <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}

			timer = u.c.clock.NewTimer(left)
			expired = timer.Chan()
		}

		var b []byte

		select {
		case b = <-u.in:
		case <-u.done:
			// What arrived before the end is read first.
			select {
			case b = <-u.in:
			default:
			}

		case <-expired:
		case <-wake:
		}

		if timer != nil {
			timer.Stop()
		}

		switch {
		case b != nil:
			if ctxID, n := quicwire.ConsumeVarint(b); n >= 0 && ctxID == 0 {
				return copy(p, b[n:]), u.peer, nil
			}

		case u.failed() != nil:
			return 0, nil, u.failed()
		case expired != nil && !u.c.clock.Now().Before(deadline):
			return 0, nil, os.ErrDeadlineExceeded
		}
	}
}

// WriteTo sends b to the target in one HTTP/3 datagram; addr is ignored.
// It never waits: a datagram the connection has no room for is dropped.
func (u *UDPConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	if err := u.failed(); err != nil {
		return 0, err
	}

	u.wmu.Lock()
	defer u.wmu.Unlock()

	u.wbuf = append(append(u.wbuf[:0], u.prefix...), b...)
	if err := u.c.qc.SendDatagram(u.wbuf); err != nil {
		return 0, err
	}

	return len(b), nil
}

// Close ends the stream (H3_REQUEST_CANCELLED both ways); the connection
// stays open.
func (u *UDPConn) Close() error {
	u.fail(net.ErrClosed)

	u.c.udpMu.Lock()
	delete(u.c.udp, u.qsid)
	u.c.udpMu.Unlock()

	return u.body.Close()
}

// LocalAddr is the unspecified address: the UDPConn has no socket of its
// own.
func (u *UDPConn) LocalAddr() net.Addr {
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv4Unspecified(), 0))
}

// SetDeadline sets the read deadline; writes never wait.
func (u *UDPConn) SetDeadline(t time.Time) error {
	return u.SetReadDeadline(t)
}

// SetReadDeadline sets the read deadline, for reads under way too.
func (u *UDPConn) SetReadDeadline(t time.Time) error {
	u.dlMu.Lock()
	defer u.dlMu.Unlock()

	u.deadline = t
	close(u.dlWake)

	u.dlWake = make(chan struct{})

	return nil
}

// SetWriteDeadline does nothing: writes never wait.
func (u *UDPConn) SetWriteDeadline(time.Time) error {
	return nil
}
