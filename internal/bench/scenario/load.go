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

// LoadResult is what a load point measured. Throughput is the response
// body bytes that arrived inside the window (MBps), and RPS the responses'
// worth of them per second; the per-request figures divide by that worth.
// The samples (Requests, latencies, errors) are the requests in flight
// during the window: ended inside it or after it, wherever they started.
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

	// Warm onto the protocol with 1 KB bodies: three of the op's own could
	// take longer than Warm allows (10 MB under netem's loss).
	req := spec.Op.Request(su.Server, su.Proto)
	if err := Warm(ctx, c, GetBytes(site.KB).Request(su.Server, su.Proto), su.Proto); err != nil {
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
		// arrived counts body bytes as they are read, in every phase.
		arrived clients.Arrivals
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

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, w := range workers {
		wreq := *req

		wreq.Arrivals = &w.arrived

		wg.Go(func() {
			for phase.Load() != phaseStop && runCtx.Err() == nil {
				start := time.Now()
				resp, err := c.Do(runCtx, &wreq)
				d := time.Since(start)

				// A request is a sample (latency, errors, protocol) when
				// it was in flight during the window: it ended inside it
				// or after it (the drain below waits for it), wherever it
				// started. The window's throughput is the bytes that
				// arrived inside it (below). Counting only the requests
				// that ended inside it depended on where the window fell:
				// a server that shares its bandwidth evenly ends all 256
				// 10 MB requests together, about every 4 s, so a 3 s
				// window caught a whole round or a few stragglers.
				if phase.Load() == phaseWarm {
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
	arrivedBefore := arrived()

	if spec.OnWindow != nil {
		spec.OnWindow(true)
	}

	phase.Store(phaseMeasure)
	sleep(ctx, spec.Duration)

	after := snapshot(ctx, su.Server)
	arrivedAfter := arrived()

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
