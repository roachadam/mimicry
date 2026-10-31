package quic

// mimicry: what a stream reports once its connection closed locally
// (stream.go's connHasClosed, UPSTREAM.md change 20), on upstream's
// synthetic peer: only there does the handshake stop short of confirmed.

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	tls "github.com/roachadam/utls"
)

// A stream on a connection that closed itself with a cause (the handshake
// timeout of a request sent before the handshake completed) reports that
// cause, as a peer's close does; it read as a closed stream instead. A
// direction the caller closed, or a close without a cause, reports as
// before (until the drain period ends).
func TestStreamErrorAfterLocalClose(t *testing.T) {
	const timeout = 5 * time.Second

	closedRead := errors.New("read from closed stream")
	closedWrite := errors.New("write to reset stream: stream error code 0")

	for _, test := range []struct {
		name string
		// before runs on the stream before the connection closes.
		before func(s *Stream) error
		// close ends the connection and consumes its CONNECTION_CLOSE.
		close     func(tc *testConn)
		wantRead  error
		wantWrite error
	}{{
		name: "handshake timeout",
		close: func(tc *testConn) {
			time.Sleep(timeout)
			tc.wantFrame("client closes connection after handshake timeout",
				packetTypeHandshake, debugFrameConnectionCloseTransport{
					code: errConnectionRefused,
				})
		},
		wantRead:  errHandshakeTimeout,
		wantWrite: errHandshakeTimeout,
	}, {
		name: "read side closed, then handshake timeout",
		before: func(s *Stream) error {
			return s.CloseRead()
		},
		close: func(tc *testConn) {
			time.Sleep(timeout)
			tc.wantFrame("client closes connection after handshake timeout",
				packetTypeHandshake, debugFrameConnectionCloseTransport{
					code: errConnectionRefused,
				})
		},
		wantRead:  closedRead,
		wantWrite: errHandshakeTimeout,
	}, {
		name: "write side closed, then handshake timeout",
		before: func(s *Stream) error {
			return s.CloseWrite()
		},
		close: func(tc *testConn) {
			time.Sleep(timeout)
			tc.wantFrame("client closes connection after handshake timeout",
				packetTypeHandshake, debugFrameConnectionCloseTransport{
					code: errConnectionRefused,
				})
		},
		wantRead:  errHandshakeTimeout,
		wantWrite: closedWrite,
	}, {
		name: "stream reset, then handshake timeout",
		before: func(s *Stream) error {
			return s.Reset(7)
		},
		close: func(tc *testConn) {
			time.Sleep(timeout)
			tc.wantFrame("client closes connection after handshake timeout",
				packetTypeHandshake, debugFrameConnectionCloseTransport{
					code: errConnectionRefused,
				})
		},
		wantRead:  errHandshakeTimeout,
		wantWrite: StreamError(7),
	}, {
		name: "Abort(nil)",
		close: func(tc *testConn) {
			tc.conn.Abort(nil)
			tc.wantFrame("client closes connection",
				packetTypeHandshake, debugFrameConnectionCloseTransport{
					code: errNo,
				})
		},
		wantRead:  closedRead,
		wantWrite: closedWrite,
	}} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := newTestConn(t, clientSide, permissiveTransportParameters, func(c *Config) {
					c.HandshakeTimeout = timeout
				})
				tc.ignoreFrame(frameTypeAck)
				tc.ignoreFrame(frameTypeNewConnectionID)
				tc.ignoreFrame(frameTypeStopSending)
				tc.ignoreFrame(frameTypeStreamBase)
				tc.ignoreFrame(frameTypeResetStream)

				// The server's flights, never confirmed (no HANDSHAKE_DONE).
				tc.wantFrameType("client sends Initial CRYPTO flight",
					packetTypeInitial, debugFrameCrypto{})
				tc.writeAckForAll()
				tc.writeFrames(packetTypeInitial,
					debugFrameCrypto{
						data: tc.cryptoDataIn[tls.QUICEncryptionLevelInitial],
					})
				tc.writeFrames(packetTypeHandshake,
					debugFrameCrypto{
						data: tc.cryptoDataIn[tls.QUICEncryptionLevelHandshake],
					})
				tc.wantFrameType("client sends Handshake CRYPTO flight",
					packetTypeHandshake, debugFrameCrypto{})
				tc.writeAckForAll()

				s := newLocalStream(t, tc, bidiStream)
				if test.before != nil {
					if err := test.before(s); err != nil {
						t.Fatal(err)
					}
				}

				tc.wantIdle("client is waiting for end of handshake")
				test.close(tc)

				_, err := s.Read(make([]byte, 1))
				checkStreamErr(t, "Read", err, test.wantRead)

				_, err = s.Write([]byte{0})
				checkStreamErr(t, "Write", err, test.wantWrite)
			})
		})
	}
}

// checkStreamErr checks err against want: errors.Is, or the same text for
// an error the package makes with errors.New. A nil want takes any error.
func checkStreamErr(t *testing.T, op string, err, want error) {
	t.Helper()

	switch {
	case err == nil:
		t.Errorf("%s = nil, want an error", op)
	case want == nil:
	case !errors.Is(err, want) && err.Error() != want.Error():
		t.Errorf("%s = %v, want %v", op, err, want)
	}
}
