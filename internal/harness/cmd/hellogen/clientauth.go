package main

import (
	"cmp"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// clientAuth derives tls.clientAuth from the client-certificate captures
// (server.ClientCertEC and its siblings), every sample of each:
//   - tls13 and tls12: the CertificateVerify scheme of every connection
//     that signed one, per TLS version and the certificate's key; they
//     must agree.
//   - resume: a connection to a host after one that sent the certificate
//     offered a session to resume (a TLS 1.3 PSK, a TLS 1.2 ticket); every
//     sample must say the same.
//   - restart: no first connection to a host asked for a certificate
//     answered (each sent nothing and closed), and a later connection to
//     it carried the requests. One first connection that answered, with
//     its certificate or an empty one, makes it false: the browser can
//     answer at once, and its other dropped connections are speculative
//     ones that never carried a request.
//
// Nil without a capture: not measured.
func clientAuth(dir string, p *profile.Spec) (*profile.ClientAuth, error) {
	var (
		schemes  = map[bool]map[string]profile.Code{}
		from     = map[bool]map[string]string{}
		resumes  []bool
		answered bool
		dropped  int
		errs     []error
	)

	for _, sc := range server.Scenarios {
		if sc.ClientCert() == "" {
			continue
		}

		source, _, err := capture.Inherited(p, sc.Name)
		if err != nil {
			return nil, err
		}

		path := filepath.Join(dir, source+"."+sc.Name+".clientauth.json")

		samples, err := capture.LoadClientAuth(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		for si, sample := range samples {
			at := fmt.Sprintf("%s sample %d", filepath.Base(path), si+1)
			conns := slices.SortedStableFunc(slices.Values(sample), func(a, b capture.ClientAuth) int {
				return cmp.Compare(a.Millis, b.Millis)
			})

			for _, c := range conns {
				if c.CertificateVerify == 0 {
					continue
				}

				if len(c.Certificates) == 0 {
					errs = append(errs, fmt.Errorf("%s: connection %d signed a CertificateVerify for no certificate", at, c.Conn))
					continue
				}

				tls13, key := c.Version == tls.VersionTLS13, c.Certificates[0].Key
				if schemes[tls13] == nil {
					schemes[tls13], from[tls13] = map[string]profile.Code{}, map[string]string{}
				}

				switch got, ok := schemes[tls13][key]; {
				case !ok:
					schemes[tls13][key], from[tls13][key] = c.CertificateVerify, at
				case got != c.CertificateVerify:
					errs = append(errs, fmt.Errorf("%s signed with %s for a %s key in %s, %s with %s", at, c.CertificateVerify, key, c.Version, from[tls13][key], got))
				}
			}

			resumes = append(resumes, resumedAfterCertificate(conns))

			a, d, err := firstRequests(conns)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", at, err))
			}

			answered = answered || a

			dropped += d
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if len(resumes) == 0 {
		return nil, nil //nolint:nilnil // nil: no capture, not measured
	}

	if slices.Contains(resumes, !resumes[0]) {
		return nil, fmt.Errorf("the samples disagree on resuming a session after sending a certificate: %v", resumes)
	}

	if !answered && dropped == 0 {
		return nil, errors.New("no connection was asked for a certificate")
	}

	return &profile.ClientAuth{
		TLS13:   slices.Sorted(maps.Values(schemes[true])),
		TLS12:   slices.Sorted(maps.Values(schemes[false])),
		Resume:  resumes[0],
		Restart: !answered,
	}, nil
}

// resumedAfterCertificate reports whether a connection of conns, in the
// order their ClientHellos arrived, offered to resume a session to a host
// after one that sent that host a certificate.
func resumedAfterCertificate(conns []capture.ClientAuth) bool {
	sent := map[string]bool{}

	for _, c := range conns {
		if sent[c.Host] && (c.PSKIdentities > 0 || c.Ticket) {
			return true
		}

		if len(c.Certificates) > 0 {
			sent[c.Host] = true
		}
	}

	return false
}

// firstRequests reads the first connection to each host of conns (in
// arrival order) that was asked for a certificate: answered reports one
// that answered it, dropped counts those that sent nothing. A dropped one
// whose host no later connection carried a request to is an error: the
// browser gave up on the host.
func firstRequests(conns []capture.ClientAuth) (answered bool, dropped int, err error) {
	seen := map[string]bool{}

	for i, c := range conns {
		if c.Request == nil || seen[c.Host] {
			continue
		}

		seen[c.Host] = true

		if len(c.Flight) > 0 {
			answered = true
			continue
		}

		dropped++

		if !slices.ContainsFunc(conns[i+1:], func(l capture.ClientAuth) bool {
			return l.Host == c.Host && len(l.Requests) > 0
		}) {
			return answered, dropped, fmt.Errorf("the first connection to %s dropped at the CertificateRequest, and no later one carried a request", c.Host)
		}
	}

	return answered, dropped, nil
}
