package quictrace

// LateMicros is how long after the client's last ACK reached the server a
// packet the server sent came after a quiet spell: well over a loopback or
// LAN round trip (well under 1 ms in the captures) and well under the
// browsers' ACK delays (20–25 ms).
const LateMicros = 5000

// AtOnceMicros is how soon after the packet it acknowledges an ACK reached
// the server when it was sent at once, not at the ACK delay.
const AtOnceMicros = 3000

// LoneACKs counts a client's ACK-only packets that acknowledge a lone
// server 1-RTT packet, the only one new since the client's previous ACK:
// so the one that elicited the ACK. Late are those whose packet the server
// sent LateMicros or more after that previous ACK reached it, Soon the
// others; *AtOnce, those acknowledged within AtOnceMicros. Only the
// server's view: the client's ACK Delay field is not read (Safari's is a
// constant).
type LoneACKs struct {
	Late, LateAtOnce, Soon, SoonAtOnce int
}

// Add counts c's: none without the server's datagrams.
func (n *LoneACKs) Add(c *Conn) {
	sent := map[uint64]int64{}
	for _, d := range c.Server {
		for _, p := range d.Packets {
			if p.Type == Packet1RTT {
				sent[p.PN] = d.Micros
			}
		}
	}

	if len(sent) == 0 {
		return
	}

	acked := map[uint64]bool{}
	prev := int64(-1)

	for _, d := range c.Datagrams {
		for pi := range d.Packets {
			p := &d.Packets[pi]
			if p.Type != Packet1RTT {
				continue
			}

			ack, ackOnly := (*Frame)(nil), true
			for i := range p.Frames {
				switch p.Frames[i].Type {
				case FrameAck:
					ack = &p.Frames[i]
				case FramePadding:
				default:
					ackOnly = false
				}
			}

			if ack == nil {
				continue
			}

			var news []uint64
			for _, r := range ack.Ranges {
				for pn := r[0]; pn <= r[1]; pn++ {
					if _, ok := sent[pn]; ok && !acked[pn] {
						acked[pn] = true
						news = append(news, pn)
					}
				}
			}

			if ackOnly && prev >= 0 && len(news) == 1 {
				at := sent[news[0]]

				atOnce := 0
				if d.Micros-at < AtOnceMicros {
					atOnce = 1
				}

				if at-prev >= LateMicros {
					n.Late++

					n.LateAtOnce += atOnce
				} else {
					n.Soon++

					n.SoonAtOnce += atOnce
				}
			}

			prev = d.Micros
		}
	}
}
