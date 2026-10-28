package main

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/internal/bench/scenario"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// multiKey is a layer 6 row: route, body, rotation, and the run it is
// from in a merged report.
type multiKey struct {
	proto  target.Proto
	proxy  target.ProxyKind
	op     string
	rotate int
	renew  scenario.Renew
	run    string
}

func (mp multiPoint) key() multiKey {
	return multiKey{
		proto:  mp.Proto,
		proxy:  mp.Proxy,
		op:     mp.Op,
		rotate: mp.Rotate,
		renew:  mp.Renew,
		run:    mp.Run,
	}
}

func (k multiKey) label() string {
	var s string

	switch {
	case k.renew != "":
		// The client-rotation section names its body and rotation once.
		s = fmt.Sprintf("%s, %s, %s", k.proto.Wire(), proxyName(k.proxy), k.renew)
	case k.rotate > 0:
		s = fmt.Sprintf("%s, %s, %s, rotating every %d", k.proto.Wire(), proxyName(k.proxy), strings.TrimPrefix(k.op, "bytes-"), k.rotate)
	default:
		s = fmt.Sprintf("%s, %s, %s", k.proto.Wire(), proxyName(k.proxy), strings.TrimPrefix(k.op, "bytes-"))
	}

	if k.run != "" {
		s += " (" + k.run + ")"
	}

	return s
}

func (mp multiPoint) rotationName() string {
	s := "one identity per client"

	switch {
	case mp.Renew != "":
		s = fmt.Sprintf("a new client every %d requests, the old one %s", mp.Rotate, mp.Renew)
	case mp.Rotate > 0:
		s = fmt.Sprintf("a new identity every %d requests", mp.Rotate)
	}

	if mp.Run != "" {
		s += " (" + mp.Run + ")"
	}

	return s
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

	// Rows in one order whatever order the points ran in: variant, route,
	// body, renew mode, and a merged report's runs side by side in the
	// order they were merged.
	ops, runs := map[string]int{}, map[string]int{}

	for _, k := range keys {
		if _, ok := ops[k.op]; !ok {
			ops[k.op] = len(ops)
		}

		if _, ok := runs[k.run]; !ok {
			runs[k.run] = len(runs)
		}
	}

	order := func(k multiKey) []int {
		route := slices.IndexFunc(multiRoutes, func(rt multiRoute) bool {
			return rt.Proto == k.proto && rt.Proxy == k.proxy
		})

		renew := slices.Index([]scenario.Renew{"", scenario.RenewDropped, scenario.RenewClosed}, k.renew)

		client := 0
		if k.renew != "" {
			client = 1
		}

		return []int{client, k.rotate, route, ops[k.op], renew, runs[k.run]}
	}

	sort.SliceStable(keys, func(i, j int) bool {
		return slices.Compare(order(keys[i]), order(keys[j])) < 0
	})

	if slices.ContainsFunc(keys, identityRow) {
		r.identityTables(w, keys, points)
	}

	if slices.ContainsFunc(keys, clientRow) {
		r.clientTables(w, keys, points)
	}
}

// identityRow and clientRow say which section a row is in: one identity
// per client or a client moving to new ones, or client rotation.
func identityRow(k multiKey) bool {
	return k.renew == ""
}

func clientRow(k multiKey) bool {
	return k.renew != ""
}

// identityTables writes the section of the fixed and proxy variants.
func (r *report) identityTables(w writer, keys []multiKey, points map[multiKey]map[string]*multiPoint) {
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

	r.renderMultiTables(w, tables, keys, points, identityRow)
}

// renderMultiTables writes tables over the rows include keeps.
func (r *report) renderMultiTables(w writer, tables []multiTable, keys []multiKey, points map[multiKey]map[string]*multiPoint,
	include func(multiKey) bool,
) {
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
			if !include(k) || (t.rotationOnly && k.rotate == 0) {
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

// saturated marks a point where the proxy used 90% or more of its CPUs.
func saturated(_ multiKey, _ string, res *scenario.MultiResult) string {
	if res.ProxyCPU >= 0.9 {
		return " ◊"
	}

	return ""
}

// signed formats a growth rate with its sign.
func signed(prec int) func(float64) string {
	return func(v float64) string {
		return fmt.Sprintf("%+.*f", prec, v)
	}
}

// clientTables writes the client-rotation section.
func (r *report) clientTables(w writer, keys []multiKey, points map[multiKey]map[string]*multiPoint) {
	st := r.Settings

	w("## Layer 6: client rotation, a new client per identity\n\n")
	w("%d routines of one kind in one process, each sending GET /bytes/1024 to nginx one request at a time through ", st.MultiN)
	w("the proxy, with credentials of its own. Every %d requests a routine builds a new client with the library's ", st.MultiRotate)
	w("ordinary constructor and the next identity's credentials (so a new tunnel or UDP association and a new handshake) ")
	w("and lets the old one go: **dropped**, abandoned as it is (code that makes a client per proxy and never closes it), or ")
	w("**closed**, with the library's close first. Each point first measures a new client alone, then warms the routines' ")
	w("first clients onto the protocol (three responses in a row), runs %v, and measures %v, longer than the other ", st.Warmup, st.MultiClientWindow)
	w("points so growth shows, sampling the process and the proxy every second. The proxy adds %s. ", r.Env.ProxyDelay)
	w("The best impersonating client in each row is **bold**; net/http is the reference, never bolded.\n\n")

	notes := []string{
		"**Constructor and close, per library.** net/http: a new `http.Transport` (HTTP/2 through its `Proxy`), closed with " +
			"`Transport.CloseIdleConnections`. mimicry: `mimicry.NewClient` (a `Config.Proxy` function returning the URL), " +
			"closed with `Transport.CloseIdleConnections` (it has no `Close`). tls-client: `tls_client.NewHttpClient` with " +
			"`WithProxyUrl`, closed with `HttpClient.CloseIdleConnections` (it has no `Close`). httpcloak: `httpcloak.NewSession` " +
			"with `WithSessionProxy`, closed with `Session.Close`. azuretls: `azuretls.NewSession` then `SetProxy` (and " +
			"`EnableHTTP3` for HTTP/3), closed with `Session.Close`, which closes its HTTP/1.1, HTTP/2 and HTTP/3 transports. " +
			"Each is the adapter's `Close`, the same call the other layers end a client with.",
		"**HTTP/3.** A new mimicry client knows nothing of HTTP/3, so the routines' clients share one `Config.AltSvc` " +
			"cache, as a caller who wants HTTP/3 from each new client does: the warm-up's clients learn nginx's, and each new " +
			"client races QUIC against TCP as Chrome does for an origin it knows (TCP after Chrome's race delay). tls-client " +
			"races too (`WithProtocolRacing`); httpcloak and azuretls force HTTP/3. A new client that loses the race goes on " +
			"TCP: up to 1% of the responses may come over another protocol in these rows, as up to 1% may fail, and the last " +
			"table gives the share. net/http has no HTTP/3 proxy: n/a.",
		"**New client under load:** from the switch (the old client's close included, when closed) to the new client's " +
			"first response, p50 / p99, and the constructor's share (p50). **Alone:** before the load, " +
			"one client at a time, each closed after its first response, 20 measured after one that warms the process up: " +
			"medians of the constructor's time, the first request's, and the bytes and allocations of both together (the " +
			"process's counters, nothing else running).",
		"**What is left, and its growth.** Once the window closes the routines finish their requests, nothing is closed, and after two GCs " +
			"the live heap and the goroutines beyond what the process had before the routines' clients were built are divided " +
			"by the routines. Growth is the least-squares slope over the per-second samples (the live heap as the last GC " +
			"marked it). RSS is the process's resident memory (`/proc/self/statm`). Open at the proxy are the point's " +
			"tunnels and UDP associations still open there: a connection a dropped client keeps shows there, a closed " +
			"client's goes. A routine still in its request a minute after the window is stuck (last table); the end is read " +
			"before its context is cancelled. Each point runs in a worker process of its own, so what it leaves ends with it.",
		"**Checked as above:** every response's protocol, and with the proxy that every identity that got a response opened " +
			"a tunnel or an association, that it relayed each routine at least the body bytes it received, and over HTTP/3 " +
			"that each routine's datagrams went through it.",
	}

	for _, n := range notes {
		w("- %s\n", n)
	}

	w("\n")

	end := func(get func(e *scenario.MultiEnd) float64) func(multiKey, string, *scenario.MultiResult) (float64, bool) {
		return func(_ multiKey, _ string, r *scenario.MultiResult) (float64, bool) {
			if r.End == nil {
				return 0, false
			}

			return get(r.End), true
		}
	}

	grow := func(get func(g *scenario.MultiGrowth) float64) func(multiKey, string, *scenario.MultiResult) (float64, bool) {
		return func(_ multiKey, _ string, r *scenario.MultiResult) (float64, bool) {
			if r.Growth == nil {
				return 0, false
			}

			return get(r.Growth), true
		}
	}

	cost := func(get func(c *scenario.NewClientCost) float64) func(multiKey, string, *scenario.MultiResult) (float64, bool) {
		return func(_ multiKey, _ string, r *scenario.MultiResult) (float64, bool) {
			if r.Cost == nil {
				return 0, false
			}

			return get(r.Cost), true
		}
	}

	open := func(e *scenario.MultiEnd) float64 {
		return float64(e.OpenTunnels + e.OpenAssociations)
	}

	tables := []multiTable{
		{
			title: "Requests per second",
			intro: "All routines together, requests completed in the window.",
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
			intro: "Milliseconds, p50 / p99, over every routine's requests in the window, each new client's first included.",
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
			title: "New client under load",
			intro: "From the switch to the new client's first response, p50 / p99 in ms / the constructor's p50 in µs.",
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
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return 1000 * r.BuildP50 }),
					format: fixed(0),
					lower:  true,
				},
			},
		},
		{
			title: "New client alone",
			intro: "Medians over 20 new clients, one at a time: the constructor in µs / the first request in ms / KB " +
				"allocated by both / allocations by both.",
			metrics: []multiMetric{
				{
					get:    cost(func(c *scenario.NewClientCost) float64 { return c.ConstructUs }),
					format: fixed(0),
					lower:  true,
				},
				{
					get:    cost(func(c *scenario.NewClientCost) float64 { return c.FirstMs }),
					format: num,
					lower:  true,
				},
				{
					get:    cost(func(c *scenario.NewClientCost) float64 { return c.KB }),
					format: fixed(0),
					lower:  true,
				},
				{
					get:    cost(func(c *scenario.NewClientCost) float64 { return c.Allocs }),
					format: fixed(0),
					lower:  true,
				},
			},
		},
		{
			title: "Live heap per routine",
			intro: "KB per routine left at the window's end (after GC) / its growth in KB per routine per second over the window.",
			metrics: []multiMetric{
				{
					get:    end(func(e *scenario.MultiEnd) float64 { return e.LiveKBPerRoutine }),
					format: fixed(0),
					lower:  true,
				},
				{
					get:    grow(func(g *scenario.MultiGrowth) float64 { return g.LiveKBPerRoutine }),
					format: signed(1),
					lower:  true,
				},
			},
		},
		{
			title: "Goroutines per routine",
			intro: "The clients' goroutines per routine left at the window's end / their growth per routine per second.",
			metrics: []multiMetric{
				{
					get:    end(func(e *scenario.MultiEnd) float64 { return e.GoroutinesPerRoutine }),
					format: fixed(1),
					lower:  true,
				},
				{
					get:    grow(func(g *scenario.MultiGrowth) float64 { return g.GoroutinesPerRoutine }),
					format: signed(2),
					lower:  true,
				},
			},
		},
		{
			title: "Process memory",
			intro: "The process's resident memory at the window's end in MB / its growth in MB per second.",
			metrics: []multiMetric{
				{
					get:    end(func(e *scenario.MultiEnd) float64 { return e.RSSMB }),
					format: fixed(0),
					lower:  true,
				},
				{
					get:    grow(func(g *scenario.MultiGrowth) float64 { return g.RSSMB }),
					format: signed(1),
					lower:  true,
				},
			},
		},
		{
			title: "Open at the proxy",
			intro: "Tunnels and UDP associations still open at the proxy at the window's end, all routines together / per " +
				"routine / their growth per second. The routines' current clients hold about one each (more for a client " +
				"that keeps a TCP connection beside its QUIC one, or races two); the rest are old clients'.",
			metrics: []multiMetric{
				{
					get:    end(open),
					format: fixed(0),
					lower:  true,
				},
				{
					get: func(_ multiKey, _ string, r *scenario.MultiResult) (float64, bool) {
						if r.End == nil || r.Clients == 0 {
							return 0, false
						}

						return open(r.End) / float64(r.Clients), true
					},
					format: fixed(1),
					lower:  true,
				},
				{
					get:    grow(func(g *scenario.MultiGrowth) float64 { return g.Open }),
					format: signed(0),
					lower:  true,
				},
			},
		},
		{
			title: "CPU and allocations per request",
			intro: "The process's CPU time per request in µs / allocations per request, new clients included.",
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
			title: "Errors, protocol and load",
			intro: "Errors in the window / routines stuck in a request a minute after it / the share of responses over " +
				"another protocol / the proxy's CPU use / nginx's, as shares of their CPUs.",
			metrics: []multiMetric{
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return float64(r.Errors) }),
					format: fixed(0),
					noBold: true,
				},
				{
					get:    plain(func(r *scenario.MultiResult) float64 { return float64(r.Stuck) }),
					format: fixed(0),
					noBold: true,
				},
				{
					get: plain(func(r *scenario.MultiResult) float64 {
						if r.Requests+r.Mismatches == 0 {
							return 0
						}

						return float64(r.Mismatches) / float64(r.Requests+r.Mismatches)
					}),
					format: func(v float64) string {
						return strconv.FormatFloat(100*v, 'f', 2, 64) + "%"
					},
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

	r.renderMultiTables(w, tables, keys, points, clientRow)
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
