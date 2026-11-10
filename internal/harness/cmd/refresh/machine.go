package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
)

const (
	platformMacOS   = "macos"
	platformWindows = "windows"
	platformLinux   = "linux"
	platformIOS     = "ios"

	remoteMini    = "mini"
	remoteWindows = "windows"
	remoteLinux   = "linux"
)

// target is a browser the tool refreshes and where it runs.
type target struct {
	// browser is the profile's browser and, unless tool is set, the
	// capture tool's name for it (-browsers).
	browser string
	// tool is the capture tool's name for the browser when it is not
	// browser: one of browser.IOSNames, a browser on an iPhone served from
	// this Mac over the network.
	tool string
	// remote is where it runs over ssh: remoteMini (the second Mac, -mini),
	// remoteWindows (a Windows PC, -windows), remoteLinux (a Linux machine,
	// or a WSL distribution on a Windows PC: -linux, -wsl), or "" for this
	// Mac (and the iPhone it serves).
	remote string
}

// platform is the profile platform of the browser's captures.
func (t target) platform() string {
	switch {
	case t.remote == remoteWindows:
		return platformWindows
	case t.remote == remoteLinux:
		return platformLinux
	case slices.Contains(browser.IOSNames(), t.tool):
		return platformIOS
	}

	return platformMacOS
}

var targets = map[string]target{
	"chrome": {
		browser: "chrome",
		remote:  remoteMini,
	},
	"chrome-windows": {
		browser: "chrome",
		remote:  remoteWindows,
	},
	"firefox": {
		browser: "firefox",
		remote:  remoteMini,
	},
	"firefox-windows": {
		browser: "firefox",
		remote:  remoteWindows,
	},
	"edge": {
		browser: "edge",
		remote:  remoteMini,
	},
	"edge-windows": {
		browser: "edge",
		remote:  remoteWindows,
	},
	"brave": {
		browser: "brave",
		remote:  remoteMini,
	},
	"brave-windows": {
		browser: "brave",
		remote:  remoteWindows,
	},
	"chrome-linux": {
		browser: "chrome",
		remote:  remoteLinux,
	},
	"firefox-linux": {
		browser: "firefox",
		remote:  remoteLinux,
	},
	"brave-linux": {
		browser: "brave",
		remote:  remoteLinux,
	},
	"safari": {
		browser: "safari",
	},
	"safari-mini": {
		browser: "safari",
		remote:  remoteMini,
	},
	"safari-ios": {
		browser: "safari",
		tool:    browser.IOSSafariName,
	},
	"chrome-ios": {
		browser: "chrome",
		tool:    browser.IOSChromeName,
	},
	"firefox-ios": {
		browser: "firefox",
		tool:    browser.IOSFirefoxName,
	},
}

// machine is where a browser runs: this Mac, or a host reached over ssh,
// where the tool's binaries and outputs live in dir under the home
// directory.
type machine struct {
	// host is the ssh host; "" for this Mac.
	host string
	// dir is the working directory: absolute here, relative to the home
	// directory (%USERPROFILE% on Windows) on host.
	dir string
	// windows is a Windows PC: its ssh shell is PowerShell, the tool's
	// binaries are windows/amd64 .exe files.
	windows bool
	// linux is a Linux machine: the tool's binaries are linux/amd64. With
	// wsl, it is that WSL distribution on host, a Windows PC, each command
	// run in it as wslUser through wsl.exe (wslLine).
	linux   bool
	wsl     string
	wslUser string
	// iphone is the iPhone this Mac serves over the network: the capture
	// tool runs here and opens each URL in the phone's browser through
	// devicectl over USB. iosDevice is its UDID (-ios-device), "" for the
	// one there.
	iphone    bool
	iosDevice string
	// chromeApp and firefoxApp are refreshRun's ChromeApp and FirefoxApp.
	chromeApp  string
	firefoxApp string
}

func (m *machine) String() string {
	switch {
	case m.iphone:
		return "the iPhone"
	case m.host == "":
		return "this Mac"
	case m.wsl != "":
		return m.host + " (WSL " + m.wsl + ")"
	}

	return m.host
}

// goos is the GOOS of the tool's binaries for m.
func (m *machine) goos() string {
	switch {
	case m.windows:
		return "windows"
	case m.linux:
		return "linux"
	}

	return "darwin"
}

// role names m in a report, which is committed: by what it is, never by
// its ssh alias, which is the maintainer's own name for the machine.
func (m *machine) role() string {
	switch {
	case m.iphone:
		return "the iPhone, served from this Mac"
	case m.host == "":
		return "this Mac"
	case m.windows:
		return "the Windows PC"
	case m.wsl != "":
		return "the Linux machine (WSL2 on the Windows PC)"
	case m.linux:
		return "the Linux machine"
	default:
		return "the second Mac"
	}
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

// sh runs the POSIX shell command line on m, a remote host that is not
// Windows: in its WSL distribution, if it has one.
func (m *machine) sh(ctx context.Context, line string) *exec.Cmd {
	if m.wsl != "" {
		line = wslLine(m.wsl, m.wslUser, line)
	}

	return ssh(ctx, m.host, line)
}

// wslLine is the command line a Windows host's ssh shell (PowerShell or
// cmd) runs to run line in WSL distribution distro as user (the
// distribution's default user if ""), from that user's home directory.
// line goes base64-encoded, so neither shell between sees anything in it
// to quote, and standard input and output stay the command's own (a tar
// stream passes through intact). It needs bash 4 or later in the
// distribution (3.2 cannot source a process substitution): every current
// one has it.
func wslLine(distro, user, line string) string {
	as := ""
	if user != "" {
		as = " -u " + user
	}

	return "wsl.exe -d " + distro + as + " --cd ~ -- bash -c \"source <(echo " + base64.StdEncoding.EncodeToString([]byte(line)) + " | base64 -d)\""
}

// powershell runs the PowerShell script on a Windows host, whatever its
// ssh shell: encoded, so nothing in it needs quoting for the shell between.
// Script errors stop it with exit status 1; native commands' output is
// read and written as UTF-8.
func powershell(ctx context.Context, host, script string) *exec.Cmd {
	script = "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'; " +
		"try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false } catch {}\n" + script

	units := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(units))

	for i, u := range units {
		b[2*i] = byte(u)
		b[2*i+1] = byte(u >> 8)
	}

	return ssh(ctx, host, "powershell -NoProfile -NonInteractive -InputFormat None -EncodedCommand "+base64.StdEncoding.EncodeToString(b))
}

// psQuote quotes s as a PowerShell string literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// psDir sets $d to m.dir's full path on a Windows host.
func (m *machine) psDir() string {
	return "$d = [System.IO.Path]::GetFullPath((Join-Path $env:USERPROFILE " + psQuote(m.dir) + "))\n"
}

// psExit exits with the last native command's status, 1 if none ran.
const psExit = "\nif ($null -eq $LASTEXITCODE) { exit 1 }\nexit $LASTEXITCODE"

// psRun is the script running name with args in m.dir on a Windows host;
// merged sends the command's standard error to its standard output, as
// plain lines.
func (m *machine) psRun(name string, args []string, merged bool) string {
	words := make([]string, 0, 2+len(args))

	words = append(words, "&", psQuote(name))

	for _, a := range args {
		words = append(words, psQuote(a))
	}

	line := strings.Join(words, " ")
	if merged {
		line += ` 2>&1 | ForEach-Object { "$_" }`
	}

	// Native commands' standard error must not stop the script.
	return m.psDir() + "Set-Location -LiteralPath $d\n$ErrorActionPreference = 'Continue'\n" + line + psExit
}

// command runs name (a path in m.dir for the tool's own binaries) with args
// in m.dir.
func (m *machine) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	if m.host == "" {
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the tool's own binaries and fixed commands

		cmd.Dir = m.dir

		return cmd
	}

	if m.windows {
		return powershell(ctx, m.host, m.psRun(name, args, false))
	}

	words := make([]string, 0, 1+len(args))

	words = append(words, shellQuote(name))

	for _, a := range args {
		words = append(words, shellQuote(a))
	}

	return m.sh(ctx, "cd "+shellQuote(m.dir)+" && "+strings.Join(words, " "))
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

// readFile returns the file name (m.path) on m.
func (m *machine) readFile(ctx context.Context, name string) ([]byte, error) {
	if !m.windows {
		return m.output(ctx, "cat", name)
	}

	// Base64, since PowerShell passes text only.
	cmd := powershell(ctx, m.host, m.psDir()+"[Convert]::ToBase64String([System.IO.File]::ReadAllBytes((Join-Path $d "+psQuote(name)+")))")

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading %s on %s: %w: %s", name, m, err, strings.TrimSpace(stderr.String()))
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, fmt.Errorf("reading %s on %s: %w", name, m, err)
	}

	return data, nil
}

// stream runs name with args on m, logging every line it prints prefixed
// with prefix and appending it to logFile. Interrupted, it also stops what
// it started on a remote host, which ssh without a terminal leaves running.
// For the iPhone it reads this tool's standard input: the capture tool
// waits there for Enter once someone has changed the phone's proxy.
func (m *machine) stream(ctx context.Context, prefix, logFile, name string, args ...string) error {
	// On Windows, standard error joins the output as plain lines.
	var cmd *exec.Cmd
	if m.windows {
		cmd = powershell(ctx, m.host, m.psRun(name, args, true))
	} else {
		cmd = m.command(ctx, name, args...)
	}

	if m.iphone {
		cmd.Stdin = os.Stdin
	}

	return m.streamCmd(ctx, prefix, logFile, name, cmd)
}

// streamCmd runs cmd for stream.
func (m *machine) streamCmd(ctx context.Context, prefix, logFile, name string, cmd *exec.Cmd) error {
	f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close()
	}()

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
		if m.windows {
			_ = powershell(context.WithoutCancel(ctx), m.host, m.psDir()+psStopUnder).Run()
		} else {
			_ = m.sh(context.WithoutCancel(ctx), "pkill -f "+shellQuote(m.dir+"/")).Run()
		}
	}

	if err != nil {
		return fmt.Errorf("%s on %s: %w", name, m, err)
	}

	return nil
}

// psStopUnder stops every process on a Windows host run from $d or naming
// it on its command line (the browser's profile directory is in it).
const psStopUnder = `Get-CimInstance Win32_Process | Where-Object {
  ($_.ExecutablePath -and $_.ExecutablePath.StartsWith($d, [System.StringComparison]::OrdinalIgnoreCase)) -or
  ($_.CommandLine -and $_.CommandLine.IndexOf($d, [System.StringComparison]::OrdinalIgnoreCase) -ge 0)
} | ForEach-Object {
  "stopping $($_.Name) ($($_.ProcessId))"
  Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
}
`

// put copies the local files into m.dir on a remote host: by scp, or, into
// a WSL distribution, which scp cannot reach, as a tar stream.
func (m *machine) put(ctx context.Context, files ...string) error {
	if m.host == "" {
		return nil
	}

	if m.wsl != "" {
		return m.putTar(ctx, files)
	}

	mkdir := ssh(ctx, m.host, "mkdir -p "+shellQuote(m.dir))
	if m.windows {
		mkdir = powershell(ctx, m.host, m.psDir()+"New-Item -ItemType Directory -Force -Path $d | Out-Null")
	}

	if out, err := mkdir.CombinedOutput(); err != nil {
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

// putTar is put through tar: the files, each by its base name, unpacked
// into m.dir.
func (m *machine) putTar(ctx context.Context, files []string) error {
	// No macOS metadata: Linux's tar would warn of its extended headers.
	args := make([]string, 0, 4+3*len(files))

	args = append(args, "--no-mac-metadata", "--no-xattrs", "-cf", "-")
	for _, f := range files {
		args = append(args, "-C", filepath.Dir(f), filepath.Base(f))
	}

	src := exec.CommandContext(ctx, "tar", args...) //nolint:gosec // the tool's own files

	src.Env = append(os.Environ(), "COPYFILE_DISABLE=1")

	dst := m.sh(ctx, "mkdir -p "+shellQuote(m.dir)+" && tar -C "+shellQuote(m.dir)+" -xf -")

	var srcErr, dstOut strings.Builder

	src.Stderr = &srcErr
	dst.Stdout = &dstOut
	dst.Stderr = &dstOut

	packErr, copyErr := pipe(src, dst)
	if copyErr != nil {
		return fmt.Errorf("copying to %s: %w: %s", m, copyErr, dstOut.String())
	}

	if packErr != nil {
		return fmt.Errorf("packing for %s: %w: %s", m, packErr, srcErr.String())
	}

	return nil
}

// pipe runs src with its standard output piped into dst's standard input
// and waits for both, returning each one's error. Only the two commands
// hold the pipe's ends, so when dst exits early (ssh losing the host),
// src's next write fails (EPIPE) instead of blocking on a full pipe.
func pipe(src, dst *exec.Cmd) (srcErr, dstErr error) {
	r, w, err := os.Pipe()
	if err != nil {
		return err, nil
	}

	src.Stdout = w
	dst.Stdin = r

	err = src.Start()
	_ = w.Close()

	if err != nil {
		_ = r.Close()
		return err, nil
	}

	dstErr = dst.Start()
	_ = r.Close()

	if dstErr == nil {
		dstErr = dst.Wait()
	}

	return src.Wait(), dstErr
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

	if m.windows {
		return m.fetchWindows(ctx, remote, local)
	}

	src := m.command(ctx, "tar", "-C", remote, "-cf", "-", ".")
	dst := exec.CommandContext(ctx, "tar", "-C", local, "-xf", "-") //nolint:gosec // the tool's own staging directory

	var srcErr, dstErr strings.Builder

	src.Stderr = &srcErr
	dst.Stderr = &dstErr

	packErr, unpackErr := pipe(src, dst)
	if unpackErr != nil {
		return fmt.Errorf("unpacking %s from %s: %w: %s", remote, m, unpackErr, dstErr.String())
	}

	if packErr != nil {
		return fmt.Errorf("packing %s on %s: %w: %s", remote, m, packErr, srcErr.String())
	}

	return nil
}

// fetchWindows is fetch from a Windows host: PowerShell passes text only,
// so Windows' tar writes an archive file in m.dir that scp copies here.
func (m *machine) fetchWindows(ctx context.Context, remote, local string) error {
	archive := remote + ".tar"

	pack := powershell(ctx, m.host, m.psDir()+"Set-Location -LiteralPath $d\n$ErrorActionPreference = 'Continue'\n"+
		`& (Join-Path $env:SystemRoot 'System32\tar.exe') -C `+psQuote(remote)+" -cf (Join-Path $d "+psQuote(archive)+`) . 2>&1 | ForEach-Object { "$_" }`+psExit)
	if out, err := pack.CombinedOutput(); err != nil {
		return fmt.Errorf("packing %s on %s: %w: %s", remote, m, err, out)
	}

	tmp, err := os.CreateTemp("", "refresh-*.tar")
	if err != nil {
		return err
	}

	_ = tmp.Close()

	defer func() {
		_ = os.Remove(tmp.Name())
	}()

	args := append(slices.Clone(sshOptions), "-q", m.host+":"+m.dir+"/"+archive, tmp.Name())
	if out, err := exec.CommandContext(ctx, "scp", args...).CombinedOutput(); err != nil { //nolint:gosec // the tool's own files and host
		return fmt.Errorf("copying %s from %s: %w: %s", archive, m, err, out)
	}

	if out, err := exec.CommandContext(ctx, "tar", "-C", local, "-xf", tmp.Name()).CombinedOutput(); err != nil { //nolint:gosec // the tool's own staging directory
		return fmt.Errorf("unpacking %s from %s: %w: %s", remote, m, err, out)
	}

	return nil
}

// cleanWindows stops what the run left running in m.dir on a Windows
// host, runs capture -cleanup (the capture CA out of the LocalMachine Root
// store, leftover mimicry-capture-* scheduled tasks deleted: never left
// behind, even after a killed capture) and, unless keep, removes m.dir,
// logging to logFile.
func (m *machine) cleanWindows(ctx context.Context, logFile string, keep bool) error {
	script := m.psDir() + `if (-not (Test-Path -LiteralPath $d)) { "nothing in $d"; exit 0 }
$ErrorActionPreference = 'Continue'
` + psStopUnder + `Set-Location -LiteralPath $d
if (Test-Path -LiteralPath '.\capture.exe') {
  "capture -cleanup: the capture CA out of the LocalMachine Root store, leftover scheduled tasks deleted"
  & '.\capture.exe' -cleanup -browser-data 'browser-data' 2>&1 | ForEach-Object { "$_" }
  if ($LASTEXITCODE -ne 0) { "capture -cleanup failed: $LASTEXITCODE"; exit 1 }
}
Set-Location -LiteralPath $env:USERPROFILE
`
	if keep {
		script += `"kept $d (-keep)"` + "\nexit 0"
	} else {
		script += `for ($i = 0; $i -lt 5 -and (Test-Path -LiteralPath $d); $i++) {
  if ($i -gt 0) { Start-Sleep -Seconds 2 }
  Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue
}
if (Test-Path -LiteralPath $d) { "could not remove $d"; exit 1 }
"removed $d"
$parent = Split-Path -Parent $d
if (-not (Get-ChildItem -LiteralPath $parent -Force)) { Remove-Item -LiteralPath $parent -Force }
exit 0`
	}

	return m.streamCmd(ctx, m.host+" cleanup", logFile, "cleanup", powershell(ctx, m.host, script))
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

// build compiles the harness command ./cmd/<name> for goos into dir:
// darwin/arm64 for the Macs, windows/amd64 into dir/windows-amd64, or
// linux/amd64 into dir/linux-amd64.
func build(ctx context.Context, dir, name, goos string) (string, error) {
	goarch, out := "arm64", filepath.Join(dir, name)

	switch goos {
	case "windows":
		goarch, out = "amd64", filepath.Join(dir, "windows-amd64", name+".exe")
	case "linux":
		goarch, out = "amd64", filepath.Join(dir, "linux-amd64", name)
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "./cmd/"+name) //nolint:gosec // the tool's own commands

	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch)

	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building %s for %s/%s: %w: %s", name, goos, goarch, err, b)
	}

	return out, nil
}
