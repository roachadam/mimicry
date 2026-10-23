package dnsstub

import (
	"context"
	"net"
	"slices"
	"testing"
)

// Names under .test resolve to 127.0.0.1 and nothing else (no IPv6 the
// capture server does not listen on); names outside .test are refused, so
// the stub can never redirect a real site.
func TestServe(t *testing.T) {
	conn, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, conn)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", conn.LocalAddr().String())
		},
	}
	for _, host := range []string{"www.kinds-a.test", "Static.Kinds-A.TEST", "capture.mimicry.test"} {
		addrs, err := r.LookupHost(t.Context(), host)
		if err != nil || !slices.Equal(addrs, []string{"127.0.0.1"}) {
			t.Errorf("%s resolved to %v, %v; want 127.0.0.1 only", host, addrs, err)
		}
	}
	if addrs, err := r.LookupHost(t.Context(), "www.example.com"); err == nil {
		t.Errorf("www.example.com resolved to %v; want refused", addrs)
	}
}
