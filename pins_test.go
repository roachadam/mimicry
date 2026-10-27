package mimicry_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profiles"
)

// loopbackDNS is a DNS server on UDP that answers every A query with
// 127.0.0.1 and every other query with no records, and a resolver using
// it, so that hosts the shop serves look up to its address (coalescing on
// lookup).
func loopbackDNS(t *testing.T) *net.Resolver {
	t.Helper()
	pc, err := new(net.ListenConfig).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pc.Close()
	})
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
				ID:                 h.ID,
				Response:           true,
				Authoritative:      true,
				RecursionDesired:   h.RecursionDesired,
				RecursionAvailable: true,
			})
			b.EnableCompression()
			_ = b.StartQuestions()
			_ = b.Question(q)
			_ = b.StartAnswers()
			if q.Type == dnsmessage.TypeA {
				_ = b.AResource(dnsmessage.ResourceHeader{
					Name:  q.Name,
					Class: dnsmessage.ClassINET,
					TTL:   60,
				}, dnsmessage.AResource{
					A: [4]byte{127, 0, 0, 1},
				})
			}
			msg, err := b.Finish()
			if err != nil {
				continue
			}
			_, _ = pc.WriteTo(msg, from)
		}
	}()
	addr := pc.LocalAddr().String()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", addr)
		},
	}
}

// pinStep is one request of a TestPins row, after before (if set).
type pinStep struct {
	url string
	// ws opens a WebSocket instead of a request.
	ws     bool
	before func(t *testing.T, tr *mimicry.Transport, chain []mimicry.CertPin)
	// wantPinErr: the step fails with a *PinError presenting the chain;
	// wantErr: with that error; neither: it succeeds.
	wantPinErr bool
	wantErr    error
	// wantProto, wantResumed and wantServerName are the response's TLS.
	wantProto      string
	wantResumed    bool
	wantServerName string
}

// TestPins: a pinned host's connections must hold a key or certificate
// one of its pins names, on every TLS handshake (HTTP/2, HTTP/1.1,
// resumed, WebSocket, through a proxy) and before a request to it goes on
// another host's HTTP/2 connection; VerifyConnection runs after the pins.
// Responses carry the connection's TLS state.
func TestPins(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live" {
			echoWebSocket(w, r)
		}
	}
	h2 := startShop(handler)
	defer h2.Close()
	h1 := startShopHTTP1(handler)
	defer h1.Close()
	proxy := startProxy(h2, "", "")
	defer proxy.Close()
	proxyH1 := startProxy(h1, "", "")
	defer proxyH1.Close()
	dns := loopbackDNS(t)
	errRejected := errors.New("rejected by VerifyConnection")
	const other = "sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

	pin := func(host string, spki ...string) []mimicry.Pin {
		return []mimicry.Pin{
			{
				Host: host,
				SPKI: spki,
			},
		}
	}
	for _, tc := range []struct {
		name     string
		shop     *shop
		proxy    bool
		resolver bool
		pins     func(chain []mimicry.CertPin) []mimicry.Pin
		// verify, when set, is Config.VerifyConnection; verifyCalls is
		// how often it must have run.
		verify      func(cs tls.ConnectionState) error
		verifyCalls int
		steps       []pinStep
	}{
		{
			name: "HTTP/2, the intermediate's key",
			shop: h2,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", other, c[1].SPKI)
			},
			steps: []pinStep{
				{
					url:            "https://www.shop.test/",
					wantProto:      "h2",
					wantServerName: "www.shop.test",
				},
			},
		},
		{
			name: "HTTP/2, the leaf's fingerprint",
			shop: h2,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("WWW.SHOP.TEST", c[0].Fingerprint)
			},
			steps: []pinStep{
				{
					url:       "https://www.shop.test/",
					wantProto: "h2",
				},
			},
		},
		{
			name: "HTTP/2, the root's key in hex",
			shop: h2,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", c[2].SPKIHex)
			},
			steps: []pinStep{
				{
					url:       "https://www.shop.test/",
					wantProto: "h2",
				},
			},
		},
		{
			name: "HTTP/2, another key",
			shop: h2,
			pins: func([]mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", other)
			},
			steps: []pinStep{
				{
					url:        "https://www.shop.test/",
					wantPinErr: true,
				},
			},
		},
		{
			name: "HTTP/1.1, the intermediate's key",
			shop: h1,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", c[1].SPKI)
			},
			steps: []pinStep{
				{
					url:       "https://www.shop.test/",
					wantProto: "http/1.1",
				},
			},
		},
		{
			name: "HTTP/1.1, another key",
			shop: h1,
			pins: func([]mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", other)
			},
			steps: []pinStep{
				{
					url:        "https://www.shop.test/",
					wantPinErr: true,
				},
			},
		},
		{
			name: "a parent's pin including subdomains",
			shop: h2,
			pins: func([]mimicry.CertPin) []mimicry.Pin {
				return []mimicry.Pin{
					{
						Host:              "shop.test",
						IncludeSubdomains: true,
						SPKI: []string{
							other,
						},
					},
				}
			},
			steps: []pinStep{
				{
					url:        "https://www.shop.test/",
					wantPinErr: true,
				},
			},
		},
		{
			name: "a parent's pin without subdomains",
			shop: h2,
			pins: func([]mimicry.CertPin) []mimicry.Pin {
				return pin("shop.test", other)
			},
			steps: []pinStep{
				{
					url:       "https://www.shop.test/",
					wantProto: "h2",
				},
			},
		},
		{
			name: "the host's own pin before its parent's",
			shop: h2,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return []mimicry.Pin{
					{
						Host:              "shop.test",
						IncludeSubdomains: true,
						SPKI: []string{
							other,
						},
					},
					{
						Host: "www.shop.test",
						SPKI: []string{
							c[1].SPKI,
						},
					},
				}
			},
			steps: []pinStep{
				{
					url:       "https://www.shop.test/",
					wantProto: "h2",
				},
			},
		},
		{
			name: "a resumed connection is checked",
			shop: h2,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", c[1].SPKI)
			},
			verify: func(tls.ConnectionState) error {
				return nil
			},
			verifyCalls: 2,
			steps: []pinStep{
				{
					url:       "https://www.shop.test/",
					wantProto: "h2",
				},
				{
					url: "https://www.shop.test/",
					before: func(_ *testing.T, tr *mimicry.Transport, _ []mimicry.CertPin) {
						tr.CloseIdleConnections()
					},
					wantProto:   "h2",
					wantResumed: true,
				},
				{
					url: "https://www.shop.test/",
					before: func(t *testing.T, tr *mimicry.Transport, _ []mimicry.CertPin) {
						if err := mimicry.SetPins(tr, pin("www.shop.test", other)); err != nil {
							t.Fatal(err)
						}
						tr.CloseIdleConnections()
					},
					wantPinErr: true,
				},
			},
		},
		{
			name: "VerifyConnection after the pins",
			shop: h2,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", c[1].SPKI)
			},
			verify: func(cs tls.ConnectionState) error {
				if cs.ServerName == "www.shop.test" && len(cs.VerifiedChains) > 0 {
					return errRejected
				}
				return nil
			},
			verifyCalls: 1,
			steps: []pinStep{
				{
					url:     "https://www.shop.test/",
					wantErr: errRejected,
				},
			},
		},
		{
			name: "VerifyConnection not run after a pin fails",
			shop: h2,
			pins: func([]mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", other)
			},
			verify: func(tls.ConnectionState) error {
				return errRejected
			},
			steps: []pinStep{
				{
					url:        "https://www.shop.test/",
					wantPinErr: true,
				},
			},
		},
		{
			name:  "through a CONNECT proxy, the intermediate's key",
			shop:  h2,
			proxy: true,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", c[1].SPKI)
			},
			steps: []pinStep{
				{
					url:       "https://www.shop.test/",
					wantProto: "h2",
				},
			},
		},
		{
			name:  "through a CONNECT proxy, another key",
			shop:  h2,
			proxy: true,
			pins: func([]mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", other)
			},
			steps: []pinStep{
				{
					url:        "https://www.shop.test/",
					wantPinErr: true,
				},
			},
		},
		{
			name: "WebSocket, the intermediate's key",
			shop: h1,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", c[1].SPKI)
			},
			steps: []pinStep{
				{
					url:       "wss://www.shop.test/live",
					ws:        true,
					wantProto: "http/1.1",
				},
			},
		},
		{
			name: "WebSocket, another key",
			shop: h1,
			pins: func([]mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", other)
			},
			steps: []pinStep{
				{
					url:        "wss://www.shop.test/live",
					ws:         true,
					wantPinErr: true,
				},
			},
		},
		{
			name:  "WebSocket through a CONNECT proxy, the intermediate's key",
			shop:  h1,
			proxy: true,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", c[1].SPKI)
			},
			steps: []pinStep{
				{
					url:       "wss://www.shop.test/live",
					ws:        true,
					wantProto: "http/1.1",
				},
			},
		},
		{
			name:  "WebSocket through a CONNECT proxy, another key",
			shop:  h1,
			proxy: true,
			pins: func([]mimicry.CertPin) []mimicry.Pin {
				return pin("www.shop.test", other)
			},
			steps: []pinStep{
				{
					url:        "wss://www.shop.test/live",
					ws:         true,
					wantPinErr: true,
				},
			},
		},
		{
			name:     "coalesced onto another host's connection",
			shop:     h2,
			resolver: true,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return append(pin("www.shop.test", c[1].SPKI), pin("www.outlet.test", c[0].SPKI)...)
			},
			steps: []pinStep{
				{
					url:            "https://www.shop.test/",
					wantProto:      "h2",
					wantServerName: "www.shop.test",
				},
				{
					url:            "https://www.outlet.test/",
					wantProto:      "h2",
					wantServerName: "www.shop.test",
				},
			},
		},
		{
			name:     "not coalesced against the other host's pins",
			shop:     h2,
			resolver: true,
			pins: func(c []mimicry.CertPin) []mimicry.Pin {
				return append(pin("www.shop.test", c[1].SPKI), pin("www.outlet.test", other)...)
			},
			steps: []pinStep{
				{
					url:       "https://www.shop.test/",
					wantProto: "h2",
				},
				{
					url:        "https://www.outlet.test/",
					wantPinErr: true,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mimicry.Config{
				Profile:     profiles.Chrome(),
				RootCAs:     tc.shop.RootCAs,
				DialContext: tc.shop.DialContext,
				// The shop speaks no HTTP/3; keep the steps on TCP.
				DisableHTTP3: true,
			}
			if tc.proxy {
				// Through the proxy that tunnels to the row's shop.
				via := proxy
				if tc.shop == h1 {
					via = proxyH1
				}
				cfg.DialContext = nil
				cfg.Proxy = http.ProxyURL(via.URL)
			}
			if tc.resolver {
				cfg.Resolver = dns
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			chain, err := mimicry.FetchPins(ctx, cfg, "https://www.shop.test/")
			if err != nil {
				t.Fatal(err)
			}
			roles := make([]string, len(chain))
			presented := make([]string, len(chain))
			for i, c := range chain {
				roles[i] = c.Role.String()
				presented[i] = c.SPKI
			}
			if want := []string{"leaf", "intermediate", "root"}; !slices.Equal(roles, want) {
				t.Fatalf("FetchPins roles %q, want %q", roles, want)
			}
			cfg.Pins = tc.pins(chain)
			var mu sync.Mutex
			calls := 0
			if tc.verify != nil {
				cfg.VerifyConnection = func(cs tls.ConnectionState) error {
					mu.Lock()
					calls++
					mu.Unlock()
					return tc.verify(cs)
				}
			}
			client, err := mimicry.NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			tr := client.Transport.(*mimicry.Transport)
			defer tr.CloseIdleConnections()
			for i, st := range tc.steps {
				if st.before != nil {
					st.before(t, tr, chain)
				}
				var state *tls.ConnectionState
				if st.ws {
					ws, resp, err := tr.DialWebSocket(ctx, st.url, mimicry.WebSocketOptions{})
					if err == nil {
						state = resp.TLS
						_ = ws.Close(1000, "")
					}
					checkPinStep(t, i, st, err, state, presented)
					continue
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, st.url, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := client.Do(req)
				if err == nil {
					state = resp.TLS
					_ = resp.Body.Close()
				}
				checkPinStep(t, i, st, err, state, presented)
			}
			mu.Lock()
			defer mu.Unlock()
			if calls != tc.verifyCalls {
				t.Errorf("VerifyConnection ran %d times, want %d", calls, tc.verifyCalls)
			}
		})
	}
}

// checkPinStep checks step i's outcome: err, or the response's TLS state.
func checkPinStep(t *testing.T, i int, st pinStep, err error, state *tls.ConnectionState, presented []string) {
	t.Helper()
	var pe *mimicry.PinError
	switch {
	case st.wantPinErr:
		if !errors.As(err, &pe) {
			t.Errorf("step %d: got %v, want a *PinError", i, err)
			return
		}
		host := strings.TrimPrefix(strings.TrimPrefix(st.url, "https://"), "wss://")
		host, _, _ = strings.Cut(host, "/")
		if pe.Host != host || !slices.Equal(pe.Presented, presented) {
			t.Errorf("step %d: PinError{%q, %q}, want {%q, %q}", i, pe.Host, pe.Presented, host, presented)
		}
		return
	case st.wantErr != nil:
		if !errors.Is(err, st.wantErr) {
			t.Errorf("step %d: got %v, want %v", i, err, st.wantErr)
		}
		return
	case err != nil:
		t.Errorf("step %d: %v", i, err)
		return
	}
	if state == nil {
		t.Errorf("step %d: the response has no TLS state", i)
		return
	}
	if !state.HandshakeComplete || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != st.wantProto || state.DidResume != st.wantResumed {
		t.Errorf("step %d: TLS state complete %t, %d verified chains, protocol %q, resumed %t; want complete, a chain, %q, resumed %t",
			i, state.HandshakeComplete, len(state.VerifiedChains), state.NegotiatedProtocol, state.DidResume, st.wantProto, st.wantResumed)
	}
	if st.wantServerName != "" && state.ServerName != st.wantServerName {
		t.Errorf("step %d: the response came on a connection to %q, want %q", i, state.ServerName, st.wantServerName)
	}
}

// TestPinConfigErrors: NewTransport refuses a Pin it cannot apply, naming
// what is wrong, rather than leaving a host unpinned.
func TestPinConfigErrors(t *testing.T) {
	const good = "sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	for _, tc := range []struct {
		name string
		pins []mimicry.Pin
		want string
	}{
		{
			name: "no SPKI",
			pins: []mimicry.Pin{
				{
					Host: "www.shop.test",
				},
			},
			want: "has no SPKI",
		},
		{
			name: "a host twice",
			pins: []mimicry.Pin{
				{
					Host: "www.shop.test",
					SPKI: []string{
						good,
					},
				},
				{
					Host: "WWW.shop.test.",
					SPKI: []string{
						good,
					},
				},
			},
			want: "twice",
		},
		{
			name: "a URL for a host",
			pins: []mimicry.Pin{
				{
					Host: "https://www.shop.test",
					SPKI: []string{
						good,
					},
				},
			},
			want: "not a host name",
		},
		{
			name: "a wildcard",
			pins: []mimicry.Pin{
				{
					Host: "*.shop.test",
					SPKI: []string{
						good,
					},
				},
			},
			want: "not a host name",
		},
		{
			name: "base64 that is not",
			pins: []mimicry.Pin{
				{
					Host: "www.shop.test",
					SPKI: []string{
						"sha256/not base64!",
					},
				},
			},
			want: "not base64",
		},
		{
			name: "a hash of the wrong length",
			pins: []mimicry.Pin{
				{
					Host: "www.shop.test",
					SPKI: []string{
						"sha256/AAAA",
					},
				},
			},
			want: "not a SHA-256 hash",
		},
		{
			name: "hex of the wrong length",
			pins: []mimicry.Pin{
				{
					Host: "www.shop.test",
					SPKI: []string{
						"AB:CD:EF",
					},
				},
			},
			want: "neither",
		},
		{
			name: "another hash's name",
			pins: []mimicry.Pin{
				{
					Host: "www.shop.test",
					SPKI: []string{
						"sha1/AAAAAAAAAAAAAAAAAAAAAAAAAAA=",
					},
				},
			},
			want: "neither",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mimicry.NewTransport(mimicry.Config{
				Profile: profiles.Chrome(),
				Pins:    tc.pins,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error saying %q", err, tc.want)
			}
		})
	}
}
