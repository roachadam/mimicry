package dial

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry/profile"
)

// fakeProxy answers each connection a Dialer opens with the next of its
// handlers, and records the addresses dialed and the requests read.
type fakeProxy struct {
	t        *testing.T
	mu       sync.Mutex
	handlers []func(conn net.Conn, br *bufio.Reader) error
	dialed   []string
	requests []string
	wg       sync.WaitGroup
	errs     chan error
}

func newFakeProxy(t *testing.T, handlers ...func(conn net.Conn, br *bufio.Reader) error) *fakeProxy {
	p := &fakeProxy{
		t:        t,
		handlers: handlers,
		errs:     make(chan error, len(handlers)),
	}
	t.Cleanup(func() {
		p.wg.Wait()
		close(p.errs)
		for err := range p.errs {
			t.Error(err)
		}
	})
	return p
}

func (p *fakeProxy) dial(_ context.Context, network, addr string) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dialed = append(p.dialed, network+" "+addr)
	if len(p.handlers) == 0 {
		return nil, errors.New("no more connections expected")
	}
	h := p.handlers[0]
	p.handlers = p.handlers[1:]
	client, server := net.Pipe()
	p.t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	p.wg.Go(func() {
		if err := h(server, bufio.NewReader(server)); err != nil {
			p.errs <- err
		}
	})
	return client, nil
}

// request reads one request head and records it.
func (p *fakeProxy) request(br *bufio.Reader) error {
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		b.WriteString(line)
		if line == "\r\n" {
			break
		}
	}
	p.mu.Lock()
	p.requests = append(p.requests, b.String())
	p.mu.Unlock()
	return nil
}

func (p *fakeProxy) answer(conn net.Conn, br *bufio.Reader, answer string) error {
	if err := p.request(br); err != nil {
		return err
	}
	_, err := io.WriteString(conn, answer)
	return err
}

const (
	challenge         = "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"p\"\r\nContent-Length: 0\r\n\r\n"
	challengeAndClose = "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"p\"\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"
	established       = "HTTP/1.1 200 Connection established\r\n\r\n"
)

// Basic credentials for user:secret.
const auth = "Basic dXNlcjpzZWNyZXQ="

func dialer(p *fakeProxy, timing profile.CredentialTiming) *Dialer {
	return &Dialer{
		Timeout:     10 * time.Second,
		DialContext: p.dial,
		UserAgent:   "UA",
		Proxy: &profile.Proxy{
			Connect: []profile.Header{
				{
					Name: "Host",
				},
				{
					Name:  "Proxy-Connection",
					Value: "keep-alive",
				},
				{
					Name: "User-Agent",
				},
			},
			ConnectAuth: []profile.Header{
				{
					Name: "Host",
				},
				{
					Name:  "Proxy-Connection",
					Value: "keep-alive",
				},
				{
					Name: "User-Agent",
				},
				{
					Name: "Proxy-Authorization",
				},
			},
			ConnectCredentials: timing,
			ForwardCredentials: profile.CredentialsAfterChallenge,
			SOCKS5: &profile.SOCKS5{
				Methods: []int{
					0,
				},
				Address: profile.SOCKS5Domain,
			},
		},
	}
}

func proxyURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

const (
	plainConnect = "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Connection: keep-alive\r\nUser-Agent: UA\r\n\r\n"
	authConnect  = "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Connection: keep-alive\r\nUser-Agent: UA\r\nProxy-Authorization: " + auth + "\r\n\r\n"
)

func (p *fakeProxy) check(t *testing.T, dialed int, requests ...string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.dialed) != dialed {
		t.Errorf("dialed %v, want %d connections", p.dialed, dialed)
	}
	if strings.Join(p.requests, "|") != strings.Join(requests, "|") {
		t.Errorf("requests\n%q\nwant\n%q", p.requests, requests)
	}
}

func TestConnectAnswersTheChallengeOnTheSameConnection(t *testing.T) {
	p := newFakeProxy(t, nil)
	p.handlers[0] = func(conn net.Conn, br *bufio.Reader) error {
		if err := p.answer(conn, br, challenge); err != nil {
			return err
		}
		return p.answer(conn, br, established)
	}
	conn, err := dialer(p, profile.CredentialsAfterChallenge).DialVia(t.Context(), proxyURL(t, "http://user:secret@proxy:3128"), "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	p.check(t, 1, plainConnect, authConnect)
}

func TestConnectReconnectsWhenTheProxyClosesAfterItsChallenge(t *testing.T) {
	p := newFakeProxy(t, nil, nil)
	p.handlers = []func(conn net.Conn, br *bufio.Reader) error{
		func(conn net.Conn, br *bufio.Reader) error {
			if err := p.answer(conn, br, challengeAndClose); err != nil {
				return err
			}
			return conn.Close()
		},
		func(conn net.Conn, br *bufio.Reader) error {
			return p.answer(conn, br, established)
		},
	}
	conn, err := dialer(p, profile.CredentialsAfterChallenge).DialVia(t.Context(), proxyURL(t, "http://user:secret@proxy:3128"), "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	p.check(t, 2, plainConnect, authConnect)
}

func TestCredentialTiming(t *testing.T) {
	tests := []struct {
		timing profile.CredentialTiming
		// second is the next tunnel's first CONNECT.
		second string
	}{
		{
			timing: profile.CredentialsAfterChallenge,
			second: authConnect,
		},
		{
			timing: profile.CredentialsOnChallenge,
			second: plainConnect,
		},
	}
	for _, tt := range tests {
		t.Run(string(tt.timing), func(t *testing.T) {
			p := newFakeProxy(t, nil, nil)
			p.handlers = []func(conn net.Conn, br *bufio.Reader) error{
				func(conn net.Conn, br *bufio.Reader) error {
					if err := p.answer(conn, br, challenge); err != nil {
						return err
					}
					return p.answer(conn, br, established)
				},
				func(conn net.Conn, br *bufio.Reader) error {
					if err := p.request(br); err != nil {
						return err
					}
					p.mu.Lock()
					last := p.requests[len(p.requests)-1]
					p.mu.Unlock()
					answer := established
					if last != authConnect {
						answer = challenge
					}
					if _, err := io.WriteString(conn, answer); err != nil {
						return err
					}
					if answer == established {
						return nil
					}
					return p.answer(conn, br, established)
				},
			}
			d := dialer(p, tt.timing)
			u := proxyURL(t, "http://user:secret@proxy:3128")
			for range 2 {
				conn, err := d.DialVia(t.Context(), u, "example.com:443")
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
			}
			p.mu.Lock()
			got := p.requests[2]
			p.mu.Unlock()
			if got != tt.second {
				t.Fatalf("second tunnel's first CONNECT\n%q\nwant\n%q", got, tt.second)
			}
		})
	}
}

func TestConnectRefusalIsAnError(t *testing.T) {
	p := newFakeProxy(t, nil)
	p.handlers[0] = func(conn net.Conn, br *bufio.Reader) error {
		return p.answer(conn, br, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
	}
	_, err := dialer(p, profile.CredentialsAfterChallenge).DialVia(t.Context(), proxyURL(t, "http://proxy"), "example.com:443")
	if err == nil || !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("err = %v", err)
	}
	p.check(t, 1, plainConnect)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dialed[0] != "tcp proxy:80" {
		t.Fatalf("dialed %q, want the http default port", p.dialed[0])
	}
}

func TestChallengeWithoutCredentialsIsAnError(t *testing.T) {
	p := newFakeProxy(t, nil)
	p.handlers[0] = func(conn net.Conn, br *bufio.Reader) error {
		return p.answer(conn, br, challenge)
	}
	_, err := dialer(p, profile.CredentialsAfterChallenge).DialVia(t.Context(), proxyURL(t, "http://proxy:3128"), "example.com:443")
	if err == nil || !strings.Contains(err.Error(), "407") {
		t.Fatalf("err = %v", err)
	}
}

func TestSOCKS5OffersPasswordAuthWithCredentials(t *testing.T) {
	p := newFakeProxy(t, nil)
	p.handlers[0] = func(conn net.Conn, br *bufio.Reader) error {
		greeting := make([]byte, 4)
		if _, err := io.ReadFull(br, greeting); err != nil {
			return err
		}
		if string(greeting) != "\x05\x02\x00\x02" {
			return fmt.Errorf("greeting %x, want no auth then username/password", greeting)
		}
		// Refuse both, which ends the handshake.
		_, err := conn.Write([]byte{5, 0xff})
		return err
	}
	_, err := dialer(p, profile.CredentialsAfterChallenge).DialVia(t.Context(), proxyURL(t, "socks5h://user:secret@proxy"), "example.com:443")
	if err == nil || !strings.Contains(err.Error(), "accepts none") {
		t.Fatalf("err = %v", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dialed[0] != "tcp proxy:1080" {
		t.Fatalf("dialed %q, want the SOCKS default port", p.dialed[0])
	}
}

func TestDialTimeoutBoundsTheProxyHandshake(t *testing.T) {
	p := newFakeProxy(t, nil)
	release := make(chan struct{})
	p.handlers[0] = func(_ net.Conn, br *bufio.Reader) error {
		if err := p.request(br); err != nil {
			return err
		}
		<-release
		return nil
	}
	d := dialer(p, profile.CredentialsAfterChallenge)
	d.Timeout = 50 * time.Millisecond
	_, err := d.DialVia(t.Context(), proxyURL(t, "http://proxy"), "example.com:443")
	close(release)
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("err = %v, want the dial timeout", err)
	}
}
