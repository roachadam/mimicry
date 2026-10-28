package qpack

import (
	"bytes"
	"errors"

	xhpack "golang.org/x/net/http2/hpack"
)

// errIncomplete is an instruction that continues beyond the bytes given.
var errIncomplete = errors.New("qpack: incomplete instruction")

// maxStringLength bounds a string literal's encoded length, so a hostile
// peer cannot make the decoder allocate without limit.
const maxStringLength = 1 << 20

// appendInt appends v as a QPACK integer with an n-bit prefix, the high
// bits of the first byte set to first (RFC 9204, 4.1.1; RFC 7541, 5.1).
func appendInt(dst []byte, first byte, n uint8, v uint64) []byte {
	limit := uint64(1)<<n - 1
	if v < limit {
		return append(dst, first|byte(v))
	}

	dst = append(dst, first|byte(limit))

	v -= limit

	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)

		v >>= 7
	}

	return append(dst, byte(v))
}

// readInt reads an integer with an n-bit prefix from the start of b, and
// how many bytes it took.
func readInt(b []byte, n uint8) (uint64, int, error) {
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
			return 0, 0, errors.New("qpack: integer overflow")
		}

		v += uint64(b[i]&0x7f) << shift
		shift += 7

		if b[i]&0x80 == 0 {
			return v, i + 1, nil
		}
	}
}

// appendString appends s as a string literal whose length has an n-bit
// prefix, the Huffman flag the bit above it, the bits above that set to
// first. It is Huffman-coded if always, or if that makes it shorter.
func appendString(dst []byte, first byte, n uint8, s string, always bool) []byte {
	hl := xhpack.HuffmanEncodeLength(s)
	if always || hl < uint64(len(s)) {
		dst = appendInt(dst, first|1<<n, n, hl)
		return xhpack.AppendHuffmanString(dst, s)
	}

	dst = appendInt(dst, first, n, uint64(len(s)))

	return append(dst, s...)
}

// readString reads a string literal whose length has an n-bit prefix, the
// Huffman flag being the bit above it.
func readString(b []byte, n uint8) (string, int, error) {
	data, huffman, k, err := splitLiteral(b, n)
	if err != nil {
		return "", 0, err
	}

	if !huffman {
		return string(data), k, nil
	}

	s, err := xhpack.HuffmanDecodeToString(data)
	if err != nil {
		return "", 0, err
	}

	return s, k, nil
}

// bufString reads a string literal as readString does, decoded onto the
// end of buf instead of into a string of its own: the decoded bytes are
// buf's from start on.
func bufString(buf *bytes.Buffer, b []byte, n uint8) (start, k int, err error) {
	data, huffman, k, err := splitLiteral(b, n)
	if err != nil {
		return 0, 0, err
	}

	start = buf.Len()
	if !huffman {
		buf.Write(data)

		return start, k, nil
	}

	if _, err := xhpack.HuffmanDecode(buf, data); err != nil {
		return 0, 0, err
	}

	return start, k, nil
}

// splitLiteral splits a string literal with an n-bit length prefix off b: its
// encoded bytes, whether they are Huffman-coded, and the bytes it took.
func splitLiteral(b []byte, n uint8) (data []byte, huffman bool, k int, err error) {
	if len(b) == 0 {
		return nil, false, 0, errIncomplete
	}

	huffman = b[0]&(1<<n) != 0

	length, k, err := readInt(b, n)
	if err != nil {
		return nil, false, 0, err
	}

	if length > maxStringLength {
		return nil, false, 0, errors.New("qpack: string literal too long")
	}

	if uint64(len(b)-k) < length {
		return nil, false, 0, errIncomplete
	}

	return b[k : k+int(length)], huffman, k + int(length), nil
}

// entrySize is a table entry's size (RFC 9204, 3.2.1).
func entrySize(name, value string) uint64 {
	return uint64(len(name)+len(value)) + 32
}

// AppendSectionAck appends a decoder stream Section Acknowledgment for
// stream (RFC 9204, 4.4.1).
func AppendSectionAck(dst []byte, stream uint64) []byte {
	return appendInt(dst, 0x80, 7, stream)
}

// AppendStreamCancel appends a decoder stream Stream Cancellation for
// stream (RFC 9204, 4.4.2).
func AppendStreamCancel(dst []byte, stream uint64) []byte {
	return appendInt(dst, 0x40, 6, stream)
}
