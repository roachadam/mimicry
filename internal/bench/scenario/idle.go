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
// it discovered Alt-Svc on, which Chrome keeps open too). Stack is the
// goroutines' stacks in use (runtime.MemStats.StackInuse), which the heap
// leaves out.
type IdleResult struct {
	N int `json:"n"`
	// Per client, before any request.
	HeapPerClientKB       float64 `json:"heapPerClientKb"`
	GoroutinesPerClient   float64 `json:"goroutinesPerClient"`
	HeapPerConnKB         float64 `json:"heapPerConnKb"`
	GoroutinesPerConn     float64 `json:"goroutinesPerConn"`
	StackPerConnKB        float64 `json:"stackPerConnKb"`
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

	m0 := settled(0)

	res.GoroutinesBeforeStart = m0.goroutines

	cs := make([]clients.Client, 0, n)

	for range n {
		c, err := su.New()
		if err != nil {
			return res, err
		}

		cs = append(cs, c)
	}

	m1 := settled(0)

	req := GetJSON.Request(su.Server, su.Proto)
	for _, c := range cs {
		if err := Warm(ctx, c, req, su.Proto); err != nil {
			res.Discarded = err.Error()

			break
		}
	}

	m2 := settled(2 * time.Second)

	for _, c := range cs {
		c.Close()
	}

	// The clients are closed and dropped: what remains is what Close left
	// behind (goroutines and memory still referenced from them).
	clear(cs)

	m3 := settled(2 * time.Second)

	res.HeapPerClientKB = kbPer(m1.heap, m0.heap, n)
	res.GoroutinesPerClient = float64(m1.goroutines-m0.goroutines) / float64(n)
	res.HeapPerConnKB = kbPer(m2.heap, m1.heap, n)
	res.GoroutinesPerConn = float64(m2.goroutines-m1.goroutines) / float64(n)
	res.StackPerConnKB = kbPer(m2.stack, m1.stack, n)
	res.HeapAfterCloseKB = kbPer(m3.heap, m0.heap, 1)
	res.GoroutinesAfterClose = m3.goroutines - m0.goroutines

	return res, nil
}

// memory is what settled reads.
type memory struct {
	heap       uint64
	stack      uint64
	goroutines int
}

// settled waits d, collects garbage twice and returns the live heap, the
// stacks in use and the goroutine count.
func settled(d time.Duration) memory {
	time.Sleep(d)
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return memory{
		heap:       m.HeapAlloc,
		stack:      m.StackInuse,
		goroutines: runtime.NumGoroutine(),
	}
}

// kbPer is the growth from before to after in KB, divided by n.
func kbPer(after, before uint64, n int) float64 {
	return float64(int64(after)-int64(before)) / 1024 / float64(n)
}
