package browser

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// EnsureDisplay gives the browsers a display for their windows and
// returns what undoes it. With none, an X display that does not answer,
// or WSL's WSLg (on the PC measured its X display accepted connections and
// never replied, or answered while its Wayland one, which Firefox
// prefers, hung), it starts Xvfb, a virtual X server, sets $DISPLAY and
// drops $WAYLAND_DISPLAY: the browsers then run as on a desktop, not
// headless (HeadlessChrome is another browser). Nothing on the wire
// depends on the display.
func EnsureDisplay(ctx context.Context) (func(), error) {
	display := os.Getenv("DISPLAY")

	switch wsl := os.Getenv("WSL_DISTRO_NAME") != ""; {
	case wsl:
		log.Printf("WSL: not using WSLg's display")
	case display != "" && xServerAnswers(ctx, display):
		return func() {}, nil
	case display == "" && os.Getenv("WAYLAND_DISPLAY") != "":
		// A Wayland session without Xwayland.
		return func() {}, nil
	case display != "":
		log.Printf("$DISPLAY does not answer")
	}

	if err := os.Unsetenv("WAYLAND_DISPLAY"); err != nil {
		return nil, err
	}

	xvfb, err := exec.LookPath("Xvfb")
	if err != nil {
		return nil, errors.New("no display that answers ($DISPLAY) and no Xvfb to make one: install xvfb")
	}

	for n := 99; n < 120; n++ {
		if displayAnswers(ctx, n) {
			continue
		}

		display := ":" + strconv.Itoa(n)

		// No window manager: none changes what a browser sends.
		cmd := exec.CommandContext(context.WithoutCancel(ctx), xvfb, display, "-screen", "0", "1920x1080x24", "-nolisten", "tcp") //nolint:gosec // a fixed program found on PATH
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("starting Xvfb: %w", err)
		}

		if err := waitForDisplay(ctx, n, 5*time.Second); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()

			return nil, err
		}

		if err := os.Setenv("DISPLAY", display); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()

			return nil, err
		}

		log.Printf("no display: browsers open on Xvfb %s", display)

		return func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}, nil
	}

	return nil, errors.New("no free X display for Xvfb (:99 to :119 taken)")
}

// dialDisplay connects to X display n: its socket file or, where
// /tmp/.X11-unix is read-only (WSLg mounts it so), its abstract socket.
func dialDisplay(ctx context.Context, n int) (net.Conn, error) {
	d := net.Dialer{
		Timeout: time.Second,
	}

	path := "/tmp/.X11-unix/X" + strconv.Itoa(n)

	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		c, err = d.DialContext(ctx, "unix", "@"+path)
	}

	return c, err
}

// displayAnswers reports whether X display n accepts connections.
func displayAnswers(ctx context.Context, n int) bool {
	c, err := dialDisplay(ctx, n)
	if err != nil {
		return false
	}

	_ = c.Close()

	return true
}

// waitForDisplay waits for X display n to accept connections.
func waitForDisplay(ctx context.Context, n int, timeout time.Duration) error {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if displayAnswers(ctx, n) {
			return nil
		}
	}

	return fmt.Errorf("no X server on display :%d within %s of starting Xvfb", n, timeout)
}

// x11Setup is an X11 connection setup request: little-endian, protocol
// 11.0, no authorization.
var x11Setup = []byte{'l', 0, 11, 0, 0, 0, 0, 0, 0, 0, 0, 0}

// xServerAnswers reports whether the local X display ($DISPLAY's ":n" or
// ":n.s") answers a connection setup within two seconds, whatever its
// answer (an authorization it wants is an answer). A display on another
// host is taken to answer.
func xServerAnswers(ctx context.Context, display string) bool {
	rest, ok := strings.CutPrefix(display, ":")
	if !ok {
		return true
	}

	num, _, _ := strings.Cut(rest, ".")

	n, err := strconv.Atoi(num)
	if err != nil {
		return true
	}

	c, err := dialDisplay(ctx, n)
	if err != nil {
		return false
	}

	defer func() {
		_ = c.Close()
	}()

	_ = c.SetDeadline(time.Now().Add(2 * time.Second))

	if _, err := c.Write(x11Setup); err != nil {
		return false
	}

	var reply [1]byte

	_, err = c.Read(reply[:])

	return err == nil
}
