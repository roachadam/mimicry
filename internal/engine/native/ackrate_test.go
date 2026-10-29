package native

import (
	"testing"

	"github.com/roachadam/mimicry/profiles"
)

// Config.HTTP3ACKEvery: zero keeps the profile's QUIC section itself, a
// rate in range dials with a copy that differs in the rate alone (the
// shared profile is never written), and one below the profile's or above
// maxACKEvery is refused.
func TestACKOverride(t *testing.T) {
	q := profiles.Chrome().QUIC
	base := q.Ack

	for _, tc := range []struct {
		every int
		ok    bool
	}{
		{0, true},
		{base.Every, true},
		{20, true},
		{maxACKEvery, true},
		{base.Every - 1, false},
		{-1, false},
		{maxACKEvery + 1, false},
	} {
		got, err := ackOverride(q, tc.every)
		if (err == nil) != tc.ok {
			t.Fatalf("every %d: err %v, want ok %v", tc.every, err, tc.ok)
		}

		switch {
		case !tc.ok:
		case tc.every == 0 && got != q:
			t.Errorf("every 0: a copy, not the profile's section")
		case tc.every != 0 && (got.Ack.Every != tc.every || got.Ack.WarmupPackets != base.WarmupPackets || got.Ack.WarmupEvery != base.WarmupEvery || got.Ack.Batched != base.Batched):
			t.Errorf("every %d: ack %+v, want the profile's %+v with every %d", tc.every, got.Ack, base, tc.every)
		}

		if q.Ack != base {
			t.Fatalf("every %d: the shared profile's ack changed to %+v", tc.every, q.Ack)
		}
	}
}
