package main

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profile"
)

// Outcomes of a row.
const (
	pass = "pass"
	fail = "fail"
	na   = "n/a"
)

// result is one row: a case (or a site) against a target, for a profile.
type result struct {
	Target  string `json:"target"`
	Case    string `json:"case"`
	Profile string `json:"profile"`
	Outcome string `json:"outcome"`
	Note    string `json:"note,omitempty"`
	Err     string `json:"error,omitempty"`
	// ServerTestcase is the TESTCASE the server ran with.
	ServerTestcase string        `json:"server_testcase,omitempty"`
	Conns          []connFacts   `json:"conns,omitempty"`
	Fetches        []fetched     `json:"fetches,omitempty"`
	Engine         []engineStep  `json:"engine,omitempty"`
	EngineSummary  engineSummary `json:"engine_summary"`
	MS             float64       `json:"ms"`
	// Dir holds the run's key logs and datagram traces.
	Dir string `json:"dir,omitempty"`
}

// failf marks r failed, keeping the first error.
func (r *result) failf(format string, args ...any) {
	r.Outcome = fail
	if r.Err == "" {
		r.Err = fmt.Sprintf(format, args...)
	}
}

// caseEnv is what a case runs against.
type caseEnv struct {
	profile *profile.Profile
	// host is the server's name, peer its UDP address.
	host  string
	peer  netip.AddrPort
	roots *x509.CertPool
	// files are the files the server serves, by path, with their sums.
	files map[string][32]byte
	// origin is the TCP front's https://host:port, which serves the same
	// files with Alt-Svc pointing at peer's port; resolver maps host to
	// 127.0.0.1 for the engine flow.
	origin   string
	resolver *net.Resolver
	dir      string
	seed     uint64
	// fallback is the TESTCASE the case asked for when the server runs
	// as http3 instead, its mode offering no h3.
	fallback string
	// front serves the files on a listener with Alt-Svc h3 on a port.
	front func(ln net.Listener, port uint16) *http.Server
}

func (e *caseEnv) prober() *prober {
	return newProber(e.profile, e.roots, e.dir, e.seed)
}

// missing is the note of a case whose server feature did not show.
func (e *caseEnv) missing(what string) string {
	if e.fallback == "" {
		return what
	}
	return fmt.Sprintf("%s (its %s mode offers no h3, so it ran as http3)", what, e.fallback)
}

// caseDef is a runner test case as a client can take part in it: the
// TESTCASE the server runs with, how long the case may take, and the
// client's side.
type caseDef struct {
	name     string
	testcase string
	timeout  time.Duration
	run      func(ctx context.Context, e *caseEnv, r *result)
}

// Files the server serves (paths under /www).
const (
	fileSmall = "/1k"
	fileTwo   = "/2m"
	fileThree = "/3m"
	fileFive  = "/5m"
	fileTen   = "/10m"
	// multiplexFiles small files /m0000... are served for multiplexing.
	multiplexFiles = 2000
	multiplexSize  = 32
)

// Impairments of the loss cases: the runner's rates, applied by the
// client's tap in both directions.
var (
	handshakeLoss = impairment{
		Drop: 0.3,
	}
	transferLoss = impairment{
		Drop: 0.02,
	}
	handshakeCorruption = impairment{
		Corrupt: 0.3,
	}
	transferCorruption = impairment{
		Corrupt: 0.02,
	}
	longRTT = impairment{
		Delay: 1500 * time.Millisecond,
	}
	blackhole = impairment{
		BlackholeAfter: 1 << 20,
		BlackholeFor:   2 * time.Second,
	}
)

// handshakeRuns is how many connections the handshake loss cases open
// (the runner's is 50).
const handshakeRuns = 20

// cases are the runner's server-side cases that a browser's client can
// take part in. v2 is left out: no browser profile offers it.
var cases = []caseDef{
	{
		name:     "handshake",
		testcase: "handshake",
		timeout:  30 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, impairment{}, false, fileSmall)
		},
	},
	{
		name:     "transfer",
		testcase: "transfer",
		timeout:  90 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, impairment{}, true, fileTwo, fileThree, fileFive)
		},
	},
	{
		name:     "multiplexing",
		testcase: "transfer",
		timeout:  120 * time.Second,
		run:      runMultiplexing,
	},
	{
		name:     "retry",
		testcase: "retry",
		timeout:  30 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, impairment{}, false, fileSmall)
			if r.Outcome == pass && len(r.Conns) > 0 && r.Conns[0].Retries == 0 {
				r.Outcome, r.Note = na, e.missing("the server sent no Retry")
			}
		},
	},
	{
		name:     "resumption",
		testcase: "resumption",
		timeout:  60 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			runResumption(ctx, e, r, false)
		},
	},
	{
		name:     "zerortt",
		testcase: "zerortt",
		timeout:  60 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			runResumption(ctx, e, r, true)
		},
	},
	{
		name:     "chacha20",
		testcase: "chacha20",
		timeout:  30 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, impairment{}, false, fileSmall)
			if r.Outcome == pass && len(r.Conns) > 0 && r.Conns[0].Cipher != "0x1303" {
				r.Outcome, r.Note = na, e.missing("the server chose "+r.Conns[0].Cipher)
			}
		},
	},
	{
		name:     "keyupdate",
		testcase: "transfer",
		timeout:  90 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, impairment{}, false, fileTen)
			if r.Outcome != pass || len(r.Conns) == 0 {
				return
			}
			c := r.Conns[0]
			switch {
			case !c.KUKnown:
				r.Outcome, r.Note = na, "no 1-RTT secrets to read key phases"
			case c.ServerKU == 0:
				r.Outcome, r.Note = na, "the server started no key update (browsers start none)"
			default:
				r.Note = fmt.Sprintf("server key updates %d, client followed %d", c.ServerKU, c.ClientKU)
			}
		},
	},
	{
		name:     "handshakeloss",
		testcase: "multiconnect",
		timeout:  300 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			manyConns(ctx, e, r, handshakeLoss)
		},
	},
	{
		name:     "handshakecorruption",
		testcase: "multiconnect",
		timeout:  300 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			manyConns(ctx, e, r, handshakeCorruption)
		},
	},
	{
		name:     "transferloss",
		testcase: "transfer",
		timeout:  180 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, transferLoss, true, fileTwo, fileThree)
		},
	},
	{
		name:     "transfercorruption",
		testcase: "transfer",
		timeout:  180 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, transferCorruption, true, fileTwo, fileThree)
		},
	},
	{
		name:     "longrtt",
		testcase: "handshake",
		timeout:  120 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, longRTT, false, fileSmall)
		},
	},
	{
		name:     "blackhole",
		testcase: "transfer",
		timeout:  120 * time.Second,
		run: func(ctx context.Context, e *caseEnv, r *result) {
			oneConn(ctx, e, r, blackhole, false, fileFive)
		},
	},
	{
		name:     "discovery",
		testcase: "http3",
		timeout:  120 * time.Second,
		run:      runDiscovery,
	},
	{
		name:     "udpblocked",
		testcase: "http3",
		timeout:  300 * time.Second,
		run:      runUDPBlocked,
	},
}

// oneConn opens one connection under imp and GETs paths on it, all at
// once when concurrent.
func oneConn(ctx context.Context, e *caseEnv, r *result, imp impairment, concurrent bool, paths ...string) {
	pr := e.prober()
	c, err := pr.dial(ctx, e.host, e.peer, imp)
	if err != nil {
		if c != nil {
			r.Conns = append(r.Conns, c.facts())
		}
		r.failf("dial: %s", errString(err))
		return
	}
	fs := getAll(ctx, pr, c, paths, concurrent)
	r.Conns = append(r.Conns, c.facts())
	pr.close(c) //nolint:contextcheck // closing outlives the case's context
	r.Fetches = append(r.Fetches, fs...)
	e.verify(r, fs)
}

// getAll GETs paths on c, concurrently or one after another.
func getAll(ctx context.Context, pr *prober, c *probeConn, paths []string, concurrent bool) []fetched {
	fs := make([]fetched, len(paths))
	if !concurrent {
		for i, p := range paths {
			fs[i] = pr.get(ctx, c, p)
		}
		return fs
	}
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Go(func() {
			fs[i] = pr.get(ctx, c, p)
		})
	}
	wg.Wait()
	return fs
}

// verify fails r unless every fetch got its file whole.
func (e *caseEnv) verify(r *result, fs []fetched) {
	for _, f := range fs {
		switch {
		case f.Err != "":
			r.failf("GET %s: %s", f.Path, f.Err)
		case f.Status != 200:
			r.failf("GET %s: status %d", f.Path, f.Status)
		case f.sum != e.files[f.Path]:
			r.failf("GET %s: body differs (%d bytes)", f.Path, f.Bytes)
		}
	}
}

// runMultiplexing GETs every small file at once on one connection, more
// streams than any server allows at first.
func runMultiplexing(ctx context.Context, e *caseEnv, r *result) {
	pr := e.prober()
	c, err := pr.dial(ctx, e.host, e.peer, impairment{})
	if err != nil {
		if c != nil {
			r.Conns = append(r.Conns, c.facts())
		}
		r.failf("dial: %s", errString(err))
		return
	}
	paths := make([]string, multiplexFiles)
	for i := range paths {
		paths[i] = multiplexPath(i)
	}
	fs := getAll(ctx, pr, c, paths, true)
	r.Conns = append(r.Conns, c.facts())
	pr.close(c) //nolint:contextcheck // closing outlives the case's context
	e.verify(r, fs)
	ok := 0
	for _, f := range fs {
		if f.Err == "" && f.Status == 200 && f.sum == e.files[f.Path] {
			ok++
			continue
		}
		if len(r.Fetches) < 10 {
			r.Fetches = append(r.Fetches, f)
		}
	}
	r.Note = fmt.Sprintf("%d/%d files", ok, len(fs))
}

func multiplexPath(i int) string {
	return fmt.Sprintf("/m%04d", i)
}

// runResumption opens a connection, waits for its ticket, closes it and
// opens a second one, which resumes it (and sends its request in 0-RTT:
// the browsers that resume QUIC always offer early data). zeroRTT: the
// server should accept the early data.
func runResumption(ctx context.Context, e *caseEnv, r *result, zeroRTT bool) {
	pr := e.prober()
	if !pr.resumes() {
		r.Outcome, r.Note = na, "the profile never resumes QUIC (Safari)"
		return
	}
	c, err := pr.dial(ctx, e.host, e.peer, impairment{})
	if err != nil {
		if c != nil {
			r.Conns = append(r.Conns, c.facts())
		}
		r.failf("dial: %s", errString(err))
		return
	}
	fs := getAll(ctx, pr, c, []string{fileSmall}, false)
	ticket := pr.waitTicket(ctx, 2*time.Second)
	r.Conns = append(r.Conns, c.facts())
	pr.close(c) //nolint:contextcheck // closing outlives the case's context
	r.Fetches = append(r.Fetches, fs...)
	e.verify(r, fs)
	if r.Outcome == fail {
		return
	}
	if !ticket {
		r.failf("no session ticket within 2 s")
		return
	}
	c, err = pr.dial(ctx, e.host, e.peer, impairment{})
	if err != nil {
		if c != nil {
			r.Conns = append(r.Conns, c.facts())
		}
		r.failf("resumed dial: %s", errString(err))
		return
	}
	fs = getAll(ctx, pr, c, []string{fileSmall}, false)
	f := c.facts()
	r.Conns = append(r.Conns, f)
	pr.close(c) //nolint:contextcheck // closing outlives the case's context
	r.Fetches = append(r.Fetches, fs...)
	e.verify(r, fs)
	if r.Outcome == fail {
		return
	}
	switch {
	case !f.Resumed:
		r.failf("the second connection did not resume")
	case zeroRTT && f.Sent0RTT == 0:
		// The profile sends 0-RTT whenever its ticket allows it, so the
		// server's ticket allowed none (max_early_data_size 0).
		r.Outcome, r.Note = na, e.missing("the server's ticket allows no 0-RTT")
	case zeroRTT && !f.EarlyData && e.fallback != "":
		r.Outcome, r.Note = na, e.missing("the server rejected 0-RTT")
	case zeroRTT && !f.EarlyData:
		r.failf("the server rejected 0-RTT (the request went again in 1-RTT)")
	case !zeroRTT && !f.EarlyData:
		r.Note = "0-RTT rejected, request resent in 1-RTT"
	case !zeroRTT:
		r.Note = "0-RTT accepted"
	}
}

// manyConns opens handshakeRuns connections one after another under imp,
// each GETting the small file. Later connections resume earlier ones'
// tickets where the profile does, as the browser's would.
func manyConns(ctx context.Context, e *caseEnv, r *result, imp impairment) {
	pr := e.prober()
	ok := 0
	for i := range handshakeRuns {
		one := result{
			Outcome: pass,
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		c, err := pr.dial(cctx, e.host, e.peer, imp)
		var fs []fetched
		if err != nil {
			one.failf("dial: %s", errString(err))
		} else {
			fs = getAll(cctx, pr, c, []string{fileSmall}, false)
			e.verify(&one, fs)
		}
		var f connFacts
		if c != nil {
			f = c.facts()
		}
		pr.close(c) //nolint:contextcheck // closing outlives the case's context
		cancel()
		if one.Outcome == pass {
			ok++
			continue
		}
		if r.Outcome != fail {
			r.failf("connection %d: %s", i+1, one.Err)
			r.Conns = append(r.Conns, f)
			r.Fetches = append(r.Fetches, fs...)
		}
	}
	r.Note = fmt.Sprintf("%d/%d connections", ok, handshakeRuns)
}

// runDiscovery walks the server through mimicry's public API: the TCP
// front's Alt-Svc moves the origin to the server's HTTP/3 as the profile
// switches.
func runDiscovery(ctx context.Context, e *caseEnv, r *result) {
	cfg := mimicry.Config{
		Profile:     e.profile,
		RootCAs:     e.roots,
		Resolver:    e.resolver,
		DialTimeout: 10 * time.Second,
	}
	steps, sum := engineFlow(ctx, cfg, e.origin+fileSmall, e.origin+fileFive)
	r.Engine, r.EngineSummary = steps, sum
	for _, s := range steps {
		if s.Err != "" {
			r.failf("%s: %s", s.Step, s.Err)
		}
	}
	switch {
	case r.Outcome == fail:
	case !sum.H3:
		r.failf("no request went over HTTP/3")
	case sum.Large != "HTTP/3.0":
		r.failf("the large download went over %s", sum.Large)
	case sum.Resumed != "HTTP/3.0":
		r.failf("the second connection's request went over %s", sum.Resumed)
	default:
		r.Note = fmt.Sprintf("HTTP/3 from request %d", sum.SwitchedAfter)
	}
}

// stallLimit is the longest a request may wait for a QUIC connection
// that never comes before it goes over TCP: the case fails past it.
// [INFERENCE] Chrome races TCP beside QUIC (its alternative and main
// jobs), so a browser shows no such wait; the limit only keeps a
// timeout-sized stall from passing.
const stallLimit = 5 * time.Second

// runUDPBlocked walks, through mimicry's public API with the default dial
// timeout, an origin whose Alt-Svc names a UDP port where every datagram
// is dropped (a network that blocks UDP, or a broken alternative): every
// request must still get its response, over TCP, and none may stall.
func runUDPBlocked(ctx context.Context, e *caseEnv, r *result) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		r.failf("listen: %v", err)
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // tcp4 listener
	sink, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		_ = ln.Close()
		r.failf("listen: %v", err)
		return
	}
	defer func() {
		_ = sink.Close()
	}()
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sink.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	front := e.front(ln, uint16(port)) //nolint:gosec // a port
	defer func() {
		_ = front.Close()
	}()
	origin := "https://" + net.JoinHostPort(e.host, strconv.Itoa(port))
	steps, sum := engineFlow(ctx, mimicry.Config{
		Profile:  e.profile,
		RootCAs:  e.roots,
		Resolver: e.resolver,
	}, origin+fileSmall, origin+fileTwo)
	r.Engine, r.EngineSummary = steps, sum
	worst := engineStep{}
	for _, s := range steps {
		if s.Err != "" {
			r.failf("%s: %s", s.Step, s.Err)
		}
		if s.MS > worst.MS {
			worst = s
		}
	}
	r.Note = fmt.Sprintf("slowest request: %s, %.0f ms", worst.Step, worst.MS)
	if r.Outcome != fail && worst.MS > ms(stallLimit) {
		r.failf("request %s waited %.1f s for QUIC before going over %s", worst.Step, worst.MS/1000, worst.Proto)
	}
}
