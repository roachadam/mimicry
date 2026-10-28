// Package scenario holds the measurements the benchmark makes with one
// client, shared by the Go benchmarks (layers 1 and 2, bench_test.go) and
// cmd/bench's worker processes (layers 3 to 5): warming a client up onto
// the protocol under test, a fixed-duration load at a concurrency, the
// memory an idle connection holds, and a page load.
package scenario

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/roachadam/mimicry/internal/bench/clients"
	"github.com/roachadam/mimicry/internal/bench/site"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// Op is one of the request shapes the micro and load scenarios send.
type Op struct {
	// Name is how results name it: GET, POST or bytes-<size>.
	Name   string
	Method string
	Path   string
	Body   []byte
	// ContentType goes with Body.
	ContentType string
}

// GetJSON is GET /json, a small JSON response.
var GetJSON = Op{
	Name:   "GET",
	Method: "GET",
	Path:   "/json",
}

// PostJSON is POST /echo with a small JSON body.
var PostJSON = Op{
	Name:        "POST",
	Method:      "POST",
	Path:        "/echo",
	Body:        []byte(site.EchoBody),
	ContentType: "application/json",
}

// GetBytes is GET /bytes/{n}.
func GetBytes(n int) Op {
	return Op{
		Name:   "bytes-" + SizeName(n),
		Method: "GET",
		Path:   "/bytes/" + strconv.Itoa(n),
	}
}

// SizeName returns a body size as 1KB, 100KB, 10MB.
func SizeName(n int) string {
	switch {
	case n >= site.MB && n%site.MB == 0:
		return strconv.Itoa(n/site.MB) + "MB"
	case n >= site.KB && n%site.KB == 0:
		return strconv.Itoa(n/site.KB) + "KB"
	}

	return strconv.Itoa(n) + "B"
}

// Request returns op's request to server s over p.
func (op Op) Request(s target.Server, p target.Proto) *clients.Request {
	return &clients.Request{
		Method:      op.Method,
		URL:         s.URL(p, "www", op.Path),
		Body:        op.Body,
		ContentType: op.ContentType,
	}
}

// Setup is a client to measure and what it talks to.
type Setup struct {
	Client clients.Factory
	Server target.Server
	Proto  target.Proto
	// Proxy is a proxy kind, or "".
	Proxy target.ProxyKind
	// Returning builds returning clients (clients.Options.Returning).
	Returning bool
}

// String names the setup in errors.
func (s Setup) String() string {
	out := s.Client.Name + " " + s.Server.Name + " " + string(s.Proto)
	if s.Proxy != "" {
		out += " via " + string(s.Proxy)
	}

	return out
}

// Supported returns nil, or why the client cannot run the setup.
func (s Setup) Supported() error {
	return s.Client.Supports(s.Proto, s.Proxy != "")
}

// New builds the setup's client.
func (s Setup) New() (clients.Client, error) {
	roots, err := target.RootCAs()
	if err != nil {
		return nil, err
	}

	o := clients.Options{
		Proto:     s.Proto,
		RootCAs:   roots,
		Returning: s.Returning,
	}
	if s.Proxy != "" {
		o.Proxy = target.ProxyURL(s.Proxy)
	}

	return s.Client.New(o)
}

// ErrWrongProtocol marks a measurement discarded because a request did not
// go over the protocol under test.
var ErrWrongProtocol = errors.New("wrong protocol")

// Warm sends req until three responses in a row come over p, for clients
// that reach a protocol only after discovering it (mimicry's HTTP/3 after
// Alt-Svc, tls-client's racing). It fails if that takes longer than 15 s.
func Warm(ctx context.Context, c clients.Client, req *clients.Request, p target.Proto) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var (
		inRow int
		last  clients.Response
	)

	for inRow < 3 {
		resp, err := c.Do(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w: still %s after warming up for 15 s", ErrWrongProtocol, last.Proto)
			}

			return err
		}

		if resp.Status != 200 {
			return fmt.Errorf("status %d", resp.Status)
		}

		last = resp

		if resp.Proto != p {
			inRow = 0

			select {
			case <-ctx.Done():
				return fmt.Errorf("%w: still %s after warming up for 15 s", ErrWrongProtocol, resp.Proto)
			case <-time.After(50 * time.Millisecond):
			}

			continue
		}

		inRow++
	}

	return nil
}
