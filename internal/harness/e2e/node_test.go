package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profile"
)

// Requests sent through the Node SDK (bindings/node) are mimicry's own on
// the wire: TestCoreMatchesGoKinds' pages (navigations, every kind,
// preflights, redirects, a form POST, uploads inline and streamed, a
// header order, Exact, a placed header), sent once by mimicry's Go client
// and once by a Node program using the SDK (the SDK's
// test/fixtures/harness-driver.ts, its core the one coreBinary builds),
// compare equal as the server recorded them. Needs nodeSDK's Node and
// dependencies.
func TestNodeMatchesGoKinds(t *testing.T) {
	nodeSDK(t)
	matchKinds(t, startNode)
}

// The Node SDK's WebSockets are mimicry's on the wire:
// TestCoreMatchesGoWebSockets' pages and sockets (directly, over HTTP/2,
// through the proxy with credentials and over SOCKS5), sent by mimicry's
// Go client and by the SDK, compare equal as the server and the proxy
// recorded them.
func TestNodeMatchesGoWebSockets(t *testing.T) {
	nodeSDK(t)
	matchWebSockets(t, startNode)
}

// nodeRoot is the Node SDK's directory, from this package's.
var nodeRoot = filepath.Join("..", "..", "..", "bindings", "node")

// nodeSDK skips the test unless Node 22.18 or later (which runs the SDK's
// TypeScript as it is) is in PATH and the SDK's dependencies are installed
// (npm ci in bindings/node); with MIMICRY_NODE_SDK=1 (CI's Node job) it
// fails instead.
func nodeSDK(t *testing.T) {
	t.Helper()

	missing := func(format string, args ...any) {
		t.Helper()

		if os.Getenv("MIMICRY_NODE_SDK") == "1" {
			t.Fatalf(format, args...)
		}

		t.Skipf(format+" (MIMICRY_NODE_SDK=1 fails instead)", args...)
	}

	node, err := exec.LookPath("node")
	if err != nil {
		missing("no node in PATH")

		return
	}

	out, err := exec.CommandContext(t.Context(), node, "--version").Output() //nolint:gosec // the node in PATH, asked its version
	if err != nil {
		missing("node --version: %v", err)

		return
	}

	m := regexp.MustCompile(`^v(\d+)\.(\d+)`).FindSubmatch(out)
	if m == nil {
		missing("node --version: %q", out)

		return
	}

	major, _ := strconv.Atoi(string(m[1]))
	minor, _ := strconv.Atoi(string(m[2]))

	if major < 22 || major == 22 && minor < 18 {
		missing("node %s is older than 22.18, which runs TypeScript", bytes.TrimSpace(out))

		return
	}

	if _, err := os.Stat(filepath.Join(nodeRoot, "node_modules", "@bufbuild", "protobuf")); err != nil {
		missing("the Node SDK's dependencies are not installed (npm ci in bindings/node)")
	}
}

// nodeVia sends a page's requests through the Node SDK: a Node program
// (harness-driver.ts) taking one JSON command per line, answering each by
// id, so a socket's reads can wait while its writes go.
type nodeVia struct {
	t *testing.T

	mu      sync.Mutex
	stdin   io.Writer
	next    int
	answers map[int]chan nodeAnswer
	// done is closed when the program's output ended, readErr then why
	// reading it failed, if it did.
	done    chan struct{}
	readErr error
}

type nodeAnswer struct {
	ID    int    `json:"id"`
	Error string `json:"error"`
	WS    int    `json:"ws"`
	Data  []byte `json:"data"`
}

// startNode starts the Node driver for p's page s, its client reaching the
// page as the Go client does: s's CA, its proxy (credentials given the
// browser's way when browserAuth), else the DNS stub at dns.
func startNode(t *testing.T, p *profile.Spec, s *pageSession, dns string, browserAuth bool) via {
	t.Helper()

	var stderr bytes.Buffer

	cmd := exec.Command("node", filepath.Join(nodeRoot, "test", "fixtures", "harness-driver.ts")) //nolint:gosec,noctx // the SDK's own driver, by the node nodeSDK checked; closing its stdin stops it

	cmd.Env = append(os.Environ(), "MIMICRY_CORE="+absPath(t, coreBinary(t)))
	cmd.Stderr = &stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	v := &nodeVia{
		t:       t,
		stdin:   stdin,
		answers: map[int]chan nodeAnswer{},
		done:    make(chan struct{}),
	}

	go v.read(stdout)

	t.Cleanup(func() {
		_ = stdin.Close()

		if err := cmd.Wait(); err != nil {
			t.Errorf("the Node driver: %v: %s", err, stderr.String())
		}
	})

	spec, err := profile.Encode(p)
	if err != nil {
		t.Fatal(err)
	}

	roots, err := os.ReadFile(s.ca.CertPath)
	if err != nil {
		t.Fatal(err)
	}

	proxy := ""
	if s.proxy != nil {
		proxy = s.proxy.String()
	}

	if _, err := v.call(map[string]any{
		"op":               "start",
		"profile":          json.RawMessage(spec),
		"rootCAs":          string(roots),
		"dnsServer":        dns,
		"proxy":            proxy,
		"browserProxyAuth": browserAuth,
	}); err != nil {
		t.Fatalf("the Node driver: %v: %s", err, stderr.String())
	}

	return v
}

func absPath(t *testing.T, p string) string {
	t.Helper()

	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}

	return a
}

// read hands each answer to its caller until the output ends.
func (v *nodeVia) read(r io.Reader) {
	defer close(v.done)

	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 64<<20)

	for sc.Scan() {
		var a nodeAnswer
		if json.Unmarshal(sc.Bytes(), &a) != nil {
			continue
		}

		v.mu.Lock()

		ch := v.answers[a.ID]

		delete(v.answers, a.ID)
		v.mu.Unlock()

		if ch != nil {
			ch <- a
		}
	}

	v.mu.Lock()

	v.readErr = sc.Err()
	v.mu.Unlock()
}

// call sends one command and waits for its answer.
func (v *nodeVia) call(cmd map[string]any) (nodeAnswer, error) {
	ch := make(chan nodeAnswer, 1)

	v.mu.Lock()

	v.next++

	id := v.next

	cmd["id"] = id
	v.answers[id] = ch

	line, err := json.Marshal(cmd)
	if err == nil {
		_, err = v.stdin.Write(append(line, '\n'))
	}

	v.mu.Unlock()

	if err != nil {
		return nodeAnswer{}, err
	}

	select {
	case a := <-ch:
		if a.Error != "" {
			return a, errors.New(a.Error)
		}

		return a, nil

	case <-v.done:
		v.mu.Lock()
		defer v.mu.Unlock()

		return nodeAnswer{}, fmt.Errorf("the Node driver ended before answering %s (reading it: %w)", cmd["op"], v.readErr)

	case <-v.t.Context().Done():
		return nodeAnswer{}, context.Cause(v.t.Context())
	}
}

// nodeOptions is r as the SDK's Options.
func nodeOptions(r mimicry.Request) map[string]any {
	o := coreOptions(r)

	places := make([]map[string]string, 0, len(o.GetHeaderPlacement()))
	for _, pl := range o.GetHeaderPlacement() {
		places = append(places, map[string]string{
			"header": pl.GetHeader(),
			"after":  pl.GetAfter(),
			"before": pl.GetBefore(),
		})
	}

	return map[string]any{
		"kind":            int(o.GetKind()),
		"page":            o.GetPage(),
		"priority":        int(o.GetPriority()),
		"crossOrigin":     int(o.GetCrossOrigin()),
		"credentials":     int(o.GetCredentials()),
		"noCORS":          o.GetNoCors(),
		"stylesheet":      o.GetStylesheet(),
		"ignoreCORS":      o.GetIgnoreCors(),
		"headerPlacement": places,
		"clientHints":     int(o.GetClientHints()),
	}
}

func (v *nodeVia) send(q pageReq) error {
	h := q.header.Clone()
	order := h[mimicry.HeaderOrderKey]

	delete(h, mimicry.HeaderOrderKey)

	header := make([]map[string]any, 0, len(h))
	for _, name := range slices.Sorted(maps.Keys(h)) {
		values := h[name]
		if values == nil {
			values = []string{}
		}

		header = append(header, map[string]any{
			"name":   name,
			"values": values,
		})
	}

	if order == nil {
		order = []string{}
	}

	_, err := v.call(map[string]any{
		"op":          "send",
		"method":      q.method,
		"url":         q.target,
		"options":     nodeOptions(q.r),
		"header":      header,
		"headerOrder": order,
		"body":        []byte(q.body),
		"stream":      q.stream,
		"exact":       q.exact,
	})

	return err
}

func (v *nodeVia) dial(target string, opts mimicry.WebSocketOptions) (wsConn, error) {
	page := ""
	if opts.From != nil {
		page = opts.From.String()
	}

	protocols := opts.Protocols
	if protocols == nil {
		protocols = []string{}
	}

	a, err := v.call(map[string]any{
		"op":        "dial",
		"url":       target,
		"page":      page,
		"protocols": protocols,
	})
	if err != nil {
		return nil, err
	}

	return nodeSocket{
		v:  v,
		id: a.WS,
	}, nil
}

// nodeSocket is a WebSocket of the Node SDK.
type nodeSocket struct {
	v  *nodeVia
	id int
}

func (s nodeSocket) write(binary bool, data []byte) error {
	_, err := s.v.call(map[string]any{
		"op":     "write",
		"ws":     s.id,
		"binary": binary,
		"data":   data,
	})

	return err
}

func (s nodeSocket) read() ([]byte, error) {
	a, err := s.v.call(map[string]any{
		"op": "read",
		"ws": s.id,
	})
	if err != nil {
		return nil, err
	}

	if a.Data == nil {
		return []byte{}, nil
	}

	return a.Data, nil
}

func (s nodeSocket) close() error {
	_, err := s.v.call(map[string]any{
		"op": "close",
		"ws": s.id,
	})

	return err
}
