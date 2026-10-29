package scenario

import (
	"context"
	"fmt"
	"runtime"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/roachadam/mimicry/internal/bench/clients"
	"github.com/roachadam/mimicry/internal/bench/site"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// LoadSpec is one load point: a client sending op at a concurrency, after
// a warm-up, over a window, on one warm client. Both stretch for long
// requests: the warm-up until at least Concurrency requests have completed
// and it has lasted twice their median latency (up to MaxWarmup); the
// window until it has lasted Duration and five times the warm-up's median
// latency, and 3×Concurrency requests have started and finished inside it
// (up to MaxWindow). A 10 MB body at 256 at once takes about 4 s, so a
// fixed 3 s window held a round, a part of one, or two, depending on where
// it fell.
type LoadSpec struct {
	Op          Op
	Concurrency int
	Warmup      time.Duration
	Duration    time.Duration
	// MaxWarmup and MaxWindow cap the stretching; zero is 60 s and 120 s.
	MaxWarmup time.Duration
	MaxWindow time.Duration
	// OnWindow is called as the measurement window opens (true) and closes
	// (false), for profiling just the window.
	OnWindow func(open bool) `json:"-"`
}

// Window stretching (LoadSpec).
const (
	// completeRounds is how many requests per concurrent request must
	// start and finish inside the window.
	completeRounds = 3
	// windowLatencies and warmupLatencies are how many of the warm-up's
	// median latencies the window and the warm-up must last at least.
	windowLatencies = 5
	warmupLatencies = 2
	// inFlightGrace is how long requests in flight when the window closes
	// may run on; cancelGrace how long they get to return once cancelled.
	inFlightGrace = 60 * time.Second
	cancelGrace   = 10 * time.Second
	// serverBound is the CPU use, as a share of the server's CPUs or in
	// cores for one worker, from which a point is server-bound.
	serverBound = 0.9
)

// LoadResult is what a load point measured. Throughput is the response
// body bytes that arrived inside the window (MBps), and RPS the responses'
// worth of them per second; the per-request figures (CPU, allocations)
// divide by that worth. The samples (Requests, latencies, errors) are the
// requests in flight during the window: ended inside it or after it,
// wherever they started.
type LoadResult struct {
	Requests   int64   `json:"requests"`
	Errors     int64   `json:"errors"`
	FirstError string  `json:"firstError,omitempty"`
	Mismatches int64   `json:"mismatches"`
	Seconds    float64 `json:"seconds"`
	RPS        float64 `json:"rps"`
	// MBps is response body megabytes (10^6) per second.
	MBps float64 `json:"mbps"`
	// Latencies, in milliseconds.
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	// WarmupSeconds is how long the warm-up ran (at least the spec's), and
	// WarmupP50 its requests' median latency in ms. Started are the
	// requests started inside the window, Complete those that also
	// finished inside it; Capped says the window stopped at MaxWindow
	// (or the warm-up at MaxWarmup) before its rule was met.
	WarmupSeconds float64 `json:"warmupSeconds,omitempty"`
	WarmupP50     float64 `json:"warmupP50,omitempty"`
	Started       int64   `json:"started,omitempty"`
	Complete      int64   `json:"complete,omitempty"`
	Capped        string  `json:"capped,omitempty"`
	// ClientCores is the client process's CPU time over the window, in
	// cores; CPUPerRequest its CPU time per request, in microseconds.
	ClientCores   float64 `json:"clientCores"`
	CPUPerRequest float64 `json:"cpuPerRequest"`
	// Allocation per request inside the window.
	BytesPerRequest  float64 `json:"bytesPerRequest"`
	AllocsPerRequest float64 `json:"allocsPerRequest"`
	// GC over the window.
	GCs         uint32  `json:"gcs"`
	GCPauseMs   float64 `json:"gcPauseMs"`
	GCPauseMaxM float64 `json:"gcPauseMaxMs"`
	// HeapPeakMB is the largest live-and-unswept heap seen (sampled every
	// 100 ms); Goroutines the count at the window's end.
	HeapPeakMB float64 `json:"heapPeakMb"`
	Goroutines int     `json:"goroutines"`
	// ServerCPU is the server's CPU use over the window, as a share of its
	// CPUs; ServerWorker its busiest worker process's, in cores (nginx);
	// -1 when unknown.
	ServerCPU    float64 `json:"serverCpu"`
	ServerWorker float64 `json:"serverWorker"`
	// ServerBound says why the server, not the client, was the limit
	// (its CPUs, or one worker's core), or is empty. The point still
	// counts in the tables; the report leaves it out of the summary for
	// every client.
	ServerBound string `json:"serverBound,omitempty"`
	// Discarded says why the point does not count, or is empty.
	Discarded string `json:"discarded,omitempty"`
}

// Phases of a load point.
const (
	phaseWarm int32 = iota
	phaseMeasure
	phaseStop
)

// Load runs a load point with su's client. It warms the client up onto the
// protocol first (Warm), then runs the warm-up at full concurrency, then
// measures over the window (LoadSpec).
func Load(ctx context.Context, su Setup, spec LoadSpec) (LoadResult, error) {
	var res LoadResult

	if spec.MaxWarmup <= 0 {
		spec.MaxWarmup = 60 * time.Second
	}

	if spec.MaxWindow <= 0 {
		spec.MaxWindow = 120 * time.Second
	}

	c, err := su.New()
	if err != nil {
		return res, err
	}

	// Not closed when requests are stuck in it (below): Close could wait
	// on them too, and the worker exits anyway.
	stuck := false
	defer func() {
		if !stuck {
			c.Close()
		}
	}()

	// Warm onto the protocol with 1 KB bodies: three of the op's own could
	// take longer than Warm allows (10 MB under netem's loss).
	req := spec.Op.Request(su.Server, su.Proto)
	if err := Warm(ctx, c, GetBytes(site.KB).Request(su.Server, su.Proto), su.Proto); err != nil {
		res.Discarded = err.Error()

		return res, nil
	}

	var (
		phase atomic.Int32
		// openAt is when the window opened (UnixNano; 0 before).
		openAt atomic.Int64
		wg     sync.WaitGroup
		total  hist
		warm   lockedHist
	)

	type worker struct {
		h          hist
		requests   int64
		bytes      int64
		errors     int64
		mismatches int64
		started    int64
		firstErr   string
		// arrived counts body bytes as they are read, in every phase;
		// complete the requests that started and finished in the window.
		arrived  clients.Arrivals
		complete atomic.Int64
	}

	workers := make([]*worker, spec.Concurrency)
	for i := range workers {
		workers[i] = new(worker)
	}

	// arrived is what every worker has read so far.
	arrived := func() int64 {
		var n int64
		for _, w := range workers {
			n += w.arrived.Load()
		}

		return n
	}

	complete := func() int64 {
		var n int64
		for _, w := range workers {
			n += w.complete.Load()
		}

		return n
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, w := range workers {
		wreq := *req

		wreq.Arrivals = &w.arrived

		wg.Go(func() {
			for phase.Load() != phaseStop && runCtx.Err() == nil {
				startPhase := phase.Load()
				start := time.Now()
				resp, err := c.Do(runCtx, &wreq)
				d := time.Since(start)

				// A request is a sample (latency, errors, protocol) when
				// it was in flight during the window: it ended inside it
				// or after it (the drain below waits for it), wherever it
				// started. The window's throughput is the bytes that
				// arrived inside it (below).
				now := phase.Load()
				if now == phaseWarm {
					if err == nil {
						warm.record(d)
					}

					continue
				}

				if startPhase == phaseMeasure {
					w.started++

					if now == phaseMeasure && err == nil && start.UnixNano() >= openAt.Load() {
						w.complete.Add(1)
					}
				}

				switch {
				case err != nil:
					w.errors++
					if w.firstErr == "" {
						w.firstErr = err.Error()
					}

				case resp.Status != 200:
					w.errors++
					if w.firstErr == "" {
						w.firstErr = fmt.Sprintf("status %d", resp.Status)
					}

				case resp.Proto != su.Proto:
					w.mismatches++
					if w.firstErr == "" {
						w.firstErr = clients.Check(resp, su.Proto).Error()
					}

				default:
					w.requests++

					w.bytes += resp.Bytes
					w.h.record(d)
				}
			}
		})
	}

	// The warm-up: at least spec.Warmup, Concurrency requests completed,
	// and twice their median latency.
	warmStart := time.Now()

	sleep(ctx, spec.Warmup)

	var warmP50 time.Duration

	for ctx.Err() == nil {
		warm.mu.Lock()

		n, p50 := warm.h.n, warm.h.quantile(0.5)

		warm.mu.Unlock()

		warmP50 = p50

		elapsed := time.Since(warmStart)

		if n >= uint64(spec.Concurrency) && elapsed >= warmupLatencies*p50 {
			break
		}

		if elapsed >= spec.MaxWarmup {
			res.Capped = fmt.Sprintf("warm-up stopped at %v: %d requests done, median %v", spec.MaxWarmup, n, p50.Round(time.Millisecond))

			break
		}

		sleep(ctx, 50*time.Millisecond)
	}

	res.WarmupSeconds = time.Since(warmStart).Seconds()
	res.WarmupP50 = ms(warmP50)

	peak := samplePeak()
	before := snapshot(ctx, su.Server)
	arrivedBefore := arrived()

	if spec.OnWindow != nil {
		spec.OnWindow(true)
	}

	openAt.Store(time.Now().UnixNano())
	phase.Store(phaseMeasure)

	// The window: at least spec.Duration and five warm-up medians, and
	// 3×Concurrency requests started and finished inside it.
	minWindow := max(spec.Duration, windowLatencies*warmP50)
	need := int64(completeRounds * spec.Concurrency)
	opened := time.Now()

	sleep(ctx, minWindow)

	for ctx.Err() == nil {
		if complete() >= need {
			break
		}

		if time.Since(opened) >= spec.MaxWindow {
			if res.Capped != "" {
				res.Capped += "; "
			}

			res.Capped += fmt.Sprintf("window stopped at %v with %d of %d requests started and finished in it",
				spec.MaxWindow, complete(), need)

			break
		}

		sleep(ctx, 50*time.Millisecond)
	}

	after := snapshot(ctx, su.Server)
	arrivedAfter := arrived()

	res.Complete = complete()

	phase.Store(phaseStop)

	if spec.OnWindow != nil {
		spec.OnWindow(false)
	}

	res.HeapPeakMB = float64(peak()) / 1e6

	// In-flight requests may take long (a 10 MB body at high concurrency);
	// give them a while, then cancel. A client that ignores the
	// cancellation (azuretls over HTTP/2 after a connection error) gets
	// a few seconds more, then the point is an error rather than a
	// worker blocked for good.
	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(inFlightGrace):
		cancel()

		select {
		case <-done:
		case <-time.After(cancelGrace):
			stuck = true

			return res, fmt.Errorf("requests still in flight %v after the window closed, %v after their context was cancelled: "+
				"the client did not return", inFlightGrace+cancelGrace, cancelGrace)
		}
	}

	var bytes int64

	for _, w := range workers {
		total.merge(&w.h)

		res.Requests += w.requests
		res.Errors += w.errors
		res.Mismatches += w.mismatches
		res.Started += w.started
		bytes += w.bytes

		if res.FirstError == "" {
			res.FirstError = w.firstErr
		}
	}

	window := after.at.Sub(before.at)
	inWindow := float64(arrivedAfter - arrivedBefore)

	res.Seconds = window.Seconds()
	res.MBps = inWindow / 1e6 / res.Seconds

	// Requests per second are the responses' worth of body bytes that
	// arrived inside the window, at the size the measured responses had
	// (every response of a point has the same body).
	res.RPS = float64(res.Requests) / res.Seconds
	if res.Requests > 0 && bytes > 0 {
		res.RPS = inWindow / (float64(bytes) / float64(res.Requests)) / res.Seconds
	}

	res.P50 = ms(total.quantile(0.50))
	res.P95 = ms(total.quantile(0.95))
	res.P99 = ms(total.quantile(0.99))

	cpu := after.cpu - before.cpu

	res.ClientCores = cpu.Seconds() / res.Seconds

	if n := res.RPS * res.Seconds; n > 0 {
		res.CPUPerRequest = float64(cpu.Microseconds()) / n
		res.BytesPerRequest = float64(after.mem.TotalAlloc-before.mem.TotalAlloc) / n
		res.AllocsPerRequest = float64(after.mem.Mallocs-before.mem.Mallocs) / n
	}

	res.GCs = after.mem.NumGC - before.mem.NumGC
	res.GCPauseMs = float64(after.mem.PauseTotalNs-before.mem.PauseTotalNs) / 1e6
	res.GCPauseMaxM = maxPause(&after.mem, before.mem.NumGC) / 1e6
	res.Goroutines = after.goroutines - spec.Concurrency

	res.ServerCPU, res.ServerWorker = -1, -1
	if before.serverErr == nil && after.serverErr == nil {
		res.ServerCPU = target.Utilisation(before.server, after.server, window)
		res.ServerWorker = target.BusiestWorker(before.server, after.server, window)
	}

	res.ServerBound = boundBy(res.ServerCPU, res.ServerWorker)
	res.Discarded = discard(res)

	return res, nil
}

// boundBy says why a server at cpu (share of its CPUs) with its busiest
// worker at worker (cores) was the limit, or "".
func boundBy(cpu, worker float64) string {
	switch {
	case cpu >= serverBound:
		return fmt.Sprintf("server at %.0f%% of its CPUs", 100*cpu)
	case worker >= serverBound:
		return fmt.Sprintf("one server worker at %.0f%% of a core", 100*worker)
	}

	return ""
}

// discard returns why a load point does not count.
func discard(r LoadResult) string {
	switch {
	case r.Mismatches > 0:
		return fmt.Sprintf("%d requests on another protocol (%s)", r.Mismatches, r.FirstError)
	case r.Requests == 0:
		return "no request completed: " + r.FirstError
	case float64(r.Errors) > 0.01*float64(r.Requests+r.Errors):
		return fmt.Sprintf("%d errors (%s)", r.Errors, r.FirstError)
	}

	return ""
}

type snap struct {
	at         time.Time
	cpu        time.Duration
	mem        runtime.MemStats
	goroutines int
	server     target.Stats
	serverErr  error
}

// snapshot reads the server's stats first (on its own connection, outside
// the window), then the process's.
func snapshot(ctx context.Context, s target.Server) *snap {
	sn := new(snap)

	sn.server, sn.serverErr = s.Stats(ctx)
	runtime.ReadMemStats(&sn.mem)

	sn.cpu = processCPU()
	sn.goroutines = runtime.NumGoroutine()
	sn.at = time.Now()

	return sn
}

// processCPU returns the process's user and system CPU time.
func processCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}

	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// maxPause returns the longest GC pause since GC number since, in ns.
func maxPause(m *runtime.MemStats, since uint32) float64 {
	ring := uint32(len(m.PauseNs))

	first := since + 1
	if m.NumGC > ring {
		first = max(first, m.NumGC-ring+1)
	}

	var longest uint64
	for n := first; n <= m.NumGC; n++ {
		longest = max(longest, m.PauseNs[(n+ring-1)%ring])
	}

	return float64(longest)
}

// samplePeak samples the heap every 100 ms until the returned function is
// called, which returns the peak.
func samplePeak() func() uint64 {
	stop := make(chan struct{})
	done := make(chan uint64)

	go func() {
		sample := []metrics.Sample{
			{
				Name: "/memory/classes/heap/objects:bytes",
			},
		}

		var peak uint64

		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()

		for {
			metrics.Read(sample)

			peak = max(peak, sample[0].Value.Uint64())

			select {
			case <-stop:
				done <- peak

				return

			case <-t.C:
			}
		}
	}()

	return func() uint64 {
		close(stop)

		return <-done
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func ms(d time.Duration) float64 {
	return float64(d) / 1e6
}
