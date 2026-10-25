package main

import (
	"context"
	"net"

	"golang.org/x/net/dns/dnsmessage"
)

// loopbackResolver answers every A query with 127.0.0.1 and every other
// query with no records, so the engine flow reaches the local servers
// under the runner's name ("server", the name lsquic's certificates are
// configured for) through Config.Resolver.
func loopbackResolver(ctx context.Context) (*net.Resolver, error) {
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	context.AfterFunc(ctx, func() {
		_ = pc.Close()
	})
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if resp, err := loopbackAnswer(buf[:n]); err == nil {
				_, _ = pc.WriteTo(resp, addr)
			}
		}
	}()
	stub := pc.LocalAddr().String()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", stub)
		},
	}, nil
}

func loopbackAnswer(query []byte) ([]byte, error) {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID:                 h.ID,
		Response:           true,
		Authoritative:      true,
		RecursionDesired:   h.RecursionDesired,
		RecursionAvailable: true,
	})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(q); err != nil {
		return nil, err
	}
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	if q.Type == dnsmessage.TypeA {
		err := b.AResource(dnsmessage.ResourceHeader{
			Name:  q.Name,
			Class: dnsmessage.ClassINET,
			TTL:   60,
		}, dnsmessage.AResource{
			A: [4]byte{127, 0, 0, 1},
		})
		if err != nil {
			return nil, err
		}
	}
	return b.Finish()
}
