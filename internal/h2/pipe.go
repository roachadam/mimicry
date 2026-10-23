package h2

import (
	"bytes"
	"errors"
	"sync"
)

var errBodyClosed = errors.New("h2: response body closed")

// pipe buffers a stream's response body between the read loop and the
// caller. Flow control bounds how much the server can send ahead of reads.
type pipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	err    error // set when the stream ends; returned once buf is drained
	ended  bool  // the stream ended (err is set)
	closed bool  // the caller closed the body
	// read counts bytes handed to the caller; expected is the response's
	// Content-Length, or -1.
	read     int64
	expected int64
	onRead   func(n int)
	// onClose runs when the caller closes the body before the stream ended.
	// complete reports that every declared byte was read, so only the peer's
	// END_STREAM is outstanding.
	onClose func(complete bool)
}

func newPipe(onRead func(n int), onClose func(complete bool)) *pipe {
	p := &pipe{
		expected: -1,
		onRead:   onRead,
		onClose:  onClose,
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// setExpected records the response's Content-Length (-1 when unknown).
func (p *pipe) setExpected(n int64) {
	p.mu.Lock()
	p.expected = n
	p.mu.Unlock()
}

// Read implements io.Reader for the response body.
func (p *pipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	for p.buf.Len() == 0 && p.err == nil && !p.closed {
		p.cond.Wait()
	}
	if p.closed {
		p.mu.Unlock()
		return 0, errBodyClosed
	}
	if p.buf.Len() > 0 {
		n, _ := p.buf.Read(b)
		p.read += int64(n)
		p.mu.Unlock()
		p.onRead(n)
		return n, nil
	}
	err := p.err
	p.mu.Unlock()
	return 0, err
}

// Close implements io.Closer. Closing before the stream ends calls onClose.
func (p *pipe) Close() error {
	p.mu.Lock()
	already, ended := p.closed, p.ended
	complete := p.expected >= 0 && p.read == p.expected && p.buf.Len() == 0
	p.closed = true
	p.buf.Reset()
	p.cond.Broadcast()
	p.mu.Unlock()
	if !already && !ended {
		p.onClose(complete)
	}
	return nil
}

// write buffers b for the caller. It reports false when the body is closed
// or ended and b was dropped, so the caller returns its flow-control credit.
func (p *pipe) write(b []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.err != nil {
		return false
	}
	p.buf.Write(b)
	p.cond.Broadcast()
	return true
}

func (p *pipe) closeWithError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ended {
		p.err, p.ended = err, true
	}
	p.cond.Broadcast()
}
