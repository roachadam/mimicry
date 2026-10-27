package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// target is a browser the tool refreshes and where it runs.
type target struct {
	// browser is the capture tool's name for it (-browsers).
	browser string
	// onMini runs it on the Mac mini, over ssh.
	onMini bool
}

var targets = map[string]target{
	"chrome": {
		browser: "chrome",
		onMini:  true,
	},
	"firefox": {
		browser: "firefox",
		onMini:  true,
	},
	"safari": {
		browser: "safari",
	},
	"safari-mini": {
		browser: "safari",
		onMini:  true,
	},
}

// machine is where a browser runs: this Mac, or a host reached over ssh,
// where the tool's binaries and outputs live in dir under the home
// directory.
type machine struct {
	// host is the ssh host; "" for this Mac.
	host string
	// dir is the working directory: absolute here, relative to the home
	// directory on host.
	dir string
}

func (m *machine) String() string {
	if m.host == "" {
		return "this Mac"
	}
	return m.host
}

var sshOptions = []string{
	"-o", "BatchMode=yes",
	"-o", "ConnectTimeout=15",
	"-o", "ServerAliveInterval=30",
}

// ssh runs the shell command line on host.
func ssh(ctx context.Context, host, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "ssh", append(slices.Clone(sshOptions), host, line)...) //nolint:gosec // the host is the tool's flag
}

// command runs name (a path in m.dir for the tool's own binaries) with args
// in m.dir.
func (m *machine) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	if m.host == "" {
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the tool's own binaries and fixed commands
		cmd.Dir = m.dir
		return cmd
	}
	words := make([]string, 0, 1+len(args))
	words = append(words, shellQuote(name))
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	return ssh(ctx, m.host, "cd "+shellQuote(m.dir)+" && "+strings.Join(words, " "))
}

// output runs name with args on m and returns its standard output.
func (m *machine) output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := m.command(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w: %s", name, m, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// stream runs name with args on m, logging every line it prints prefixed
// with prefix and appending it to logFile. Interrupted, it also stops what
// it started on a remote host, which ssh without a terminal leaves running.
func (m *machine) stream(ctx context.Context, prefix, logFile, name string, args ...string) error {
	f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()
	cmd := m.command(ctx, name, args...)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	var wg sync.WaitGroup
	wg.Go(func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			log.Printf("[%s] %s", prefix, sc.Text())
			_, _ = fmt.Fprintln(f, sc.Text())
		}
		if err := sc.Err(); err != nil {
			log.Printf("[%s] reading output: %v", prefix, err)
		}
		_, _ = io.Copy(io.Discard, pr)
	})
	err = cmd.Run()
	_ = pw.Close()
	wg.Wait()
	if err != nil && ctx.Err() != nil && m.host != "" {
		_ = ssh(context.WithoutCancel(ctx), m.host, "pkill -f "+shellQuote(m.dir+"/")).Run()
	}
	if err != nil {
		return fmt.Errorf("%s on %s: %w", name, m, err)
	}
	return nil
}

// put copies the local files into m.dir on a remote host.
func (m *machine) put(ctx context.Context, files ...string) error {
	if m.host == "" {
		return nil
	}
	if out, err := ssh(ctx, m.host, "mkdir -p "+shellQuote(m.dir)).CombinedOutput(); err != nil {
		return fmt.Errorf("creating %s on %s: %w: %s", m.dir, m, err, out)
	}
	args := append(slices.Clone(sshOptions), "-q", "-r")
	args = append(args, files...)
	args = append(args, m.host+":"+m.dir+"/")
	if out, err := exec.CommandContext(ctx, "scp", args...).CombinedOutput(); err != nil { //nolint:gosec // the tool's own files and host
		return fmt.Errorf("copying to %s: %w: %s", m, err, out)
	}
	return nil
}

// fetch copies the directory remote (in m.dir) of a remote host into
// local, through tar, so an empty directory is no error. Here, remote is
// local already.
func (m *machine) fetch(ctx context.Context, remote, local string) error {
	if err := os.MkdirAll(local, 0o750); err != nil {
		return err
	}
	if m.host == "" {
		return nil
	}
	src := m.command(ctx, "tar", "-C", remote, "-cf", "-", ".")
	dst := exec.CommandContext(ctx, "tar", "-C", local, "-xf", "-") //nolint:gosec // the tool's own staging directory
	pipe, err := src.StdoutPipe()
	if err != nil {
		return err
	}
	dst.Stdin = pipe
	var srcErr, dstErr strings.Builder
	src.Stderr = &srcErr
	dst.Stderr = &dstErr
	if err := src.Start(); err != nil {
		return err
	}
	if err := dst.Run(); err != nil {
		_ = src.Wait()
		return fmt.Errorf("unpacking %s from %s: %w: %s", remote, m, err, dstErr.String())
	}
	if err := src.Wait(); err != nil {
		return fmt.Errorf("packing %s on %s: %w: %s", remote, m, err, srcErr.String())
	}
	return nil
}

// path is the file name in m's working directory as m sees it.
func (m *machine) path(name string) string {
	if m.host == "" {
		return filepath.Join(m.dir, name)
	}
	return name
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=,@+") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// build compiles the harness command ./cmd/<name> for the Macs,
// darwin/arm64, into dir.
func build(ctx context.Context, dir, name string) (string, error) {
	out := filepath.Join(dir, name)
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "./cmd/"+name) //nolint:gosec // the tool's own commands
	cmd.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=arm64")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building %s: %w: %s", name, err, b)
	}
	return out, nil
}
