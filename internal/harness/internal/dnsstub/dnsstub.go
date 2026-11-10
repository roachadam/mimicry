// Package dnsstub answers DNS queries for names under .test with the capture
// server's address, so a browser that resolves through the system (Safari,
// which cannot be told to map hostnames) reaches the capture server under
// real-looking site names. macOS sends *.test lookups to it once
// ResolverFile holds ResolverConfig; a phone capturing over the network
// (cmd/capture -listen) uses it as its only DNS server, so there it forwards
// every other name to the Mac's own resolver. DoH answers the same queries
// over DNS-over-HTTPS (RFC 8484), for a browser that can be pointed at a
// resolver of its own but not at the system's (Chrome on macOS, the ech
// scenario).
package dnsstub

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	// Addr is where the stub for this machine's Safari listens.
	Addr = "127.0.0.1:15353"
	// LANPort is the port of the stub a device on the network uses: its DNS
	// settings take an address but no port.
	LANPort = 53
	// ResolverFile routes *.test lookups on macOS (resolver(5)).
	ResolverFile = "/etc/resolver/test"
	// ResolverConfig is ResolverFile's content, pointing at Addr.
	ResolverConfig = "nameserver 127.0.0.1\nport 15353\n"
	// InstallCommand creates ResolverFile.
	InstallCommand = `sudo mkdir -p /etc/resolver && printf 'nameserver 127.0.0.1\nport 15353\n' | sudo tee /etc/resolver/test`
	// forwardTimeout bounds a forwarded query's wait for its answer.
	forwardTimeout = 5 * time.Second
	// maxForwards bounds the forwarded queries waiting at once; past it a
	// query is answered SERVFAIL.
	maxForwards = 64
)

// Config is what a stub answers.
type Config struct {
	// Answer is the A record of every name under .test: where the capture
	// server listens.
	Answer netip.Addr
	// Upstream, when set, is the resolver ("host:port") names outside .test
	// are forwarded to; otherwise they are refused.
	Upstream string
	// Clients, when valid, are the addresses answered (the -listen
	// network); every query from elsewhere is refused, so the stub is no
	// open resolver.
	Clients netip.Prefix
	// HTTPS, when set, gives the HTTPS record (RFC 9460) of a name under
	// .test (lowercase, without the final dot): its service parameters;
	// ok is false for a name without one.
	HTTPS func(name string) (params []dnsmessage.SVCParam, ok bool)
	// OnQuery, when set, is told of every query for a name under .test
	// (lowercase, without the final dot) and how it came (ViaSystem,
	// ViaDoH), from the goroutine answering it.
	OnQuery func(name string, typ dnsmessage.Type, via string)
}

// How a query reached the stub, for Config.OnQuery.
const (
	// ViaSystem is a query over UDP: from the system's resolver, or a
	// device using the stub as its DNS server.
	ViaSystem = "system"
	// ViaDoH is a query to DoH.
	ViaDoH = "doh"
)

// ResolverInstalled reports whether ResolverFile routes *.test to the stub.
func ResolverInstalled() bool {
	data, err := os.ReadFile(ResolverFile)
	return err == nil && string(data) == ResolverConfig
}

// Serve answers queries arriving on conn until ctx ends: A records for names
// under .test are cfg.Answer; other types under .test have no records (the
// capture server listens on IPv4 only); names under resolver.arpa do not
// exist, so a device never learns of an encrypted resolver that would
// bypass the stub (RFC 9462); other names go to cfg.Upstream, or are
// refused without one. Queries from outside cfg.Clients are refused.
func Serve(ctx context.Context, conn net.PacketConn, cfg Config) error {
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stop()

	forwards := make(chan struct{}, maxForwards)
	buf := make([]byte, 1500)

	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return err
		}

		fail := dnsmessage.RCodeSuccess
		if cfg.Clients.IsValid() && !cfg.Clients.Contains(peer(addr)) {
			fail = dnsmessage.RCodeRefused
		}

		resp, forward, err := answer(buf[:n], cfg, fail, ViaSystem)
		if err != nil {
			continue // not a DNS query
		}

		if forward {
			select {
			case forwards <- struct{}{}:
			default:
				if resp, _, err := answer(buf[:n], cfg, dnsmessage.RCodeServerFailure, ViaSystem); err == nil {
					_, _ = conn.WriteTo(resp, addr)
				}

				continue
			}

			query := append([]byte(nil), buf[:n]...)
			go func() {
				defer func() {
					<-forwards
				}()

				if resp, err := exchange(ctx, cfg.Upstream, query); err == nil {
					_, _ = conn.WriteTo(resp, addr)
				}
			}()

			continue
		}

		_, _ = conn.WriteTo(resp, addr)
	}
}

// peer is addr's IP address, unmapped; invalid for another kind of address.
func peer(addr net.Addr) netip.Addr {
	if u, ok := addr.(*net.UDPAddr); ok {
		return u.AddrPort().Addr().Unmap()
	}

	return netip.Addr{}
}

// exchange sends query to the resolver at upstream and returns its answer.
func exchange(ctx context.Context, upstream string, query []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, forwardTimeout)
	defer cancel()

	c, err := (&net.Dialer{}).DialContext(ctx, "udp", upstream)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = c.Close()
	}()

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(deadline)
	}

	if _, err := c.Write(query); err != nil {
		return nil, err
	}

	// EDNS(0) answers can be larger than a query's 1500 bytes.
	resp := make([]byte, 65535)
	for {
		n, err := c.Read(resp)
		if err != nil {
			return nil, err
		}

		// The ID must match; anything else on the socket is ignored.
		if n >= 2 && resp[0] == query[0] && resp[1] == query[1] {
			return resp[:n], nil
		}
	}
}

// answer returns the stub's own answer to query, which came via (ViaSystem,
// ViaDoH), or forward when query is for cfg.Upstream. A fail rcode (not
// RCodeSuccess) is the answer to any query, forwarded or not.
func answer(query []byte, cfg Config, fail dnsmessage.RCode, via string) ([]byte, bool, error) {
	var p dnsmessage.Parser

	h, err := p.Start(query)
	if err != nil {
		return nil, false, err
	}

	q, err := p.Question()
	if err != nil {
		return nil, false, err
	}

	name := strings.ToLower(q.Name.String())
	inZone := strings.HasSuffix(name, ".test.") && fail == dnsmessage.RCodeSuccess
	ddr := name == "resolver.arpa." || strings.HasSuffix(name, ".resolver.arpa.")

	if inZone && cfg.OnQuery != nil {
		cfg.OnQuery(strings.TrimSuffix(name, "."), q.Type, via)
	}

	if !inZone && !ddr && cfg.Upstream != "" && fail == dnsmessage.RCodeSuccess {
		return nil, true, nil
	}

	rcode := fail

	switch {
	case fail != dnsmessage.RCodeSuccess:
	case ddr && cfg.Upstream != "":
		rcode = dnsmessage.RCodeNameError
	case !inZone:
		rcode = dnsmessage.RCodeRefused
	}

	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID:                 h.ID,
		Response:           true,
		Authoritative:      inZone,
		RecursionDesired:   h.RecursionDesired,
		RecursionAvailable: cfg.Upstream != "",
		RCode:              rcode,
	})
	b.EnableCompression()

	if err := b.StartQuestions(); err != nil {
		return nil, false, err
	}

	if err := b.Question(q); err != nil {
		return nil, false, err
	}

	if !inZone || q.Class != dnsmessage.ClassINET {
		resp, err := b.Finish()

		return resp, false, err
	}

	header := dnsmessage.ResourceHeader{
		Name:  q.Name,
		Class: dnsmessage.ClassINET,
		TTL:   1,
	}

	switch q.Type {
	case dnsmessage.TypeA:
		if err := b.StartAnswers(); err != nil {
			return nil, false, err
		}

		if err := b.AResource(header, dnsmessage.AResource{
			A: cfg.Answer.As4(),
		}); err != nil {
			return nil, false, err
		}

	case dnsmessage.TypeHTTPS:
		if cfg.HTTPS == nil {
			break
		}

		params, ok := cfg.HTTPS(strings.TrimSuffix(name, "."))
		if !ok {
			break
		}

		if err := b.StartAnswers(); err != nil {
			return nil, false, err
		}

		// A ServiceMode record for the name itself (TargetName ".").
		if err := b.HTTPSResource(header, dnsmessage.HTTPSResource{
			Priority: 1,
			Target:   dnsmessage.MustNewName("."),
			Params:   params,
		}); err != nil {
			return nil, false, err
		}

	default:
		// No records of other types: the capture server listens on IPv4.
	}

	resp, err := b.Finish()

	return resp, false, err
}

// maxDoHQuery bounds a DoH query's size: a DNS message over UDP or HTTPS
// is at most 64 KiB.
const maxDoHQuery = 65535

// DoH answers DNS-over-HTTPS queries (RFC 8484, GET with ?dns= or POST) as
// Serve answers queries over UDP, without forwarding: names outside .test
// are refused.
func DoH(cfg Config) http.Handler {
	cfg.Upstream = ""

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var (
			query []byte
			err   error
		)

		switch r.Method {
		case http.MethodGet:
			query, err = base64.RawURLEncoding.DecodeString(r.URL.Query().Get("dns"))
		case http.MethodPost:
			query, err = io.ReadAll(io.LimitReader(r.Body, maxDoHQuery))
		default:
			http.Error(w, "GET or POST", http.StatusMethodNotAllowed)

			return
		}

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		resp, _, err := answer(query, cfg, dnsmessage.RCodeSuccess, ViaDoH)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		w.Header().Set("Content-Type", "application/dns-message")

		_, _ = w.Write(resp) //nolint:gosec // a DNS message, application/dns-message, never rendered
	})
}
