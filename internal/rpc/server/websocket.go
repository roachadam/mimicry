package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/rpc/convert"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/internal/rpc/wire"
)

// webSocket is an open WebSocket's object.
type webSocket struct {
	ws    *mimicry.WebSocket
	close func()
}

func newWebSocket(ws *mimicry.WebSocket) *webSocket {
	return &webSocket{
		ws: ws,
		// The public API has no abort: a socket the SDK drops (Close of
		// its id, a RESET of its stream, a disconnect) closes as
		// Close(1000, "") does. It runs on goroutines of its own
		// (context.AfterFunc, freeAll), and sync.OnceFunc would panic
		// again on every call: a panic is contained here.
		close: sync.OnceFunc(func() {
			contain(func() {
				checkpoint("websocket close")

				_ = ws.Close(1000, "")
			}, nil)
		}),
	}
}

// dialWebSocket answers DialWebSocket. On success its stream carries the
// socket's messages both ways (convert.WriteRecord) until the socket
// closes and the SDK ends its side. A RESET of the stream, or the
// connection's loss, closes the socket at any time: readMessages waits on
// the socket, not on the stream, once the SDK has ended its side.
func (s *session) dialWebSocket(st *wire.Stream, c *pb.DialWebSocket) {
	tr, err := lookup[*transport](&s.objects, c.GetTransport(), "client or transport")
	if err != nil {
		_ = s.replyErr(st, err)

		return
	}

	opts, err := convert.WebSocketOptions(c.GetOptions())
	if err != nil {
		_ = s.replyErr(st, err)

		return
	}

	sock, resp, err := s.dial(st, tr, c, opts)
	if resp != nil {
		defer func() {
			_ = resp.Body.Close()
		}()
	}

	if err != nil {
		if resp == nil {
			_ = s.replyErr(st, err)

			return
		}

		s.respond(st, err, resp)

		return
	}

	w := newWebSocket(sock)
	id := s.objects.add(w)

	err = s.reply(st, &pb.Reply{
		Result: &pb.Reply_Websocket{
			Websocket: &pb.WebSocket{
				Id:         id,
				Protocol:   convert.Text(sock.Protocol()),
				Extensions: convert.Text(sock.Extensions()),
				Response:   convert.Response(resp),
			},
		},
	}, false)
	if err != nil {
		w.close()

		return
	}

	// The stream's context ends canceled when both sides ended it (the
	// socket closed and the SDK wrote its last), with the stream's error
	// when it was reset or the connection failed.
	stop := context.AfterFunc(st.Context(), func() {
		if !errors.Is(context.Cause(st.Context()), context.Canceled) {
			w.close()
		}
	})
	defer stop()

	var reading sync.WaitGroup

	// A panic on either side fails the stream, then closes the socket;
	// the other side then ends too, its writes failing on the reset
	// stream.
	failed := func() {
		s.failStream(st, errInternal)
		w.close()
	}

	reading.Go(func() {
		contain(func() {
			s.readMessages(st, w)
		}, failed)
	})

	contain(func() {
		s.writeMessages(st, id, w)
	}, failed)
	reading.Wait()
}

// dial opens the socket under the stream's context and the call's
// timeout, the handshake counted as the transport's request.
func (s *session) dial(st *wire.Stream, tr *transport, c *pb.DialWebSocket, opts mimicry.WebSocketOptions) (*mimicry.WebSocket, *http.Response, error) {
	ctx := st.Context()

	if d := c.GetTimeout(); d != nil {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, d.AsDuration())
		defer cancel()
	}

	if tr.verify != nil {
		defer tr.verify.waiting(ctx)()
	}

	return tr.t.DialWebSocket(ctx, c.GetUrl(), opts)
}

// readMessages sends the server's messages to the SDK until the socket
// closes, then a RecordEnd with why and END.
func (s *session) readMessages(st *wire.Stream, w *webSocket) {
	for {
		checkpoint("websocket read")

		typ, data, err := w.ws.ReadMessage(s.ctx)
		if err != nil {
			end, _ := proto.Marshal(convert.Error(err))
			if convert.WriteRecord(st, convert.RecordEnd, end) == nil {
				_ = st.CloseWrite()
			}

			return
		}

		if err := convert.WriteRecord(st, byte(typ), data); err != nil {
			w.close()

			return
		}
	}
}

// writeMessages sends the SDK's messages until it ends its side, telling
// it how each went (a WebSocketWritten notice). A malformed record closes
// the socket.
func (s *session) writeMessages(st *wire.Stream, id uint64, w *webSocket) {
	var seq uint64

	for {
		checkpoint("websocket write")

		typ, data, err := convert.ReadRecord(st)
		if errors.Is(err, io.EOF) {
			return
		}

		if err != nil {
			w.close()

			return
		}

		seq++

		written := &pb.WebSocketWritten{
			Websocket: id,
			Seq:       seq,
		}
		if err := w.ws.WriteMessage(s.ctx, mimicry.MessageType(typ), data); err != nil {
			written.Error = convert.Error(err)
		}

		s.notify(&pb.Event{
			Event: &pb.Event_WebsocketWritten{
				WebsocketWritten: written,
			},
		})
	}
}
