package zdeflate

import (
	"encoding/binary"
)

// trees.c's constants.
const (
	lengthCodes = 29
	literals    = 256
	lCodes      = literals + 1 + lengthCodes
	dCodes      = 30
	blCodes     = 19
	heapSize    = 2*lCodes + 1
	maxBits     = 15
	maxBLBits   = 7
	endBlock    = 256

	// rep3To6 repeats the previous bit length 3-6 times (2 extra bits),
	// repz3To10 a zero length 3-10 times (3), repz11To138 11-138 times (7).
	rep3To6     = 16
	repz3To10   = 17
	repz11To138 = 18

	storedBlock = 0
	staticTrees = 1
	dynTrees    = 2
)

var (
	extraLBits = [lengthCodes]int{
		0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0,
	}
	extraDBits = [dCodes]int{
		0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13,
	}
	extraBLBits = [blCodes]int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 3, 7,
	}
	// blOrder is the order bit length code lengths are sent in.
	blOrder = [blCodes]uint8{
		16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15,
	}
)

// ct is zlib's ct_data, its unions kept: fc is a node's frequency while a
// tree is built and its code once gen_codes ran; dl its parent while the
// tree is built and its bit length after. Code paths that read one name
// after the other was written depend on the sharing.
type ct struct {
	fc uint16
	dl uint16
}

// staticDesc is zlib's static_tree_desc.
type staticDesc struct {
	staticTree []ct
	extraBits  []int
	extraBase  int
	elems      int
	maxLength  int
}

// treeDesc is zlib's tree_desc.
type treeDesc struct {
	dynTree []ct
	maxCode int
	stat    *staticDesc
}

// static holds trees.c's tables, built as tr_static_init builds them.
var static = buildStatic()

type staticTables struct {
	ltree      [lCodes + 2]ct
	dtree      [dCodes]ct
	distCode   [512]uint8
	lengthCode [maxMatch - minMatch + 1]uint8
	baseLength [lengthCodes]int
	baseDist   [dCodes]int

	lDesc  staticDesc
	dDesc  staticDesc
	blDesc staticDesc
}

func buildStatic() *staticTables {
	t := &staticTables{}
	length := 0
	code := 0

	for ; code < lengthCodes-1; code++ {
		t.baseLength[code] = length
		for range 1 << extraLBits[code] {
			t.lengthCode[length] = uint8(code)
			length++
		}
	}

	// Length 258 has a code of its own: 285 rather than 284 with 31.
	t.lengthCode[length-1] = uint8(code)

	dist := 0
	for code := range 16 {
		t.baseDist[code] = dist
		for range 1 << extraDBits[code] {
			t.distCode[dist] = uint8(code)
			dist++
		}
	}

	dist >>= 7 // from here on, distances are divided by 128
	for code := 16; code < dCodes; code++ {
		t.baseDist[code] = dist << 7
		for range 1 << (extraDBits[code] - 7) {
			t.distCode[256+dist] = uint8(code)
			dist++
		}
	}

	var blCount [maxBits + 1]uint16

	n := 0

	for ; n <= 143; n++ {
		t.ltree[n].dl = 8
		blCount[8]++
	}

	for ; n <= 255; n++ {
		t.ltree[n].dl = 9
		blCount[9]++
	}

	for ; n <= 279; n++ {
		t.ltree[n].dl = 7
		blCount[7]++
	}

	for ; n <= 287; n++ {
		t.ltree[n].dl = 8
		blCount[8]++
	}

	genCodes(t.ltree[:], lCodes+1, &blCount)

	for n := range dCodes {
		t.dtree[n] = ct{
			fc: reverseBits(uint16(n), 5),
			dl: 5,
		}
	}

	t.lDesc = staticDesc{
		staticTree: t.ltree[:],
		extraBits:  extraLBits[:],
		extraBase:  literals + 1,
		elems:      lCodes,
		maxLength:  maxBits,
	}
	t.dDesc = staticDesc{
		staticTree: t.dtree[:],
		extraBits:  extraDBits[:],
		elems:      dCodes,
		maxLength:  maxBits,
	}
	t.blDesc = staticDesc{
		extraBits: extraBLBits[:],
		elems:     blCodes,
		maxLength: maxBLBits,
	}

	return t
}

// dCode is zlib's d_code: the distance code of a distance - 1.
func dCode(dist uint32) uint8 {
	if dist < 256 {
		return static.distCode[dist]
	}

	return static.distCode[256+(dist>>7)]
}

// reverseBits is zlib's bi_reverse: the low n bits of code reversed.
func reverseBits(code uint16, n int) uint16 {
	var res uint16
	for range n {
		res = res<<1 | code&1

		code >>= 1
	}

	return res
}

// genCodes is zlib's gen_codes: canonical codes for the bit lengths of
// tree[0..maxCode], given how many codes have each length.
func genCodes(tree []ct, maxCode int, blCount *[maxBits + 1]uint16) {
	var (
		nextCode [maxBits + 1]uint16
		code     uint16
	)

	for bits := 1; bits <= maxBits; bits++ {
		code = (code + blCount[bits-1]) << 1
		nextCode[bits] = code
	}

	for n := 0; n <= maxCode; n++ {
		length := tree[n].dl
		if length == 0 {
			continue
		}

		tree[n].fc = reverseBits(nextCode[length], int(length))
		nextCode[length]++
	}
}

// sym is one symbol of a block: a literal (dist 0) or a match.
type sym struct {
	dist uint16
	lc   uint8
}

// trees is the block state of zlib's deflate_state: the symbols of the
// block being built and the trees that code them.
type trees struct {
	dynLTree [heapSize]ct
	dynDTree [2*dCodes + 1]ct
	blTree   [2*blCodes + 1]ct
	lDesc    treeDesc
	dDesc    treeDesc
	blDesc   treeDesc

	blCount [maxBits + 1]uint16
	// heap holds leaves and nodes by frequency: heap[1:heapLen+1] while
	// the tree is built, sorted nodes from heapMax on.
	heap    [heapSize]int
	heapLen int
	heapMax int
	depth   [heapSize]uint8

	syms    [symEnd]sym
	symNext int

	// optLen and staticLen are the block's length in bits with the
	// dynamic and static trees.
	optLen    uint64
	staticLen uint64
}

func (t *trees) init() {
	t.lDesc = treeDesc{
		dynTree: t.dynLTree[:],
		stat:    &static.lDesc,
	}
	t.dDesc = treeDesc{
		dynTree: t.dynDTree[:],
		stat:    &static.dDesc,
	}
	t.blDesc = treeDesc{
		dynTree: t.blTree[:],
		stat:    &static.blDesc,
	}
	t.initBlock()
}

// initBlock is zlib's init_block.
func (t *trees) initBlock() {
	for n := range lCodes {
		t.dynLTree[n].fc = 0
	}

	for n := range dCodes {
		t.dynDTree[n].fc = 0
	}

	for n := range blCodes {
		t.blTree[n].fc = 0
	}

	t.dynLTree[endBlock].fc = 1
	t.optLen = 0
	t.staticLen = 0
	t.symNext = 0
}

// tally is zlib's _tr_tally: it records a literal (dist 0, lc the byte) or
// a match (dist its distance, lc its length - minMatch) and reports that
// the block is full.
func (t *trees) tally(dist, lc uint32) bool {
	t.syms[t.symNext] = sym{
		dist: uint16(dist),
		lc:   uint8(lc),
	}
	t.symNext++

	if dist == 0 {
		t.dynLTree[lc].fc++
	} else {
		dist--

		t.dynLTree[int(static.lengthCode[lc])+literals+1].fc++

		t.dynDTree[dCode(dist)].fc++
	}

	return t.symNext == symEnd
}

// smaller is zlib's smaller: by frequency, then by depth.
func (t *trees) smaller(tree []ct, n, m int) bool {
	return tree[n].fc < tree[m].fc || (tree[n].fc == tree[m].fc && t.depth[n] <= t.depth[m])
}

// pqDownHeap is zlib's pqdownheap: it restores the heap property from k
// down.
func (t *trees) pqDownHeap(tree []ct, k int) {
	v := t.heap[k]
	j := k << 1

	for j <= t.heapLen {
		if j < t.heapLen && t.smaller(tree, t.heap[j+1], t.heap[j]) {
			j++
		}

		if t.smaller(tree, v, t.heap[j]) {
			break
		}

		t.heap[k] = t.heap[j]
		k = j

		j <<= 1
	}

	t.heap[k] = v
}

// pqRemove is zlib's pqremove: it takes the smallest element off the heap.
func (t *trees) pqRemove(tree []ct) int {
	top := t.heap[1]

	t.heap[1] = t.heap[t.heapLen]
	t.heapLen--
	t.pqDownHeap(tree, 1)

	return top
}

// genBitLen is zlib's gen_bitlen: bit lengths from the built tree, limited
// to the descriptor's maximum as zlib limits them, and the block's
// lengths with these and with the static codes.
func (t *trees) genBitLen(desc *treeDesc) {
	tree := desc.dynTree
	maxCode := desc.maxCode
	stree := desc.stat.staticTree
	extra := desc.stat.extraBits
	base := desc.stat.extraBase
	maxLength := desc.stat.maxLength
	overflow := 0

	for bits := range t.blCount {
		t.blCount[bits] = 0
	}

	// The root has length 0; every other node one more than its parent,
	// parents coming first in the sorted part of the heap.
	tree[t.heap[t.heapMax]].dl = 0

	h := t.heapMax + 1

	for ; h < heapSize; h++ {
		n := t.heap[h]
		bits := int(tree[tree[n].dl].dl) + 1

		if bits > maxLength {
			bits = maxLength
			overflow++
		}

		tree[n].dl = uint16(bits)
		if n > maxCode {
			continue // not a leaf
		}

		t.blCount[bits]++

		xbits := 0

		if n >= base {
			xbits = extra[n-base]
		}

		f := uint64(tree[n].fc)

		t.optLen += f * uint64(bits+xbits)

		if stree != nil {
			t.staticLen += f * uint64(int(stree[n].dl)+xbits)
		}
	}

	if overflow == 0 {
		return
	}

	// Find the first bit length that could grow, move a leaf from it one
	// level down and an overflowing leaf next to it.
	for overflow > 0 {
		bits := maxLength - 1
		for t.blCount[bits] == 0 {
			bits--
		}

		t.blCount[bits]--

		t.blCount[bits+1] += 2
		t.blCount[maxLength]--

		overflow -= 2
	}

	// Give the leaves their new lengths, longest frequencies first in the
	// heap's order.
	for bits := maxLength; bits != 0; bits-- {
		n := int(t.blCount[bits])
		for n != 0 {
			h--

			m := t.heap[h]

			if m > maxCode {
				continue
			}

			if int(tree[m].dl) != bits {
				t.optLen += uint64(int64(bits)-int64(tree[m].dl)) * uint64(tree[m].fc)

				tree[m].dl = uint16(bits)
			}

			n--
		}
	}
}

// buildTree is zlib's build_tree: the Huffman tree of desc's frequencies,
// its bit lengths and codes, and desc.maxCode.
func (t *trees) buildTree(desc *treeDesc) {
	tree := desc.dynTree
	stree := desc.stat.staticTree
	elems := desc.stat.elems
	maxCode := -1

	t.heapLen = 0
	t.heapMax = heapSize

	for n := range elems {
		if tree[n].fc != 0 {
			t.heapLen++

			t.heap[t.heapLen] = n
			maxCode = n
			t.depth[n] = 0
		} else {
			tree[n].dl = 0
		}
	}

	// The format needs at least one distance code, and two codes of any
	// kind; zlib forces them as codes 0 and 1.
	for t.heapLen < 2 {
		node := 0
		if maxCode < 2 {
			maxCode++

			node = maxCode
		}

		t.heapLen++

		t.heap[t.heapLen] = node
		tree[node].fc = 1
		t.depth[node] = 0
		t.optLen--

		if stree != nil {
			t.staticLen -= uint64(stree[node].dl)
		}
	}

	desc.maxCode = maxCode

	for n := t.heapLen / 2; n >= 1; n-- {
		t.pqDownHeap(tree, n)
	}

	// Join the two least frequent nodes until one remains.
	node := elems
	for {
		n := t.pqRemove(tree)
		m := t.heap[1]

		t.heapMax--

		t.heap[t.heapMax] = n
		t.heapMax--

		t.heap[t.heapMax] = m

		tree[node].fc = tree[n].fc + tree[m].fc
		t.depth[node] = max(t.depth[n], t.depth[m]) + 1
		tree[n].dl = uint16(node)
		tree[m].dl = uint16(node)
		t.heap[1] = node
		node++

		t.pqDownHeap(tree, 1)

		if t.heapLen < 2 {
			break
		}
	}

	t.heapMax--

	t.heap[t.heapMax] = t.heap[1]

	t.genBitLen(desc)
	genCodes(tree, maxCode, &t.blCount)
}

// scanTree is zlib's scan_tree: it counts the bit length codes that will
// send tree's lengths.
func (t *trees) scanTree(tree []ct, maxCode int) {
	prevLen := -1
	nextLen := int(tree[0].dl)
	count := 0
	maxCount, minCount := 7, 4

	if nextLen == 0 {
		maxCount, minCount = 138, 3
	}

	tree[maxCode+1].dl = 0xffff // guard

	for n := 0; n <= maxCode; n++ {
		curLen := nextLen

		nextLen = int(tree[n+1].dl)
		count++

		switch {
		case count < maxCount && curLen == nextLen:
			continue
		case count < minCount:
			t.blTree[curLen].fc += uint16(count)
		case curLen != 0:
			if curLen != prevLen {
				t.blTree[curLen].fc++
			}

			t.blTree[rep3To6].fc++

		case count <= 10:
			t.blTree[repz3To10].fc++
		default:
			t.blTree[repz11To138].fc++
		}

		count = 0
		prevLen = curLen

		switch {
		case nextLen == 0:
			maxCount, minCount = 138, 3
		case curLen == nextLen:
			maxCount, minCount = 6, 3
		default:
			maxCount, minCount = 7, 4
		}
	}
}

// sendTree is zlib's send_tree: tree's lengths in bit length codes.
func (t *trees) sendTree(out *bitWriter, tree []ct, maxCode int) {
	prevLen := -1
	nextLen := int(tree[0].dl)
	count := 0
	maxCount, minCount := 7, 4

	if nextLen == 0 {
		maxCount, minCount = 138, 3
	}

	for n := 0; n <= maxCode; n++ {
		curLen := nextLen

		nextLen = int(tree[n+1].dl)
		count++

		switch {
		case count < maxCount && curLen == nextLen:
			continue
		case count < minCount:
			for ; count != 0; count-- {
				out.code(t.blTree[curLen])
			}

		case curLen != 0:
			if curLen != prevLen {
				out.code(t.blTree[curLen])

				count--
			}

			out.code(t.blTree[rep3To6])
			out.bits(uint32(count-3), 2)

		case count <= 10:
			out.code(t.blTree[repz3To10])
			out.bits(uint32(count-3), 3)

		default:
			out.code(t.blTree[repz11To138])
			out.bits(uint32(count-11), 7)
		}

		count = 0
		prevLen = curLen

		switch {
		case nextLen == 0:
			maxCount, minCount = 138, 3
		case curLen == nextLen:
			maxCount, minCount = 6, 3
		default:
			maxCount, minCount = 7, 4
		}
	}
}

// buildBLTree is zlib's build_bl_tree: the bit length tree, and the index
// in blOrder of the last bit length code sent.
func (t *trees) buildBLTree() int {
	t.scanTree(t.dynLTree[:], t.lDesc.maxCode)
	t.scanTree(t.dynDTree[:], t.dDesc.maxCode)
	t.buildTree(&t.blDesc)

	// At least 4 bit length codes are sent.
	maxBLIndex := blCodes - 1
	for ; maxBLIndex >= 3; maxBLIndex-- {
		if t.blTree[blOrder[maxBLIndex]].dl != 0 {
			break
		}
	}

	t.optLen += 3*(uint64(maxBLIndex)+1) + 5 + 5 + 4

	return maxBLIndex
}

// sendAllTrees is zlib's send_all_trees: a dynamic block's header.
func (t *trees) sendAllTrees(out *bitWriter, lcodes, dcodes, blcodes int) {
	out.bits(uint32(lcodes-257), 5)
	out.bits(uint32(dcodes-1), 5)
	out.bits(uint32(blcodes-4), 4)

	for rank := range blcodes {
		out.bits(uint32(t.blTree[blOrder[rank]].dl), 3)
	}

	t.sendTree(out, t.dynLTree[:], lcodes-1)
	t.sendTree(out, t.dynDTree[:], dcodes-1)
}

// compressBlock is zlib's compress_block: the block's symbols in the
// given trees, then the end of block.
func (t *trees) compressBlock(out *bitWriter, ltree, dtree []ct) {
	for _, s := range t.syms[:t.symNext] {
		if s.dist == 0 {
			out.code(ltree[s.lc])

			continue
		}

		lc := int(s.lc)
		code := int(static.lengthCode[lc])
		out.code(ltree[code+literals+1])

		if extra := extraLBits[code]; extra != 0 {
			out.bits(uint32(lc-static.baseLength[code]), extra)
		}

		dist := uint32(s.dist) - 1
		dcode := int(dCode(dist))
		out.code(dtree[dcode])

		if extra := extraDBits[dcode]; extra != 0 {
			out.bits(dist-uint32(static.baseDist[dcode]), extra)
		}
	}

	out.code(ltree[endBlock])
}

// storedBlock is zlib's _tr_stored_block, never the last block: buf
// stored as it is.
func (t *trees) storedBlock(out *bitWriter, buf []byte) {
	out.bits(storedBlock<<1, 3)
	out.windup()

	n := uint16(len(buf))

	out.buf = binary.LittleEndian.AppendUint16(out.buf, n)
	out.buf = binary.LittleEndian.AppendUint16(out.buf, ^n)
	out.buf = append(out.buf, buf...)
}

// flushBlock is zlib's _tr_flush_block, never the last block: the block's
// symbols as a stored, static or dynamic block, whichever is shortest, in
// zlib's order of preference. stored is the block's input, nil once the
// window has slid past its start; storedLen its length.
func (t *trees) flushBlock(out *bitWriter, stored []byte, storedLen uint64) {
	t.buildTree(&t.lDesc)
	t.buildTree(&t.dDesc)

	maxBLIndex := t.buildBLTree()

	optLenB := (t.optLen + 3 + 7) >> 3
	staticLenB := (t.staticLen + 3 + 7) >> 3

	if staticLenB <= optLenB {
		optLenB = staticLenB
	}

	switch {
	case storedLen+4 <= optLenB && stored != nil:
		t.storedBlock(out, stored)
	case staticLenB == optLenB:
		out.bits(staticTrees<<1, 3)
		t.compressBlock(out, static.ltree[:], static.dtree[:])

	default:
		out.bits(dynTrees<<1, 3)
		t.sendAllTrees(out, t.lDesc.maxCode+1, t.dDesc.maxCode+1, maxBLIndex+1)
		t.compressBlock(out, t.dynLTree[:], t.dynDTree[:])
	}

	t.initBlock()
}

// bitWriter writes deflate's bit stream, least significant bit first, as
// zlib's send_bits does.
type bitWriter struct {
	buf   []byte
	acc   uint64
	nbits int
}

func (b *bitWriter) bits(value uint32, n int) {
	b.acc |= uint64(value) << b.nbits

	b.nbits += n

	if b.nbits >= 32 {
		b.buf = binary.LittleEndian.AppendUint32(b.buf, uint32(b.acc))

		b.acc >>= 32

		b.nbits -= 32
	}
}

func (b *bitWriter) code(c ct) {
	b.bits(uint32(c.fc), int(c.dl))
}

// windup is zlib's bi_windup: the bits written so far, padded to a byte.
func (b *bitWriter) windup() {
	for b.nbits > 0 {
		b.buf = append(b.buf, byte(b.acc))

		b.acc >>= 8

		b.nbits -= 8
	}

	b.acc = 0
	b.nbits = 0
}
