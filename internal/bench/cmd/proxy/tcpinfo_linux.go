package main

import (
	"encoding/binary"
	"net"
	"syscall"
	"unsafe"
)

// struct tcp_info (linux/tcp.h): tcpi_bytes_acked and tcpi_bytes_received
// are u64s at these offsets (Linux 4.1 and later).
const (
	tcpInfoBytesAcked    = 120
	tcpInfoBytesReceived = 128
	tcpInfoSize          = 256
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
		buf   [tcpInfoSize]byte
		size  = uint32(len(buf))
		errno syscall.Errno
	)

	err = raw.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, syscall.IPPROTO_TCP, syscall.TCP_INFO,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	})
	if err != nil || errno != 0 || size < tcpInfoBytesReceived+8 {
		return 0, 0
	}

	up = int64(binary.NativeEndian.Uint64(buf[tcpInfoBytesAcked:]))
	down = int64(binary.NativeEndian.Uint64(buf[tcpInfoBytesReceived:]))

	return up, down
}
