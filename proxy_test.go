package mimicry_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profiles"
)

// fakeSOCKS5 is a SOCKS5 proxy that fails every connection as mode says:
// "no-method" (none of the offered methods), "refuse" (no auth, then
// connection refused), "bad-auth" (asks for a username and password, then
// rejects them), "silent" (never answers).
func fakeSOCKS5(t *testing.T, mode string) string {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() {
					_ = c.Close()
				}()
				head := make([]byte, 2)
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, head[1])); err != nil {
					return
				}
				switch mode {
				case "silent":
					_, _ = io.Copy(io.Discard, c)
				case "no-method":
					_, _ = c.Write([]byte{5, 0xff})
				case "refuse":
					_, _ = c.Write([]byte{5, 0})
					// VER CMD RSV ATYP(domain) LEN: the rest is the name
					// and port.
					req := make([]byte, 5)
					if _, err := io.ReadFull(c, req); err != nil {
						return
					}
					if _, err := io.ReadFull(c, make([]byte, int(req[4])+2)); err != nil {
						return
					}
					_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
				case "bad-auth":
					_, _ = c.Write([]byte{5, 2})
					// VER ULEN UNAME PLEN PASSWD.
					n := make([]byte, 2)
					if _, err := io.ReadFull(c, n); err != nil {
						return
					}
					if _, err := io.ReadFull(c, make([]byte, n[1])); err != nil {
						return
					}
					if _, err := io.ReadFull(c, n[:1]); err != nil {
						return
					}
					if _, err := io.ReadFull(c, make([]byte, n[0])); err != nil {
						return
					}
					_, _ = c.Write([]byte{1, 1})
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// silentListener accepts connections and never answers.
func silentListener(t *testing.T) string {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(io.Discard, c)
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// closedAddr is an address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// TestProxyErrors: every way a proxy fails a request is a *ProxyError
// naming it without its password, with the proxy's answer where there was
// one, wrapping the deadline where that was the cause.
func TestProxyErrors(t *testing.T) {
	const password = "s3cr3t-pw"
	shop := startShop(func(http.ResponseWriter, *http.Request) {
		t.Error("the request reached the server")
	})
	defer shop.Close()
	asking := startProxy(shop, "alice", "right")
	defer asking.Close()
	refusing := startProxy(shop, "", "")
	defer refusing.Close()
	refusing.Refuse("403 Forbidden")

	proxyURL := func(scheme, addr string, user *url.Userinfo) *url.URL {
		return &url.URL{
			Scheme: scheme,
			Host:   addr,
			User:   user,
		}
	}
	wrong := url.UserPassword("alice", password)
	for _, tc := range []struct {
		name   string
		proxy  *url.URL
		target string
		// timeout bounds the request; 0 is 10 s.
		timeout    time.Duration
		wantStatus int
		// wantChallenge: Header holds the proxy's Proxy-Authenticate.
		wantChallenge bool
		wantDeadline  bool
		// wantLog is what the CONNECT proxy was asked, when it was one.
		wantLog []string
	}{
		{
			name:   "unreachable",
			proxy:  proxyURL("http", closedAddr(t), wrong),
			target: "https://www.shop.test/",
		},
		{
			name:       "CONNECT refused",
			proxy:      proxyURL("http", refusing.URL.Host, nil),
			target:     "https://www.shop.test/",
			wantStatus: http.StatusForbidden,
			wantLog: []string{
				"CONNECT www.shop.test:443 without credentials: 403 Forbidden",
			},
		},
		{
			name:          "407 without credentials to give",
			proxy:         proxyURL("http", asking.URL.Host, nil),
			target:        "https://www.shop.test/",
			wantStatus:    http.StatusProxyAuthRequired,
			wantChallenge: true,
			wantLog: []string{
				"CONNECT www.shop.test:443 without credentials: 407",
			},
		},
		{
			name:          "407 after the credential flow",
			proxy:         proxyURL("http", asking.URL.Host, wrong),
			target:        "https://www.shop.test/",
			wantStatus:    http.StatusProxyAuthRequired,
			wantChallenge: true,
			wantLog: []string{
				"CONNECT www.shop.test:443 without credentials: 407",
				"CONNECT www.shop.test:443 as alice: 407",
			},
		},
		{
			name:          "plain HTTP 407 after the credential flow",
			proxy:         proxyURL("http", asking.URL.Host, wrong),
			target:        "http://www.shop.test/",
			wantStatus:    http.StatusProxyAuthRequired,
			wantChallenge: true,
			wantLog: []string{
				"GET www.shop.test without credentials: 407",
				"GET www.shop.test as alice: 407",
			},
		},
		{
			name:         "CONNECT unanswered",
			proxy:        proxyURL("http", silentListener(t), wrong),
			target:       "https://www.shop.test/",
			timeout:      300 * time.Millisecond,
			wantDeadline: true,
		},
		{
			name:   "SOCKS5 unreachable",
			proxy:  proxyURL("socks5h", closedAddr(t), nil),
			target: "https://www.shop.test/",
		},
		{
			name:   "SOCKS5 refused",
			proxy:  proxyURL("socks5h", fakeSOCKS5(t, "refuse"), nil),
			target: "https://www.shop.test/",
		},
		{
			name:   "SOCKS5 no acceptable method",
			proxy:  proxyURL("socks5", fakeSOCKS5(t, "no-method"), nil),
			target: "http://www.shop.test/",
		},
		{
			name:   "SOCKS5 credentials rejected",
			proxy:  proxyURL("socks5h", fakeSOCKS5(t, "bad-auth"), wrong),
			target: "https://www.shop.test/",
		},
		{
			name:         "SOCKS5 unanswered",
			proxy:        proxyURL("socks5h", fakeSOCKS5(t, "silent"), wrong),
			target:       "https://www.shop.test/",
			timeout:      300 * time.Millisecond,
			wantDeadline: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := map[*proxy]int{
				asking:   len(asking.Log()),
				refusing: len(refusing.Log()),
			}
			client, err := mimicry.NewClient(mimicry.Config{
				Profile: profiles.Chrome155MacOS(),
				Proxy:   http.ProxyURL(tc.proxy),
				RootCAs: shop.RootCAs,
			})
			if err != nil {
				t.Fatal(err)
			}
			timeout := tc.timeout
			if timeout == 0 {
				timeout = 10 * time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatalf("got %s, want a *ProxyError", resp.Status)
			}
			var pe *mimicry.ProxyError
			if !errors.As(err, &pe) {
				t.Fatalf("got %v (%T), want a *ProxyError", err, err)
			}
			if strings.Contains(err.Error(), password) {
				t.Errorf("the error shows the password: %v", err)
			}
			if _, ok := pe.Proxy.User.Password(); ok {
				t.Errorf("ProxyError.Proxy keeps the password: %v", pe.Proxy)
			}
			if pe.Proxy.Host != tc.proxy.Host || pe.Proxy.User.Username() != tc.proxy.User.Username() {
				t.Errorf("ProxyError.Proxy = %v, want %v without its password", pe.Proxy, tc.proxy.Redacted())
			}
			if pe.StatusCode != tc.wantStatus {
				t.Errorf("StatusCode = %d, want %d (%v)", pe.StatusCode, tc.wantStatus, err)
			}
			if got := pe.Header.Get("Proxy-Authenticate") != ""; got != tc.wantChallenge {
				t.Errorf("Header = %v, want Proxy-Authenticate: %t", pe.Header, tc.wantChallenge)
			}
			if tc.wantStatus == 0 && pe.Header != nil {
				t.Errorf("Header = %v with no answer", pe.Header)
			}
			if got := errors.Is(err, context.DeadlineExceeded); got != tc.wantDeadline {
				t.Errorf("errors.Is(err, context.DeadlineExceeded) = %t, want %t (%v)", got, tc.wantDeadline, err)
			}
			if tc.wantLog != nil {
				for p, n := range before {
					if p.URL.Host == tc.proxy.Host {
						if got := p.Log()[n:]; !slices.Equal(got, tc.wantLog) {
							t.Errorf("proxy was asked %q, want %q", got, tc.wantLog)
						}
					}
				}
			}
		})
	}
}
