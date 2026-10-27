package mimicry_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/ws"
)

// The examples talk to a stand-in shop on the loopback interface instead of a
// real site. It answers for every *.test name (www.shop.test,
// www.outlet.test) on one TLS server. The examples reach it through
// Config.RootCAs (its certificate) and Config.DialContext (every *.test
// name dials it); real code leaves both nil.

// shop is a running stand-in shop.
type shop struct {
	srv  *httptest.Server
	addr string
	// RootCAs trusts the shop's certificate.
	RootCAs *x509.CertPool
}

// startShop serves handler over TLS, HTTP/2 or HTTP/1.1 as the client asks.
func startShop(handler http.HandlerFunc) *shop {
	cert, pool := shopCertificate()
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{
			cert,
		},
	}
	srv.StartTLS()
	return &shop{
		srv:     srv,
		addr:    srv.Listener.Addr().String(),
		RootCAs: pool,
	}
}

// startShopHTTP1 is startShop speaking HTTP/1.1 only, so that handler can
// read each request's header lines as they were sent (headerLines): Go's
// server keeps no order.
func startShopHTTP1(handler http.HandlerFunc) *shop {
	cert, pool := shopCertificate()
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = recordingListener{
		Listener: tls.NewListener(ln, &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
			NextProtos: []string{
				"http/1.1",
			},
		}),
	}
	srv.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, connKey{}, c)
	}
	srv.Start()
	return &shop{
		srv:     srv,
		addr:    ln.Addr().String(),
		RootCAs: pool,
	}
}

// DialContext dials the shop for every *.test name, and anything else as
// net.Dialer does.
func (s *shop) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(host, ".test") {
		addr = s.addr
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// Close stops the shop.
func (s *shop) Close() {
	s.srv.Close()
}

// shopCertificate returns a certificate for the *.test names the examples
// use, issued as a real site's is (root, intermediate, leaf; the server
// sends the leaf and the intermediate), and a pool that trusts the root.
// Every call makes new keys.
func shopCertificate() (tls.Certificate, *x509.CertPool) {
	root, rootKey := issue(&x509.Certificate{
		Subject: pkix.Name{
			CommonName: "Shop Test Root CA",
		},
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, nil, nil)
	intermediate, intermediateKey := issue(&x509.Certificate{
		Subject: pkix.Name{
			CommonName: "Shop Test Issuing CA",
		},
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, root, rootKey)
	leaf, leafKey := issue(&x509.Certificate{
		Subject: pkix.Name{
			CommonName: "shop.test",
		},
		DNSNames: []string{
			"shop.test",
			"*.shop.test",
			"outlet.test",
			"*.outlet.test",
		},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, intermediate, intermediateKey)
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return tls.Certificate{
		Certificate: [][]byte{
			leaf.Raw,
			intermediate.Raw,
		},
		PrivateKey: leafKey,
		Leaf:       leaf,
	}, pool
}

// issue makes tmpl a certificate with a new key, signed by parent's key
// (self-signed when parent is nil), valid for an hour either side of now.
func issue(tmpl, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		panic(err)
	}
	tmpl.SerialNumber = serial
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		panic(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return c, key
}

type connKey struct{}

// recordingListener keeps what its connections read, after TLS.
type recordingListener struct {
	net.Listener
}

func (l recordingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &recordingConn{
		Conn: c,
	}, nil
}

type recordingConn struct {
	net.Conn
	mu  sync.Mutex
	buf []byte
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	c.buf = append(c.buf, p[:n]...)
	c.mu.Unlock()
	return n, err
}

// headerLines returns r's header lines as the client sent them, in order,
// for a shop started with startShopHTTP1.
func headerLines(r *http.Request) []string {
	c, ok := r.Context().Value(connKey{}).(*recordingConn)
	if !ok {
		panic("headerLines: request is not from a startShopHTTP1 shop")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	start := bytes.LastIndex(c.buf, []byte(r.Method+" "+r.RequestURI+" HTTP/1.1\r\n"))
	if start < 0 {
		return nil
	}
	block := c.buf[start:]
	end := bytes.Index(block, []byte("\r\n\r\n"))
	if end < 0 {
		return nil
	}
	lines := strings.Split(string(block[:end]), "\r\n")
	c.buf = c.buf[start+end+4:]
	return lines[1:]
}

// proxy is a stand-in HTTP proxy that tunnels CONNECT requests to the shop.
// A plain-HTTP request sent to it whole gets only its 407.
type proxy struct {
	// URL is the proxy's, with its credentials.
	URL  *url.URL
	ln   net.Listener
	shop *shop
	user string
	pass string
	mu   sync.Mutex
	log  []string
	// refuse, when set, is the status line answering every CONNECT that
	// passes the credentials check.
	refuse string
}

// startProxy starts a proxy in front of s that asks for user and pass, or
// for nothing when user is empty.
func startProxy(s *shop, user, pass string) *proxy {
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	u := &url.URL{
		Scheme: "http",
		Host:   ln.Addr().String(),
	}
	if user != "" {
		u.User = url.UserPassword(user, pass)
	}
	p := &proxy{
		URL:  u,
		ln:   ln,
		shop: s,
		user: user,
		pass: pass,
	}
	go p.serve()
	return p
}

func (p *proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.tunnel(c)
	}
}

func (p *proxy) tunnel(c net.Conn) {
	defer func() {
		_ = c.Close()
	}()
	br := bufio.NewReader(c)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		user, pass, ok := parseProxyAuth(req.Header.Get("Proxy-Authorization"))
		who := "without credentials"
		if ok {
			who = "as " + user
		}
		if p.user != "" && (!ok || user != p.user || pass != p.pass) {
			p.record(fmt.Sprintf("%s %s %s: 407", req.Method, req.Host, who))
			_, _ = io.Copy(io.Discard, req.Body)
			_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"proxy\"\r\nContent-Length: 0\r\n\r\n")
			continue
		}
		if req.Method != http.MethodConnect {
			return
		}
		p.mu.Lock()
		refuse := p.refuse
		p.mu.Unlock()
		if refuse != "" {
			p.record(fmt.Sprintf("%s %s %s: %s", req.Method, req.Host, who, refuse))
			_, _ = io.WriteString(c, "HTTP/1.1 "+refuse+"\r\nContent-Length: 0\r\n\r\n")
			continue
		}
		p.record(fmt.Sprintf("%s %s %s: 200", req.Method, req.Host, who))
		upstream, err := new(net.Dialer).DialContext(req.Context(), "tcp", p.shop.addr)
		if err != nil {
			return
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
		go func() {
			_, _ = io.Copy(upstream, br)
			_ = upstream.Close()
		}()
		_, _ = io.Copy(c, upstream)
		return
	}
}

func parseProxyAuth(h string) (string, string, bool) {
	enc, ok := strings.CutPrefix(h, "Basic ")
	if !ok {
		return "", "", false
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", "", false
	}
	return strings.Cut(string(b), ":")
}

func (p *proxy) record(line string) {
	p.mu.Lock()
	p.log = append(p.log, line)
	p.mu.Unlock()
}

// Log returns what the proxy was asked, in order.
func (p *proxy) Log() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.log...)
}

// Refuse makes the proxy answer every CONNECT it would let through with
// status ("403 Forbidden").
func (p *proxy) Refuse(status string) {
	p.mu.Lock()
	p.refuse = status
	p.mu.Unlock()
}

// Close stops the proxy.
func (p *proxy) Close() {
	_ = p.ln.Close()
}

// echoWebSocket answers a WebSocket handshake on HTTP/1.1 and echoes each text
// message back as "echo: <message>" until the client closes.
func echoWebSocket(w http.ResponseWriter, r *http.Request) {
	c, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer func() {
		_ = c.Close()
	}()
	_, _ = fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", ws.AcceptKey(r.Header.Get("Sec-WebSocket-Key")))
	_ = brw.Flush()
	for {
		op, payload, err := readFrame(brw.Reader)
		if err != nil {
			return
		}
		switch op {
		case 0x1:
			writeFrame(brw.Writer, 0x1, append([]byte("echo: "), payload...))
		case 0x8:
			writeFrame(brw.Writer, 0x8, payload)
			return
		}
	}
}

// readFrame reads one masked client frame (no fragments, under 64 KiB).
func readFrame(r *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := int(h[1] & 0x7f)
	if n == 126 {
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int(binary.BigEndian.Uint16(ext[:]))
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return h[0] & 0x0f, payload, nil
}

// writeFrame writes one unmasked server frame of under 126 bytes.
func writeFrame(w *bufio.Writer, op byte, payload []byte) {
	_, _ = w.Write([]byte{
		0x80 | op,
		byte(len(payload)),
	})
	_, _ = w.Write(payload)
	_ = w.Flush()
}
