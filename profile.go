package mimicry

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"weak"

	"github.com/roachadam/mimicry/profile"
)

// snapshots are the decoded profiles Transports hold, by the SHA-256 of
// their encoding: Transports built from equal profiles share one decoded,
// validated copy (most of a Transport's memory) instead of each decoding
// its own. An entry goes when the last Transport holding its copy is
// collected. The copies are read-only: nothing may write into a profile a
// Transport holds.
var snapshots sync.Map // [sha256.Size]byte → weak.Pointer[profile.Profile]

// snapshot returns a validated copy of p that later changes to p do not
// reach, shared with every Transport built from an equal profile. The
// encoding is compact: it is only hashed and decoded, never written
// (profile.Encode's indented form is for files).
func snapshot(p *profile.Profile) (*profile.Profile, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	key := sha256.Sum256(data)
	if v, ok := snapshots.Load(key); ok {
		if shared := snapshotOf(v); shared != nil {
			return shared, nil
		}
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
