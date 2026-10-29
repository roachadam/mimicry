package main

import (
	"fmt"
	"strings"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// stockSection compares the HTTP/3 points with the receive buffers this
// host grants (the report's own) with those of the passes run at stock
// Linux values (StockLoad, StockMulti).
func (r *report) stockSection(w writer) {
	if len(r.StockLoad) == 0 && len(r.StockMulti) == 0 {
		return
	}

	granted, stock := r.Env, r.Env
	if r.StockEnv != nil {
		stock = *r.StockEnv
	}

	w("## HTTP/3 at stock Linux receive buffers\n\n")
	w("The HTTP/3 points again with the client container's `net.core.rmem_max` and `rmem_default` at %s and %s ", stock.RmemMax, stock.RmemDefault)
	w("(stock Linux; `BENCH_UDP_STOCK=1`, %d pass(es)), against the rest of this report's (%s and %s here). At stock ", max(r.StockPasses, 1),
		granted.RmemMax, granted.RmemDefault)
	w("values the quic-go clients' 7 MB request is cut to rmem_max, about 208 KB, and every client has about the ")
	w("buffer mimicry has everywhere (`Config.UDPReadBufferSize` 0: the OS's default). UDP: datagrams the client's kernel ")
	w("dropped on a full receive buffer during the point (`RcvbufErrors`), granted / stock.\n\n")

	if len(r.StockLoad) > 0 {
		w("### Summary at stock buffers: mimicry against the others (layer 3, HTTP/3)\n\n")
		w("As the summary at the top, over the stock-buffer points.\n\n")

		summaryTables(w, r.StockLoad, r.others(), false)

		w("### Each point, granted against stock\n\n")
		w("| server, body, conc | client | req/s granted / stock | CPU µs/req granted / stock | p99 ms granted / stock | UDP granted / stock |\n")
		w("|---|---|---:|---:|---:|---:|\n")

		mine := map[string]loadPoint{}

		for _, lp := range r.Load {
			if lp.Proto == target.H3 {
				mine[stockKey(lp)] = lp
			}
		}

		for _, sp := range r.StockLoad {
			if sp.Proto != target.H3 {
				continue
			}

			gp, ok := mine[stockKey(sp)]

			setup := fmt.Sprintf("%s, %s, c%d", sp.Server, strings.TrimPrefix(sp.Op, "bytes-"), sp.Conc)

			if !ok || !gp.valid() || !sp.valid() {
				w("| %s | %s | %s / %s ||||\n", setup, sp.Client, oneLine(validOr(gp, ok)), oneLine(validOr(sp, true)))

				continue
			}

			g, s := gp.Result, sp.Result
			w("| %s | %s | %.0f / %.0f | %.1f / %.1f | %s / %s | %s / %s |\n", setup, sp.Client, g.RPS, s.RPS,
				g.CPUPerRequest, s.CPUPerRequest, num(g.P99), num(s.P99), udpCell(gp.UDP), udpCell(sp.UDP))
		}

		w("\n")
	}

	if len(r.StockMulti) > 0 {
		w("### Layer 6 over HTTP/3, granted against stock\n\n")
		w("| setup | client | req/s granted / stock | CPU µs/req granted / stock | p99 ms granted / stock | UDP granted / stock |\n")
		w("|---|---|---:|---:|---:|---:|\n")

		mine := map[string]multiPoint{}

		for _, mp := range r.Multi {
			if mp.Proto == target.H3 {
				mine[multiStockKey(mp)] = mp
			}
		}

		for _, sp := range r.StockMulti {
			if sp.Proto != target.H3 {
				continue
			}

			gp, ok := mine[multiStockKey(sp)]

			setup := sp.key().label()
			if sp.N != r.Settings.MultiN {
				setup += fmt.Sprintf(", %d clients", sp.N)
			}

			if !ok || !gp.valid() || !sp.valid() {
				gs, ss := gp.summaryOr(ok), sp.summaryOr(true)
				if gs == ss && strings.HasPrefix(ss, "n/a") {
					// Unsupported either way: in the n/a list.
					continue
				}

				w("| %s | %s | %s / %s ||||\n", setup, sp.Client, oneLine(gs), oneLine(ss))

				continue
			}

			g, s := gp.Result, sp.Result
			w("| %s | %s | %.0f / %.0f | %.0f / %.0f | %s / %s | %s / %s |\n", setup, sp.Client, g.RPS, s.RPS,
				g.CPUPerRequest, s.CPUPerRequest, num(g.P99), num(s.P99), udpCell(gp.UDP), udpCell(sp.UDP))
		}

		w("\n")
	}
}

func stockKey(lp loadPoint) string {
	return fmt.Sprintf("%s|%s|%s|%d|%s", lp.Server, lp.Op, lp.Condition, lp.Conc, lp.Client)
}

func multiStockKey(mp multiPoint) string {
	return fmt.Sprintf("%s|%s|%d|%s|%d|%s", mp.Proxy, mp.Op, mp.Rotate, mp.Renew, mp.N, mp.Client)
}

func validOr(lp loadPoint, ok bool) string {
	switch {
	case !ok:
		return "not run"
	case lp.valid():
		return "ok"
	}

	return lp.summary()
}

func (mp multiPoint) summaryOr(ok bool) string {
	switch {
	case !ok:
		return "not run"
	case mp.valid():
		return "ok"
	}

	return mp.summary()
}

func udpCell(d *udpDelta) string {
	if d == nil {
		return "?"
	}

	return fmt.Sprint(d.RcvbufErrors)
}
