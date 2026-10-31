package quic

// mimicry: Chrome's move on a degrading path (client_path.go), on
// upstream's synthetic peer (testConn).

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/roachadam/mimicry/profiles"
)

// A Chrome connection whose path stops answering moves to a new port
// (DialConfig.NewPath) unless the server sent disable_active_migration
// (RFC 9000, section 18.2: the client MUST NOT migrate; Chrome 102 and
// later does not). Either way the path is marked degraded, so the watch
// does not fire again until the server answers. Moving anyway sends the
// stuck request from an address such a server may not answer.
func TestClientPathDegradingMove(t *testing.T) {
	for _, tt := range []struct {
		name    string
		disable bool
		moves   int32
	}{
		{"migrates", false, 1},
		{"disable_active_migration", true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := newProfileTestConn(t, profiles.Chrome().Name, func(p *transportParameters) {
					p.disableActiveMigration = tt.disable
					p.initialMaxData = 1 << 20
					p.initialMaxStreamDataBidiRemote = 1 << 20
					p.initialMaxStreamsBidi = 10
				})
				tc.readFlight()
				tc.serverFlight()
				tc.readFlight()

				// The server confirms the handshake and gives a spare
				// connection ID to move with.
				tc.serverOneRTT(debugFrameHandshakeDone{}, debugFrameNewConnectionID{
					seq:    1,
					connID: testPeerConnID(1),
				})

				var moves atomic.Int32

				err := tc.conn.runOnLoop(context.Background(), func(_ time.Time, c *Conn) {
					ps := &c.client.pathState

					ps.socket = &swapPacketConn{}
					ps.newPath = func(context.Context) (net.PacketConn, error) {
						moves.Add(1)

						return nil, errors.New("no new socket in the test")
					}
				})
				if err != nil {
					t.Fatal(err)
				}

				// A request the server never answers: the path is
				// blackholed from here.
				s, err := tc.conn.NewStream(canceledContext())
				if err != nil {
					t.Fatal(err)
				}

				if _, err := s.Write([]byte("GET")); err != nil {
					t.Fatal(err)
				}

				if err := s.Flush(); err != nil {
					t.Fatal(err)
				}

				for range 100 {
					time.Sleep(100 * time.Millisecond)
					tc.readFlight()
				}

				if got := moves.Load(); got != tt.moves {
					t.Errorf("NewPath called %d times, want %d", got, tt.moves)
				}

				var degraded bool

				err = tc.conn.runOnLoop(context.Background(), func(_ time.Time, c *Conn) {
					degraded = c.client.pathState.degraded
				})
				if err != nil {
					t.Fatal(err)
				}

				if !degraded {
					t.Error("the path is not marked degraded")
				}
			})
		})
	}
}

// serverOneRTT gives the connection a 1-RTT packet of the server's
// directly: the test endpoint routes short headers on 8-byte connection
// IDs only.
func (tc *testConn) serverOneRTT(frames ...debugFrame) {
	tc.t.Helper()

	d := tc.newDatagramWithFrames(packetType1RTT, frames...)
	tc.peerNextPacketNum[appDataSpace]++
	tc.conn.sendMsg(&datagram{
		b:    encodeTestPacket(tc.t, tc, d.packets[0], 0),
		path: tc.path,
	})
	synctest.Wait()
}
