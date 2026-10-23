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
	mu      sync.Mutex
	cond    *sync.Cond
	buf     bytes.Buffer
	err     error // set when the stream ends; returned once buf is drained
	ended   bool  // the stream ended (err is set)
	closed  bool  // the caller closed the body
	onRead  func(n int)
	onClose func()
}

func newPipe(onRead func(n int), onClose func()) *pipe {
	p := &pipe{
		onRead:  onRead,
		onClose: onClose,
	}
	p.cond = sync.NewCond(&p.mu)
	return p
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
		p.mu.Unlock()
		p.onRead(n)
		return n, nil
	}
	err := p.err
	p.mu.Unlock()
	return 0, err
}

// Close implements io.Closer. Closing before the stream ends resets it.
func (p *pipe) Close() error {
	p.mu.Lock()
	already, ended := p.closed, p.ended
	p.closed = true
	p.buf.Reset()
	p.cond.Broadcast()
	p.mu.Unlock()
	if !already && !ended {
		p.onClose()
	}
	return nil
}

func (p *pipe) write(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.err != nil {
		return
	}
	p.buf.Write(b)
	p.cond.Broadcast()
}

func (p *pipe) closeWithError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ended {
		p.err, p.ended = err, true
	}
	p.cond.Broadcast()
}
