package profile

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"weak"
)

// Profiles of equal content share one compiled (decision 0044, amended): a
// profile is most of a client's memory. The maps hold them weakly: an
// entry goes once no Profile, and no client built from one, holds its
// compiled (a client holds its Spec, inside the compiled). The shared
// copies are read-only: nothing may write into a Spec a Profile holds.
var (
	// byContent holds them by their content's fingerprint.
	byContent sync.Map // [sha256.Size]byte → weak.Pointer[compiled]
	// lastByName holds, by name, the one last found or made for a Spec of
	// that name: an edit is usually of a profile With has just seen.
	lastByName sync.Map // string → weak.Pointer[compiled]
)

// find returns the shared compiled holding what s holds, or nil and the
// fingerprint to store a new one under. It compares s with the last
// compiled of s's name first, which needs no fingerprint, then looks the
// fingerprint up: both read all of s, as they must, since only its content
// says what it is.
func find(s *Spec) (*compiled, [sha256.Size]byte) {
	if c := load(&lastByName, s.Name); c != nil && equal(s, &c.spec) {
		return c, [sha256.Size]byte{}
	}

	key := fingerprint(s)
	if c := load(&byContent, key); c != nil {
		lastByName.Store(s.Name, weak.Make(c))

		return c, key
	}

	return nil, key
}

// load returns the compiled m holds under key, nil once collected.
func load(m *sync.Map, key any) *compiled {
	v, ok := m.Load(key)
	if !ok {
		return nil
	}

	wc, _ := v.(weak.Pointer[compiled])

	return wc.Value()
}

// keep stores c, of content not shared yet, under key and returns it, or
// returns the equal one another goroutine stored first.
func keep(key [sha256.Size]byte, c *compiled) *compiled {
	wc := weak.Make(c)

	for {
		v, loaded := byContent.LoadOrStore(key, wc)
		if !loaded {
			break
		}

		// Another goroutine stored one first: use it while it lives,
		// else replace the dead entry.
		old, _ := v.(weak.Pointer[compiled])
		if shared := old.Value(); shared != nil {
			return shared
		}

		if byContent.CompareAndSwap(key, v, wc) {
			break
		}
	}

	lastByName.Store(c.spec.Name, wc)
	runtime.AddCleanup(c, drop, entry{
		key:  key,
		name: c.spec.Name,
		wc:   wc,
	})

	return c
}

// entry is where keep stored a compiled, for its cleanup.
type entry struct {
	key  [sha256.Size]byte
	name string
	wc   weak.Pointer[compiled]
}

// drop removes a collected compiled's entries, unless newer ones have
// replaced them.
func drop(e entry) {
	byContent.CompareAndDelete(e.key, e.wc)
	lastByName.CompareAndDelete(e.name, e.wc)
}

// checkEdited checks s, a Spec an edit has changed, and returns its
// compiled: the shared one of equal content, else a new one. The new one
// holds a copy made by encoding and decoding s, as the edit may have kept s
// to change it later.
func checkEdited(s *Spec) *compiled {
	c, key := find(s)
	if c != nil {
		return c
	}

	data, err := json.Marshal(s)
	if err != nil {
		return failed(s, fmt.Errorf("profile: %w", err))
	}

	d, err := Decode(data)
	if err != nil {
		return failed(s, err)
	}

	c = &compiled{
		spec: *d,
	}
	if err := c.spec.Validate(); err != nil {
		c.err = err

		return c
	}

	return keep(key, c)
}

// failed is the compiled of s, which cannot be encoded: a copy of s and
// err.
func failed(s *Spec, err error) *compiled {
	c := &compiled{
		err: err,
	}
	copySpec(&c.spec, s)

	return c
}

// walk is how copySpec and equal visit the values of one type, worked out
// once for each type rather than at every value.
type walk struct {
	kind reflect.Kind
	// whole says the type holds no pointer, slice or map (strings, which
	// are immutable, are allowed): assigning a value copies all of it,
	// and == compares all of it.
	whole bool
	// elem walks a pointer's, slice's, array's or map's element; fields a
	// struct's fields.
	elem   *walk
	fields []*walk
}

// specWalk is Spec's walk.
var specWalk = sync.OnceValue(func() *walk {
	return walkOf(reflect.TypeFor[Spec](), make(map[reflect.Type]*walk))
})

// walkOf works out t's walk. A profile holds no interfaces, channels or
// functions, and no type that contains itself
// (TestFingerprintCoversProfile).
func walkOf(t reflect.Type, seen map[reflect.Type]*walk) *walk {
	if w, ok := seen[t]; ok {
		return w
	}

	w := &walk{
		kind: t.Kind(),
	}

	seen[t] = w

	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		w.elem = walkOf(t.Elem(), seen)
	case reflect.Array:
		w.elem = walkOf(t.Elem(), seen)
		w.whole = w.elem.whole

	case reflect.Struct:
		w.whole = true
		for f := range t.Fields() {
			fw := walkOf(f.Type, seen)

			w.fields = append(w.fields, fw)
			w.whole = w.whole && fw.whole
		}

	case reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
	default:
		w.whole = true
	}

	return w
}

// copySpec copies src into dst, which is empty, sharing nothing a caller
// can change: every pointer, slice and map is new; strings, which are
// immutable, are shared.
func copySpec(dst, src *Spec) {
	copyValue(reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem(), specWalk())
}

// copyValue copies src, walked by w, into dst, its type's zero value.
func copyValue(dst, src reflect.Value, w *walk) {
	if w.whole {
		dst.Set(src)

		return
	}

	switch w.kind {
	case reflect.Pointer:
		if !src.IsNil() {
			n := reflect.New(src.Type().Elem())
			copyValue(n.Elem(), src.Elem(), w.elem)
			dst.Set(n)
		}

	case reflect.Slice:
		if !src.IsNil() {
			n := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
			if w.elem.whole {
				reflect.Copy(n, src)
			} else {
				for i := range src.Len() {
					copyValue(n.Index(i), src.Index(i), w.elem)
				}
			}

			dst.Set(n)
		}

	case reflect.Array:
		for i := range src.Len() {
			copyValue(dst.Index(i), src.Index(i), w.elem)
		}

	case reflect.Map:
		if !src.IsNil() {
			n := reflect.MakeMapWithSize(src.Type(), src.Len())
			for it := src.MapRange(); it.Next(); {
				v := reflect.New(src.Type().Elem()).Elem()
				copyValue(v, it.Value(), w.elem)
				n.SetMapIndex(it.Key(), v)
			}

			dst.Set(n)
		}

	case reflect.Struct:
		for i, fw := range w.fields {
			copyValue(dst.Field(i), src.Field(i), fw)
		}

	default:
		panic("profile: a profile holds a " + src.Kind().String())
	}
}

// equal says whether a and b hold the same content, exactly as their
// fingerprints would: nil and empty differ.
func equal(a, b *Spec) bool {
	return equalValue(reflect.ValueOf(a).Elem(), reflect.ValueOf(b).Elem(), specWalk())
}

// equalValue says whether a and b, walked by w, hold the same content.
func equalValue(a, b reflect.Value, w *walk) bool {
	if w.whole {
		return a.Equal(b)
	}

	switch w.kind {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}

		return equalValue(a.Elem(), b.Elem(), w.elem)

	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}

		if w.elem.kind == reflect.Uint8 {
			return bytes.Equal(a.Bytes(), b.Bytes())
		}

		for i := range a.Len() {
			if !equalValue(a.Index(i), b.Index(i), w.elem) {
				return false
			}
		}

		return true

	case reflect.Array:
		for i := range a.Len() {
			if !equalValue(a.Index(i), b.Index(i), w.elem) {
				return false
			}
		}

		return true

	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}

		for it := a.MapRange(); it.Next(); {
			bv := b.MapIndex(it.Key())
			if !bv.IsValid() || !equalValue(it.Value(), bv, w.elem) {
				return false
			}
		}

		return true

	case reflect.Struct:
		for i, fw := range w.fields {
			if !equalValue(a.Field(i), b.Field(i), fw) {
				return false
			}
		}

		return true

	default:
		panic("profile: a profile holds a " + a.Kind().String())
	}
}

// fingerprint is the SHA-256 of s's content in a binary form no other
// content has: every field in declaration order, a byte saying whether each
// pointer, slice and map is set, a length before each string, slice and
// map, and maps by sorted key. Equal fingerprints are equal profiles (nil
// and empty differ, which costs only sharing). The form is about 60% of
// the bytes of s's compact JSON and is written without encoding/json's
// work, in chunks into the hash: no buffer the size of the profile.
func fingerprint(s *Spec) [sha256.Size]byte {
	f := fingerprinter{
		h:   sha256.New(),
		buf: make([]byte, 0, 2*fingerprintChunk),
	}
	f.value(reflect.ValueOf(s).Elem())
	f.h.Write(f.buf)

	var sum [sha256.Size]byte

	f.h.Sum(sum[:0])

	return sum
}

// fingerprintChunk is how much of the binary form fingerprint writes into
// the hash at a time.
const fingerprintChunk = 4096

// fingerprinter writes a value's binary form into h, through buf.
type fingerprinter struct {
	h   hash.Hash
	buf []byte
}

// value writes v's binary form. A profile holds no interfaces, channels,
// functions, complex numbers or recursive types
// (TestFingerprintCoversProfile).
func (f *fingerprinter) value(v reflect.Value) {
	if len(f.buf) >= fingerprintChunk {
		f.h.Write(f.buf)

		f.buf = f.buf[:0]
	}

	switch v.Kind() {
	case reflect.Pointer:
		f.bool(!v.IsNil())

		if !v.IsNil() {
			f.value(v.Elem())
		}

	case reflect.Slice:
		f.bool(!v.IsNil())

		f.buf = binary.AppendUvarint(f.buf, uint64(v.Len()))

		if v.Type().Elem().Kind() == reflect.Uint8 {
			f.buf = append(f.buf, v.Bytes()...)

			return
		}

		for i := range v.Len() {
			f.value(v.Index(i))
		}

	case reflect.Array:
		for i := range v.Len() {
			f.value(v.Index(i))
		}

	case reflect.Map:
		f.bool(!v.IsNil())

		f.buf = binary.AppendUvarint(f.buf, uint64(v.Len()))

		keys := v.MapKeys()
		slices.SortFunc(keys, compareKeys)

		for _, k := range keys {
			f.value(k)
			f.value(v.MapIndex(k))
		}

	case reflect.Struct:
		// Not v.Fields: it builds each StructField, allocating its Index.
		n := v.NumField()
		for i := range n {
			f.value(v.Field(i))
		}

	case reflect.String:
		f.buf = binary.AppendUvarint(f.buf, uint64(v.Len()))
		f.buf = append(f.buf, v.String()...)

	case reflect.Bool:
		f.bool(v.Bool())

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f.buf = binary.AppendVarint(f.buf, v.Int())

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		f.buf = binary.AppendUvarint(f.buf, v.Uint())

	case reflect.Float32, reflect.Float64:
		f.buf = binary.AppendUvarint(f.buf, math.Float64bits(v.Float()))

	default:
		panic("profile: a profile holds a " + v.Kind().String())
	}
}

// bool writes b as one byte.
func (f *fingerprinter) bool(b bool) {
	if b {
		f.buf = append(f.buf, 1)

		return
	}

	f.buf = append(f.buf, 0)
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
		panic("profile: a profile has a map keyed by " + a.Kind().String())
	}
}
