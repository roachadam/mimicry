package zdeflate

import (
	"encoding/binary"
	"math/bits"
)

// zlib-rs 0.6.3's deflate parameters, where they differ from zlib's.
const (
	// rsHashSize is zlib-rs's HASH_SIZE: 16 hash bits whatever memLevel.
	rsHashSize = 1 << 16

	// wantMinMatch is WANT_MIN_MATCH: deflate_medium emits shorter
	// matches as literals, and its hash covers 4 bytes.
	wantMinMatch = 4

	// Level 6's line of CONFIGURATION_TABLE (deflate/algorithm/mod.rs),
	// which selects deflate_medium. goodLength does not appear: it
	// quarters the chain after a previous match that long, and
	// deflate_medium leaves prev_length 0.
	rsMaxLazy   = 16
	rsNiceMatch = 128
	rsMaxChain  = 128

	// rsOutLen is the output buffer Firefox gives each deflate call:
	// PMCECompression's kBufferLen (netwerk/protocol/websocket/
	// WebSocketChannel.cpp). It shapes the output, because deflate_medium
	// forgets its look-ahead match when a full buffer stops it.
	rsOutLen = 4096
)

// rsWriter is zlib-rs 0.6.3's deflate stream at level 6, driven as
// Firefox's PMCECompression::Deflate drives it: deflate(Z_SYNC_FLUSH) into
// a 4096-byte buffer, called again while input remains or the buffer came
// back full.
//
// Positions are zlib-rs's Pos (u16) where it keeps them as such; the
// window is at most 64 KiB, so they never wrap.
type rsWriter struct {
	// wSize is the window, 1 << windowBits; maxDist the farthest a match
	// may lie (State::max_dist).
	wSize      int
	wMask      int
	windowSize int
	maxDist    int

	// window holds the last wSize bytes compressed and the lookahead;
	// prev links positions with the same hash, head is each hash's most
	// recent position.
	window []byte
	prev   []uint16
	head   [rsHashSize]uint16

	strstart   int
	blockStart int
	lookahead  int
	insert     int

	// in is the rest of the message being compressed.
	in []byte

	trees trees
	// out is the message's output; sent counts its bytes copied to
	// Firefox's buffer, avail the room left there in this deflate call.
	out   bitWriter
	sent  int
	avail int
}

func newRSWriter(windowBits int) *rsWriter {
	wSize := 1 << windowBits
	w := &rsWriter{
		wSize:      wSize,
		wMask:      wSize - 1,
		windowSize: 2 * wSize,
		maxDist:    wSize - minLookahead,
		window:     make([]byte, 2*wSize),
		prev:       make([]uint16, wSize),
	}
	w.trees.init()
	return w
}

// reset is zlib-rs's deflate::reset: zng_tr_init and lm_init. The window
// and prev keep their contents, as zlib-rs's do.
func (w *rsWriter) reset() {
	w.trees.initBlock()
	clear(w.head[:])
	w.strstart = 0
	w.blockStart = 0
	w.lookahead = 0
	w.insert = 0
}

// message is PMCECompression::Deflate's loop. zlib-rs's deflate returns
// Z_BUF_ERROR only when called with no input, nothing pending and no
// buffer filled since its last sync flush, which this loop never does
// with a non-empty p.
func (w *rsWriter) message(p []byte) []byte {
	w.out.buf = make([]byte, 0, len(p)/2+16)
	w.sent = 0
	w.in = p
	for {
		w.avail = rsOutLen
		w.deflate()
		if len(w.in) == 0 && w.avail != 0 {
			break
		}
	}
	out := w.out.buf
	w.out.buf = nil
	return out
}

// deflate is one call of zlib-rs's deflate (deflate.rs) on a raw stream
// with Z_SYNC_FLUSH.
func (w *rsWriter) deflate() {
	if w.pending() != 0 {
		w.flushPending()
		if w.avail == 0 {
			return
		}
	}
	if w.deflateMedium() {
		return
	}
	// BlockState::BlockDone: the sync flush's empty stored block. When
	// the buffer fills here, Firefox calls again and gets another one.
	w.trees.storedBlock(&w.out, nil)
	w.flushPending()
}

// pending is the number of whole bytes written and not yet copied out:
// zlib-rs's pending buffer after BitWriter::flush_bits. Each deflate
// call ends with flush_pending, so the bits left over are never a byte.
func (w *rsWriter) pending() int {
	return len(w.out.buf) + w.out.nbits/8 - w.sent
}

// flushPending is zlib-rs's flush_pending: as much of the pending output
// as the buffer holds.
func (w *rsWriter) flushPending() {
	n := min(w.pending(), w.avail)
	w.sent += n
	w.avail -= n
}

// flushBlock is zlib-rs's flush_block! macro, never the last block:
// flush_block_only, then whether the buffer is full, which stops
// deflate_medium (BlockState::NeedMore).
func (w *rsWriter) flushBlock() bool {
	var stored []byte
	if w.blockStart >= 0 {
		stored = w.window[w.blockStart:w.strstart]
	}
	// zng_tr_flush_block is zlib's _tr_flush_block but for a block of no
	// symbols, which deflate_medium never flushes.
	w.trees.flushBlock(&w.out, stored, uint64(w.strstart-w.blockStart)) //nolint:gosec // strstart >= blockStart
	w.blockStart = w.strstart
	w.flushPending()
	return w.avail == 0
}

// hash is StandardHashCalc's (deflate/hash_calc.rs): a multiplicative
// hash of the 4 bytes at s, the same on every CPU. Level 9 alone uses the
// rolling RollHashCalc.
func (w *rsWriter) hash(s int) uint32 {
	return (binary.LittleEndian.Uint32(w.window[s:]) * 2654435761) >> 16
}

// quickInsertString is StandardHashCalc::quick_insert_string: it adds the
// string at s to its hash chain, unless it heads it already, and returns
// the chain's previous head.
func (w *rsWriter) quickInsertString(s int) uint16 {
	h := w.hash(s)
	head := w.head[h]
	if head != uint16(s) { //nolint:gosec // s < windowSize <= 1<<16
		w.prev[s&w.wMask] = head
		w.head[h] = uint16(s) //nolint:gosec // s < windowSize <= 1<<16
	}
	return head
}

// insertString is StandardHashCalc::insert_string: count strings from s,
// but none whose 4 bytes run past the window.
func (w *rsWriter) insertString(s, count int) {
	end := min(len(w.window), s+count+3)
	for ; s+4 <= end; s++ {
		w.quickInsertString(s)
	}
}

// slideHash is zlib-rs's slide_hash: positions move down by wSize with
// the window, saturating at 0. Its SIMD variants compute the same.
func (w *rsWriter) slideHash() {
	for i, m := range w.head {
		w.head[i] = uint16(max(int(m)-w.wSize, 0)) //nolint:gosec // m < 1<<16
	}
	for i, m := range w.prev {
		w.prev[i] = uint16(max(int(m)-w.wSize, 0)) //nolint:gosec // m < 1<<16
	}
}

// fillWindow is zlib-rs's fill_window: it slides the window when strstart
// nears its end and reads input into the lookahead. Unlike zlib's, it
// moves the whole upper half down, never zeroes past the data, and hashes
// from the string before the uninserted ones.
func (w *rsWriter) fillWindow() {
	for {
		more := w.windowSize - w.lookahead - w.strstart
		if w.strstart >= w.wSize+w.maxDist {
			copy(w.window[:w.wSize], w.window[w.wSize:])
			// match_start and prev_length move too; deflate_medium
			// reads neither.
			w.strstart -= w.wSize
			w.blockStart -= w.wSize
			w.insert = min(w.insert, w.strstart)
			w.slideHash()
			more += w.wSize
		}
		if len(w.in) == 0 {
			break
		}
		start := w.strstart + w.lookahead
		n := copy(w.window[start:start+more], w.in)
		w.in = w.in[n:]
		w.lookahead += n

		// Hash the strings the previous call left uninserted.
		if w.lookahead+w.insert >= minMatch {
			str := w.strstart - w.insert
			if str >= 1 {
				w.quickInsertString(str + 2 - minMatch)
			}
			count := w.insert
			if w.lookahead == 1 {
				count--
			}
			if count > 0 {
				w.insertString(str, count)
				w.insert -= count
			}
		}
		if w.lookahead >= minLookahead || len(w.in) == 0 {
			break
		}
	}
}

// longestMatch is zlib-rs's longest_match (deflate/longest_match.rs, not
// the SLOW variant): the longest match for the string at strstart along
// the hash chain from curMatch, cut to the lookahead, and where it starts.
// A result under wantMinMatch is a literal to deflate_medium, whatever its
// start.
func (w *rsWriter) longestMatch(curMatch uint16) (int, uint16) {
	win := w.window
	strstart := w.strstart
	var matchStart uint16
	// prev_length is 0 under deflate_medium, so best_len starts at
	// STD_MIN_MATCH - 1, below good_match: the chain is never quartered.
	bestLen := minMatch - 1
	// offset is where the 8 bytes compared ahead of a candidate start: so
	// they end at bestLen, the byte a longer match must also match.
	offset := bestLen - 1
	chainLength := uint16(rsMaxChain)
	limit := uint16(max(strstart-w.maxDist, 0)) //nolint:gosec // strstart < 1<<16

	// next is goto_next_in_chain: whether the chain goes on; the
	// candidate loops below continue without the outer loop's check that
	// the candidate lies before strstart, as zlib-rs's do.
	next := func() bool {
		chainLength--
		if chainLength == 0 {
			return false
		}
		curMatch = w.prev[int(curMatch)&w.wMask]
		return curMatch > limit
	}

	for int(curMatch) < strstart {
		length := 0
		if bestLen < 8 {
			// A candidate is checked by its first 8 bytes: a match
			// shorter than that and longer than bestLen is taken from
			// them, one of 8 or more is measured below.
			scanVal := binary.LittleEndian.Uint64(win[strstart:])
			for {
				cmp := scanVal ^ binary.LittleEndian.Uint64(win[curMatch:])
				if cmp == 0 {
					break
				}
				if n := bits.TrailingZeros64(cmp) / 8; n > bestLen {
					length = n
					break
				}
				if !next() {
					return bestLen, matchStart
				}
			}
		} else {
			for !equal8(win, int(curMatch)+offset, strstart+offset) || !equal8(win, int(curMatch), strstart) {
				if !next() {
					return bestLen, matchStart
				}
			}
		}
		if length == 0 {
			length = compare256(win[strstart+2:], win[int(curMatch)+2:]) + 2
		}
		if length > bestLen {
			matchStart = curMatch
			// A match reaching the end of the input ends the search.
			if length >= w.lookahead {
				return w.lookahead, matchStart
			}
			bestLen = length
			if bestLen >= rsNiceMatch {
				return bestLen, matchStart
			}
			offset = bestLen - 1
			if bestLen >= 4 {
				offset -= 2
				if bestLen >= 8 {
					offset -= 4
				}
			}
		}
		if !next() {
			return bestLen, matchStart
		}
	}
	return bestLen, matchStart
}

// equal8 reports whether the 8 bytes at a and b in win are equal.
func equal8(win []byte, a, b int) bool {
	return binary.LittleEndian.Uint64(win[a:]) == binary.LittleEndian.Uint64(win[b:])
}

// compare256 is zlib-rs's compare256 and Chromium's (compare256.h): how
// many of the first 256 bytes of a and b are equal before the first that
// differs. zlib-rs's SIMD variants compute the same.
func compare256(a, b []byte) int {
	a, b = a[:256], b[:256]
	for i := 0; i < 256; i += 8 {
		if cmp := binary.LittleEndian.Uint64(a[i:]) ^ binary.LittleEndian.Uint64(b[i:]); cmp != 0 {
			return i + bits.TrailingZeros64(cmp)/8
		}
	}
	return 256
}

// rsMatch is deflate_medium's Match: a match, or a literal (length 1),
// at strstart; the strings before orgstart are in the hash table.
type rsMatch struct {
	matchStart  uint16
	matchLength uint16
	strstart    uint16
	orgstart    uint16
}

// deflateMedium is zlib-rs's deflate_medium (deflate/algorithm/medium.rs)
// with a sync flush: each match is searched for, then the one where it
// ends, and the second may move left to shorten the first. It reports
// that the buffer filled (BlockState::NeedMore); the look-ahead match is
// a local, lost then.
func (w *rsWriter) deflateMedium() bool {
	var current, next rsMatch
	for {
		if w.lookahead < minLookahead {
			w.fillWindow()
			if w.lookahead == 0 {
				break
			}
			next.matchLength = 0
		}

		if next.matchLength > 0 {
			current = next
			next.matchLength = 0
		} else {
			var hashHead uint16
			if w.lookahead >= wantMinMatch {
				hashHead = w.quickInsertString(w.strstart)
			}
			current = w.findMatch(hashHead)
			if current.matchLength < wantMinMatch || current.matchStart >= current.strstart {
				current.matchLength = 1
			}
		}

		w.insertMatch(current)

		// Look ahead one: the match where this one ends.
		end := int(current.strstart) + int(current.matchLength)
		if w.lookahead > minLookahead && end < w.windowSize-minLookahead {
			w.strstart = end
			next = w.findMatch(w.quickInsertString(w.strstart))
			if next.matchStart >= next.strstart || next.matchLength < wantMinMatch {
				next.matchLength = 1
			} else {
				w.fizzleMatches(&current, &next)
			}
			w.strstart = int(current.strstart)
		} else {
			next.matchLength = 0
		}

		flush := w.emitMatch(current)
		w.strstart += int(current.matchLength)
		if flush && w.flushBlock() {
			return true
		}
	}
	w.insert = min(w.strstart, minMatch-1)
	return w.trees.symNext != 0 && w.flushBlock()
}

// findMatch is deflate_medium's search at strstart, hashHead its chain:
// a match, or length 0 and start 0 where there is no candidate, which
// the caller makes a literal.
func (w *rsWriter) findMatch(hashHead uint16) rsMatch {
	m := rsMatch{
		strstart: uint16(w.strstart), //nolint:gosec // strstart < 1<<16
		orgstart: uint16(w.strstart), //nolint:gosec // strstart < 1<<16
	}
	// Position 0 is never matched: it would match itself at the start.
	if dist := w.strstart - int(hashHead); dist <= w.maxDist && dist > 0 && hashHead != 0 {
		length, start := w.longestMatch(hashHead)
		m.matchLength = uint16(length) //nolint:gosec // length <= maxMatch
		m.matchStart = start
	}
	return m
}

// emitMatch is deflate_medium's emit_match: a short match's bytes as
// literals, a long one as a match, and whether the block is full.
func (w *rsWriter) emitMatch(m rsMatch) bool {
	flush := false
	if m.matchLength < wantMinMatch {
		for _, c := range w.window[w.strstart : w.strstart+int(m.matchLength)] {
			if w.trees.tally(0, uint32(c)) {
				flush = true
			}
		}
	} else {
		flush = w.trees.tally(uint32(m.strstart-m.matchStart), uint32(m.matchLength)-minMatch)
	}
	w.lookahead -= int(m.matchLength)
	return flush
}

// insertMatch is deflate_medium's insert_match: the strings inside a
// match go into the hash table, all of them for one up to 16 * max_lazy
// long, else only the one before its end. A literal reaches it with
// length 1, for which zlib-rs inserts nothing.
func (w *rsWriter) insertMatch(m rsMatch) {
	if w.lookahead <= int(m.matchLength)+wantMinMatch || m.matchLength < wantMinMatch {
		return
	}
	if int(m.matchLength) <= 16*rsMaxLazy && w.lookahead >= wantMinMatch {
		m.matchLength-- // the string at strstart is in the table
		m.strstart++
		switch {
		case m.strstart >= m.orgstart && m.strstart+m.matchLength > m.orgstart:
			w.insertString(int(m.strstart), int(m.matchLength))
		case m.strstart >= m.orgstart:
			w.insertString(int(m.strstart), int(m.orgstart-m.strstart)+1)
		case m.orgstart < m.strstart+m.matchLength:
			w.insertString(int(m.orgstart), int(m.strstart+m.matchLength-m.orgstart))
		}
		return
	}
	m.strstart += m.matchLength
	if m.strstart >= minMatch-2 {
		w.quickInsertString(int(m.strstart) + 2 - minMatch)
	}
}

// fizzleMatches is deflate_medium's fizzle_matches: where the bytes before
// next repeat those before its source, next moves left over the end of
// current, kept only if current is left a literal or nothing.
func (w *rsWriter) fizzleMatches(current, next *rsMatch) {
	win := w.window
	if current.matchLength <= 1 ||
		current.matchLength > 1+next.matchStart ||
		current.matchLength > 1+next.strstart {
		return
	}
	// Quick exit: the first byte current would give up.
	if win[int(next.matchStart)+1-int(current.matchLength)] != win[int(next.strstart)+1-int(current.matchLength)] {
		return
	}

	limit := uint16(max(int(next.strstart)-w.maxDist, 0)) //nolint:gosec // strstart < 1<<16
	c, n := *current, *next
	changed := 0
	// The bytes before next's source and before next, walking back from
	// where they started; matchStart < strstart, so the source runs out
	// first, which ends the walk.
	for k := 1; int(next.matchStart)-k >= 0 && win[int(next.matchStart)-k] == win[int(next.strstart)-k]; k++ {
		if c.matchLength < 1 || n.strstart <= limit || n.matchLength >= 256 || n.matchStart <= 1 {
			break
		}
		n.strstart--
		n.matchStart--
		n.matchLength++
		c.matchLength--
		changed++
	}
	if changed == 0 {
		return
	}
	if c.matchLength <= 1 && n.matchLength != 2 {
		n.orgstart++
		*current = c
		*next = n
	}
}
