// Package dnsstub answers DNS queries for names under .test with 127.0.0.1,
// so a browser that resolves through the system (Safari, which cannot be told
// to map hostnames) reaches the capture server under real-looking site names.
// macOS sends *.test lookups to it once ResolverFile holds ResolverConfig.
package dnsstub

import (
	"context"
	"net"
	"os"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	// Addr is where the stub listens.
	Addr = "127.0.0.1:15353"
	// ResolverFile routes *.test lookups on macOS (resolver(5)).
	ResolverFile = "/etc/resolver/test"
	// ResolverConfig is ResolverFile's content, pointing at Addr.
	ResolverConfig = "nameserver 127.0.0.1\nport 15353\n"
	// InstallCommand creates ResolverFile.
	InstallCommand = `sudo mkdir -p /etc/resolver && printf 'nameserver 127.0.0.1\nport 15353\n' | sudo tee /etc/resolver/test`
)

// ResolverInstalled reports whether ResolverFile routes *.test to the stub.
func ResolverInstalled() bool {
	data, err := os.ReadFile(ResolverFile)
	return err == nil && string(data) == ResolverConfig
}

// Serve answers queries arriving on conn until ctx ends: A records for names
// under .test are 127.0.0.1; other types under .test have no records (the
// capture server listens on IPv4 only); names outside .test are refused.
func Serve(ctx context.Context, conn net.PacketConn) error {
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stop()

	buf := make([]byte, 1500)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return err
		}

		resp, err := answer(buf[:n])
		if err != nil {
			continue // not a DNS query
		}

		_, _ = conn.WriteTo(resp, addr)
	}
}

func answer(query []byte) ([]byte, error) {
	var p dnsmessage.Parser

	h, err := p.Start(query)
	if err != nil {
		return nil, err
	}

	q, err := p.Question()
	if err != nil {
		return nil, err
	}

	inZone := strings.HasSuffix(strings.ToLower(q.Name.String()), ".test.")
	rcode := dnsmessage.RCodeSuccess

	if !inZone {
		rcode = dnsmessage.RCodeRefused
	}

	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID:               h.ID,
		Response:         true,
		Authoritative:    inZone,
		RecursionDesired: h.RecursionDesired,
		RCode:            rcode,
	})
	b.EnableCompression()

	if err := b.StartQuestions(); err != nil {
		return nil, err
	}

	if err := b.Question(q); err != nil {
		return nil, err
	}

	if inZone && q.Type == dnsmessage.TypeA && q.Class == dnsmessage.ClassINET {
		if err := b.StartAnswers(); err != nil {
			return nil, err
		}

		if err := b.AResource(dnsmessage.ResourceHeader{
			Name:  q.Name,
			Class: dnsmessage.ClassINET,
			TTL:   1,
		}, dnsmessage.AResource{
			A: [4]byte{127, 0, 0, 1},
		}); err != nil {
			return nil, err
		}
	}

	return b.Finish()
}
