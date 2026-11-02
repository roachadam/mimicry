package quic

// mimicry: the loop's three shortcuts (UPSTREAM.md changes 24-26), each
// against the slow way it replaced: a wrong shortcut sends wrong stream
// bytes, misjudges the path, or answers a read's datagrams differently.

import (
	"bytes"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/roachadam/mimicry/profiles"
)

// The operations TestPipeReadCursor applies to a send pipe.
type (
	pipeWrite   struct{ n int64 }      // append n bytes at the end
	pipeSend    struct{ n int64 }      // read the next n unsent bytes
	pipeResend  struct{ off, n int64 } // read n bytes from off again
	pipeAck     struct{ off int64 }    // discard everything before off
	pipeRelease struct{}               // releaseIfEmpty
)

// A send pipe read from its cursor gives the same bytes as one walked from
// its start, through sends in order, retransmissions of earlier data, acks
// that discard the buffer the cursor is in, and the pipe emptied and
// filled again; the cursor never points at a buffer the pipe no longer
// holds.
func TestPipeReadCursor(t *testing.T) {
	src := make([]byte, 1<<20)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // seeded on purpose: the same bytes every run

	for i := range src {
		src[i] = byte(rng.Uint32())
	}

	for _, tt := range []struct {
		name string
		ops  []any
	}{{
		name: "sends in order across buffers",
		ops:  []any{pipeWrite{20000}, pipeSend{1200}, pipeSend{1200}, pipeSend{4000}, pipeSend{9000}, pipeSend{4600}},
	}, {
		name: "send ends on a buffer boundary",
		ops:  []any{pipeWrite{12288}, pipeSend{4096}, pipeSend{4096}, pipeSend{4096}},
	}, {
		name: "retransmission before the cursor",
		ops:  []any{pipeWrite{30000}, pipeSend{9000}, pipeSend{9000}, pipeResend{1000, 1200}, pipeSend{1200}, pipeResend{8000, 5000}, pipeSend{3000}},
	}, {
		name: "ack discards the cursor's buffer after a retransmission",
		ops:  []any{pipeWrite{20000}, pipeSend{14000}, pipeResend{1000, 1200}, pipeAck{9000}, pipeSend{3000}, pipeResend{9000, 100}},
	}, {
		name: "ack past everything sent, then more",
		ops:  []any{pipeWrite{8192}, pipeSend{8192}, pipeAck{8192}, pipeRelease{}, pipeWrite{5000}, pipeSend{5000}, pipeResend{8192, 100}},
	}, {
		name: "partial acks and retransmissions at random",
		ops:  randomPipeOps(rand.New(rand.NewPCG(3, 4)), int64(len(src))), //nolint:gosec // seeded on purpose: the same operations every run
	}} {
		t.Run(tt.name, func(t *testing.T) {
			var (
				p    pipe
				sent int64
			)

			read := func(off, n int64) {
				t.Helper()

				got := make([]byte, n)
				p.copy(off, got)

				if !bytes.Equal(got, src[off:off+n]) {
					t.Fatalf("read [%d,%d): wrong bytes", off, off+n)
				}

				// The cursor is a buffer the pipe holds.
				if p.last != nil {
					held := false
					for pb := p.head; pb != nil; pb = pb.next {
						held = held || pb == p.last
					}

					if !held {
						t.Fatalf("read [%d,%d): the cursor is a buffer the pipe no longer holds", off, off+n)
					}
				}
			}

			for _, op := range tt.ops {
				switch op := op.(type) {
				case pipeWrite:
					p.writeAt(src[p.end:p.end+op.n], p.end)
				case pipeSend:
					n := min(op.n, p.end-sent)
					read(sent, n)

					sent += n

				case pipeResend:
					if op.off >= p.start && op.off+op.n <= sent {
						read(op.off, op.n)
					}

				case pipeAck:
					off := min(op.off, sent)
					if off > p.start {
						p.discardBefore(off)
					}

					if p.last != nil && p.head == nil {
						t.Fatalf("ack %d: the pipe is empty but holds a cursor", off)
					}

				case pipeRelease:
					p.releaseIfEmpty()
				}
			}
		})
	}
}

// randomPipeOps is a stream's send buffer under load: writes, sends of a
// packet's worth, acks of part of what was sent, and retransmissions.
func randomPipeOps(rng *rand.Rand, size int64) []any {
	var (
		ops              []any
		end, sent, acked int64
	)

	for range 5000 {
		switch k := rng.IntN(10); {
		case k < 2 && end < size-65536:
			n := 1 + rng.Int64N(65535)

			ops = append(ops, pipeWrite{n})

			end += n

		case k < 6 && sent < end:
			n := min(1+rng.Int64N(1300), end-sent)

			ops = append(ops, pipeSend{n})

			sent += n

		case k < 8 && acked < sent:
			acked += 1 + rng.Int64N(sent-acked)

			ops = append(ops, pipeAck{acked})

		case k < 10 && acked < sent:
			off := acked + rng.Int64N(sent-acked)

			ops = append(ops, pipeResend{off, min(1+rng.Int64N(1300), sent-off)})
		}
	}

	return ops
}

// The oldest ack-eliciting packet in flight, found from where the last
// search stopped, is the one a scan from the start finds: through ACK-only
// and padding-only packets, skipped packet numbers, ACKs with gaps, losses
// by packet and by time threshold, and on a connection across key updates.
func TestOldestAckEliciting(t *testing.T) {
	scan := func(s *sentPacketList) *sentPacket {
		for i := range s.size {
			sent := s.nth(i)
			if sent.state == sentPacketSent && sent.ackEliciting && sent.inFlight {
				return sent
			}
		}

		return nil
	}

	check := func(t *testing.T, step string, s *sentPacketList) {
		t.Helper()

		want := scan(s)
		got := s.oldestAckEliciting()

		if got != want {
			t.Fatalf("%s: oldest ack-eliciting packet %v, want %v", step, pnumOf(got), pnumOf(want))
		}
	}

	t.Run("random sends, acks and losses", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // seeded on purpose: the same packets every run
		now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

		var c lossState
		c.init(clientSide, 1200, now)

		s := &c.spaces[appDataSpace].sentPacketList
		nop := func(numberSpace, *sentPacket, packetFate) {}

		for step := range 20000 {
			switch k := rng.IntN(20); {
			case k < 10:
				sent := newSentPacket()

				sent.num = c.nextNumber(appDataSpace)
				sent.size = 1200

				switch rng.IntN(3) {
				case 0: // ack-eliciting
					sent.ackEliciting, sent.inFlight = true, true
				case 1: // ACK only
				case 2: // padding: in flight, not ack-eliciting
					sent.inFlight = true
				}

				c.packetSent(now, nil, appDataSpace, sent)

			case k < 11:
				c.skipNumber(now, appDataSpace)
			case k < 17:
				ackRandomRanges(rng, &c, now, nop)
			default:
				now = now.Add(time.Duration(rng.IntN(5000)) * time.Microsecond)
				c.advance(now, nop)
			}

			check(t, "step "+strconv.Itoa(step), s)
		}
	})

	t.Run("key updates", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tc := newTestConn(t, serverSide)
			tc.handshake()

			tc.ignoreFrames = nil

			s := &tc.conn.loss.spaces[appDataSpace].sentPacketList
			ping := func() packetNumber {
				t.Helper()

				tc.conn.ping(appDataSpace)
				tc.wantFrame("conn sends PING", packetType1RTT, debugFramePing{})

				return tc.lastPacket.num
			}

			check(t, "after the handshake", s)

			p0 := ping()
			p1 := ping()
			ping() // lost below
			check(t, "three PINGs in phase 0", s)

			// The peer updates its keys; the conn follows with an ACK
			// and a PING in phase 1.
			tc.sendKeyNumber = 1
			tc.sendKeyPhaseBit = true
			tc.writeFrames(packetType1RTT, debugFramePing{})
			tc.advanceToTimer()
			tc.wantFrameType("conn ACKs the peer's packet", packetType1RTT, debugFrameAck{})
			tc.wantFrame("first packet after a key update is ack-eliciting", packetType1RTT, debugFramePing{})

			p3 := tc.lastPacket.num

			check(t, "key update to phase 1", s)

			// An ACK with a gap: phase 0's middle packet and phase 1's.
			tc.writeFrames(packetType1RTT, debugFrameAck{
				ranges: []i64range[packetNumber]{{p1, p1 + 1}, {p3, p3 + 1}},
			})
			check(t, "ACK of one packet from each phase", s)

			p4 := ping()
			tc.writeFrames(packetType1RTT, debugFrameAck{
				ranges: []i64range[packetNumber]{{p0, p0 + 1}, {p4, p4 + 1}},
			})
			check(t, "ACK of the oldest and the newest", s)

			// The peer updates again; phase 0's last PING is lost by
			// packet threshold once three later packets are acknowledged.
			tc.sendKeyNumber = 2
			tc.sendKeyPhaseBit = false
			tc.writeFrames(packetType1RTT, debugFramePing{})
			tc.advanceToTimer()
			tc.wantFrameType("conn ACKs the peer's packet", packetType1RTT, debugFrameAck{})
			tc.wantFrame("first packet after a key update is ack-eliciting", packetType1RTT, debugFramePing{})
			check(t, "key update to phase 2", s)

			ping()

			p6 := ping()
			tc.writeFrames(packetType1RTT, debugFrameAck{
				ranges: []i64range[packetNumber]{{p3, p6 + 1}},
			})
			check(t, "phase 0's last PING lost", s)

			if got := s.oldestAckEliciting(); got != nil {
				t.Fatalf("phase 0's last PING lost: oldest ack-eliciting packet %v, want none", got.num)
			}

			ping()
			check(t, "a PING after the loss", s)

			tc.writeAckForAll()
			check(t, "all acknowledged", s)
		})
	})
}

// ackRandomRanges acknowledges up to three random ranges of the packets
// sent, largest first, as an ACK frame lists them; ranges that would
// acknowledge a skipped packet number are left out.
func ackRandomRanges(rng *rand.Rand, c *lossState, now time.Time, f func(numberSpace, *sentPacket, packetFate)) {
	s := &c.spaces[appDataSpace]
	if s.size == 0 {
		return
	}

	start, end := s.start(), s.end()
	hi := start + packetNumber(rng.Int64N(int64(end-start))) + 1

	var ranges []i64range[packetNumber]

	for range 1 + rng.IntN(3) {
		if hi <= start {
			break
		}

		lo := hi - 1 - packetNumber(rng.Int64N(min(int64(hi-start), 8)))
		ok := true

		for n := lo; n < hi; n++ {
			if s.num(n).state == sentPacketUnsent {
				ok = false
			}
		}

		if ok {
			ranges = append(ranges, i64range[packetNumber]{lo, hi})
		}

		hi = lo - 1 - packetNumber(rng.IntN(3))
	}

	if len(ranges) == 0 {
		return
	}

	c.receiveAckStart()

	for i, r := range ranges {
		if err := c.receiveAckRange(now, appDataSpace, i, r.start, r.end, f); err != nil {
			panic(err)
		}
	}

	c.receiveAckEnd(now, nil, appDataSpace, 0, f)
}

func pnumOf(sent *sentPacket) any {
	if sent == nil {
		return "none"
	}

	return sent.num
}

// A read's datagrams handed to the loop at once are handled one at a time
// and in order, with the send pass after each that a datagram alone gets:
// Chrome's ACKs are the ones it sends when each comes alone. A browser
// that batches its ACKs (Firefox) reads the whole read before answering,
// so it sends one ACK for all of them.
func TestDatagramBatchHandledInOrder(t *testing.T) {
	for _, tt := range []struct {
		name    string
		profile string
		batched bool
	}{
		{"chrome", profiles.ChromeMacOS().Name(), false},
		{"firefox", profiles.FirefoxMacOS().Name(), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			acks := func(batch bool) (acks [][]i64range[packetNumber], last packetNumber) {
				synctest.Test(t, func(t *testing.T) {
					tc := newProfileTestConn(t, tt.profile, func(p *transportParameters) {
						p.initialMaxData = 1 << 20
						p.initialMaxStreamDataBidiRemote = 1 << 20
						p.initialMaxStreamsBidi = 10
					})
					tc.readFlight()
					tc.serverFlight()
					tc.readFlight()
					tc.serverOneRTT(debugFrameHandshakeDone{})

					// Past any ACK delay: nothing is owed when the
					// datagrams arrive.
					time.Sleep(time.Second)
					tc.readFlight()

					const n = 6

					ds := make([]*datagram, n)
					for i := range ds {
						last = tc.peerNextPacketNum[appDataSpace]
						ds[i] = tc.serverOneRTTDatagram(debugFramePing{})
					}

					if batch {
						tc.conn.sendMsg(newDatagramBatch(ds))
					} else {
						for _, d := range ds {
							tc.conn.sendMsg(d)
							synctest.Wait()
						}
					}

					for {
						d := tc.readDatagram()
						if d == nil {
							break
						}

						for _, p := range d.packets {
							for _, f := range p.frames {
								if a, ok := f.(debugFrameAck); ok {
									acks = append(acks, a.ranges)
								}
							}
						}
					}
				})

				return acks, last
			}

			alone, _ := acks(false)
			got, last := acks(true)

			if !tt.batched {
				if !slices.EqualFunc(got, alone, slices.Equal) {
					t.Fatalf("ACKs for a read's datagrams %v, want those for each alone %v", got, alone)
				}

				return
			}

			if len(got) != 1 || got[0][len(got[0])-1].end != last+1 {
				t.Fatalf("ACKs for a read's datagrams %v, want one up to %d", got, last)
			}

			if len(alone) < 2 {
				t.Fatalf("ACKs for each datagram alone %v, want more than one", alone)
			}
		})
	}
}
