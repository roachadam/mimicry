package browser

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
)

// On Windows only Chrome is captured. A process started from an ssh session
// runs in that session, which has no desktop, so Chrome is started through a
// one-shot scheduled task that runs as the user logged on at the console with
// their interactive token: its window shows on their desktop. The profile
// directory (DataDir) is shared by the two sessions: Chrome writes
// DevToolsActivePort there and the capture tool reads it.

// TaskPrefix starts the name of every scheduled task the capture tool
// registers; Cleanup deletes the ones a killed run left.
const TaskPrefix = "mimicry-capture-"

// ErrNoConsoleUser is a launch with nobody logged on at the console.
var ErrNoConsoleUser = errors.New("nobody is logged on at this PC's console: Chrome must open on a logged-on desktop; log on at the screen (as the user running the capture tool) and run again")

// Supported reports whether every named browser can be captured here: on
// Windows, Chrome only.
func Supported(names []string) error {
	var errs []error

	for _, n := range names {
		if n != "chrome" {
			errs = append(errs, fmt.Errorf("%s is not supported on Windows: the Windows capture covers Chrome only", n))
		}
	}

	return errors.Join(errs...)
}

// ChromeLocations are where Find looks for chrome.exe, in order: the
// machine-wide installations (64-bit, then 32-bit) and the per-user one.
func ChromeLocations() []string {
	var paths []string

	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if dir := os.Getenv(env); dir != "" {
			paths = append(paths, filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}

	return paths
}

// Find returns the supported browsers installed here: Chrome, the first of
// ChromeLocations that exists.
func Find() []*Browser {
	for _, p := range ChromeLocations() {
		if _, err := os.Stat(p); err != nil {
			continue
		}

		return []*Browser{
			{
				Name:    "chrome",
				Path:    p,
				prepare: chromeArgs,
				warmup:  chromeWarmup,
				binary:  p,
			},
		}
	}

	return nil
}

// Version is the installed browser's version, from its executable's
// version resource (chrome.exe: "156.0.8078.76").
func (b *Browser) Version(ctx context.Context) (string, error) {
	v, err := PowerShell(ctx, `(Get-Item -LiteralPath `+PSQuote(b.Path)+`).VersionInfo.ProductVersion`)
	if err != nil {
		return "", fmt.Errorf("reading %s's version: %w", b.Path, err)
	}

	if v == "" {
		return "", fmt.Errorf("%s has no version resource", b.Path)
	}

	return v, nil
}

// handTo passes url to the instance running on profile directory dir by
// invoking the browser again on it, on the console user's desktop: the
// running instance is found through its window there.
func (b *Browser) handTo(ctx context.Context, dir, url string) error {
	task, err := runInteractive(ctx, b.binary, []string{"--user-data-dir=" + dir, url})
	if err != nil {
		return fmt.Errorf("handing %s the capture URL: %w", b.Name, err)
	}

	code, err := waitTask(ctx, task)

	err = errors.Join(err, deleteTask(task))
	if err != nil {
		return fmt.Errorf("handing %s the capture URL: %w", b.Name, err)
	}

	if code != 0 {
		return fmt.Errorf("handing %s the capture URL: %s exited with %#x", b.Name, filepath.Base(b.binary), code)
	}

	return nil
}

// start launches b with args on the profile directory dir on the console
// user's desktop, which the session removes when it closes (and start, if
// it fails).
func (b *Browser) start(ctx context.Context, dir string, args []string) (*Session, error) {
	task, err := runInteractive(ctx, b.Path, args)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("starting %s: %w", b.Name, err)
	}

	proc, err := findBrowser(ctx, b.Path, dir)
	if err != nil {
		err = fmt.Errorf("starting %s: %w%s", b.Name, err, taskNote(task))
		_ = deleteTask(task)
		_ = os.RemoveAll(dir)

		return nil, err
	}

	s := &Session{
		proc: proc,
		dir:  dir,
		release: func() error {
			return deleteTask(task)
		},
		done: make(chan struct{}),
	}

	go func() {
		_, _ = proc.Wait()

		close(s.done)
	}()

	return s, nil
}

// stop ends the browser with its helper processes. Nothing reaches a
// process on another session's desktop but termination, so both ways are
// the same; the capture is taken before a session closes.
func (s *Session) stop(kill bool) {
	if kill {
		_ = s.proc.Kill()
		return
	}

	_ = exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(s.proc.Pid)).Run() //nolint:noctx,gosec // must run after the caller's context ends; a fixed program and our own child's PID
}

// removeProfile deletes the session's profile directory, whose files stay
// locked for a moment after the processes holding them end.
func (s *Session) removeProfile() error {
	var err error
	for deadline := time.Now().Add(10 * time.Second); ; {
		if err = os.RemoveAll(s.dir); err == nil || time.Now().After(deadline) {
			break
		}

		time.Sleep(200 * time.Millisecond)
	}

	return err
}

// PowerShell runs script in Windows PowerShell and returns its output,
// trimmed. The script travels encoded, so it needs no command-line quoting;
// any error stops it with a failing exit status.
func PowerShell(ctx context.Context, script string) (string, error) {
	script = "$ProgressPreference = 'SilentlyContinue'\n" +
		"$ErrorActionPreference = 'Stop'\n" +
		"[Console]::OutputEncoding = [Text.Encoding]::UTF8\n" +
		script

	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", //nolint:gosec // a fixed program; the script is ours, passed encoded
		"-EncodedCommand", base64.StdEncoding.EncodeToString(utf16LE(script)))

	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}

		return "", fmt.Errorf("powershell: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// PSQuote is s as a PowerShell single-quoted string literal.
func PSQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// ConsoleUser is the user logged on at the console (DOMAIN\name), "" when
// nobody is.
func ConsoleUser(ctx context.Context) (string, error) {
	return PowerShell(ctx, `(Get-CimInstance Win32_ComputerSystem).UserName`)
}

// consoleUser caches the console user once someone is logged on: a capture
// asks for every launch.
var consoleUser struct {
	sync.Mutex

	name string
}

// launchUser is the console user the scheduled tasks run as.
func launchUser(ctx context.Context) (string, error) {
	consoleUser.Lock()
	defer consoleUser.Unlock()

	if consoleUser.name != "" {
		return consoleUser.name, nil
	}

	name, err := ConsoleUser(ctx)
	if err != nil {
		return "", fmt.Errorf("finding the console user: %w", err)
	}

	if name == "" {
		return "", ErrNoConsoleUser
	}

	consoleUser.name = name

	return name, nil
}

// taskSeq numbers this process's scheduled tasks.
var taskSeq atomic.Int64

// taskDefinition is a scheduled task that runs a program once on demand,
// with no trigger, time limit or power conditions, as a user's interactive
// token (the console session, without elevation) at normal priority (a
// task's default, 7, lowers its CPU and I/O priority below a program the
// user starts).
const taskDefinition = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>%s</Description>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>Parallel</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>4</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`

// runInteractive registers a one-shot scheduled task running exe with args
// on the console user's desktop and runs it (schtasks /Create, /Run; the
// definition's interactive token is /IT). It returns the task's name, which
// the caller deletes with deleteTask.
func runInteractive(ctx context.Context, exe string, args []string) (string, error) {
	user, err := launchUser(ctx)
	if err != nil {
		return "", err
	}

	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}

	name := TaskPrefix + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(taskSeq.Add(1), 10)
	def := fmt.Sprintf(taskDefinition,
		xmlText("mimicry capture: "+filepath.Base(exe)+" on the console user's desktop; the capture tool deletes it when done"),
		xmlText(user),
		xmlText(exe),
		xmlText(strings.Join(quoted, " ")),
		xmlText(filepath.Dir(exe)),
	)

	f, err := os.CreateTemp("", name+"-*.xml")
	if err != nil {
		return "", err
	}

	defer func() {
		_ = os.Remove(f.Name())
	}()

	// Task Scheduler reads a definition in UTF-16, as it writes them.
	_, err = f.Write(append([]byte{0xFF, 0xFE}, utf16LE(def)...))
	if err = errors.Join(err, f.Close()); err != nil {
		return "", err
	}

	if out, err := exec.CommandContext(ctx, "schtasks.exe", "/Create", "/TN", name, "/XML", f.Name(), "/F").CombinedOutput(); err != nil { //nolint:gosec // our own task and file
		return "", fmt.Errorf("registering scheduled task %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}

	if out, err := exec.CommandContext(ctx, "schtasks.exe", "/Run", "/TN", name).CombinedOutput(); err != nil { //nolint:gosec // our own task
		return "", errors.Join(fmt.Errorf("running scheduled task %s: %w: %s", name, err, strings.TrimSpace(string(out))), deleteTask(name))
	}

	return name, nil
}

// deleteTask unregisters a scheduled task (schtasks /Delete), leaving
// whatever it started running.
func deleteTask(name string) error {
	if out, err := exec.Command("schtasks.exe", "/Delete", "/TN", name, "/F").CombinedOutput(); err != nil { //nolint:noctx,gosec // must run after the caller's context ends; our own task
		return fmt.Errorf("deleting scheduled task %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// Task Scheduler's results for a task that has not finished running.
const (
	schedTaskReady      = 0x41300
	schedTaskRunning    = 0x41301
	schedTaskHasNotRun  = 0x41303
	taskPollInterval    = 250 * time.Millisecond
	taskStartupDeadline = 30 * time.Second
)

// taskResult is a scheduled task's last result: its program's exit status
// once it has run; pending while it has not run or still runs.
func taskResult(ctx context.Context, name string) (code int64, pending bool, err error) {
	out, err := PowerShell(ctx, `(Get-ScheduledTaskInfo -TaskPath '\' -TaskName `+PSQuote(name)+`).LastTaskResult`)
	if err != nil {
		return 0, false, err
	}

	code, err = strconv.ParseInt(out, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("scheduled task %s: last result %q", name, out)
	}

	switch code {
	case schedTaskReady, schedTaskRunning, schedTaskHasNotRun:
		return code, true, nil
	}

	return code, false, nil
}

// waitTask waits for a scheduled task's program to exit and returns its
// exit status.
func waitTask(ctx context.Context, name string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, taskStartupDeadline)
	defer cancel()

	for {
		code, pending, err := taskResult(ctx, name)
		if err == nil && !pending {
			return code, nil
		}

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("scheduled task %s had not finished after %s (last: %w)", name, taskStartupDeadline, errors.Join(ctx.Err(), err))
		case <-time.After(taskPollInterval):
		}
	}
}

// taskNote describes a scheduled task's last result for an error message.
func taskNote(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	code, pending, err := taskResult(ctx, name)

	switch {
	case err != nil:
		return ""
	case pending:
		return fmt.Sprintf(" (scheduled task %s: %#x, not finished)", name, code)
	default:
		return fmt.Sprintf(" (scheduled task %s: its program exited with %#x)", name, code)
	}
}

// findBrowser waits for the browser process exe started on profile
// directory dir: the one whose command line names dir and is not a helper
// (--type=). The process is in another session, but its user's.
func findBrowser(ctx context.Context, exe, dir string) (*os.Process, error) {
	ctx, cancel := context.WithTimeout(ctx, taskStartupDeadline)
	defer cancel()

	// dir ends the --user-data-dir argument: a space or the closing quote
	// follows it.
	script := `$d = ` + PSQuote(dir) + `
Get-CimInstance Win32_Process -Filter ` + PSQuote("Name = '"+filepath.Base(exe)+"'") + ` |
	Where-Object { $_.CommandLine -and ($_.CommandLine.Contains($d + ' ') -or $_.CommandLine.Contains($d + '"')) -and -not $_.CommandLine.Contains('--type=') } |
	Sort-Object CreationDate | Select-Object -First 1 -ExpandProperty ProcessId`

	for {
		out, err := PowerShell(ctx, script)
		if err == nil && out != "" {
			pid, err := strconv.Atoi(out)
			if err != nil {
				return nil, fmt.Errorf("process id %q", out)
			}

			return os.FindProcess(pid)
		}

		select {
		case <-ctx.Done():
			if err == nil {
				err = ctx.Err()
			}

			return nil, fmt.Errorf("%s did not start on the console user's desktop within %s: %w", filepath.Base(exe), taskStartupDeadline, err)

		case <-time.After(taskPollInterval):
		}
	}
}

// Cleanup ends what a killed capture run left behind: the browser processes
// on profile directories under dataDir (none looked for if dataDir is ""),
// then every scheduled task named TaskPrefix*, stopping any still running.
// It returns what it removed.
func Cleanup(ctx context.Context, dataDir string) ([]string, error) {
	var done []string

	if dataDir != "" {
		abs, err := filepath.Abs(dataDir)
		if err != nil {
			return nil, err
		}

		out, err := PowerShell(ctx, `$d = `+PSQuote(abs+`\`)+`
Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -and $_.CommandLine.Contains($d) } | ForEach-Object {
	Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
	"ended process $($_.ProcessId) ($($_.Name))"
}`)
		if err != nil {
			return nil, fmt.Errorf("ending browsers under %s: %w", abs, err)
		}

		done = append(done, lines(out)...)
	}

	// The root folder's task files carry the task names: listing them finds
	// ours without parsing any other task's definition (Get-ScheduledTask
	// fails as a whole when any task on the machine has malformed XML).
	tasks := filepath.Join(os.Getenv("WINDIR"), "System32", "Tasks")

	entries, err := os.ReadDir(tasks)
	if err != nil {
		return done, fmt.Errorf("listing scheduled tasks %s* in %s: %w", TaskPrefix, tasks, err)
	}

	var errs []error

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, TaskPrefix) {
			continue
		}

		// Ending a task that is not running fails; deleting it is what matters.
		_ = exec.CommandContext(ctx, "schtasks.exe", "/End", "/TN", name).Run() //nolint:gosec // our own task

		if derr := deleteTask(name); derr != nil {
			errs = append(errs, derr)
			continue
		}

		done = append(done, "deleted scheduled task "+name)
	}

	return done, errors.Join(errs...)
}

// lines splits output into its non-empty lines.
func lines(out string) []string {
	var ls []string

	for l := range strings.Lines(out) {
		if l = strings.TrimSpace(l); l != "" {
			ls = append(ls, l)
		}
	}

	return ls
}

// xmlText escapes s for XML character data.
func xmlText(s string) string {
	var b strings.Builder

	_ = xml.EscapeText(&b, []byte(s))

	return b.String()
}

// utf16LE encodes s as UTF-16, little-endian, without a byte order mark.
func utf16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))

	for i, c := range u {
		b[2*i] = byte(c)
		b[2*i+1] = byte(c >> 8)
	}

	return b
}
