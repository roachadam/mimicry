// Package hpack is an HPACK encoder whose choices follow a browser's
// (profile.HPACK): which fields enter the dynamic table, which entry a
// literal's name refers to, and when strings are Huffman-coded. Decoding
// stays with golang.org/x/net/http2/hpack; only what the client sends is
// fingerprinted.
package hpack

import (
	"slices"

	xhpack "golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/profile"
)

// defaultTableSize is the dynamic table size before the peer's SETTINGS.
const defaultTableSize = 4096

// maxTableSize bounds the table whatever is announced, as Chromium's encoder
// does: offered 1 MiB, Chrome 155 announces 1 MiB (captured), but uses at
// most this.
const maxTableSize = 65536

// Encoder encodes header blocks for one connection. It is not safe for
// concurrent use; the connection serialises header blocks.
type Encoder struct {
	policy profile.HPACK
	// dynamic table, newest first: dynamic[0] has index 62.
	dynamic []entry
	size    uint32
	maxSize uint32
	// peerSize is the peer's latest SETTINGS_HEADER_TABLE_SIZE.
	peerSize uint32
	// pending is set when size updates must start the next block. blockSize
	// is the size in effect at the last block and pendingMin the smallest
	// size set since.
	pending    bool
	blockSize  uint32
	pendingMin uint32
}

type entry struct {
	name, value string
}

func (e entry) size() uint32 {
	return uint32(len(e.name) + len(e.value) + 32) //nolint:gosec // header fields are far below 4 GiB
}

// NewEncoder returns an encoder following policy, which must be a valid
// profile's (profile.Validate requires http2.hpack): there is no default,
// since how a browser encodes headers must be measured (decision 0008).
func NewEncoder(policy *profile.HPACK) *Encoder {
	return &Encoder{
		policy:   *policy,
		maxSize:  defaultTableSize,
		peerSize: defaultTableSize,
	}
}

// SetPeerTableSize applies the peer's SETTINGS_HEADER_TABLE_SIZE, capped by
// the policy's MaxTableSize. The next block starts with size updates (RFC
// 7541, 4.2): the smallest size set since the last block if it went below the
// size then in effect, then the final one. An unchanged size is signalled
// only under SizeUpdatesAlways.
func (e *Encoder) SetPeerTableSize(size uint32) {
	if e.policy.MaxTableSize > 0 {
		size = min(size, e.policy.MaxTableSize)
	}
	if size == e.peerSize && !e.pending && e.policy.SizeUpdates != profile.SizeUpdatesAlways {
		return
	}
	if !e.pending {
		e.blockSize, e.pendingMin = e.peerSize, e.peerSize
	}
	e.pendingMin = min(e.pendingMin, size)
	e.pending = true
	e.peerSize = size
	e.maxSize = min(size, maxTableSize)
	e.evict(0)
}

// Field is a header field to encode.
type Field struct {
	Name, Value string
}

// AppendBlock encodes fields in order and appends the header block to dst.
func (e *Encoder) AppendBlock(dst []byte, fields []Field) []byte {
	if e.pending {
		if e.pendingMin < e.blockSize && e.pendingMin < e.peerSize {
			dst = appendInt(dst, 0x20, 5, uint64(e.pendingMin))
		}
		dst = appendInt(dst, 0x20, 5, uint64(e.peerSize))
		e.pending = false
	}
	for _, f := range fields {
		dst = e.appendField(dst, f)
	}
	return dst
}

func (e *Encoder) appendField(dst []byte, f Field) []byte {
	without := slices.Contains(e.policy.WithoutIndexing, f.Name)
	never := slices.Contains(e.policy.NeverIndexed, f.Name)
	if n, ok := e.policy.NeverIndexBelow[f.Name]; ok && len(f.Value) < n {
		never = true
	}
	exact := e.exactIndex(f)
	if exact != 0 && !never && (!without || !e.policy.LiteralOnExactMatch) {
		return appendInt(dst, 0x80, 7, exact)
	}
	nameIdx := exact
	if nameIdx == 0 {
		nameIdx = e.nameIndex(f.Name)
	}
	switch {
	case never:
		dst = appendInt(dst, 0x10, 4, nameIdx)
	case without:
		dst = appendInt(dst, 0x00, 4, nameIdx)
	default:
		dst = appendInt(dst, 0x40, 6, nameIdx)
	}
	if nameIdx == 0 {
		dst = e.appendString(dst, f.Name)
	}
	dst = e.appendString(dst, f.Value)
	if !never && !without {
		e.insert(entry{
			name:  f.Name,
			value: f.Value,
		})
	}
	return dst
}

// exactIndex is the lowest index whose entry matches f exactly, or 0.
func (e *Encoder) exactIndex(f Field) uint64 {
	for i, s := range staticTable {
		if s.name == f.Name && s.value == f.Value {
			return uint64(i + 1)
		}
	}
	for i, d := range e.dynamic {
		if d.name == f.Name && d.value == f.Value {
			return uint64(len(staticTable) + 1 + i)
		}
	}
	return 0
}

// nameIndex is the index of the entry a literal named name refers to, or 0.
func (e *Encoder) nameIndex(name string) uint64 {
	switch e.policy.NameMatch {
	case profile.NameMatchHighestIndex:
		for i := len(e.dynamic) - 1; i >= 0; i-- {
			if e.dynamic[i].name == name {
				return uint64(len(staticTable) + 1 + i)
			}
		}
		for i := len(staticTable) - 1; i >= 0; i-- {
			if staticTable[i].name == name {
				return uint64(i + 1)
			}
		}
	default:
		for i, s := range staticTable {
			if s.name == name {
				return uint64(i + 1)
			}
		}
		for i, d := range e.dynamic {
			if d.name == name {
				return uint64(len(staticTable) + 1 + i)
			}
		}
	}
	return 0
}

func (e *Encoder) insert(en entry) {
	size := en.size()
	if size > e.maxSize {
		// Too large for the table: inserting it empties the table.
		e.dynamic, e.size = e.dynamic[:0], 0
		return
	}
	e.evict(size)
	e.dynamic = slices.Insert(e.dynamic, 0, en)
	e.size += size
}

// evict drops the oldest entries until room more bytes fit.
func (e *Encoder) evict(room uint32) {
	for len(e.dynamic) > 0 && e.size+room > e.maxSize {
		last := e.dynamic[len(e.dynamic)-1]
		e.dynamic = e.dynamic[:len(e.dynamic)-1]
		e.size -= last.size()
	}
}

// appendInt appends v as an HPACK integer with an n-bit prefix, the high bits
// of the first byte set to first (RFC 7541, 5.1).
func appendInt(dst []byte, first byte, n uint8, v uint64) []byte {
	limit := uint64(1)<<n - 1
	if v < limit {
		return append(dst, first|byte(v)) //nolint:gosec // v < limit <= 255
	}
	dst = append(dst, first|byte(limit)) //nolint:gosec // limit <= 255
	v -= limit
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// appendString appends s as a string literal, Huffman-coded as the policy
// says.
func (e *Encoder) appendString(dst []byte, s string) []byte {
	hl := xhpack.HuffmanEncodeLength(s)
	if hl < uint64(len(s)) || e.policy.Huffman == profile.HuffmanAlways {
		dst = appendInt(dst, 0x80, 7, hl)
		return xhpack.AppendHuffmanString(dst, s)
	}
	dst = appendInt(dst, 0x00, 7, uint64(len(s)))
	return append(dst, s...)
}
