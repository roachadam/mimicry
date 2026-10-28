package fingerprint

import (
	"math/rand/v2"
	"slices"

	"github.com/roachadam/mimicry/profile"
)

// quicPlan is the ClientHello a browser sends over QUIC (profile
// tls.quic), compiled for fresh and resumed connections, and for one
// retried after Version Negotiation.
type quicPlan struct {
	cipherSuites    []profile.Code
	sessionIDLength uint8
	fresh, resumed  quicHello
	// retry is nil without tls.quic.versionNegotiation.
	retry *quicHello
}

// quicHello is one QUIC hello's extensions in their base order, the
// indexes a shuffle may move (nil when the order is fixed), and whether
// it reuses the X25519 key: tls.x25519Reuse, where both shares are sent
// (not in Firefox's hello retried after Version Negotiation).
type quicHello struct {
	extensions  []profile.Extension
	movable     []int
	x25519Reuse bool
}

// compileQUIC compiles p's QUIC hello; nil without Profile.QUIC.
func compileQUIC(p *profile.Profile) *quicPlan {
	q := p.TLS.QUIC
	if q == nil || p.QUIC == nil {
		return nil
	}

	plan := &quicPlan{
		cipherSuites:    q.CipherSuites,
		sessionIDLength: q.SessionIDLength,
		fresh:           compileQUICHello(p, false),
	}
	if p.TLS.Resumption != nil {
		plan.resumed = compileQUICHello(p, true)
	}

	if q.VersionNegotiation != nil {
		retry := compileQUICRetry(plan.fresh, q.VersionNegotiation)

		plan.retry = &retry
	}

	return plan
}

// compileQUICRetry lays out the hello of a connection retried after
// Version Negotiation: the full one without r's drops, bodies replaced in
// place, padding last where r pads (Firefox: 512 bytes in all).
func compileQUICRetry(fresh quicHello, r *profile.QUICHelloRetry) quicHello {
	out := quicHello{}
	at := map[int]int{}

	for i, e := range fresh.extensions {
		if slices.Contains(r.Drop, e.Type) {
			continue
		}

		for _, rep := range r.Replace {
			if rep.Type == e.Type {
				e = rep
			}
		}

		at[i] = len(out.extensions)
		out.extensions = append(out.extensions, e)
	}

	for _, i := range fresh.movable {
		if j, ok := at[i]; ok {
			out.movable = append(out.movable, j)
		}
	}

	if r.Padding {
		out.extensions = append(out.extensions, profile.Extension{
			Type: profile.ExtPadding,
		})
	}

	out.x25519Reuse = fresh.x25519Reuse && bothShares(out.extensions)

	return out
}

// compileQUICHello lays out the QUIC hello's extensions from TLS's, with
// tls.quic's drops, replaced bodies and quic_transport_parameters placed as
// the profile says; a resumed hello adds early_data where the browser sends
// 0-RTT and ends with pre_shared_key.
func compileQUICHello(p *profile.Profile, resumed bool) quicHello {
	q := p.TLS.QUIC
	shuffled := q.ExtensionOrder == profile.ExtensionOrderShuffled
	tp := profile.Extension{
		Type: profile.ExtQUICTransportParameters,
	}

	var exts []profile.Extension

	placed := false

	for _, e := range p.TLS.Extensions {
		if slices.Contains(q.Drop, e.Type) || e.Type == profile.ExtPreSharedKey {
			continue
		}

		if resumed && slices.Contains(p.TLS.Resumption.Drop, e.Type) {
			continue
		}

		for _, r := range q.Replace {
			if r.Type == e.Type {
				e = r
			}
		}

		if resumed && e.ECH != nil && len(q.ResumedECHPayloadLengths) > 0 {
			ech := *e.ECH

			ech.PayloadLengths = q.ResumedECHPayloadLengths
			e.ECH = &ech
		}

		if !shuffled && !placed && q.TransportParametersBefore != nil && e.Type == *q.TransportParametersBefore {
			exts = append(exts, tp)
			placed = true
		}

		exts = append(exts, e)
	}

	if !placed {
		exts = append(exts, tp)
	}

	if resumed && p.QUIC.ZeroRTT {
		exts = append(exts, profile.Extension{
			Type: profile.ExtEarlyData,
		})
	}

	h := quicHello{}
	if shuffled {
		// The extensions kept last go to the end, in tls.quic.last's
		// order; the others may move.
		var last []profile.Extension
		for _, typ := range q.Last {
			i := slices.IndexFunc(exts, func(e profile.Extension) bool {
				return e.Type == typ
			})
			if i >= 0 {
				last = append(last, exts[i])
				exts = slices.Delete(exts, i, i+1)
			}
		}

		for i := range exts {
			if !exts[i].Pinned() {
				h.movable = append(h.movable, i)
			}
		}

		exts = append(exts, last...)
	}

	if resumed {
		exts = append(exts, profile.Extension{
			Type: profile.ExtPreSharedKey,
		})
	}

	h.extensions = exts
	h.x25519Reuse = p.TLS.X25519Reuse && bothShares(exts)

	return h
}

// bothShares reports whether exts' key_share has X25519MLKEM768's share
// and then x25519's, as X25519 key reuse needs.
func bothShares(exts []profile.Extension) bool {
	for _, e := range exts {
		if e.Type == profile.ExtKeyShare {
			hybrid := slices.Index(e.KeyShares, 0x11ec) // X25519MLKEM768
			x25519 := slices.Index(e.KeyShares, 0x001d)

			return hybrid >= 0 && x25519 > hybrid
		}
	}

	return false
}

// HasQUIC reports whether the profile speaks QUIC (Profile.QUIC).
func (p *Plan) HasQUIC() bool {
	return p.quic != nil
}

// QUICHello returns a new QUIC connection's ClientHello (profile tls.quic),
// a shuffled one in a fresh order. Only for a plan that HasQUIC.
func (p *Plan) QUICHello() Hello {
	return p.quicHello(p.quic.fresh, rand.IntN)
}

// ResumedQUICHello returns a new QUIC connection's ClientHello offering a
// ticket: early_data when the browser sends 0-RTT, pre_shared_key last.
// Only for a plan that HasQUIC and Resumes.
func (p *Plan) ResumedQUICHello() Hello {
	return p.quicHello(p.quic.resumed, rand.IntN)
}

// RetryQUICHello returns the ClientHello of a QUIC connection retried
// after Version Negotiation (profile tls.quic.versionNegotiation), a
// shuffled one in a fresh order, and whether the profile has one.
func (p *Plan) RetryQUICHello() (Hello, bool) {
	if p.quic == nil || p.quic.retry == nil {
		return Hello{}, false
	}

	return p.quicHello(*p.quic.retry, rand.IntN), true
}

// quicHello builds a QUIC Hello from h, drawing the permutation with
// intn(n), a uniform integer in [0, n).
func (p *Plan) quicHello(h quicHello, intn func(int) int) Hello {
	out := Hello{
		RecordVersion:      p.tls.RecordVersion,
		LegacyVersion:      p.tls.LegacyVersion,
		SessionIDLength:    p.quic.sessionIDLength,
		CipherSuites:       append([]profile.Code(nil), p.quic.cipherSuites...),
		CompressionMethods: append([]uint8(nil), p.tls.CompressionMethods...),
		Extensions:         make([]profile.Extension, len(h.extensions)),
		X25519Reuse:        h.x25519Reuse,
	}
	copy(out.Extensions, h.extensions)

	m := h.movable
	for i := len(m) - 1; i > 0; i-- {
		j := intn(i + 1)

		out.Extensions[m[i]], out.Extensions[m[j]] = out.Extensions[m[j]], out.Extensions[m[i]]
	}

	return out
}
