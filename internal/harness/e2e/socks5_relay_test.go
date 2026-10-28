package e2e

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
)

// socksServer is a SOCKS5 proxy (RFC 1928, RFC 1929) for TestQUICInterop's
// proxy rows: CONNECT, and UDP ASSOCIATE whose relay forwards each datagram
// to its DST (or, without forward, records it and drops it). Every host
// name resolves to the IPv4 loopback address. Its reply to UDP ASSOCIATE
// names the unspecified address, the proxy's own.
type socksServer struct {
	t  *testing.T
	ln net.Listener
	// user and password, when user is set, are the credentials it
	// requires; otherwise it requires none.
	user, password string
	// forward: the relay forwards datagrams; otherwise it only records
	// them.
	forward bool

	mu sync.Mutex
	// refuseUDP answers UDP ASSOCIATE "command not supported".
	refuseUDP bool
	// associations counts the UDP ASSOCIATE requests, refused ones
	// included.
	associations int
	// datagrams counts the client's datagrams at the relays; dsts are
	// their DSTs as sent ("host:port").
	datagrams int
	dsts      []string
	// controls are the open associations' control connections, with
	// their relays.
	controls map[net.Conn]*net.UDPConn
	wg       sync.WaitGroup
}

// newSOCKSServer starts a SOCKS5 proxy, stopped when the test ends.
func newSOCKSServer(t *testing.T, forward bool, user, password string) *socksServer {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	s := &socksServer{
		t:        t,
		ln:       ln,
		user:     user,
		password: password,
		forward:  forward,
		controls: map[net.Conn]*net.UDPConn{},
	}

	s.wg.Go(s.accept)
	t.Cleanup(func() {
		_ = ln.Close()

		s.dropAssociations()
		s.wg.Wait()
	})

	return s
}

// url is the proxy's URL with scheme and, when set, user.
func (s *socksServer) url(scheme string, user *url.Userinfo) *url.URL {
	return &url.URL{
		Scheme: scheme,
		Host:   s.ln.Addr().String(),
		User:   user,
	}
}

// setRefuseUDP makes the proxy refuse UDP ASSOCIATE from now on, or not.
func (s *socksServer) setRefuseUDP(refuse bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.refuseUDP = refuse
}

// counts returns the UDP ASSOCIATE requests and the datagrams relayed so
// far, and the DSTs the datagrams named.
func (s *socksServer) counts() (associations, datagrams int, dsts []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.associations, s.datagrams, slices.Clone(s.dsts)
}

// dropAssociations ends every open association, as a proxy dropping them
// does: their control connections close.
func (s *socksServer) dropAssociations() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for ctrl, relay := range s.controls {
		_ = ctrl.Close()
		_ = relay.Close()
	}

	clear(s.controls)
}

func (s *socksServer) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}

		s.wg.Go(func() {
			if err := s.serve(conn); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				s.t.Logf("SOCKS5 proxy: %v", err)
			}
		})
	}
}

// serve runs one client connection: the handshake, then a CONNECT
// tunnel or a UDP association until the client closes it.
func (s *socksServer) serve(conn net.Conn) error {
	defer func() {
		_ = conn.Close()
	}()

	if err := s.authenticate(conn); err != nil {
		return err
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}

	host, port, err := readSOCKSAddr(conn, head[3])
	if err != nil {
		return err
	}

	switch head[1] {
	case 1:
		return s.connect(conn, host, port)
	case 3:
		return s.associate(conn)
	}

	_, _ = conn.Write(socksReply(7, netip.AddrPort{}))

	return fmt.Errorf("command %d", head[1])
}

// authenticate picks a method from the greeting and checks credentials
// where it requires them.
func (s *socksServer) authenticate(conn net.Conn) error {
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return err
	}

	methods := make([]byte, greeting[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}

	method := byte(0)
	if s.user != "" {
		method = 2
	}

	if !slices.Contains(methods, method) {
		_, _ = conn.Write([]byte{5, 0xff})
		return fmt.Errorf("methods %v lack %d", methods, method)
	}

	if _, err := conn.Write([]byte{5, method}); err != nil {
		return err
	}

	if method == 0 {
		return nil
	}

	// RFC 1929: VER, ULEN, UNAME, PLEN, PASSWD.
	field := func() (string, error) {
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return "", err
		}

		b := make([]byte, n[0])
		_, err := io.ReadFull(conn, b)

		return string(b), err
	}

	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		return err
	}

	user, err := field()
	if err != nil {
		return err
	}

	password, err := field()
	if err != nil {
		return err
	}

	if user != s.user || password != s.password {
		_, _ = conn.Write([]byte{1, 1})
		return errors.New("wrong credentials")
	}

	_, err = conn.Write([]byte{1, 0})

	return err
}

// connect opens a tunnel to the loopback address at port.
func (s *socksServer) connect(conn net.Conn, _ string, port uint16) error {
	up, err := (&net.Dialer{}).DialContext(s.t.Context(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		_, _ = conn.Write(socksReply(5, netip.AddrPort{}))
		return err
	}

	defer func() {
		_ = up.Close()
	}()

	if _, err := conn.Write(socksReply(0, netip.MustParseAddrPort("127.0.0.1:0"))); err != nil {
		return err
	}

	done := make(chan struct{})

	go func() {
		_, _ = io.Copy(up, conn)
		_ = up.Close()

		close(done)
	}()

	_, _ = io.Copy(conn, up)
	_ = conn.Close()

	<-done

	return nil
}

// associate answers UDP ASSOCIATE with a relay of its own, which lives
// until the control connection closes.
func (s *socksServer) associate(ctrl net.Conn) error {
	s.mu.Lock()

	refuse := s.refuseUDP
	s.associations++
	s.mu.Unlock()

	if refuse {
		_, _ = ctrl.Write(socksReply(7, netip.AddrPort{}))
		return nil
	}

	relay, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.IPv4(127, 0, 0, 1),
	})
	if err != nil {
		_, _ = ctrl.Write(socksReply(1, netip.AddrPort{}))
		return err
	}

	s.mu.Lock()

	s.controls[ctrl] = relay
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()

		delete(s.controls, ctrl)
		s.mu.Unlock()

		_ = relay.Close()
	}()

	port := relay.LocalAddr().(*net.UDPAddr).AddrPort().Port() //nolint:forcetypeassert // a UDP listener's address
	if _, err := ctrl.Write(socksReply(0, netip.AddrPortFrom(netip.IPv4Unspecified(), port))); err != nil {
		return err
	}

	s.wg.Go(func() {
		s.relay(relay)
	})

	// The association ends with its control connection.
	_, _ = io.Copy(io.Discard, ctrl)

	return nil
}

// relay carries one association's datagrams: the client's to their DST
// (when forwarding), the answers back behind the header naming their
// source.
func (s *socksServer) relay(relay *net.UDPConn) {
	ups := map[string]*net.UDPConn{}

	defer func() {
		for _, up := range ups {
			_ = up.Close()
		}
	}()

	buf := make([]byte, 1<<16)
	for {
		n, client, err := relay.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}

		dst, payload, err := parseSOCKSDatagram(buf[:n])
		if err != nil {
			s.t.Errorf("SOCKS5 relay: %v", err)

			continue
		}

		s.mu.Lock()

		s.dsts = append(s.dsts, dst)
		s.datagrams++
		s.mu.Unlock()

		if !s.forward {
			continue
		}

		up := ups[dst]
		if up == nil {
			_, port, _ := net.SplitHostPort(dst)

			raddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort("127.0.0.1", port))
			if err != nil {
				s.t.Errorf("SOCKS5 relay: %v", err)

				continue
			}

			if up, err = net.DialUDP("udp4", nil, raddr); err != nil {
				s.t.Errorf("SOCKS5 relay: %v", err)

				continue
			}

			ups[dst] = up
			s.wg.Go(func() {
				back := make([]byte, 1<<16)
				from := raddr.AddrPort()

				for {
					n, err := up.Read(back[10:])
					if err != nil {
						return
					}

					// RSV, then FRAG 0 and the source: a reply's
					// bytes from its RSV on (0, ATYP, IPv4, port).
					copy(back, []byte{0, 0})
					copy(back[2:], socksReply(0, from)[2:])

					if _, err := relay.WriteToUDPAddrPort(back[:10+n], client); err != nil {
						return
					}
				}
			})
		}

		_, _ = up.Write(payload)
	}
}

// socksReply is a reply with code rep and bound address bound (IPv4).
func socksReply(rep byte, bound netip.AddrPort) []byte {
	ip := bound.Addr().Unmap()
	if !ip.IsValid() {
		ip = netip.IPv4Unspecified()
	}

	b := append([]byte{5, rep, 0, 1}, ip.AsSlice()...)

	return binary.BigEndian.AppendUint16(b, bound.Port())
}

// readSOCKSAddr reads a SOCKS5 address of type atyp from r.
func readSOCKSAddr(r io.Reader, atyp byte) (string, uint16, error) {
	var host string

	switch atyp {
	case 1, 4:
		ip := make([]byte, 4)
		if atyp == 4 {
			ip = make([]byte, 16)
		}

		if _, err := io.ReadFull(r, ip); err != nil {
			return "", 0, err
		}

		host = net.IP(ip).String()

	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(r, n); err != nil {
			return "", 0, err
		}

		name := make([]byte, n[0])
		if _, err := io.ReadFull(r, name); err != nil {
			return "", 0, err
		}

		host = string(name)

	default:
		return "", 0, fmt.Errorf("address type %d", atyp)
	}

	port := make([]byte, 2)
	if _, err := io.ReadFull(r, port); err != nil {
		return "", 0, err
	}

	return host, binary.BigEndian.Uint16(port), nil
}

// parseSOCKSDatagram splits a client's datagram into its DST and payload.
func parseSOCKSDatagram(b []byte) (dst string, payload []byte, err error) {
	if len(b) < 4 || b[0] != 0 || b[1] != 0 || b[2] != 0 {
		return "", nil, fmt.Errorf("datagram header % x", b[:min(len(b), 4)])
	}

	r := strings.NewReader(string(b[4:]))

	host, port, err := readSOCKSAddr(r, b[3])
	if err != nil {
		return "", nil, err
	}

	return net.JoinHostPort(host, strconv.Itoa(int(port))), b[len(b)-r.Len():], nil
}

// connectProxy is an HTTP proxy that opens CONNECT tunnels to the
// loopback address at the target's port.
func connectProxy(t *testing.T) *url.URL {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	t.Cleanup(func() {
		_ = ln.Close()

		wg.Wait()
	})

	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			wg.Go(func() {
				defer func() {
					_ = conn.Close()
				}()

				br := bufio.NewReader(conn)

				req, err := http.ReadRequest(br)
				if err != nil || req.Method != http.MethodConnect {
					return
				}

				_, port, _ := net.SplitHostPort(req.Host)

				up, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", port))
				if err != nil {
					return
				}

				defer func() {
					_ = up.Close()
				}()

				if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
					return
				}

				go func() {
					_, _ = io.Copy(up, br)
					_ = up.Close()
				}()

				_, _ = io.Copy(conn, up)
			})
		}
	})

	return &url.URL{
		Scheme: "http",
		Host:   ln.Addr().String(),
	}
}

// udpTap forwards datagrams between its clients and target, recording
// each client's first flight: the sizes of the datagrams it sent before
// the first answer.
type udpTap struct {
	pc *net.UDPConn

	mu      sync.Mutex
	flights map[netip.AddrPort]*tapFlight
	order   []netip.AddrPort
}

type tapFlight struct {
	up       *net.UDPConn
	sizes    []int
	answered bool
}

// newUDPTap starts a tap to target, stopped when the test ends.
func newUDPTap(t *testing.T, target netip.AddrPort) *udpTap {
	t.Helper()

	pc, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.IPv4(127, 0, 0, 1),
	})
	if err != nil {
		t.Fatal(err)
	}

	tap := &udpTap{
		pc:      pc,
		flights: map[netip.AddrPort]*tapFlight{},
	}

	var wg sync.WaitGroup

	t.Cleanup(func() {
		_ = pc.Close()

		tap.mu.Lock()

		for _, f := range tap.flights {
			_ = f.up.Close()
		}

		tap.mu.Unlock()

		wg.Wait()
	})

	wg.Go(func() {
		buf := make([]byte, 1<<16)
		for {
			n, from, err := pc.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}

			tap.mu.Lock()

			f := tap.flights[from]
			if f == nil {
				up, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(target))
				if err != nil {
					tap.mu.Unlock()
					t.Errorf("tap: %v", err)

					continue
				}

				f = &tapFlight{
					up: up,
				}
				tap.flights[from] = f
				tap.order = append(tap.order, from)

				wg.Go(func() {
					back := make([]byte, 1<<16)
					for {
						n, err := up.Read(back)
						if err != nil {
							return
						}

						tap.mu.Lock()

						f.answered = true
						tap.mu.Unlock()

						if _, err := pc.WriteToUDPAddrPort(back[:n], from); err != nil {
							return
						}
					}
				})
			}

			if !f.answered {
				f.sizes = append(f.sizes, n)
			}

			tap.mu.Unlock()

			_, _ = f.up.Write(buf[:n])
		}
	})

	return tap
}

// port is the tap's port on the loopback address.
func (tap *udpTap) port() uint16 {
	return tap.pc.LocalAddr().(*net.UDPAddr).AddrPort().Port() //nolint:forcetypeassert // a UDP listener's address
}

// firstFlights returns each client's first flight, in the order the
// clients started.
func (tap *udpTap) firstFlights() [][]int {
	tap.mu.Lock()
	defer tap.mu.Unlock()

	out := make([][]int, 0, len(tap.order))
	for _, from := range tap.order {
		out = append(out, slices.Clone(tap.flights[from].sizes))
	}

	return out
}

// via returns e with a new Transport of its Config sending every request
// through proxy (DialContext reaching it), HTTP/3 through SOCKS5 as
// h3SOCKS5 says, with cache as its Alt-Svc cache (nil: its own).
func (e *interopEnv) via(proxy *url.URL, h3SOCKS5 bool, cache *mimicry.AltSvcCache) *interopEnv {
	next := *e

	next.cfg.Proxy = http.ProxyURL(proxy)
	next.cfg.DialContext = nil
	next.cfg.HTTP3OverSOCKS5 = h3SOCKS5
	next.cfg.AltSvc = cache
	next.setTransport(next.cfg)

	return &next
}

// altSvcAt returns a cache knowing the origin's HTTP/3 alternative at
// port, for a client to start there before any response.
func (e *interopEnv) altSvcAt(port uint16) *mimicry.AltSvcCache {
	e.t.Helper()

	cache := mimicry.NewAltSvcCache()
	saved := fmt.Sprintf(`[{"origin": %q, "port": %d, "expires": %q}]`, e.origin, port, time.Now().Add(time.Hour).Format(time.RFC3339))

	if err := cache.ReadJSON(strings.NewReader(saved)); err != nil {
		e.t.Fatal(err)
	}

	return cache
}

// untilH3 asks for /info until a response comes over HTTP/3 (a cached
// alternative: ?noalt keeps the TCP server from replacing it).
func (e *interopEnv) untilH3(what string) {
	e.t.Helper()

	for {
		resp, _, err := e.do(http.MethodGet, "/info?noalt", nil)
		if err != nil {
			e.t.Fatalf("%s: %v", what, err)
		}

		if resp.ProtoMajor == 3 {
			e.h3(resp, what)

			return
		}

		select {
		case <-e.ctx.Done():
			e.t.Fatalf("%s never moved to HTTP/3", what)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// onTCP sends GET /info, which must succeed over TCP.
func (e *interopEnv) onTCP(what string) {
	e.t.Helper()

	resp, _, err := e.do(http.MethodGet, "/info", nil)
	if err != nil {
		e.t.Fatalf("%s: %v", what, err)
	}

	if resp.ProtoMajor == 3 {
		e.t.Fatalf("%s went over HTTP/3", what)
	}
}

// waitFor polls cond until it holds or the case's deadline passes.
func (e *interopEnv) waitFor(what string, cond func() bool) {
	e.t.Helper()

	for !cond() {
		select {
		case <-e.ctx.Done():
			e.t.Fatalf("never: %s", what)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// settle gives background QUIC attempts time to start and fail.
func settle(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(300 * time.Millisecond):
	}
}
