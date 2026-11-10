package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"path/filepath"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// proxySOCKS4 derives proxy.socks4 from the SOCKS4 scenarios' captures
// (Scenario.Proxy server.ProxySOCKS4 and server.ProxySOCKS4A), every
// sample of each, and refuses captures it cannot be:
//   - Every request the proxy saw is a SOCKS4 CONNECT (VN 4, CD 1) with an
//     empty USERID, naming its target either by an IPv4 address alone (the
//     browser resolved the host) or as SOCKS4A (DSTIP 0.0.0.x, x not 0,
//     then the host name and NUL).
//   - proxy-socks4: every request names its target by address, or every
//     one as SOCKS4A: the browser, asked for SOCKS4, never resolves
//     (hostnameOnly, Safari through macOS's SOCKS proxy setting).
//   - proxy-socks4a: every request is SOCKS4A (hostname); the scenario is
//     never captured from a browser without SOCKS4A (Chromium's).
//   - Every SOCKS4A request carries the same DSTIP (hostnameDstip:
//     Firefox's 0.0.0.1, Safari's 0.0.0.16).
//   - Each scenario captured has a request in every sample, and
//     proxy-socks4a is captured only beside proxy-socks4.
//
// Nil without a capture: not measured.
func proxySOCKS4(dir string, p *profile.Spec) (*profile.SOCKS4, error) {
	var (
		out      *profile.SOCKS4
		errs     []error
		dstips   = map[netip.Addr]bool{}
		resolved = map[string]map[bool]bool{}
	)

	for _, sc := range server.Scenarios {
		if sc.Proxy != server.ProxySOCKS4 && sc.Proxy != server.ProxySOCKS4A {
			continue
		}

		source, _, err := capture.Inherited(p, sc.Name)
		if err != nil {
			return nil, err
		}

		path := filepath.Join(dir, source+"."+sc.Name+".proxytrace.json")

		traces, err := capture.LoadProxyRequests(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		if out == nil {
			out = &profile.SOCKS4{}
		}

		resolved[sc.Proxy] = map[bool]bool{}

		for si, sample := range traces {
			at := fmt.Sprintf("%s sample %d", filepath.Base(path), si+1)
			if len(sample) == 0 {
				errs = append(errs, fmt.Errorf("%s: nothing reached the proxy", at))
			}

			for ri, req := range sample {
				dstip, err := checkSOCKS4(req)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s request %d: %w", at, ri+1, err))
					continue
				}

				resolved[sc.Proxy][!dstip.IsValid()] = true

				if dstip.IsValid() {
					dstips[dstip] = true
				}
			}
		}
	}

	if out == nil {
		return nil, nil //nolint:nilnil // nil: no capture, not measured
	}

	plain, a := resolved[server.ProxySOCKS4], resolved[server.ProxySOCKS4A]

	switch {
	case plain == nil:
		errs = append(errs, errors.New("proxy-socks4a captured without proxy-socks4"))
	case len(plain) > 1:
		errs = append(errs, errors.New("proxy-socks4's requests disagree: some name the target by address, some as SOCKS4A"))
	case a != nil && a[true]:
		errs = append(errs, errors.New("proxy-socks4a sent a resolved address, not the host name"))
	case len(dstips) > 1:
		errs = append(errs, fmt.Errorf("SOCKS4A requests disagree on DSTIP: %v", dstips))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	out.HostnameOnly = plain[false]
	out.Hostname = out.HostnameOnly || a != nil

	for ip := range dstips {
		out.HostnameDSTIP = ip.String()
	}

	return out, nil
}

// checkSOCKS4 refuses req unless it is a SOCKS4 CONNECT with an empty
// USERID naming its target by an IPv4 address alone, or as SOCKS4A by its
// host name, then returning DSTIP (invalid for an address).
func checkSOCKS4(req capture.ProxyRequest) (netip.Addr, error) {
	if req.Kind != capture.ProxySOCKS4 {
		return netip.Addr{}, fmt.Errorf("a %s request, not SOCKS4", req.Kind)
	}

	b, err := hex.DecodeString(req.Request)
	if err != nil {
		return netip.Addr{}, err
	}

	// VN, CD, DSTPORT, DSTIP, then USERID's NUL.
	if len(b) < 9 || b[0] != 4 || b[1] != 1 {
		return netip.Addr{}, fmt.Errorf("not a SOCKS4 CONNECT: %x", b)
	}

	if b[8] != 0 {
		return netip.Addr{}, fmt.Errorf("a USERID: %x", b[8:])
	}

	host, _, err := net.SplitHostPort(req.Target)
	if err != nil {
		return netip.Addr{}, err
	}

	dstip, rest := netip.AddrFrom4([4]byte(b[4:8])), b[9:]
	socks4a := b[4] == 0 && b[5] == 0 && b[6] == 0 && b[7] != 0

	switch {
	case !socks4a && len(rest) != 0:
		return netip.Addr{}, fmt.Errorf("DSTIP %s and %x after USERID", dstip, rest)
	case !socks4a:
		return netip.Addr{}, nil
	case !bytes.Equal(rest, append([]byte(host), 0)):
		return netip.Addr{}, fmt.Errorf("%q after USERID, not the host name %s and NUL", rest, host)
	}

	return dstip, nil
}
