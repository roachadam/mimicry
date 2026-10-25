package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/profile"
)

// quicConn is one captured QUIC connection's ClientHello and where it is.
type quicConn struct {
	at      string
	hello   *profile.TLS
	resumed bool
}

// quicHello derives tls.quic from the ClientHello of every QUIC connection
// in the h3 captures (<name>.h3-*.pagetrace.json.gz, forced or not): how it
// differs from t, the TLS hello. Every full hello must be t's with the same
// cipher suites, session ID, dropped extensions and replaced bodies, and
// quic_transport_parameters added; their orders all alike (fixed) or not
// (shuffled, keeping the extensions every one ends with last). Every
// resumed hello must be a full one resumed as t.Resumption says, with
// early_data. Nil without h3 captures.
func quicHello(dir, name string, t *profile.TLS) (*profile.QUICHello, error) {
	conns, err := quicConns(dir, name)
	if err != nil || len(conns) == 0 {
		return nil, err
	}
	var full, resumed []quicConn
	for _, c := range conns {
		if c.resumed {
			resumed = append(resumed, c)
		} else {
			full = append(full, c)
		}
	}
	if len(full) == 0 {
		return nil, errors.New("no full QUIC handshake captured")
	}
	first := full[0].hello
	out := &profile.QUICHello{
		CipherSuites:    first.CipherSuites,
		SessionIDLength: first.SessionIDLength,
	}
	for _, e := range t.Extensions {
		if extension(first, e.Type) == nil && !slices.Contains(out.Drop, e.Type) {
			out.Drop = append(out.Drop, e.Type)
		}
	}
	for _, e := range first.Extensions {
		if e.Type != profile.ExtQUICTransportParameters && extension(t, e.Type) == nil {
			return nil, fmt.Errorf("%s: extension %s is not in the TLS hello", full[0].at, e.Type)
		}
	}
	if err := replacements(out, t, full); err != nil {
		return nil, err
	}
	order(out, full, resumed)
	if len(resumed) > 0 && t.Resumption != nil && extension(t, profile.ExtEncryptedClientHello) != nil {
		for _, c := range resumed {
			if e := extension(c.hello, profile.ExtEncryptedClientHello); e != nil {
				out.ResumedECHPayloadLengths = append(out.ResumedECHPayloadLengths, e.ECH.PayloadLengths...)
			}
		}
		slices.Sort(out.ResumedECHPayloadLengths)
		out.ResumedECHPayloadLengths = slices.Compact(out.ResumedECHPayloadLengths)
		// As for full hellos: lengths among the TLS hello's resumed set are
		// picked from it (Chrome's nine resumed QUIC hellos showed two of
		// its four).
		if within(out.ResumedECHPayloadLengths, t.Resumption.ECHPayloadLengths) {
			out.ResumedECHPayloadLengths = slices.Clone(t.Resumption.ECHPayloadLengths)
		}
	}
	var errs []error
	for _, c := range conns {
		if err := checkQUICHello(t, out, c); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.at, err))
		}
	}
	return out, errors.Join(errs...)
}

// quicConns reads every QUIC connection's ClientHello from the h3
// captures, in file, sample and connection order.
func quicConns(dir, name string) ([]quicConn, error) {
	paths, err := filepath.Glob(filepath.Join(dir, name+".h3-*.pagetrace.json.gz"))
	if err != nil {
		return nil, err
	}
	var out []quicConn
	for _, path := range paths {
		pages, err := capture.LoadPages(path)
		if err != nil {
			return nil, err
		}
		for si, page := range pages {
			for ci, c := range page.QUIC {
				at := fmt.Sprintf("%s: sample %d, QUIC connection %d", path, si+1, ci+1)
				if c.Hello == nil {
					return nil, fmt.Errorf("%s: no ClientHello: %s", at, c.HelloError)
				}
				out = append(out, quicConn{
					at:      at,
					hello:   c.Hello,
					resumed: extension(c.Hello, profile.ExtPreSharedKey) != nil,
				})
			}
		}
	}
	return out, nil
}

// replacements fills out.Replace with the bodies the full hellos give
// extensions t also sends: those of the first, or for GREASE ECH every
// cipher suite and payload length any shows, kept as t's when they are
// among t's.
func replacements(out *profile.QUICHello, t *profile.TLS, full []quicConn) error {
	for _, te := range t.Extensions {
		qe := extension(full[0].hello, te.Type)
		if qe == nil || te.Type == profile.GREASE {
			continue
		}
		if te.ECH == nil {
			if render(te) != render(*qe) {
				out.Replace = append(out.Replace, *qe)
			}
			continue
		}
		ech := *qe.ECH
		ech.CipherSuites, ech.PayloadLengths = nil, nil
		for _, c := range full {
			e := extension(c.hello, te.Type)
			if e == nil || e.ECH == nil {
				return fmt.Errorf("%s: no GREASE ECH", c.at)
			}
			ech.CipherSuites = append(ech.CipherSuites, e.ECH.CipherSuites...)
			ech.PayloadLengths = append(ech.PayloadLengths, e.ECH.PayloadLengths...)
		}
		slices.SortFunc(ech.CipherSuites, profile.HPKESuite.Compare)
		ech.CipherSuites = slices.Compact(ech.CipherSuites)
		slices.Sort(ech.PayloadLengths)
		ech.PayloadLengths = slices.Compact(ech.PayloadLengths)
		if ech.EncLength == te.ECH.EncLength && within(ech.CipherSuites, te.ECH.CipherSuites) && within(ech.PayloadLengths, te.ECH.PayloadLengths) {
			continue
		}
		replaced := *qe
		replaced.ECH = &ech
		out.Replace = append(out.Replace, replaced)
	}
	return nil
}

// render is an extension as JSON, for comparing bodies.
func render(e profile.Extension) string {
	p := profile.Profile{
		TLS: profile.TLS{
			Extensions: []profile.Extension{
				e,
			},
		},
	}
	data, err := profile.Encode(&p)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func within[T comparable](s, of []T) bool {
	for _, v := range s {
		if !slices.Contains(of, v) {
			return false
		}
	}
	return true
}

// order sets out's extension order: fixed if every full hello's is the
// same, with quic_transport_parameters placed before the extension that
// follows it; otherwise shuffled, with Last the extensions every hello,
// full or resumed (pre_shared_key aside), ends with.
func order(out *profile.QUICHello, full, resumed []quicConn) {
	first := typesOf(full[0].hello, false)
	fixed := true
	for _, c := range full[1:] {
		if !slices.Equal(typesOf(c.hello, false), first) {
			fixed = false
		}
	}
	if fixed {
		out.ExtensionOrder = profile.ExtensionOrderFixed
		if i := slices.Index(first, profile.ExtQUICTransportParameters); i >= 0 && i+1 < len(first) {
			out.TransportParametersBefore = &first[i+1]
		}
		return
	}
	out.ExtensionOrder = profile.ExtensionOrderShuffled
	suffix := first
	for _, c := range slices.Concat(full, resumed) {
		got := typesOf(c.hello, true)
		n := 0
		for n < len(suffix) && n < len(got) && suffix[len(suffix)-1-n] == got[len(got)-1-n] {
			n++
		}
		suffix = suffix[len(suffix)-n:]
	}
	out.Last = slices.Clone(suffix)
}

// typesOf lists h's extension types in order, without pre_shared_key if
// noPSK.
func typesOf(h *profile.TLS, noPSK bool) []profile.Code {
	var out []profile.Code
	for _, e := range h.Extensions {
		if noPSK && e.Type == profile.ExtPreSharedKey {
			continue
		}
		out = append(out, e.Type)
	}
	return out
}

// expectedQUICHello is the QUIC hello q describes, from t, in t's order
// with quic_transport_parameters (body tp) added, before
// q.TransportParametersBefore or else last, and q.Last moved last; resumed,
// without t.Resumption.Drop, with early_data before the moved ones and
// pre_shared_key last.
func expectedQUICHello(t *profile.TLS, q *profile.QUICHello, tp []byte, resumed bool) *profile.TLS {
	out := profile.TLS{
		LegacyVersion:      t.LegacyVersion,
		SessionIDLength:    q.SessionIDLength,
		CipherSuites:       q.CipherSuites,
		CompressionMethods: t.CompressionMethods,
		ExtensionOrder:     q.ExtensionOrder,
	}
	params := profile.Extension{
		Type: profile.ExtQUICTransportParameters,
		Data: tp,
	}
	var last []profile.Extension
	add := func(e profile.Extension) {
		if slices.Contains(q.Last, e.Type) {
			last = append(last, e)
			return
		}
		out.Extensions = append(out.Extensions, e)
	}
	for _, e := range t.Extensions {
		if slices.Contains(q.Drop, e.Type) || resumed && slices.Contains(t.Resumption.Drop, e.Type) {
			continue
		}
		if b := q.TransportParametersBefore; b != nil && e.Type == *b {
			add(params)
		}
		for _, r := range q.Replace {
			if r.Type == e.Type {
				e = r
			}
		}
		if e.ECH != nil && resumed {
			ech := *e.ECH
			ech.PayloadLengths = q.ResumedECHPayloadLengths
			e.ECH = &ech
		}
		add(e)
	}
	if q.TransportParametersBefore == nil {
		add(params)
	}
	if resumed {
		out.Extensions = append(out.Extensions, profile.Extension{
			Type: profile.ExtEarlyData,
		})
	}
	slices.SortStableFunc(last, func(a, b profile.Extension) int {
		return slices.Index(q.Last, a.Type) - slices.Index(q.Last, b.Type)
	})
	out.Extensions = append(out.Extensions, last...)
	if resumed {
		out.Extensions = append(out.Extensions, profile.Extension{
			Type: profile.ExtPreSharedKey,
		})
	}
	return &out
}

// checkQUICHello compares one connection's hello with the one t and q
// describe: contents as diff.Compare does, its order exactly when fixed,
// and with q.Last (then pre_shared_key) at its end when shuffled.
func checkQUICHello(t *profile.TLS, q *profile.QUICHello, c quicConn) error {
	if c.resumed && t.Resumption == nil {
		return errors.New("resumed, but the profile does not resume sessions")
	}
	var tp []byte
	if e := extension(c.hello, profile.ExtQUICTransportParameters); e != nil {
		tp = e.Data
	}
	want := expectedQUICHello(t, q, tp, c.resumed)
	got := *c.hello
	got.ExtensionOrder = want.ExtensionOrder
	got.RecordVersion = 0
	var lines []string
	for _, d := range diff.Compare(&profile.Profile{TLS: *want}, &profile.Profile{TLS: got}) {
		lines = append(lines, d.String())
	}
	if q.ExtensionOrder == profile.ExtensionOrderShuffled {
		tail := typesOf(&got, true)
		if len(tail) < len(q.Last) || !slices.Equal(tail[len(tail)-len(q.Last):], q.Last) {
			lines = append(lines, fmt.Sprintf("does not end with %v: %v", q.Last, tail))
		}
		if c.resumed && got.Extensions[len(got.Extensions)-1].Type != profile.ExtPreSharedKey {
			lines = append(lines, "pre_shared_key is not last")
		}
	}
	if len(lines) > 0 {
		return errors.New("its hello is not the profile's QUIC hello:\n" + strings.Join(lines, "\n"))
	}
	return nil
}
