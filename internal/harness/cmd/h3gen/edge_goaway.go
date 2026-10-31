package main

import (
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// closeOnGoAway derives http3.closeOnGoaway from every QUIC connection the
// server sent GOAWAY on (a request of a step that closes over HTTP/3, in
// the h3 and the edge captures): the browser closed each itself before
// the server's CONNECTION_CLOSE a second later, with an application
// CONNECTION_CLOSE (0x1d) of H3_NO_ERROR, or none.
func (s *edgeSet) closeOnGoAway(e *collector) bool {
	closed, kept := 0, 0
	check := func(c *conn) {
		if c.c.H3 == nil {
			return
		}

		sc, err := server.ScenarioByName(c.scenario)
		if err != nil {
			e.add(c, "%v", err)

			return
		}

		if !slices.ContainsFunc(c.c.H3.Requests, func(r quictrace.H3Request) bool {
			return sc.GoesAwayH3(c.c.Host, r.Path)
		}) {
			return
		}

		if c.c.Close == nil || c.c.Close.By != "client" {
			kept++

			return
		}

		closed++

		if f := clientClose(c.c); f == nil || f.Code != 0x1d || f.ErrorCode == nil || *f.ErrorCode != h3NoError {
			e.add(c, "closed itself after the server's GOAWAY with %+v, not an application CONNECTION_CLOSE of H3_NO_ERROR", f)
		}
	}

	for _, c := range s.set.conns {
		check(c)
	}

	for _, p := range s.pages {
		for _, a := range p.attempts {
			check(p.conn(a))
		}
	}

	if closed > 0 && kept > 0 {
		edgeErr(e, s.set.name, "%d connections the server sent GOAWAY on were closed by the browser, %d were not", closed, kept)
	}

	return closed > 0 && kept == 0
}

// h3NoError is HTTP/3's H3_NO_ERROR (RFC 9114, section 8.1).
const h3NoError = 0x100

// clientClose is the client's first CONNECTION_CLOSE frame on c, nil for
// none.
func clientClose(c *quictrace.Conn) *quictrace.Frame {
	for di := range c.Datagrams {
		for pi := range c.Datagrams[di].Packets {
			p := &c.Datagrams[di].Packets[pi]
			for fi := range p.Frames {
				if p.Frames[fi].Type == quictrace.FrameConnectionClose {
					return &p.Frames[fi]
				}
			}
		}
	}

	return nil
}
