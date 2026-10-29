package scenario

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roachadam/mimicry/internal/bench/clients"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// MultiSpec is a multi-client point (layer 6): Clients clients of one kind
// in this process, each its own client (Transport or session) through the
// proxy as a user of its own, each sending Op one request at a time, for
// Duration after a warm-up.
type MultiSpec struct {
	Op       Op
	Clients  int
	Warmup   time.Duration
	Duration time.Duration
	// Rotate gives each client a new proxy identity every Rotate requests
	// (new credentials: a new tunnel or association and a new handshake,
	// as a rotating proxy forces); 0 keeps one per client.
	Rotate int
	// Renew, with Rotate, replaces the client instead of moving it: every
	// Rotate requests each routine builds a new client with the library's
	// constructor and the next identity's credentials, and drops the old
	// one (RenewDropped) or closes it first (RenewClosed: Client.Close).
	Renew Renew
	// Tag makes the run's proxy users unique: <target.ProxyUser>-<Tag>-
	// <client>-<identity>.
	Tag string
	// OnWindow is called as the measurement window opens (true) and closes
	// (false), for profiling just the window.
	OnWindow func(open bool) `json:"-"`
}

// Renew is how a client-rotation point retires a client.
type Renew string

// Renew modes.
const (
	// RenewDropped abandons the old client, as code that makes a client
	// per proxy and lets it go does.
	RenewDropped Renew = "dropped"
	// RenewClosed closes it first (clients.Client.Close: the library's
	// CloseIdleConnections, or its Close where it has one).
	RenewClosed Renew = "closed"
)

// costClients is how many new clients the cost phase measures, after one
// that warms the process up (and, for a returning mimicry, learns the
// origin's HTTP/3).
const costClients = 20

// MultiResult is what a multi-client point measured: the requests that
// completed inside the window, over every client.
type MultiResult struct {
	Clients    int     `json:"clients"`
	Requests   int64   `json:"requests"`
	Errors     int64   `json:"errors"`
	FirstError string  `json:"firstError,omitempty"`
	Mismatches int64   `json:"mismatches"`
	Seconds    float64 `json:"seconds"`
	RPS        float64 `json:"rps"`
	// MBps is response body megabytes (10^6) per second.
	MBps float64 `json:"mbps"`
	// Latencies of every request, in milliseconds.
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	// NewConns are the requests that were their identity's first (with
	// rotation): each needs a new tunnel or association and a handshake.
	// Their latency (from the switch to the new identity to the response)
	// in ms, how many the server said resumed a TLS session, and the
	// identities started per second.
	NewConns        int64   `json:"newConns"`
	NewP50          float64 `json:"newP50"`
	NewP99          float64 `json:"newP99"`
	NewResumed      int64   `json:"newResumed"`
	RotationsPerSec float64 `json:"rotationsPerSec"`
	// TunnelsPerSec and AssociationsPerSec are the TCP tunnels and SOCKS5
	// UDP associations the proxy opened for the run's users per second of
	// the window: the new connections, each with its handshake.
	TunnelsPerSec      float64 `json:"tunnelsPerSec"`
	AssociationsPerSec float64 `json:"associationsPerSec"`
	// ClientCores is the process's CPU time over the window, in cores;
	// CPUPerRequest its CPU time per request, in microseconds.
	ClientCores   float64 `json:"clientCores"`
	CPUPerRequest float64 `json:"cpuPerRequest"`
	// Allocation per request inside the window.
	BytesPerRequest  float64 `json:"bytesPerRequest"`
	AllocsPerRequest float64 `json:"allocsPerRequest"`
	// GC over the window.
	GCs       uint32  `json:"gcs"`
	GCPauseMs float64 `json:"gcPauseMs"`
	// Memory per client, beyond what the process held before the clients
	// were built: the live heap as the window's GCs marked it, averaged
	// over the window (steady state); the peak heap, garbage not yet swept
	// included (sampled every 100 ms); the goroutine stacks in use at the
	// window's end, the goroutine sending each client's requests included.
	// HeapPeakMB is the process's peak heap in all; GoroutinesPerClient
	// the clients' own goroutines (the senders left out).
	LiveKBPerClient     float64 `json:"liveKbPerClient"`
	PeakKBPerClient     float64 `json:"peakKbPerClient"`
	StackKBPerClient    float64 `json:"stackKbPerClient"`
	HeapPeakMB          float64 `json:"heapPeakMb"`
	GoroutinesPerClient float64 `json:"goroutinesPerClient"`
	// ServerCPU and ProxyCPU are their CPU use over the window, as a share
	// of their CPUs; -1 when unknown.
	ServerCPU float64 `json:"serverCpu"`
	ProxyCPU  float64 `json:"proxyCpu"`
	// With Renew (client rotation), a "client" above is a routine: the
	// one goroutine that sends one request at a time, its client replaced
	// every Rotate requests. NewP50/NewP99 then run from the switch (the
	// old client's Close included) to the new client's first response, and
	// BuildP50/BuildP99 are the constructor's share, in ms. Cost is a new
	// client measured alone before the load; Samples the process and the
	// proxy every second of the window; End what is left once the window
	// closes and the routines stop; Growth the slope over Samples.
	BuildP50 float64        `json:"buildP50,omitempty"`
	BuildP99 float64        `json:"buildP99,omitempty"`
	Cost     *NewClientCost `json:"cost,omitempty"`
	Samples  []MultiSample  `json:"samples,omitempty"`
	End      *MultiEnd      `json:"end,omitempty"`
	Growth   *MultiGrowth   `json:"growth,omitempty"`
	// Plateau, with proxy rotation (Rotate without Renew), is where the
	// process's memory levels off over a long window, from the per-second
	// Samples.
	Plateau *MultiPlateau `json:"plateau,omitempty"`
	// Stuck is how many routines were still in a request a minute after
	// the window closed (their context is cancelled then).
	Stuck int `json:"stuck,omitempty"`
	// ProxyCheck is what the proxy-side check found wrong, or empty: every
	// identity a client got a response on was seen by the proxy, the proxy
	// relayed each client at least the body bytes it received, and over
	// HTTP/3 each client had a UDP association that carried datagrams.
	ProxyCheck string `json:"proxyCheck,omitempty"`
	// Discarded says why the point does not count, or is empty.
	Discarded string `json:"discarded,omitempty"`
}

// NewClientCost is what one new client costs, measured alone (nothing
// else running in the process) over costClients clients, each with an
// identity of its own: medians of the constructor's time, the first
// request's, and what both allocated together. OnProto is how many first
// responses came over the protocol under test.
type NewClientCost struct {
	Clients     int     `json:"clients"`
	ConstructUs float64 `json:"constructUs"`
	FirstMs     float64 `json:"firstMs"`
	KB          float64 `json:"kb"`
	Allocs      float64 `json:"allocs"`
	OnProto     int     `json:"onProto"`
}

// MultiSample is one second's sample of a client-rotation window: the
// seconds since it opened, the process's live heap (as the last GC marked
// it) in MB, its goroutines, its resident memory in MB (-1 when unknown),
// and the tunnels and associations open at the proxy for the run's users
// (-1 when unknown).
type MultiSample struct {
	T          float64 `json:"t"`
	LiveMB     float64 `json:"liveMb"`
	Goroutines int     `json:"goroutines"`
	RSSMB      float64 `json:"rssMb"`
	Open       int64   `json:"open"`
}

// MultiEnd is what a client-rotation point left once its window closed and
// its routines stopped, nothing closed by the benchmark yet: the live heap
// (after GC) and the goroutines beyond what the process had before the
// clients were built, per routine; the process's resident memory in MB
// (-1 when unknown); and the tunnels and associations still open at the
// proxy for the run's users.
type MultiEnd struct {
	LiveKBPerRoutine     float64 `json:"liveKbPerRoutine"`
	GoroutinesPerRoutine float64 `json:"goroutinesPerRoutine"`
	RSSMB                float64 `json:"rssMb"`
	OpenTunnels          int64   `json:"openTunnels"`
	OpenAssociations     int64   `json:"openAssociations"`
}

// MultiGrowth is the least-squares slope over a window's samples, per
// second: live heap KB and goroutines per routine, resident MB, and open
// tunnels and associations at the proxy (all routines together).
type MultiGrowth struct {
	LiveKBPerRoutine     float64 `json:"liveKbPerRoutine"`
	GoroutinesPerRoutine float64 `json:"goroutinesPerRoutine"`
	RSSMB                float64 `json:"rssMb"`
	Open                 float64 `json:"open"`
}

// sender is one client and what its requests measured.
type sender struct {
	c clients.Client
	// identity is the proxy identity in use, sent the requests sent on it.
	identity int
	sent     int
	// used are the identities that got a response, in order; bytes the
	// body bytes received over the whole run (warm-up and window).
	used  []int
	bytes int64

	requests, errors, mismatches int64
	newConns, newResumed         int64
	body                         int64
	firstErr                     string
}

// lockedHist is a histogram the senders share.
type lockedHist struct {
	mu sync.Mutex
	h  hist
}

func (l *lockedHist) record(d time.Duration) {
	l.mu.Lock()
	l.h.record(d)
	l.mu.Unlock()
}

// Multi runs a multi-client point with su's client kind through su.Proxy,
// which must take credentials (target.ProxyUserURL). It builds the
// clients, warms each onto the protocol (Warm), runs Warmup, then measures
// for Duration, then checks with the proxy that every client went through
// it. With Renew it first measures a new client's cost alone, samples the
// process and the proxy every second of the window, and reads what is left
// once the routines stop.
func Multi(ctx context.Context, su Setup, spec MultiSpec) (MultiResult, error) {
	res := MultiResult{
		Clients: spec.Clients,
	}

	if target.ProxyUserURL(su.Proxy, "x") == "" {
		return res, fmt.Errorf("scenario: proxy kind %q takes no credentials", su.Proxy)
	}

	if spec.Renew != "" && spec.Rotate <= 0 {
		return res, fmt.Errorf("scenario: client rotation (%s) needs Rotate", spec.Renew)
	}

	roots, err := target.RootCAs()
	if err != nil {
		return res, err
	}

	prefix := target.ProxyUser + "-" + spec.Tag + "-"
	user := func(client, identity int) string {
		return prefix + strconv.Itoa(client) + "-" + strconv.Itoa(identity)
	}

	build := func(name string) (clients.Client, error) {
		o := su.options(roots)

		o.Proxy = target.ProxyUserURL(su.Proxy, name)

		return su.Client.New(o)
	}

	req := spec.Op.Request(su.Server, su.Proto)
	window, fresh, built := new(lockedHist), new(lockedHist), new(lockedHist)

	if spec.Renew != "" {
		// Its users have a prefix of their own, outside the run's: the
		// proxy check and the open counts are the routines' alone.
		costPrefix := target.ProxyUser + "-" + spec.Tag + "cost-"

		cost, err := newClientCost(ctx, su, req, func(j int) (clients.Client, error) {
			return build(costPrefix + strconv.Itoa(j))
		})
		if err != nil {
			res.Discarded = "new-client cost: " + err.Error()

			return res, nil
		}

		res.Cost = &cost

		_, _ = target.FetchProxyUsage(ctx, costPrefix, false, true)
	}

	senders := make([]*sender, spec.Clients)
	for i := range senders {
		senders[i] = new(sender)
	}

	base := settled(0)
	baseHeap := heapMetrics()

	defer func() {
		for _, s := range senders {
			if s.c != nil {
				s.c.Close()
			}
		}
	}()

	for i, s := range senders {
		if s.c, err = build(user(i, 0)); err != nil {
			return res, err
		}
	}

	if err := warmAll(ctx, senders, req, su.Proto); err != nil {
		res.Discarded = err.Error()

		return res, nil
	}

	for _, s := range senders {
		s.used = append(s.used, 0)
	}

	var (
		phase   atomic.Int32
		running atomic.Int32
		wg      sync.WaitGroup
	)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for i, s := range senders {
		rot := rotation{
			every: spec.Rotate,
			renew: spec.Renew,
			proxyURL: func(identity int) string {
				return target.ProxyUserURL(su.Proxy, user(i, identity))
			},
			build: func(identity int) (clients.Client, error) {
				return build(user(i, identity))
			},
		}

		running.Add(1)

		wg.Go(func() {
			defer running.Add(-1)

			s.run(runCtx, &phase, req, su, rot, window, fresh, built)
		})
	}

	sleep(ctx, spec.Warmup)

	heap := sampleHeap()
	useBefore, useBeforeErr := target.FetchProxyUsage(ctx, prefix, false, false)
	proxyBefore, proxyBeforeErr := target.FetchStats(ctx, target.ProxyHost, target.ProxyStatsPort)
	before := snapshot(ctx, su.Server)

	var samples func() []MultiSample
	if spec.Rotate > 0 {
		samples = sampleEverySecond(ctx, prefix)
	}

	if spec.OnWindow != nil {
		spec.OnWindow(true)
	}

	phase.Store(phaseMeasure)
	sleep(ctx, spec.Duration)

	useAfter, useAfterErr := target.FetchProxyUsage(ctx, prefix, false, false)
	proxyAfter, proxyAfterErr := target.FetchStats(ctx, target.ProxyHost, target.ProxyStatsPort)
	after := snapshot(ctx, su.Server)

	phase.Store(phaseStop)

	if spec.OnWindow != nil {
		spec.OnWindow(false)
	}

	peak, live := heap()

	if samples != nil {
		res.Samples = samples()
	}

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	// A routine still in its request a minute after the window closed is
	// stuck: the end is read before its context is cancelled, which could
	// end connections tied to it.
	select {
	case <-done:
	case <-time.After(60 * time.Second):
	}

	stuck := int(running.Load())

	var end memory
	if spec.Renew != "" {
		// Nothing is closed yet.
		end = settled(0)
	}

	endRSS := rssMB()
	usage, usageErr := target.FetchProxyUsage(ctx, prefix, true, true)

	if stuck > 0 {
		cancel()
		<-done
	}

	res.Stuck = stuck

	var bytes int64

	for _, s := range senders {
		res.Requests += s.requests
		res.Errors += s.errors
		res.Mismatches += s.mismatches
		res.NewConns += s.newConns
		res.NewResumed += s.newResumed
		bytes += s.body

		if res.FirstError == "" {
			res.FirstError = s.firstErr
		}
	}

	w := after.at.Sub(before.at)
	n := float64(spec.Clients)

	res.Seconds = w.Seconds()
	res.RPS = float64(res.Requests) / res.Seconds
	res.MBps = float64(bytes) / 1e6 / res.Seconds
	res.P50 = ms(window.h.quantile(0.50))
	res.P95 = ms(window.h.quantile(0.95))
	res.P99 = ms(window.h.quantile(0.99))
	res.NewP50 = ms(fresh.h.quantile(0.50))
	res.NewP99 = ms(fresh.h.quantile(0.99))
	res.RotationsPerSec = float64(res.NewConns) / res.Seconds

	if spec.Renew != "" {
		res.BuildP50 = ms(built.h.quantile(0.50))
		res.BuildP99 = ms(built.h.quantile(0.99))
	}

	if useBeforeErr == nil && useAfterErr == nil {
		res.TunnelsPerSec = float64(useAfter.Total.Tunnels-useBefore.Total.Tunnels) / res.Seconds
		res.AssociationsPerSec = float64(useAfter.Total.Associations-useBefore.Total.Associations) / res.Seconds
	}

	cpu := after.cpu - before.cpu

	res.ClientCores = cpu.Seconds() / res.Seconds

	if res.Requests > 0 {
		r := float64(res.Requests)

		res.CPUPerRequest = float64(cpu.Microseconds()) / r
		res.BytesPerRequest = float64(after.mem.TotalAlloc-before.mem.TotalAlloc) / r
		res.AllocsPerRequest = float64(after.mem.Mallocs-before.mem.Mallocs) / r
	}

	res.GCs = after.mem.NumGC - before.mem.NumGC
	res.GCPauseMs = float64(after.mem.PauseTotalNs-before.mem.PauseTotalNs) / 1e6
	res.LiveKBPerClient = kbPer(live, baseHeap.live, spec.Clients)
	res.PeakKBPerClient = kbPer(peak, baseHeap.objects, spec.Clients)
	res.StackKBPerClient = kbPer(after.mem.StackInuse, base.stack, spec.Clients)
	res.HeapPeakMB = float64(peak) / 1e6
	res.GoroutinesPerClient = float64(after.goroutines-base.goroutines-spec.Clients) / n

	res.ServerCPU = -1
	if before.serverErr == nil && after.serverErr == nil {
		res.ServerCPU = target.Utilisation(before.server, after.server, w)
	}

	res.ProxyCPU = -1
	if proxyBeforeErr == nil && proxyAfterErr == nil {
		res.ProxyCPU = target.Utilisation(proxyBefore, proxyAfter, w)
	}

	if usageErr != nil {
		res.ProxyCheck = "reading the proxy's usage: " + usageErr.Error()
	} else {
		res.ProxyCheck = proxyCheck(senders, usage.Users, user, su.Proto == target.H3)
	}

	if spec.Renew != "" {
		res.End = &MultiEnd{
			LiveKBPerRoutine:     kbPer(end.heap, base.heap, spec.Clients),
			GoroutinesPerRoutine: float64(end.goroutines-base.goroutines-stuck) / n,
			RSSMB:                endRSS,
			OpenTunnels:          -1,
			OpenAssociations:     -1,
		}
		if usageErr == nil {
			res.End.OpenTunnels = usage.Total.OpenTunnels
			res.End.OpenAssociations = usage.Total.OpenAssociations
		}

		res.Growth = growth(res.Samples, spec.Clients)
	} else if spec.Rotate > 0 {
		res.Plateau = plateau(res.Samples, baseHeap.live, base.goroutines+spec.Clients, spec.Clients)
	}

	res.Discarded = multiDiscard(res, spec.Renew != "")

	return res, nil
}

// newClientCost measures costClients new clients one at a time, each
// built by build(j) and closed after its first response, nothing else
// running: the first (j 0) only warms the process up.
func newClientCost(ctx context.Context, su Setup, req *clients.Request, build func(j int) (clients.Client, error)) (NewClientCost, error) {
	var (
		construct, first, kb, allocs []float64
		onProto                      int
	)

	for j := range costClients + 1 {
		var m0, m1 runtime.MemStats

		runtime.ReadMemStats(&m0)

		start := time.Now()

		c, err := build(j)
		if err != nil {
			return NewClientCost{}, err
		}

		built := time.Now()

		resp, err := c.Do(ctx, req)
		got := time.Now()

		runtime.ReadMemStats(&m1)
		c.Close()

		switch {
		case err != nil:
			return NewClientCost{}, err
		case resp.Status != 200:
			return NewClientCost{}, fmt.Errorf("status %d", resp.Status)
		case j == 0:
			continue
		}

		if resp.Proto == su.Proto {
			onProto++
		}

		construct = append(construct, float64(built.Sub(start).Microseconds()))
		first = append(first, ms(got.Sub(built)))
		kb = append(kb, float64(m1.TotalAlloc-m0.TotalAlloc)/1024)
		allocs = append(allocs, float64(m1.Mallocs-m0.Mallocs))
	}

	return NewClientCost{
		Clients:     costClients,
		ConstructUs: median(construct),
		FirstMs:     median(first),
		KB:          median(kb),
		Allocs:      median(allocs),
		OnProto:     onProto,
	}, nil
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}

	s := slices.Clone(v)
	slices.Sort(s)

	if len(s)%2 == 1 {
		return s[len(s)/2]
	}

	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// sampleEverySecond samples the process, and the tunnels and associations
// open at the proxy for the users with prefix, now and every second until
// the returned function is called, which returns the samples.
func sampleEverySecond(ctx context.Context, prefix string) func() []MultiSample {
	stop := make(chan struct{})
	done := make(chan []MultiSample)
	start := time.Now()

	go func() {
		var out []MultiSample

		t := time.NewTicker(time.Second)
		defer t.Stop()

		for {
			sm := MultiSample{
				T:          time.Since(start).Seconds(),
				LiveMB:     float64(heapMetrics().live) / 1e6,
				Goroutines: runtime.NumGoroutine(),
				RSSMB:      rssMB(),
				Open:       -1,
			}

			if u, err := target.FetchProxyUsage(ctx, prefix, false, false); err == nil {
				sm.Open = u.Total.OpenTunnels + u.Total.OpenAssociations
			}

			out = append(out, sm)

			select {
			case <-stop:
				done <- out

				return

			case <-t.C:
			}
		}
	}()

	return func() []MultiSample {
		close(stop)

		return <-done
	}
}

// rssMB is the process's resident memory in MB, or -1 off Linux.
func rssMB() float64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return -1
	}

	f := strings.Fields(string(b))
	if len(f) < 2 {
		return -1
	}

	pages, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return -1
	}

	return float64(pages*int64(os.Getpagesize())) / 1e6
}

// growth is the least-squares slope of each sampled value per second, per
// routine where MultiGrowth says so; nil with fewer than two samples.
func growth(samples []MultiSample, routines int) *MultiGrowth {
	if len(samples) < 2 {
		return nil
	}

	slope := func(get func(MultiSample) float64) float64 {
		var sx, sy, sxx, sxy float64

		k := float64(len(samples))

		for _, sm := range samples {
			x, y := sm.T, get(sm)

			sx += x
			sy += y
			sxx += x * x
			sxy += x * y
		}

		d := k*sxx - sx*sx
		if d == 0 {
			return 0
		}

		return (k*sxy - sx*sy) / d
	}

	n := float64(routines)

	return &MultiGrowth{
		LiveKBPerRoutine: slope(func(sm MultiSample) float64 {
			return sm.LiveMB * 1e6 / 1024 / n
		}),
		GoroutinesPerRoutine: slope(func(sm MultiSample) float64 {
			return float64(sm.Goroutines) / n
		}),
		RSSMB: slope(func(sm MultiSample) float64 {
			return sm.RSSMB
		}),
		Open: slope(func(sm MultiSample) float64 {
			return float64(sm.Open)
		}),
	}
}

// warmAll warms every sender's client onto p at once.
func warmAll(ctx context.Context, senders []*sender, req *clients.Request, p target.Proto) error {
	errs := make([]error, len(senders))

	var wg sync.WaitGroup

	for i, s := range senders {
		wg.Go(func() {
			errs[i] = Warm(ctx, s.c, req, p)
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return fmt.Errorf("client %d: %w", i, err)
		}
	}

	return nil
}

// rotation is how a sender moves to its next identity every requests:
// SetProxy to proxyURL's, or with renew a new client from build, the old
// one closed first with RenewClosed.
type rotation struct {
	every    int
	renew    Renew
	proxyURL func(identity int) string
	build    func(identity int) (clients.Client, error)
}

// run sends req one at a time until phase says stop, moving to a new
// identity every rot.every requests when it is set. Latencies of requests
// completing in the window go to window, those of each identity's first
// request (from the switch on) to fresh as well, and with renew the
// constructor's time to built.
func (s *sender) run(ctx context.Context, phase *atomic.Int32, req *clients.Request, su Setup, rot rotation,
	window, fresh, built *lockedHist,
) {
	for phase.Load() != phaseStop && ctx.Err() == nil {
		start := time.Now()
		first := false

		if rot.every > 0 && s.sent >= rot.every {
			s.identity++

			s.sent = 0
			first = true

			if err := s.next(rot, phase, built); err != nil {
				s.fail(phase, err.Error())

				continue
			}
		}

		resp, err := s.c.Do(ctx, req)
		d := time.Since(start)

		s.sent++

		if err == nil && resp.Status == 200 {
			s.bytes += resp.Bytes

			if s.used[len(s.used)-1] != s.identity {
				s.used = append(s.used, s.identity)
			}
		}

		if phase.Load() != phaseMeasure {
			continue
		}

		switch {
		case err != nil:
			s.fail(phase, err.Error())

		case resp.Status != 200:
			s.fail(phase, fmt.Sprintf("status %d", resp.Status))

		case resp.Proto != su.Proto:
			s.mismatches++
			if s.firstErr == "" {
				s.firstErr = clients.Check(resp, su.Proto).Error()
			}

		default:
			s.requests++

			s.body += resp.Bytes

			window.record(d)

			if first {
				s.newConns++

				fresh.record(d)

				if resp.Resumed {
					s.newResumed++
				}
			}
		}
	}
}

// next moves s to its current identity as rot says.
func (s *sender) next(rot rotation, phase *atomic.Int32, built *lockedHist) error {
	if rot.renew == "" {
		if err := s.c.SetProxy(rot.proxyURL(s.identity)); err != nil {
			return fmt.Errorf("SetProxy: %w", err)
		}

		return nil
	}

	start := time.Now()

	c, err := rot.build(s.identity)
	if err != nil {
		return fmt.Errorf("new client: %w", err)
	}

	if phase.Load() == phaseMeasure {
		built.record(time.Since(start))
	}

	if rot.renew == RenewClosed {
		s.c.Close()
	}

	s.c = c

	return nil
}

// fail counts an error inside the window.
func (s *sender) fail(phase *atomic.Int32, msg string) {
	if phase.Load() != phaseMeasure {
		return
	}

	s.errors++
	if s.firstErr == "" {
		s.firstErr = msg
	}
}

// proxyCheck checks, against what the proxy relayed per user, that every
// sender went through it: each identity it got a response on opened a
// tunnel or an association, the proxy relayed it at least the body bytes
// it received, and over HTTP/3 it had an association that carried
// datagrams. It returns what failed, or "".
func proxyCheck(senders []*sender, users map[string]target.ProxyUse, user func(client, identity int) string, h3 bool) string {
	var unseen, short, noUDP, first int

	firstWhat := ""

	note := func(i int, what string) {
		if firstWhat == "" {
			first, firstWhat = i, what
		}
	}

	for i, s := range senders {
		var all target.ProxyUse

		for id := 0; id <= s.identity; id++ {
			all.Add(users[user(i, id)])
		}

		for _, id := range s.used {
			if u := users[user(i, id)]; u.Tunnels+u.Associations == 0 {
				unseen++

				note(i, fmt.Sprintf("identity %d got responses but the proxy never saw it", id))
			}
		}

		if all.BytesDown < s.bytes {
			short++

			note(i, fmt.Sprintf("received %d body bytes, the proxy relayed it %d", s.bytes, all.BytesDown))
		}

		if h3 && (all.Associations == 0 || all.Datagrams == 0) {
			noUDP++

			note(i, fmt.Sprintf("%d UDP associations, %d datagrams", all.Associations, all.Datagrams))
		}
	}

	if firstWhat == "" {
		return ""
	}

	return fmt.Sprintf("%d identities with responses unseen by the proxy, %d clients relayed fewer bytes than they received, "+
		"%d HTTP/3 clients without relayed datagrams (client %d: %s)", unseen, short, noUDP, first, firstWhat)
}

// multiDiscard returns why a multi-client point does not count. With
// client rotation (renew) up to 1% of the responses may come over another
// protocol, as up to 1% may fail: each new client of a racing client
// (mimicry with an Alt-Svc cache, tls-client) races QUIC against TCP, and
// one that loses goes on TCP.
func multiDiscard(r MultiResult, renew bool) string {
	mismatched := r.Mismatches > 0
	if renew {
		mismatched = float64(r.Mismatches) > 0.01*float64(r.Requests+r.Mismatches)
	}

	switch {
	case r.ProxyCheck != "":
		return "proxy check: " + r.ProxyCheck
	case mismatched:
		return fmt.Sprintf("%d requests on another protocol (%s)", r.Mismatches, r.FirstError)
	case r.Requests == 0:
		return "no request completed: " + r.FirstError
	case float64(r.Errors) > 0.01*float64(r.Requests+r.Errors):
		return fmt.Sprintf("%d errors (%s)", r.Errors, r.FirstError)
	case r.ServerCPU > 0.9:
		return fmt.Sprintf("server saturated (%.0f%% of its CPUs)", 100*r.ServerCPU)
	}

	return ""
}

// heapNow is the heap as runtime/metrics reads it.
type heapNow struct {
	// objects is the heap's objects, garbage not yet swept included; live
	// what the last GC marked live.
	objects uint64
	live    uint64
}

var heapSamples = []string{
	"/memory/classes/heap/objects:bytes",
	"/gc/heap/live:bytes",
}

func heapMetrics() heapNow {
	sample := make([]metrics.Sample, len(heapSamples))
	for i, name := range heapSamples {
		sample[i].Name = name
	}

	metrics.Read(sample)

	return heapNow{
		objects: sample[0].Value.Uint64(),
		live:    sample[1].Value.Uint64(),
	}
}

// sampleHeap samples the heap every 100 ms until the returned function is
// called, which returns the peak of its objects and the mean live heap.
func sampleHeap() func() (peak, live uint64) {
	stop := make(chan struct{})
	done := make(chan [2]uint64)

	go func() {
		var (
			peak       uint64
			sum, count uint64
		)

		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()

		for {
			h := heapMetrics()

			peak = max(peak, h.objects)

			sum += h.live
			count++

			select {
			case <-stop:
				done <- [2]uint64{peak, sum / count}

				return

			case <-t.C:
			}
		}
	}()

	return func() (uint64, uint64) {
		close(stop)

		v := <-done

		return v[0], v[1]
	}
}

// MultiPlateau is where a proxy-rotation point's memory levels off: over
// the last third of its per-second samples, the mean live heap (as the
// last GC marked it) beyond what the process held before the clients
// were built, and the mean goroutines beyond the senders, per client, and
// the tunnels and associations open at the proxy per client; and the
// least-squares slope of the live heap and the goroutines per client per
// second over the window's second half (near zero once level).
type MultiPlateau struct {
	LiveKBPerClient     float64 `json:"liveKbPerClient"`
	GoroutinesPerClient float64 `json:"goroutinesPerClient"`
	OpenPerClient       float64 `json:"openPerClient"`
	SlopeKBPerClient    float64 `json:"slopeKbPerClient"`
	SlopeGoroutines     float64 `json:"slopeGoroutines"`
	Samples             int     `json:"samples"`
}

// plateau summarises samples (MultiPlateau) for clients, against the live
// heap and goroutines the process had without them; nil with fewer than
// four samples.
func plateau(samples []MultiSample, baseLive uint64, baseGoroutines, clients int) *MultiPlateau {
	if len(samples) < 4 {
		return nil
	}

	n := float64(clients)
	last := samples[len(samples)-len(samples)/3:]

	p := &MultiPlateau{
		Samples: len(samples),
	}

	var open, opens float64

	for _, sm := range last {
		p.LiveKBPerClient += (sm.LiveMB*1e6 - float64(baseLive)) / 1024 / n
		p.GoroutinesPerClient += float64(sm.Goroutines-baseGoroutines) / n

		if sm.Open >= 0 {
			open += float64(sm.Open)
			opens++
		}
	}

	k := float64(len(last))

	p.LiveKBPerClient /= k
	p.GoroutinesPerClient /= k

	if opens > 0 {
		p.OpenPerClient = open / opens / n
	}

	if g := growth(samples[len(samples)/2:], clients); g != nil {
		p.SlopeKBPerClient = g.LiveKBPerRoutine
		p.SlopeGoroutines = g.GoroutinesPerRoutine
	}

	return p
}
