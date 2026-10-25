package qpack

import (
	"errors"
	"fmt"
)

// ErrBlocked is a field section that refers to entries the encoder stream
// has not inserted yet: decode it again after more encoder stream data.
var ErrBlocked = errors.New("qpack: field section blocked on the encoder stream")

// ErrEncoderStream is an encoder stream instruction the decoder cannot
// apply: a connection error (QPACK_ENCODER_STREAM_ERROR).
var ErrEncoderStream = errors.New("qpack: encoder stream error")

// ErrDecompression is a field section the decoder cannot decode: a
// connection error (QPACK_DECOMPRESSION_FAILED).
var ErrDecompression = errors.New("qpack: decompression failed")

// Decoder decodes the peer's field sections and applies its encoder
// stream. It is not safe for concurrent use.
type Decoder struct {
	// max is the SETTINGS_QPACK_MAX_TABLE_CAPACITY sent to the peer.
	max uint64
	t   table
	// in holds an encoder stream instruction not yet complete.
	in []byte
}

// NewDecoder returns a decoder for a connection that advertised
// maxCapacity as its SETTINGS_QPACK_MAX_TABLE_CAPACITY.
func NewDecoder(maxCapacity uint64) *Decoder {
	return &Decoder{
		max: maxCapacity,
	}
}

// Inserted is how many entries the encoder stream has inserted.
func (d *Decoder) Inserted() uint64 {
	return d.t.inserted()
}

// OnEncoderStream applies the peer's encoder stream bytes. An instruction
// cut short is kept for the next call.
func (d *Decoder) OnEncoderStream(b []byte) error {
	d.in = append(d.in, b...)
	for len(d.in) > 0 {
		n, err := d.instruction(d.in)
		if errors.Is(err, errIncomplete) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrEncoderStream, err)
		}
		d.in = d.in[n:]
	}
	d.in = nil
	return nil
}

// instruction applies the encoder stream instruction at the start of b
// (RFC 9204, 4.3) and returns its length.
func (d *Decoder) instruction(b []byte) (int, error) {
	switch c := b[0]; {
	case c&0x80 != 0:
		index, n, err := readInt(b, 6)
		if err != nil {
			return 0, err
		}
		value, m, err := readString(b[n:], 7)
		if err != nil {
			return 0, err
		}
		var name string
		if c&0x40 != 0 {
			if index >= uint64(len(staticTable)) {
				return 0, fmt.Errorf("static index %d", index)
			}
			name = staticTable[index][0]
		} else {
			en, err := d.relative(index)
			if err != nil {
				return 0, err
			}
			name = en.Name
		}
		return n + m, d.insert(Field{
			Name:  name,
			Value: value,
		})
	case c&0x40 != 0:
		name, n, err := readString(b, 5)
		if err != nil {
			return 0, err
		}
		value, m, err := readString(b[n:], 7)
		if err != nil {
			return 0, err
		}
		return n + m, d.insert(Field{
			Name:  name,
			Value: value,
		})
	case c&0x20 != 0:
		capacity, n, err := readInt(b, 5)
		if err != nil {
			return 0, err
		}
		if capacity > d.max {
			return 0, fmt.Errorf("capacity %d above the maximum %d", capacity, d.max)
		}
		d.t.capacity = capacity
		d.t.evictTo(capacity)
		return n, nil
	default:
		index, n, err := readInt(b, 5)
		if err != nil {
			return 0, err
		}
		en, err := d.relative(index)
		if err != nil {
			return 0, err
		}
		return n, d.insert(en)
	}
}

// relative is the entry an encoder stream instruction's relative index
// names.
func (d *Decoder) relative(index uint64) (Field, error) {
	if index >= d.t.inserted() {
		return Field{}, fmt.Errorf("relative index %d", index)
	}
	en, ok := d.t.entry(d.t.inserted() - 1 - index)
	if !ok {
		return Field{}, fmt.Errorf("relative index %d is evicted", index)
	}
	return en, nil
}

func (d *Decoder) insert(f Field) error {
	if entrySize(f.Name, f.Value) > d.t.capacity {
		return errors.New("entry larger than the table")
	}
	d.t.insert(f)
	return nil
}

// Decode decodes a field section and returns its fields and Required
// Insert Count (the caller acknowledges a section whose count is not
// zero). It returns ErrBlocked if the section needs inserts not received
// yet.
func (d *Decoder) Decode(block []byte) ([]Field, uint64, error) {
	ric, base, n, err := d.prefix(block)
	if err != nil {
		return nil, 0, err
	}
	if ric > d.t.inserted() {
		return nil, ric, ErrBlocked
	}
	var fields []Field
	for b := block[n:]; len(b) > 0; {
		f, n, err := d.line(b, ric, base)
		if err != nil {
			return nil, ric, fmt.Errorf("%w: %w", ErrDecompression, err)
		}
		fields = append(fields, f)
		b = b[n:]
	}
	return fields, ric, nil
}

// prefix decodes a section's Required Insert Count and base (RFC 9204,
// 4.5.1).
func (d *Decoder) prefix(block []byte) (ric, base uint64, n int, err error) {
	enc, n, err := readInt(block, 8)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: %w", ErrDecompression, err)
	}
	if n >= len(block) {
		return 0, 0, 0, fmt.Errorf("%w: truncated prefix", ErrDecompression)
	}
	sign := block[n]&0x80 != 0
	delta, m, err := readInt(block[n:], 7)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: %w", ErrDecompression, err)
	}
	n += m
	if enc == 0 {
		return 0, 0, n, nil
	}
	maxEntries := d.max / 32
	full := 2 * maxEntries
	if enc > full {
		return 0, 0, 0, fmt.Errorf("%w: encoded insert count %d", ErrDecompression, enc)
	}
	maxValue := d.t.inserted() + maxEntries
	ric = maxValue/full*full + enc - 1
	if ric > maxValue {
		if ric <= full {
			return 0, 0, 0, fmt.Errorf("%w: encoded insert count %d", ErrDecompression, enc)
		}
		ric -= full
	}
	if ric == 0 {
		return 0, 0, 0, fmt.Errorf("%w: encoded insert count %d", ErrDecompression, enc)
	}
	if !sign {
		return ric, ric + delta, n, nil
	}
	if delta >= ric {
		return 0, 0, 0, fmt.Errorf("%w: delta base %d", ErrDecompression, delta)
	}
	return ric, ric - delta - 1, n, nil
}

// line decodes one field line (RFC 9204, 4.5.2-4.5.6).
func (d *Decoder) line(b []byte, ric, base uint64) (Field, int, error) {
	dynamic := func(abs uint64) (Field, error) {
		if abs >= ric {
			return Field{}, fmt.Errorf("index %d at or above the required insert count %d", abs, ric)
		}
		en, ok := d.t.entry(abs)
		if !ok {
			return Field{}, fmt.Errorf("index %d is evicted", abs)
		}
		return en, nil
	}
	relative := func(index uint64) (Field, error) {
		if index >= base {
			return Field{}, fmt.Errorf("relative index %d with base %d", index, base)
		}
		return dynamic(base - 1 - index)
	}
	static := func(index uint64) (Field, error) {
		if index >= uint64(len(staticTable)) {
			return Field{}, fmt.Errorf("static index %d", index)
		}
		return Field{
			Name:  staticTable[index][0],
			Value: staticTable[index][1],
		}, nil
	}
	var f Field
	var n int
	var err error
	switch c := b[0]; {
	case c&0x80 != 0:
		var index uint64
		if index, n, err = readInt(b, 6); err != nil {
			return Field{}, 0, err
		}
		if c&0x40 != 0 {
			f, err = static(index)
		} else {
			f, err = relative(index)
		}
		return f, n, err
	case c&0x40 != 0:
		var index uint64
		if index, n, err = readInt(b, 4); err != nil {
			return Field{}, 0, err
		}
		if c&0x10 != 0 {
			f, err = static(index)
		} else {
			f, err = relative(index)
		}
	case c&0x20 != 0:
		if f.Name, n, err = readString(b, 3); err != nil {
			return Field{}, 0, err
		}
	case c&0x10 != 0:
		var index uint64
		if index, n, err = readInt(b, 4); err != nil {
			return Field{}, 0, err
		}
		f, err = dynamic(base + index)
		return f, n, err
	default:
		var index uint64
		if index, n, err = readInt(b, 3); err != nil {
			return Field{}, 0, err
		}
		f, err = dynamic(base + index)
	}
	if err != nil {
		return Field{}, 0, err
	}
	value, m, err := readString(b[n:], 7)
	if err != nil {
		return Field{}, 0, err
	}
	f.Value = value
	return f, n + m, nil
}
