//go:build !quicbasicnet && darwin

package quic

// mimicry: macOS has no recvmmsg (UPSTREAM.md change 13): Read reads one
// datagram per recvmsg, as upstream does.
func (c *netUDPConn) readBatch(func(*datagram)) (ok bool, err error) {
	return false, nil
}
