// Package dial is the single path from an address to a connected socket,
// directly or through an HTTP, HTTPS or SOCKS5 proxy (the TLS to an https
// proxy and its HTTP/2 streams are the engine's, through ProxyTLS and
// TunnelH2), and to a UDP socket for QUIC, directly, through a SOCKS5 UDP
// association, or as a CONNECT-UDP stream of the engine's HTTP/3
// connection to a proxy (ConnectUDP). Every step honours the context and
// its deadline (decision 0010); no other package opens connections.
package dial

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/roachadam/mimicry/internal/proxy"
	"github.com/roachadam/mimicry/profile"
)

// Dialer opens TCP connections, directly or through a proxy, and UDP
// sockets for QUIC. It is the one place connections start.
type Dialer struct {
	// Timeout bounds the connect (through a proxy: to the proxy, and the
	// proxy's handshake), on top of the context's deadline.
	Timeout time.Duration
	// DialContext, when set, opens the connection (to the proxy, when
	// there is one) instead of net.Dialer; Timeout still bounds it.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// Resolver looks host names up for net.Dialer; nil uses
	// net.DefaultResolver. DialContext does its own lookups.
	Resolver *net.Resolver
	// Proxy is how the profile's browser talks to proxies; nil (not
	// measured) makes proxies an error.
	Proxy *profile.Proxy
	// UserAgent is the profile's, which CONNECT requests carry.
	UserAgent string
	// BrowserAuth gives an HTTP proxy the URL's credentials as Proxy's
	// ConnectCredentials and ForwardCredentials say, after its 407.
	// Otherwise they go from the first request, placed as Proxy places
	// them unasked (ConnectAuth, ForwardAuth): only the proxy sees the
	// difference (decision 0039).
	BrowserAuth bool
	// HTTP3ReadBufferSize is the receive buffer a QUIC socket asks for
	// (SetReadBuffer; the OS may grant less): zero asks for
	// DefaultHTTP3ReadBufferSize, negative leaves the OS's default (see
	// setReadBuffer).
	HTTP3ReadBufferSize int
	// OnConnect, when set, is told how long each TCP connect took: from
	// its connect() to the connection being up, so without the lookup
	// and, through a proxy, without the tunnel's setup; with DialContext,
	// DialContext's whole call. The rtt client hint estimates from it.
	OnConnect func(time.Duration)
	// LocalAddrs, when set, are the local addresses the Dialer's own
	// sockets bind to, at most one per family: TCP connects (unless
	// DialContext opens them), QUIC's UDP sockets and SOCKS5 UDP relays.
	// A remote address of a family with none is not used.
	LocalAddrs []netip.Addr
	// ProxyTLS runs the TLS handshake with an https proxy p on conn, its
	// new connection, and returns the TLS connection: the engine's, with
	// the profile's hello. Required for https proxies; on error conn is
	// closed.
	ProxyTLS func(ctx context.Context, conn net.Conn, p *url.URL) (net.Conn, error)

	// asked holds the proxies (by URL) that have asked for credentials:
	// browsers send them unasked from then on.
	asked sync.Map
	// udpRefused holds the proxies (by URL) that refused a SOCKS5 UDP
	// association (ListenUDPVia) or CONNECT-UDP (ConnectUDP).
	udpRefused sync.Map
}

// Dial connects to addr ("host:port"). It honours ctx's cancellation and
// deadline.
func (d *Dialer) Dial(ctx context.Context, addr string) (net.Conn, error) {
	if d.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, d.Timeout)

		defer cancel()
	}

	return d.dial(ctx, addr)
}

func (d *Dialer) dial(ctx context.Context, addr string) (net.Conn, error) {
	if d.DialContext != nil {
		start := time.Now()

		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil && d.OnConnect != nil {
			d.OnConnect(time.Since(start))
		}

		return conn, err
	}

	// started is when the last connect() began: the one that succeeded,
	// unless Happy Eyeballs raced two addresses.
	var started atomic.Pointer[time.Time]

	network := "tcp"
	nd := net.Dialer{
		KeepAlive: 30 * time.Second,
		Resolver:  d.Resolver,
		ControlContext: func(_ context.Context, network, _ string, c syscall.RawConn) error {
			now := time.Now()
			started.Store(&now)

			if len(d.LocalAddrs) == 0 {
				return nil
			}

			// Each family's socket from its own address, bound before
			// connect(), so Happy Eyeballs still races the two as without
			// LocalAddrs (net.Dialer.LocalAddr binds one for both).
			local, ok := d.localFor(network == "tcp6")
			if !ok {
				return fmt.Errorf("mimicry: no %s address in Config.LocalAddrs", network)
			}

			return bindTo(c, local)
		},
	}

	// With one family's address only, the other family's server
	// addresses are not tried.
	_, v4 := d.localFor(false)
	_, v6 := d.localFor(true)

	switch {
	case v4 && !v6:
		network = "tcp4"
	case v6 && !v4:
		network = "tcp6"
	}

	conn, err := nd.DialContext(ctx, network, addr)
	if start := started.Load(); err == nil && start != nil && d.OnConnect != nil {
		d.OnConnect(time.Since(*start))
	}

	return conn, err
}

// localFor returns the local address LocalAddrs gives the IPv6 (v6) or
// IPv4 family.
func (d *Dialer) localFor(v6 bool) (netip.Addr, bool) {
	for _, a := range d.LocalAddrs {
		if a.Unmap().Is6() == v6 {
			return a.Unmap(), true
		}
	}

	return netip.Addr{}, false
}

// ListenUDP resolves host with Resolver and opens a UDP socket for a QUIC
// connection to its first address at port (with LocalAddrs, its first of
// a family they give, the socket bound to that family's), returned with
// the socket. It honours ctx, bounded by Timeout. The socket asks for the
// receive buffer HTTP3ReadBufferSize says and keeps what the OS grants.
func (d *Dialer) ListenUDP(ctx context.Context, host string, port uint16) (net.PacketConn, netip.AddrPort, error) {
	if d.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, d.Timeout)

		defer cancel()
	}

	resolver := d.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	addrs := []netip.Addr{}
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = append(addrs, a)
	} else if addrs, err = resolver.LookupNetIP(ctx, "ip", host); err != nil {
		return nil, netip.AddrPort{}, err
	}

	var (
		addr  netip.Addr
		local netip.Addr
	)

	for _, a := range addrs {
		a = a.Unmap()
		if l, ok := d.localFor(a.Is6()); ok || len(d.LocalAddrs) == 0 {
			addr, local = a, l

			break
		}
	}

	if !addr.IsValid() {
		return nil, netip.AddrPort{}, fmt.Errorf("mimicry: no address for %s of the families Config.LocalAddrs gives", host)
	}

	network, bind := "udp4", "0.0.0.0:0"
	if addr.Is6() {
		network, bind = "udp6", "[::]:0"
	}

	if local.IsValid() {
		bind = netip.AddrPortFrom(local, 0).String()
	}

	pc, err := (&net.ListenConfig{}).ListenPacket(ctx, network, bind)
	if err != nil {
		return nil, netip.AddrPort{}, err
	}

	if uc, ok := pc.(*net.UDPConn); ok {
		d.setReadBuffer(uc)
	}

	return pc, netip.AddrPortFrom(addr, port), nil
}

// DefaultHTTP3ReadBufferSize is the receive buffer a QUIC socket asks for
// by default: Chrome's (kQuicSocketReceiveBufferSize, net/quic/
// quic_context.h, set on every QUIC socket it opens).
const DefaultHTTP3ReadBufferSize = 1 << 20

// setReadBuffer asks for uc's receive buffer as HTTP3ReadBufferSize says.
// Best effort: Linux caps the request at net.core.rmem_max (stock: 208
// KB, so 416 KB granted, the kernel doubling it) without an error, and a
// failure leaves the OS's default. No SO_RCVBUFFORCE: it needs privileges.
//
// 1 MB because more is not faster across the board (bench 2026-10-29 on
// a Linux test host, Intel i5-13500, rmem_max 16 MB, nginx, HTTP/3, 3
// passes; decision 0045): against the OS's default (208 KB), a 1 MB request (2 MB
// granted) ran 100 KB bodies at 64 at once 10-13% faster and 1 KB at 256
// 11-18% faster, with no drops at 1 KB; 2 MB and 7 MB ran 100 KB at 64 at
// once 40-100% faster, but at 256 at once 7 MB ran at about half the rate
// in two runs of three, and 2 MB too: the client answers its backlog in
// bursts that overflow nginx's socket, its congestion window falls and
// MAX_DATA waits behind it while nginx idles on flow control.
func (d *Dialer) setReadBuffer(uc *net.UDPConn) {
	size := d.HTTP3ReadBufferSize
	if size == 0 {
		size = DefaultHTTP3ReadBufferSize
	}

	if size > 0 {
		_ = uc.SetReadBuffer(size)
	}
}

// Supported proxy URL schemes. An https proxy is an HTTP proxy reached
// over TLS (ProxyTLS).
const (
	SchemeHTTP    = "http"
	SchemeHTTPS   = "https"
	SchemeSOCKS5  = "socks5"
	SchemeSOCKS5H = "socks5h"
)

// CheckProxy reports whether p is a proxy URL the dialer can use. The
// engine checks that the profile describes the proxy protocol.
func (d *Dialer) CheckProxy(p *url.URL) error {
	switch {
	case p.Scheme != SchemeHTTP && p.Scheme != SchemeHTTPS && p.Scheme != SchemeSOCKS5 && p.Scheme != SchemeSOCKS5H:
		return fmt.Errorf("mimicry: proxy scheme %q is not supported (http, https, socks5, socks5h)", p.Scheme)
	case p.Host == "":
		return fmt.Errorf("mimicry: proxy URL %q has no host", p.Redacted())
	}

	return nil
}

// ProxyError is a failure to reach a proxy or open a tunnel through it
// (connect, non-2xx CONNECT, SOCKS5 refusal, deadline). The root package
// turns it into mimicry.ProxyError.
type ProxyError struct {
	// Proxy is the proxy's URL, credentials included.
	Proxy *url.URL
	// StatusCode and Header are the proxy's answer, if any.
	StatusCode int
	Header     http.Header
	Err        error
}

func (e *ProxyError) Error() string {
	return fmt.Sprintf("mimicry: proxy %s: %v", e.Proxy.Redacted(), e.Err)
}

func (e *ProxyError) Unwrap() error {
	return e.Err
}

// proxyError wraps err, a failure on the way to or through p.
func proxyError(p *url.URL, err error) error {
	return &ProxyError{
		Proxy: p,
		Err:   err,
	}
}

// answerError is p's refusal of a CONNECT: its answer a, err saying why.
func answerError(p *url.URL, a proxy.Answer, err error) error {
	return &ProxyError{
		Proxy:      p,
		StatusCode: a.StatusCode,
		Header:     a.Header,
		Err:        err,
	}
}

// DialProxy connects to the proxy p itself: for plain-HTTP requests sent
// to an http proxy whole, or an https proxy's connection, TLS done
// (ProxyTLS), for the engine to send CONNECTs on over the protocol the
// proxy picked. Timeout bounds both. A failure is a *ProxyError.
func (d *Dialer) DialProxy(ctx context.Context, p *url.URL) (net.Conn, error) {
	if d.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, d.Timeout)

		defer cancel()
	}

	conn, err := d.dialProxy(ctx, p)
	if err != nil {
		return nil, proxyError(p, err)
	}

	return conn, nil
}

// dialProxy connects to p: TCP, then TLS for an https proxy.
func (d *Dialer) dialProxy(ctx context.Context, p *url.URL) (net.Conn, error) {
	conn, err := d.dial(ctx, proxyAddr(p))
	if err != nil || p.Scheme != SchemeHTTPS {
		return conn, err
	}

	return d.ProxyTLS(ctx, conn, p)
}

// DialVia connects to addr through the proxy p, an http or SOCKS5 one: an
// HTTP proxy's CONNECT tunnel or a SOCKS5 connection, as the profile's
// browser asks for them. An https proxy's tunnels are the engine's
// (DialProxy, then ConnectOn or TunnelH2 by the protocol it picked). A
// failure before the tunnel is up is a *ProxyError.
func (d *Dialer) DialVia(ctx context.Context, p *url.URL, addr string) (net.Conn, error) {
	if d.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, d.Timeout)

		defer cancel()
	}

	conn, err := d.dial(ctx, proxyAddr(p))
	if err != nil {
		return nil, proxyError(p, err)
	}

	if p.Scheme == SchemeHTTP {
		return d.ConnectOn(ctx, conn, p, addr)
	}

	methods, err := d.socksMethods(p)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if err := proxy.SOCKS5(ctx, conn, addr, methods, p.User); err != nil {
		_ = conn.Close()
		return nil, proxyError(p, err)
	}

	return conn, nil
}

// socksMethods are the SOCKS5 methods offered to p: the profile's, and
// username and password when p's URL has credentials.
func (d *Dialer) socksMethods(p *url.URL) ([]byte, error) {
	methods := make([]byte, 0, len(d.Proxy.SOCKS5.Methods)+1)
	for _, m := range d.Proxy.SOCKS5.Methods {
		if m < 0 || m > 255 {
			return nil, fmt.Errorf("mimicry: SOCKS5 method %d is not a byte", m)
		}

		methods = append(methods, byte(m))
	}

	if p.User != nil {
		// Browsers do not authenticate to SOCKS5 proxies: with credentials
		// in the URL, username and password are offered too.
		methods = append(methods, proxy.MethodUser)
	}

	return methods, nil
}

// ListenUDPVia opens a SOCKS5 UDP association through p (a socks5 or
// socks5h proxy) for a QUIC connection to host at port, and returns it
// with the peer address its datagrams are reported from: host's address
// when host is an IP address, else the unspecified IPv4 address (the
// proxy resolves the name, as for its TCP connections, and host never
// goes to Resolver). The association authenticates as DialVia does and
// lives as long as its control connection (proxy.Association). A failure
// is a *ProxyError; a refusal of UDP ASSOCIATE is remembered
// (UDPRefused). It honours ctx, bounded by Timeout; the socket asks for
// the receive buffer HTTP3ReadBufferSize says, as ListenUDP's does.
func (d *Dialer) ListenUDPVia(ctx context.Context, p *url.URL, host string, port uint16) (net.PacketConn, netip.AddrPort, error) {
	if d.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, d.Timeout)

		defer cancel()
	}

	if p.Scheme != SchemeSOCKS5 && p.Scheme != SchemeSOCKS5H {
		return nil, netip.AddrPort{}, fmt.Errorf("mimicry: a UDP association needs a SOCKS5 proxy, not %q", p.Scheme)
	}

	peer := netip.AddrPortFrom(netip.IPv4Unspecified(), port)
	if addr, err := netip.ParseAddr(host); err == nil {
		peer = netip.AddrPortFrom(addr.Unmap(), port)
	}

	methods, err := d.socksMethods(p)
	if err != nil {
		return nil, netip.AddrPort{}, err
	}

	ctrl, err := d.dial(ctx, proxyAddr(p))
	if err != nil {
		return nil, netip.AddrPort{}, proxyError(p, err)
	}

	a, err := d.associate(ctx, ctrl, p, methods, host, port, peer)
	if err != nil {
		_ = ctrl.Close()

		if _, ok := errors.AsType[*proxy.RefusedError](err); ok {
			d.udpRefused.Store(p.String(), true)
		}

		return nil, netip.AddrPort{}, proxyError(p, err)
	}

	return a, peer, nil
}

// associate sets up a UDP association to host at port on ctrl, p's
// control connection, which the caller closes on failure.
func (d *Dialer) associate(ctx context.Context, ctrl net.Conn, p *url.URL, methods []byte, host string, port uint16, peer netip.AddrPort) (*proxy.Association, error) {
	relay, err := proxy.UDPAssociate(ctx, ctrl, methods, p.User)
	if err != nil {
		return nil, err
	}

	udp, err := d.dialRelay(ctx, ctrl, p, relay)
	if err != nil {
		return nil, err
	}

	a, err := proxy.NewAssociation(ctrl, udp, host, port, peer)
	if err != nil {
		_ = udp.Close()
		return nil, err
	}

	return a, nil
}

// UDPRefused reports whether p has refused a UDP association or
// CONNECT-UDP: its requests stay on TCP.
func (d *Dialer) UDPRefused(p *url.URL) bool {
	_, ok := d.udpRefused.Load(p.String())
	return ok
}

// Forget drops what d has learned of the proxy whose URL is via
// (url.URL.String): that it asked for credentials, or refused a UDP
// association or CONNECT-UDP. It is then a proxy d has never used.
func (d *Dialer) Forget(via string) {
	d.asked.Delete(via)
	d.udpRefused.Delete(via)
}

// dialRelay opens a UDP socket connected to relay, the address p's answer
// to UDP ASSOCIATE on ctrl gave: an unspecified IP there is the proxy's,
// the address ctrl reached.
func (d *Dialer) dialRelay(ctx context.Context, ctrl net.Conn, p *url.URL, relay string) (*net.UDPConn, error) {
	relayHost, relayPort, err := net.SplitHostPort(relay)
	if err != nil {
		return nil, err
	}

	if ip, err := netip.ParseAddr(relayHost); err == nil && ip.IsUnspecified() {
		relayHost = p.Hostname()
		if ta, ok := ctrl.RemoteAddr().(*net.TCPAddr); ok {
			relayHost = ta.AddrPort().Addr().Unmap().String()
		}
	}

	nd := net.Dialer{
		Resolver: d.Resolver,
	}

	network := "udp"
	if len(d.LocalAddrs) > 0 {
		// A literal address picks its family's local address; a name
		// goes from the first one given, over that family.
		local := d.LocalAddrs[0].Unmap()
		if ip, err := netip.ParseAddr(relayHost); err == nil {
			var ok bool
			if local, ok = d.localFor(ip.Unmap().Is6()); !ok {
				return nil, fmt.Errorf("SOCKS5 UDP relay %s: no address of its family in Config.LocalAddrs", relay)
			}
		}

		network, nd.LocalAddr = "udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, 0))
		if local.Is6() {
			network = "udp6"
		}
	}

	conn, err := nd.DialContext(ctx, network, net.JoinHostPort(relayHost, relayPort))
	if err != nil {
		return nil, fmt.Errorf("SOCKS5 UDP relay %s: %w", relay, err)
	}

	udp, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("SOCKS5 UDP relay %s: not a UDP socket", relay)
	}

	d.setReadBuffer(udp)

	return udp, nil
}

// ConnectOn opens a CONNECT tunnel to addr over HTTP/1.1 on conn, a new
// connection to the http or https proxy p, closing it on failure. With
// credentials it sends them from the first CONNECT, or with BrowserAuth as
// the profile says (after_challenge or on_challenge): a 407 is then
// answered once, on a new connection if the proxy closed this one. A
// failure is a *ProxyError.
func (d *Dialer) ConnectOn(ctx context.Context, conn net.Conn, p *url.URL, addr string) (net.Conn, error) {
	return tunnel(ctx, d, p, addr, d.Proxy.Connect, d.Proxy.ConnectAuth, &h1Exchange{
		d:    d,
		p:    p,
		addr: addr,
		conn: conn,
	})
}

// ConnectStream sends one CONNECT as a new stream of an HTTP/2 connection
// to a proxy, header its fields after the pseudo-header fields, and
// returns the proxy's answer and, after a 2xx, the tunnel.
type ConnectStream func(ctx context.Context, header []profile.Header) (proxy.Answer, net.Conn, error)

// TunnelH2 opens a tunnel to addr through the https proxy p as a stream
// of its HTTP/2 connection (open), with proxy.https's header lists and the
// credential flow of ConnectOn, the 407 answered on a new stream of the
// same connection. A failure is a *ProxyError wrapping open's error.
func (d *Dialer) TunnelH2(ctx context.Context, p *url.URL, addr string, open ConnectStream) (net.Conn, error) {
	return tunnel(ctx, d, p, addr, d.Proxy.HTTPS.Connect, d.Proxy.HTTPS.ConnectAuth, open)
}

// ConnectUDPStream sends one CONNECT-UDP request (RFC 9298) as a new
// stream of an HTTP/3 connection to a proxy, header its fields after the
// pseudo-header fields, and returns the proxy's answer and, after a 2xx,
// the stream as a socket. A connection that cannot carry CONNECT-UDP
// fails with an error wrapping ErrNoConnectUDP before sending anything.
type ConnectUDPStream func(ctx context.Context, header []profile.Header) (proxy.Answer, net.PacketConn, error)

// ErrNoConnectUDP is a proxy whose HTTP/3 connection cannot carry
// CONNECT-UDP (ConnectUDPStream).
var ErrNoConnectUDP = errors.New("the proxy offers no CONNECT-UDP")

// CONNECT-UDP requests' fields, without and with credentials: the capsule
// protocol (RFC 9297, section 3.4, as RFC 9298 asks), the profile's
// User-Agent as its CONNECTs carry it, and Proxy-Authorization. No browser
// sends CONNECT-UDP to a configured proxy, so neither the fields nor their
// order is measured (decision 0066).
var (
	connectUDPFields = []profile.Header{
		{
			Name:  "capsule-protocol",
			Value: "?1",
		},
		{
			Name: "user-agent",
		},
	}
	connectUDPAuthFields = []profile.Header{
		{
			Name:  "capsule-protocol",
			Value: "?1",
		},
		{
			Name: "user-agent",
		},
		{
			Name: "proxy-authorization",
		},
	}
)

// ConnectUDP opens a CONNECT-UDP stream through the proxy p, an HTTP/3
// one, with open (a request on its connection) and the credential flow of
// TunnelH2, the 407 answered on a new stream of the same connection. A
// proxy that cannot carry CONNECT-UDP, or refuses it whatever the target
// (refusesConnectUDP), is remembered (UDPRefused): its requests stay on
// TCP. A failure is a *ProxyError wrapping open's error.
func (d *Dialer) ConnectUDP(ctx context.Context, p *url.URL, open ConnectUDPStream) (net.PacketConn, error) {
	pc, err := tunnel(ctx, d, p, "", connectUDPFields, connectUDPAuthFields, open)
	if err == nil {
		return pc, nil
	}

	if pe, ok := errors.AsType[*ProxyError](err); ok && (refusesConnectUDP(pe.StatusCode) || errors.Is(err, ErrNoConnectUDP)) {
		d.udpRefused.Store(p.String(), true)
	}

	return nil, err
}

// refusesConnectUDP reports whether a proxy answering a CONNECT-UDP with
// code refuses it for every target: it does not understand it (400, 404,
// 405, 501) or turned the credentials down (407 after them). Others (403,
// 502, 503, 504...) may be the target's, which another may not share:
// Oxylabs refuses some UDP targets (decision 0066).
func refusesConnectUDP(code int) bool {
	switch code {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusProxyAuthRequired, http.StatusNotImplemented:
		return true
	}

	return false
}

// exchanger sends a tunnel's CONNECTs: over HTTP/1.1 on one connection
// (h1Exchange), or as streams of an HTTP/2 or HTTP/3 connection
// (ConnectStream, ConnectUDPStream). T is the tunnel.
type exchanger[T any] interface {
	// connect sends one CONNECT carrying header and returns the proxy's
	// answer and, after a 2xx, the tunnel.
	connect(ctx context.Context, header []profile.Header) (proxy.Answer, T, error)
	// again readies the CONNECT that answers a, a 407; on failure it
	// holds nothing open.
	again(ctx context.Context, a proxy.Answer) error
	// abandon closes what the exchange holds once the tunnel failed.
	abandon()
}

// tunnel is the browser's credential flow for a CONNECT to addr through
// p, its lists plain and withAuth (without and with credentials), sent by
// x.
func tunnel[T any](ctx context.Context, d *Dialer, p *url.URL, addr string, plain, withAuth []profile.Header, x exchanger[T]) (T, error) {
	var none T

	unasked := p.User != nil && (!d.BrowserAuth || d.askedConnect(p))
	header := d.connectHeader(addr, plain, "")

	if unasked {
		header = d.connectHeader(addr, withAuth, Credentials(p))
	}

	answer, tunnel, err := x.connect(ctx, header)
	switch {
	case err != nil:
		x.abandon()

		return none, proxyError(p, err)

	case answer.StatusCode/100 == 2:
		return tunnel, nil
	case answer.StatusCode != http.StatusProxyAuthRequired || unasked || p.User == nil:
		x.abandon()

		what := "CONNECT"

		if unasked {
			what = "CONNECT with credentials"
		}

		return none, answerError(p, answer, fmt.Errorf("answered %s with %s", what, answer.Status))
	}

	if err := basicChallenge(answer.Header); err != nil {
		x.abandon()

		return none, answerError(p, answer, err)
	}

	d.asked.Store(p.String(), true)

	if err := x.again(ctx, answer); err != nil {
		return none, proxyError(p, err)
	}

	answer, tunnel, err = x.connect(ctx, d.connectHeader(addr, withAuth, Credentials(p)))
	switch {
	case err != nil:
		x.abandon()

		return none, proxyError(p, err)

	case answer.StatusCode/100 != 2:
		x.abandon()

		return none, answerError(p, answer, fmt.Errorf("answered CONNECT with credentials with %s", answer.Status))
	}

	return tunnel, nil
}

// h1Exchange sends CONNECTs to addr over HTTP/1.1 on conn, a connection
// to p.
type h1Exchange struct {
	d    *Dialer
	p    *url.URL
	addr string
	conn net.Conn
}

func (x *h1Exchange) connect(ctx context.Context, header []profile.Header) (proxy.Answer, net.Conn, error) {
	lines := make([]string, len(header))
	for i, h := range header {
		lines[i] = h.Name + ": " + h.Value
	}

	answer, tunnel, err := proxy.Connect(ctx, x.conn, x.addr, lines)
	if tunnel != nil {
		// What the proxy sent past a refusal is the next answer's.
		x.conn = tunnel
	}

	return answer, tunnel, err
}

// again reconnects when the proxy closes the connection after its 407.
func (x *h1Exchange) again(ctx context.Context, a proxy.Answer) error {
	if !a.Close {
		return nil
	}

	_ = x.conn.Close()

	conn, err := x.d.dialProxy(ctx, x.p)
	if err != nil {
		return err
	}

	x.conn = conn

	return nil
}

func (x *h1Exchange) abandon() {
	_ = x.conn.Close()
}

func (s ConnectStream) connect(ctx context.Context, header []profile.Header) (proxy.Answer, net.Conn, error) {
	return s(ctx, header)
}

// again does nothing: the next CONNECT is a new stream of the same
// connection.
func (ConnectStream) again(context.Context, proxy.Answer) error {
	return nil
}

// abandon does nothing: a refused stream has ended, and the connection
// is the engine's.
func (ConnectStream) abandon() {}

func (s ConnectUDPStream) connect(ctx context.Context, header []profile.Header) (proxy.Answer, net.PacketConn, error) {
	return s(ctx, header)
}

// again does nothing: the next CONNECT-UDP is a new stream of the same
// connection.
func (ConnectUDPStream) again(context.Context, proxy.Answer) error {
	return nil
}

// abandon does nothing: a refused stream has ended, and the connection
// is the engine's.
func (ConnectUDPStream) abandon() {}

// askedConnect reports whether, the browser's way (BrowserAuth), a
// CONNECT to p carries credentials unasked: p asked before and the
// profile sends them after a challenge.
func (d *Dialer) askedConnect(p *url.URL) bool {
	if d.Proxy.ConnectCredentials != profile.CredentialsAfterChallenge {
		return false
	}

	_, asked := d.asked.Load(p.String())

	return asked
}

// Challenged records that p asked for credentials (a 407 with header h)
// on a plain-HTTP request, and reports whether they can be given: p has
// them and asks for Basic.
func (d *Dialer) Challenged(p *url.URL, h http.Header) bool {
	if p.User == nil || basicChallenge(h) != nil {
		return false
	}

	d.asked.Store(p.String(), true)

	return true
}

// ForwardAuthorization returns the Proxy-Authorization a plain-HTTP
// request to p carries unasked, or "": p's credentials from the first
// request, or with BrowserAuth once p has asked or where the profile
// always sends them.
func (d *Dialer) ForwardAuthorization(p *url.URL) string {
	if p.User == nil {
		return ""
	}

	if !d.BrowserAuth || d.Proxy.ForwardCredentials == profile.CredentialsAlways {
		return Credentials(p)
	}

	if _, asked := d.asked.Load(p.String()); asked {
		return Credentials(p)
	}

	return ""
}

// Credentials returns p's Basic Proxy-Authorization value, or "".
func Credentials(p *url.URL) string {
	if p.User == nil {
		return ""
	}

	password, _ := p.User.Password()

	return "Basic " + base64.StdEncoding.EncodeToString([]byte(p.User.Username()+":"+password))
}

// connectHeader is a CONNECT header list for addr, filled in: Host is
// addr, User-Agent the profile's, Proxy-Authorization auth.
func (d *Dialer) connectHeader(addr string, list []profile.Header, auth string) []profile.Header {
	out := make([]profile.Header, 0, len(list))
	for _, h := range list {
		value := h.Value
		switch strings.ToLower(h.Name) {
		case "host":
			value = addr
		case "user-agent":
			if value == "" {
				value = d.UserAgent
			}

		case "proxy-authorization":
			value = auth
		}

		out = append(out, profile.Header{
			Name:  h.Name,
			Value: value,
		})
	}

	return out
}

// basicChallenge checks that a 407's header h offers Basic authentication,
// the only scheme supported.
func basicChallenge(h http.Header) error {
	for _, c := range h.Values("Proxy-Authenticate") {
		if scheme, _, _ := strings.Cut(strings.TrimSpace(c), " "); strings.EqualFold(scheme, "basic") {
			return nil
		}
	}

	return fmt.Errorf("proxy asks for authentication by %q, and only Basic is supported", h.Values("Proxy-Authenticate"))
}

// proxyAddr is p's host and port; without a port, the scheme's default.
func proxyAddr(p *url.URL) string {
	if p.Port() != "" {
		return p.Host
	}

	port := "1080"
	switch p.Scheme {
	case SchemeHTTP:
		port = "80"
	case SchemeHTTPS:
		port = "443"
	}

	return net.JoinHostPort(p.Hostname(), port)
}
