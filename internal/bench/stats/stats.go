// Package stats serves a container's /_stats: its CPU time from cgroup v2
// and how many CPUs it may use, so the runner can tell whether a server (or
// the proxy) was the bottleneck during a measurement.
package stats

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// Counters are connection counts a server keeps for /_stats.
type Counters struct {
	TCP  atomic.Int64
	QUIC atomic.Int64
}

// Handler serves /_stats with c's counts (c may be nil).
func Handler(c *Counters) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_stats", func(w http.ResponseWriter, _ *http.Request) {
		st := target.Stats{
			CPUMicros: cpuMicros(),
			CPUs:      cpus(),
		}
		if c != nil {
			st.TCPConns = c.TCP.Load()
			st.QUICConns = c.QUIC.Load()
		}

		w.Header().Set("Content-Type", "application/json")

		_ = json.NewEncoder(w).Encode(st)
	})

	return mux
}

// cpuMicros returns the cgroup's usage_usec, or -1 without cgroup v2.
func cpuMicros() int64 {
	b, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return -1
	}

	for line := range strings.Lines(string(b)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "usage_usec "); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return -1
			}

			return n
		}
	}

	return -1
}

// cpus returns how many CPUs the process may run on (its cpuset).
func cpus() int {
	return runtime.NumCPU()
}
