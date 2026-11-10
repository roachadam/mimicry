//go:build !windows

package httpsrr

import (
	"net/netip"
	"os"
	"strings"
)

const (
	// resolvConf names the nameservers on Unix-like systems.
	resolvConf = "/etc/resolv.conf"
	// maxNameservers is how many resolvConf lines are used, as Go's
	// resolver and the C library's (MAXNS) use.
	maxNameservers = 3
)

// systemNameservers returns the addresses of resolvConf's nameserver
// lines, on port 53, or defaultNameservers without any.
func systemNameservers() []string {
	data, err := os.ReadFile(resolvConf)
	if err != nil {
		return defaultNameservers
	}

	var servers []string

	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}

		addr, err := netip.ParseAddr(fields[1])
		if err != nil {
			continue
		}

		servers = append(servers, netip.AddrPortFrom(addr, 53).String())
		if len(servers) == maxNameservers {
			break
		}
	}

	if len(servers) == 0 {
		return defaultNameservers
	}

	return servers
}
