package main

import (
	"net"

	"golang.org/x/sys/unix"
)

// tunnelBytes returns what a tunnel relayed, from its server-side socket:
// up, the bytes the server acknowledged (sent to it); down, the bytes
// received from it. Zero when the socket cannot be read (closed).
func tunnelBytes(server net.Conn) (up, down int64) {
	tc, ok := server.(*net.TCPConn)
	if !ok {
		return 0, 0
	}

	raw, err := tc.SyscallConn()
	if err != nil {
		return 0, 0
	}

	var (
		info    *unix.TCPInfo
		sockErr error
	)

	err = raw.Control(func(fd uintptr) {
		info, sockErr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
	})
	if err != nil || sockErr != nil {
		return 0, 0
	}

	return int64(info.Bytes_acked), int64(info.Bytes_received)
}
