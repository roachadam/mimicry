package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/proxy"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// mimicry asks proxies for connections as the browser does, against its
// captures through the capture proxy: CONNECT lines, SOCKS5 bytes, the
// credential flow (challenge, retry on the same connection, unasked after),
// and absolute-form plain-HTTP requests. A tunnel carries the profile's
// direct ClientHello.
func TestProxyMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			t.Run("connect", func(t *testing.T) {
				want := loadSamples[[]capture.ProxyRequest](t, p, "proxy-connect", ".proxytrace.json")
				s := startProxied(t, p, scenario(t, "proxy-connect"), nil)
				s.get("https", server.CaptureHost, "/")

				got := s.proxy.Requests()
				if len(got) != 1 {
					t.Fatalf("%d proxy requests, want 1: %v", len(got), got)
				}

				if !slices.ContainsFunc(want, func(sample []capture.ProxyRequest) bool {
					return slices.Equal(portless(nthOK(sample, 0).Lines), portless(got[0].Lines))
				}) {
					t.Errorf("CONNECT:\n got %q\nwant %q", portless(got[0].Lines), portless(nthOK(want[0], 0).Lines))
				}

				s.checkTunnelledHello(p)
			})
			t.Run("socks5", func(t *testing.T) {
				want := loadSamples[[]capture.ProxyRequest](t, p, "proxy-socks5", ".proxytrace.json")
				s := startProxied(t, p, scenario(t, "proxy-socks5"), nil)
				s.get("https", server.CaptureHost, "/")

				got := s.proxy.Requests()
				if len(got) != 1 {
					t.Fatalf("%d proxy requests, want 1: %v", len(got), got)
				}

				w := want[0][0]
				if got[0].Greeting != w.Greeting || socksPortless(got[0].Request) != socksPortless(w.Request) {
					t.Errorf("SOCKS5:\n got %s %s\nwant %s %s", got[0].Greeting, got[0].Request, w.Greeting, w.Request)
				}

				s.checkTunnelledHello(p)
			})
			t.Run("auth", func(t *testing.T) {
				wantFlow := loadSamples[[]capture.ProxyRequest](t, p, "proxy-auth", ".proxytrace.json")
				sc := scenario(t, "proxy-auth")
				hosts, _, ok := sc.MultiHost()

				if !ok || len(hosts) == 0 {
					t.Fatalf("scenario %s serves no hosts", sc.Name)
				}

				s := startProxied(t, p, sc, url.UserPassword(server.ProxyUser, server.ProxyPassword))
				// A new tunnel each time.
				for range 3 {
					s.get("https", hosts[0], "/")
					s.tr.CloseIdleConnections()
				}

				got := connectFlow(s.proxy.Requests())
				for i, sample := range wantFlow {
					w := connectFlow(sample)
					if len(w) < len(got) {
						t.Errorf("CONNECT flow against sample %d: mimicry made %d steps, the browser %d:\n got %v\nwant %v", i+1, len(got), len(w), got, w)

						continue
					}

					if w = w[:len(got)]; !slices.Equal(got, w) {
						t.Errorf("CONNECT flow (connection, status, credentials) against sample %d:\n got %v\nwant %v", i+1, got, w)
					}
				}

				// The first tunnel's CONNECT answering the challenge, and the
				// second's (unasked, for browsers that send them so).
				for n := range 2 {
					g, w := nthOK(s.proxy.Requests(), n), nthOK(wantFlow[0], n)
					if !slices.Equal(portless(g.Lines), portless(w.Lines)) {
						t.Errorf("CONNECT %d with credentials:\n got %q\nwant %q", n+1, portless(g.Lines), portless(w.Lines))
					}
				}
			})
			t.Run("plain", func(t *testing.T) {
				want := loadSamples[[]capture.H1Request](t, p, "proxy-plain", ".h1trace.json")
				s := startProxied(t, p, scenario(t, "proxy-plain"), nil)
				s.get("http", server.CaptureHost, "/")

				got := s.h1()
				if g, w := forwarded(got[0]), forwarded(want[0][0]); !slices.Equal(g, w) {
					t.Errorf("forwarded request:\n got %q\nwant %q", g, w)
				}
			})
			t.Run("plain-auth", func(t *testing.T) {
				want := loadSamples[[]capture.H1Request](t, p, "proxy-plain-auth", ".h1trace.json")
				s := startProxied(t, p, scenario(t, "proxy-plain-auth"), url.UserPassword(server.ProxyUser, server.ProxyPassword))
				s.get("http", server.CaptureHost, "/")
				s.get("http", server.CaptureHost, "/nav")

				got := s.h1()
				g, w := forwardAuthFlow(got), forwardAuthFlow(want[0])

				if len(w) < len(g) {
					t.Errorf("forwarded requests: mimicry made %d, the browser %d:\n got %v\nwant %v", len(g), len(w), g, w)
				} else if w = w[:len(g)]; !slices.Equal(g, w) {
					t.Errorf("forwarded requests (connection, credentials):\n got %v\nwant %v", g, w)
				}

				// The first navigation with credentials (for most browsers, the
				// one answering the challenge), and the second (unasked).
				for _, path := range []string{"/", "/nav"} {
					g, w := credentialed(got, path), credentialed(want[0], path)
					if g == nil || w == nil {
						t.Fatalf("%s: no request with credentials (mimicry %v, browser %v)", path, g != nil, w != nil)
					}

					if gp, wp := authPlace(g.Headers), authPlace(w.Headers); gp != wp {
						t.Errorf("%s: Proxy-Authorization %s, want %s", path, gp, wp)
					}
				}
			})
		})
	}
}

// What a profile does not describe is an error, not a guess: a proxy without
// a proxy section, credentials without connectAuth, and a WebSocket through
// a proxy its browser was not measured with.
func TestUnmeasuredProxyIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		strip func(p *profile.Proxy) *profile.Proxy
		user  *url.Userinfo
		// scheme is the proxy's; empty: http.
		scheme string
		// websocket dials target with DialWebSocket; empty target:
		// https://example.test/ as a request.
		websocket bool
		target    string
	}{
		{
			name: "no proxy section",
			strip: func(*profile.Proxy) *profile.Proxy {
				return nil
			},
		},
		{
			name: "credentials not measured",
			strip: func(p *profile.Proxy) *profile.Proxy {
				p.ConnectAuth, p.ConnectCredentials, p.ForwardCredentials, p.ForwardAuth = nil, "", "", nil
				p.WebSocket.Credentials = false

				return p
			},
			user: url.UserPassword("u", "p"),
		},
		{
			name: "WebSocket without proxy.websocket",
			strip: func(p *profile.Proxy) *profile.Proxy {
				p.WebSocket = nil
				return p
			},
			websocket: true,
			target:    "wss://example.test/socket",
		},
		{
			name: "WebSocket with credentials, proxy.websocket.credentials unset",
			strip: func(p *profile.Proxy) *profile.Proxy {
				p.WebSocket.Credentials = false
				return p
			},
			user:      url.UserPassword("u", "p"),
			websocket: true,
			target:    "wss://example.test/socket",
		},
		{
			name:      "ws:// through SOCKS5, never captured",
			scheme:    "socks5h",
			websocket: true,
			target:    "ws://example.test/socket",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := profiles.Chrome()
			if tc.strip != nil {
				p.Proxy = tc.strip(p.Proxy)
			}

			scheme := tc.scheme
			if scheme == "" {
				scheme = "http"
			}

			tr, err := mimicry.NewTransport(mimicry.Config{
				Profile: p,
				Proxy: http.ProxyURL(&url.URL{
					Scheme: scheme,
					Host:   "127.0.0.1:1",
					User:   tc.user,
				}),
			})
			if err != nil {
				t.Fatal(err)
			}

			if tc.websocket {
				ws, _, err := tr.DialWebSocket(t.Context(), tc.target, mimicry.WebSocketOptions{})
				if err == nil {
					_ = ws.Close(1000, "")
				}

				if !errors.Is(err, mimicry.ErrNotMeasured) {
					t.Fatalf("got %v, want ErrNotMeasured", err)
				}

				return
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/", nil)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := tr.RoundTrip(req); err == nil || !strings.Contains(err.Error(), "not measured") {
				t.Fatalf("got %v, want an error saying it is not measured", err)
			}
		})
	}
}

// proxySession is a capture server behind the capture proxy, and a mimicry
// transport for a profile using that proxy.
type proxySession struct {
	t        *testing.T
	tr       *mimicry.Transport
	proxy    *proxy.Proxy
	port     string
	pool     *x509.CertPool
	captures chan *capture.Capture
}

func startProxied(t *testing.T, p *profile.Profile, sc *server.Scenario, user *url.Userinfo) *proxySession {
	t.Helper()

	ca, err := server.LoadOrCreateCA(t.TempDir(), server.CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	cert, err := ca.Issue(server.CAHosts, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	s := &proxySession{
		t:        t,
		pool:     x509.NewCertPool(),
		captures: make(chan *capture.Capture, 64),
		// Behind a proxy the port is only named, never dialled.
		port: "8443",
		proxy: &proxy.Proxy{
			Allow: func(host string) bool {
				return strings.HasSuffix(host, ".test")
			},
			Plain: sc.Plain,
			Logf:  t.Logf,
		},
	}
	if sc.ProxyAuth {
		s.proxy.Auth = server.ProxyAuthorization()
	}

	s.pool.AddCert(ca.Cert)

	srv := &server.Server{
		Scenario: sc,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
		},
		IssueCert: func(hosts []string) (tls.Certificate, error) {
			return ca.Issue(hosts, time.Now())
		},
		OnCapture: func(c *capture.Capture) {
			s.captures <- c
		},
		Logf: t.Logf,
	}

	pln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	served, proxied := make(chan error, 1), make(chan error, 1)

	go func() {
		proxied <- s.proxy.Serve(ctx, pln)
	}()
	go func() {
		served <- srv.Serve(ctx, s.proxy.Listener())
	}()

	t.Cleanup(func() {
		cancel()

		for _, ch := range []chan error{served, proxied} {
			if err := <-ch; err != nil && !errors.Is(err, net.ErrClosed) {
				t.Error(err)
			}
		}
	})

	s.tr, err = mimicry.NewTransport(mimicry.Config{
		Profile: p,
		RootCAs: s.pool,
		Proxy: http.ProxyURL(&url.URL{
			Scheme: sc.Proxy,
			Host:   pln.Addr().String(),
			User:   user,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(s.tr.CloseIdleConnections)

	return s
}

// get requests path on host over scheme and reads it.
func (s *proxySession) get(scheme, host, path string) {
	s.t.Helper()

	req, err := http.NewRequestWithContext(s.t.Context(), http.MethodGet, scheme+"://"+net.JoinHostPort(host, s.port)+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}

	resp, err := s.tr.RoundTrip(req)
	if err != nil {
		s.t.Fatal(err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("%s: %s", req.URL, resp.Status)
	}
}

// capture closes mimicry's connections, which completes the capture of
// the one used.
func (s *proxySession) capture() *capture.Capture {
	s.t.Helper()
	s.tr.CloseIdleConnections()

	for {
		select {
		case c := <-s.captures:
			if c.Complete() {
				return c
			}

		case <-time.After(10 * time.Second):
			s.t.Fatal("no capture")
		}
	}
}

// h1 returns the HTTP/1.1 requests the server recorded.
func (s *proxySession) h1() []capture.H1Request {
	s.t.Helper()

	return s.capture().H1
}

// checkTunnelledHello checks that the ClientHello through the tunnel is
// the profile's.
func (s *proxySession) checkTunnelledHello(p *profile.Profile) {
	s.t.Helper()

	c := s.capture()
	got := profile.Profile{
		TLS: c.Profile.TLS,
	}
	want := profile.Profile{
		TLS: p.TLS,
	}

	got.TLS.ExtensionOrder, want.TLS.ExtensionOrder = p.TLS.ExtensionOrder, p.TLS.ExtensionOrder

	for _, d := range diff.Compare(&want, &got) {
		s.t.Errorf("ClientHello through the tunnel: %s", d)
	}
}

func scenario(t *testing.T, name string) *server.Scenario {
	t.Helper()

	sc, err := server.ScenarioByName(name)
	if err != nil {
		t.Fatal(err)
	}

	return sc
}

// missingCaptures are the captures known not to exist, and why; a test
// needing one is skipped. Any other missing capture fails. Keyed by the file
// of the profile that holds the scenario's captures, so a profile that
// inherits the scenario (capturesFrom) inherits the entry too.
var missingCaptures = map[string]string{
	"safari-26-macos.proxy-auth.proxytrace.json":    "Safari 26 proxy credentials were never captured",
	"safari-26-macos.proxy-plain-auth.h1trace.json": "Safari 26 proxy credentials were never captured",
	// Safari answers a proxy's 407 only with credentials macOS holds: a
	// com.apple.proxy.http.global device profile and the system's login
	// dialog, which need a person at the screen (docs/capturing.md). Safari 26's
	// socket credential rules are copied from Safari 27's capture.
	"safari-26-macos.proxy-websocket-auth.pagetrace.json.gz": "Safari 26 WebSockets through a proxy with credentials were never captured (macOS proxy credentials need a person at the screen); its rules are copied from Safari 27's",
	// The capture tool clicks in Safari through the system's mouse
	// events, which need Accessibility access; the second Mac's
	// captures run over ssh, which has none.
	"safari-26-macos.nav-redirect-click.pagetrace.json.gz": "Safari 26 clicks were never captured (no Accessibility access over ssh)",
}

// nthOK is the CONNECT the proxy let through n-th, from 0.
func nthOK(sample []capture.ProxyRequest, n int) capture.ProxyRequest {
	for _, r := range sample {
		if r.Status != http.StatusOK {
			continue
		}

		if n == 0 {
			return r
		}

		n--
	}

	return capture.ProxyRequest{}
}

// credentialed is the first request for path that carries
// Proxy-Authorization, or nil.
func credentialed(sample []capture.H1Request, path string) *capture.H1Request {
	for i, r := range sample {
		u, err := url.Parse(strings.Fields(r.Line)[1])
		if err == nil && u.Path == path && authPlace(r.Headers) != "none" {
			return &sample[i]
		}
	}

	return nil
}

// portless replaces ports after .test hosts.
func portless(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = testPort.ReplaceAllString(l, ".test:PORT")
	}

	return out
}

// socksPortless drops a SOCKS5 request's port (its last two bytes, hex).
func socksPortless(req string) string {
	return req[:max(len(req)-4, 0)]
}

// connectFlow describes CONNECTs as (connection, status, credentials),
// connections numbered from 1 in order of use. Connections that only ever
// got a 407 are left out: Chrome's speculative preconnects, which mimicry
// does not make, abandon theirs.
func connectFlow(sample []capture.ProxyRequest) []string {
	ok := map[int]bool{}
	for _, r := range sample {
		if r.Status == http.StatusOK {
			ok[r.Conn] = true
		}
	}

	number := map[int]int{}

	var out []string

	for _, r := range sample {
		if !ok[r.Conn] {
			continue
		}

		if number[r.Conn] == 0 {
			number[r.Conn] = len(number) + 1
		}

		auth := "-"
		if slices.ContainsFunc(r.Lines, func(l string) bool {
			return strings.HasPrefix(strings.ToLower(l), "proxy-authorization:")
		}) {
			auth = "credentials"
		}

		out = append(out, fmt.Sprintf("conn %d %d %s", number[r.Conn], r.Status, auth))
	}

	return out
}

// forwarded is how a request was sent to a proxy whole: its request line
// and header lines, ports replaced.
func forwarded(r capture.H1Request) []string {
	return portless(append([]string{r.Line}, r.Headers...))
}

// forwardAuthFlow describes forwarded requests as (connection, credentials),
// connections numbered from 1 in order of use.
func forwardAuthFlow(sample []capture.H1Request) []string {
	number := map[int]int{}
	out := make([]string, 0, len(sample))

	for _, r := range sample {
		if number[r.Conn] == 0 {
			number[r.Conn] = len(number) + 1
		}

		auth := "-"
		if authPlace(r.Headers) != "none" {
			auth = "credentials"
		}

		out = append(out, fmt.Sprintf("conn %d %s", number[r.Conn], auth))
	}

	return out
}

// authPlace says where Proxy-Authorization is: after which header, last,
// or none.
func authPlace(headers []string) string {
	for i, h := range headers {
		if !strings.HasPrefix(strings.ToLower(h), "proxy-authorization:") {
			continue
		}

		if i == len(headers)-1 {
			return "last"
		}

		if i == 0 {
			return "first"
		}

		name, _, _ := strings.Cut(headers[i-1], ":")

		return "after " + name
	}

	return "none"
}
