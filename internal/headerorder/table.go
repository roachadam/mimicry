package headerorder

import (
	"math/bits"
	"slices"

	"github.com/roachadam/mimicry/profile"
)

// TableOrder returns the order browser table t sends a request's table
// headers in. members are the list's table names the request sends, page
// the page's own names in insertion order (sorted for fetch(), as set for
// XHR). ok is false when t cannot hold members in the list's order. See
// decision 0021.
func TableOrder(t *profile.HeaderTable, members, page []string) (order []string, ok bool) {
	var own, late []string
	pageNames := slices.Clone(page)
	for _, name := range members {
		switch {
		case slices.Contains(pageNames, name):
		case slices.Contains(t.AsPage, name):
			at := slices.IndexFunc(pageNames, func(p string) bool {
				return p > name
			})
			if at < 0 {
				at = len(pageNames)
			}
			pageNames = slices.Insert(pageNames, at, name)
		case slices.Contains(t.Late, name):
			late = append(late, name)
		default:
			own = append(own, name)
		}
	}

	tb := newHashTable(t.Hash)
	entering := slices.Concat(pageNames, own, late)
	if !t.PageFirst {
		// The browser's own are in the table as the list has them.
		if !tb.layout(own) {
			return nil, false
		}
		entering = slices.Concat(pageNames, late)
	}
	for _, name := range entering {
		if !tb.insert(name) {
			return nil, false
		}
	}
	return tb.names(), true
}

// hashTable is an open-addressing hash table of header names, iterated in
// bucket order.
type hashTable interface {
	// insert adds name, growing the table as the browser's does; false
	// when the table would outgrow what is modelled.
	insert(name string) bool
	// layout fills an empty table, sized for names, with names in
	// order: the table as the list shows it. False when the table's order
	// is not names'.
	layout(names []string) bool
	// names returns the table's names in bucket order.
	names() []string
}

func newHashTable(hash string) hashTable {
	if hash == profile.HashCF {
		return &cfTable{}
	}
	return &blinkTable{}
}

// placeAll places names in an empty table and reports whether it then
// iterates them in that order.
func placeAll(buckets []string, names []string, place func(string)) bool {
	for _, name := range names {
		place(name)
	}
	return slices.Equal(occupied(buckets), names)
}

// occupied returns buckets' names in bucket order ("" is empty).
func occupied(buckets []string) []string {
	out := make([]string, 0, len(buckets))
	for _, b := range buckets {
		if b != "" {
			out = append(out, b)
		}
	}
	return out
}

// blinkTable is Blink's WTF::HashTable as HTTPHeaderMap uses it: 8
// buckets, doubled at half full, triangular probing, rehashed in bucket
// order.
type blinkTable struct {
	buckets []string
	count   int
}

func (t *blinkTable) insert(name string) bool {
	if t.buckets == nil {
		t.buckets = make([]string, blinkMinimumSize)
	}
	if !t.place(name) {
		return true
	}
	t.count++
	if t.count*2 >= len(t.buckets) {
		old := t.buckets
		t.buckets = make([]string, 2*len(old))
		for _, b := range old {
			if b != "" {
				t.place(b)
			}
		}
	}
	return true
}

// place puts name in its bucket; false when it is already there.
func (t *blinkTable) place(name string) bool {
	mask := uint32(len(t.buckets) - 1)
	i := blinkHash(name) & mask
	for probe := uint32(1); t.buckets[i] != ""; probe++ {
		if t.buckets[i] == name {
			return false
		}
		i = (i + probe) & mask
	}
	t.buckets[i] = name
	return true
}

func (t *blinkTable) layout(names []string) bool {
	size := blinkMinimumSize
	for len(names)*2 >= size {
		size *= 2
	}
	t.buckets, t.count = make([]string, size), len(names)
	return placeAll(t.buckets, names, func(name string) {
		t.place(name)
	})
}

func (t *blinkTable) names() []string {
	return occupied(t.buckets)
}

const blinkMinimumSize = 8

// rapidhash's seed and secrets (third_party/rapidhash/rapidhash.h).
const (
	rapidSeed = 0xbdd89aa982704029
	rapid0    = 0x2d358dccaa6c78a5
	rapid1    = 0x8bb84b93962eacc9
	rapid2    = 0x4b33a62ed433d4a3
)

func rapidMix(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return lo ^ hi
}

// blinkHash is Blink's DeprecatedCaseFoldingHash of a header name: rapidhash
// of the case-folded name as UTF-16 (CaseFoldingHashReader), keeping the
// low 24 bits, never 0 (string_hasher.h, case_folding_hash.h).
func blinkHash(name string) uint32 {
	n := len(name)
	length := uint64(2 * n)
	ch := func(i int) uint64 {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		return uint64(c)
	}
	read32 := func(i int) uint64 {
		return ch(i) | ch(i+1)<<16
	}
	read64 := func(i int) uint64 {
		return ch(i) | ch(i+1)<<16 | ch(i+2)<<32 | ch(i+3)<<48
	}
	seed := uint64(rapidSeed)
	seed ^= rapidMix(seed^rapid0, rapid1) ^ length
	var a, b uint64
	switch {
	case length > 16:
		// Offsets count bytes of UTF-16, two per character.
		left, p := 2*n, 0
		if left > 48 {
			see1, see2 := seed, seed
			for left >= 48 {
				seed = rapidMix(read64(p)^rapid0, read64(p+4)^seed)
				see1 = rapidMix(read64(p+8)^rapid1, read64(p+12)^see1)
				see2 = rapidMix(read64(p+16)^rapid2, read64(p+20)^see2)
				p += 24
				left -= 48
			}
			seed ^= see1 ^ see2
		}
		if left > 16 {
			seed = rapidMix(read64(p)^rapid2, read64(p+4)^seed^rapid1)
			if left > 32 {
				seed = rapidMix(read64(p+8)^rapid2, read64(p+12)^seed)
			}
		}
		a = read64(p + (left-16)/2)
		b = read64(p + (left-8)/2)
	case length >= 4:
		last := n - 2
		a = read32(0)<<32 | read32(last)
		delta := (((2 * n) & 24) >> ((2 * n) >> 3)) / 2
		b = read32(delta)<<32 | read32(last-delta)
	case length > 0:
		a = ch(0)
	}
	a ^= rapid1
	b ^= seed
	b, a = bits.Mul64(a, b)
	h := uint32(rapidMix(a^rapid0^length, b^rapid1) & (1<<24 - 1))
	if h == 0 {
		h = 1 << 23
	}
	return h
}

// cfTable is CFBasicHash as a CFDictionary uses it (CoreFoundation
// CFBasicHash.c): prime bucket counts, grown before an insert that would
// pass the count's capacity, linear probing, rehashed in bucket order.
type cfTable struct {
	buckets []string
	count   int
}

// cfSizes and cfCapacities are CFBasicHash's bucket counts and the names
// each holds before growing (__CFBasicHashTableSizes, ...Capacities), up to
// far more headers than any request carries.
var (
	cfSizes      = []int{0, 3, 7, 13, 23, 41, 71, 127, 191, 251, 383, 631, 1087, 1723, 2803}
	cfCapacities = []int{0, 3, 6, 11, 19, 32, 52, 85, 118, 155, 237, 390, 672, 1065, 1732}
)

// cfSizeFor is the bucket count of the smallest table holding n names, or 0
// past the modelled ones.
func cfSizeFor(n int) int {
	for i, c := range cfCapacities {
		if c >= n {
			return cfSizes[i]
		}
	}
	return 0
}

func (t *cfTable) insert(name string) bool {
	if slices.Contains(t.buckets, name) {
		return true
	}
	size := cfSizeFor(t.count + 1)
	if size == 0 {
		return false
	}
	if size > len(t.buckets) {
		old := t.buckets
		t.buckets = make([]string, size)
		for _, b := range old {
			if b != "" {
				t.place(b)
			}
		}
	}
	t.place(name)
	t.count++
	return true
}

// place puts name in the first empty bucket from its own.
func (t *cfTable) place(name string) {
	n := uint64(len(t.buckets))
	i := cfHash(name) % n
	for t.buckets[i] != "" {
		i = (i + 1) % n
	}
	t.buckets[i] = name
}

func (t *cfTable) layout(names []string) bool {
	if len(names) == 0 {
		return true
	}
	size := cfSizeFor(len(names))
	if size == 0 {
		return false
	}
	t.buckets, t.count = make([]string, size), len(names)
	return placeAll(t.buckets, names, t.place)
}

func (t *cfTable) names() []string {
	return occupied(t.buckets)
}

// cfHash is CFString's hash (__CFStrHashCharacters, 64-bit CFHashCode) of
// the lowercased name: every character up to 96, else the first, middle
// and last 32.
func cfHash(name string) uint64 {
	n := len(name)
	ch := func(i int) uint64 {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		return uint64(c)
	}
	r := uint64(n)
	four := func(from int) {
		for i := from; i < from+32; i += 4 {
			r = r*67503105 + ch(i)*16974593 + ch(i+1)*66049 + ch(i+2)*257 + ch(i+3)
		}
	}
	if n <= 96 {
		i := 0
		for ; i+4 <= n; i += 4 {
			r = r*67503105 + ch(i)*16974593 + ch(i+1)*66049 + ch(i+2)*257 + ch(i+3)
		}
		for ; i < n; i++ {
			r = r*257 + ch(i)
		}
	} else {
		four(0)
		four(n/2 - 16)
		four(n - 32)
	}
	return r + r<<(uint(n)&31)
}
