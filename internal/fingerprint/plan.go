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
	return plan
}

// Hello is the ClientHello layout for one connection. GREASE values are
// placeholders and key shares carry no keys; the handshake fills both in.
type Hello struct {
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

// ResumedHello returns a new connection's ClientHello offering a ticket: the
// full one, shuffled alike, without the extensions the browser drops when
// resuming, and with pre_shared_key last. Only for a plan that Resumes.
func (p *Plan) ResumedHello() Hello {
	h := p.Hello()
	h.Extensions = slices.DeleteFunc(h.Extensions, func(e profile.Extension) bool {
		return slices.Contains(p.tls.Resumption.Drop, e.Type)
	})
	h.Extensions = append(h.Extensions, profile.Extension{
		Type: profile.ExtPreSharedKey,
	})
	return h
}

// hello builds a Hello, drawing the permutation with intn(n), which returns a
// uniform integer in [0, n).
func (p *Plan) hello(intn func(int) int) Hello {
	h := Hello{
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
