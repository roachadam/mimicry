package h2

import (
	"bytes"
	"errors"
	"io"
	"sync"
)

var errBodyClosed = errors.New("h2: response body closed")

// pipe buffers a stream's response body between the read loop and the
// caller; the stream's receive window bounds it. Every buffered byte leaves
// once: read (onRead), or discarded by Close or an error end, which report
// how many so the connection returns that credit.
type pipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	err    error // set when the stream ends; io.EOF once buf is drained
	closed bool  // the caller closed the body
	// read counts bytes handed to the caller; expected is the response's
	// Content-Length, or -1.
	read     int64
	expected int64
	onRead   func(n int)
	// onClose runs once, when the caller closes the body. complete means
	// every declared byte was read; discarded is how many buffered bytes
	// nobody read.
	onClose func(complete bool, discarded int)
}

func newPipe(onRead func(n int), onClose func(complete bool, discarded int)) *pipe {
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

// Close implements io.Closer: it discards what is buffered and, the first
// time, calls onClose.
func (p *pipe) Close() error {
	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()

		return nil
	}

	p.closed = true

	discarded := p.buf.Len()
	complete := p.expected >= 0 && p.read == p.expected && discarded == 0
	p.buf.Reset()
	p.cond.Broadcast()
	p.mu.Unlock()
	p.onClose(complete, discarded)

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

// end ends the body with err, the first time. io.EOF leaves the buffered
// bytes to be read first; any other error is returned at once and discards
// them, and end reports how many.
func (p *pipe) end(err error) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.err != nil {
		return 0
	}

	p.err = err

	var discarded int

	if !errors.Is(err, io.EOF) {
		discarded = p.buf.Len()
		p.buf.Reset()
	}

	p.cond.Broadcast()

	return discarded
}
