package native

import (
	"runtime"
	"sync"
	"weak"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/h1"
	"github.com/roachadam/mimicry/profile"
)

// compiled is what an engine derives from its profile once: the ClientHello
// plan and the HTTP/1.1 writer. Engines given the same profile share one
// (Transports built from equal profiles share their profile, and so this),
// so it is read-only once built, like the profile it shares values with.
type compiled struct {
	plan *fingerprint.Plan
	h1   h1.Writer
}

// compiledByProfile holds each profile's compiled, weakly both ways: an
// entry goes once no engine holds its compiled.
var compiledByProfile sync.Map // weak.Pointer[profile.Profile] → weak.Pointer[compiled]

// compile returns p's compiled, shared with every engine holding p, which
// must not change once an engine holds it (New takes ownership).
func compile(p *profile.Profile) *compiled {
	key := weak.Make(p)
	if v, ok := compiledByProfile.Load(key); ok {
		if c := compiledOf(v); c != nil {
			return c
		}
	}

	c := &compiled{
		plan: fingerprint.Compile(p),
		h1:   h1.NewWriter(p),
	}
	wc := weak.Make(c)

	for {
		v, loaded := compiledByProfile.LoadOrStore(key, wc)
		if !loaded {
			break
		}

		// Another engine stored one first: use it while it lives, else
		// replace the dead entry.
		if shared := compiledOf(v); shared != nil {
			return shared
		}

		if compiledByProfile.CompareAndSwap(key, v, wc) {
			break
		}
	}

	runtime.AddCleanup(c, dropCompiled, compiledEntry{
		key: key,
		wc:  wc,
	})

	return c
}

// compiledOf returns the compiled of a compiledByProfile value, nil once
// collected.
func compiledOf(v any) *compiled {
	wc, _ := v.(weak.Pointer[compiled])

	return wc.Value()
}

// compiledEntry is a compiledByProfile entry, for its cleanup.
type compiledEntry struct {
	key weak.Pointer[profile.Profile]
	wc  weak.Pointer[compiled]
}

// dropCompiled removes e once its compiled is collected, unless a newer one
// has replaced it.
func dropCompiled(e compiledEntry) {
	compiledByProfile.CompareAndDelete(e.key, e.wc)
}
