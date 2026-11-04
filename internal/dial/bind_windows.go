//go:build windows

package dial

import (
	"net/netip"
	"syscall"
)

// bindTo binds c, a socket not yet connected, to local at an ephemeral
// port.
func bindTo(c syscall.RawConn, local netip.Addr) error {
	var sa syscall.Sockaddr
	if local.Is4() {
		sa = &syscall.SockaddrInet4{
			Addr: local.As4(),
		}
	} else {
		sa = &syscall.SockaddrInet6{
			Addr: local.As16(),
		}
	}

	var err error
	if cerr := c.Control(func(fd uintptr) {
		err = syscall.Bind(syscall.Handle(fd), sa)
	}); cerr != nil {
		return cerr
	}

	return err
}
