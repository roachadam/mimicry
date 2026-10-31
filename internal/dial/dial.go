// Package dial is the single path from an address to a connected socket,
// directly or through an HTTP or SOCKS5 proxy, and to a UDP socket for
// QUIC, directly or through a SOCKS5 UDP association. Every step honours
// the context and its deadline (decision 0010); no other package opens
// connections.
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

	// asked holds the proxies (by URL) that have asked for credentials:
	// browsers send them unasked from then on.
	asked sync.Map
	// udpRefused holds the SOCKS5 proxies (by URL) that refused a UDP
	// association (ListenUDPVia).
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
		return d.DialContext(ctx, "tcp", addr)
	}

	nd := net.Dialer{
		KeepAlive: 30 * time.Second,
		Resolver:  d.Resolver,
	}

	return nd.DialContext(ctx, "tcp", addr)
}

// ListenUDP resolves host with Resolver and opens a UDP socket for a QUIC
// connection to its first address at port, returned with the socket. It
// honours ctx, bounded by Timeout. The socket asks for the receive buffer
// HTTP3ReadBufferSize says and keeps what the OS grants.
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

	addr, err := netip.ParseAddr(host)
	if err != nil {
		addrs, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, netip.AddrPort{}, err
		}

		if len(addrs) == 0 {
			return nil, netip.AddrPort{}, fmt.Errorf("mimicry: no address for %s", host)
		}

		addr = addrs[0]
	}

	addr = addr.Unmap()

	network, local := "udp4", "0.0.0.0:0"

	if addr.Is6() {
		network, local = "udp6", "[::]:0"
	}

	pc, err := (&net.ListenConfig{}).ListenPacket(ctx, network, local)
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

// Supported proxy URL schemes.
const (
	SchemeHTTP    = "http"
	SchemeSOCKS5  = "socks5"
	SchemeSOCKS5H = "socks5h"
)

// CheckProxy reports whether p is a proxy URL the dialer can use. The
// engine checks that the profile describes the proxy protocol.
func (d *Dialer) CheckProxy(p *url.URL) error {
	switch {
	case p.Scheme != SchemeHTTP && p.Scheme != SchemeSOCKS5 && p.Scheme != SchemeSOCKS5H:
		return fmt.Errorf("mimicry: proxy scheme %q is not supported (http, socks5, socks5h)", p.Scheme)
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

// DialProxy connects to the proxy p itself, for plain-HTTP requests sent
// to it whole. A failure is a *ProxyError.
func (d *Dialer) DialProxy(ctx context.Context, p *url.URL) (net.Conn, error) {
	conn, err := d.Dial(ctx, proxyAddr(p))
	if err != nil {
		return nil, proxyError(p, err)
	}

	return conn, nil
}

// DialVia connects to addr through the proxy p: an HTTP proxy's CONNECT
// tunnel or a SOCKS5 connection, as the profile's browser asks for them.
// A failure before the tunnel is up is a *ProxyError.
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
		return d.connect(ctx, conn, p, addr)
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

// UDPRefused reports whether p has refused a UDP association: its
// requests stay on TCP.
func (d *Dialer) UDPRefused(p *url.URL) bool {
	_, ok := d.udpRefused.Load(p.String())
	return ok
}

// Forget drops what d has learned of the proxy whose URL is via
// (url.URL.String): that it asked for credentials, or refused a UDP
// association. It is then a proxy d has never used.
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

	conn, err := nd.DialContext(ctx, "udp", net.JoinHostPort(relayHost, relayPort))
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

// connect opens a CONNECT tunnel on conn, closing it on failure. With
// credentials it sends them from the first CONNECT, or with BrowserAuth as
// the profile says (after_challenge or on_challenge): a 407 is then
// answered once, on a new connection if the proxy closed this one.
func (d *Dialer) connect(ctx context.Context, conn net.Conn, p *url.URL, addr string) (net.Conn, error) {
	unasked := p.User != nil && (!d.BrowserAuth || d.askedConnect(p))
	header := d.connectHeader(addr, d.Proxy.Connect, "")

	if unasked {
		header = d.connectHeader(addr, d.Proxy.ConnectAuth, Credentials(p))
	}

	answer, tunnel, err := proxy.Connect(ctx, conn, addr, header)
	switch {
	case err != nil:
		_ = conn.Close()
		return nil, proxyError(p, err)

	case answer.StatusCode/100 == 2:
		return tunnel, nil
	case answer.StatusCode != http.StatusProxyAuthRequired || unasked || p.User == nil:
		_ = conn.Close()

		what := "CONNECT"

		if unasked {
			what = "CONNECT with credentials"
		}

		return nil, answerError(p, answer, fmt.Errorf("answered %s with %s", what, answer.Status))
	}

	if err := basicChallenge(answer.Header); err != nil {
		_ = conn.Close()
		return nil, answerError(p, answer, err)
	}

	d.asked.Store(p.String(), true)

	conn = tunnel
	if answer.Close {
		_ = conn.Close()
		if conn, err = d.dial(ctx, proxyAddr(p)); err != nil {
			return nil, proxyError(p, err)
		}
	}

	answer, tunnel, err = proxy.Connect(ctx, conn, addr, d.connectHeader(addr, d.Proxy.ConnectAuth, Credentials(p)))
	switch {
	case err != nil:
		_ = conn.Close()
		return nil, proxyError(p, err)

	case answer.StatusCode/100 != 2:
		_ = conn.Close()
		return nil, answerError(p, answer, fmt.Errorf("answered CONNECT with credentials with %s", answer.Status))
	}

	return tunnel, nil
}

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

// connectHeader is a CONNECT header list for addr: Host is addr,
// User-Agent the profile's, Proxy-Authorization auth.
func (d *Dialer) connectHeader(addr string, list []profile.Header, auth string) []string {
	lines := make([]string, 0, len(list))
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

		lines = append(lines, h.Name+": "+value)
	}

	return lines
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
	if p.Scheme == SchemeHTTP {
		port = "80"
	}

	return net.JoinHostPort(p.Hostname(), port)
}
