// Package qpack is a QPACK (RFC 9204) encoder whose dynamic table use and
// Huffman rule follow a browser's, and a decoder for the peer's field
// sections and encoder stream.
//
// The encoder tracks the peer's acknowledgments (Known Received Count,
// blocked streams) from its decoder stream and never evicts an entry the
// peer may still need.
package qpack

import (
	"errors"
	"fmt"
	"math"
)

// Strategy is how an encoder uses the dynamic table.
type Strategy int

// Strategies.
const (
	// StrategyStatic never inserts: fields the static table matches
	// exactly are indexed, others are literals with a static name
	// reference where one exists.
	StrategyStatic Strategy = iota
	// StrategyIndexAll is Chromium's (quiche's QpackEncoder): every field
	// without an exact static match is inserted and referenced, except that
	// entries in the draining oldest quarter are duplicated instead. The
	// base is the Required Insert Count, so every reference is relative.
	StrategyIndexAll
	// StrategyNewNames is Firefox's (neqo's): only a field whose name
	// neither table holds is inserted, and referenced post-base; other
	// fields are indexed on an exact match or literals with a name reference.
	StrategyNewNames
)

// Config is how an encoder encodes.
type Config struct {
	Strategy Strategy
	// HuffmanAlways codes every string with Huffman; otherwise only those
	// it makes shorter.
	HuffmanAlways bool
	// MaxCapacity caps the dynamic table capacity the encoder sets,
	// whatever the peer allows.
	MaxCapacity uint64
}

// Field is a header field to encode, or one decoded.
type Field struct {
	Name, Value string
}

// table is a dynamic table: entries[i] has absolute index dropped+i.
type table struct {
	entries  []Field
	dropped  uint64
	size     uint64
	capacity uint64
}

// inserted is the number of inserts so far.
func (t *table) inserted() uint64 {
	return t.dropped + uint64(len(t.entries))
}

// entry returns the entry at absolute index abs.
func (t *table) entry(abs uint64) (Field, bool) {
	if abs < t.dropped || abs >= t.inserted() {
		return Field{}, false
	}
	return t.entries[abs-t.dropped], true
}

// insert adds f, evicting the oldest entries to make room, and returns its
// absolute index. The caller has checked that f fits.
func (t *table) insert(f Field) uint64 {
	size := entrySize(f.Name, f.Value)
	t.evictTo(t.capacity - size)
	t.entries = append(t.entries, f)
	t.size += size
	return t.inserted() - 1
}

// evictTo drops the oldest entries until the table's size is at most
// limit.
func (t *table) evictTo(limit uint64) {
	n := 0
	for t.size > limit && n < len(t.entries) {
		t.size -= entrySize(t.entries[n].Name, t.entries[n].Value)
		n++
	}
	if n > 0 {
		clear(t.entries[:n])
		t.entries = t.entries[n:]
		t.dropped += uint64(n)
	}
}

// section is a field section the peer has not acknowledged: its Required
// Insert Count and the smallest absolute index it refers to.
type section struct {
	ric    uint64
	minRef uint64
}

// Encoder encodes field sections for one connection's requests and writes
// the encoder stream's instructions. It is not safe for concurrent use;
// the connection serialises its requests' encoding.
type Encoder struct {
	cfg Config
	t   table
	// peerMax is the peer's SETTINGS_QPACK_MAX_TABLE_CAPACITY and
	// maxBlocked its SETTINGS_QPACK_BLOCKED_STREAMS.
	peerMax    uint64
	maxBlocked uint64
	// setCapacity is set while the capacity instruction is unsent.
	setCapacity bool
	// known is the Known Received Count; sections the unacknowledged
	// sections of each stream, oldest first.
	known    uint64
	sections map[uint64][]section
	// in holds a decoder stream instruction not yet complete.
	in []byte
}

// NewEncoder returns an encoder with no dynamic table until the peer's
// SETTINGS allow one (SetPeerSettings).
func NewEncoder(cfg Config) *Encoder {
	return &Encoder{
		cfg:      cfg,
		sections: map[uint64][]section{},
	}
}

// SetPeerSettings applies the peer's SETTINGS_QPACK_MAX_TABLE_CAPACITY
// and SETTINGS_QPACK_BLOCKED_STREAMS: the table's capacity becomes the
// smallest of the peer's, the configured maximum and none for
// StrategyStatic, set by the encoder stream's first instruction.
func (e *Encoder) SetPeerSettings(maxCapacity, blockedStreams uint64) {
	e.peerMax, e.maxBlocked = maxCapacity, blockedStreams
	capacity := min(maxCapacity, e.cfg.MaxCapacity)
	if e.cfg.Strategy == StrategyStatic {
		capacity = 0
	}
	if capacity != e.t.capacity {
		e.t.capacity = capacity
		e.setCapacity = true
	}
}

// TakeCapacity returns the Set Dynamic Table Capacity instruction the
// encoder has not sent yet, or nil, for an encoder stream that opens with
// it before any section; Encode then leaves it out.
func (e *Encoder) TakeCapacity() []byte {
	if !e.setCapacity {
		return nil
	}
	e.setCapacity = false
	return appendInt(nil, 0x20, 5, e.t.capacity)
}

// rep is one field line before its section's base is known.
type rep struct {
	kind   repKind
	static bool
	// index is a static index, or a dynamic entry's absolute index.
	index       uint64
	name, value string
}

type repKind int

const (
	repIndexed repKind = iota
	repNameRef
	repLiteral
)

// Encode encodes fields as the field section of a request on stream. It
// returns the encoder stream instructions the section needs (to be sent
// as the strategy orders them against the section) and the section.
func (e *Encoder) Encode(stream uint64, fields []Field) (instructions, block []byte) {
	if e.setCapacity {
		instructions = appendInt(instructions, 0x20, 5, e.t.capacity)
		e.setCapacity = false
	}
	start := e.t.inserted()
	var reps []rep
	switch e.cfg.Strategy {
	case StrategyIndexAll:
		reps, instructions = e.indexAll(stream, fields, instructions)
	case StrategyNewNames:
		reps, instructions = e.newNames(stream, fields, instructions)
	default:
		reps = staticOnly(fields)
	}
	ric, minRef := uint64(0), uint64(math.MaxUint64)
	for _, r := range reps {
		if r.kind != repLiteral && !r.static {
			ric = max(ric, r.index+1)
			minRef = min(minRef, r.index)
		}
	}
	base := ric
	if e.cfg.Strategy == StrategyNewNames && ric > 0 {
		base = start
	}
	block = e.appendPrefix(nil, ric, base)
	for _, r := range reps {
		block = e.appendRep(block, r, base)
	}
	if ric > 0 {
		e.sections[stream] = append(e.sections[stream], section{
			ric:    ric,
			minRef: minRef,
		})
	}
	return instructions, block
}

// staticOnly encodes fields without the dynamic table.
func staticOnly(fields []Field) []rep {
	reps := make([]rep, 0, len(fields))
	for _, f := range fields {
		reps = append(reps, staticRep(f))
	}
	return reps
}

// staticRep is f from the static table: indexed, a literal with its name,
// or a literal.
func staticRep(f Field) rep {
	if i, ok := staticExact[[2]string{f.Name, f.Value}]; ok {
		return rep{
			kind:   repIndexed,
			static: true,
			index:  i,
		}
	}
	if i, ok := staticName[f.Name]; ok {
		return rep{
			kind:   repNameRef,
			static: true,
			index:  i,
			value:  f.Value,
		}
	}
	return rep{
		kind:  repLiteral,
		name:  f.Name,
		value: f.Value,
	}
}

// draining is the fraction of the table's capacity, at its oldest end,
// whose entries StrategyIndexAll no longer references (quiche's
// kDrainingFraction).
const draining = 0.25

// match is a table lookup's result.
type match struct {
	found, exact, static bool
	index                uint64
}

// lookup finds f as quiche does: an exact static match, an exact dynamic
// match (the newest), a static name match (the first), a dynamic name match
// (the newest). usable limits the dynamic entries considered.
func (e *Encoder) lookup(f Field, usable func(uint64) bool) match {
	if i, ok := staticExact[[2]string{f.Name, f.Value}]; ok {
		return match{
			found:  true,
			exact:  true,
			static: true,
			index:  i,
		}
	}
	var name match
	if i, ok := staticName[f.Name]; ok {
		name = match{
			found:  true,
			static: true,
			index:  i,
		}
	}
	for i := len(e.t.entries) - 1; i >= 0; i-- {
		abs := e.t.dropped + uint64(i)
		en := e.t.entries[i]
		if en.Name != f.Name || !usable(abs) {
			continue
		}
		if en.Value == f.Value {
			return match{
				found: true,
				exact: true,
				index: abs,
			}
		}
		if !name.found {
			name = match{
				found: true,
				index: abs,
			}
		}
	}
	return name
}

// lookupName finds a name match only: static first, then the newest
// dynamic entry.
func (e *Encoder) lookupName(name string) match {
	if i, ok := staticName[name]; ok {
		return match{
			found:  true,
			static: true,
			index:  i,
		}
	}
	for i := len(e.t.entries) - 1; i >= 0; i-- {
		if e.t.entries[i].Name == name {
			return match{
				found: true,
				index: e.t.dropped + uint64(i),
			}
		}
	}
	return match{}
}

func anyEntry(uint64) bool {
	return true
}

// indexAll is StrategyIndexAll (quiche's QpackEncoder::FirstPassEncode).
func (e *Encoder) indexAll(stream uint64, fields []Field, ins []byte) ([]rep, []byte) {
	reps := make([]rep, 0, len(fields))
	known := e.known
	// Entries at or after this index must not be evicted: those referenced
	// by unacknowledged sections, and those not acknowledged at all.
	keep := min(e.smallestBlocking(), known)
	drain := e.drainingIndex()
	blocking := e.blockingAllowed(stream)
	for _, f := range fields {
		size := entrySize(f.Name, f.Value)
		m := e.lookup(f, anyEntry)
		if m.found && m.exact {
			if m.static {
				reps = append(reps, rep{
					kind:   repIndexed,
					static: true,
					index:  m.index,
				})
				continue
			}
			if m.index >= drain {
				if blocking || m.index < known {
					reps = append(reps, rep{
						kind:  repIndexed,
						index: m.index,
					})
					keep = min(keep, m.index)
					continue
				}
			} else if blocking && size <= e.maxInsertSize(min(keep, m.index)) {
				// Draining: a copy at the table's new end instead.
				ins = appendInt(ins, 0x00, 5, e.t.inserted()-1-m.index)
				abs := e.t.insert(f)
				reps = append(reps, rep{
					kind:  repIndexed,
					index: abs,
				})
				keep = min(keep, m.index)
				continue
			}
			// The exact match cannot be used: try its name.
			n := e.lookupName(f.Name)
			if !n.found || (n.static == m.static && n.index == m.index) {
				reps = append(reps, literal(f))
				continue
			}
			m = n
		}
		switch {
		case !m.found:
			if blocking && size <= e.maxInsertSize(keep) {
				ins = appendString(ins, 0x40, 5, f.Name, e.cfg.HuffmanAlways)
				ins = appendString(ins, 0x00, 7, f.Value, e.cfg.HuffmanAlways)
				abs := e.t.insert(f)
				reps = append(reps, rep{
					kind:  repIndexed,
					index: abs,
				})
				keep = min(keep, abs)
				continue
			}
			reps = append(reps, literal(f))
		case m.static:
			if blocking && size <= e.maxInsertSize(keep) {
				ins = appendInt(ins, 0xc0, 6, m.index)
				ins = appendString(ins, 0x00, 7, f.Value, e.cfg.HuffmanAlways)
				abs := e.t.insert(f)
				reps = append(reps, rep{
					kind:  repIndexed,
					index: abs,
				})
				keep = min(keep, abs)
				continue
			}
			reps = append(reps, rep{
				kind:   repNameRef,
				static: true,
				index:  m.index,
				value:  f.Value,
			})
		default:
			if blocking && size <= e.maxInsertSize(min(keep, m.index)) {
				ins = appendInt(ins, 0x80, 6, e.t.inserted()-1-m.index)
				ins = appendString(ins, 0x00, 7, f.Value, e.cfg.HuffmanAlways)
				abs := e.t.insert(f)
				reps = append(reps, rep{
					kind:  repIndexed,
					index: abs,
				})
				keep = min(keep, m.index)
				continue
			}
			if (blocking || m.index < known) && m.index >= drain {
				reps = append(reps, rep{
					kind:  repNameRef,
					index: m.index,
					value: f.Value,
				})
				keep = min(keep, m.index)
				continue
			}
			reps = append(reps, literal(f))
		}
	}
	return reps, ins
}

// newNames is StrategyNewNames (neqo's encode_header_block).
func (e *Encoder) newNames(stream uint64, fields []Field, ins []byte) ([]rep, []byte) {
	reps := make([]rep, 0, len(fields))
	blocking := e.blockingAllowed(stream)
	usable := func(abs uint64) bool {
		return blocking || abs < e.known
	}
	keep := min(e.smallestBlocking(), e.known)
	full := false
	for _, f := range fields {
		if m := e.lookup(f, usable); m.found {
			kind := repNameRef
			if m.exact {
				kind = repIndexed
			}
			reps = append(reps, rep{
				kind:   kind,
				static: m.static,
				index:  m.index,
				value:  f.Value,
			})
			if !m.static {
				keep = min(keep, m.index)
			}
			continue
		}
		if blocking && !full && entrySize(f.Name, f.Value) <= e.maxInsertSize(keep) {
			ins = appendString(ins, 0x40, 5, f.Name, e.cfg.HuffmanAlways)
			ins = appendString(ins, 0x00, 7, f.Value, e.cfg.HuffmanAlways)
			abs := e.t.insert(f)
			reps = append(reps, rep{
				kind:  repIndexed,
				index: abs,
			})
			keep = min(keep, abs)
			continue
		}
		// The table is full: no more inserts in this section.
		full = blocking
		reps = append(reps, literal(f))
	}
	return reps, ins
}

func literal(f Field) rep {
	return rep{
		kind:  repLiteral,
		name:  f.Name,
		value: f.Value,
	}
}

// maxInsertSize is the largest entry that fits by evicting only entries
// before absolute index keep (quiche's
// MaxInsertSizeWithoutEvictingGivenEntry).
func (e *Encoder) maxInsertSize(keep uint64) uint64 {
	t := &e.t
	if keep > t.inserted() {
		return t.capacity
	}
	room := t.capacity - t.size
	for i, en := range t.entries {
		if t.dropped+uint64(i) >= keep {
			break
		}
		room += entrySize(en.Name, en.Value)
	}
	return room
}

// drainingIndex is the absolute index from which entries may be
// referenced: those before it fill the oldest quarter of the capacity
// the table needs free (quiche's draining_index).
func (e *Encoder) drainingIndex() uint64 {
	t := &e.t
	required := uint64(draining * float64(t.capacity))
	free := t.capacity - t.size
	if len(t.entries) == 0 || free >= required {
		return t.dropped
	}
	index := t.dropped
	for _, en := range t.entries {
		if free >= required {
			break
		}
		free += entrySize(en.Name, en.Value)
		index++
	}
	return index
}

// smallestBlocking is the smallest absolute index an unacknowledged
// section refers to.
func (e *Encoder) smallestBlocking() uint64 {
	m := uint64(math.MaxUint64)
	for _, secs := range e.sections {
		for _, s := range secs {
			m = min(m, s.minRef)
		}
	}
	return m
}

// blockingAllowed reports whether a section on stream may refer to entries
// the peer has not acknowledged: the stream is blocked already, or fewer
// streams are than the peer allows.
func (e *Encoder) blockingAllowed(stream uint64) bool {
	blocked := uint64(0)
	for s, secs := range e.sections {
		for _, sec := range secs {
			if sec.ric > e.known {
				if s == stream {
					return true
				}
				blocked++
				break
			}
		}
	}
	return blocked < e.maxBlocked
}

// appendPrefix appends a section's prefix (RFC 9204, 4.5.1).
func (e *Encoder) appendPrefix(dst []byte, ric, base uint64) []byte {
	if ric == 0 {
		return append(dst, 0, 0)
	}
	maxEntries := e.peerMax / 32
	dst = appendInt(dst, 0x00, 8, ric%(2*maxEntries)+1)
	if base >= ric {
		return appendInt(dst, 0x00, 7, base-ric)
	}
	return appendInt(dst, 0x80, 7, ric-base-1)
}

// appendRep appends one field line against base (RFC 9204, 4.5.2-4.5.6).
func (e *Encoder) appendRep(dst []byte, r rep, base uint64) []byte {
	always := e.cfg.HuffmanAlways
	switch {
	case r.kind == repIndexed && r.static:
		return appendInt(dst, 0xc0, 6, r.index)
	case r.kind == repIndexed && r.index < base:
		return appendInt(dst, 0x80, 6, base-1-r.index)
	case r.kind == repIndexed:
		return appendInt(dst, 0x10, 4, r.index-base)
	case r.kind == repNameRef && r.static:
		dst = appendInt(dst, 0x50, 4, r.index)
	case r.kind == repNameRef && r.index < base:
		dst = appendInt(dst, 0x40, 4, base-1-r.index)
	case r.kind == repNameRef:
		dst = appendInt(dst, 0x00, 3, r.index-base)
	default:
		dst = appendString(dst, 0x20, 3, r.name, always)
	}
	return appendString(dst, 0x00, 7, r.value, always)
}

// ErrDecoderStream is a decoder stream instruction the encoder cannot
// accept: a connection error (QPACK_DECODER_STREAM_ERROR).
var ErrDecoderStream = errors.New("qpack: decoder stream error")

// OnDecoderStream applies the peer's decoder stream bytes: section
// acknowledgments, stream cancellations and insert count increments. An
// instruction cut short is kept for the next call.
func (e *Encoder) OnDecoderStream(b []byte) error {
	e.in = append(e.in, b...)
	for len(e.in) > 0 {
		var n int
		var err error
		switch c := e.in[0]; {
		case c&0x80 != 0:
			var stream uint64
			if stream, n, err = readInt(e.in, 7); err == nil {
				err = e.acknowledged(stream)
			}
		case c&0x40 != 0:
			var stream uint64
			if stream, n, err = readInt(e.in, 6); err == nil {
				delete(e.sections, stream)
			}
		default:
			var inc uint64
			if inc, n, err = readInt(e.in, 6); err == nil {
				if inc == 0 || e.known+inc > e.t.inserted() {
					err = fmt.Errorf("%w: insert count increment %d", ErrDecoderStream, inc)
				}
				e.known += inc
			}
		}
		if errors.Is(err, errIncomplete) {
			return nil
		}
		if err != nil {
			return err
		}
		e.in = e.in[n:]
	}
	e.in = nil
	return nil
}

// acknowledged applies a Section Acknowledgment for stream's oldest
// unacknowledged section.
func (e *Encoder) acknowledged(stream uint64) error {
	secs := e.sections[stream]
	if len(secs) == 0 {
		return fmt.Errorf("%w: acknowledgment for stream %d, which has no section outstanding", ErrDecoderStream, stream)
	}
	e.known = max(e.known, secs[0].ric)
	if len(secs) == 1 {
		delete(e.sections, stream)
	} else {
		e.sections[stream] = secs[1:]
	}
	return nil
}
