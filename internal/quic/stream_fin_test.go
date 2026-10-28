package quic

// mimicry: a FIN that closes just as a PTO probe goes out (stream.go's
// appendOutFramesLocked, UPSTREAM.md change 10), on upstream's synthetic
// peer: only there can the probe and the close be put in that order.

import (
	"testing"
	"testing/synctest"
	"time"
)

// A probe resends the start of a stream whose data it cannot hold whole, so
// its frame goes without the FIN; the FIN must still be sent. Marking it
// sent with the probe left it waiting on a packet that did not carry it:
// never sent, and the peer never saw the stream end (found by
// TestStreamBuffersReturned under load: a request waiting for its EOF until
// the idle timeout).
func TestStreamFINAfterTruncatedProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc, s := newTestConnAndLocalStream(t, serverSide, uniStream, func(p *transportParameters) {
			p.initialMaxStreamsUni = 1
			p.initialMaxData = 1 << 20
			p.initialMaxStreamDataUni = 1 << 20
		})
		tc.ignoreFrame(frameTypeAck)

		// More than a packet holds, sent and not acknowledged.
		data := makeTestData(2000)
		if _, err := s.Write(data); err != nil {
			t.Fatal(err)
		}

		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}

		// Its packets, unacknowledged.
		for {
			if f, _ := tc.readFrame(); f == nil {
				break
			}
		}

		// The loop handles the PTO's timer, then the stream closes before
		// the loop sends the probe (a loaded process: CloseWrite from
		// another goroutine in between).
		err := tc.conn.runOnLoop(t.Context(), func(_ time.Time, c *Conn) {
			time.Sleep(time.Until(c.loss.timer))
			c.loss.advance(time.Now(), c.handleAckOrLoss)

			if err := s.CloseWrite(); err != nil {
				t.Error(err)
			}
		})
		if err != nil {
			t.Fatal(err)
		}

		fin := false
		probe := true

		for range 2 {
			for f, _ := tc.readFrame(); f != nil; f, _ = tc.readFrame() {
				sf, ok := f.(debugFrameStream)
				if !ok || sf.id != s.id {
					continue
				}

				if probe && (sf.off != 0 || len(sf.data) == len(data)) {
					t.Fatalf("probe: STREAM frame at %d with %d bytes, want the start of the stream without its end", sf.off, len(sf.data))
				}

				probe = false
				fin = fin || sf.fin
			}

			// Everything sent is acknowledged: what is left to send is
			// the FIN.
			tc.writeAckForAll()
		}

		if probe {
			t.Fatal("no probe sent")
		}

		if !fin {
			t.Fatal("no FIN sent after the probe")
		}
	})
}
