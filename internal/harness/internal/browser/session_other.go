//go:build !windows

package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var macOSApps = map[string]string{
	"firefox": "/Applications/Firefox.app",
	"safari":  "/Applications/Safari.app",
}

var linuxCommands = map[string][]string{
	"chrome": {
		"google-chrome-stable",
		"google-chrome",
	},
	"firefox": {
		"firefox",
	},
}

// Supported reports whether every named browser can be captured on this
// platform: here all of them.
func Supported([]string) error {
	return nil
}

// Find returns the supported browsers installed here: Chrome, Firefox, then
// Safari.
func Find() []*Browser {
	var found []*Browser
	for _, name := range []string{"chrome", "firefox", "safari"} {
		b := &Browser{
			Name: name,
		}

		app := macOSApps[name]
		if name == "chrome" {
			app = ChromeApp
		}

		if app != "" {
			if _, err := os.Stat(app); err == nil {
				b.App = app
			}
		}

		if b.App == "" {
			for _, cmd := range linuxCommands[name] {
				if p, err := exec.LookPath(cmd); err == nil {
					b.Path = p
					break
				}
			}
		}

		if b.App == "" && b.Path == "" {
			continue
		}

		switch name {
		case "chrome":
			b.prepare = chromeArgs
			b.warmup = chromeWarmup
			b.binary = b.Path

			if b.App != "" {
				// The executable is named after the application.
				b.binary = filepath.Join(b.App, "Contents", "MacOS", strings.TrimSuffix(filepath.Base(b.App), ".app"))
			}

		case "firefox":
			b.prepare = firefoxProfile
		case "safari":
			b.Shared = true
		}

		found = append(found, b)
	}

	return found
}

// Version is the installed browser's version as its executable records it,
// which only Windows reads (macOS has the bundle's Info.plist).
func (b *Browser) Version(context.Context) (string, error) {
	return "", fmt.Errorf("%s: no executable version resource here", b.Name)
}

// handTo passes url to the instance running on profile directory dir by
// invoking the browser again on it.
func (b *Browser) handTo(ctx context.Context, dir, url string) error {
	if out, err := exec.CommandContext(ctx, b.binary, "--user-data-dir="+dir, url).CombinedOutput(); err != nil { //nolint:gosec // the binary is a known browser location; dir is our own
		return fmt.Errorf("handing %s the capture URL: %w: %s", b.Name, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// start launches b with args on the profile directory dir, which the
// session removes when it closes (and start, if it fails).
func (b *Browser) start(ctx context.Context, dir string, args []string) (*Session, error) {
	var cmd *exec.Cmd
	if b.App != "" {
		// -n: a new instance even if the user's browser is open. -W: open
		// exits when the browser does, so Wait tracks its lifetime.
		cmd = exec.CommandContext(ctx, "open", append([]string{"-n", "-W", "-a", b.App, "--args"}, args...)...) //nolint:gosec // the app is a known browser location
	} else {
		cmd = exec.CommandContext(ctx, b.Path, args...) //nolint:gosec // the path is a known browser location
	}

	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("starting %s: %w", b.Name, err)
	}

	s := &Session{
		proc: cmd.Process,
		dir:  dir,
		app:  b.App != "",
		done: make(chan struct{}),
	}

	go func() {
		_ = cmd.Wait()

		close(s.done)
	}()

	return s, nil
}

// stop signals the browser. A browser started through open(1) is not our
// child, so it is found by its profile directory, which no other process uses.
func (s *Session) stop(kill bool) {
	if s.app {
		signal := "-TERM"
		if kill {
			signal = "-KILL"
		}

		_ = exec.Command("pkill", signal, "-f", s.dir).Run() //nolint:noctx,gosec // must run after the caller's context ends; s.dir is our own temp directory
		if kill {
			_ = s.proc.Kill()
		}

		return
	}

	if kill {
		_ = s.proc.Kill()
		return
	}

	if err := s.proc.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = s.proc.Kill()
	}
}

// removeProfile removes the stopped browser's profile directory.
func (s *Session) removeProfile() error {
	return os.RemoveAll(s.dir)
}
