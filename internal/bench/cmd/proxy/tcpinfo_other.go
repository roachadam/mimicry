//go:build !linux

package main

import "net"

// tunnelBytes reads TCP_INFO, which only Linux has: the proxy runs in its
// Linux container. Elsewhere a tunnel's bytes are not counted.
func tunnelBytes(net.Conn) (up, down int64) {
	return 0, 0
}
