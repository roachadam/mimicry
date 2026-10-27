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
	"github.com/roachadam/mimicry/internal/bench/target"
)

// LoadSpec is one load point: a client sending op at a concurrency for a
// fixed duration after a warm-up, on one warm client.
type LoadSpec struct {
	Op          Op
	Concurrency int
	Warmup      time.Duration
	Duration    time.Duration
	// OnWindow is called as the measurement window opens (true) and closes
	// (false), for profiling just the window.
	OnWindow func(open bool) `json:"-"`
}

// LoadResult is what a load point measured: the requests that completed
// inside the window.
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
	// CPUs; -1 when unknown.
	ServerCPU float64 `json:"serverCpu"`
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
// protocol first (Warm), then runs Warmup at full concurrency, then
// measures for Duration.
func Load(ctx context.Context, su Setup, spec LoadSpec) (LoadResult, error) {
	var res LoadResult

	c, err := su.New()
	if err != nil {
		return res, err
	}
	defer c.Close()

	req := spec.Op.Request(su.Server, su.Proto)
	if err := Warm(ctx, c, req, su.Proto); err != nil {
		res.Discarded = err.Error()

		return res, nil
	}

	var (
		phase atomic.Int32
		wg    sync.WaitGroup
		total hist
	)

	type worker struct {
		h          hist
		requests   int64
		bytes      int64
		errors     int64
		mismatches int64
		firstErr   string
	}

	workers := make([]*worker, spec.Concurrency)
	for i := range workers {
		workers[i] = new(worker)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, w := range workers {
		wg.Go(func() {
			for phase.Load() != phaseStop && runCtx.Err() == nil {
				start := time.Now()
				resp, err := c.Do(runCtx, req)
				d := time.Since(start)

				// A request counts when it completes inside the window,
				// wherever it started: the warm-up runs at the same
				// concurrency, and a 10 MB body at 256 concurrent
				// requests can take longer than the window.
				if phase.Load() != phaseMeasure {
					continue
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

	sleep(ctx, spec.Warmup)

	peak := samplePeak()
	before := snapshot(ctx, su.Server)

	if spec.OnWindow != nil {
		spec.OnWindow(true)
	}

	phase.Store(phaseMeasure)
	sleep(ctx, spec.Duration)

	after := snapshot(ctx, su.Server)

	phase.Store(phaseStop)

	if spec.OnWindow != nil {
		spec.OnWindow(false)
	}

	res.HeapPeakMB = float64(peak()) / 1e6

	// In-flight requests may take long (a 10 MB body at high concurrency);
	// give them a while, then cancel.
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

	for _, w := range workers {
		total.merge(&w.h)

		res.Requests += w.requests
		res.Errors += w.errors
		res.Mismatches += w.mismatches
		bytes += w.bytes

		if res.FirstError == "" {
			res.FirstError = w.firstErr
		}
	}

	window := after.at.Sub(before.at)

	res.Seconds = window.Seconds()
	res.RPS = float64(res.Requests) / res.Seconds
	res.MBps = float64(bytes) / 1e6 / res.Seconds
	res.P50 = ms(total.quantile(0.50))
	res.P95 = ms(total.quantile(0.95))
	res.P99 = ms(total.quantile(0.99))

	cpu := after.cpu - before.cpu

	res.ClientCores = cpu.Seconds() / res.Seconds

	if res.Requests > 0 {
		n := float64(res.Requests)

		res.CPUPerRequest = float64(cpu.Microseconds()) / n
		res.BytesPerRequest = float64(after.mem.TotalAlloc-before.mem.TotalAlloc) / n
		res.AllocsPerRequest = float64(after.mem.Mallocs-before.mem.Mallocs) / n
	}

	res.GCs = after.mem.NumGC - before.mem.NumGC
	res.GCPauseMs = float64(after.mem.PauseTotalNs-before.mem.PauseTotalNs) / 1e6
	res.GCPauseMaxM = maxPause(&after.mem, before.mem.NumGC) / 1e6
	res.Goroutines = after.goroutines - spec.Concurrency

	res.ServerCPU = -1
	if before.serverErr == nil && after.serverErr == nil {
		res.ServerCPU = target.Utilisation(before.server, after.server, window)
	}

	res.Discarded = discard(res)

	return res, nil
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
	case r.ServerCPU > 0.9:
		return fmt.Sprintf("server saturated (%.0f%% of its CPUs)", 100*r.ServerCPU)
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
