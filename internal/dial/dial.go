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

	// asked holds the proxies (by URL) that have asked for credentials:
	// browsers send them unasked from then on.
	asked sync.Map
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

// ListenUDP looks host up and opens a UDP socket of its own for a QUIC
// connection to its first address at port, which it returns with the
// socket. Resolver does the lookup; DialContext, which opens TCP
// connections, has no part in it. It honours ctx's cancellation and
// deadline, bounded by Timeout.
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
			return nil, netip.AddrPort{}, fmt.Errorf("dial: no address for %s", host)
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
	return pc, netip.AddrPortFrom(addr, port), nil
}

// Supported proxy URL schemes.
const (
	SchemeHTTP    = "http"
	SchemeSOCKS5  = "socks5"
	SchemeSOCKS5H = "socks5h"
)

// CheckProxy reports whether the profile can go through p.
func (d *Dialer) CheckProxy(p *url.URL) error {
	switch {
	case p.Scheme != SchemeHTTP && p.Scheme != SchemeSOCKS5 && p.Scheme != SchemeSOCKS5H:
		return fmt.Errorf("mimicry: proxy scheme %q is not supported (http, socks5, socks5h)", p.Scheme)
	case p.Host == "":
		return fmt.Errorf("mimicry: proxy URL %q has no host", p.Redacted())
	case d.Proxy == nil:
		return errors.New("mimicry: the profile does not describe how its browser talks to proxies (not measured)")
	case p.Scheme != SchemeHTTP && d.Proxy.SOCKS5 == nil:
		return errors.New("mimicry: the profile does not describe its browser's SOCKS5 handshake (not measured)")
	case p.Scheme == SchemeHTTP && p.User != nil && d.Proxy.ConnectAuth == nil:
		return errors.New("mimicry: the profile does not describe how its browser gives a proxy credentials (not measured)")
	}
	return nil
}

// DialProxy connects to the proxy p itself, for plain-HTTP requests sent
// to it whole.
func (d *Dialer) DialProxy(ctx context.Context, p *url.URL) (net.Conn, error) {
	return d.Dial(ctx, proxyAddr(p))
}

// DialVia connects to addr through the proxy p: an HTTP proxy's CONNECT
// tunnel or a SOCKS5 connection, as the profile's browser asks for them.
func (d *Dialer) DialVia(ctx context.Context, p *url.URL, addr string) (net.Conn, error) {
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}
	conn, err := d.dial(ctx, proxyAddr(p))
	if err != nil {
		return nil, err
	}
	if p.Scheme == SchemeHTTP {
		return d.connect(ctx, conn, p, addr)
	}
	methods := make([]byte, 0, len(d.Proxy.SOCKS5.Methods)+1)
	for _, m := range d.Proxy.SOCKS5.Methods {
		if m < 0 || m > 255 {
			_ = conn.Close()
			return nil, fmt.Errorf("mimicry: SOCKS5 method %d is not a byte", m)
		}
		methods = append(methods, byte(m))
	}
	if p.User != nil {
		// Browsers do not authenticate to SOCKS5 proxies: with credentials
		// in the URL, username and password are offered too.
		methods = append(methods, proxy.MethodUser)
	}
	if err := proxy.SOCKS5(ctx, conn, addr, methods, p.User); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mimicry: proxy %s: %w", p.Host, err)
	}
	return conn, nil
}

// connect opens a CONNECT tunnel on conn, closing it on failure, with
// credentials as the profile's browser gives them: unasked once the proxy
// has asked for them (after_challenge), or only in answer to a 407
// (on_challenge), which is answered once, on the same connection (a new
// one if the proxy closed it).
func (d *Dialer) connect(ctx context.Context, conn net.Conn, p *url.URL, addr string) (net.Conn, error) {
	_, asked := d.asked.Load(p.String())
	unasked := p.User != nil && asked && d.Proxy.ConnectCredentials == profile.CredentialsAfterChallenge
	header := d.connectHeader(addr, d.Proxy.Connect, "")
	if unasked {
		header = d.connectHeader(addr, d.Proxy.ConnectAuth, Credentials(p))
	}
	answer, tunnel, err := proxy.Connect(ctx, conn, addr, header)
	switch {
	case err != nil:
		_ = conn.Close()
		return nil, fmt.Errorf("mimicry: proxy %s: %w", p.Host, err)
	case answer.StatusCode/100 == 2:
		return tunnel, nil
	case answer.StatusCode != http.StatusProxyAuthRequired || unasked || p.User == nil:
		_ = conn.Close()
		return nil, fmt.Errorf("mimicry: proxy %s answered CONNECT with %s", p.Host, answer.Status)
	}
	if err := basicChallenge(answer.Header); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mimicry: proxy %s: %w", p.Host, err)
	}
	d.asked.Store(p.String(), true)
	conn = tunnel
	if answer.Close {
		_ = conn.Close()
		if conn, err = d.dial(ctx, proxyAddr(p)); err != nil {
			return nil, err
		}
	}
	answer, tunnel, err = proxy.Connect(ctx, conn, addr, d.connectHeader(addr, d.Proxy.ConnectAuth, Credentials(p)))
	switch {
	case err != nil:
		_ = conn.Close()
		return nil, fmt.Errorf("mimicry: proxy %s: %w", p.Host, err)
	case answer.StatusCode/100 != 2:
		_ = conn.Close()
		return nil, fmt.Errorf("mimicry: proxy %s answered CONNECT with credentials with %s", p.Host, answer.Status)
	}
	return tunnel, nil
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

// ForwardAuthorization returns the Proxy-Authorization value a plain-HTTP
// request to p carries unasked: its Basic credentials from the first
// request (always) or once p has asked for them (after_challenge); else
// "". A request answering p's challenge carries Credentials instead.
func (d *Dialer) ForwardAuthorization(p *url.URL) string {
	if p.User == nil {
		return ""
	}
	if _, asked := d.asked.Load(p.String()); asked || d.Proxy.ForwardCredentials == profile.CredentialsAlways {
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
