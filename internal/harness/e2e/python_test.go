package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profile"
)

// repoRoot is the repository's root, from this package's directory.
var repoRoot = filepath.Join("..", "..", "..")

// pythonEnv names a Python interpreter that imports the Python SDK
// (bindings/python); TestPythonSDKMatchesGo is skipped without it.
const pythonEnv = "MIMICRY_PYTHON"

// Requests sent through the Python SDK (bindings/python, decision 0069)
// are mimicry's own on the wire: TestCoreMatchesGoKinds' pages (a
// navigation, every request kind with its options, uploads inline and
// streamed, a caller's header order, Exact, a placed header), sent once by
// mimicry's Go client and once by a Python process using the SDK's async
// API (bindings/python/tests/e2e_driver.py) on the mimicry-core built
// here, compare equal as the server recorded them. It runs where
// MIMICRY_PYTHON names a Python with the SDK installed (the Python SDK's
// CI job).
func TestPythonSDKMatchesGoKinds(t *testing.T) {
	matchKinds(t, pythonStarter(t))
}

// The Python SDK's WebSockets are mimicry's on the wire:
// TestCoreMatchesGoWebSockets' pages, directly and through the proxies,
// through the Python SDK as TestPythonSDKMatchesGoKinds sends its pages.
func TestPythonSDKMatchesGoWebSockets(t *testing.T) {
	matchWebSockets(t, pythonStarter(t))
}

// pythonStarter is startPython for the Python MIMICRY_PYTHON names, or
// skips the test without one. A relative path in it is the repository
// root's (bindings/python/.venv/bin/python), as the driver's is; a bare
// name is looked up in PATH.
func pythonStarter(t *testing.T) startVia {
	t.Helper()

	python := os.Getenv(pythonEnv)
	if python == "" {
		t.Skipf("%s is not set: no Python SDK to drive", pythonEnv)
	}

	if !filepath.IsAbs(python) && strings.ContainsAny(python, `/\`) {
		abs, err := filepath.Abs(filepath.Join(repoRoot, filepath.FromSlash(python)))
		if err != nil {
			t.Fatal(err)
		}

		python = abs
	}

	return func(t *testing.T, p *profile.Spec, s *pageSession, dns string, browserAuth bool) via {
		t.Helper()

		return startPython(t, python, p, s, dns, browserAuth)
	}
}

// pythonVia sends a page's requests through the Python SDK: a driver
// process taking one JSON command per line and answering each, tagged
// with its id, on a line of its own; commands run concurrently there.
type pythonVia struct {
	t   *testing.T
	enc *json.Encoder

	mu      sync.Mutex
	nextID  int
	waiting map[int]chan pythonAnswer
	failed  error
}

// pythonCommand is one command to the driver; e2e_driver.py reads them.
type pythonCommand struct {
	ID   int    `json:"id"`
	Op   string `json:"op"`
	Args any    `json:"args,omitempty"`
}

// pythonAnswer is the driver's answer to a command: an error, or its
// result.
type pythonAnswer struct {
	ID     int             `json:"id"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// startPython starts the driver for p's page s with a client reaching it as
// startCore's does.
func startPython(t *testing.T, python string, p *profile.Spec, s *pageSession, dns string, browserAuth bool) *pythonVia {
	t.Helper()

	driver, err := filepath.Abs(filepath.Join(repoRoot, "bindings", "python", "tests", "e2e_driver.py"))
	if err != nil {
		t.Fatal(err)
	}

	var stderr lockedBuffer

	cmd := exec.Command(python, driver) //nolint:gosec,noctx // the test's own driver; closing its stdin ends it

	cmd.Env = append(os.Environ(), "MIMICRY_CORE="+coreBinary(t))
	cmd.Stderr = &stderr

	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	pv := &pythonVia{
		t:       t,
		enc:     json.NewEncoder(in),
		waiting: map[int]chan pythonAnswer{},
	}

	done := make(chan struct{})

	go pv.answers(out, done)

	t.Cleanup(func() {
		_ = in.Close()

		<-done

		if err := cmd.Wait(); err != nil {
			t.Errorf("the Python driver: %v: %s", err, stderr.String())
		}
	})

	data, err := profile.Encode(p)
	if err != nil {
		t.Fatal(err)
	}

	roots, err := os.ReadFile(s.ca.CertPath)
	if err != nil {
		t.Fatal(err)
	}

	init := map[string]any{
		"profile":            string(data),
		"root_cas":           string(roots),
		"dns_server":         dns,
		"browser_proxy_auth": browserAuth,
	}
	if s.proxy != nil {
		init["proxy"] = s.proxy.String()
	}

	if _, err := pv.call("init", init); err != nil {
		t.Fatalf("the Python driver: %v: %s", err, stderr.String())
	}

	return pv
}

// lockedBuffer is a bytes.Buffer safe to read while exec's goroutine
// copies a child's output into it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// answers hands the driver's answers to their callers until it ends.
func (pv *pythonVia) answers(out io.Reader, done chan<- struct{}) {
	defer close(done)

	sc := bufio.NewScanner(out)
	sc.Buffer(nil, 64<<20)

	for sc.Scan() {
		var a pythonAnswer
		if err := json.Unmarshal(sc.Bytes(), &a); err != nil {
			pv.fail(fmt.Errorf("the Python driver wrote %q: %w", sc.Text(), err))

			return
		}

		pv.mu.Lock()

		ch := pv.waiting[a.ID]
		delete(pv.waiting, a.ID)
		pv.mu.Unlock()

		if ch != nil {
			ch <- a
		}
	}

	if err := sc.Err(); err != nil {
		pv.fail(fmt.Errorf("reading the Python driver: %w", err))

		return
	}

	pv.fail(errors.New("the Python driver ended"))
}

func (pv *pythonVia) fail(err error) {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	if pv.failed == nil {
		pv.failed = err
	}

	for id, ch := range pv.waiting {
		ch <- pythonAnswer{
			ID:    id,
			Error: err.Error(),
		}

		delete(pv.waiting, id)
	}
}

// call runs one command and returns its result.
func (pv *pythonVia) call(op string, args any) (json.RawMessage, error) {
	ch := make(chan pythonAnswer, 1)

	pv.mu.Lock()

	if pv.failed != nil {
		pv.mu.Unlock()

		return nil, pv.failed
	}

	pv.nextID++

	id := pv.nextID

	pv.waiting[id] = ch

	err := pv.enc.Encode(pythonCommand{
		ID:   id,
		Op:   op,
		Args: args,
	})
	pv.mu.Unlock()

	if err != nil {
		return nil, err
	}

	select {
	case a := <-ch:
		if a.Error != "" {
			return nil, errors.New(a.Error)
		}

		return a.Result, nil

	case <-pv.t.Context().Done():
		return nil, pv.t.Context().Err()
	}
}

// pythonHeader is a header for the driver: names with their values, a
// name with none removing a default as in Go.
func pythonHeader(h http.Header) [][2]any {
	out := [][2]any{}

	for name, values := range h {
		if name == mimicry.HeaderOrderKey {
			continue
		}

		if len(values) == 0 {
			out = append(out, [2]any{
				name,
				nil,
			})
		}

		for _, v := range values {
			out = append(out, [2]any{
				name,
				v,
			})
		}
	}

	return out
}

func (pv *pythonVia) send(q pageReq) error {
	o := coreOptions(q.r)

	places := make([]map[string]string, 0, len(o.GetHeaderPlacement()))
	for _, pl := range o.GetHeaderPlacement() {
		places = append(places, map[string]string{
			"header": pl.GetHeader(),
			"after":  pl.GetAfter(),
			"before": pl.GetBefore(),
		})
	}

	_, err := pv.call("send", map[string]any{
		"method":       q.method,
		"url":          q.target,
		"headers":      pythonHeader(q.header),
		"header_order": q.header[mimicry.HeaderOrderKey],
		"body":         q.body,
		"stream":       q.stream,
		"exact":        q.exact,
		"options": map[string]any{
			"kind":             int(o.GetKind()),
			"page":             o.GetPage(),
			"priority":         int(o.GetPriority()),
			"cross_origin":     int(o.GetCrossOrigin()),
			"credentials":      int(o.GetCredentials()),
			"no_cors":          o.GetNoCors(),
			"stylesheet":       o.GetStylesheet(),
			"ignore_cors":      o.GetIgnoreCors(),
			"header_placement": places,
			"client_hints":     int(o.GetClientHints()),
		},
	})

	return err
}

func (pv *pythonVia) dial(target string, opts mimicry.WebSocketOptions) (wsConn, error) {
	page := ""
	if opts.From != nil {
		page = opts.From.String()
	}

	r, err := pv.call("dial", map[string]any{
		"url":       target,
		"page":      page,
		"protocols": opts.Protocols,
		"headers":   pythonHeader(opts.Header),
	})
	if err != nil {
		return nil, err
	}

	var id int
	if err := json.Unmarshal(r, &id); err != nil {
		return nil, err
	}

	return pythonSocket{
		pv: pv,
		id: id,
	}, nil
}

// pythonSocket is a WebSocket of the Python SDK, by the driver's id.
type pythonSocket struct {
	pv *pythonVia
	id int
}

func (s pythonSocket) write(binary bool, data []byte) error {
	_, err := s.pv.call("write", map[string]any{
		"id":     s.id,
		"binary": binary,
		"data":   data,
	})

	return err
}

func (s pythonSocket) read() ([]byte, error) {
	r, err := s.pv.call("read", map[string]any{
		"id": s.id,
	})
	if err != nil {
		return nil, err
	}

	var data []byte

	err = json.Unmarshal(r, &data)

	return data, err
}

func (s pythonSocket) close() error {
	_, err := s.pv.call("close", map[string]any{
		"id": s.id,
	})

	return err
}
