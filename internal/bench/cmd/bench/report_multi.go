package main

import (
	"fmt"
	"strings"

	"github.com/roachadam/mimicry/internal/bench/scenario"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// multiKey is a layer 6 row: route, body and rotation.
type multiKey struct {
	proto  target.Proto
	proxy  target.ProxyKind
	op     string
	rotate int
}

func (mp multiPoint) key() multiKey {
	return multiKey{
		proto:  mp.Proto,
		proxy:  mp.Proxy,
		op:     mp.Op,
		rotate: mp.Rotate,
	}
}

func (k multiKey) label() string {
	s := fmt.Sprintf("%s, %s, %s", k.proto.Wire(), proxyName(k.proxy), strings.TrimPrefix(k.op, "bytes-"))
	if k.rotate > 0 {
		s += fmt.Sprintf(", rotating every %d", k.rotate)
	}

	return s
}

func rotationName(rotate int) string {
	if rotate == 0 {
		return "one identity per client"
	}

	return fmt.Sprintf("a new identity every %d requests", rotate)
}

func proxyName(k target.ProxyKind) string {
	switch k {
	case target.ProxyConnect:
		return "CONNECT"
	case target.ProxyConnectAuth:
		return "CONNECT (Basic auth)"
	case target.ProxySOCKS5:
		return "SOCKS5 (socks5h, user/pass)"
	}

	return string(k)
}

// referenceClient is the baseline the layer 6 tables show but never bold.
const referenceClient = "net/http"

// multiMetric is one value a layer 6 cell shows: get returns it (ok false
// when there is none), lower says lower is better, noBold leaves it
// unmarked.
type multiMetric struct {
	get    func(k multiKey, client string, r *scenario.MultiResult) (float64, bool)
	format func(float64) string
	lower  bool
	noBold bool
}

// multiTable is one layer 6 table: a row per setup, a column per client,
// each cell its metrics joined by " / ".
type multiTable struct {
	title, intro string
	// rotationOnly keeps the rows with rotation.
	rotationOnly bool
	metrics      []multiMetric
	// mark returns a marker for a cell, or "".
	mark func(k multiKey, client string, r *scenario.MultiResult) string
}

func plain(get func(r *scenario.MultiResult) float64) func(multiKey, string, *scenario.MultiResult) (float64, bool) {
	return func(_ multiKey, _ string, r *scenario.MultiResult) (float64, bool) {
		return get(r), true
	}
}

func fixed(prec int) func(float64) string {
	return func(v float64) string {
		return fmt.Sprintf("%.*f", prec, v)
	}
}

func (r *report) multiTables(w writer) {
	if len(r.Multi) == 0 {
		return
	}

	var keys []multiKey

	points := map[multiKey]map[string]*multiPoint{}

	for i := range r.Multi {
		mp := &r.Multi[i]
		k := mp.key()

		if points[k] == nil {
			points[k] = map[string]*multiPoint{}

			keys = append(keys, k)
		}

		points[k][mp.Client] = mp
	}

	steady := func(k multiKey, client string) *scenario.MultiResult {
		k.rotate = 0
		if mp := points[k][client]; mp != nil && mp.valid() {
			return mp.Result
		}

		return nil
	}

	perIdentity := func(get func(*scenario.MultiResult) float64) func(multiKey, string, *scenario.MultiResult) (float64, bool) {
		return func(k multiKey, client string, res *scenario.MultiResult) (float64, bool) {
			s := steady(k, client)
			if s == nil || k.rotate == 0 {
				return 0, false
			}

			return (get(res) - get(s)) * float64(k.rotate), true
		}
	}

	st := r.Settings

	w("## Layer 6: many clients, each through its own proxy identity\n\n")
	w("%d clients of one kind in one process, each its own Transport or session with its own proxy credentials ", st.MultiN)
	w("(`bench-<run>-<client>-<identity>`, so no two clients can share a tunnel), each sending GET /bytes/{size} to nginx ")
	w("one request at a time: the workload of a scraper running a client per proxy. Each point warms every client onto ")
	w("the protocol (three responses in a row), runs %v, then measures %v. The rotation variant gives each client a new ", st.Warmup, st.Window)
	w("identity every %d requests: new credentials, so a new tunnel (or UDP association) and a new handshake, as a ", st.MultiRotate)
	w("rotating residential proxy forces. The proxy adds %s; its CPU set is in Environment. ", r.Env.ProxyDelay)
	w("The best impersonating client in each row is **bold**; net/http is the reference, never bolded.\n\n")

	notes := []string{
		"**Every request through the proxy, checked.** Each response must have come over the route's protocol " +
			"(`x-proto`, as in the other layers; a point with one that did not is discarded). After the window the proxy's " +
			"per-user counts are checked against each client: every identity that got a response opened a tunnel or a UDP " +
			"association at the proxy, the proxy relayed each client at least the body bytes it received, and over HTTP/3 " +
			"each client had an association that carried datagrams. A point failing any of it is discarded.",
		"**How each client goes through a proxy and rotates.** The URL carries the credentials (`http://user:pass@…` for " +
			"CONNECT, `socks5h://user:pass@…` for SOCKS5). mimicry takes a `Proxy` function that returns the current URL and " +
			"rotates by changing what it returns (its documented way); its pool keeps each old identity's connection until " +
			"it idles out, so with rotation memory and goroutines grow over the warm-up and window. net/http, the reference, " +
			"does the same and then closes its idle connections, as a net/http user who rotates must: its HTTP/2 pool is " +
			"keyed by host alone, and without that every request went on through the first identity's tunnel (the proxy " +
			"check caught it). tls-client, httpcloak and azuretls rotate with their `SetProxy` (tls-client builds a new " +
			"transport and leaves the old one's connections open; azuretls closes its TCP transport's idle connections, " +
			"but its HTTP/3 connections stay, each new identity's requests going on over the old association: the proxy " +
			"check discards those points). The latency of an identity's first request runs from the switch to the response.",
		"**CONNECT credentials ‡.** mimicry sends credentials as Chrome does: the first tunnel to a proxy URL goes without " +
			"them, and is answered after the proxy's 407 (unasked from then on, per URL). A URL with new credentials is a new " +
			"proxy to it, so each new identity through CONNECT costs mimicry one extra round trip to the proxy; the others " +
			"send `Proxy-Authorization` with the first CONNECT.",
		"**HTTP/3 through SOCKS5.** mimicry with `Config.HTTP3OverSOCKS5`, moving to HTTP/3 after Alt-Svc as directly; " +
			"httpcloak with `WithSessionProxy` (its TCP and UDP proxy) and `WithForceHTTP3`; azuretls with `EnableHTTP3` and " +
			"`ForceHTTP3`; tls-client with `WithProtocolRacing`, which races QUIC over a UDP association against TCP through " +
			"the proxy. net/http's HTTP/3 (quic-go's `http3.Transport`) has no proxy setting: n/a. With rotation, mimicry " +
			"races each new identity's QUIC connection against TCP as Chrome does for an origin it knows serves HTTP/3 (TCP " +
			"after Chrome's 305 ms race delay): a request whose QUIC handshake through the proxy takes longer goes on HTTP/2, " +
			"and the protocol check discards the point for it.",
		"**Memory per client** is beyond what the process held before the clients were built (after GC): the live heap " +
			"as the window's collections marked it, averaged over the window (steady state), and the peak heap with garbage " +
			"not yet swept (sampled every 100 ms), divided by the clients. Stack is the goroutine stacks in use at the " +
			"window's end, the goroutine sending each client's requests included; goroutines per client leave the senders " +
			"out. CPU, allocations and GC are the process's over the window, divided by the requests.",
		"**One shared proxy.** Every client goes through the benchmark's one proxy. ◊ marks a point where it used 90% or " +
			"more of its CPUs: requests/s there measure the proxy as much as the client; CPU, allocations and memory per " +
			"request and per client still compare the clients. The benchmark's own rotation work (a proxy URL string per " +
			"identity) is in every client's numbers alike.",
	}

	for _, n := range notes {
		w("- %s\n", n)
	}

	w("\n")

	saturated := func(_ multiKey, _ string, res *scenario.MultiResult) string {
		if res.ProxyCPU >= 0.9 {
			return " ◊"
		}

		return ""
	}

	tables := []multiTable{
		{
			title: "Requests per second",
			intro: "All clients together, requests completed in the window.",
			metrics: []multiMetric{
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.RPS }),
					format: fixed(0),
				},
			},
			mark: saturated,
		},
		{
			title: "Latency per request",
			intro: "Milliseconds, p50 / p99, over every client's requests in the window.",
			metrics: []multiMetric{
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.P50 }),
					format: num,
					lower:  true,
				},
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.P99 }),
					format: num,
					lower:  true,
				},
			},
		},
		{
			title: "CPU and allocations per request",
			intro: "The process's CPU time per request in µs / allocations per request.",
			metrics: []multiMetric{
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.CPUPerRequest }),
					format: fixed(0),
					lower:  true,
				},
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.AllocsPerRequest }),
					format: fixed(0),
					lower:  true,
				},
			},
		},
		{
			title: "Heap per client",
			intro: fmt.Sprintf("KB per client: live heap at steady state / peak heap. With rotation, a client that keeps "+
				"its old identities' connections open (mimicry until they idle out, tls-client's old transports) grows "+
				"with time: these are over the first %v of load.", st.Warmup+st.Window),
			metrics: []multiMetric{
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.LiveKBPerClient }),
					format: fixed(0),
					lower:  true,
				},
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.PeakKBPerClient }),
					format: fixed(0),
					lower:  true,
				},
			},
		},
		{
			title: "Goroutines and stack per client",
			intro: "The client's own goroutines at the window's end / goroutine stack KB per client (its sender included).",
			metrics: []multiMetric{
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.GoroutinesPerClient }),
					format: fixed(1),
					lower:  true,
				},
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.StackKBPerClient }),
					format: fixed(0),
					lower:  true,
				},
			},
		},
		{
			title: "Rotation: new-connection latency",
			intro: "An identity's first request, from the switch to the response (a new tunnel or association and a " +
				"handshake): ms, p50 / p99. ‡: mimicry's extra round trip for the proxy's 407 through CONNECT.",
			rotationOnly: true,
			metrics: []multiMetric{
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.NewP50 }),
					format: num,
					lower:  true,
				},
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.NewP99 }),
					format: num,
					lower:  true,
				},
			},
			mark: func(k multiKey, client string, _ *scenario.MultiResult) string {
				if client == "mimicry" && k.proxy == target.ProxyConnectAuth {
					return " ‡"
				}

				return ""
			},
		},
		{
			title: "Rotation: handshakes",
			intro: "New connections per second at the proxy (tunnels + UDP associations opened for the run's identities " +
				"in the window), and the share of identities' first requests whose TLS session the server says was resumed " +
				"(a ticket from another identity: linkable across proxies). Not bolded.",
			rotationOnly: true,
			metrics: []multiMetric{
				{
					get: plain(func(r *scenario.MultiResult) float64 {
						return r.TunnelsPerSec + r.AssociationsPerSec
					}),
					format: fixed(0),
					noBold: true,
				},
				{
					get: plain(func(r *scenario.MultiResult) float64 {
						if r.NewConns == 0 {
							return 0
						}

						return 100 * float64(r.NewResumed) / float64(r.NewConns)
					}),
					format: func(v float64) string {
						return fmt.Sprintf("%.0f%% resumed", v)
					},
					noBold: true,
				},
			},
		},
		{
			title: "Rotation: allocation per new identity",
			intro: "Against the same route and body without rotation: (allocated per request with rotation − without) × " +
				"requests per identity, in KB / the same for allocations: what each new identity (proxy switch, tunnel, " +
				"handshake) costs the process's heap. CPU per request with rotation is in the CPU table; its difference " +
				"from the row without, times the requests per identity, is within the run-to-run noise of short windows, " +
				"so it is not shown.",
			rotationOnly: true,
			metrics: []multiMetric{
				{
					get:    perIdentity(func(r *scenario.MultiResult) float64 { return r.BytesPerRequest / 1024 }),
					format: fixed(0),
					lower:  true,
				},
				{
					get:    perIdentity(func(r *scenario.MultiResult) float64 { return r.AllocsPerRequest }),
					format: fixed(0),
					lower:  true,
				},
			},
		},
		{
			title: "Errors and the proxy's and server's load",
			intro: "Errors in the window / the proxy's CPU use / nginx's CPU use, as shares of their CPUs.",
			metrics: []multiMetric{
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return float64(r.Errors) }),
					format: fixed(0),
					noBold: true,
				},
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.ProxyCPU }),
					format: pct,
					noBold: true,
				},
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return r.ServerCPU }),
					format: pct,
					noBold: true,
				},
			},
		},
	}

	cols := r.clientNames()
	heads := make([]string, len(cols))

	for i, c := range cols {
		heads[i] = c
		if c == referenceClient {
			heads[i] = c + " (reference)"
		}
	}

	for _, t := range tables {
		w("### %s\n\n%s\n\n", t.title, t.intro)
		w("| setup | %s |\n|---|%s\n", strings.Join(heads, " | "), strings.Repeat("---:|", len(cols)))

		for _, k := range keys {
			if t.rotationOnly && k.rotate == 0 {
				continue
			}

			w("| %s |", k.label())

			best := make([]float64, len(t.metrics))
			valid := make([]int, len(t.metrics))

			for i, m := range t.metrics {
				for _, c := range cols {
					mp := points[k][c]
					if c == referenceClient || mp == nil || !mp.valid() {
						continue
					}

					v, ok := m.get(k, c, mp.Result)
					if !ok {
						continue
					}

					if valid[i] == 0 || (m.lower && v < best[i]) || (!m.lower && v > best[i]) {
						best[i] = v
					}

					valid[i]++
				}
			}

			for _, c := range cols {
				w(" %s |", t.cell(k, c, points[k][c], best, valid))
			}

			w("\n")
		}

		w("\n")
	}
}

// cell renders one client's cell in t's row k, bolding a value equal to
// best when at least two impersonating clients have one.
func (t multiTable) cell(k multiKey, client string, mp *multiPoint, best []float64, valid []int) string {
	switch {
	case mp == nil:
		return ""
	case mp.NA != "":
		return "n/a"
	case mp.Error != "":
		return "error"
	case mp.Result == nil || mp.Result.Discarded != "":
		return "discarded"
	}

	parts := make([]string, 0, len(t.metrics))

	for i, m := range t.metrics {
		v, ok := m.get(k, client, mp.Result)
		if !ok {
			parts = append(parts, "–")

			continue
		}

		s := m.format(v)
		if !m.noBold && client != referenceClient && valid[i] > 1 && m.format(best[i]) == s {
			s = "**" + s + "**"
		}

		parts = append(parts, s)
	}

	out := strings.Join(parts, " / ")
	if t.mark != nil {
		out += t.mark(k, client, mp.Result)
	}

	return out
}
