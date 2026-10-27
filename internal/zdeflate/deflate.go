// Package zdeflate is permessage-deflate (RFC 7692) as the browsers write
// it. Writer reproduces byte for byte the deflate of zlib (Safari),
// Chromium's zlib (Chrome) or zlib-rs (Firefox), keeping the context from
// message to message until Reset. Reader inflates a peer's messages with
// compress/flate. Raw window bits are 9 to 15: every library rejects 8.
//
// This package is an altered version of zlib, Chromium's zlib and zlib-rs,
// translated to Go and reduced to deflate; it is not the original software.
// Their notices are in LICENSE-zlib, LICENSE-chromium and LICENSE-zlib-rs.
package zdeflate

import (
	"encoding/binary"
	"fmt"
)

// Library is the deflate implementation a Writer reproduces.
type Library int

const (
	// Zlib is zlib 1.3.1 (Safari). 1.3.2's deflate writes the same bytes.
	Zlib Library = iota
	// ZlibRS is zlib-rs 0.6.3 (Firefox 158, through its libz-rs-sys C API).
	ZlibRS
	// Chromium is Chromium's zlib (third_party/zlib at Chrome 155: zlib
	// 1.3.2 with Chromium's changes, built for x64 or arm64).
	Chromium
)

// Parameters shared by the libraries' deflate: memLevel 8, the window
// bits each stream's own.
const (
	minMatch = 3
	maxMatch = 258

	// maxWBits is the largest window: 32 KiB.
	maxWBits = 15

	memLevel  = 8
	hashBits  = memLevel + 7
	hashSize  = 1 << hashBits
	hashMask  = hashSize - 1
	hashShift = (hashBits + minMatch - 1) / minMatch

	// minLookahead is the lookahead deflate keeps while input remains: a
	// longest match and the strings after it to insert.
	minLookahead = maxMatch + minMatch + 1

	// symEnd is how many symbols a block holds before it is flushed:
	// lit_bufsize - 1, lit_bufsize being 1 << (memLevel + 6) in both.
	symEnd = 1<<(memLevel+6) - 1

	// tooFar is the distance beyond which zlib's deflate_slow drops a
	// match of minMatch bytes.
	tooFar = 4096

	// nilPos ends a hash chain: position 0 is never matched.
	nilPos = 0

	// windowPadding is Chromium's WINDOW_PADDING: bytes past each half of
	// the window.
	windowPadding = 8
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

// Writer compresses the messages of one permessage-deflate socket as its
// library's deflate with Z_SYNC_FLUSH, on a raw stream at memLevel 8 and
// the default strategy. The context carries over until Reset. It is not
// safe for concurrent use.
type Writer struct {
	c compressor
}

// compressor is one library's deflate stream.
type compressor interface {
	// message is a non-empty p deflated with Z_SYNC_FLUSH, the sync
	// flush's trailer included.
	message(p []byte) []byte
	// reset is deflateReset.
	reset()
}

// NewWriter returns a Writer for lib's deflateInit2(level, Z_DEFLATED,
// -windowBits, 8, Z_DEFAULT_STRATEGY). It fails where the library rejects
// the arguments, and for a level the port lacks: zlib and Chromium cover
// 1-9, zlib-rs only 6 (Firefox's Z_DEFAULT_COMPRESSION).
func NewWriter(lib Library, level, windowBits int) (*Writer, error) {
	// Every deflateInit2 here takes window bits 8 to 15 but 8 only with a
	// zlib header (where it becomes 9): a raw stream's are 9 to 15 (zlib's
	// and Chromium's deflate.c, zlib-rs's deflate.rs, init).
	if windowBits < 9 || windowBits > maxWBits {
		return nil, fmt.Errorf("zdeflate: raw window bits %d rejected by deflateInit2", windowBits)
	}
	switch lib {
	case Zlib, Chromium:
		if level < 1 || level > 9 {
			return nil, fmt.Errorf("zdeflate: zlib level %d not supported", level)
		}
		return &Writer{
			c: newZlibWriter(lib, level, windowBits),
		}, nil
	case ZlibRS:
		if level != 6 {
			return nil, fmt.Errorf("zdeflate: zlib-rs level %d not supported", level)
		}
		return &Writer{
			c: newRSWriter(windowBits),
		}, nil
	}
	return nil, fmt.Errorf("zdeflate: unknown library %d", lib)
}

// Message returns p as one permessage-deflate message: deflate(Z_SYNC_FLUSH)
// output with the trailing 00 00 ff ff cut. The context carries over to the
// next call. An empty p gives 00, the empty stored block, which no library
// writes after a sync flush: Chrome writes it by hand.
func (w *Writer) Message(p []byte) []byte {
	if len(p) == 0 {
		return []byte{
			0x00,
		}
	}
	out := w.c.message(p)
	return out[:len(out)-len(syncTrailer)]
}

// Reset is deflateReset: the next message starts with an empty context,
// as client_no_context_takeover has it.
func (w *Writer) Reset() {
	w.c.reset()
}

// zlibWriter is zlib 1.3.1's deflate stream, or Chromium's: one call per
// message. Fields keep deflate_state's unsigned types (uInt and Pos as
// uint32, long as int64), so the arithmetic wraps where zlib's does.
type zlibWriter struct {
	cfg config
	// chromium selects Chromium's changes (chromium_zlib_hash): a hash
	// of 4 bytes, from contrib/optimizations/insert_string.h, and what
	// follows from it in fill_window and longest_match.
	chromium bool

	// wSize is the window, 1 << windowBits; maxDist the farthest a match
	// may lie (MAX_DIST).
	wSize      uint32
	wMask      uint32
	windowSize uint32
	maxDist    uint32

	// window holds the last wSize bytes compressed and the lookahead;
	// prev links positions with the same hash, head is each hash's most
	// recent position.
	window []byte
	prev   []uint32
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

func newZlibWriter(lib Library, level, windowBits int) *zlibWriter {
	wSize := uint32(1) << windowBits
	w := &zlibWriter{
		cfg:        configs[level],
		chromium:   lib == Chromium,
		wSize:      wSize,
		wMask:      wSize - 1,
		windowSize: 2 * wSize,
		maxDist:    wSize - minLookahead,
		// Chromium's WINDOW_PADDING: its hash reads a byte past the
		// window when the lookahead ends there. Zeroed, as Chromium's.
		window: make([]byte, 2*(wSize+windowPadding)),
		prev:   make([]uint32, wSize),
	}
	w.trees.init()
	w.lmInit()
	return w
}

// reset is zlib's deflateReset: _tr_init and lm_init. The window and prev
// keep their contents, as zlib's do; what of them a new context reads is
// what zlib's reads.
func (w *zlibWriter) reset() {
	w.trees.initBlock()
	w.lmInit()
}

// lmInit is zlib's lm_init.
func (w *zlibWriter) lmInit() {
	clear(w.head[:])
	w.strstart = 0
	w.blockStart = 0
	w.lookahead = 0
	w.insert = 0
	// A match of minMatch-1 is no match.
	w.matchLength = minMatch - 1
	w.prevLength = minMatch - 1
	w.matchAvailable = false
	w.insH = 0
}

func (w *zlibWriter) message(p []byte) []byte {
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
	return out
}

// updateHash is zlib's UPDATE_HASH.
func updateHash(h uint32, c byte) uint32 {
	return ((h << hashShift) ^ uint32(c)) & hashMask
}

// insertString is zlib's INSERT_STRING: it adds the string at str to its
// hash chain and returns the chain's previous head. Chromium's
// insert_string hashes the 4 bytes at str, little-endian, on their own.
func (w *zlibWriter) insertString(str uint32) uint32 {
	if w.chromium {
		v := binary.LittleEndian.Uint32(w.window[str:])
		w.insH = ((v*66521 + 66521) >> 16) & hashMask
	} else {
		w.insH = updateHash(w.insH, w.window[str+minMatch-1])
	}
	head := w.head[w.insH]
	w.prev[str&w.wMask] = head
	w.head[w.insH] = str
	return head
}

// slideHash is zlib's slide_hash: positions move down by wSize with the
// window, those that fall out of it become nilPos.
func (w *zlibWriter) slideHash() {
	for i, m := range w.head {
		if m >= w.wSize {
			w.head[i] = m - w.wSize
		} else {
			w.head[i] = nilPos
		}
	}
	for i, m := range w.prev {
		if m >= w.wSize {
			w.prev[i] = m - w.wSize
		} else {
			w.prev[i] = nilPos
		}
	}
}

// fillWindow is zlib's fill_window: it slides the window when strstart
// nears its end and reads input into the lookahead.
func (w *zlibWriter) fillWindow() {
	for {
		more := w.windowSize - w.lookahead - w.strstart
		if w.strstart >= w.wSize+w.maxDist {
			copy(w.window[:w.wSize-more], w.window[w.wSize:])
			w.matchStart -= w.wSize
			w.strstart -= w.wSize
			w.blockStart -= int64(w.wSize)
			if w.insert > w.strstart {
				w.insert = w.strstart
			}
			w.slideHash()
			more += w.wSize
		}
		if len(w.in) == 0 {
			break
		}
		start := w.strstart + w.lookahead
		n := copy(w.window[start:start+more], w.in)
		w.in = w.in[n:]
		w.lookahead += uint32(n)

		// Hash the strings the previous call left uninserted. Chromium's
		// hash reads 4 bytes, so it inserts while more than minMatch are
		// there.
		switch {
		case w.chromium && w.lookahead+w.insert > minMatch:
			str := w.strstart - w.insert
			for w.insert != 0 {
				w.insertString(str)
				str++
				w.insert--
				if w.lookahead+w.insert <= minMatch {
					break
				}
			}
		case !w.chromium && w.lookahead+w.insert >= minMatch:
			str := w.strstart - w.insert
			w.insH = uint32(w.window[str])
			w.insH = updateHash(w.insH, w.window[str+1])
			for w.insert != 0 {
				w.insH = updateHash(w.insH, w.window[str+minMatch-1])
				w.prev[str&w.wMask] = w.head[w.insH]
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

// longestMatch is zlib's longest_match from curMatch along the hash chain,
// longer than prevLength, setting matchStart. Chromium's (with
// DEFLATE_COMPARE256_64LE, as built for x64 and arm64) measures a candidate
// from its third byte, which its hash does not guarantee equal.
func (w *zlibWriter) longestMatch(curMatch uint32) uint32 {
	chainLength := w.cfg.maxChain
	scan := int(w.strstart)
	bestLen := int(w.prevLength)
	niceMatch := w.cfg.niceLength
	var limit uint32 = nilPos
	if w.strstart > w.maxDist {
		limit = w.strstart - w.maxDist
	}
	strend := scan + maxMatch
	scanEnd1 := w.window[scan+bestLen-1]
	scanEnd := w.window[scan+bestLen]

	if w.prevLength >= w.cfg.goodLength {
		chainLength >>= 2
	}
	if uint32(niceMatch) > w.lookahead {
		niceMatch = int(w.lookahead)
	}

	win := w.window
	for {
		match := int(curMatch)
		if win[match+bestLen] == scanEnd &&
			win[match+bestLen-1] == scanEnd1 &&
			win[match] == win[scan] &&
			win[match+1] == win[scan+1] {
			var length int
			if w.chromium {
				length = 2 + compare256(win[scan+2:], win[match+2:])
			} else {
				// scan[2] and match[2] are equal: same hash, hashBits >= 8.
				s, m := scan+2, match+2
				for {
					s++
					m++
					if win[s] != win[m] || s >= strend {
						break
					}
				}
				length = maxMatch - (strend - s)
			}
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
		curMatch = w.prev[curMatch&w.wMask]
		if curMatch <= limit {
			break
		}
		chainLength--
		if chainLength == 0 {
			break
		}
	}
	if uint32(bestLen) <= w.lookahead {
		return uint32(bestLen)
	}
	return w.lookahead
}

// flushBlock is zlib's FLUSH_BLOCK_ONLY, never the last block: the
// symbols since blockStart go out as one block.
func (w *zlibWriter) flushBlock() {
	var stored []byte
	if w.blockStart >= 0 {
		stored = w.window[w.blockStart:w.strstart]
	}
	w.trees.flushBlock(&w.out, stored, uint64(int64(w.strstart)-w.blockStart))
	w.blockStart = int64(w.strstart)
}

// deflateFast is zlib's deflate_fast (levels 1-3) with a sync flush: no
// lazy matching, and only the strings of short matches inserted.
func (w *zlibWriter) deflateFast() {
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
		if hashHead != nilPos && w.strstart-hashHead <= w.maxDist {
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
				// Chromium skips this: its hash does not roll.
				if !w.chromium {
					w.insH = uint32(w.window[w.strstart])
					w.insH = updateHash(w.insH, w.window[w.strstart+1])
				}
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
func (w *zlibWriter) deflateSlow() {
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
		if hashHead != nilPos && w.prevLength < w.cfg.maxLazy && w.strstart-hashHead <= w.maxDist {
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
