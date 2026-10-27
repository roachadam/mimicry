package h2

import (
	"context"
	"errors"
	"net/http"

	"golang.org/x/net/http2"
)

// errStreamReleased ends a stream the caller let go of without telling the
// peer (Stream.Release).
var errStreamReleased = errors.New("h2: stream released")

// ConnectProtocol reports whether the peer sent
// SETTINGS_ENABLE_CONNECT_PROTOCOL 1, allowing extended CONNECT (RFC 8441).
func (c *Conn) ConnectProtocol() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectProtocol
}

// Stream is an extended CONNECT stream: a byte stream in DATA frames both
// ways, the server's in its response's Body.
type Stream struct {
	c *Conn
	s *stream
}

// Connect opens an extended CONNECT stream (RFC 8441) for req, with
// :protocol after the profile's pseudo-headers, and returns the response
// once its headers arrive. A 2xx response comes with the stream, which
// outlives ctx until it is ended; any other response has no stream and its
// body is read as RoundTrip's. Errors are those of RoundTrip.
func (c *Conn) Connect(ctx context.Context, req *http.Request, protocol string) (*http.Response, *Stream, error) {
	head, err := c.head(req, 0, protocol)
	if err != nil {
		return nil, nil, err
	}
	s, err := c.newStream(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := c.writeHeaders(s, head); err != nil {
		c.endStream(s, ending{
			err: err,
		})
		r := <-s.resp
		return nil, nil, r.err
	}
	r := <-s.resp
	if r.err != nil {
		return nil, nil, r.err
	}
	r.resp.Request = req
	if r.resp.StatusCode < 200 || r.resp.StatusCode > 299 {
		return r.resp, nil, nil
	}
	// From here on, only the stream's own end ends it.
	c.mu.Lock()
	s.stopCtx()
	c.mu.Unlock()
	return r.resp, &Stream{
		c: c,
		s: s,
	}, nil
}

// Read reads what the peer sent on the stream: its response's Body.
func (st *Stream) Read(p []byte) (int, error) {
	return st.s.body.Read(p)
}

// Send writes each part in DATA frames of its own, cut at the profile's
// DATA cap and the flow-control windows; end sets END_STREAM on the last.
func (st *Stream) Send(ctx context.Context, parts [][]byte, end bool) error {
	for i, p := range parts {
		if err := st.c.sendData(ctx, st.s, p, end && i == len(parts)-1); err != nil {
			return err
		}
	}
	return nil
}

// Close resets the stream (RST_STREAM NO_ERROR), unless both sides ended
// it already.
func (st *Stream) Close() error {
	st.c.reset(st.s, http2.ErrCodeNo, errStreamClosed)
	return nil
}

// Release forgets the stream without sending a frame: for a stream the
// peer ended, when the profile does not describe how the browser ends its.
func (st *Stream) Release() {
	st.c.endStream(st.s, ending{
		err: errStreamReleased,
	})
}

// MaxData is the most one DATA frame carries now: the profile's cap, or
// the peer's frame size if smaller.
func (st *Stream) MaxData() int {
	st.c.mu.Lock()
	defer st.c.mu.Unlock()
	limit := st.c.maxFrameSize
	if m := st.c.h2.MaxDataFrame; m > 0 {
		limit = min(limit, m)
	}
	return int(limit)
}
