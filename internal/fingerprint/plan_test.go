package fingerprint

import (
	"math/rand/v2"
	"testing"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

func types(exts []profile.Extension) []profile.Code {
	out := make([]profile.Code, len(exts))
	for i, e := range exts {
		out[i] = e.Type
	}

	return out
}

// The shuffle must be uniform over movable slots. uTLS's
// ShuffleChromeTLSExtensions is biased (it skips swaps touching pinned
// slots); this catches that kind of bias.
func TestShuffleIsUniform(t *testing.T) {
	p := profiles.Chrome()
	plan := Compile(p)
	k := len(plan.movable)
	if k < 10 {
		t.Fatalf("only %d movable extensions; expected Chrome's shuffled set", k)
	}
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic test randomness
	const draws = 40000
	// counts[slot][extension type] for the movable slots.
	counts := make(map[int]map[profile.Code]int, k)
	for _, slot := range plan.movable {
		counts[slot] = map[profile.Code]int{}
	}
	for range draws {
		h := plan.hello(rng.IntN)
		for _, slot := range plan.movable {
			counts[slot][h.Extensions[slot].Type]++
		}
	}
	want := float64(draws) / float64(k)
	for slot, byType := range counts {
		for typ, n := range byType {
			if dev := (float64(n) - want) / want; dev > 0.1 || dev < -0.1 {
				t.Errorf("extension %s in slot %d: %d times, want about %.0f", typ, slot, n, want)
			}
		}
		if len(byType) != k {
			t.Errorf("slot %d saw %d different extensions, want all %d", slot, len(byType), k)
		}
	}
}

// Every connection needs its own order (Chrome redraws it per handshake). A
// per-client or per-session order is a known bug in other libraries: two
// connections then carry an identical order.
func TestEachConnectionGetsAFreshOrder(t *testing.T) {
	plan := Compile(profiles.Chrome())
	seen := map[string]bool{}
	for range 20 {
		var key string
		for _, typ := range types(plan.Hello().Extensions) {
			key += typ.String() + ","
		}
		seen[key] = true
	}
	// 15 movable extensions: a repeat among 20 draws has probability ~1e-10.
	if len(seen) != 20 {
		t.Fatalf("20 connections produced only %d distinct orders", len(seen))
	}
}
