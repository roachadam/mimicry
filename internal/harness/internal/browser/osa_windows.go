package browser

import (
	"context"
	"errors"
)

// errNoOSA is what every AppleScript path (Safari, its clicker and live
// window) returns on Windows.
var errNoOSA = errors.New("AppleScript (Safari) is not supported on Windows: the Windows capture covers Chrome only")

// runOSA cannot run on Windows.
func runOSA(context.Context, string, string) (string, error) {
	return "", errNoOSA
}
