package dial

import (
	"context"
	"net"
	"time"
)

// Dialer opens TCP connections. It is the one place connections start;
// proxies and resolver policy plug in here.
type Dialer struct {
	// Timeout bounds the TCP connect, on top of the context's deadline.
	Timeout time.Duration
}

// Dial connects to addr ("host:port"). It honours ctx's cancellation and
// deadline.
func (d *Dialer) Dial(ctx context.Context, addr string) (net.Conn, error) {
	nd := net.Dialer{
		Timeout:   d.Timeout,
		KeepAlive: 30 * time.Second,
	}
	return nd.DialContext(ctx, "tcp", addr)
}
