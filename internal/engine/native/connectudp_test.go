package native

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/roachadam/mimicry/internal/proxy"
	"github.com/roachadam/mimicry/profile"
)

// A CONNECT-UDP proxy that several proxies map to keeps what it taught (its
// refusal here) while any of them is remembered: sweeping one forgets its
// own state only, the last one the CONNECT-UDP proxy's too.
func TestSharedConnectUDPProxyIsForgottenLast(t *testing.T) {
	e := newTestEngine(t, options{})
	udp := &url.URL{
		Scheme: "https",
		Host:   "masque.test:443",
	}

	_, err := e.dialer.ConnectUDP(t.Context(), udp, func(context.Context, []profile.Header) (proxy.Answer, net.PacketConn, error) {
		return proxy.Answer{
			StatusCode: http.StatusNotImplemented,
			Status:     "501 Not Implemented",
		}, nil, nil
	})
	if err == nil || !e.dialer.UDPRefused(udp) {
		t.Fatalf("a 501 was not remembered as a refusal: %v", err)
	}

	vias := []string{
		"http://a.test:8080",
		"http://b.test:8080",
	}

	e.proxies.mu.Lock()
	defer e.proxies.mu.Unlock()

	for _, via := range vias {
		e.proxies.byURL[via] = &proxyKeys{
			keys: map[string]struct{}{},
			udp:  udp,
		}
	}

	for i, via := range vias {
		delete(e.proxies.byURL, via)
		e.forgetProxy(staleProxy{
			via: via,
			udp: udp,
		})

		if got, want := e.dialer.UDPRefused(udp), i < len(vias)-1; got != want {
			t.Errorf("after forgetting %s the CONNECT-UDP proxy's refusal is remembered: %v, want %v", via, got, want)
		}
	}
}
