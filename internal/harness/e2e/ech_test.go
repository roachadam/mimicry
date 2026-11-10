package e2e

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/diff"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// mimicry walks the ech page as the browser did, its hosts publishing ECH
// configs in DNS HTTPS records, and its connections are the browser's, in
// order: the same host, ECH config, HPKE suite, acceptance and retry
// configs (a rejection followed by the retry, or by the GREASE extension);
// the same outer hello and inner hello (a shuffled order compared as a
// set), the same extensions copied through ech_outer_extensions, the same
// session offers; and none to a host whose list does not parse where the
// browser made none. mimicry looks up the HTTPS record of every host the
// browser did, before its first connection there. A browser that ignores
// ECH configs gets its usual hello. Profiles without tls.realEch are not
// measured, and skipped.
func TestECHMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			if p.TLS.RealECH == nil {
				t.Skip("tls.realEch not measured: GREASE ECH everywhere, no HTTPS records looked up")
			}

			pages, err := loadPages(t, p, server.ECH.Name, ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}

			got := runECHPage(t, p)
			mine := echConnections(got)

			compared := 0

			for si, page := range pages {
				want := echConnections(page)
				if len(want) != len(mine) {
					if racedRejection(want) {
						t.Logf("sample %d: %d connections, mimicry %d: the sample has the rejected offer twice (hellogen's echGaps)", si+1, len(want), len(mine))

						continue
					}

					t.Errorf("sample %d: %d connections to the page's hosts, mimicry %d:\n browser %s\n mimicry %s", si+1, len(want), len(mine), describeECH(want), describeECH(mine))

					continue
				}

				compared++

				for i := range want {
					for _, d := range compareECHConnection(p, &want[i], &mine[i]) {
						t.Errorf("sample %d, connection %d (%s): %s", si+1, i+1, want[i].Host, d)
					}
				}

				// A browser that ignores ECH configs still looks the
				// records up (Safari); mimicry has no use for them.
				if !p.TLS.RealECH.Ignore {
					checkECHLookups(t, si+1, page, got)
				}
			}

			if compared == 0 {
				t.Error("no sample to compare with")
			}
		})
	}
}

// runECHPage walks the ech page through mimicry as profile p: its
// document, server.ECHSteps one after another, then /done.
func runECHPage(t *testing.T, p *profile.Spec) *capture.Page {
	t.Helper()

	s := startPage(t, p, server.ECH)
	hosts, _, _ := server.ECH.MultiHost()

	s.send(http.MethodGet, s.url(hosts[0], "/"), mimicry.Request{}, nil, "")

	for _, label := range server.ECHSteps {
		host := echStepHost(label)

		err := s.do(http.MethodGet, s.url(host, "/k/"+label), mimicry.Request{}, nil, "")
		if err != nil && (!strings.HasSuffix(label, ".bad") || p.TLS.RealECH.Malformed != profile.MalformedFail) {
			t.Fatalf("%s: %v", label, err)
		}
	}

	return s.finish(s.url(hosts[0], "/done"))
}

// echStepHost is the host of an ECHSteps label, named by its suffix.
func echStepHost(label string) string {
	_, suffix, _ := strings.Cut(label, ".")

	return map[string]string{
		"ech":     "www.ech.test",
		"retry":   "www.ech-retry.test",
		"noretry": "www.ech-noretry.test",
		"bad":     "www.ech-bad.test",
		"suite":   "www.ech-suite.test",
	}[suffix]
}

// echConnections are a page's TLS connections to its hosts and public
// names, but those that carried no request without being a rejected ECH
// offer: a browser's speculative connections (Chrome on Linux, Firefox
// over DoH), which mimicry does not open.
func echConnections(page *capture.Page) []capture.Connection {
	var out []capture.Connection

	for _, c := range page.Connections {
		rejected := c.ECH != nil && !c.ECH.Accepted
		if server.IsECHHost(c.Host) && c.Hello != nil && (c.Requests > 0 || rejected) {
			out = append(out, c)
		}
	}

	return out
}

// racedRejection reports two rejected offers in a row: Firefox's racing
// connections, which mimicry does not reproduce.
func racedRejection(conns []capture.Connection) bool {
	for i := 1; i < len(conns); i++ {
		a, b := conns[i-1].ECH, conns[i].ECH
		if a != nil && b != nil && !a.Accepted && !b.Accepted {
			return true
		}
	}

	return false
}

// describeECH lists connections by host and ECH config, for messages.
func describeECH(conns []capture.Connection) string {
	parts := make([]string, len(conns))
	for i, c := range conns {
		parts[i] = c.Host
		if c.ECH != nil {
			parts[i] += "/" + profile.Code(c.ECH.ConfigID).String()
		}
	}

	return strings.Join(parts, " ")
}

// compareECHConnection lists how mimicry's connection got differs from
// the browser's want.
func compareECHConnection(p *profile.Spec, want, got *capture.Connection) []string {
	var out []string

	if want.Host != got.Host {
		out = append(out, "host "+got.Host+", the browser's "+want.Host)
	}

	if want.Resumed != got.Resumed || want.Accepted != got.Accepted {
		out = append(out, "a resumption offer or its acceptance differs from the browser's")
	}

	switch {
	case (want.ECH == nil) != (got.ECH == nil):
		return append(out, "real ECH present differs from the browser's")
	case want.ECH == nil:
		return append(out, diffHellos(p.TLS.ExtensionOrder, blankGREASE(want.Hello), blankGREASE(got.Hello))...)
	}

	w, g := want.ECH, got.ECH
	if w.ConfigID != g.ConfigID || w.KDF != g.KDF || w.AEAD != g.AEAD || w.Accepted != g.Accepted || w.Retry != g.Retry {
		out = append(out, "ECH config, suite, acceptance or retry differs from the browser's")
	}

	out = append(out, diffHellos(p.TLS.ExtensionOrder, want.Hello, got.Hello)...)

	if w.Inner == nil || g.Inner == nil {
		if (w.Inner == nil) != (g.Inner == nil) {
			out = append(out, "inner hello kept differs from the browser's")
		}

		return out
	}

	order := p.TLS.RealECH.Inner.ExtensionOrder
	for _, d := range diffHellos(order, blankCopied(w.Inner), blankCopied(g.Inner)) {
		out = append(out, "inner: "+d)
	}

	sameCopied := slices.Equal(w.OuterExtensions, g.OuterExtensions)
	if p.TLS.ExtensionOrder == profile.ExtensionOrderShuffled {
		sameCopied = sameCodes(w.OuterExtensions, g.OuterExtensions)
	}

	if !sameCopied {
		out = append(out, "ech_outer_extensions differ from the browser's")
	}

	if w.Padding != g.Padding {
		out = append(out, "inner padding differs from the browser's")
	}

	return out
}

// blankCopied is an inner hello without ech_outer_extensions' body, the
// list compareECHConnection checks as ECH.OuterExtensions.
func blankCopied(h *profile.TLS) *profile.TLS {
	c := *h

	c.Extensions = slices.Clone(h.Extensions)

	for i := range c.Extensions {
		if c.Extensions[i].Type == profile.ExtECHOuterExtensions {
			c.Extensions[i].Data = nil
		}
	}

	return &c
}

// diffHellos diffs two hellos, their extension order compared as order
// says.
func diffHellos(order string, want, got *profile.TLS) []string {
	w, g := profile.Spec{
		TLS: *want,
	}, profile.Spec{
		TLS: *got,
	}

	w.TLS.ExtensionOrder, g.TLS.ExtensionOrder = order, order

	diffs := diff.Compare(&w, &g)
	out := make([]string, len(diffs))

	for i, d := range diffs {
		out[i] = d.String()
	}

	return out
}

// blankGREASE is h without its GREASE ECH's per-connection choices, which
// TestProfilesMatchCaptures checks against the profile's sets.
func blankGREASE(h *profile.TLS) *profile.TLS {
	c := *h

	c.Extensions = slices.Clone(h.Extensions)

	for i := range c.Extensions {
		if c.Extensions[i].ECH != nil {
			c.Extensions[i].ECH = nil
		}
	}

	return &c
}

// sameCodes reports a and b holding the same codes as many times each.
func sameCodes(a, b []profile.Code) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b)
}

// checkECHLookups checks that mimicry looked up the HTTPS record of every
// host the browser did, before its first connection to that host.
func checkECHLookups(t *testing.T, sample int, browser, got *capture.Page) {
	t.Helper()

	for _, q := range browser.DNS {
		if q.Type != "HTTPS" {
			continue
		}

		i := slices.IndexFunc(got.DNS, func(m capture.DNSQuery) bool {
			return m.Type == "HTTPS" && m.Name == q.Name
		})
		if i < 0 {
			t.Errorf("sample %d: the browser looked up %s's HTTPS record; mimicry did not", sample, q.Name)

			continue
		}

		for _, c := range got.Connections {
			if c.Host == q.Name && c.Millis < got.DNS[i].Millis {
				t.Errorf("sample %d: mimicry connected to %s at %d ms, before looking up its HTTPS record (%d ms)", sample, q.Name, c.Millis, got.DNS[i].Millis)

				break
			}
		}
	}
}

// mimicry stays on HTTP/2 with ECH for a host whose ECH config it uses,
// whatever Alt-Svc its responses advertise, as the browser did on the
// ech-h3 page: no QUIC packet, every connection's ECH accepted. Profiles
// whose browser was not seen to ignore Alt-Svc there are skipped.
func TestECHH3MatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			if p.TLS.RealECH == nil || !p.TLS.RealECH.AltSvcIgnored {
				t.Skip("tls.realEch.altSvcIgnored not measured")
			}

			s := startPage(t, p, server.ECHH3)
			hosts, _, _ := server.ECHH3.MultiHost()

			s.send(http.MethodGet, s.url(hosts[0], "/"), mimicry.Request{}, nil, "")

			for _, label := range server.ECHH3Steps {
				s.send(http.MethodGet, s.url(echStepHost(label), "/k/"+label), mimicry.Request{}, nil, "")
			}

			got := s.finish(s.url(hosts[0], "/done"))
			if len(got.QUIC) > 0 {
				t.Errorf("%d QUIC connections; the browser made none", len(got.QUIC))
			}

			for i, c := range got.Connections {
				if c.ECH == nil || !c.ECH.Accepted {
					t.Errorf("connection %d (%s): no accepted ECH", i+1, c.Host)
				}
			}
		})
	}
}
