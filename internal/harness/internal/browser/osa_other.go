//go:build !windows

package browser

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// runOSA runs script in OSA language lang.
func runOSA(ctx context.Context, lang, script string) (string, error) {
	out, err := exec.CommandContext(ctx, "osascript", "-l", lang, "-e", script).Output() //nolint:gosec // our own scripts
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("osascript: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}

		return "", err
	}

	return strings.TrimRight(string(out), "\n"), nil
}
