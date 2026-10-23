package h2trace

import (
	"errors"
	"fmt"

	"golang.org/x/net/http2/hpack"
)

// Trace is everything a client sent on one HTTP/2 connection, frame by frame,
// with each request's header block as encoded on the wire.
type Trace struct {
	// Host is the connection's server name, recorded when a page spans
	// several hosts (replay scenarios).
	Host     string    `json:"host,omitempty"`
	Frames   []Frame   `json:"frames"`
	Requests []Request `json:"requests"`
}

// Frame is one frame the client sent. Payload details are kept only where
// they describe client behaviour.
type Frame struct {
	// Millis since the connection preface.
	Millis int64  `json:"ms"`
	Type   string `json:"type"`
	Stream uint32 `json:"stream,omitempty"`
	Flags  uint8  `json:"flags,omitempty"`
	Length uint32 `json:"length"`
	// Increment of a WINDOW_UPDATE.
	Increment uint32 `json:"increment,omitempty"`
	// SentBefore, for a stream's WINDOW_UPDATE, is how many DATA bytes the
	// server had committed to that stream when it arrived. An increment
	// above it cannot be credit for data received: it is a top-up.
	SentBefore uint64 `json:"sent_before,omitempty"`
	// ErrCode of an RST_STREAM or GOAWAY.
	ErrCode string `json:"err_code,omitempty"`
	// Data of a PING, hex.
	Data string `json:"data,omitempty"`
	// Priority of a PRIORITY frame or HEADERS frame.
	Priority *Priority `json:"priority,omitempty"`
}

// Priority is a stream's priority as sent.
type Priority struct {
	DependsOn uint32 `json:"depends_on"`
	Exclusive bool   `json:"exclusive"`
	Weight    uint8  `json:"weight"`
}

// Request is one request's header block.
type Request struct {
	Stream uint32 `json:"stream"`
	Path   string `json:"path"`
	// Block is the complete header block (HEADERS plus CONTINUATION), hex.
	Block        string        `json:"block"`
	Instructions []Instruction `json:"instructions"`
}

// Representation is how a header field was encoded (RFC 7541, section 6).
type Representation string

// Representations.
const (
	Indexed     Representation = "indexed"     // 6.1: a table entry, name and value
	Incremental Representation = "incremental" // 6.2.1: literal, added to the dynamic table
	Without     Representation = "without"     // 6.2.2: literal, not added
	Never       Representation = "never"       // 6.2.3: literal, never to be indexed
	SizeUpdate  Representation = "size_update" // 6.3: dynamic table size update
)

// Instruction is one HPACK instruction of a header block.
type Instruction struct {
	Rep Representation `json:"rep"`
	// Index is the table index referenced: the entry for Indexed, the name for
	// literals (0 means the name is a literal string).
	Index uint64 `json:"index,omitempty"`
	// Size of a SizeUpdate.
	Size  uint64 `json:"size,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
	// HuffmanName and HuffmanValue report Huffman-coded literal strings.
	HuffmanName  bool `json:"huffman_name,omitempty"`
	HuffmanValue bool `json:"huffman_value,omitempty"`
}

// Decode splits a header block into instructions, decoding names and values
// with dec, which must be the connection's decoder so dynamic-table
// references resolve.
func Decode(dec *hpack.Decoder, block []byte) ([]Instruction, error) {
	fields, err := dec.DecodeFull(block)
	if err != nil {
		return nil, err
	}
	var out []Instruction
	p := parser{
		b: block,
	}
	for len(p.b) > 0 {
		ins, err := p.next()
		if err != nil {
			return nil, err
		}
		if ins.Rep != SizeUpdate {
			if len(fields) == 0 {
				return nil, errors.New("h2trace: more instructions than decoded fields")
			}
			ins.Name, ins.Value = fields[0].Name, fields[0].Value
			fields = fields[1:]
		}
		out = append(out, ins)
	}
	if len(fields) > 0 {
		return nil, errors.New("h2trace: fewer instructions than decoded fields")
	}
	return out, nil
}

type parser struct {
	b []byte
}

func (p *parser) next() (Instruction, error) {
	first := p.b[0]
	switch {
	case first&0x80 != 0:
		i, err := p.integer(7)
		return Instruction{
			Rep:   Indexed,
			Index: i,
		}, err
	case first&0xc0 == 0x40:
		return p.literal(Incremental, 6)
	case first&0xe0 == 0x20:
		s, err := p.integer(5)
		return Instruction{
			Rep:  SizeUpdate,
			Size: s,
		}, err
	case first&0xf0 == 0x10:
		return p.literal(Never, 4)
	default:
		return p.literal(Without, 4)
	}
}

func (p *parser) literal(rep Representation, prefix uint8) (Instruction, error) {
	ins := Instruction{
		Rep: rep,
	}
	idx, err := p.integer(prefix)
	if err != nil {
		return ins, err
	}
	ins.Index = idx
	if idx == 0 {
		if ins.HuffmanName, err = p.str(); err != nil {
			return ins, err
		}
	}
	ins.HuffmanValue, err = p.str()
	return ins, err
}

// str skips a string literal and reports whether it was Huffman-coded.
func (p *parser) str() (bool, error) {
	if len(p.b) == 0 {
		return false, errors.New("h2trace: truncated string")
	}
	huffman := p.b[0]&0x80 != 0
	n, err := p.integer(7)
	if err != nil {
		return false, err
	}
	if uint64(len(p.b)) < n {
		return false, errors.New("h2trace: truncated string")
	}
	p.b = p.b[n:]
	return huffman, nil
}

// integer reads an HPACK integer with an n-bit prefix (RFC 7541, 5.1).
func (p *parser) integer(n uint8) (uint64, error) {
	limit := uint64(1)<<n - 1
	v := uint64(p.b[0]) & limit
	p.b = p.b[1:]
	if v < limit {
		return v, nil
	}
	var shift uint
	for {
		if len(p.b) == 0 {
			return 0, errors.New("h2trace: truncated integer")
		}
		c := p.b[0]
		p.b = p.b[1:]
		v += uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, nil
		}
		shift += 7
		if shift > 56 {
			return 0, fmt.Errorf("h2trace: integer too long")
		}
	}
}
