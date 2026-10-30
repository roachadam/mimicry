package mimicry

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"weak"

	"github.com/roachadam/mimicry/profile"
)

// snapshots are the decoded profiles Transports hold, by their fingerprint:
// Transports built from equal profiles share one decoded, validated copy
// (most of a Transport's memory) instead of each decoding its own. An entry
// goes when the last Transport holding its copy is collected. The copies
// are read-only: nothing may write into a profile a Transport holds.
var snapshots sync.Map // [sha256.Size]byte → weak.Pointer[profile.Profile]

// snapshot returns a validated copy of p that later changes to p do not
// reach, shared with every Transport built from an equal profile (decision
// 0044). Finding the shared copy reads all of p, as it must: a profile is
// plain data its caller may change at any time, so only its content says
// what it is. Only a profile no live Transport holds yet is encoded and
// decoded into a new copy; the encoding is compact, as it is never written
// (profile.Encode's indented form is for files).
func snapshot(p *profile.Profile) (*profile.Profile, error) {
	key := fingerprint(p)
	if v, ok := snapshots.Load(key); ok {
		if shared := snapshotOf(v); shared != nil {
			return shared, nil
		}
	}

	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("mimicry: %w", err)
	}

	decoded, err := profile.Parse(data)
	if err != nil {
		return nil, err
	}

	wp := weak.Make(decoded)

	for {
		v, loaded := snapshots.LoadOrStore(key, wp)
		if !loaded {
			break
		}

		// Another Transport stored one first: use it while it lives,
		// else replace the dead entry.
		if shared := snapshotOf(v); shared != nil {
			return shared, nil
		}

		if snapshots.CompareAndSwap(key, v, wp) {
			break
		}
	}

	runtime.AddCleanup(decoded, dropSnapshot, snapshotEntry{
		key: key,
		wp:  wp,
	})

	return decoded, nil
}

// snapshotEntry is a snapshots entry, for its cleanup.
type snapshotEntry struct {
	key [sha256.Size]byte
	wp  weak.Pointer[profile.Profile]
}

// dropSnapshot removes e from snapshots once its profile is collected,
// unless a newer copy has replaced it.
func dropSnapshot(e snapshotEntry) {
	snapshots.CompareAndDelete(e.key, e.wp)
}

// snapshotOf returns the profile of a snapshots value, nil once collected.
func snapshotOf(v any) *profile.Profile {
	wp, _ := v.(weak.Pointer[profile.Profile])

	return wp.Value()
}

// fingerprint is the SHA-256 of p's content in a binary form no other
// content has: every field in declaration order, a byte saying whether each
// pointer, slice and map is set, a length before each string, slice and
// map, and maps by sorted key. Equal fingerprints are equal profiles (nil
// and empty differ, which costs only sharing). The form is about 60% of
// the bytes of p's compact JSON and is written without encoding/json's
// work, into a pooled buffer.
func fingerprint(p *profile.Profile) [sha256.Size]byte {
	bp, _ := fingerprintBufs.Get().(*[]byte)
	if bp == nil {
		bp = new([]byte)
	}

	buf := appendValue((*bp)[:0], reflect.ValueOf(p).Elem())
	sum := sha256.Sum256(buf)

	*bp = buf
	fingerprintBufs.Put(bp)

	return sum
}

// fingerprintBufs are fingerprint's buffers, kept between Transports.
var fingerprintBufs sync.Pool // *[]byte

// appendValue appends v's binary form to buf. A profile holds no
// interfaces, channels, functions, complex numbers or recursive types
// (TestFingerprintCoversProfile).
func appendValue(buf []byte, v reflect.Value) []byte {
	switch v.Kind() {
	case reflect.Pointer:
		buf = appendBool(buf, !v.IsNil())
		if !v.IsNil() {
			buf = appendValue(buf, v.Elem())
		}

	case reflect.Slice:
		buf = appendBool(buf, !v.IsNil())
		buf = binary.AppendUvarint(buf, uint64(v.Len()))

		if v.Type().Elem().Kind() == reflect.Uint8 {
			return append(buf, v.Bytes()...)
		}

		for i := range v.Len() {
			buf = appendValue(buf, v.Index(i))
		}

	case reflect.Array:
		for i := range v.Len() {
			buf = appendValue(buf, v.Index(i))
		}

	case reflect.Map:
		buf = appendBool(buf, !v.IsNil())
		buf = binary.AppendUvarint(buf, uint64(v.Len()))

		keys := v.MapKeys()
		slices.SortFunc(keys, compareKeys)

		for _, k := range keys {
			buf = appendValue(buf, k)
			buf = appendValue(buf, v.MapIndex(k))
		}

	case reflect.Struct:
		// Not v.Fields: it builds each StructField, allocating its Index.
		n := v.NumField()
		for i := range n {
			buf = appendValue(buf, v.Field(i))
		}

	case reflect.String:
		buf = binary.AppendUvarint(buf, uint64(v.Len()))
		buf = append(buf, v.String()...)

	case reflect.Bool:
		buf = appendBool(buf, v.Bool())

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		buf = binary.AppendVarint(buf, v.Int())

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		buf = binary.AppendUvarint(buf, v.Uint())

	case reflect.Float32, reflect.Float64:
		buf = binary.AppendUvarint(buf, math.Float64bits(v.Float()))

	default:
		panic("mimicry: a profile holds a " + v.Kind().String())
	}

	return buf
}

// appendBool appends b as one byte.
func appendBool(buf []byte, b bool) []byte {
	if b {
		return append(buf, 1)
	}

	return append(buf, 0)
}

// compareKeys orders map keys: strings, then integers, as a profile's maps
// are keyed.
func compareKeys(a, b reflect.Value) int {
	switch a.Kind() {
	case reflect.String:
		return cmp.Compare(a.String(), b.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(a.Int(), b.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmp.Compare(a.Uint(), b.Uint())
	default:
		panic("mimicry: a profile has a map keyed by " + a.Kind().String())
	}
}
