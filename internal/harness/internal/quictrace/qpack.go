package quictrace

import (
	"errors"
	"fmt"

	"golang.org/x/net/http2/hpack"
)

// QPACK field line representations (RFC 9204, section 4.5).
const (
	RepIndexed            = "indexed"               // 4.5.2: a table entry, name and value
	RepIndexedPostBase    = "indexed_post_base"     // 4.5.3: a dynamic entry after the base
	RepLiteralRef         = "literal_ref"           // 4.5.4: literal value, name from a table
	RepLiteralPostBaseRef = "literal_post_base_ref" // 4.5.5: literal value, name of an entry after the base
	RepLiteral            = "literal"               // 4.5.6: literal name and value
)

// FieldLine is one field line of an encoded field section, as sent.
type FieldLine struct {
	Rep string `json:"rep"`
	// Static marks a reference into the static table; otherwise Index is
	// into the dynamic table, relative to the section's base (post-base
	// for the post-base representations), as on the wire.
	Static bool   `json:"static,omitempty"`
	Index  uint64 `json:"index,omitempty"`
	// Never is the N bit: the field must never be added to a table by an
	// intermediary.
	Never        bool   `json:"never,omitempty"`
	NameHuffman  bool   `json:"nameHuffman,omitempty"`
	ValueHuffman bool   `json:"valueHuffman,omitempty"`
	Name         string `json:"name"`
	Value        string `json:"value"`
}

// Section is an encoded field section (a HEADERS frame's payload)
// decoded: its prefix as sent (RFC 9204, section 4.5.1) and its field
// lines in order.
type Section struct {
	EncodedInsertCount  uint64      `json:"encodedInsertCount,omitempty"`
	RequiredInsertCount uint64      `json:"requiredInsertCount,omitempty"`
	Sign                bool        `json:"sign,omitempty"`
	DeltaBase           uint64      `json:"deltaBase,omitempty"`
	Fields              []FieldLine `json:"fields"`
}

// Encoder stream instructions (RFC 9204, section 4.3).
const (
	OpSetCapacity   = "set_capacity"
	OpInsertNameRef = "insert_name_ref"
	OpInsertLiteral = "insert_literal"
	OpDuplicate     = "duplicate"
)

// EncoderInstruction is one instruction of a QPACK encoder stream.
type EncoderInstruction struct {
	Op       string `json:"op"`
	Capacity uint64 `json:"capacity,omitempty"`
	// Static marks a name reference into the static table; otherwise
	// Index is relative to the inserts so far, as on the wire (also the
	// entry a duplicate copies).
	Static       bool   `json:"static,omitempty"`
	Index        uint64 `json:"index,omitempty"`
	NameHuffman  bool   `json:"nameHuffman,omitempty"`
	ValueHuffman bool   `json:"valueHuffman,omitempty"`
	Name         string `json:"name,omitempty"`
	Value        string `json:"value,omitempty"`
}

// Decoder stream instructions (RFC 9204, section 4.4).
const (
	OpSectionAck           = "section_ack"
	OpStreamCancel         = "stream_cancel"
	OpInsertCountIncrement = "insert_count_increment"
)

// DecoderInstruction is one instruction of a QPACK decoder stream.
type DecoderInstruction struct {
	Op        string `json:"op"`
	Stream    uint64 `json:"stream,omitempty"`
	Increment uint64 `json:"increment,omitempty"`
}

// errIncomplete is an instruction that continues beyond the bytes given.
var errIncomplete = errors.New("incomplete instruction")

// ErrBlocked is a field section referring to dynamic table entries the
// encoder stream has not yet inserted.
var ErrBlocked = errors.New("field section blocked on the encoder stream")

// prefixInt reads an integer with an n-bit prefix (RFC 7541, section 5.1)
// from the start of b.
func prefixInt(b []byte, n uint) (uint64, int, error) {
	if len(b) == 0 {
		return 0, 0, errIncomplete
	}

	limit := uint64(1)<<n - 1
	v := uint64(b[0]) & limit

	if v < limit {
		return v, 1, nil
	}

	for i, shift := 1, uint(0); ; i++ {
		if i >= len(b) {
			return 0, 0, errIncomplete
		}

		if shift > 56 {
			return 0, 0, errors.New("integer overflow")
		}

		v += uint64(b[i]&0x7f) << shift
		shift += 7

		if b[i]&0x80 == 0 {
			return v, i + 1, nil
		}
	}
}

// prefixString reads a string literal whose length has an n-bit prefix,
// the Huffman flag being the bit above it.
func prefixString(b []byte, n uint) (s string, huffman bool, consumed int, err error) {
	if len(b) == 0 {
		return "", false, 0, errIncomplete
	}

	huffman = b[0]&(1<<n) != 0

	length, k, err := prefixInt(b, n)
	if err != nil {
		return "", false, 0, err
	}

	if uint64(len(b)-k) < length {
		return "", false, 0, errIncomplete
	}

	data := b[k : k+int(length)]
	if huffman {
		if s, err = hpack.HuffmanDecodeToString(data); err != nil {
			return "", false, 0, err
		}
	} else {
		s = string(data)
	}

	return s, huffman, k + int(length), nil
}

// staticTable is QPACK's static table (RFC 9204, appendix A).
var staticTable = [...][2]string{
	{":authority", ""},
	{":path", "/"},
	{"age", "0"},
	{"content-disposition", ""},
	{"content-length", "0"},
	{"cookie", ""},
	{"date", ""},
	{"etag", ""},
	{"if-modified-since", ""},
	{"if-none-match", ""},
	{"last-modified", ""},
	{"link", ""},
	{"location", ""},
	{"referer", ""},
	{"set-cookie", ""},
	{":method", "CONNECT"},
	{":method", "DELETE"},
	{":method", "GET"},
	{":method", "HEAD"},
	{":method", "OPTIONS"},
	{":method", "POST"},
	{":method", "PUT"},
	{":scheme", "http"},
	{":scheme", "https"},
	{":status", "103"},
	{":status", "200"},
	{":status", "304"},
	{":status", "404"},
	{":status", "503"},
	{"accept", "*/*"},
	{"accept", "application/dns-message"},
	{"accept-encoding", "gzip, deflate, br"},
	{"accept-ranges", "bytes"},
	{"access-control-allow-headers", "cache-control"},
	{"access-control-allow-headers", "content-type"},
	{"access-control-allow-origin", "*"},
	{"cache-control", "max-age=0"},
	{"cache-control", "max-age=2592000"},
	{"cache-control", "max-age=604800"},
	{"cache-control", "no-cache"},
	{"cache-control", "no-store"},
	{"cache-control", "public, max-age=31536000"},
	{"content-encoding", "br"},
	{"content-encoding", "gzip"},
	{"content-type", "application/dns-message"},
	{"content-type", "application/javascript"},
	{"content-type", "application/json"},
	{"content-type", "application/x-www-form-urlencoded"},
	{"content-type", "image/gif"},
	{"content-type", "image/jpeg"},
	{"content-type", "image/png"},
	{"content-type", "text/css"},
	{"content-type", "text/html; charset=utf-8"},
	{"content-type", "text/plain"},
	{"content-type", "text/plain;charset=utf-8"},
	{"range", "bytes=0-"},
	{"strict-transport-security", "max-age=31536000"},
	{"strict-transport-security", "max-age=31536000; includesubdomains"},
	{"strict-transport-security", "max-age=31536000; includesubdomains; preload"},
	{"vary", "accept-encoding"},
	{"vary", "origin"},
	{"x-content-type-options", "nosniff"},
	{"x-xss-protection", "1; mode=block"},
	{":status", "100"},
	{":status", "204"},
	{":status", "206"},
	{":status", "302"},
	{":status", "400"},
	{":status", "403"},
	{":status", "421"},
	{":status", "425"},
	{":status", "500"},
	{"accept-language", ""},
	{"access-control-allow-credentials", "FALSE"},
	{"access-control-allow-credentials", "TRUE"},
	{"access-control-allow-headers", "*"},
	{"access-control-allow-methods", "get"},
	{"access-control-allow-methods", "get, post, options"},
	{"access-control-allow-methods", "options"},
	{"access-control-expose-headers", "content-length"},
	{"access-control-request-headers", "content-type"},
	{"access-control-request-method", "get"},
	{"access-control-request-method", "post"},
	{"alt-svc", "clear"},
	{"authorization", ""},
	{"content-security-policy", "script-src 'none'; object-src 'none'; base-uri 'none'"},
	{"early-data", "1"},
	{"expect-ct", ""},
	{"forwarded", ""},
	{"if-range", ""},
	{"origin", ""},
	{"purpose", "prefetch"},
	{"server", ""},
	{"timing-allow-origin", "*"},
	{"upgrade-insecure-requests", "1"},
	{"user-agent", ""},
	{"x-forwarded-for", ""},
	{"x-frame-options", "deny"},
	{"x-frame-options", "sameorigin"},
}

func staticEntry(i uint64) ([2]string, error) {
	if i >= uint64(len(staticTable)) {
		return [2]string{}, fmt.Errorf("static index %d out of range", i)
	}

	return staticTable[i], nil
}

// Table is a QPACK decoder's dynamic table (RFC 9204, section 3.2), fed
// by the peer's encoder stream.
type Table struct {
	maxCapacity uint64
	capacity    uint64
	size        uint64
	// entries are the entries not yet evicted, oldest first; dropped is
	// how many were evicted, the absolute index of entries[0].
	entries [][2]string
	dropped uint64
}

// NewTable returns an empty dynamic table whose capacity may grow to
// maxCapacity (the decoder's SETTINGS_QPACK_MAX_TABLE_CAPACITY).
func NewTable(maxCapacity uint64) *Table {
	return &Table{
		maxCapacity: maxCapacity,
	}
}

// Inserted is how many entries the encoder stream has inserted.
func (t *Table) Inserted() uint64 {
	return t.dropped + uint64(len(t.entries))
}

func (t *Table) entry(absolute uint64) ([2]string, error) {
	if absolute < t.dropped || absolute >= t.Inserted() {
		return [2]string{}, fmt.Errorf("dynamic entry %d not in the table", absolute)
	}

	return t.entries[absolute-t.dropped], nil
}

func (t *Table) evict(room uint64) error {
	if room > t.capacity {
		return fmt.Errorf("entry of %d bytes larger than the table's capacity %d", room, t.capacity)
	}

	for t.size+room > t.capacity {
		e := t.entries[0]

		t.entries = t.entries[1:]
		t.dropped++

		t.size -= entrySize(e)
	}

	return nil
}

func entrySize(e [2]string) uint64 {
	return uint64(len(e[0]) + len(e[1]) + 32)
}

func (t *Table) insert(e [2]string) error {
	if err := t.evict(entrySize(e)); err != nil {
		return err
	}

	t.entries = append(t.entries, e)

	t.size += entrySize(e)

	return nil
}

// ReadEncoderStream applies the complete encoder instructions at the start
// of b to the table and returns them with the bytes they took; an
// incomplete instruction at the end is left for the next call.
func (t *Table) ReadEncoderStream(b []byte) ([]EncoderInstruction, int, error) {
	var out []EncoderInstruction

	read := 0

	for read < len(b) {
		in, n, err := t.encoderInstruction(b[read:])
		if errors.Is(err, errIncomplete) {
			break
		}

		if err != nil {
			return out, read, err
		}

		out = append(out, in)

		read += n
	}

	return out, read, nil
}

func (t *Table) encoderInstruction(b []byte) (EncoderInstruction, int, error) {
	var in EncoderInstruction
	switch {
	case b[0]&0x80 != 0:
		in.Op, in.Static = OpInsertNameRef, b[0]&0x40 != 0

		index, n, err := prefixInt(b, 6)
		if err != nil {
			return in, 0, err
		}

		value, huff, m, err := prefixString(b[n:], 7)
		if err != nil {
			return in, 0, err
		}

		in.Index, in.Value, in.ValueHuffman = index, value, huff

		var name [2]string

		switch {
		case in.Static:
			name, err = staticEntry(index)
		case index >= t.Inserted():
			err = fmt.Errorf("insert refers to relative index %d of %d entries", index, t.Inserted())
		default:
			name, err = t.entry(t.Inserted() - 1 - index)
		}

		if err != nil {
			return in, 0, err
		}

		in.Name = name[0]

		return in, n + m, t.insert([2]string{in.Name, in.Value})

	case b[0]&0x40 != 0:
		in.Op = OpInsertLiteral

		name, nameHuff, n, err := prefixString(b, 5)
		if err != nil {
			return in, 0, err
		}

		value, valueHuff, m, err := prefixString(b[n:], 7)
		if err != nil {
			return in, 0, err
		}

		in.Name, in.NameHuffman, in.Value, in.ValueHuffman = name, nameHuff, value, valueHuff

		return in, n + m, t.insert([2]string{name, value})

	case b[0]&0x20 != 0:
		in.Op = OpSetCapacity

		capacity, n, err := prefixInt(b, 5)
		if err != nil {
			return in, 0, err
		}

		if capacity > t.maxCapacity {
			return in, 0, fmt.Errorf("capacity %d above the %d allowed", capacity, t.maxCapacity)
		}

		in.Capacity, t.capacity = capacity, capacity

		return in, n, t.evict(0)
	}

	in.Op = OpDuplicate

	index, n, err := prefixInt(b, 5)
	if err != nil {
		return in, 0, err
	}

	if index >= t.Inserted() {
		return in, 0, fmt.Errorf("duplicate of relative index %d of %d entries", index, t.Inserted())
	}

	e, err := t.entry(t.Inserted() - 1 - index)
	if err != nil {
		return in, 0, err
	}

	in.Index, in.Name, in.Value = index, e[0], e[1]

	return in, n, t.insert(e)
}

// DecodeSection decodes an encoded field section against the table. It
// returns ErrBlocked, with the required insert count, if the section needs
// entries not yet inserted.
func (t *Table) DecodeSection(block []byte) (Section, error) {
	var s Section

	encoded, n, err := prefixInt(block, 8)
	if err != nil {
		return s, err
	}

	block = block[n:]
	if len(block) == 0 {
		return s, errIncomplete
	}

	s.Sign = block[0]&0x80 != 0

	delta, n, err := prefixInt(block, 7)
	if err != nil {
		return s, err
	}

	block = block[n:]
	s.EncodedInsertCount, s.DeltaBase = encoded, delta

	if s.RequiredInsertCount, err = t.requiredInsertCount(encoded); err != nil {
		return s, err
	}

	if s.RequiredInsertCount > t.Inserted() {
		return s, ErrBlocked
	}

	base := s.RequiredInsertCount + delta
	if s.Sign {
		if delta >= s.RequiredInsertCount {
			return s, errors.New("negative base")
		}

		base = s.RequiredInsertCount - delta - 1
	}

	s.Fields = []FieldLine{}
	for len(block) > 0 {
		f, n, err := t.fieldLine(block, base)
		if err != nil {
			return s, err
		}

		s.Fields = append(s.Fields, f)
		block = block[n:]
	}

	return s, nil
}

// requiredInsertCount decodes a section's Required Insert Count (RFC 9204,
// section 4.5.1.1).
func (t *Table) requiredInsertCount(encoded uint64) (uint64, error) {
	if encoded == 0 {
		return 0, nil
	}

	maxEntries := t.maxCapacity / 32
	fullRange := 2 * maxEntries

	if encoded > fullRange {
		return 0, fmt.Errorf("encoded insert count %d above %d", encoded, fullRange)
	}

	maxValue := t.Inserted() + maxEntries
	ric := maxValue/fullRange*fullRange + encoded - 1

	if ric > maxValue {
		if ric <= fullRange {
			return 0, errors.New("bad encoded insert count")
		}

		ric -= fullRange
	}

	if ric == 0 {
		return 0, errors.New("bad encoded insert count")
	}

	return ric, nil
}

func (t *Table) fieldLine(b []byte, base uint64) (FieldLine, int, error) {
	var (
		f    FieldLine
		name [2]string
		n    int
		err  error
	)

	dynamic := func(absolute uint64, ok bool) {
		if !ok {
			err = errors.New("dynamic reference outside the table")
			return
		}

		name, err = t.entry(absolute)
	}

	switch {
	case b[0]&0x80 != 0:
		f.Rep, f.Static = RepIndexed, b[0]&0x40 != 0
		if f.Index, n, err = prefixInt(b, 6); err != nil {
			return f, 0, err
		}

		if f.Static {
			name, err = staticEntry(f.Index)
		} else {
			dynamic(base-1-f.Index, f.Index < base)
		}

		f.Name, f.Value = name[0], name[1]

		return f, n, err

	case b[0]&0x40 != 0:
		f.Rep, f.Never, f.Static = RepLiteralRef, b[0]&0x20 != 0, b[0]&0x10 != 0
		if f.Index, n, err = prefixInt(b, 4); err != nil {
			return f, 0, err
		}

		if f.Static {
			name, err = staticEntry(f.Index)
		} else {
			dynamic(base-1-f.Index, f.Index < base)
		}

	case b[0]&0x20 != 0:
		f.Rep, f.Never = RepLiteral, b[0]&0x10 != 0
		if f.Name, f.NameHuffman, n, err = prefixString(b, 3); err != nil {
			return f, 0, err
		}

		name[0] = f.Name

	case b[0]&0x10 != 0:
		f.Rep = RepIndexedPostBase
		if f.Index, n, err = prefixInt(b, 4); err != nil {
			return f, 0, err
		}

		dynamic(base+f.Index, true)

		f.Name, f.Value = name[0], name[1]

		return f, n, err

	default:
		f.Rep, f.Never = RepLiteralPostBaseRef, b[0]&0x08 != 0
		if f.Index, n, err = prefixInt(b, 3); err != nil {
			return f, 0, err
		}

		dynamic(base+f.Index, true)
	}

	if err != nil {
		return f, 0, err
	}

	f.Name = name[0]

	value, huff, m, err := prefixString(b[n:], 7)
	if err != nil {
		return f, 0, err
	}

	f.Value, f.ValueHuffman = value, huff

	return f, n + m, nil
}

// ReadDecoderStream reads the complete decoder instructions at the start
// of b and returns them with the bytes they took.
func ReadDecoderStream(b []byte) ([]DecoderInstruction, int, error) {
	var out []DecoderInstruction

	read := 0

	for read < len(b) {
		var (
			in  DecoderInstruction
			v   uint64
			n   int
			err error
		)

		switch c := b[read]; {
		case c&0x80 != 0:
			in.Op = OpSectionAck
			v, n, err = prefixInt(b[read:], 7)
			in.Stream = v

		case c&0x40 != 0:
			in.Op = OpStreamCancel
			v, n, err = prefixInt(b[read:], 6)
			in.Stream = v

		default:
			in.Op = OpInsertCountIncrement
			v, n, err = prefixInt(b[read:], 6)
			in.Increment = v
		}

		if errors.Is(err, errIncomplete) {
			break
		}

		if err != nil {
			return out, read, err
		}

		out = append(out, in)

		read += n
	}

	return out, read, nil
}

// appendPrefixInt appends v with an n-bit prefix, the bits above it in
// first.
func appendPrefixInt(dst []byte, first byte, n uint, v uint64) []byte {
	limit := uint64(1)<<n - 1
	if v < limit {
		return append(dst, first|byte(v))
	}

	dst = append(dst, first|byte(limit))
	for v -= limit; v >= 0x80; v >>= 7 {
		dst = append(dst, byte(v)|0x80)
	}

	return append(dst, byte(v))
}

// AppendSectionAck appends a decoder stream's Section Acknowledgment.
func AppendSectionAck(dst []byte, stream uint64) []byte {
	return appendPrefixInt(dst, 0x80, 7, stream)
}

// AppendInsertCountIncrement appends a decoder stream's Insert Count
// Increment.
func AppendInsertCountIncrement(dst []byte, increment uint64) []byte {
	return appendPrefixInt(dst, 0x00, 6, increment)
}

// EncodeSection encodes fields (name, value pairs) as a field section
// using only the static table: an entry where one matches name and value,
// else a literal with the static name where one matches the name, else a
// literal name; strings are not Huffman-coded.
func EncodeSection(fields [][2]string) []byte {
	out := []byte{0x00, 0x00}
	for _, f := range fields {
		exact, named := -1, -1
		for i, e := range staticTable {
			if e[0] != f[0] {
				continue
			}

			if named < 0 {
				named = i
			}

			if e[1] == f[1] {
				exact = i
				break
			}
		}

		switch {
		case exact >= 0:
			out = appendPrefixInt(out, 0xc0, 6, uint64(exact))
			continue

		case named >= 0:
			out = appendPrefixInt(out, 0x50, 4, uint64(named))
		default:
			out = appendPrefixInt(out, 0x20, 3, uint64(len(f[0])))
			out = append(out, f[0]...)
		}

		out = appendPrefixInt(out, 0x00, 7, uint64(len(f[1])))
		out = append(out, f[1]...)
	}

	return out
}
