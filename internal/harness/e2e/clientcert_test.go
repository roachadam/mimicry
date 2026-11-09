package e2e

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/internal/harness/internal/testprofiles"
)

// mimicry answers a server asking for a client certificate as the browser
// does, on the resume page of each client-certificate scenario (TLS 1.3
// and TLS 1.2-only servers, an EC P-256 and an RSA-2048 identity), given
// the run's identity through Config.GetClientCertificate: every connection
// that carried requests is one the browser made (TLS version, resumed or
// not, its flight, the certificates it sent, the leaf alone, and its
// CertificateVerify scheme); per host, as many such connections, offering
// a session, resumed and sending the certificate, as one browser sample;
// and in every sample as many connections per host dropped at the
// CertificateRequest, with nothing sent and the same end on the server's
// side, before the first that answered (tls.clientAuth.restart). Chrome's
// preconnect (clientCertPreconnects) and Firefox's speculative connections
// (clientCertSpeculative) are left out of the browser's samples.
func TestClientCertMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		for _, sc := range clientCertScenarios() {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}

				path, file, _ := capturePath(t, p, sc.Name, ".clientauth.json")
				skipIfMissing(t, path, file)

				want, err := capture.LoadClientAuth(path)
				if err != nil {
					t.Fatal(err)
				}

				got := runResumeCapture(t, p, sc).ClientAuth
				checkClientAuth(t, capture.Source(p, sc.Name), want, got)
			})
		}
	}
}

// clientCertScenarios are the scenarios whose server asks for a client
// certificate.
func clientCertScenarios() []*server.Scenario {
	return slices.DeleteFunc(slices.Clone(server.Scenarios), func(sc *server.Scenario) bool {
		return sc.ClientCert() == ""
	})
}

// clientCertPreconnects are the hosts, by the profile that took the
// capture, where every sample dropped one more connection at the
// CertificateRequest than mimicry: Chrome's preconnect to the page's host,
// which mimicry does not make (decision 0023, amendment 2026-10-30), met
// the CertificateRequest before Chrome had chosen its certificate. Edge
// makes none.
var clientCertPreconnects = map[string][]string{
	"chrome-156-macos": {
		"www.kinds-a.test",
	},
	"chrome-156-windows": {
		"www.kinds-a.test",
	},
	"chrome-157-linux": {
		"www.kinds-a.test",
	},
}

// clientCertSpeculative are the browsers, by the profile that took the
// capture, that open connections no request goes on, some dropped at the
// CertificateRequest and some answering it with an empty Certificate, in
// no pattern across samples: Firefox's speculative connections, which
// mimicry does not make (profiles/EVIDENCE.md, Not covered). They are left
// out of the browser's samples.
var clientCertSpeculative = map[string]bool{
	"firefox-159-macos":   true,
	"firefox-159-windows": true,
	"firefox-159-linux":   true,
}

// checkClientAuth compares got, mimicry's handshakes on a
// client-certificate scenario's page, with want, the browser's samples
// captured by source.
func checkClientAuth(t *testing.T, source string, want [][]capture.ClientAuth, got []capture.ClientAuth) {
	t.Helper()

	browserConns := map[string]bool{}
	browserEnds := map[string]bool{}
	samples := make([]string, len(want))

	for i, sample := range want {
		for _, c := range sample {
			switch {
			case len(c.Requests) > 0:
				browserConns[clientAuthConn(c)] = true
			case len(c.Flight) == 0:
				browserEnds[c.Error] = true
			}
		}

		samples[i] = clientAuthCounts(sample)
	}

	for i, c := range got {
		switch {
		case len(c.Requests) > 0:
			if d := clientAuthConn(c); !browserConns[d] {
				t.Errorf("connection %d (%s) is %s; the browser's are %q", i+1, c.Host, d, slices.Sorted(maps.Keys(browserConns)))
			}

		case len(c.Flight) == 0:
			if !browserEnds[c.Error] {
				t.Errorf("connection %d (%s), dropped at the CertificateRequest, ended as %q; the browser's %q", i+1, c.Host, c.Error, slices.Sorted(maps.Keys(browserEnds)))
			}

		default:
			t.Errorf("connection %d (%s) answered the CertificateRequest (%s) and carried no request", i+1, c.Host, strings.Join(c.Flight, ", "))
		}
	}

	if g := clientAuthCounts(got); !slices.Contains(samples, g) {
		t.Errorf("connections per host (carrying requests/offering a session/resumed/sending the certificate):\n got %s\nwant one of %q", g, samples)
	}

	gotDrops := clientAuthDrops(got)

	if clientCertSpeculative[source] {
		if len(gotDrops) > 0 {
			t.Errorf("connections dropped at the CertificateRequest %v; the browser drops none it needs (its others are speculative)", gotDrops)
		}

		if !slices.ContainsFunc(want, func(sample []capture.ClientAuth) bool {
			return slices.ContainsFunc(sample, func(c capture.ClientAuth) bool {
				return len(c.Requests) == 0
			})
		}) {
			t.Error("known gap no longer appears: the browser opened no speculative connection")
		}

		return
	}

	for i, sample := range want {
		drops := clientAuthDrops(sample)

		for _, host := range clientCertPreconnects[source] {
			if drops[host] != gotDrops[host]+1 {
				t.Errorf("sample %d: %d connections to %s dropped at the CertificateRequest, mimicry's %d: the known gap (Chrome's preconnect, one more) no longer appears", i+1, drops[host], host, gotDrops[host])
			}

			drops[host]--
		}

		maps.DeleteFunc(drops, func(_ string, n int) bool {
			return n == 0
		})

		if !maps.Equal(drops, gotDrops) {
			t.Errorf("connections dropped at the CertificateRequest per host: got %v, sample %d's %v", gotDrops, i+1, drops)
		}

		if slices.ContainsFunc(sample, func(c capture.ClientAuth) bool {
			return len(c.Requests) == 0 && len(c.Flight) > 0
		}) {
			t.Errorf("sample %d answered a CertificateRequest on a connection that carried no request, which only clientCertSpeculative's browsers do", i+1)
		}
	}
}

// clientAuthConn describes a connection that carried requests as compared:
// its TLS version, whether it resumed, its flight, the certificates sent
// and the CertificateVerify scheme.
func clientAuthConn(c capture.ClientAuth) string {
	certs := make([]string, len(c.Certificates))
	for i, cert := range c.Certificates {
		certs[i] = fmt.Sprintf("%s from %s (%s, CA %v, %d bytes of extensions)", cert.Subject, cert.Issuer, cert.Key, cert.CA, cert.Extensions)
	}

	return fmt.Sprintf("TLS %s, resumed %v, flight [%s], certificates [%s], CertificateVerify %s", c.Version, c.Resumed, strings.Join(c.Flight, " "), strings.Join(certs, "; "), c.CertificateVerify)
}

// clientAuthCounts summarizes the connections of conns that carried
// requests, per host in the order hosts first appear: how many, how many
// offered a session to resume, resumed and sent the certificate.
func clientAuthCounts(conns []capture.ClientAuth) string {
	type counts struct {
		used, offered, resumed, certified int
	}

	var hosts []string

	per := map[string]*counts{}

	for _, c := range slices.SortedStableFunc(slices.Values(conns), func(a, b capture.ClientAuth) int {
		return cmp.Compare(a.Millis, b.Millis)
	}) {
		if len(c.Requests) == 0 {
			continue
		}

		n := per[c.Host]
		if n == nil {
			n = &counts{}
			per[c.Host] = n
			hosts = append(hosts, c.Host)
		}

		n.used++

		if c.PSKIdentities > 0 || c.Ticket {
			n.offered++
		}

		if c.Resumed {
			n.resumed++
		}

		if len(c.Certificates) > 0 {
			n.certified++
		}
	}

	parts := make([]string, len(hosts))
	for i, h := range hosts {
		n := per[h]

		parts[i] = fmt.Sprintf("%s %d/%d/%d/%d", h, n.used, n.offered, n.resumed, n.certified)
	}

	return strings.Join(parts, ", ")
}

// clientAuthDrops counts, per host, the connections of conns that sent
// nothing after the server's CertificateRequest and carried no request.
func clientAuthDrops(conns []capture.ClientAuth) map[string]int {
	out := map[string]int{}

	for _, c := range conns {
		if c.Request != nil && len(c.Flight) == 0 && len(c.Requests) == 0 {
			out[c.Host]++
		}
	}

	return out
}

// clientCertMissing are the profiles with no client-certificate capture,
// and why: missingCaptures holds an entry for each of their scenarios.
// Without one a profile has no tls.clientAuth, and a client certificate is
// ErrNotMeasured (the root package's TestClientCertificate).
var clientCertMissing = map[string]string{
	"chrome-155-macos":          "the client-certificate scenarios came after Chrome 155, no longer installable as branded Chrome",
	"firefox-158-macos":         "the client-certificate scenarios were captured with Firefox 159 only (2026-11-08)",
	"firefox-158-windows":       "the client-certificate scenarios were captured with Firefox 159 only (2026-11-08)",
	"safari-26-macos":           "Safari takes no client-certificate scenario (server.Scenario.UnsupportedBy)",
	"safari-27-macos":           "Safari takes no client-certificate scenario (server.Scenario.UnsupportedBy)",
	testprofiles.InheritingName: "chrome-155-macos, whose captures it inherits, has none",
}

// withClientCertMissing adds to missing an entry for each
// client-certificate scenario of each profile in clientCertMissing.
func withClientCertMissing(missing map[string]string) map[string]string {
	for name, why := range clientCertMissing {
		for _, sc := range clientCertScenarios() {
			missing[name+"."+sc.Name+".clientauth.json"] = why
		}
	}

	return missing
}
