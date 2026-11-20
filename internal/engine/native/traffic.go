package native

import (
	"context"
	"net/url"

	"github.com/roachadam/mimicry/internal/traffic"
)

// trafficTag returns ctx carrying a tag for the sockets a dial under it
// opens to host through proxy, carrying p, and the tag; ctx and nil without
// Config.Traffic.
func (e *Engine) trafficTag(ctx context.Context, p traffic.Protocol, host string, proxy *url.URL) (context.Context, *traffic.Tag) {
	if e.traffic == nil {
		return ctx, nil
	}

	t := traffic.NewTag(p, traffic.Peer{
		Host:  host,
		Proxy: traffic.ProxyName(proxy),
	})

	return traffic.With(ctx, t), t
}

// alpnProtocol is the traffic protocol of a TCP connection that negotiated
// proto: HTTP/2 for "h2", else HTTP/1.1.
func alpnProtocol(proto string) traffic.Protocol {
	if proto == "h2" {
		return traffic.HTTP2
	}

	return traffic.HTTP1
}
