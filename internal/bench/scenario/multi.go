package scenario

import (
	"context"
	"fmt"
	"runtime/metrics"
	"strconv"
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
	// Tag makes the run's proxy users unique: <target.ProxyUser>-<Tag>-
	// <client>-<identity>.
	Tag string
	// OnWindow is called as the measurement window opens (true) and closes
	// (false), for profiling just the window.
	OnWindow func(open bool) `json:"-"`
}

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
	// ProxyCheck is what the proxy-side check found wrong, or empty: every
	// identity a client got a response on was seen by the proxy, the proxy
	// relayed each client at least the body bytes it received, and over
	// HTTP/3 each client had a UDP association that carried datagrams.
	ProxyCheck string `json:"proxyCheck,omitempty"`
	// Discarded says why the point does not count, or is empty.
	Discarded string `json:"discarded,omitempty"`
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
// it.
func Multi(ctx context.Context, su Setup, spec MultiSpec) (MultiResult, error) {
	res := MultiResult{
		Clients: spec.Clients,
	}

	if target.ProxyUserURL(su.Proxy, "x") == "" {
		return res, fmt.Errorf("scenario: proxy kind %q takes no credentials", su.Proxy)
	}

	roots, err := target.RootCAs()
	if err != nil {
		return res, err
	}

	prefix := target.ProxyUser + "-" + spec.Tag + "-"
	user := func(client, identity int) string {
		return prefix + strconv.Itoa(client) + "-" + strconv.Itoa(identity)
	}

	req := spec.Op.Request(su.Server, su.Proto)
	window, fresh := new(lockedHist), new(lockedHist)

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
		o := su.options(roots)

		o.Proxy = target.ProxyUserURL(su.Proxy, user(i, 0))

		if s.c, err = su.Client.New(o); err != nil {
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
		phase atomic.Int32
		wg    sync.WaitGroup
	)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for i, s := range senders {
		wg.Go(func() {
			s.run(runCtx, &phase, req, su, spec.Rotate, func(identity int) string {
				return target.ProxyUserURL(su.Proxy, user(i, identity))
			}, window, fresh)
		})
	}

	sleep(ctx, spec.Warmup)

	heap := sampleHeap()
	useBefore, useBeforeErr := target.FetchProxyUsage(ctx, prefix, false, false)
	proxyBefore, proxyBeforeErr := target.FetchStats(ctx, target.ProxyHost, target.ProxyStatsPort)
	before := snapshot(ctx, su.Server)

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

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		cancel()
		<-done
	}

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

	usage, err := target.FetchProxyUsage(ctx, prefix, true, true)
	if err != nil {
		res.ProxyCheck = "reading the proxy's usage: " + err.Error()
	} else {
		res.ProxyCheck = proxyCheck(senders, usage.Users, user, su.Proto == target.H3)
	}

	res.Discarded = multiDiscard(res)

	return res, nil
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

// run sends req one at a time until phase says stop, moving to a new
// identity (proxyURL) every rotate requests when rotate is set. Latencies
// of requests completing in the window go to window, and those of each
// identity's first request (from the switch on) to fresh as well.
func (s *sender) run(ctx context.Context, phase *atomic.Int32, req *clients.Request, su Setup, rotate int,
	proxyURL func(identity int) string, window, fresh *lockedHist,
) {
	for phase.Load() != phaseStop && ctx.Err() == nil {
		start := time.Now()
		first := false

		if rotate > 0 && s.sent >= rotate {
			s.identity++

			s.sent = 0
			first = true

			if err := s.c.SetProxy(proxyURL(s.identity)); err != nil {
				s.fail(phase, "SetProxy: "+err.Error())

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

// multiDiscard returns why a multi-client point does not count.
func multiDiscard(r MultiResult) string {
	switch {
	case r.ProxyCheck != "":
		return "proxy check: " + r.ProxyCheck
	case r.Mismatches > 0:
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
