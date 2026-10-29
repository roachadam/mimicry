package main

import (
	"net"
	"sync"
	"time"
)

// The in-process delay (BENCH_PROXY_DELAY_MODE=proxy, for hosts without
// sch_netem): what netem on the proxy container's eth0 does, done in the
// relay. Everything the proxy sends waits delay: each relayed chunk and
// datagram, in both directions, through a delay line (a reader stamps each
// chunk, a writer sends it once its stamp plus delay has passed, so the
// throughput stays and only the latency grows); the dial to the server
// (netem holds its SYN) and the handshake replies to the client (netem
// holds the SYN-ACK and the reply: the connection starts delay late,
// replies go out delay late). TCP's own ACKs are not held, so each leg's
// congestion control sees its bare RTT; on a local network with ample
// buffers that does not limit the rates (README.md, "Proxy delay").

const (
	// chunkSize is a delay line's read size.
	chunkSize = 32 << 10
	// lineDepth bounds the chunks in flight per direction (2 MB at
	// chunkSize): the reader waits beyond it, as a full socket would.
	lineDepth = 64
)

var chunks = sync.Pool{
	New: func() any {
		b := make([]byte, chunkSize)

		return &b
	},
}

type chunk struct {
	at time.Time
	b  *[]byte
	n  int
}

// delayedConn is an accepted client connection whose writes (the CONNECT
// and SOCKS5 replies) each wait delay. relay unwraps it.
type delayedConn struct {
	net.Conn
	delay time.Duration
}

func (c *delayedConn) Write(b []byte) (int, error) {
	time.Sleep(c.delay)

	return c.Conn.Write(b)
}

// unwrap returns c's underlying connection.
func unwrap(c net.Conn) net.Conn {
	if d, ok := c.(*delayedConn); ok {
		return d.Conn
	}

	return c
}

// delayCopy copies src to dst through a delay line until src ends or a
// write fails, as io.Copy would without the delay.
func delayCopy(dst, src net.Conn, delay time.Duration) {
	line := make(chan chunk, lineDepth)
	done := make(chan struct{})

	go func() {
		defer close(done)

		failed := false

		for ch := range line {
			if !failed {
				time.Sleep(time.Until(ch.at))

				if _, err := dst.Write((*ch.b)[:ch.n]); err != nil {
					failed = true
					// The reader stops at its next read, as io.Copy
					// would at the failed write.
					_ = src.SetReadDeadline(time.Now())
				}
			}

			chunks.Put(ch.b)
		}
	}()

	for {
		b, _ := chunks.Get().(*[]byte)

		n, err := src.Read(*b)
		if n > 0 {
			line <- chunk{at: time.Now().Add(delay), b: b, n: n}
		} else {
			chunks.Put(b)
		}

		if err != nil {
			break
		}
	}

	close(line)
	<-done
}

// datagram is one UDP datagram in a delay line, with what sends it.
type datagram struct {
	at   time.Time
	b    []byte
	send func([]byte)
}

// datagramLine starts a delay line for datagrams: each goes out delay
// after it was queued. Close the channel to end it.
func datagramLine() chan datagram {
	line := make(chan datagram, 1024)

	go func() {
		for d := range line {
			time.Sleep(time.Until(d.at))
			d.send(d.b)
		}
	}()

	return line
}
