package main

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profile"
)

// engineStep is one request of the engine flow.
type engineStep struct {
	Step   string  `json:"step"`
	URL    string  `json:"url"`
	Proto  string  `json:"proto,omitempty"`
	Status int     `json:"status,omitempty"`
	Bytes  int64   `json:"bytes"`
	MS     float64 `json:"ms"`
	Err    string  `json:"error,omitempty"`
	// altSvc is the response's Alt-Svc.
	altSvc string
}

// engineSummary is what the engine flow showed.
type engineSummary struct {
	// AltSvc is the first response's Alt-Svc.
	AltSvc string `json:"alt_svc,omitempty"`
	// SwitchedAfter is the number of requests before the first one on
	// HTTP/3 (0: never).
	SwitchedAfter int `json:"switched_after"`
	// H3 is true when any request went over HTTP/3.
	H3 bool `json:"h3"`
	// Large and Resumed are the protocols of the larger download and the
	// request on the second connection.
	Large   string `json:"large,omitempty"`
	Resumed string `json:"resumed,omitempty"`
}

// switchAttempts bounds the requests the flow sends waiting for HTTP/3.
const switchAttempts = 8

// engineFlow walks an origin through mimicry's public API, as a page
// would: a first request over TCP that learns Alt-Svc, then the same
// request again until one goes over HTTP/3 the way the profile switches
// (Safari only on a new connection, so its idle connections are closed
// first), a larger download (large, when set), then all connections
// closed and one more request: the second QUIC connection, resumed with
// 0-RTT where the profile does. Every step is a navigation. The Alt-Svc
// is the first one any response carried, redirects included: Firefox
// may already follow a redirect over HTTP/3, on a response without one.
func engineFlow(ctx context.Context, cfg mimicry.Config, home, large string) ([]engineStep, engineSummary) {
	var steps []engineStep
	var sum engineSummary
	client, err := mimicry.NewClient(cfg)
	if err != nil {
		return []engineStep{{
			Step: "config",
			Err:  err.Error(),
		}}, sum
	}
	rec := &altSvcRecorder{
		rt: client.Transport,
	}
	client.Transport = rec
	defer client.CloseIdleConnections()
	get := func(step, url string) engineStep {
		s := fetchVia(ctx, client, step, url)
		steps = append(steps, s)
		return s
	}
	first := get("tcp", home)
	sum.AltSvc = rec.first()
	if first.Err != "" {
		return steps, sum
	}
	if !strings.Contains(sum.AltSvc, "h3=") {
		return steps, sum
	}
	if first.Proto == "HTTP/3.0" {
		sum.SwitchedAfter = 1
	}
	if cfg.Profile.HTTP3.Discovery.Switch == profile.SwitchNewConnection {
		client.CloseIdleConnections()
	}
	for i := range switchAttempts {
		if sum.SwitchedAfter != 0 {
			break
		}
		s := get("switch-"+strconv.Itoa(i+1), home)
		if s.Proto == "HTTP/3.0" {
			sum.SwitchedAfter = i + 2
			break
		}
		if s.Err != "" || ctx.Err() != nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if large != "" {
		sum.Large = get("large", large).Proto
	}
	client.CloseIdleConnections()
	sum.Resumed = get("second-connection", home).Proto
	for _, s := range steps {
		if s.Proto == "HTTP/3.0" {
			sum.H3 = true
		}
	}
	return steps, sum
}

// altSvcRecorder keeps the first h3 Alt-Svc of any response that passes
// through it.
type altSvcRecorder struct {
	rt http.RoundTripper
	mu sync.Mutex
	h3 string
}

func (a *altSvcRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := a.rt.RoundTrip(req)
	if err == nil {
		if v := resp.Header.Get("Alt-Svc"); strings.Contains(v, "h3=") {
			a.mu.Lock()
			if a.h3 == "" {
				a.h3 = v
			}
			a.mu.Unlock()
		}
	}
	return resp, err
}

// CloseIdleConnections closes the wrapped transport's idle connections.
func (a *altSvcRecorder) CloseIdleConnections() {
	if c, ok := a.rt.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

func (a *altSvcRecorder) first() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.h3
}

// fetchVia GETs url through client and reads the body.
func fetchVia(ctx context.Context, client *http.Client, step, url string) (s engineStep) {
	s = engineStep{
		Step: step,
		URL:  url,
	}
	start := time.Now()
	defer func() {
		s.MS = ms(time.Since(start))
	}()
	rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		s.Err = err.Error()
		return s
	}
	resp, err := client.Do(req)
	if err != nil {
		s.Err = err.Error()
		return s
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	s.Proto, s.Status, s.altSvc = resp.Proto, resp.StatusCode, resp.Header.Get("Alt-Svc")
	s.Bytes, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		s.Err = err.Error()
	}
	return s
}

// h3Port returns the port of an Alt-Svc value's h3 alternative on the
// same host, or 0.
func h3Port(altSvc string) uint16 {
	for _, alt := range strings.Split(altSvc, ",") {
		alt = strings.TrimSpace(alt)
		v, ok := strings.CutPrefix(alt, "h3=")
		if !ok {
			continue
		}
		v, _, _ = strings.Cut(v, ";")
		v = strings.Trim(strings.TrimSpace(v), `"`)
		host, port, ok := strings.Cut(v, ":")
		if !ok || host != "" {
			continue
		}
		n, err := strconv.ParseUint(port, 10, 16)
		if err == nil {
			return uint16(n)
		}
	}
	return 0
}
