//go:build !quicbasicnet && linux

package quic

// mimicry: received datagrams are read in batches with recvmmsg (UPSTREAM.md
// change 13), up to recvBatch per system call; upstream reads one per
// recvmsg. Each still goes to f, and so to the conn's loop, one at a time.
// The batch is read with raw recvmmsg rather than x/net/ipv4's ReadBatch,
// which allocates an address per message. A read takes as many buffers
// from the pool as the traffic has needed, not a whole batch each time the
// socket wakes (UPSTREAM.md change 18).

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
		return false, nil //nolint:nilerr // no raw access: Read reads one at a time
	}

	var (
		hdrs    [recvBatch]mmsghdr
		iovs    [recvBatch]syscall.Iovec
		names   [recvBatch]syscall.RawSockaddrInet6
		control = make([]byte, recvBatch*controlSize)
		// dgrams[:held] hold the buffers the last read left unfilled, at
		// the front, for the next read.
		dgrams [recvBatch]*datagram
		held   int
		// want is how many datagrams the next recvmmsg may return; got,
		// how many have been read since the socket last woke.
		want = recvBatch
		got  int
	)

	// arm gives the first want slots a buffer and resets their headers.
	arm := func() {
		for i := range want {
			if i >= held {
				dgrams[i] = newDatagram()
			}

			d := dgrams[i]

			iovs[i].Base = &d.b[0]
			iovs[i].SetLen(len(d.b))

			h := &hdrs[i].hdr

			h.Name = (*byte)(unsafe.Pointer(&names[i])) //nolint:gosec // G103: the msghdr points at the slot's sockaddr, as the kernel's ABI has it
			h.Namelen = uint32(unsafe.Sizeof(names[i]))
			h.Iov = &iovs[i]
			h.Iovlen = 1
			h.Control = &control[i*controlSize]
			h.SetControllen(controlSize)

			h.Flags = 0
			hdrs[i].len = 0
		}

		held = max(held, want)
	}

	for {
		var (
			n     int
			errno syscall.Errno
		)

		err := rc.Read(func(fd uintptr) bool {
			arm()

			r, _, e := syscall.Syscall6(syscall.SYS_RECVMMSG, fd, uintptr(unsafe.Pointer(&hdrs[0])), uintptr(want), 0, 0, 0) //nolint:gosec // G103: recvmmsg's array of headers
			if e == syscall.EAGAIN {
				// Nothing to read: while the socket waits, as an idle
				// connection's does, it holds one buffer, not a batch.
				for i := 1; i < held; i++ {
					dgrams[i].recycle()

					dgrams[i] = nil
				}

				held = 1

				// The next wake-up reads as many as this one did, and one
				// more, so the read that takes them all is short and the
				// next finds the socket empty with the buffer it left.
				if got > 0 {
					want = min(got+1, recvBatch)
					got = 0
				}

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
			d := dgrams[i]

			dgrams[i] = nil

			size := int(hdrs[i].len)
			if size == 0 {
				d.recycle() // as Read: an empty datagram is dropped

				continue
			}

			d.b = d.b[:size]
			d.path = pathAddrs{
				local: c.localAddr,
				peer:  unmapAddrPort(sockaddrAddrPort(&names[i])),
			}
			parseControl(d, control[i*controlSize:][:hdrs[i].hdr.Controllen])
			f(d)
		}

		// The buffers the read left unfilled move to the front. A full
		// read may have left more queued: the next takes twice as many.
		// A short one emptied the socket: the next, most likely finding
		// it empty, takes only what is left.
		held = copy(dgrams[:], dgrams[n:held])
		clear(dgrams[held:])

		got += n

		if n == want {
			want = min(2*want, recvBatch)
		} else {
			want = max(held, 1)
		}
	}
}

// sockaddrAddrPort is the address recvmmsg wrote: a sockaddr_in or a
// sockaddr_in6, its scope ID as the zone (the interface's name, as the net
// package names it).
func sockaddrAddrPort(sa *syscall.RawSockaddrInet6) netip.AddrPort {
	port := func(p uint16) uint16 {
		// p is in network byte order: its bytes as stored.
		var b [2]byte
		binary.NativeEndian.PutUint16(b[:], p)

		return binary.BigEndian.Uint16(b[:])
	}

	switch sa.Family {
	case syscall.AF_INET:
		sa4 := (*syscall.RawSockaddrInet4)(unsafe.Pointer(sa)) //nolint:gosec // G103: a sockaddr_in in a sockaddr_in6's room, as Family says

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
