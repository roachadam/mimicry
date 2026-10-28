package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// UDPAssociate asks a SOCKS5 proxy over conn, which becomes the
// association's control connection, for a UDP association (RFC 1928,
// section 7), offering methods and authenticating as SOCKS5 does. It
// returns the relay's address as the proxy gives it ("host:port"); an
// unspecified IP there is the proxy's own address, which the caller
// substitutes. A refusal is a *RefusedError.
func UDPAssociate(ctx context.Context, conn net.Conn, methods []byte, user *url.Userinfo) (string, error) {
	defer watch(ctx, conn)()

	if err := socksAuth(conn, methods, user); err != nil {
		return "", contextError(ctx, err)
	}

	// The address the datagrams will come from is not known here (a NAT
	// may change it): zeros, as RFC 1928 allows, and the proxy takes the
	// first datagram's.
	relay, err := socksRequest(conn, socksUDPAssociate, "0.0.0.0", 0)
	if err != nil {
		return "", contextError(ctx, err)
	}

	return relay, nil
}

// ErrAssociationEnded is the error of an Association's reads and writes
// once its control connection has closed or failed: the proxy has dropped
// the association (RFC 1928, section 7).
var ErrAssociationEnded = errors.New("the SOCKS5 proxy ended the UDP association: its control connection closed")

// relayBuffer is an Association's receive buffer: the largest UDP
// payload, so that no datagram from the relay is cut short.
const relayBuffer = 1<<16 - 1

// Association is a SOCKS5 UDP association carrying one connection's
// datagrams to one target. It is a net.PacketConn: a write goes to the
// target whatever address it names, behind the SOCKS5 UDP header, and a
// read is a datagram from the relay without it, reported from the peer
// address given to NewAssociation. Its socket is connected to the relay,
// so nothing it sends can go anywhere else. Fragments (FRAG other than 0)
// and malformed datagrams are dropped. It ends with its control
// connection: once that closes or fails, reads and writes fail with
// ErrAssociationEnded. Close closes both.
type Association struct {
	udp    *net.UDPConn
	ctrl   net.Conn
	header []byte
	peer   *net.UDPAddr
	// ended is closed once the control connection has ended, before the
	// socket is closed; closed is set by Close.
	ended  chan struct{}
	closed atomic.Bool

	rmu  sync.Mutex
	rbuf []byte
	wmu  sync.Mutex
	wbuf []byte
}

// NewAssociation starts an association over udp, a socket connected to
// the relay, for the target host at port: an IP address goes as one, a
// host name as one for the proxy to resolve. ctrl is the control
// connection UDPAssociate set it up on; peer is the source address reads
// report.
func NewAssociation(ctrl net.Conn, udp *net.UDPConn, host string, port uint16, peer netip.AddrPort) (*Association, error) {
	// RSV (2 bytes) and FRAG, then the target.
	header, err := appendAddr([]byte{0, 0, 0}, host, port)
	if err != nil {
		return nil, err
	}

	a := &Association{
		udp:    udp,
		ctrl:   ctrl,
		header: header,
		peer:   net.UDPAddrFromAddrPort(peer),
		ended:  make(chan struct{}),
		rbuf:   make([]byte, relayBuffer),
	}

	go a.watch()

	return a, nil
}

// watch ends the association when its control connection ends; the proxy
// sends nothing on it after the reply.
func (a *Association) watch() {
	_, _ = io.Copy(io.Discard, a.ctrl)

	close(a.ended)

	_ = a.udp.Close()
}

// fail is err, a failure on the socket, as reads and writes return it.
func (a *Association) fail(err error) error {
	select {
	case <-a.ended:
		if !a.closed.Load() {
			return ErrAssociationEnded
		}

	default:
	}

	return err
}

// ReadFrom reads the target's next datagram into p.
func (a *Association) ReadFrom(p []byte) (int, net.Addr, error) {
	a.rmu.Lock()
	defer a.rmu.Unlock()

	for {
		n, err := a.udp.Read(a.rbuf)
		if err != nil {
			return 0, nil, a.fail(err)
		}

		if payload, ok := udpPayload(a.rbuf[:n]); ok {
			return copy(p, payload), a.peer, nil
		}
	}
}

// WriteTo sends b to the association's target; addr is ignored.
func (a *Association) WriteTo(b []byte, _ net.Addr) (int, error) {
	a.wmu.Lock()
	defer a.wmu.Unlock()

	a.wbuf = append(append(a.wbuf[:0], a.header...), b...)
	if _, err := a.udp.Write(a.wbuf); err != nil {
		return 0, a.fail(err)
	}

	return len(b), nil
}

// Close ends the association: its control connection and its socket.
func (a *Association) Close() error {
	a.closed.Store(true)

	_ = a.ctrl.Close()

	return a.udp.Close()
}

// LocalAddr is the socket's local address.
func (a *Association) LocalAddr() net.Addr {
	return a.udp.LocalAddr()
}

// SetDeadline sets the socket's read and write deadlines.
func (a *Association) SetDeadline(t time.Time) error {
	return a.udp.SetDeadline(t)
}

// SetReadDeadline sets the socket's read deadline.
func (a *Association) SetReadDeadline(t time.Time) error {
	return a.udp.SetReadDeadline(t)
}

// SetWriteDeadline sets the socket's write deadline.
func (a *Association) SetWriteDeadline(t time.Time) error {
	return a.udp.SetWriteDeadline(t)
}

// udpPayload is a datagram from the relay without its SOCKS5 UDP header;
// ok is false for a fragment (FRAG other than 0, which RFC 1928 lets a
// client drop) or a malformed header.
func udpPayload(b []byte) (payload []byte, ok bool) {
	if len(b) < 4 || b[2] != 0 {
		return nil, false
	}

	var n int
	switch b[3] {
	case socksIPv4:
		n = 4 + net.IPv4len + 2
	case socksIPv6:
		n = 4 + net.IPv6len + 2
	case socksDomain:
		if len(b) < 5 {
			return nil, false
		}

		n = 5 + int(b[4]) + 2

	default:
		return nil, false
	}

	if len(b) < n {
		return nil, false
	}

	return b[n:], true
}
