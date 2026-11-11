package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/roachadam/mimicry/internal/rpc/convert"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/internal/rpc/wire"
)

// defaultDialTimeout is mimicry's Config.DialTimeout when zero, which
// bounds a handshake and so the SDK's answer within it.
const defaultDialTimeout = 30 * time.Second

// errNoWaiter ends a VerifyConnection call no request waits for any more:
// each ended, by its deadline, a RESET or the connection's end.
var errNoWaiter = errors.New("no request of the transport waits for it any more")

// verifier is a transport's VerifyConnection: the state of each verified
// connection goes to the SDK as a reverse call (Event.verify_connection),
// and the handshake waits for its answer. tls.Config's VerifyConnection
// gets no context, so the wait is bounded by what it may hold up: the
// handshake's DialTimeout, and the transport's requests that are still
// waiting (a request's deadline or RESET ends its wait). A RESET of the
// call or the connection's end fails the handshake.
type verifier struct {
	s *session
	// transport is the transport's id, set before any request runs.
	transport uint64
	timeout   time.Duration

	mu   sync.Mutex
	live int
	// idle is closed while no request of the transport is live.
	idle chan struct{}
}

func newVerifier(s *session, dialTimeout time.Duration) *verifier {
	if dialTimeout <= 0 {
		dialTimeout = defaultDialTimeout
	}

	idle := make(chan struct{})
	close(idle)

	return &verifier{
		s:       s,
		timeout: dialTimeout,
		idle:    idle,
	}
}

// waiting counts a request of the transport, under ctx, as live until ctx
// ends or the returned func is called, whichever is first.
func (v *verifier) waiting(ctx context.Context) func() {
	v.mu.Lock()

	if v.live == 0 {
		v.idle = make(chan struct{})
	}

	v.live++
	v.mu.Unlock()

	var once sync.Once

	end := func() {
		once.Do(func() {
			v.mu.Lock()
			defer v.mu.Unlock()

			v.live--
			if v.live == 0 {
				close(v.idle)
			}
		})
	}
	stop := context.AfterFunc(ctx, end)

	return func() {
		stop()
		end()
	}
}

// verify asks the SDK about cs: Config.VerifyConnection. It runs on the
// handshake's goroutine, mimicry's: a panic fails the handshake with
// errInternal instead of ending the process.
func (v *verifier) verify(cs tls.ConnectionState) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("mimicry-core: VerifyConnection: %w", errInternal)
		}
	}()

	checkpoint("verify")

	v.mu.Lock()

	idle := v.idle
	v.mu.Unlock()

	ctx, cancel := context.WithCancelCause(v.s.ctx)
	defer cancel(nil)

	ctx, cancelTimeout := context.WithTimeout(ctx, v.timeout)
	defer cancelTimeout()

	go func() {
		select {
		case <-idle:
			cancel(errNoWaiter)
		case <-ctx.Done():
		}
	}()

	b, err := proto.Marshal(&pb.Event{
		Event: &pb.Event_VerifyConnection{
			VerifyConnection: &pb.VerifyConnection{
				Transport: v.transport,
				State:     convert.ConnectionState(&cs),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("mimicry-core: VerifyConnection: %w", err)
	}

	answer, err := v.ask(ctx, b)
	if err != nil {
		if ctx.Err() != nil {
			err = context.Cause(ctx)
		}

		return fmt.Errorf("mimicry-core: VerifyConnection unanswered: %w", err)
	}

	r := &pb.EventReply{}
	if err := proto.Unmarshal(answer, r); err != nil {
		return fmt.Errorf("mimicry-core: VerifyConnection: a malformed answer: %w", err)
	}

	if e := r.GetError(); e != nil {
		return &convert.VerifyError{
			Message: e.GetMessage(),
		}
	}

	return nil
}

// ask opens a reverse call with the event b, continued as DATA when too
// large for a frame, and returns the SDK's REPLY.
func (v *verifier) ask(ctx context.Context, b []byte) ([]byte, error) {
	var (
		st  *wire.Stream
		err error
	)

	if len(b) <= wire.MaxPayload {
		st, err = v.s.wc.Open(ctx, b, true)
	} else {
		head, _ := proto.Marshal(&pb.Event{
			Continued: uint32(len(b)),
		})
		if st, err = v.s.wc.Open(ctx, head, false); err == nil {
			if _, err = st.Write(b); err == nil {
				err = st.CloseWrite()
			}
		}
	}

	if st != nil {
		defer closeStream(st)
	}

	if err != nil {
		return nil, err
	}

	return st.Reply()
}
