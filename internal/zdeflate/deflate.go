package zdeflate

import (
	"fmt"
)

// zlib's deflate parameters: window bits 15, memLevel 8.
const (
	minMatch = 3
	maxMatch = 258

	wBits      = 15
	wSize      = 1 << wBits
	wMask      = wSize - 1
	windowSize = 2 * wSize

	memLevel  = 8
	hashBits  = memLevel + 7
	hashSize  = 1 << hashBits
	hashMask  = hashSize - 1
	hashShift = (hashBits + minMatch - 1) / minMatch

	// minLookahead is the lookahead deflate keeps while input remains: a
	// longest match and the strings after it to insert.
	minLookahead = maxMatch + minMatch + 1
	maxDist      = wSize - minLookahead

	// symEnd is how many symbols a block holds before it is flushed:
	// zlib's lit_bufsize - 1, lit_bufsize being 1 << (memLevel + 6).
	symEnd = 1<<(memLevel+6) - 1

	// tooFar is the distance beyond which deflate_slow drops a match of
	// minMatch bytes.
	tooFar = 4096

	// nilPos ends a hash chain: position 0 is never matched.
	nilPos = 0
)

// config is a line of zlib's configuration_table.
type config struct {
	// goodLength: a previous match this long quarters the chain searched.
	goodLength uint32
	// maxLazy: deflate_slow looks for a better match only after a shorter
	// one; deflate_fast inserts the strings of a match no longer than this.
	maxLazy uint32
	// niceLength: a match this long ends the search.
	niceLength int
	// maxChain: the most hash chain entries searched.
	maxChain uint32
	// lazy selects deflate_slow (levels 4-9) over deflate_fast (1-3).
	lazy bool
}

// configs is zlib's configuration_table, by level; level 0 (stored) is not
// supported.
var configs = [10]config{
	1: {
		goodLength: 4,
		maxLazy:    4,
		niceLength: 8,
		maxChain:   4,
	},
	2: {
		goodLength: 4,
		maxLazy:    5,
		niceLength: 16,
		maxChain:   8,
	},
	3: {
		goodLength: 4,
		maxLazy:    6,
		niceLength: 32,
		maxChain:   32,
	},
	4: {
		goodLength: 4,
		maxLazy:    4,
		niceLength: 16,
		maxChain:   16,
		lazy:       true,
	},
	5: {
		goodLength: 8,
		maxLazy:    16,
		niceLength: 32,
		maxChain:   32,
		lazy:       true,
	},
	6: {
		goodLength: 8,
		maxLazy:    16,
		niceLength: 128,
		maxChain:   128,
		lazy:       true,
	},
	7: {
		goodLength: 8,
		maxLazy:    32,
		niceLength: 128,
		maxChain:   256,
		lazy:       true,
	},
	8: {
		goodLength: 32,
		maxLazy:    128,
		niceLength: 258,
		maxChain:   1024,
		lazy:       true,
	},
	9: {
		goodLength: 32,
		maxLazy:    258,
		niceLength: 258,
		maxChain:   4096,
		lazy:       true,
	},
}

// syncTrailer ends every sync flush: the empty stored block's LEN and NLEN.
// permessage-deflate leaves it off the wire (RFC 7692, section 7.2.1).
var syncTrailer = [4]byte{
	0x00,
	0x00,
	0xff,
	0xff,
}

// Writer compresses the messages of one permessage-deflate socket, each as
// zlib's deflate called with the whole message and Z_SYNC_FLUSH, on a
// stream opened by deflateInit2(level, Z_DEFLATED, -15, 8,
// Z_DEFAULT_STRATEGY) and kept from message to message (context takeover).
// It is not safe for concurrent use.
//
// Fields follow zlib's deflate_state, with its unsigned types (uInt and
// Pos as uint32, long as int64), so the arithmetic wraps where zlib's does.
type Writer struct {
	cfg config

	// window holds the last wSize bytes compressed and the lookahead;
	// prev links positions with the same hash, head is each hash's most
	// recent position.
	window [windowSize]byte
	prev   [wSize]uint32
	head   [hashSize]uint32
	insH   uint32

	strstart       uint32
	blockStart     int64
	lookahead      uint32
	matchStart     uint32
	matchLength    uint32
	prevLength     uint32
	prevMatch      uint32
	matchAvailable bool
	insert         uint32

	// in is the rest of the message being compressed.
	in []byte

	trees trees
	// out is the message's output, bits not yet a whole byte in bits.
	out bitWriter
}

// NewWriter returns a Writer at the zlib compression level (1-9). It panics
// on any other level.
func NewWriter(level int) *Writer {
	if level < 1 || level > 9 {
		panic(fmt.Sprintf("zdeflate: unsupported level %d", level))
	}
	w := &Writer{
		cfg: configs[level],
		// lm_init: a match of minMatch-1 is no match.
		matchLength: minMatch - 1,
		prevLength:  minMatch - 1,
	}
	w.trees.init()
	return w
}

// Message returns p compressed as one permessage-deflate message: the
// output of deflate(Z_SYNC_FLUSH) with p as its input, the sync flush's
// 00 00 ff ff cut. The compression context carries over to the next call.
//
// An empty p gives 00: the empty stored block a sync flush with no input
// writes, which leaves the context as it was.
func (w *Writer) Message(p []byte) []byte {
	if len(p) == 0 {
		return []byte{
			0x00,
		}
	}
	w.out.buf = make([]byte, 0, len(p)/2+16)
	w.in = p
	if w.cfg.lazy {
		w.deflateSlow()
	} else {
		w.deflateFast()
	}
	w.in = nil
	// The sync flush: an empty stored block.
	w.trees.storedBlock(&w.out, nil)
	out := w.out.buf
	w.out.buf = nil
	return out[:len(out)-len(syncTrailer)]
}

// updateHash is zlib's UPDATE_HASH.
func updateHash(h uint32, c byte) uint32 {
	return ((h << hashShift) ^ uint32(c)) & hashMask
}

// insertString is zlib's INSERT_STRING: it adds the string at str to its
// hash chain and returns the chain's previous head.
func (w *Writer) insertString(str uint32) uint32 {
	w.insH = updateHash(w.insH, w.window[str+minMatch-1])
	head := w.head[w.insH]
	w.prev[str&wMask] = head
	w.head[w.insH] = str
	return head
}

// slideHash is zlib's slide_hash: positions move down by wSize with the
// window, those that fall out of it become nilPos.
func (w *Writer) slideHash() {
	for i, m := range w.head {
		if m >= wSize {
			w.head[i] = m - wSize
		} else {
			w.head[i] = nilPos
		}
	}
	for i, m := range w.prev {
		if m >= wSize {
			w.prev[i] = m - wSize
		} else {
			w.prev[i] = nilPos
		}
	}
}

// fillWindow is zlib's fill_window: it slides the window when strstart
// nears its end and reads input into the lookahead.
func (w *Writer) fillWindow() {
	for {
		more := windowSize - w.lookahead - w.strstart
		if w.strstart >= wSize+maxDist {
			copy(w.window[:wSize-more], w.window[wSize:])
			w.matchStart -= wSize
			w.strstart -= wSize
			w.blockStart -= wSize
			if w.insert > w.strstart {
				w.insert = w.strstart
			}
			w.slideHash()
			more += wSize
		}
		if len(w.in) == 0 {
			break
		}
		start := w.strstart + w.lookahead
		n := copy(w.window[start:start+more], w.in)
		w.in = w.in[n:]
		w.lookahead += uint32(n) //nolint:gosec // n <= more < windowSize

		// Hash the strings the previous call left uninserted.
		if w.lookahead+w.insert >= minMatch {
			str := w.strstart - w.insert
			w.insH = uint32(w.window[str])
			w.insH = updateHash(w.insH, w.window[str+1])
			for w.insert != 0 {
				w.insH = updateHash(w.insH, w.window[str+minMatch-1])
				w.prev[str&wMask] = w.head[w.insH]
				w.head[w.insH] = str
				str++
				w.insert--
				if w.lookahead+w.insert < minMatch {
					break
				}
			}
		}
		if w.lookahead >= minLookahead || len(w.in) == 0 {
			break
		}
	}
	// zlib zeroes the bytes past the data that longest_match may read
	// (WIN_INIT); the Go window starts zeroed, and every byte it may read
	// later was written as zlib writes it, so it holds the same values.
}

// longestMatch is zlib's longest_match: the longest match for the string
// at strstart along the hash chain from curMatch, longer than prevLength,
// with matchStart set to where it starts.
func (w *Writer) longestMatch(curMatch uint32) uint32 {
	chainLength := w.cfg.maxChain
	scan := int(w.strstart)
	bestLen := int(w.prevLength)
	niceMatch := w.cfg.niceLength
	var limit uint32 = nilPos
	if w.strstart > maxDist {
		limit = w.strstart - maxDist
	}
	strend := scan + maxMatch
	scanEnd1 := w.window[scan+bestLen-1]
	scanEnd := w.window[scan+bestLen]

	if w.prevLength >= w.cfg.goodLength {
		chainLength >>= 2
	}
	if uint32(niceMatch) > w.lookahead { //nolint:gosec // niceMatch <= maxMatch
		niceMatch = int(w.lookahead)
	}

	win := &w.window
	for {
		match := int(curMatch)
		if win[match+bestLen] == scanEnd &&
			win[match+bestLen-1] == scanEnd1 &&
			win[match] == win[scan] &&
			win[match+1] == win[scan+1] {
			// scan[2] and match[2] are equal: same hash, hashBits >= 8.
			s, m := scan+2, match+2
			for {
				s++
				m++
				if win[s] != win[m] || s >= strend {
					break
				}
			}
			length := maxMatch - (strend - s)
			if length > bestLen {
				w.matchStart = curMatch
				bestLen = length
				if length >= niceMatch {
					break
				}
				scanEnd1 = win[scan+bestLen-1]
				scanEnd = win[scan+bestLen]
			}
		}
		curMatch = w.prev[curMatch&wMask]
		if curMatch <= limit {
			break
		}
		chainLength--
		if chainLength == 0 {
			break
		}
	}
	if uint32(bestLen) <= w.lookahead { //nolint:gosec // bestLen <= maxMatch
		return uint32(bestLen) //nolint:gosec // bestLen <= maxMatch
	}
	return w.lookahead
}

// flushBlock is zlib's FLUSH_BLOCK_ONLY, never the last block: the
// symbols since blockStart go out as one block.
func (w *Writer) flushBlock() {
	var stored []byte
	if w.blockStart >= 0 {
		stored = w.window[w.blockStart:w.strstart]
	}
	w.trees.flushBlock(&w.out, stored, uint64(int64(w.strstart)-w.blockStart)) //nolint:gosec // strstart >= blockStart
	w.blockStart = int64(w.strstart)
}

// deflateFast is zlib's deflate_fast (levels 1-3) with a sync flush: no
// lazy matching, and only the strings of short matches inserted.
func (w *Writer) deflateFast() {
	for {
		if w.lookahead < minLookahead {
			w.fillWindow()
			if w.lookahead == 0 {
				break
			}
		}
		var hashHead uint32 = nilPos
		if w.lookahead >= minMatch {
			hashHead = w.insertString(w.strstart)
		}
		if hashHead != nilPos && w.strstart-hashHead <= maxDist {
			w.matchLength = w.longestMatch(hashHead)
		}
		var flush bool
		if w.matchLength >= minMatch {
			flush = w.trees.tally(w.strstart-w.matchStart, w.matchLength-minMatch)
			w.lookahead -= w.matchLength
			if w.matchLength <= w.cfg.maxLazy && w.lookahead >= minMatch {
				w.matchLength-- // the string at strstart is in the table
				for {
					w.strstart++
					w.insertString(w.strstart)
					w.matchLength--
					if w.matchLength == 0 {
						break
					}
				}
				w.strstart++
			} else {
				w.strstart += w.matchLength
				w.matchLength = 0
				w.insH = uint32(w.window[w.strstart])
				w.insH = updateHash(w.insH, w.window[w.strstart+1])
			}
		} else {
			flush = w.trees.tally(0, uint32(w.window[w.strstart]))
			w.lookahead--
			w.strstart++
		}
		if flush {
			w.flushBlock()
		}
	}
	w.insert = min(w.strstart, minMatch-1)
	if w.trees.symNext != 0 {
		w.flushBlock()
	}
}

// deflateSlow is zlib's deflate_slow (levels 4-9) with a sync flush: a
// match is taken only if the next position has no longer one.
func (w *Writer) deflateSlow() {
	for {
		if w.lookahead < minLookahead {
			w.fillWindow()
			if w.lookahead == 0 {
				break
			}
		}
		var hashHead uint32 = nilPos
		if w.lookahead >= minMatch {
			hashHead = w.insertString(w.strstart)
		}

		w.prevLength, w.prevMatch = w.matchLength, w.matchStart
		w.matchLength = minMatch - 1
		if hashHead != nilPos && w.prevLength < w.cfg.maxLazy && w.strstart-hashHead <= maxDist {
			w.matchLength = w.longestMatch(hashHead)
			if w.matchLength == minMatch && w.strstart-w.matchStart > tooFar {
				// Too far for 3 bytes; if prevMatch is also minMatch
				// long, matchStart is garbage but the match is ignored.
				w.matchLength = minMatch - 1
			}
		}

		switch {
		case w.prevLength >= minMatch && w.matchLength <= w.prevLength:
			// The previous match is no shorter: take it.
			maxInsert := w.strstart + w.lookahead - minMatch
			flush := w.trees.tally(w.strstart-1-w.prevMatch, w.prevLength-minMatch)
			// Insert the strings up to the end of the match; strstart-1
			// and strstart are in already, and none past maxInsert.
			w.lookahead -= w.prevLength - 1
			w.prevLength -= 2
			for {
				w.strstart++
				if w.strstart <= maxInsert {
					w.insertString(w.strstart)
				}
				w.prevLength--
				if w.prevLength == 0 {
					break
				}
			}
			w.matchAvailable = false
			w.matchLength = minMatch - 1
			w.strstart++
			if flush {
				w.flushBlock()
			}
		case w.matchAvailable:
			// No match at the previous position, or a longer one here:
			// the previous byte is a literal.
			if w.trees.tally(0, uint32(w.window[w.strstart-1])) {
				w.flushBlock()
			}
			w.strstart++
			w.lookahead--
		default:
			// Nothing to compare with yet: decide at the next position.
			w.matchAvailable = true
			w.strstart++
			w.lookahead--
		}
	}
	if w.matchAvailable {
		w.trees.tally(0, uint32(w.window[w.strstart-1]))
		w.matchAvailable = false
	}
	w.insert = min(w.strstart, minMatch-1)
	if w.trees.symNext != 0 {
		w.flushBlock()
	}
}
