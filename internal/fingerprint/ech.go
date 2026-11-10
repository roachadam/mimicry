package fingerprint

import (
	"math/rand/v2"
	"slices"

	"github.com/roachadam/mimicry/profile"
)

// InnerHello is a connection's ClientHelloInner (profile tls.realEch.inner):
// its own extensions in this connection's order, as the server expands
// them, ech_outer_extensions where the copied ones go.
type InnerHello struct {
	CipherSuites []profile.Code
	// Extensions are the inner hello's own, ExtECHOuterExtensions among
	// them, and pre_shared_key last when it resumes a session.
	Extensions []profile.Extension
	// Copied are the types ech_outer_extensions copies from the outer
	// hello, in its order.
	Copied []profile.Code
}

// RealECH is the profile's tls.realEch: nil when it was not measured, or
// when the browser ignores ECH configs (both then send every server the
// usual hello).
func (p *Plan) RealECH() *profile.RealECH {
	if r := p.tls.RealECH; r != nil && !r.Ignore {
		return r
	}

	return nil
}

// ECHHello returns h, a connection's hello (Hello or ResumedHello), as the
// outer hello of a connection sending real ECH, carrying the inner one.
// For a resumed h the inner hello offers the ticket and the outer one
// carries no pre_shared_key, or a GREASE one (tls.realEch.outerPsk). Only
// for a plan whose RealECH is set.
func (p *Plan) ECHHello(h Hello) Hello {
	return p.echHello(h, rand.IntN)
}

func (p *Plan) echHello(h Hello, intn func(int) int) Hello {
	r := p.tls.RealECH
	in := r.Inner

	resumed := slices.ContainsFunc(h.Extensions, func(e profile.Extension) bool {
		return e.Type == profile.ExtPreSharedKey
	})
	if resumed {
		h.Extensions = slices.DeleteFunc(slices.Clone(h.Extensions), func(e profile.Extension) bool {
			return e.Type == profile.ExtPreSharedKey
		})
		h.GREASEPSK = r.OuterPSK == profile.OuterPSKGREASE
	}

	inner := &InnerHello{
		CipherSuites: in.CipherSuites,
		Extensions:   make([]profile.Extension, len(in.Extensions), len(in.Extensions)+1),
	}
	copy(inner.Extensions, in.Extensions)

	if in.ExtensionOrder == profile.ExtensionOrderShuffled {
		// Every own extension but ech_outer_extensions, which stays last.
		movable := inner.Extensions[:len(inner.Extensions)-1]
		for i := len(movable) - 1; i > 0; i-- {
			j := intn(i + 1)

			movable[i], movable[j] = movable[j], movable[i]
		}
	}

	if resumed {
		inner.Extensions = append(inner.Extensions, profile.Extension{
			Type: profile.ExtPreSharedKey,
		})
	}

	for _, e := range h.Extensions {
		own := slices.ContainsFunc(in.Extensions, func(o profile.Extension) bool {
			return o.Type == e.Type
		})
		if !own && !slices.Contains(in.Drop, e.Type) && e.Type != profile.ExtCookie {
			inner.Copied = append(inner.Copied, e.Type)
		}
	}

	h.Inner = inner

	return h
}

// RetryHello returns h as the hello sent again after the server rejected
// ECH: without the extensions the browser leaves out then
// (tls.realEch.retryDrop). Only for a plan whose RealECH is set.
func (p *Plan) RetryHello(h Hello) Hello {
	h.Extensions = slices.DeleteFunc(slices.Clone(h.Extensions), func(e profile.Extension) bool {
		return slices.Contains(p.tls.RealECH.RetryDrop, e.Type)
	})

	return h
}
