package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/diff"
	"github.com/roachadam/mimicry/internal/harness/internal/proxy"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/internal/harness/internal/testprofiles"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// mimicry asks proxies for connections as the browser does, against its
// captures through the capture proxy, with Config.BrowserProxyAuth:
// CONNECT lines, SOCKS5 bytes, the credential flow (challenge, retry on
// the same connection, unasked after), and absolute-form plain-HTTP
// requests, every one of the capture page's. A tunnel carries the
// profile's direct ClientHello. By
// default ("auth-upfront", "plain-auth-upfront") the credentials go on
// the first request, where the browser places them on its credentialed
// ones, with no 407 round trip; the tunnel is unchanged (decision 0039).
func TestProxyMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			t.Run("connect", func(t *testing.T) {
				want := loadSamples[[]capture.ProxyRequest](t, p, "proxy-connect", ".proxytrace.json")
				s := startProxied(t, p, scenario(t, "proxy-connect"), nil, true)
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
				s := startProxied(t, p, scenario(t, "proxy-socks5"), nil, true)
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

				s := startProxied(t, p, sc, url.UserPassword(server.ProxyUser, server.ProxyPassword), true)
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
			t.Run("auth-upfront", func(t *testing.T) {
				wantFlow := loadSamples[[]capture.ProxyRequest](t, p, "proxy-auth", ".proxytrace.json")
				sc := scenario(t, "proxy-auth")
				hosts, _, ok := sc.MultiHost()

				if !ok || len(hosts) == 0 {
					t.Fatalf("scenario %s serves no hosts", sc.Name)
				}

				s := startProxied(t, p, sc, url.UserPassword(server.ProxyUser, server.ProxyPassword), false)
				// A new tunnel each time, each with credentials unasked.
				for range 3 {
					s.get("https", hosts[0], "/")
					s.tr.CloseIdleConnections()
				}

				got := s.proxy.Requests()
				if flow, want := connectFlow(got), []string{"conn 1 200 credentials", "conn 2 200 credentials", "conn 3 200 credentials"}; !slices.Equal(flow, want) {
					t.Errorf("CONNECT flow (connection, status, credentials):\n got %v\nwant %v", flow, want)
				}

				// Placed as the browser's first CONNECT with credentials.
				w := portless(nthOK(wantFlow[0], 0).Lines)
				for i, r := range got {
					if g := portless(r.Lines); !slices.Equal(g, w) {
						t.Errorf("CONNECT %d:\n got %q\nwant %q", i+1, g, w)
					}
				}

				s.checkTunnelledHello(p)
			})
			t.Run("plain", func(t *testing.T) {
				want := loadSamples[[]capture.H1Request](t, p, "proxy-plain", ".h1trace.json")
				s := startProxied(t, p, scenario(t, "proxy-plain"), nil, true)
				s.page()

				got := s.h1()
				for _, r := range want[0] {
					if !slices.ContainsFunc(got, func(g capture.H1Request) bool {
						return portless([]string{g.Line})[0] == portless([]string{r.Line})[0]
					}) {
						t.Errorf("mimicry made no %s", portless([]string{r.Line})[0])
					}
				}

				seen := map[string]bool{}

				for _, g := range got {
					if slices.ContainsFunc(want, func(sample []capture.H1Request) bool {
						return slices.ContainsFunc(sample, func(w capture.H1Request) bool {
							return slices.Equal(forwarded(g), forwarded(w))
						})
					}) {
						continue
					}

					line := portless([]string{g.Line})[0]
					if why, ok := proxyPlainGaps[name+" "+line]; ok {
						seen[name+" "+line] = true
						t.Logf("known gap: %s: %s", line, why)

						continue
					}

					t.Errorf("forwarded request:\n got %q\nwant %q", forwarded(g), forwardedLike(want[0], g))
				}

				for k := range proxyPlainGaps {
					if strings.HasPrefix(k, name+" ") && !seen[k] {
						t.Errorf("listed gap %q no longer appears", k)
					}
				}
			})
			t.Run("plain-auth", func(t *testing.T) {
				want := loadSamples[[]capture.H1Request](t, p, "proxy-plain-auth", ".h1trace.json")
				s := startProxied(t, p, scenario(t, "proxy-plain-auth"), url.UserPassword(server.ProxyUser, server.ProxyPassword), true)
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
			t.Run("plain-auth-upfront", func(t *testing.T) {
				want := loadSamples[[]capture.H1Request](t, p, "proxy-plain-auth", ".h1trace.json")
				s := startProxied(t, p, scenario(t, "proxy-plain-auth"), url.UserPassword(server.ProxyUser, server.ProxyPassword), false)
				s.get("http", server.CaptureHost, "/")
				s.get("http", server.CaptureHost, "/nav")

				got := s.h1()
				if g, w := forwardAuthFlow(got), []string{"conn 1 credentials", "conn 1 credentials"}; !slices.Equal(g, w) {
					t.Errorf("forwarded requests (connection, credentials):\n got %v\nwant %v", g, w)
				}

				// Both placed as the browser places them unasked: its second
				// request's.
				w := credentialed(want[0], "/nav")
				if w == nil {
					t.Fatal("the browser sent /nav without credentials")
				}

				for _, g := range got {
					if gp, wp := authPlace(g.Headers), authPlace(w.Headers); gp != wp {
						t.Errorf("%s: Proxy-Authorization %s, want %s", g.Line, gp, wp)
					}
				}
			})
		})
	}
}

// proxyPlainGaps are forwarded plain-HTTP requests of the capture page
// known to differ from every sample, as "<profile> <request line>" (ports
// as PORT), with why: the request is not compared. A listed gap that no
// longer appears fails.
var proxyPlainGaps = func() map[string]string {
	const (
		nav    = "GET http://capture.mimicry.test:PORT/nav HTTP/1.1"
		upload = "POST http://capture.mimicry.test:PORT/upload HTTP/1.1"
		// linkNav: kinds-plain navigates from its plain page only by
		// form POST, so no profile list measures a GET navigation from a
		// plain-HTTP page.
		linkNav = "the page's script navigation to /nav, from a plain-HTTP page: no profile list measures one (kinds-plain's plain navigations are the typed page and a form POST), so mimicry sends the typed navigation's list, which has no Referer and, for Safari, Cookie after Connection (macOS Safari: the typed order as well)"
		// safariUpload: kinds-plain's POST fetches all carry a
		// Content-Type.
		safariUpload = "Safari put Content-Length after Accept on the page's upload, whose body has no Content-Type; kinds-plain's POST fetches all have one, and their list puts Content-Length after Origin, as mimicry does"
	)

	gaps := map[string]string{}
	for _, name := range []string{"firefox-158-macos", "firefox-158-windows", "firefox-159-macos", "firefox-159-windows", "firefox-159-linux"} {
		gaps[name+" GET http://capture.mimicry.test:PORT/app.js HTTP/1.1"] = "Firefox sent no Priority for the page's script, last in its body; kinds-plain's blocking script, in the head, went at u=2, the profile's one script list"
	}

	for _, name := range []string{"chrome-155-macos", testprofiles.InheritingName, "chrome-156-macos", "chrome-156-windows", "chrome-157-linux", "chrome-157-ios", "edge-157-macos", "firefox-158-macos", "firefox-159-ios", "firefox-158-windows", "firefox-159-macos", "firefox-159-windows", "firefox-159-linux", "safari-26-macos", "safari-27-macos", "safari-26-ios", "safari-27-ios"} {
		gaps[name+" "+nav] = linkNav
	}

	// Chrome and Firefox on the iPhone are WebKit (decision 0053).
	for _, name := range []string{"chrome-157-ios", "firefox-159-ios", "safari-26-macos", "safari-27-macos", "safari-26-ios", "safari-27-ios"} {
		gaps[name+" "+upload] = safariUpload
	}

	return gaps
}()

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
			p := profiles.ChromeMacOS().With(func(s *profile.Spec) {
				if tc.strip != nil {
					s.Proxy = tc.strip(s.Proxy)
				}
			})

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

			if _, err := tr.RoundTrip(req); !errors.Is(err, mimicry.ErrNotMeasured) {
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
	jar      *cookies.Jar
	proxy    *proxy.Proxy
	port     string
	pool     *x509.CertPool
	captures chan *capture.Capture
}

func startProxied(t *testing.T, p *profile.Spec, sc *server.Scenario, user *url.Userinfo, browserAuth bool) *proxySession {
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
		jar:      newJar(t, p),
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
		Profile: checked(p),
		RootCAs: s.pool,
		Cookies: s.jar,
		Proxy: http.ProxyURL(&url.URL{
			Scheme: sc.Proxy,
			Host:   pln.Addr().String(),
			User:   user,
		}),
		BrowserProxyAuth: browserAuth,
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
	s.send(http.MethodGet, scheme+"://"+net.JoinHostPort(host, s.port)+path, nil, mimicry.Request{}, nil)
}

// send makes a request as r describes, with the page-set headers h, and
// reads it.
func (s *proxySession) send(method, target string, body []byte, r mimicry.Request, h http.Header) {
	s.t.Helper()

	req, err := http.NewRequestWithContext(s.t.Context(), method, target, bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}

	if body == nil {
		req.Body = http.NoBody
	}

	maps.Copy(req.Header, h)

	resp, err := s.tr.RoundTrip(mimicry.With(req, r))
	if err != nil {
		s.t.Fatal(err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("%s %s: %s", method, req.URL, resp.Status)
	}
}

// page loads the capture page over plain HTTP as the browser does: the
// navigation, its stylesheet, image and script, the script's fetches, the
// cookies it sets, its navigation to /nav (a script's: no user
// activation) and that page's fetch of /done.
func (s *proxySession) page() {
	s.t.Helper()

	base := "http://" + net.JoinHostPort(server.CaptureHost, s.port)

	page, err := url.Parse(base + "/")
	if err != nil {
		s.t.Fatal(err)
	}

	nav, err := url.Parse(base + "/nav")
	if err != nil {
		s.t.Fatal(err)
	}

	on := func(k mimicry.Kind, from *url.URL) mimicry.Request {
		return mimicry.Request{
			Kind: k,
			From: from,
		}
	}

	s.send(http.MethodGet, base+"/", nil, mimicry.Request{}, nil)
	s.send(http.MethodGet, base+"/style.css", nil, on(mimicry.Style, page), nil)
	s.send(http.MethodGet, base+"/img.png", nil, on(mimicry.Image, page), nil)
	s.send(http.MethodGet, base+"/app.js", nil, on(mimicry.Script, page), nil)
	s.send(http.MethodGet, base+"/big", nil, on(mimicry.Fetch, page), nil)
	s.send(http.MethodPost, base+"/upload", make([]byte, 200_000), on(mimicry.Fetch, page), nil)
	s.send(http.MethodGet, base+"/again", nil, on(mimicry.Fetch, page), http.Header{
		"x-mimicry-huffman": {"{^}~"},
	})
	s.jar.SetCookies(cookies.Request{
		URL:  page,
		Page: page,
	}, []string{
		"mimicry_a=1; path=/nav",
		"mimicry_b=2; path=/nav",
	})
	s.send(http.MethodGet, base+"/nav", nil, on(mimicry.Navigate, page), http.Header{
		"Sec-Fetch-User": nil,
	})
	s.send(http.MethodGet, base+"/done", nil, on(mimicry.Fetch, nav), nil)
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
func (s *proxySession) checkTunnelledHello(p *profile.Spec) {
	s.t.Helper()

	c := s.capture()
	got := profile.Spec{
		TLS: c.Profile.TLS,
	}
	want := profile.Spec{
		TLS: p.TLS,
	}

	got.TLS.ExtensionOrder, want.TLS.ExtensionOrder = p.TLS.ExtensionOrder, p.TLS.ExtensionOrder

	for _, d := range diff.Compare(&want, &got) {
		s.t.Errorf("ClientHello through the tunnel: %s", d)
	}
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

// forwardedLike is the request in sample with g's request line, forwarded,
// or nil.
func forwardedLike(sample []capture.H1Request, g capture.H1Request) []string {
	line := portless([]string{g.Line})[0]
	for _, w := range sample {
		if portless([]string{w.Line})[0] == line {
			return forwarded(w)
		}
	}

	return nil
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
