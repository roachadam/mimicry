// Command proxy is the benchmark's proxy: HTTP CONNECT without credentials
// (target.ProxyPort), HTTP CONNECT asking for Basic credentials
// (target.ProxyAuthPort; a request without them, or with wrong ones, gets a
// 407 and the connection stays open for the retry, as browsers expect), and
// SOCKS5 (target.ProxySOCKSPort): username and password (RFC 1929) when the
// client offers them, else no authentication; CONNECT and UDP ASSOCIATE. It
// accepts every user target.ProxyCredentialsValid allows, so many clients
// can each have an identity of their own, and counts what it relays per
// user (tunnels and associations opened and still open, datagrams and
// bytes; target.ProxyUsagePath on the stats port). It only relays bytes;
// its container gets its own netem delay (../../docker/proxy-entrypoint.sh).
//
// A UDP association (RFC 1928, section 7) gets a relay socket of its own
// on the address the client reached the proxy at, which the reply names,
// and a second socket towards the targets. It relays datagrams from the
// control connection's address only (its port taken from the first one),
// sends back only what comes from a target it sent to, drops fragments,
// resolves host names itself, and ends with its control connection. The
// relay ports are ephemeral and not published: HTTP/3 through it needs the
// client on the proxy's Docker network.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/roachadam/mimicry/internal/bench/stats"
	"github.com/roachadam/mimicry/internal/bench/target"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx)

	stop()

	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	p := &proxy{
		users: map[string]*usage{},
	}
	errs := make(chan error, 4)

	serve := func(port int, handle func(net.Conn)) {
		var lc net.ListenConfig

		ln, err := lc.Listen(ctx, "tcp", ":"+strconv.Itoa(port))
		if err != nil {
			errs <- err

			return
		}

		go func() {
			<-ctx.Done()

			_ = ln.Close()
		}()

		for {
			c, err := ln.Accept()
			if err != nil {
				errs <- err

				return
			}

			go handle(c)
		}
	}

	go serve(target.ProxyPort, func(c net.Conn) {
		p.connect(c, false)
	})
	go serve(target.ProxyAuthPort, func(c net.Conn) {
		p.connect(c, true)
	})
	go serve(target.ProxySOCKSPort, p.socks5)

	mux := http.NewServeMux()
	mux.Handle("GET /_stats", stats.Handler(nil))
	mux.HandleFunc("GET "+target.ProxyUsagePath, p.usage)

	statsSrv := &http.Server{
		Addr:              ":" + strconv.Itoa(target.ProxyStatsPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		errs <- statsSrv.ListenAndServe()
	}()

	log.Printf("proxy: CONNECT :%d, CONNECT with Basic auth :%d, SOCKS5 :%d, stats :%d",
		target.ProxyPort, target.ProxyAuthPort, target.ProxySOCKSPort, target.ProxyStatsPort)

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		return nil
	}
}

// proxy holds what the proxy relayed, per user ("" for clients without
// credentials).
type proxy struct {
	mu    sync.Mutex
	users map[string]*usage
}

// usage is one user's counts (target.ProxyUse). up and down are the bytes
// of its closed tunnels and of its datagrams; an open tunnel's are read
// from its server-side socket's TCP_INFO when asked (tunnelBytes), so the
// relay can splice instead of copying through a counting reader.
type usage struct {
	tunnels          atomic.Int64
	associations     atomic.Int64
	openTunnels      atomic.Int64
	openAssociations atomic.Int64
	datagrams        atomic.Int64
	up               atomic.Int64
	down             atomic.Int64

	mu   sync.Mutex
	open map[net.Conn]struct{}
}

func (u *usage) snapshot() target.ProxyUse {
	u.mu.Lock()
	defer u.mu.Unlock()

	use := target.ProxyUse{
		Tunnels:          u.tunnels.Load(),
		Associations:     u.associations.Load(),
		OpenTunnels:      u.openTunnels.Load(),
		OpenAssociations: u.openAssociations.Load(),
		Datagrams:        u.datagrams.Load(),
		BytesUp:          u.up.Load(),
		BytesDown:        u.down.Load(),
	}

	for c := range u.open {
		up, down := tunnelBytes(c)

		use.BytesUp += up
		use.BytesDown += down
	}

	return use
}

// opened counts a tunnel to the server over server.
func (u *usage) opened(server net.Conn) {
	u.tunnels.Add(1)
	u.openTunnels.Add(1)

	u.mu.Lock()
	defer u.mu.Unlock()

	if u.open == nil {
		u.open = map[net.Conn]struct{}{}
	}

	u.open[server] = struct{}{}
}

// closed moves a tunnel's bytes into the totals, before server closes.
func (u *usage) closed(server net.Conn) {
	up, down := tunnelBytes(server)

	u.mu.Lock()
	defer u.mu.Unlock()

	u.up.Add(up)
	u.down.Add(down)
	delete(u.open, server)
	u.openTunnels.Add(-1)
}

// user returns name's counts.
func (p *proxy) user(name string) *usage {
	p.mu.Lock()
	defer p.mu.Unlock()

	u := p.users[name]
	if u == nil {
		u = new(usage)
		p.users[name] = u
	}

	return u
}

// usage serves target.ProxyUsagePath: the users whose name starts with
// ?prefix, in total and (?users=1) each, forgotten once read with
// ?forget=1. A tunnel still open goes on counting into a forgotten user.
func (p *proxy) usage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	forget := q.Get("forget") != ""

	var out target.ProxyUsage
	if q.Get("users") != "" {
		out.Users = map[string]target.ProxyUse{}
	}

	p.mu.Lock()

	for name, u := range p.users {
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		use := u.snapshot()
		out.Total.Add(use)

		if out.Users != nil {
			out.Users[name] = use
		}

		if forget {
			delete(p.users, name)
		}
	}

	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")

	_ = json.NewEncoder(w).Encode(out)
}

// connect serves HTTP CONNECT on c. With auth set, a request without valid
// Basic Proxy-Authorization gets a 407 and the connection waits for the
// next one.
func (p *proxy) connect(c net.Conn, auth bool) {
	defer func() {
		_ = c.Close()
	}()

	br := bufio.NewReader(c)

	for {
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))

		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}

		_ = c.SetReadDeadline(time.Time{})

		if req.Method != http.MethodConnect {
			_, _ = io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")

			return
		}

		user := ""

		if auth {
			name, ok := basicUser(req.Header.Get("Proxy-Authorization"))
			if !ok {
				_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\n"+
					"Proxy-Authenticate: Basic realm=\"bench\"\r\nContent-Length: 0\r\n\r\n")

				continue
			}

			user = name
		}

		up, err := dial(req.Host)
		if err != nil {
			_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")

			return
		}

		if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			_ = up.Close()

			return
		}

		relay(c, br, up, p.user(user))

		return
	}
}

// basicUser returns the user of a Basic Proxy-Authorization value the
// proxy accepts.
func basicUser(v string) (string, bool) {
	scheme, enc, ok := strings.Cut(v, " ")
	if !ok || !strings.EqualFold(scheme, "basic") {
		return "", false
	}

	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		return "", false
	}

	user, password, ok := strings.Cut(string(b), ":")
	if !ok || !target.ProxyCredentialsValid(user, password) {
		return "", false
	}

	return user, true
}

// SOCKS5 values (RFC 1928, RFC 1929).
const (
	socksNoAuth       = 0x00
	socksUserPass     = 0x02
	socksNoAcceptable = 0xff
	socksConnect      = 0x01
	socksUDPAssociate = 0x03
	socksIPv4         = 0x01
	socksDomain       = 0x03
	socksIPv6         = 0x04
	socksSucceeded    = 0x00
	socksFailure      = 0x01
	socksRefused      = 0x05
	socksCmdNotSupp   = 0x07
)

// socks5 serves a SOCKS5 CONNECT or UDP ASSOCIATE (RFC 1928), with username
// and password when the client offers them (RFC 1929), else without
// authentication.
func (p *proxy) socks5(c net.Conn) {
	defer func() {
		_ = c.Close()
	}()

	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))

	br := bufio.NewReader(c)

	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil || hdr[0] != 5 {
		return
	}

	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}

	user := ""

	switch {
	case slices.Contains(methods, socksUserPass):
		if _, err := c.Write([]byte{5, socksUserPass}); err != nil {
			return
		}

		name, ok := socksLogin(br, c)
		if !ok {
			return
		}

		user = name

	case slices.Contains(methods, socksNoAuth):
		if _, err := c.Write([]byte{5, socksNoAuth}); err != nil {
			return
		}

	default:
		_, _ = c.Write([]byte{5, socksNoAcceptable})

		return
	}

	var req [4]byte
	if _, err := io.ReadFull(br, req[:]); err != nil || req[0] != 5 {
		return
	}

	host, err := socksAddr(br, req[3])
	if err != nil {
		return
	}

	var port [2]byte
	if _, err := io.ReadFull(br, port[:]); err != nil {
		return
	}

	_ = c.SetReadDeadline(time.Time{})

	switch req[1] {
	case socksConnect:
		up, err := dial(net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))))
		if err != nil {
			_, _ = c.Write(socksReply(socksRefused, netip.AddrPort{}))

			return
		}

		if _, err := c.Write(socksReply(socksSucceeded, netip.AddrPort{})); err != nil {
			_ = up.Close()

			return
		}

		relay(c, br, up, p.user(user))

	case socksUDPAssociate:
		// The request's DST is the client's sending address, which
		// clients fill in variously (zeros, or the target): the first
		// datagram from the control connection's address decides it.
		p.associate(c, br, user)

	default:
		_, _ = c.Write(socksReply(socksCmdNotSupp, netip.AddrPort{}))
	}
}

// socksLogin reads a username/password subnegotiation (RFC 1929) and
// answers it, returning the user when the proxy accepts it.
func socksLogin(br *bufio.Reader, c net.Conn) (string, bool) {
	ver, err := br.ReadByte()
	if err != nil || ver != 1 {
		return "", false
	}

	field := func() (string, bool) {
		n, err := br.ReadByte()
		if err != nil {
			return "", false
		}

		b := make([]byte, n)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", false
		}

		return string(b), true
	}

	user, ok := field()
	if !ok {
		return "", false
	}

	password, ok := field()
	if !ok {
		return "", false
	}

	if !target.ProxyCredentialsValid(user, password) {
		_, _ = c.Write([]byte{1, 1})

		return "", false
	}

	if _, err := c.Write([]byte{1, 0}); err != nil {
		return "", false
	}

	return user, true
}

// socksReply is a SOCKS5 reply with rep and the bound address (zeros when
// invalid).
func socksReply(rep byte, bound netip.AddrPort) []byte {
	ip := bound.Addr().Unmap()
	if !ip.IsValid() {
		ip = netip.IPv4Unspecified()
	}

	out := []byte{5, rep, 0}
	if ip.Is4() {
		out = append(out, socksIPv4)
	} else {
		out = append(out, socksIPv6)
	}

	out = append(out, ip.AsSlice()...)

	return binary.BigEndian.AppendUint16(out, bound.Port())
}

func socksAddr(br *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case socksIPv4:
		var ip [4]byte
		if _, err := io.ReadFull(br, ip[:]); err != nil {
			return "", err
		}

		return net.IP(ip[:]).String(), nil

	case socksDomain:
		n, err := br.ReadByte()
		if err != nil {
			return "", err
		}

		name := make([]byte, n)
		if _, err := io.ReadFull(br, name); err != nil {
			return "", err
		}

		return string(name), nil

	case socksIPv6:
		var ip [16]byte
		if _, err := io.ReadFull(br, ip[:]); err != nil {
			return "", err
		}

		return net.IP(ip[:]).String(), nil
	}

	return "", errors.New("unknown address type")
}

func dial(addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var d net.Dialer

	return d.DialContext(ctx, "tcp", addr)
}

// relay copies both ways until either side closes, counting the tunnel
// and its bytes into use. br holds what the client sent after its request:
// that goes first, then both directions are plain io.Copy between the two
// TCP connections, which Linux splices (no copy through user space). The
// bytes are the server-side socket's TCP_INFO counts (tunnelBytes), read
// live while it is open and once more before it closes.
func relay(c net.Conn, br *bufio.Reader, up net.Conn, use *usage) {
	use.opened(up)

	done := make(chan struct{}, 2)

	go func() {
		if n := br.Buffered(); n > 0 {
			b, _ := br.Peek(n)
			if _, err := up.Write(b); err != nil {
				_ = up.Close()
			}
		}

		_, _ = io.Copy(up, c)

		if tc, ok := up.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}

		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(c, up)

		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}

		done <- struct{}{}
	}()

	<-done
	<-done

	use.closed(up)

	_ = up.Close()
}

const (
	// maxDatagram is the largest UDP payload.
	maxDatagram = 65535
	// headerRoom fits the longest SOCKS5 UDP header the proxy writes (an
	// IPv6 address).
	headerRoom = 4 + 16 + 2
	// udpBuffer is the sockets' receive buffer asked for: a burst of a
	// 100 KB response's datagrams.
	udpBuffer = 4 << 20
)

// association is one UDP ASSOCIATE.
type association struct {
	relay  *net.UDPConn
	out    *net.UDPConn
	client netip.Addr
	use    *usage

	mu sync.Mutex
	// from is the client's sending address, from its first datagram.
	from netip.AddrPort
	// targets are the addresses datagrams went to: only their answers go
	// back.
	targets map[netip.AddrPort]struct{}
}

// associate serves a UDP ASSOCIATE on c until c closes.
func (p *proxy) associate(c net.Conn, br *bufio.Reader, user string) {
	local, ok1 := c.LocalAddr().(*net.TCPAddr)
	remote, ok2 := c.RemoteAddr().(*net.TCPAddr)

	if !ok1 || !ok2 {
		_, _ = c.Write(socksReply(socksFailure, netip.AddrPort{}))

		return
	}

	relaySock, err := net.ListenUDP("udp", &net.UDPAddr{
		IP: local.IP,
	})
	if err != nil {
		_, _ = c.Write(socksReply(socksFailure, netip.AddrPort{}))

		return
	}

	out, err := net.ListenUDP("udp", nil)
	if err != nil {
		_ = relaySock.Close()
		_, _ = c.Write(socksReply(socksFailure, netip.AddrPort{}))

		return
	}

	_ = relaySock.SetReadBuffer(udpBuffer)
	_ = out.SetReadBuffer(udpBuffer)

	clientIP, _ := netip.AddrFromSlice(remote.IP)

	a := &association{
		relay:   relaySock,
		out:     out,
		client:  clientIP.Unmap(),
		use:     p.user(user),
		targets: map[netip.AddrPort]struct{}{},
	}

	var wg sync.WaitGroup

	bound, _ := relaySock.LocalAddr().(*net.UDPAddr)

	if _, err := c.Write(socksReply(socksSucceeded, bound.AddrPort())); err == nil {
		a.use.associations.Add(1)
		a.use.openAssociations.Add(1)

		wg.Go(a.toTargets)
		wg.Go(a.toClient)

		// The association lasts as long as its control connection.
		_, _ = io.Copy(io.Discard, br)

		a.use.openAssociations.Add(-1)
	}

	_ = relaySock.Close()
	_ = out.Close()

	wg.Wait()
}

// toTargets relays the client's datagrams to their targets.
func (a *association) toTargets() {
	buf := make([]byte, maxDatagram)
	names := map[string]netip.Addr{}

	for {
		n, from, err := a.relay.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}

		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		if from.Addr() != a.client {
			continue
		}

		a.mu.Lock()

		if !a.from.IsValid() {
			a.from = from
		}

		ok := a.from == from

		a.mu.Unlock()

		if !ok {
			continue
		}

		dst, payload, ok := destination(buf[:n], names)
		if !ok {
			continue
		}

		a.mu.Lock()

		a.targets[dst] = struct{}{}

		a.mu.Unlock()

		if _, err := a.out.WriteToUDPAddrPort(payload, dst); err == nil {
			a.use.up.Add(int64(len(payload)))
		}
	}
}

// toClient relays the targets' datagrams to the client, each with a SOCKS5
// UDP header naming its source.
func (a *association) toClient() {
	buf := make([]byte, headerRoom+maxDatagram)

	for {
		n, from, err := a.out.ReadFromUDPAddrPort(buf[headerRoom:])
		if err != nil {
			return
		}

		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())

		a.mu.Lock()

		_, known := a.targets[from]
		to := a.from

		a.mu.Unlock()

		if !known {
			continue
		}

		ip := from.Addr().AsSlice()
		start := headerRoom - 4 - len(ip) - 2
		h := buf[start:headerRoom]

		h[0], h[1], h[2] = 0, 0, 0

		h[3] = socksIPv4
		if len(ip) == 16 {
			h[3] = socksIPv6
		}

		copy(h[4:], ip)
		binary.BigEndian.PutUint16(h[4+len(ip):], from.Port())

		if _, err := a.relay.WriteToUDPAddrPort(buf[start:headerRoom+n], to); err == nil {
			a.use.datagrams.Add(1)
			a.use.down.Add(int64(n))
		}
	}
}

// destination parses a client's datagram: its target (a host name
// resolved here, cached in names) and payload. ok is false for a fragment
// (FRAG other than 0, which RFC 1928 lets a relay drop), a malformed
// header or a name that does not resolve.
func destination(b []byte, names map[string]netip.Addr) (netip.AddrPort, []byte, bool) {
	if len(b) < 4 || b[2] != 0 {
		return netip.AddrPort{}, nil, false
	}

	var (
		ip netip.Addr
		n  int
	)

	switch b[3] {
	case socksIPv4, socksIPv6:
		size := 4
		if b[3] == socksIPv6 {
			size = 16
		}

		n = 4 + size
		if len(b) < n+2 {
			return netip.AddrPort{}, nil, false
		}

		ip, _ = netip.AddrFromSlice(b[4:n])

	case socksDomain:
		if len(b) < 5 {
			return netip.AddrPort{}, nil, false
		}

		n = 5 + int(b[4])
		if len(b) < n+2 {
			return netip.AddrPort{}, nil, false
		}

		name := string(b[5:n])

		var ok bool
		if ip, ok = names[name]; !ok {
			ip, ok = resolve(name)
			if !ok {
				return netip.AddrPort{}, nil, false
			}

			names[name] = ip
		}

	default:
		return netip.AddrPort{}, nil, false
	}

	port := binary.BigEndian.Uint16(b[n:])

	return netip.AddrPortFrom(ip.Unmap(), port), b[n+2:], true
}

// resolve returns a host name's address, IPv4 first.
func resolve(name string) (netip.Addr, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, network := range []string{"ip4", "ip"} {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, network, name)
		if err == nil && len(ips) > 0 {
			return ips[0], true
		}
	}

	return netip.Addr{}, false
}
