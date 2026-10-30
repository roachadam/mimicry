// Layers 1 and 2 of the benchmark, as Go benchmarks: the cost of one request
// on a warm connection (BenchmarkRequest), of one request on a new
// connection (BenchmarkConnect) and of building a client (BenchmarkNewClient),
// for every client, protocol and server. They need the server stack and run
// in the client container (README.md): moon run bench:micro runs BENCH_COUNT
// (6) passes of -count 1 and summarises them with benchstat. Names are key=value so benchstat can pivot: -col /client.
//
// A sub-benchmark the client cannot run is skipped with "n/a: why"; one
// whose requests did not all go over the protocol under test (or, for
// handshake=resumed, did not all resume) is skipped with "discarded: why"
// and prints no result.
package bench_test

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry/internal/bench/clients"
	"github.com/roachadam/mimicry/internal/bench/scenario"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// servers are the servers to run against: BENCH_SERVERS (comma-separated
// names), else both.
func servers(b *testing.B) []target.Server {
	b.Helper()

	names := os.Getenv("BENCH_SERVERS")
	if names == "" {
		return target.Servers
	}

	var out []target.Server

	for name := range strings.SplitSeq(names, ",") {
		s, err := target.ServerNamed(strings.TrimSpace(name))
		if err != nil {
			b.Fatal(err)
		}

		out = append(out, s)
	}

	return out
}

// benchName is a client's name without slashes, which would split the
// benchmark's name (net/http is "nethttp").
func benchName(f clients.Factory) string {
	return strings.ReplaceAll(f.Name, "/", "")
}

// benchClients are the clients in the order BENCH_CLIENTS names them
// (bench.sh rotates it between its passes, so no client always runs
// first or last), else clients.All's.
func benchClients(b *testing.B) []clients.Factory {
	b.Helper()

	list := os.Getenv("BENCH_CLIENTS")
	if list == "" {
		return clients.All()
	}

	var out []clients.Factory

	for name := range strings.SplitSeq(list, ",") {
		f, err := clients.Named(strings.TrimSpace(name))
		if err != nil {
			b.Fatal(err)
		}

		out = append(out, f)
	}

	return out
}

// BenchmarkNewClient is the constructor alone, in a tight loop: the
// library's ordinary constructor with the protocol's settings, then its
// Close, no request (layer 6's "new client alone" measures one at a time
// between round trips, on a CPU that idles between them). proxy=socks5
// builds it with SOCKS5 credentials, as layer 6's clients are.
func BenchmarkNewClient(b *testing.B) {
	roots, err := target.RootCAs()
	if err != nil {
		b.Fatal(err)
	}

	for _, p := range target.Protos {
		for _, proxy := range []target.ProxyKind{"", target.ProxySOCKS5} {
			for _, f := range benchClients(b) {
				via := "none"
				if proxy != "" {
					via = string(proxy)
				}

				name := "proto=" + string(p) + "/proxy=" + via + "/client=" + benchName(f)
				b.Run(name, func(b *testing.B) {
					if err := f.Supports(p, proxy); err != nil {
						b.Skip("n/a: ", err)
					}

					o := clients.Options{
						Proto:   p,
						RootCAs: roots,
					}
					if proxy != "" {
						o.Proxy = target.ProxyUserURL(proxy, target.ProxyUser+"-newclient")
					}

					b.ReportAllocs()

					for b.Loop() {
						c, err := f.New(o)
						if err != nil {
							b.Fatal(err)
						}

						c.Close()
					}
				})
			}
		}
	}
}

// BenchmarkRequest is layer 1: GET /json and POST /echo (small JSON) on a
// warm connection, one request at a time.
func BenchmarkRequest(b *testing.B) {
	for _, srv := range servers(b) {
		for _, p := range target.Protos {
			for _, op := range []scenario.Op{scenario.GetJSON, scenario.PostJSON} {
				for _, f := range benchClients(b) {
					name := "server=" + srv.Name + "/proto=" + string(p) + "/method=" + op.Name + "/client=" + benchName(f)
					b.Run(name, func(b *testing.B) {
						benchRequest(b, scenario.Setup{
							Client: f,
							Server: srv,
							Proto:  p,
						}, op)
					})
				}
			}
		}
	}
}

func benchRequest(b *testing.B, su scenario.Setup, op scenario.Op) {
	b.Helper()

	if err := su.Supported(); err != nil {
		b.Skip("n/a: ", err)
	}

	c, err := su.New()
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()

	req := op.Request(su.Server, su.Proto)
	if err := scenario.Warm(b.Context(), c, req, su.Proto); err != nil {
		b.Skip("discarded: ", err)
	}

	var (
		wrong int
		check error
	)

	b.ReportAllocs()

	for b.Loop() {
		resp, err := c.Do(b.Context(), req)
		if err != nil {
			b.Fatal(err)
		}

		if resp.Proto != su.Proto {
			wrong++

			check = clients.Check(resp, su.Proto)
		}
	}

	if wrong > 0 {
		b.Skipf("discarded: %d of %d requests: %v", wrong, b.N, check)
	}
}

// BenchmarkConnect is layer 2: one GET /json on a new connection per
// iteration. handshake=full builds a new client each time (outside the
// timer), so nothing is resumed; handshake=resumed closes the client's
// connections between iterations (outside the timer) and keeps its TLS
// session cache, for clients that resume. Each connection is given
// settleWait (untimed) before it is closed.
func BenchmarkConnect(b *testing.B) {
	for _, srv := range servers(b) {
		for _, p := range target.Protos {
			for _, hs := range []string{"full", "resumed"} {
				for _, f := range benchClients(b) {
					name := "server=" + srv.Name + "/proto=" + string(p) + "/handshake=" + hs + "/client=" + benchName(f)
					b.Run(name, func(b *testing.B) {
						su := scenario.Setup{
							Client: f,
							Server: srv,
							Proto:  p,
						}
						if hs == "full" {
							benchFull(b, su)
						} else {
							benchResumed(b, su)
						}
					})
				}
			}
		}
	}
}

func benchFull(b *testing.B, su scenario.Setup) {
	b.Helper()

	if err := su.Supported(); err != nil {
		b.Skip("n/a: ", err)
	}

	if su.Client.Name == "mimicry" && su.Proto == target.H3 {
		b.Skip("n/a: a new mimicry Transport starts on TCP and moves to HTTP/3 only after Alt-Svc, as Chrome does; " +
			"nothing starts one on HTTP/3")
	}

	req := scenario.GetJSON.Request(su.Server, su.Proto)

	var (
		wrong   int
		resumed int
		check   error
	)

	b.ReportAllocs()

	for b.Loop() {
		b.StopTimer()

		c, err := su.New()
		if err != nil {
			b.Fatal(err)
		}

		b.StartTimer()

		resp, err := c.Do(b.Context(), req)

		b.StopTimer()
		spin(settleWait)
		c.Close()
		b.StartTimer()

		if err != nil {
			b.Fatal(err)
		}

		if resp.Proto != su.Proto {
			wrong++

			check = clients.Check(resp, su.Proto)
		}

		if resp.Resumed {
			resumed++
		}
	}

	switch {
	case wrong > 0:
		b.Skipf("discarded: %d of %d requests: %v", wrong, b.N, check)
	case resumed > 0:
		b.Skipf("discarded: %d of %d new clients resumed a TLS session", resumed, b.N)
	}
}

func benchResumed(b *testing.B, su scenario.Setup) {
	b.Helper()

	if err := su.Supported(); err != nil {
		b.Skip("n/a: ", err)
	}

	c, err := su.New()
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()

	req := scenario.GetJSON.Request(su.Server, su.Proto)
	if err := scenario.Warm(b.Context(), c, req, su.Proto); err != nil {
		b.Skip("discarded: ", err)
	}

	var (
		wrong   int
		resumed int
		check   error
	)

	b.ReportAllocs()

	for b.Loop() {
		b.StopTimer()
		spin(settleWait)
		c.CloseIdle()
		b.StartTimer()

		resp, err := c.Do(b.Context(), req)
		if err != nil {
			b.Fatal(err)
		}

		if resp.Proto != su.Proto {
			wrong++

			check = clients.Check(resp, su.Proto)
		}

		if resp.Resumed {
			resumed++
		}
	}

	misses := b.N - resumed

	switch {
	case wrong > 0:
		b.Skipf("discarded: %d of %d requests: %v", wrong, b.N, check)
	case resumed == 0:
		b.Skip("n/a: the client does not resume TLS sessions here (no request was resumed)")
	case misses > maxMisses(b.N):
		b.Skipf("discarded: only %d of %d requests resumed (up to %d may not)", resumed, b.N, maxMisses(b.N))
	}

	// The misses are timed with the rest (each a full handshake) and
	// counted: benchstat shows them as a metric of their own.
	b.ReportMetric(float64(misses), "unresumed")
}

// maxMisses is how many of n resumed requests may make a full handshake
// instead: 1%, plus one (settleWait).
func maxMisses(n int) int {
	return n/100 + 1
}

// settleWait is how long each connection lives after its response before
// BenchmarkConnect closes it, untimed:
//   - closing idle connections right after the body's EOF misses those not
//     yet back in their pool (net/http's goes back from its read loop,
//     after the EOF): they stay open, and 2,000 leaked connections slowed
//     every later iteration;
//   - TLS 1.3 servers send session tickets after the handshake, and a
//     connection resumed with 0-RTT (mimicry over HTTP/3) can have its
//     response before its new ticket: without the wait, every other one
//     found no ticket (mimicry keeps one per connection, as Chrome does).
//     With it, about 1 in 200 still misses; up to 1% plus one is kept
//     (maxMisses), counted as the "unresumed" metric.
const settleWait = 5 * time.Millisecond

// spin waits d without sleeping: a sleeping benchmark lets the CPU idle,
// and waking it again slows the next iteration.
func spin(d time.Duration) {
	for start := time.Now(); time.Since(start) < d; {
		runtime.Gosched()
	}
}
