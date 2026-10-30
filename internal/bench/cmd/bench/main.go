// Command bench runs layers 3 to 6 of the benchmark (throughput and
// latency, memory, realistic network, many clients through proxies) in the
// client container and writes reports/bench-<date>.md and .json. Layers 1
// and 2 are Go benchmarks (bench_test.go); bench run embeds their benchstat
// summary when given one (-micro). See ../../README.md.
//
//	bench run [flags]      the orchestrator
//	bench render [flags]   rewrite a report's Markdown from its JSON, with new micro results
//	bench merge [flags] label=report.json...   one report of several runs' layer 6 points, side by side
//	bench merge -passes [flags] pass.json...   one report of several passes: medians and ranges (-udp-stock: the stock-buffer passes)
//	bench microstat        benchstat's output (stdin) without the geomean rows it flags as not comparable
//	bench wait             wait until the servers and the proxy answer
//	bench worker           one measurement with one client, spec on stdin
//	bench offloads         a sidecar on the client's Docker host: its segmentation offloads, off under netem
//	bench charts [flags]   docs/performance.md's SVG charts from a merged report's JSON (-in) and its micro output
//
// Every measurement runs in a worker process of its own, so one client's
// heap, goroutines and GC never mix with another's.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/roachadam/mimicry/internal/bench/target"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	var err error

	switch os.Args[1] {
	case "run":
		err = run(os.Args[2:])
	case "render":
		err = render(os.Args[2:])
	case "merge":
		err = merge(os.Args[2:])
	case "microstat":
		err = microstat(os.Stdin, os.Stdout)
	case "worker":
		err = worker(os.Stdin, os.Stdout)
	case "wait":
		err = waitReady(time.Minute)
	case "offloads":
		err = offloadsAgent()
	case "charts":
		err = charts(os.Args[2:])
	default:
		usage()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bench run [flags] | bench render [flags] | bench merge [flags] label=report.json... | bench merge -passes [flags] pass.json... | bench charts [flags] | bench microstat | bench wait | bench worker | bench offloads")
	os.Exit(2)
}

// waitReady waits until every server and the proxy answer /_stats.
func waitReady(limit time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	type endpoint struct {
		host string
		port int
	}

	eps := make([]endpoint, 0, 1+len(target.Servers))

	eps = append(eps, endpoint{
		host: target.ProxyHost,
		port: target.ProxyStatsPort,
	})

	for _, s := range target.Servers {
		eps = append(eps, endpoint{
			host: s.Host("www"),
			port: s.StatsPort,
		})
	}

	for _, ep := range eps {
		for {
			_, err := target.FetchStats(ctx, ep.host, ep.port)
			if err == nil {
				break
			}

			select {
			case <-ctx.Done():
				return fmt.Errorf("%s:%d not ready: %w", ep.host, ep.port, err)
			case <-time.After(500 * time.Millisecond):
			}
		}
	}

	return nil
}
