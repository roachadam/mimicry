package headerorder

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrPlacement reports a header placement that cannot be applied. The root
// package exports it as mimicry.ErrPlacement; it lives here so the
// connections can return it too.
var ErrPlacement = errors.New("mimicry: header placement")

// Placement moves Header to directly after (After) or before Anchor. Both
// names are lowercase.
type Placement struct {
	Header string
	Anchor string
	After  bool
}

type placementsKey struct{}

// WithPlacements returns ctx carrying ps, applied in order to the request's
// headers once they are in the profile's order.
func WithPlacements(ctx context.Context, ps []Placement) context.Context {
	return context.WithValue(ctx, placementsKey{}, ps)
}

// PlacementsFrom returns the placements ctx carries, or nil.
func PlacementsFrom(ctx context.Context) []Placement {
	ps, _ := ctx.Value(placementsKey{}).([]Placement)
	return ps
}

// Place applies ps to fields, in their final order, one by one: each
// field named p.Header moves, keeping its order, right after the last
// p.Anchor (After) or before the first. A missing header or anchor is an
// ErrPlacement. fields' array is reused. See decision 0030.
func Place(fields []Field, ps []Placement) ([]Field, error) {
	var moved []Field
	for _, p := range ps {
		moved = moved[:0]

		kept := fields[:0]

		for _, f := range fields {
			if f.Name == p.Header {
				moved = append(moved, f)
				continue
			}

			kept = append(kept, f)
		}

		if len(moved) == 0 {
			return nil, fmt.Errorf("%w: %s is not sent on this request", ErrPlacement, p.Header)
		}

		at := -1
		for i, f := range kept {
			if f.Name != p.Anchor {
				continue
			}

			if p.After {
				at = i + 1
				continue
			}

			at = i

			break
		}

		if at < 0 {
			return nil, fmt.Errorf("%w: %s, the anchor for %s, is not sent on this request", ErrPlacement, p.Anchor, p.Header)
		}

		fields = slices.Insert(kept, at, moved...)
	}

	return fields, nil
}
