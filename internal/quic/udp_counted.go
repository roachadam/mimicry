package quic

import (
	"net"
)

// mimicry: countedUDP is a UDP socket whose bytes are counted
// (traffic.UDPConn, Config.Traffic): read and written as the *net.UDPConn
// it is, batches and all, each datagram's size counted.
type countedUDP interface {
	net.PacketConn
	UDP() *net.UDPConn
	Sent(n int)
	Received(n int)
}

// countedConn is the endpoint's packetConn over a countedUDP.
type countedConn struct {
	packetConn

	count countedUDP
}

func newCountedConn(cu countedUDP) (*countedConn, error) {
	c, err := newNetUDPConn(cu.UDP())
	if err != nil {
		return nil, err
	}

	return &countedConn{
		packetConn: c,
		count:      cu,
	}, nil
}

func (c *countedConn) Read(f func([]*datagram)) error {
	return c.packetConn.Read(func(ds []*datagram) {
		n := 0
		for _, d := range ds {
			n += len(d.b)
		}

		c.count.Received(n)
		f(ds)
	})
}

func (c *countedConn) Write(dgram datagram) error {
	err := c.packetConn.Write(dgram)
	if err == nil {
		c.count.Sent(len(dgram.b))
	}

	return err
}

// Close closes the socket through the counter, which folds its counts in.
func (c *countedConn) Close() error {
	return c.count.Close()
}
