//go:build !race

package quic

// mimicry: outside race builds a stream buffer goes back to the pool as it
// is (pipe_poison_race.go). Not in upstream; see UPSTREAM.md.

// poisonPipebuf does nothing.
func poisonPipebuf([]byte) {}
