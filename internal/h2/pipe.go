package h2

import (
	"errors"
	"io"
	"sync"
)

var errBodyClosed = errors.New("h2: response body closed")

// pipe buffers a stream's response body between the read loop and the
// caller; the stream's receive window bounds it. Every buffered byte leaves
// once: read (Conn.consumed), or discarded by Close or an error end, which
// report how many so the connection returns that credit.
type pipe struct {
	mu     sync.Mutex
	cond   sync.Cond
	buf    chunkBuffer
	err    error // set when the stream ends; io.EOF once buf is drained
	closed bool  // the caller closed the body
	// read counts bytes handed to the caller, written bytes buffered;
	// expected is the response's Content-Length, or -1.
	read     int64
	written  int64
	expected int64
	// s is the stream whose body this is. Reads return its credit, and
	// the first Close tells its connection (Conn.bodyClosed): complete
	// means every declared byte was read, discarded is how many buffered
	// bytes nobody read.
	s *stream
}

// init readies p, which is s's body, in place: it is part of s, so a
// stream costs one allocation for both.
func (p *pipe) init(s *stream) {
	p.cond.L = &p.mu
	p.expected = -1
	p.s = s
	p.buf.chunks = p.buf.inline[:0]
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

	for p.buf.size == 0 && p.err == nil && !p.closed {
		p.cond.Wait()
	}

	if p.closed {
		p.mu.Unlock()

		return 0, errBodyClosed
	}

	if p.buf.size > 0 {
		n := p.buf.read(b)

		p.read += int64(n)
		p.mu.Unlock()
		p.s.c.consumed(p.s, n)

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

	discarded := p.buf.size
	complete := p.expected >= 0 && p.read == p.expected && discarded == 0
	p.buf.reset()
	p.cond.Broadcast()
	p.mu.Unlock()
	p.s.c.bodyClosed(p.s, complete, discarded)

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

	// Size new chunks for what the response still declares, so a small
	// body takes one small chunk.
	want := int64(len(b))
	if rest := p.expected - p.written; rest > want {
		want = rest
	}

	p.buf.write(b, want)

	p.written += int64(len(b))
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
		discarded = p.buf.size
		p.buf.reset()
	}

	p.cond.Broadcast()

	return discarded
}

// chunkBuffer is a FIFO of bytes held in pooled chunks: each goes back to
// its pool once read past or discarded, so a response body costs no
// allocation once the pools are warm. Its callers hold the pipe's lock.
type chunkBuffer struct {
	chunks [][]byte
	// inline is chunks' first array: a body that fits two chunks, as most
	// do, grows no slice.
	inline [2][]byte
	r      int // next byte to read is chunks[0][r]
	w      int // next byte to write is chunks[len(chunks)-1][w]
	size   int // bytes buffered
}

// The chunk sizes, smallest first; the largest is a default DATA frame.
const (
	chunk1K  = 1 << 10
	chunk2K  = 2 << 10
	chunk4K  = 4 << 10
	chunk8K  = 8 << 10
	chunk16K = 16 << 10
)

// The pools hold array pointers so that putting a chunk back allocates
// nothing.
var (
	chunkPool1K = sync.Pool{
		New: func() any {
			return new([chunk1K]byte)
		},
	}
	chunkPool2K = sync.Pool{
		New: func() any {
			return new([chunk2K]byte)
		},
	}
	chunkPool4K = sync.Pool{
		New: func() any {
			return new([chunk4K]byte)
		},
	}
	chunkPool8K = sync.Pool{
		New: func() any {
			return new([chunk8K]byte)
		},
	}
	chunkPool16K = sync.Pool{
		New: func() any {
			return new([chunk16K]byte)
		},
	}
)

// getChunk returns the smallest chunk that holds want bytes, or the largest.
func getChunk(want int64) []byte {
	switch {
	case want <= chunk1K:
		return chunkPool1K.Get().(*[chunk1K]byte)[:] //nolint:forcetypeassert // the pool holds only this type
	case want <= chunk2K:
		return chunkPool2K.Get().(*[chunk2K]byte)[:] //nolint:forcetypeassert // the pool holds only this type
	case want <= chunk4K:
		return chunkPool4K.Get().(*[chunk4K]byte)[:] //nolint:forcetypeassert // the pool holds only this type
	case want <= chunk8K:
		return chunkPool8K.Get().(*[chunk8K]byte)[:] //nolint:forcetypeassert // the pool holds only this type
	default:
		return chunkPool16K.Get().(*[chunk16K]byte)[:] //nolint:forcetypeassert // the pool holds only this type
	}
}

// putChunk returns a chunk from getChunk to its pool.
func putChunk(c []byte) {
	switch len(c) {
	case chunk1K:
		chunkPool1K.Put((*[chunk1K]byte)(c))
	case chunk2K:
		chunkPool2K.Put((*[chunk2K]byte)(c))
	case chunk4K:
		chunkPool4K.Put((*[chunk4K]byte)(c))
	case chunk8K:
		chunkPool8K.Put((*[chunk8K]byte)(c))
	case chunk16K:
		chunkPool16K.Put((*[chunk16K]byte)(c))
	}
}

// write appends p, taking new chunks sized for want bytes (at least len(p)).
func (cb *chunkBuffer) write(p []byte, want int64) {
	for len(p) > 0 {
		if len(cb.chunks) == 0 || cb.w == len(cb.chunks[len(cb.chunks)-1]) {
			cb.chunks = append(cb.chunks, getChunk(want))
			cb.w = 0
		}

		n := copy(cb.chunks[len(cb.chunks)-1][cb.w:], p)

		cb.w += n
		cb.size += n

		want -= int64(n)

		p = p[n:]
	}
}

// read moves up to len(b) buffered bytes into b, as bytes.Buffer does, and
// returns each chunk it empties to its pool.
func (cb *chunkBuffer) read(b []byte) int {
	var n int

	for n < len(b) && cb.size > 0 {
		c := cb.chunks[0]

		end := len(c)
		if len(cb.chunks) == 1 {
			end = cb.w
		}

		m := copy(b[n:], c[cb.r:end])

		n += m
		cb.r += m

		cb.size -= m

		if cb.r == end {
			cb.pop()
		}
	}

	return n
}

// pop returns the first chunk to its pool.
func (cb *chunkBuffer) pop() {
	putChunk(cb.chunks[0])

	last := len(cb.chunks) - 1
	copy(cb.chunks, cb.chunks[1:])

	cb.chunks[last] = nil
	cb.chunks = cb.chunks[:last]
	cb.r = 0

	if last == 0 {
		cb.w = 0
	}
}

// reset discards everything buffered.
func (cb *chunkBuffer) reset() {
	for _, c := range cb.chunks {
		putChunk(c)
	}

	clear(cb.chunks)

	cb.chunks = cb.chunks[:0]
	cb.r, cb.w, cb.size = 0, 0, 0
}
