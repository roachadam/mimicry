// Package fingerprint compiles a profile once into an immutable plan, then
// fills in per-connection randomness (GREASE, extension shuffle, key
// shares) to produce each connection's ClientHello.
//
// The shuffle is drawn fresh per connection and reused for a second
// ClientHello after HelloRetryRequest. The HRR cookie's placement is a known
// gap; see profiles/EVIDENCE.md, "Behaviour in code, not in profiles".
package fingerprint

import (
	"math/rand/v2"
	"slices"

	"github.com/roachadam/mimicry/profile"
)

// Plan is a profile's ClientHello compiled once for every connection that
// uses it. It is immutable and safe for concurrent use.
type Plan struct {
	tls profile.TLS
	// movable are the indexes of the extensions a shuffle may move; nil when
	// the profile's extension order is fixed.
	movable []int
	// resumedECH is GREASE ECH's shape in a resumed hello; nil without
	// resumption or ECH.
	resumedECH *profile.ECH
	// quic is the hello sent over QUIC; nil without Profile.QUIC.
	quic *quicPlan
}

// Compile turns p's ClientHello into a Plan. p must be valid (profile.Parse
// or Validate), and the Plan shares its values: p must not change
// afterwards.
func Compile(p *profile.Profile) *Plan {
	plan := &Plan{
		tls: p.TLS,
	}
	if p.TLS.ExtensionOrder == profile.ExtensionOrderShuffled {
		for i := range p.TLS.Extensions {
			if !p.TLS.Extensions[i].Pinned() {
				plan.movable = append(plan.movable, i)
			}
		}
	}
	if r := p.TLS.Resumption; r != nil {
		for _, e := range p.TLS.Extensions {
			if e.ECH != nil {
				resumed := *e.ECH
				resumed.PayloadLengths = r.ECHPayloadLengths
				plan.resumedECH = &resumed
			}
		}
	}
	plan.quic = compileQUIC(p)
	return plan
}

// Hello is the ClientHello layout for one connection. GREASE values are
// placeholders and key shares carry no keys; the handshake fills both in,
// and the random, the session ID and GREASE ECH's random fields.
type Hello struct {
	// RecordVersion is the version of the records carrying the hello.
	RecordVersion      profile.Code
	LegacyVersion      profile.Code
	SessionIDLength    uint8
	CipherSuites       []profile.Code
	CompressionMethods []uint8
	// Extensions in the order this connection sends them.
	Extensions []profile.Extension
}

// Hello returns a new connection's ClientHello. For a shuffled profile, it
// draws a fresh uniform permutation of the movable extensions, so every
// connection gets its own order, as Chrome does.
func (p *Plan) Hello() Hello {
	return p.hello(rand.IntN) //nolint:gosec // order only needs to be uniform; math/rand/v2's default source is ChaCha8, seeded per process
}

// Resumes reports whether the browser resumes sessions (profile
// tls.resumption).
func (p *Plan) Resumes() bool {
	return p.tls.Resumption != nil
}

// ResumedHello returns a new connection's ClientHello offering a ticket:
// the full one, shuffled alike, minus the extensions the browser drops on
// resumption, with pre_shared_key last. Only for a plan that Resumes.
func (p *Plan) ResumedHello() Hello {
	h := p.Hello()
	h.Extensions = slices.DeleteFunc(h.Extensions, func(e profile.Extension) bool {
		return slices.Contains(p.tls.Resumption.Drop, e.Type)
	})
	for i := range h.Extensions {
		if h.Extensions[i].ECH != nil {
			h.Extensions[i].ECH = p.resumedECH
		}
	}
	h.Extensions = append(h.Extensions, profile.Extension{
		Type: profile.ExtPreSharedKey,
	})
	return h
}

// WebSocketHello returns h as the hello of a WebSocket's own HTTP/1.1
// connection (profile tls.websocket_h1). Only for a profile with
// tls.websocket_h1.
func (p *Plan) WebSocketHello(h Hello) Hello {
	ws := p.tls.WebSocketH1
	h.Extensions = slices.DeleteFunc(h.Extensions, func(e profile.Extension) bool {
		return slices.Contains(ws.Drop, e.Type)
	})
	for i := range h.Extensions {
		if h.Extensions[i].Type == profile.ExtALPN {
			// A copy of the extension: its values are the plan's.
			h.Extensions[i].ALPN = ws.ALPN
		}
	}
	return h
}

// hello builds a Hello, drawing the permutation with intn(n), which returns a
// uniform integer in [0, n).
func (p *Plan) hello(intn func(int) int) Hello {
	h := Hello{
		RecordVersion:      p.tls.RecordVersion,
		LegacyVersion:      p.tls.LegacyVersion,
		SessionIDLength:    p.tls.SessionIDLength,
		CipherSuites:       append([]profile.Code(nil), p.tls.CipherSuites...),
		CompressionMethods: append([]uint8(nil), p.tls.CompressionMethods...),
		Extensions:         make([]profile.Extension, len(p.tls.Extensions)),
	}
	// Extension values share slices with the plan; they are never modified
	// after Compile, and the handshake only reads them.
	copy(h.Extensions, p.tls.Extensions)
	// Fisher-Yates over the movable slots only: every permutation of the
	// movable extensions is equally likely, and pinned ones never move.
	m := p.movable
	for i := len(m) - 1; i > 0; i-- {
		j := intn(i + 1)
		h.Extensions[m[i]], h.Extensions[m[j]] = h.Extensions[m[j]], h.Extensions[m[i]]
	}
	return h
}
