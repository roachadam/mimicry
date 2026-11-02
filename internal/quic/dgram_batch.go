package quic

// mimicry: a read's datagrams go to a Dial endpoint's connection in one
// message (UPSTREAM.md change 26). The loop still handles them one at a
// time, with its send pass and timers between them, as if each had come
// alone.

import "sync"

// A datagramBatch is the datagrams of one read, in the order they arrived;
// next is the first the loop has not taken.
type datagramBatch struct {
	ds   []*datagram
	next int
}

var datagramBatchPool = sync.Pool{
	New: func() any {
		return &datagramBatch{
			ds: make([]*datagram, 0, 8),
		}
	},
}

// newDatagramBatch is a batch of ds, which it takes; the slice stays the
// caller's.
func newDatagramBatch(ds []*datagram) *datagramBatch {
	b := datagramBatchPool.Get().(*datagramBatch) //nolint:forcetypeassert // the pool holds only this type

	b.ds = append(b.ds[:0], ds...)
	b.next = 0

	return b
}

// take returns the next datagram, and whether more follow it. The batch
// goes back to the pool with its last datagram: the caller must not use it
// after more is false.
func (b *datagramBatch) take() (d *datagram, more bool) {
	d = b.ds[b.next]
	b.ds[b.next] = nil
	b.next++

	if b.next < len(b.ds) {
		return d, true
	}

	b.ds = b.ds[:0]
	datagramBatchPool.Put(b)

	return d, false
}

// recycle returns the datagrams not taken, and the batch, to their pools.
func (b *datagramBatch) recycle() {
	for _, d := range b.ds[b.next:] {
		d.recycle()
	}

	clear(b.ds)

	b.ds = b.ds[:0]
	datagramBatchPool.Put(b)
}

// handleSoleDatagrams gives a read's datagrams to the endpoint's only
// connection: one alone as it is, more in one batch.
func (e *Endpoint) handleSoleDatagrams(ms []*datagram) {
	c := e.soleConn.Load()
	if c == nil {
		for _, m := range ms {
			m.recycle()
		}

		return
	}

	switch len(ms) {
	case 0:
	case 1:
		c.sendMsg(ms[0])
	default:
		c.sendMsg(newDatagramBatch(ms))
	}
}
