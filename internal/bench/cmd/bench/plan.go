package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// bench run -plan walks the run's layers as the run would, counting each
// job and estimating its duration instead of running it, and prints them
// grouped with the total. The estimate is arithmetic over the settings:
// a load point's warm-up and window (stretched as scenario.LoadSpec does,
// for a nominal throughput), a layer 6 point's warm-up and window, page
// loads at the condition's RTT and loss, plus each worker's start. The
// constants come from the job timestamps of a run's log (bench-2026-10-29,
// BENCH_FAST: 1 KB load and layer 6 points 4.0-4.4 s at a 1 s warm-up and 3 s
// window, client rotation 12.4-13 s at 10 s, page-load sets 1.7-17 s).
const (
	// planOverhead is a worker's start, its client and first connections,
	// beyond the warm-up and window.
	planOverhead = 500 * time.Millisecond
	// planNA is a job the client cannot run: the worker says so at once.
	planNA = 200 * time.Millisecond
	// planThroughput is the bytes per second a load point is assumed to
	// move, for the stretch rule's latency (conc × body / throughput):
	// 10 MB at 256 at once then stretches to about 13 s of warm-up and
	// 32 s of window.
	planThroughput = 400e6
	// planNewClients is client rotation's new clients measured alone
	// before the load.
	planNewClients = 1500 * time.Millisecond
	// planPageBase is a page-load job's own start beyond planOverhead,
	// planPageLoad a cold and a warm load without delay, planPageRTTs the
	// round trips they take, planLossRTTs those 1% loss adds, and
	// planProxyLoad what a proxy (and its own delay) adds per load.
	planPageBase  = 500 * time.Millisecond
	planPageLoad  = 200 * time.Millisecond
	planPageRTTs  = 28
	planLossRTTs  = 10
	planProxyLoad = 400 * time.Millisecond
)

// planner counts and estimates a run's jobs, grouped by what they measure.
type planner struct {
	groups []*planGroup
	byKey  map[string]*planGroup
}

// planGroup is the jobs that differ only in their client and in values
// (concurrency, client count, page loads).
type planGroup struct {
	layer   int
	what    string
	values  []string
	jobs    int
	na      int
	seconds time.Duration
}

// plan prints the run st describes: its jobs by layer and group, and how
// long they should take.
func plan(ctx context.Context, st settings) error {
	p := &planner{
		byKey: map[string]*planGroup{},
	}
	rn := &runner{
		st:   st,
		ctx:  ctx,
		plan: p,
	}

	if slices.Contains(st.Layers, 3) {
		rn.load()
	}

	if slices.Contains(st.Layers, 4) {
		rn.idle()
	}

	if slices.Contains(st.Layers, 5) {
		rn.pages()
	}

	if slices.Contains(st.Layers, 6) {
		rn.multi()
	}

	return p.print(os.Stdout, st)
}

// add counts j, run under cond.
func (p *planner) add(j job, cond condition) {
	layer, what, value := describe(j, cond)

	key := strconv.Itoa(layer) + " " + what

	g := p.byKey[key]
	if g == nil {
		g = &planGroup{
			layer: layer,
			what:  what,
		}
		p.byKey[key] = g
		p.groups = append(p.groups, g)
	}

	if value != "" && !slices.Contains(g.values, value) {
		g.values = append(g.values, value)
	}

	d, ok := estimate(j, cond)

	g.jobs++

	g.seconds += d

	if !ok {
		g.na++
	}
}

// describe is j's layer, its group, and the value that varies within it.
func describe(j job, cond condition) (layer int, what, value string) {
	via := ""
	if j.Proxy != "" {
		via = " via " + string(j.Proxy)
	}

	switch j.Kind {
	case kindLoad:
		what = fmt.Sprintf("%s %s %s", j.Server, j.Proto, j.Op.Name)
		if cond.Name != "" {
			what += " under " + cond.Name
		}

		return 3, what, strconv.Itoa(j.Conc)

	case kindIdle:
		return 4, fmt.Sprintf("idle %s %s", j.Server, j.Proto), strconv.Itoa(j.N)

	case kindPage:
		return 5, fmt.Sprintf("%s %s%s", cond.Name, j.Proto, via), strconv.Itoa(j.Reps)

	case kindMulti:
		variant := variantFixed

		switch {
		case j.Renew != "":
			variant = variantClient + " " + string(j.Renew)
		case j.Rotate > 0:
			variant = variantProxy
		}

		return 6, fmt.Sprintf("%s %s%s %s", variant, j.Proto, via, j.Op.Name), strconv.Itoa(j.N)
	}

	return 0, j.Kind, ""
}

// valueName is what a group's values are, by layer.
var valueName = map[int]string{
	3: "c",
	4: "n",
	5: "loads",
	6: "n",
}

// estimate is how long j should take under cond, and false for a job the
// client cannot run.
func estimate(j job, cond condition) (time.Duration, bool) {
	su, err := j.setup()
	if err == nil {
		err = su.Supported()
	}

	if err != nil {
		return planNA, false
	}

	switch j.Kind {
	case kindLoad:
		// scenario.LoadSpec: the warm-up until twice the median latency (up
		// to a minute), the window until five of them (up to MaxWindow),
		// then the requests in flight drain.
		bytes := float64(j.Conc * bodySize(j.Op.Path))
		lat := cond.RTT + time.Duration(bytes/planThroughput*float64(time.Second))
		warm := min(max(j.Warmup, 2*lat), time.Minute)
		win := min(max(j.Window, 5*lat), j.MaxWindow)

		return planOverhead + warm + win + lat, true

	case kindIdle:
		return planOverhead + 3*time.Second + time.Duration(j.N)*25*time.Millisecond, true

	case kindPage:
		load := planPageLoad + time.Duration(float64(cond.RTT)*(planPageRTTs+planLossRTTs*cond.Loss))
		if j.Proxy != "" {
			load += planProxyLoad
		}

		return planOverhead + planPageBase + time.Duration(j.Reps)*load, true

	case kindMulti:
		d := planOverhead + j.Warmup + j.Window
		if j.Renew != "" {
			d += planNewClients
		}

		return d, true
	}

	return planOverhead, true
}

// bodySize is the response size of a GET /bytes/{n}, else 0.
func bodySize(path string) int {
	s, ok := strings.CutPrefix(path, "/bytes/")
	if !ok {
		return 0
	}

	n, _ := strconv.Atoi(s)

	return n
}

// print writes the plan: the clients, each layer's total and groups, and
// the run's total, whose last line bench.sh reads ("(<seconds> s)").
func (p *planner) print(out io.Writer, st settings) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	var err error

	printf := func(format string, args ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, args...)
		}
	}

	printf("clients: %s\n", strings.Join(st.Clients, ", "))

	var jobs, na int

	var total time.Duration

	for _, layer := range []int{3, 4, 5, 6} {
		var lj int

		var ld time.Duration

		for _, g := range p.groups {
			if g.layer == layer {
				lj += g.jobs
				ld += g.seconds
				na += g.na
			}
		}

		if lj == 0 {
			continue
		}

		printf("layer %d: %d jobs, ~%s\n", layer, lj, minutes(ld))

		for _, g := range p.groups {
			if g.layer != layer {
				continue
			}

			values := ""
			if len(g.values) > 0 {
				values = valueName[layer] + "=" + strings.Join(g.values, ",")
			}

			nas := ""
			if g.na > 0 {
				nas = fmt.Sprintf(" (%d n/a)", g.na)
			}

			printf("  %s\t%s\t%d jobs%s\t~%s\n", g.what, values, g.jobs, nas, minutes(g.seconds))
		}

		jobs += lj
		total += ld
	}

	printf("plan: %d jobs (%d n/a), estimated %s (%d s)\n", jobs, na, minutes(total), int(total.Seconds()))

	if err != nil {
		return err
	}

	return w.Flush()
}

// minutes prints d in minutes, to a tenth.
func minutes(d time.Duration) string {
	return fmt.Sprintf("%.1f min", d.Minutes())
}
