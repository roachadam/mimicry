// Package server is mimicry-core's server (decision 0069): it answers one
// SDK's calls on one connection of the wire protocol (internal/rpc/wire),
// their messages those of internal/rpc/pb, through mimicry's public API.
//
// Every object a call creates (client, transport, jar, caches, store,
// profile, WebSocket) is the connection's, named by an id, and freed by a
// Close call or when the connection ends; the server keeps nothing
// global. Every stream it accepts it ends or resets, and Serve returns
// only once every call has.
package server

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

// Config configures a server.
type Config struct {
	// Version is the core's version, sent in HelloReply.
	Version string
}

// maxContinued bounds a call or reply sent as DATA (Call.continued): more
// than any call needs, little enough that a bad length fails at once.
const maxContinued = 64 << 20

// errInternal is the error of a call that panicked: a bug of the core's,
// told without anything of the call's (the panic's value may hold its
// data) and never written to stderr.
var errInternal = errors.New("mimicry-core: internal error")

// session is one connection and the objects it owns.
type session struct {
	cfg Config
	wc  *wire.Conn
	// ctx ends when the connection does.
	ctx     context.Context
	objects objects
	calls   sync.WaitGroup
}

// Serve answers the calls on rwc until the SDK disconnects (nil), ctx
// ends or the connection fails. Hello must be the first call. When it
// returns, every call has ended and every object the connection owned is
// freed.
func Serve(ctx context.Context, rwc io.ReadWriteCloser, cfg Config) error {
	wc, err := wire.NewConn(rwc, wire.Config{
		Role: wire.RoleCore,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(ctx, func() {
		_ = wc.Close()
	})
	defer stop()

	s := &session{
		cfg: cfg,
		wc:  wc,
		ctx: ctx,
	}

	err = s.serve()

	cancel()

	_ = wc.Close()

	s.calls.Wait()
	s.objects.freeAll()

	return err
}

// serve accepts calls until the connection ends.
func (s *session) serve() error {
	st, err := s.wc.Accept(s.ctx)
	if err != nil {
		return s.ended(err)
	}

	if err := s.hello(st); err != nil {
		return err
	}

	for {
		st, err := s.wc.Accept(s.ctx)
		if err != nil {
			return s.ended(err)
		}

		s.calls.Go(func() {
			s.handle(st)
		})
	}
}

// ended is Serve's result for the connection's end err: nil for the SDK
// closing it between frames.
func (s *session) ended(err error) error {
	if s.ctx.Err() != nil {
		return context.Cause(s.ctx)
	}

	// The wire fails a connection that ended between frames with
	// ErrConnClosed itself; any other failure wraps it with its cause.
	if err == wire.ErrConnClosed { //nolint:errorlint // the sentinel itself, not one wrapping it
		return nil
	}

	return err
}

// hello answers the first call, which must be Hello with this protocol's
// major version.
func (s *session) hello(st *wire.Stream) error {
	defer closeStream(st)

	c, err := readCall(st)
	if err != nil {
		_ = s.replyErr(st, err)

		return err
	}

	h := c.GetHello()
	if h == nil {
		err := convert.Invalid("the first call must be Hello, not %T", c.GetMethod())

		_ = s.replyErr(st, err)

		return err
	}

	r := &pb.Reply{
		Result: &pb.Reply_Hello{
			Hello: &pb.HelloReply{
				ProtocolMajor: pb.ProtocolMajor,
				ProtocolMinor: pb.ProtocolMinor,
				CoreVersion:   s.cfg.Version,
			},
		},
	}

	if h.GetProtocolMajor() != pb.ProtocolMajor {
		err := convert.Invalid("the SDK (%s) speaks protocol %d.%d, mimicry-core %s speaks %d.%d",
			h.GetSdkVersion(), h.GetProtocolMajor(), h.GetProtocolMinor(), s.cfg.Version, pb.ProtocolMajor, pb.ProtocolMinor)

		r.Error = convert.Error(err)
		_ = s.reply(st, r, true)

		return err
	}

	return s.reply(st, r, true)
}

// closeStream ends st, resetting it if either side has not ended it: the
// last thing done with every stream.
func closeStream(st *wire.Stream) {
	_ = st.Close()
}

// handle answers one call and ends its stream.
func (s *session) handle(st *wire.Stream) {
	defer closeStream(st)
	defer s.recovered(st)

	c, err := readCall(st)
	if err != nil {
		_ = s.replyErr(st, err)

		return
	}

	switch m := c.GetMethod().(type) {
	case *pb.Call_Do:
		s.do(st, m.Do)
	case *pb.Call_DialWebsocket:
		s.dialWebSocket(st, m.DialWebsocket)
	case *pb.Call_WriteJson, *pb.Call_WriteNetscape, *pb.Call_ProfileSpec:
		r, body, err := s.bodyCall(c)
		if err != nil {
			_ = s.replyErr(st, err)

			return
		}

		if s.reply(st, r, false) != nil {
			return
		}

		if _, err := st.Write(body); err != nil {
			return
		}

		_ = st.CloseWrite()

	default:
		r, err := s.call(st, c)
		if err != nil {
			_ = s.replyErr(st, err)

			return
		}

		_ = s.reply(st, r, true)
	}
}

// recovered answers a call that panicked with errInternal (CODE_INTERNAL):
// its REPLY or, once that is sent, a StreamError notice and a RESET. The
// connection and its other calls go on.
func (s *session) recovered(st *wire.Stream) {
	if recover() == nil {
		return
	}

	if s.replyErr(st, errInternal) != nil {
		s.failStream(st, errInternal)
	}
}

// readCall reads a stream's call, from its CALL or, when continued, the
// first bytes of its DATA.
func readCall(st *wire.Stream) (*pb.Call, error) {
	c := &pb.Call{}
	if err := proto.Unmarshal(st.Payload(), c); err != nil {
		return nil, convert.Invalid("a malformed call: %v", err)
	}

	n := c.GetContinued()
	if n == 0 {
		return c, nil
	}

	if n > maxContinued {
		return nil, convert.Invalid("a %d-byte call", n)
	}

	buf := make([]byte, n)
	if _, err := io.ReadFull(st, buf); err != nil {
		return nil, fmt.Errorf("mimicry-core: reading a continued call: %w", err)
	}

	c = &pb.Call{}
	if err := proto.Unmarshal(buf, c); err != nil {
		return nil, convert.Invalid("a malformed call: %v", err)
	}

	if c.GetContinued() != 0 {
		return nil, convert.Invalid("a continued call continued again")
	}

	if carriesBody(c) {
		return c, nil
	}

	// Nothing follows the call but the SDK's END, which may arrive after
	// the call is answered: wait for it, or ending the stream then would
	// find its receiving side open and reset it, failing the reply.
	var one [1]byte

	switch n, err := st.Read(one[:]); {
	case n > 0:
		return nil, convert.Invalid("DATA after a call that takes none")
	case !errors.Is(err, io.EOF):
		return nil, fmt.Errorf("mimicry-core: reading a continued call's end: %w", err)
	}

	return c, nil
}

// carriesBody reports a call whose stream carries DATA after it from the
// SDK (proto/README.md, Bodies).
func carriesBody(c *pb.Call) bool {
	switch m := c.GetMethod().(type) {
	case *pb.Call_Do:
		return m.Do.GetRequest().GetStreamBody()
	case *pb.Call_DialWebsocket, *pb.Call_ParseProfile, *pb.Call_ReadJson, *pb.Call_ReadNetscape:
		return true
	}

	return false
}

// reply sends r, as DATA after a continued REPLY when it is too large for
// one frame; end ends the stream's DATA after it.
func (s *session) reply(st *wire.Stream, r *pb.Reply, end bool) error {
	b, err := proto.Marshal(r)
	if err != nil {
		b, _ = proto.Marshal(&pb.Reply{
			Error: convert.Error(fmt.Errorf("mimicry-core: encoding the reply: %w", err)),
		})
	}

	if len(b) <= wire.MaxPayload {
		return st.WriteReply(b, end)
	}

	head, _ := proto.Marshal(&pb.Reply{
		Continued: uint32(len(b)),
	})
	if err := st.WriteReply(head, false); err != nil {
		return err
	}

	if _, err := st.Write(b); err != nil {
		return err
	}

	if end {
		return st.CloseWrite()
	}

	return nil
}

// replyErr answers with err alone.
func (s *session) replyErr(st *wire.Stream, err error) error {
	return s.reply(st, &pb.Reply{
		Error: convert.Error(err),
	}, true)
}

// notify sends a notice (EVENT on stream 0); one too large for a frame
// is dropped, as no notice the core sends is (SetCookie cuts its line).
func (s *session) notify(e *pb.Event) {
	b, err := proto.Marshal(e)
	if err != nil || len(b) > wire.MaxPayload {
		return
	}

	_ = s.wc.Notify(s.ctx, b)
}

// failStream says why st fails (a StreamError notice), then resets it:
// for a call whose REPLY is sent, so its error cannot go there.
func (s *session) failStream(st *wire.Stream, err error) {
	s.notify(&pb.Event{
		Event: &pb.Event_StreamError{
			StreamError: &pb.StreamError{
				Stream: st.ID(),
				Error:  convert.Error(err),
			},
		},
	})

	_ = st.Reset(wire.CodeCancel)
}
