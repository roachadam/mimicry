package scenario

import (
	"context"
	"runtime"
	"time"

	"github.com/roachadam/mimicry/internal/bench/clients"
)

// IdleResult is the memory a client holds per idle connection: N clients
// built (heap and goroutines per client), then each warmed onto the
// protocol and left idle (the growth per client is per idle connection;
// for mimicry over HTTP/3 that is the QUIC connection plus the HTTP/2 one
// it discovered Alt-Svc on, which Chrome keeps open too).
type IdleResult struct {
	N int `json:"n"`
	// Per client, before any request.
	HeapPerClientKB       float64 `json:"heapPerClientKb"`
	GoroutinesPerClient   float64 `json:"goroutinesPerClient"`
	HeapPerConnKB         float64 `json:"heapPerConnKb"`
	GoroutinesPerConn     float64 `json:"goroutinesPerConn"`
	HeapAfterCloseKB      float64 `json:"heapAfterCloseKb"`
	GoroutinesAfterClose  int     `json:"goroutinesAfterClose"`
	Discarded             string  `json:"discarded,omitempty"`
	GoroutinesBeforeStart int     `json:"goroutinesBeforeStart"`
}

// Idle measures su's client's memory per idle connection with n clients.
func Idle(ctx context.Context, su Setup, n int) (IdleResult, error) {
	res := IdleResult{
		N: n,
	}

	heap0, g0 := settled(0)

	res.GoroutinesBeforeStart = g0

	cs := make([]clients.Client, 0, n)

	for range n {
		c, err := su.New()
		if err != nil {
			return res, err
		}

		cs = append(cs, c)
	}

	heap1, g1 := settled(0)

	req := GetJSON.Request(su.Server, su.Proto)
	for _, c := range cs {
		if err := Warm(ctx, c, req, su.Proto); err != nil {
			res.Discarded = err.Error()

			break
		}
	}

	heap2, g2 := settled(2 * time.Second)

	for _, c := range cs {
		c.Close()
	}

	// The clients are closed and dropped: what remains is what Close left
	// behind (goroutines and memory still referenced from them).
	clear(cs)

	heap3, g3 := settled(2 * time.Second)

	res.HeapPerClientKB = float64(int64(heap1)-int64(heap0)) / 1024 / float64(n)
	res.GoroutinesPerClient = float64(g1-g0) / float64(n)
	res.HeapPerConnKB = float64(int64(heap2)-int64(heap1)) / 1024 / float64(n)
	res.GoroutinesPerConn = float64(g2-g1) / float64(n)
	res.HeapAfterCloseKB = float64(int64(heap3)-int64(heap0)) / 1024
	res.GoroutinesAfterClose = g3 - g0

	return res, nil
}

// settled waits d, collects garbage twice and returns the live heap and the
// goroutine count.
func settled(d time.Duration) (uint64, int) {
	time.Sleep(d)
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return m.HeapAlloc, runtime.NumGoroutine()
}
