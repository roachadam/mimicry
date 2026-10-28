//go:build !quicbasicnet && linux

package quic

// mimicry: received datagrams are read in batches with recvmmsg (UPSTREAM.md
// change 13), up to recvBatch per system call; upstream reads one per
// recvmsg. Each still goes to f, and so to the conn's loop, one at a time.
// The batch is read with raw recvmmsg rather than x/net/ipv4's ReadBatch,
// which allocates an address per message.

import (
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"unsafe"
)

// recvBatch is how many datagrams one recvmmsg may return: quic-go's batch.
const recvBatch = 8

// mmsghdr is struct mmsghdr: a msghdr and the length received.
type mmsghdr struct {
	hdr syscall.Msghdr
	len uint32
}

// controlSize is the control buffer per datagram, as netUDPConn.Read sizes
// its own.
var controlSize = syscall.CmsgSpace(12) + syscall.CmsgSpace(20) + syscall.CmsgSpace(4) + syscall.CmsgSpace(4)

// readBatch reads datagrams until an error, as Read does, recvBatch at a
// time. ok is false when the socket gives no raw access; Read reads one at
// a time then.
func (c *netUDPConn) readBatch(f func(*datagram)) (ok bool, err error) {
	rc, err := c.c.SyscallConn()
	if err != nil {
		return false, nil
	}

	var (
		hdrs    [recvBatch]mmsghdr
		iovs    [recvBatch]syscall.Iovec
		names   [recvBatch]syscall.RawSockaddrInet6
		dgrams  [recvBatch]*datagram
		control = make([]byte, recvBatch*controlSize)
	)

	for {
		for i := range hdrs {
			if dgrams[i] == nil {
				dgrams[i] = newDatagram()
			}

			d := dgrams[i]
			iovs[i].Base = &d.b[0]
			iovs[i].SetLen(len(d.b))

			h := &hdrs[i].hdr
			h.Name = (*byte)(unsafe.Pointer(&names[i]))
			h.Namelen = uint32(unsafe.Sizeof(names[i]))
			h.Iov = &iovs[i]
			h.Iovlen = 1
			h.Control = &control[i*controlSize]
			h.SetControllen(controlSize)
			h.Flags = 0
			hdrs[i].len = 0
		}

		var (
			n     int
			errno syscall.Errno
		)

		err := rc.Read(func(fd uintptr) bool {
			r, _, e := syscall.Syscall6(syscall.SYS_RECVMMSG, fd, uintptr(unsafe.Pointer(&hdrs[0])), recvBatch, 0, 0, 0)
			if e == syscall.EAGAIN {
				return false
			}

			n, errno = int(r), e

			return true
		})
		if err != nil {
			return true, err
		}

		if errno == syscall.EINTR {
			continue
		}

		if errno != 0 {
			return true, errno
		}

		for i := range n {
			size := int(hdrs[i].len)
			if size == 0 {
				continue // as Read: an empty datagram is dropped
			}

			d := dgrams[i]
			dgrams[i] = nil

			d.b = d.b[:size]
			d.path = pathAddrs{
				local: c.localAddr,
				peer:  unmapAddrPort(sockaddrAddrPort(&names[i])),
			}
			parseControl(d, control[i*controlSize:][:hdrs[i].hdr.Controllen])
			f(d)
		}
	}
}

// sockaddrAddrPort is the address recvmmsg wrote: a sockaddr_in or a
// sockaddr_in6, its scope ID as the zone (the interface's name, as the net
// package names it).
func sockaddrAddrPort(sa *syscall.RawSockaddrInet6) netip.AddrPort {
	port := func(p uint16) uint16 {
		b := (*[2]byte)(unsafe.Pointer(&p))

		return binary.BigEndian.Uint16(b[:])
	}

	switch sa.Family {
	case syscall.AF_INET:
		sa4 := (*syscall.RawSockaddrInet4)(unsafe.Pointer(sa))

		return netip.AddrPortFrom(netip.AddrFrom4(sa4.Addr), port(sa4.Port))

	case syscall.AF_INET6:
		a := netip.AddrFrom16(sa.Addr)
		if sa.Scope_id != 0 {
			zone := strconv.FormatUint(uint64(sa.Scope_id), 10)
			if ifi, err := net.InterfaceByIndex(int(sa.Scope_id)); err == nil {
				zone = ifi.Name
			}

			a = a.WithZone(zone)
		}

		return netip.AddrPortFrom(a, port(sa.Port))
	}

	return netip.AddrPort{}
}
