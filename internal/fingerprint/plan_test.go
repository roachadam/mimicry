package fingerprint

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

func compile(t *testing.T, p *profile.Profile) *Plan {
	t.Helper()
	plan, err := Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func types(exts []profile.Extension) []profile.Code {
	out := make([]profile.Code, len(exts))
	for i, e := range exts {
		out[i] = e.Type
	}
	return out
}

func TestFixedOrderNeverMoves(t *testing.T) {
	p := profiles.Firefox158MacOS()
	plan := compile(t, p)
	want := types(p.TLS.Extensions)
	for range 100 {
		if got := types(plan.Hello().Extensions); !slices.Equal(got, want) {
			t.Fatalf("fixed profile reordered:\n got %v\nwant %v", got, want)
		}
	}
}

// The shuffle must be uniform: each movable extension lands in each movable
// slot equally often. uTLS's ShuffleChromeTLSExtensions skips swaps that touch
// a pinned slot, which leaves each extension in place about twice as often as
// chance (~12.5% vs ~6.7% for Chrome); this test catches that kind of bias.
func TestShuffleIsUniform(t *testing.T) {
	p := profiles.Chrome155MacOS()
	plan := compile(t, p)
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

func TestShufflePinsGREASE(t *testing.T) {
	p := profiles.Chrome155MacOS()
	plan := compile(t, p)
	want := map[int]profile.Code{}
	for i, e := range p.TLS.Extensions {
		if e.Pinned() {
			want[i] = e.Type
		}
	}
	if len(want) == 0 {
		t.Fatal("Chrome profile has no pinned extensions")
	}
	for range 1000 {
		h := plan.Hello()
		for i, typ := range want {
			if h.Extensions[i].Type != typ {
				t.Fatalf("pinned %s moved from slot %d", typ, i)
			}
		}
	}
}

// Every connection needs its own order (Chrome redraws it per handshake). A
// per-client or per-session order is a known bug in other libraries: two
// connections then carry an identical order.
func TestEachConnectionGetsAFreshOrder(t *testing.T) {
	plan := compile(t, profiles.Chrome155MacOS())
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

func TestPlanIsIndependentOfItsProfile(t *testing.T) {
	p := profiles.Chrome155MacOS()
	plan := compile(t, p)
	p.TLS.CipherSuites[1] = 0x0005
	p.TLS.Extensions = p.TLS.Extensions[:3]
	h := plan.Hello()
	if h.CipherSuites[1] == 0x0005 || len(h.Extensions) <= 3 {
		t.Fatal("changing the profile after Compile changed the plan")
	}
	h.CipherSuites[1] = 0x0005
	if plan.Hello().CipherSuites[1] == 0x0005 {
		t.Fatal("changing a Hello changed the plan")
	}
}

func TestCompileRejectsInvalidProfile(t *testing.T) {
	p := profiles.Chrome155MacOS()
	p.TLS.ExtensionOrder = ""
	if _, err := Compile(p); err == nil {
		t.Fatal("compiled an invalid profile")
	}
}
