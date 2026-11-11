//go:build unix

package client

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"
)

// Spawn starts cmd (mimicry-core) as an SDK does on Linux and macOS: one
// end of a socketpair as its stdin and stdout, both ends close-on-exec so
// only the child's is passed (decision 0069), and returns the other end.
// Closing it makes the core exit.
func Spawn(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	// syscall.ForkLock keeps a concurrent exec from inheriting the pair
	// before it is close-on-exec (Darwin has no SOCK_CLOEXEC).
	syscall.ForkLock.RLock()

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}

	syscall.ForkLock.RUnlock()

	if err != nil {
		return nil, fmt.Errorf("socketpair: %w", err)
	}

	parent := os.NewFile(uintptr(fds[0]), "mimicry-core (parent)")
	child := os.NewFile(uintptr(fds[1]), "mimicry-core (child)")

	defer func() {
		_ = child.Close()
	}()

	conn, err := net.FileConn(parent)

	_ = parent.Close()

	if err != nil {
		return nil, err
	}

	cmd.Stdin = child
	cmd.Stdout = child

	if err := cmd.Start(); err != nil {
		_ = conn.Close()

		return nil, err
	}

	return conn, nil
}
