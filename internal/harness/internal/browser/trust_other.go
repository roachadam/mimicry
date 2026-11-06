//go:build !linux

package browser

import (
	"context"
)

// trustInProfile does nothing here: the browsers trust the capture CA
// through the system roots (macOS by hand, once; Windows for the run).
func trustInProfile(context.Context, *Browser, string, Target) error {
	return nil
}
