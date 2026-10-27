package scenario

import (
	"math/bits"
	"time"
)

// hist is a log-linear latency histogram (64 sub-buckets per power of two,
// under 1.6% error), fixed-size so recording never allocates.
type hist struct {
	counts [histBuckets]uint64
	n      uint64
}

const histBuckets = 128 + 40*64

func histIndex(v uint64) int {
	if v < 128 {
		return int(v)
	}

	e := bits.Len64(v) - 7
	m := v >> e

	i := 128 + (e-1)*64 + int(m-64)
	if i >= histBuckets {
		return histBuckets - 1
	}

	return i
}

// histValue returns the middle of bucket i.
func histValue(i int) uint64 {
	if i < 128 {
		return uint64(i)
	}

	k := i - 128
	e := k/64 + 1
	m := uint64(k%64 + 64)

	return m<<e + (1<<e)/2
}

func (h *hist) record(d time.Duration) {
	if d < 0 {
		d = 0
	}

	h.counts[histIndex(uint64(d))]++

	h.n++
}

func (h *hist) merge(o *hist) {
	for i, c := range o.counts {
		h.counts[i] += c
	}

	h.n += o.n
}

// quantile returns the q-quantile (0 < q <= 1).
func (h *hist) quantile(q float64) time.Duration {
	if h.n == 0 {
		return 0
	}

	rank := uint64(q * float64(h.n))
	if rank == 0 {
		rank = 1
	}

	var seen uint64

	for i, c := range h.counts {
		seen += c
		if seen >= rank {
			return time.Duration(histValue(i))
		}
	}

	return time.Duration(histValue(histBuckets - 1))
}
