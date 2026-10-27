package profile

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Code is a 16-bit TLS registry value: a cipher suite, extension type, named
// group, signature scheme, protocol version or certificate compression
// algorithm. In JSON it is a hex string such as "0x1301".
//
// All GREASE values (RFC 8701) are represented by GREASE and written as
// "GREASE": a browser picks them at random per connection, so a profile only
// records where they go.
type Code uint16

// GREASE stands for any GREASE value. It is the lowest one, 0x0a0a.
const GREASE Code = 0x0a0a

// IsGREASE reports whether v is one of the sixteen GREASE values.
func IsGREASE(v uint16) bool {
	return v&0x0f0f == 0x0a0a && v>>8 == v&0xff
}

// String returns "GREASE" or the value as four hex digits, "0x1301".
func (c Code) String() string {
	if c == GREASE {
		return "GREASE"
	}

	return fmt.Sprintf("0x%04x", uint16(c))
}

// MarshalText implements encoding.TextMarshaler.
func (c Code) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

// UnmarshalText accepts "GREASE" or "0x" followed by one to four hex digits.
// Any GREASE value is stored as GREASE.
func (c *Code) UnmarshalText(text []byte) error {
	s := string(text)
	if s == "GREASE" {
		*c = GREASE
		return nil
	}

	digits, ok := strings.CutPrefix(s, "0x")
	if !ok || digits == "" || len(digits) > 4 {
		return fmt.Errorf("invalid code %q: want \"GREASE\" or 0x followed by 1-4 hex digits", s)
	}

	v, err := strconv.ParseUint(digits, 16, 16)
	if err != nil {
		return fmt.Errorf("invalid code %q: %w", s, err)
	}

	if IsGREASE(uint16(v)) {
		*c = GREASE
		return nil
	}

	*c = Code(v)

	return nil
}

// Bytes is raw data written in JSON as a hex string.
type Bytes []byte

// MarshalText implements encoding.TextMarshaler.
func (b Bytes) MarshalText() ([]byte, error) {
	return []byte(hex.EncodeToString(b)), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (b *Bytes) UnmarshalText(text []byte) error {
	decoded, err := hex.DecodeString(string(text))
	if err != nil {
		return fmt.Errorf("invalid hex %q: %w", text, err)
	}

	*b = decoded

	return nil
}

// ByteList is a list of single-byte values written in JSON as an array of
// numbers rather than encoding/json's default base64 string.
type ByteList []uint8

// MarshalJSON implements json.Marshaler.
func (l ByteList) MarshalJSON() ([]byte, error) {
	values := make([]int, len(l))
	for i, v := range l {
		values[i] = int(v)
	}

	return json.Marshal(values)
}

// UnmarshalJSON implements json.Unmarshaler.
func (l *ByteList) UnmarshalJSON(data []byte) error {
	var values []int
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}

	out := make(ByteList, len(values))
	for i, v := range values {
		if v < 0 || v > 0xff {
			return fmt.Errorf("value %d does not fit in a byte", v)
		}

		out[i] = uint8(v)
	}

	*l = out

	return nil
}
