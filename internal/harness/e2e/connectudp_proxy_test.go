package e2e

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profile"
)

// connectUDPProxy is a CONNECT-UDP proxy (RFC 9298) over HTTP/3, on
// quic-go's http3.Server, for TestQUICInterop's connect-udp rows, at
// interopHost (the stub resolver maps every name to the IPv4 loopback
// address) with the interop leaf certificate. It answers a CONNECT-UDP to
// the default URI template with a UDP socket to the loopback address at
// the target's port, and relays the stream's HTTP/3 datagrams (context ID
// 0) both ways until the stream or the connection ends.
type connectUDPProxy struct {
	srv  *http3.Server
	port uint16
	// user and password, when user is set, are the credentials it asks
	// for (407 with a Basic challenge without them).
	user, password string

	mu sync.Mutex
	// refuse, when set, is the status it answers every CONNECT-UDP with.
	refuse int
	// conns counts the QUIC connections to it; requests are the
	// CONNECT-UDP requests in order; relays the local addresses of the
	// relay sockets, which the target sees the client's datagrams from.
	conns    int
	requests []connectUDPRequest
	relays   []netip.AddrPort
	// streams are the open tunnels' streams, for dropStreams.
	streams map[*http3.Stream]struct{}
}

// connectUDPRequest is a CONNECT-UDP request the proxy received.
type connectUDPRequest struct {
	path    string
	capsule string
	auth    bool
	status  int
}

// newConnectUDPProxy starts a CONNECT-UDP proxy, stopped when the test
// ends. Without datagrams its SETTINGS and transport parameters announce
// none, so a client cannot use it for CONNECT-UDP.
func newConnectUDPProxy(e *interopEnv, datagrams bool, user, password string) *connectUDPProxy {
	t := e.t
	t.Helper()

	pc, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.IPv4(127, 0, 0, 1),
	})
	if err != nil {
		t.Fatal(err)
	}

	p := &connectUDPProxy{
		port:     netip.MustParseAddrPort(pc.LocalAddr().String()).Port(),
		user:     user,
		password: password,
		streams:  map[*http3.Stream]struct{}{},
	}

	p.srv = &http3.Server{
		Handler:         p,
		EnableDatagrams: datagrams,
		TLSConfig: http3.ConfigureTLSConfig(&tls.Config{
			Certificates: []tls.Certificate{
				e.pki.leaf,
			},
			MinVersion: tls.VersionTLS13,
		}),
		QUICConfig: &quic.Config{
			EnableDatagrams: datagrams,
			// Room for a site's datagram (up to quic-go's 1280 at
			// first) in a DATAGRAM frame of the proxy's packet.
			InitialPacketSize: 1400,
		},
		ConnContext: func(ctx context.Context, _ *quic.Conn) context.Context {
			p.mu.Lock()

			p.conns++
			p.mu.Unlock()

			return ctx
		},
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = p.srv.Serve(pc)
	}()

	t.Cleanup(func() {
		_ = p.srv.Close()
		_ = pc.Close()

		<-done
	})

	return p
}

// url is the proxy's URL with user, if set.
func (p *connectUDPProxy) url(user *url.Userinfo) *url.URL {
	return &url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(interopHost, strconv.Itoa(int(p.port))),
		User:   user,
	}
}

// setRefuse makes the proxy answer every CONNECT-UDP with status from now
// on (zero: serve them).
func (p *connectUDPProxy) setRefuse(status int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.refuse = status
}

// stats returns the connections to the proxy, its CONNECT-UDP requests and
// its relay sockets' addresses so far.
func (p *connectUDPProxy) stats() (conns int, requests []connectUDPRequest, relays []netip.AddrPort) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.conns, append([]connectUDPRequest(nil), p.requests...), append([]netip.AddrPort(nil), p.relays...)
}

// dropStreams ends every open tunnel, as a proxy dropping them does: the
// stream ends both ways and its relay closes.
func (p *connectUDPProxy) dropStreams() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for s := range p.streams {
		s.CancelRead(quic.StreamErrorCode(http3.ErrCodeNoError))

		_ = s.Close()
	}
}

// ServeHTTP answers one CONNECT-UDP request and, after a 200, relays its
// datagrams until the stream ends.
func (p *connectUDPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	port, target := connectUDPTarget(r)
	rec := connectUDPRequest{
		path:    r.URL.Path,
		capsule: r.Header.Get("Capsule-Protocol"),
		auth:    r.Header.Get("Proxy-Authorization") != "",
		status:  http.StatusOK,
	}

	p.mu.Lock()

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(p.user+":"+p.password))

	switch {
	case r.Method != http.MethodConnect || r.Proto != "connect-udp" || !target:
		rec.status = http.StatusBadRequest
	case p.refuse != 0:
		rec.status = p.refuse
	case p.user != "" && r.Header.Get("Proxy-Authorization") != want:
		rec.status = http.StatusProxyAuthRequired
	}

	p.requests = append(p.requests, rec)
	p.mu.Unlock()

	if rec.status != http.StatusOK {
		if rec.status == http.StatusProxyAuthRequired {
			w.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
		}

		w.WriteHeader(rec.status)

		return
	}

	relay, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)))
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)

		return
	}

	defer func() {
		_ = relay.Close()
	}()

	w.Header().Set("Capsule-Protocol", "?1")
	w.WriteHeader(http.StatusOK)

	s := h3Stream(w)

	p.mu.Lock()

	p.relays = append(p.relays, relay.LocalAddr().(*net.UDPAddr).AddrPort()) //nolint:forcetypeassert // a UDP socket's address
	p.streams[s] = struct{}{}
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.streams, s)
		p.mu.Unlock()
	}()

	go func() {
		buf := make([]byte, 1<<16)
		for {
			n, err := relay.Read(buf[1:])
			if err != nil {
				return
			}

			buf[0] = 0 // context ID 0: a UDP payload
			// One too large for the proxy's packets is dropped, as a
			// proxy drops it (RFC 9298, section 5).
			_ = s.SendDatagram(buf[:1+n])
		}
	}()

	for {
		b, err := s.ReceiveDatagram(r.Context())
		if err != nil {
			return
		}

		// No QUIC packet is empty (and udprelay takes none).
		if len(b) < 2 || b[0] != 0 {
			continue
		}

		if _, err := relay.Write(b[1:]); err != nil {
			return
		}
	}
}

// connectUDPTarget reads the target's port from r's path, the default URI
// template's (/.well-known/masque/udp/{host}/{port}/); ok is false for
// another path.
func connectUDPTarget(r *http.Request) (port uint16, ok bool) {
	rest, found := strings.CutPrefix(r.URL.Path, "/.well-known/masque/udp/")
	if !found {
		return 0, false
	}

	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[2] != "" {
		return 0, false
	}

	n, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return 0, false
	}

	return uint16(n), true
}

// viaConnectUDP returns e with a new Transport of its Config sending every
// request through the HTTP proxy tcp (DialContext reaching it) and HTTP/3
// through the CONNECT-UDP proxy udp (Config.ConnectUDPProxy), credentials
// the browser's way with browserAuth, with cache as its Alt-Svc cache
// (nil: its own).
func (e *interopEnv) viaConnectUDP(tcp, udp *url.URL, browserAuth bool, cache *mimicry.AltSvcCache) *interopEnv {
	next := *e

	next.cfg.Proxy = http.ProxyURL(tcp)
	next.cfg.DialContext = nil
	next.cfg.ConnectUDPProxy = func(*url.URL) (*url.URL, error) {
		return udp, nil
	}
	next.cfg.BrowserProxyAuth = browserAuth
	next.cfg.AltSvc = cache
	next.setTransport(next.cfg)

	return &next
}

// connectUDPPath is the default URI template's path for the interop host
// at port.
func connectUDPPath(port uint16) string {
	return fmt.Sprintf("/.well-known/masque/udp/%s/%d/", interopHost, port)
}

// connectUDPProfile reports whether p's QUIC and HTTP/3 carry datagrams,
// which CONNECT-UDP needs: its transport parameters announce
// max_datagram_frame_size (0x20) and its SETTINGS H3_DATAGRAM (0x33).
func connectUDPProfile(p *profile.Spec) bool {
	frames := slices.ContainsFunc(p.QUIC.TransportParameters, func(tp profile.TransportParameter) bool {
		return tp.ID == 0x20 && tp.Value != nil && *tp.Value > 0
	})

	return frames && slices.ContainsFunc(p.HTTP3.Settings, func(s profile.H3Setting) bool {
		return s.ID == 0x33 && s.Value == 1
	})
}
