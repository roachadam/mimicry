//go:build !linux

package browser

import (
	"context"
)

// EnsureDisplay does nothing here: the browsers open on the logged-in
// desktop (macOS, and Windows through a scheduled task).
func EnsureDisplay(context.Context) (func(), error) {
	return func() {}, nil
}
