package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// vnRetryHello derives tls.quic.versionNegotiation: how hellos retried
// after Version Negotiation differ from the full QUIC hello. All retries
// must differ alike. Nil without such connections.
func vnRetryHello(dir string, p *profile.Profile, q *profile.QUICHello) (*profile.QUICHelloRetry, error) {
	t := &p.TLS
	retries, err := vnRetries(dir, p)

	if err != nil || len(retries) == 0 {
		return nil, err
	}

	var out *profile.QUICHelloRetry
	for _, c := range retries {
		want := expectedQUICHello(t, q, transportParameters(c.hello), false)
		got := &profile.QUICHelloRetry{}

		for _, e := range want.Extensions {
			if extension(c.hello, e.Type) == nil {
				got.Drop = append(got.Drop, e.Type)
			}
		}

		for _, e := range c.hello.Extensions {
			w := extension(want, e.Type)
			switch {
			case e.Type == profile.ExtPadding && w == nil:
				got.Padding = true
			case w == nil:
				return nil, fmt.Errorf("%s: the retried hello adds %s", c.at, e.Type)
			case e.Type != profile.ExtQUICTransportParameters && !e.Pinned() && render(*w) != render(e):
				got.Replace = append(got.Replace, e)
			}
		}

		// A shuffled hello's order says nothing: replacements go in
		// place, sorted.
		slices.SortFunc(got.Replace, func(a, b profile.Extension) int {
			return int(a.Type) - int(b.Type)
		})

		switch {
		case out == nil:
			out = got
		case renderRetry(got) != renderRetry(out):
			return nil, fmt.Errorf("%s: the retried hello differs as %s, an earlier one as %s", c.at, renderRetry(got), renderRetry(out))
		}
	}

	var errs []error
	for _, c := range retries {
		if err := checkRetryHello(t, q, out, c); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.at, err))
		}
	}

	return out, errors.Join(errs...)
}

// renderRetry is r as text, for comparing.
func renderRetry(r *profile.QUICHelloRetry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "drop %v, padding %v, replace", r.Drop, r.Padding)

	for _, e := range r.Replace {
		b.WriteString(" ")
		b.WriteString(render(e))
	}

	return b.String()
}

// transportParameters is h's quic_transport_parameters body.
func transportParameters(h *profile.TLS) []byte {
	if e := extension(h, profile.ExtQUICTransportParameters); e != nil {
		return e.Data
	}

	return nil
}

// checkRetryHello compares one retried hello with the one t, q and r
// describe: contents as diff.Compare does, and, shuffled, q.Last less
// the dropped then padding at its end.
func checkRetryHello(t *profile.TLS, q *profile.QUICHello, r *profile.QUICHelloRetry, c quicConn) error {
	want := expectedQUICHello(t, q, transportParameters(c.hello), false)
	exts := want.Extensions[:0:0]

	for _, e := range want.Extensions {
		if slices.Contains(r.Drop, e.Type) {
			continue
		}

		for _, rep := range r.Replace {
			if rep.Type == e.Type {
				e = rep
			}
		}

		exts = append(exts, e)
	}

	last := slices.DeleteFunc(slices.Clone(q.Last), func(c profile.Code) bool {
		return slices.Contains(r.Drop, c)
	})
	if r.Padding {
		exts = append(exts, profile.Extension{
			Type: profile.ExtPadding,
		})
		last = append(last, profile.ExtPadding)
	}

	want.Extensions = exts

	got := *c.hello

	got.ExtensionOrder = want.ExtensionOrder
	got.RecordVersion = 0

	var lines []string

	for _, d := range diff.Compare(&profile.Profile{TLS: *want}, &profile.Profile{TLS: got}) {
		lines = append(lines, d.String())
	}

	if q.ExtensionOrder == profile.ExtensionOrderShuffled {
		tail := typesOf(&got, true)
		if len(tail) < len(last) || !slices.Equal(tail[len(tail)-len(last):], last) {
			lines = append(lines, fmt.Sprintf("does not end with %v: %v", last, tail))
		}
	}

	// NSS and BoringSSL pad a hello of 256 to 511 bytes to 512.
	if r.Padding && c.length != 512 {
		lines = append(lines, fmt.Sprintf("padded to %d bytes, not 512", c.length))
	}

	if len(lines) > 0 {
		return errors.New("its hello is not the profile's retried QUIC hello:\n" + strings.Join(lines, "\n"))
	}

	return nil
}

// vnRetries are the h3-vn captures' connections that followed the one
// before's Version Negotiation in another version.
func vnRetries(dir string, p *profile.Profile) ([]quicConn, error) {
	files, err := server.H3EdgeCaptures(dir, p)
	if err != nil {
		return nil, err
	}

	var out []quicConn
	for _, f := range files {
		if f.Scenario != server.H3VN.Name {
			continue
		}

		path := f.Path

		pages, err := capture.LoadPages(path)
		if err != nil {
			return nil, err
		}

		for si, page := range pages {
			for ci, c := range page.QUIC {
				if ci == 0 || c.Version == "00000001" || !gotVN(page.QUIC[ci-1]) {
					continue
				}

				at := fmt.Sprintf("%s: sample %d, QUIC connection %d", path, si+1, ci+1)
				if c.Hello == nil {
					return nil, fmt.Errorf("%s: no ClientHello: %s", at, c.HelloError)
				}

				out = append(out, quicConn{
					at:     at,
					hello:  c.Hello,
					length: len(c.ClientHelloBytes()),
				})
			}
		}
	}

	return out, nil
}

// gotVN reports whether the server answered c with Version Negotiation.
func gotVN(c *quictrace.Conn) bool {
	for _, d := range c.Server {
		for _, p := range d.Packets {
			if p.Type == quictrace.PacketVN {
				return true
			}
		}
	}

	return false
}
