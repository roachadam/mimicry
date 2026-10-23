package fingerprint

import (
	"math/rand/v2"

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

// Compile validates p and copies its ClientHello into a Plan. Later changes to
// p do not affect the Plan.
func Compile(p *profile.Profile) (*Plan, error) {
	// A round trip through the canonical encoding is a deep copy, and Parse
	// validates the profile.
	data, err := profile.Encode(p)
	if err != nil {
		return nil, err
	}
	copied, err := profile.Parse(data)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		tls: copied.TLS,
	}
	if copied.TLS.ExtensionOrder == profile.ExtensionOrderShuffled {
		for i := range copied.TLS.Extensions {
			if !copied.TLS.Extensions[i].Pinned() {
				plan.movable = append(plan.movable, i)
			}
		}
	}
	return plan, nil
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
