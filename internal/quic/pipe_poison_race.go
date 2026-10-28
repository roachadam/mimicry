//go:build race

package quic

// mimicry: in race builds a stream buffer going back to the pool is
// overwritten, so a stream still using one it gave up reads or sends
// garbage the tests see (TestStreamBuffersReturned). Not in upstream; see
// UPSTREAM.md.

// poisonPipebuf fills b with 0xdb.
func poisonPipebuf(b []byte) {
	for i := range b {
		b[i] = 0xdb
	}
}
