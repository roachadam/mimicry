package mimicry

import (
	"github.com/roachadam/mimicry/internal/traffic"
)

// TrafficMeter counts the bytes sent and received on the sockets of every
// Transport given it (Config.Traffic): TLS and QUIC overhead, handshakes,
// retransmitted QUIC packets, CORS preflights, redirect hops, Alt-Svc QUIC
// attempts and ECH's DNS lookups included. Counts are payload bytes, as the
// sockets read and write them: IP, TCP and UDP headers and the kernel's TCP
// retransmissions are not counted, nor are lookups through the system's
// resolver. Each socket counts on its own, so the cost is one uncontended
// atomic add per read or write. It is safe for concurrent use.
type TrafficMeter struct {
	m *traffic.Meter
}

// TrafficOptions configures a TrafficMeter.
type TrafficOptions struct {
	// ByHost also keeps a total per Peer: one entry for each host and
	// proxy reached, kept until Reset.
	ByHost bool
}

// NewTrafficMeter returns a meter counting from zero.
func NewTrafficMeter(opts TrafficOptions) *TrafficMeter {
	return &TrafficMeter{
		m: traffic.New(opts.ByHost),
	}
}

// Snapshot returns the counts so far: closed connections' and the open
// ones' bytes until now.
func (m *TrafficMeter) Snapshot() Traffic {
	return trafficOf(m.m.Snapshot(false))
}

// Reset returns the counts so far, as Snapshot, and starts again from
// zero.
func (m *TrafficMeter) Reset() Traffic {
	return trafficOf(m.m.Snapshot(true))
}

// Traffic is a TrafficMeter's counts, by what the connections carried.
type Traffic struct {
	// HTTP1 is TCP connections that carried HTTP/1.1 (plain http://
	// included).
	HTTP1 Bytes
	// HTTP2 is TCP connections that negotiated HTTP/2, an https proxy's
	// HTTP/2 connection included, whatever its tunnels carry.
	HTTP2 Bytes
	// HTTP3 is QUIC connections, attempts that lost the race to TCP and
	// connections to CONNECT-UDP and SOCKS5 proxies included.
	HTTP3 Bytes
	// WebSocket is the HTTP/1.1 connections WebSockets opened of their own,
	// their handshakes included. A WebSocket over HTTP/2 is in HTTP2.
	WebSocket Bytes
	// DNS is the lookups mimicry sends itself: ECH's HTTPS records.
	DNS Bytes
	// Other is connections that ended before their protocol was known (a
	// TCP connection that lost a race before its TLS handshake ended).
	Other Bytes

	// ByHost is the counts per host and proxy, with TrafficOptions.ByHost;
	// nil otherwise. A connection counts under the host it was dialed
	// for: an HTTP/2 connection other hosts coalesced onto counts under
	// its first (Config.DisableCoalescing gives each host its own).
	ByHost map[Peer]Bytes
}

// Total is every protocol's bytes.
func (t Traffic) Total() Bytes {
	var b Bytes
	for _, p := range []Bytes{t.HTTP1, t.HTTP2, t.HTTP3, t.WebSocket, t.DNS, t.Other} {
		b.Sent += p.Sent
		b.Received += p.Received
	}

	return b
}

// Bytes is what was sent and received.
type Bytes struct {
	Sent, Received int64
}

// Peer is where connections went: the host they were dialed for and the
// proxy they went through, its scheme, host and port without credentials
// ("" for none). An https proxy's HTTP/2 connection, carrying tunnels to
// many hosts, has no Host; a DNS lookup's Host is the nameserver.
type Peer struct {
	Host, Proxy string
}

func trafficOf(s traffic.Snapshot) Traffic {
	b := func(p traffic.Protocol) Bytes {
		return Bytes{
			Sent:     s.Protocols[p].Sent,
			Received: s.Protocols[p].Received,
		}
	}

	t := Traffic{
		HTTP1:     b(traffic.HTTP1),
		HTTP2:     b(traffic.HTTP2),
		HTTP3:     b(traffic.HTTP3),
		WebSocket: b(traffic.WebSocket),
		DNS:       b(traffic.DNS),
		Other:     b(traffic.Other),
	}

	if s.Hosts != nil {
		t.ByHost = make(map[Peer]Bytes, len(s.Hosts))
		for p, v := range s.Hosts {
			t.ByHost[Peer{
				Host:  p.Host,
				Proxy: p.Proxy,
			}] = Bytes{
				Sent:     v.Sent,
				Received: v.Received,
			}
		}
	}

	return t
}
