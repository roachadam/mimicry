//go:build !windows && !linux

package dnsstub

import (
	"errors"
	"net"
	"os"
	"strings"
)

// resolvConf lists the system's resolvers (on macOS, the primary network
// service's, written by configd).
const resolvConf = "/etc/resolv.conf"

// SystemResolver returns the first nameserver of /etc/resolv.conf as
// "host:53": the resolver this machine uses. Only macOS's capture host
// forwards to it (cmd/capture, host_other.go).
func SystemResolver() (string, error) {
	data, err := os.ReadFile(resolvConf)
	if err != nil {
		return "", err
	}

	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			return net.JoinHostPort(fields[1], "53"), nil
		}
	}

	return "", errors.New(resolvConf + " names no nameserver")
}
