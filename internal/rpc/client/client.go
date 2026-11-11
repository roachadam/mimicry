// Package client is a Go client of mimicry-core's protocol (decision
// 0069), as an SDK speaks it: Hello first, calls on streams of their own,
// bodies and WebSocket messages as DATA, VerifyConnection answered, the
// core's notices handed off without blocking the reader. It exists for
// tests (internal/rpc/server's, and the harness's e2e test that sends
// requests through mimicry-core), so its API is what they need, not an
// SDK's.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/roachadam/mimicry/internal/rpc/convert"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/internal/rpc/wire"
)

// Config configures a client.
type Config struct {
	// Verify answers each VerifyConnection reverse call, on a goroutine
	// of its own: nil accepts the connection. ctx ends if the core gives
	// up the call. Nil accepts every connection.
	Verify func(ctx context.Context, v *pb.VerifyConnection) *pb.Error
}

// Client is one connection to a core.
type Client struct {
	cfg   Config
	wc    *wire.Conn
	hello *pb.HelloReply

	mu sync.Mutex
	// streamErrs are StreamError notices by stream id.
	streamErrs map[uint32]*pb.Error
	// written are WebSocketWritten notices by socket, then seq.
	written map[uint64]map[uint64]*pb.WebSocketWritten
	// notice is closed, and replaced, after each notice.
	notice     chan struct{}
	setCookies []*pb.SetCookie

	accepting sync.WaitGroup
}

// Error is a call's error, as the core sent it.
type Error struct {
	Proto *pb.Error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Proto.GetCode(), e.Proto.GetMessage())
}

// New sends Hello on rwc and returns the client.
func New(ctx context.Context, rwc io.ReadWriteCloser, cfg Config) (*Client, error) {
	c := &Client{
		cfg:        cfg,
		streamErrs: map[uint32]*pb.Error{},
		written:    map[uint64]map[uint64]*pb.WebSocketWritten{},
		notice:     make(chan struct{}),
	}

	wc, err := wire.NewConn(rwc, wire.Config{
		Role:    wire.RoleSDK,
		OnEvent: c.onEvent,
	})
	if err != nil {
		return nil, err
	}

	c.wc = wc

	c.accepting.Go(c.accept)

	r, err := c.Call(ctx, &pb.Call{
		Method: &pb.Call_Hello{
			Hello: &pb.Hello{
				ProtocolMajor: pb.ProtocolMajor,
				ProtocolMinor: pb.ProtocolMinor,
				SdkVersion:    "go-test",
			},
		},
	})
	if err != nil {
		c.Close()

		return nil, err
	}

	c.hello = r.GetHello()

	return c, nil
}

// HelloReply is the core's answer to Hello.
func (c *Client) HelloReply() *pb.HelloReply {
	return c.hello
}

// Close closes the connection, which frees every object the core holds
// for it.
func (c *Client) Close() {
	_ = c.wc.Close()
	c.accepting.Wait()
}

// Done is closed once the connection has ended.
func (c *Client) Done() <-chan struct{} {
	return c.wc.Done()
}

// onEvent takes a notice: it records it and returns, never waiting
// (wire.Config.OnEvent).
func (c *Client) onEvent(payload []byte) {
	e := &pb.Event{}
	if proto.Unmarshal(payload, e) != nil {
		return
	}

	c.mu.Lock()

	switch e := e.GetEvent().(type) {
	case *pb.Event_StreamError:
		c.streamErrs[e.StreamError.GetStream()] = e.StreamError.GetError()
	case *pb.Event_WebsocketWritten:
		w := e.WebsocketWritten
		if c.written[w.GetWebsocket()] == nil {
			c.written[w.GetWebsocket()] = map[uint64]*pb.WebSocketWritten{}
		}

		c.written[w.GetWebsocket()][w.GetSeq()] = w

	case *pb.Event_SetCookie:
		c.setCookies = append(c.setCookies, e.SetCookie)
	}

	close(c.notice)

	c.notice = make(chan struct{})
	c.mu.Unlock()
}

// SetCookies returns the SetCookie notices received so far.
func (c *Client) SetCookies() []*pb.SetCookie {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]*pb.SetCookie(nil), c.setCookies...)
}

// accept answers the core's reverse calls until the connection ends.
func (c *Client) accept() {
	var calls sync.WaitGroup
	defer calls.Wait()

	for {
		st, err := c.wc.Accept(context.Background())
		if err != nil {
			return
		}

		calls.Go(func() {
			c.answer(st)
		})
	}
}

// answer answers one reverse call.
func (c *Client) answer(st *wire.Stream) {
	defer func() {
		_ = st.Close()
	}()

	e := &pb.Event{}
	if proto.Unmarshal(st.Payload(), e) != nil {
		return
	}

	if n := e.GetContinued(); n > 0 {
		buf := make([]byte, n)
		if _, err := io.ReadFull(st, buf); err != nil {
			return
		}

		if proto.Unmarshal(buf, e) != nil {
			return
		}
	}

	r := &pb.EventReply{}
	if v := e.GetVerifyConnection(); v != nil && c.cfg.Verify != nil {
		r.Error = c.cfg.Verify(st.Context(), v)
	}

	b, err := proto.Marshal(r)
	if err != nil {
		return
	}

	_ = st.WriteReply(b, true)
}

// Call makes a call with no body and returns its reply; a reply with an
// error is returned with it as an *Error.
func (c *Client) Call(ctx context.Context, call *pb.Call) (*pb.Reply, error) {
	k, err := c.Open(ctx, call, false)
	if err != nil {
		return nil, err
	}
	defer k.Close()

	r, err := k.Reply()
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(io.Discard, k); err != nil {
		return r, err
	}

	return r, replyErr(r)
}

func replyErr(r *pb.Reply) error {
	if r.GetError() != nil {
		return &Error{
			Proto: r.GetError(),
		}
	}

	return nil
}

// Call is an open call: its stream, for a body either way.
type Call struct {
	c  *Client
	st *wire.Stream
}

// Open opens a call; body says DATA follows it (end it with CloseWrite).
// A call too large for a frame goes as DATA (Call.continued).
func (c *Client) Open(ctx context.Context, call *pb.Call, body bool) (*Call, error) {
	b, err := proto.Marshal(call)
	if err != nil {
		return nil, err
	}

	if len(b) <= wire.MaxPayload {
		st, err := c.wc.Open(ctx, b, !body)
		if err != nil {
			return nil, err
		}

		return &Call{
			c:  c,
			st: st,
		}, nil
	}

	head, _ := proto.Marshal(&pb.Call{
		Continued: uint32(len(b)),
	})

	st, err := c.wc.Open(ctx, head, false)
	if err != nil {
		return nil, err
	}

	k := &Call{
		c:  c,
		st: st,
	}

	if _, err := st.Write(b); err != nil {
		k.Close()

		return nil, err
	}

	if !body {
		if err := st.CloseWrite(); err != nil {
			k.Close()

			return nil, err
		}
	}

	return k, nil
}

// ID is the call's stream id.
func (k *Call) ID() uint32 {
	return k.st.ID()
}

// Reply waits for the reply, reading it from DATA when continued.
func (k *Call) Reply() (*pb.Reply, error) {
	b, err := k.st.Reply()
	if err != nil {
		return nil, err
	}

	r := &pb.Reply{}
	if err := proto.Unmarshal(b, r); err != nil {
		return nil, err
	}

	if n := r.GetContinued(); n > 0 {
		buf := make([]byte, n)
		if _, err := io.ReadFull(k.st, buf); err != nil {
			return nil, err
		}

		r = &pb.Reply{}
		if err := proto.Unmarshal(buf, r); err != nil {
			return nil, err
		}
	}

	return r, nil
}

// Read reads the reply's body; a body the core failed part way returns
// its StreamError's error.
func (k *Call) Read(p []byte) (int, error) {
	n, err := k.st.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		if e := k.c.streamErr(k.st.ID()); e != nil {
			return n, &Error{
				Proto: e,
			}
		}
	}

	return n, err
}

func (c *Client) streamErr(id uint32) *pb.Error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.streamErrs[id]
}

// Write sends body DATA.
func (k *Call) Write(p []byte) (int, error) {
	return k.st.Write(p)
}

// CloseWrite ends the body.
func (k *Call) CloseWrite() error {
	return k.st.CloseWrite()
}

// Close ends the call, resetting it if it has not ended.
func (k *Call) Close() {
	_ = k.st.Close()
}

// Do sends req through the client or transport id, with body as its
// streamed body when not nil, and returns the response and its body.
func (c *Client) Do(ctx context.Context, id uint64, req *pb.Request, body io.Reader) (*pb.Response, []byte, error) {
	req.StreamBody = body != nil

	k, err := c.Open(ctx, &pb.Call{
		Method: &pb.Call_Do{
			Do: &pb.Do{
				Client:  id,
				Request: req,
			},
		},
	}, body != nil)
	if err != nil {
		return nil, nil, err
	}
	defer k.Close()

	if body != nil {
		go func() {
			if _, err := io.Copy(k, body); err != nil {
				k.Close()

				return
			}

			_ = k.CloseWrite()
		}()
	}

	r, err := k.Reply()
	if err != nil {
		return nil, nil, err
	}

	if err := replyErr(r); err != nil {
		return r.GetResponse(), nil, err
	}

	data, err := io.ReadAll(k)

	return r.GetResponse(), data, err
}

// WebSocket is an open WebSocket: its call's stream carries its messages.
type WebSocket struct {
	k   *Call
	id  uint64
	seq uint64
}

// DialWebSocket opens a WebSocket. A failed handshake returns its error
// and the response.
func (c *Client) DialWebSocket(ctx context.Context, d *pb.DialWebSocket) (*WebSocket, *pb.WebSocket, error) {
	k, err := c.Open(ctx, &pb.Call{
		Method: &pb.Call_DialWebsocket{
			DialWebsocket: d,
		},
	}, true)
	if err != nil {
		return nil, nil, err
	}

	r, err := k.Reply()
	if err != nil {
		k.Close()

		return nil, nil, err
	}

	if err := replyErr(r); err != nil {
		k.Close()

		return nil, &pb.WebSocket{
			Response: r.GetResponse(),
		}, err
	}

	return &WebSocket{
		k:  k,
		id: r.GetWebsocket().GetId(),
	}, r.GetWebsocket(), nil
}

// ID is the socket's object id.
func (w *WebSocket) ID() uint64 {
	return w.id
}

// WriteMessage sends a message and waits until the core has written it
// (its WebSocketWritten notice), returning its error. One writer at a
// time.
func (w *WebSocket) WriteMessage(ctx context.Context, typ byte, data []byte) error {
	if err := convert.WriteRecord(w.k, typ, data); err != nil {
		return err
	}

	w.seq++

	c := w.k.c

	for {
		c.mu.Lock()

		got := c.written[w.id][w.seq]
		delete(c.written[w.id], w.seq)

		notice := c.notice
		c.mu.Unlock()

		if got != nil {
			if got.GetError() != nil {
				return &Error{
					Proto: got.GetError(),
				}
			}

			return nil
		}

		select {
		case <-notice:
		case <-ctx.Done():
			return ctx.Err()
		case <-c.wc.Done():
			return c.wc.Err()
		}
	}
}

// ReadMessage returns the next message; once the socket has closed, the
// error that closed it (an *Error, CODE_CLOSE after a close handshake).
func (w *WebSocket) ReadMessage() (byte, []byte, error) {
	typ, data, err := convert.ReadRecord(w.k)
	if err != nil {
		return 0, nil, err
	}

	if typ == convert.RecordEnd {
		e := &pb.Error{}
		if err := proto.Unmarshal(data, e); err != nil {
			return 0, nil, err
		}

		return 0, nil, &Error{
			Proto: e,
		}
	}

	return typ, data, nil
}

// CloseWrite says the SDK writes no more messages; the socket stays open.
func (w *WebSocket) CloseWrite() error {
	return w.k.CloseWrite()
}

// Abort resets the socket's stream, which closes it.
func (w *WebSocket) Abort() {
	w.k.Close()
}
