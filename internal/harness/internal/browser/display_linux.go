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
	"syscall"
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

		stop, err := startXvfb(ctx, xvfb, n)
		if errors.Is(err, errDisplayTaken) {
			log.Printf("%v; trying the next display", err)

			continue
		}

		if err != nil {
			return nil, err
		}

		display := ":" + strconv.Itoa(n)
		if err := os.Setenv("DISPLAY", display); err != nil {
			stop()

			return nil, err
		}

		log.Printf("no display: browsers open on Xvfb %s", display)

		return stop, nil
	}

	return nil, errors.New("no free X display for Xvfb (:99 to :119 taken)")
}

// errDisplayTaken is a display another X server holds: our Xvfb exited
// (Xvfb exits when another server holds the display's lock file), or the
// display answers but is not ours.
var errDisplayTaken = errors.New("another X server holds the display")

// startXvfb starts Xvfb on display n and returns what stops it, once the
// display accepts connections and is ours: its lock file names our Xvfb,
// so a display another server holds (a lock without a socket that
// answers, or one starting at the same time) is never taken for ours
// (errDisplayTaken). It stops on SIGTERM, which lets it remove its lock
// file and socket (killed only if it lingers), and gets SIGTERM as well
// when this process dies first (Pdeathsig, sent when the thread that
// started it ends: in Go, with the process, unless a goroutine exits
// locked to that thread).
func startXvfb(ctx context.Context, xvfb string, n int) (func(), error) {
	xctx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	// No window manager: none changes what a browser sends. Not
	// -displayfd, with which Xvfb takes the display without its lock.
	cmd := exec.CommandContext(xctx, xvfb, ":"+strconv.Itoa(n), "-screen", "0", "1920x1080x24", "-nolisten", "tcp") //nolint:gosec // a fixed program found on PATH

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Start(); err != nil {
		cancel()

		return nil, fmt.Errorf("starting Xvfb: %w", err)
	}

	exited := make(chan struct{})

	go func() {
		_ = cmd.Wait()

		close(exited)
	}()

	stop := func() {
		cancel()
		<-exited
	}

	const timeout = 5 * time.Second

	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		select {
		case <-exited:
			cancel()

			return nil, fmt.Errorf("display :%d: our Xvfb exited: %w", n, errDisplayTaken)

		case <-ctx.Done():
			stop()

			return nil, ctx.Err()

		default:
		}

		if !displayAnswers(ctx, n) {
			continue
		}

		if lockPID(n) != cmd.Process.Pid {
			stop()

			return nil, fmt.Errorf("display :%d answers, but not from our Xvfb: %w", n, errDisplayTaken)
		}

		return stop, nil
	}

	stop()

	return nil, fmt.Errorf("display :%d of our Xvfb not ready within %s", n, timeout)
}

// lockPID is the process holding X display n's lock file, 0 if none.
func lockPID(n int) int {
	data, err := os.ReadFile("/tmp/.X" + strconv.Itoa(n) + "-lock")
	if err != nil {
		return 0
	}

	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))

	return pid
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
