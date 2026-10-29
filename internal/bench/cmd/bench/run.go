package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/roachadam/mimicry/internal/bench/clients"
	"github.com/roachadam/mimicry/internal/bench/scenario"
	"github.com/roachadam/mimicry/internal/bench/site"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// settings are run's flags, recorded in the report.
type settings struct {
	Window time.Duration `json:"window"`
	Warmup time.Duration `json:"warmup"`
	// MaxWindow caps layer 3's stretched windows (scenario.LoadSpec).
	MaxWindow   time.Duration `json:"maxWindow,omitempty"`
	Layers      []int         `json:"layers"`
	Clients     []string      `json:"clients"`
	Servers     []string      `json:"servers"`
	Reduced     []string      `json:"reduced"`
	Protos      []string      `json:"protos"`
	Concurrency []int         `json:"concurrency"`
	// IdleNs are layer 4's client counts (two or more: the report fits a
	// slope and an intercept), IdleN the largest.
	IdleN  int   `json:"idleN"`
	IdleNs []int `json:"idleNs,omitempty"`
	// Reps are layer 5's page loads per setup and condition, RepsLoss
	// under the conditions with loss.
	Reps     int  `json:"reps"`
	RepsLoss int  `json:"repsLoss,omitempty"`
	Netem    bool `json:"netem"`
	// LoadNetem are the network conditions layer 3 runs under (none: the
	// network as it is), Bodies the response sizes it runs (none: all).
	LoadNetem  []string `json:"loadNetem,omitempty"`
	Bodies     []string `json:"bodies,omitempty"`
	ProfileDir string   `json:"profileDir,omitempty"`
	// MultiN is layer 6's clients (routines) per point, MultiVariants its
	// variants, MultiRotate the requests per proxy identity (or client)
	// in the rotating ones, MultiProxyWindow the proxy-rotation variant's
	// window and MultiClientWindow the client-rotation variant's.
	// MultiProxyRoutes and MultiProxyBodies limit the proxy-rotation
	// variant to those routes (<proto>-<proxy>) and sizes (none: all).
	// MultiN2, when set, runs MultiN2Variants' 1 KB points again with that
	// many clients, for the report's slope and intercept per client
	// (reports from before the field: the fixed and proxy variants').
	MultiN            int           `json:"multiN,omitempty"`
	MultiN2           int           `json:"multiN2,omitempty"`
	MultiN2Variants   []string      `json:"multiN2Variants,omitempty"`
	MultiVariants     []string      `json:"multiVariants,omitempty"`
	MultiRotate       int           `json:"multiRotate,omitempty"`
	MultiProxyWindow  time.Duration `json:"multiProxyWindow,omitempty"`
	MultiProxyRoutes  []string      `json:"multiProxyRoutes,omitempty"`
	MultiProxyBodies  []string      `json:"multiProxyBodies,omitempty"`
	MultiClientWindow time.Duration `json:"multiClientWindow,omitempty"`
}

// n2Variants are the variants whose 1 KB points run again at MultiN2.
func (st settings) n2Variants() []string {
	if len(st.MultiN2Variants) == 0 {
		return []string{variantFixed, variantProxy}
	}

	return st.MultiN2Variants
}

// variants are layer 6's variants (reports from before -multi-variants
// ran fixed and proxy).
func (st settings) variants() []string {
	if len(st.MultiVariants) == 0 {
		return []string{variantFixed, variantProxy}
	}

	return st.MultiVariants
}

// n2Ran are the variants that ran at MultiN2 as well: none without it.
func (st settings) n2Ran() []string {
	if st.MultiN2 <= 0 {
		return nil
	}

	var out []string

	for _, v := range st.n2Variants() {
		if slices.Contains(st.variants(), v) {
			out = append(out, v)
		}
	}

	return out
}

// render rewrites a report's Markdown (and JSON) from its JSON, replacing
// its micro results when -micro is given: for a micro run redone after the
// long layers, or a change to the report's text.
func render(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	from := fs.String("json", "", "the report's JSON (bench-<date>.json)")
	micro := fs.String("micro", "", "benchstat output of the micro benchmarks to embed instead")
	microRaw := fs.String("micro-raw", "", "the micro benchmarks' raw output, named in the report")

	_ = fs.Parse(args)

	b, err := os.ReadFile(*from)
	if err != nil {
		return err
	}

	var r report
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}

	if *micro != "" {
		m, err := os.ReadFile(*micro)
		if err != nil {
			return err
		}

		r.Micro = string(m)
		r.MicroRaw = *microRaw
	}

	return r.write(filepath.Dir(*from))
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	out := fs.String("out", "reports", "directory for the report")
	window := fs.Duration("window", 5*time.Second, "measurement window per load point (at least; stretched for long requests)")
	warmup := fs.Duration("warmup", 2*time.Second, "warm-up per load point, at full concurrency (at least; stretched for long requests)")
	maxWindow := fs.Duration("max-window", 120*time.Second, "layer 3: the longest a stretched window may run")
	layers := fs.String("layers", "3,4,6", "layers to run (3 load, 4 memory, 5 network, 6 many clients through proxies)")
	clientList := fs.String("clients", "", "clients to run (comma-separated; default all)")
	serverList := fs.String("servers", "nginx", "servers for layers 3 and 4 (nginx, go)")
	reduced := fs.String("reduced", "go", "servers that get the reduced load matrix (bodies 1KB and 100KB, concurrency 1 and 64)")
	protoList := fs.String("protos", "h1,h2,h3", "protocols for layers 3 and 5")
	concList := fs.String("conc", "1,64", "concurrency levels (e.g. 1,16,64,256)")
	idleN := fs.String("idle-n", "10,50", "clients per idle-memory measurement; two or more counts fit a slope (per connection) and an intercept (fixed)")
	reps := fs.Int("reps", 10, "page loads per setup and network condition")
	repsLoss := fs.Int("reps-loss", 20, "page loads per setup under the conditions with loss")
	useNetem := fs.Bool("netem", true, "shape the client's network for layer 5 (off: the network as it is, e.g. a remote server over Ethernet)")
	loadNetem := fs.String("load-netem", "", "layer 3: network conditions to run the load points under (e.g. rtt80-loss1; empty: the network as it is)")
	bodyList := fs.String("bodies", "1KB,100KB", "layer 3: response sizes to run (1KB, 100KB, 10MB; all: every size)")
	multiN := fs.Int("multi-n", 100, "layer 6: clients per point, each through a proxy identity of its own")
	multiN2 := fs.Int("multi-n2", 25, "layer 6: a second client count for -multi-n2-variants' 1 KB points, for memory per client as a slope (0: none)")
	multiN2Variants := fs.String("multi-n2-variants", variantFixed, "layer 6: the variants whose 1 KB points run again at -multi-n2 (fixed, proxy)")
	multiVariantList := fs.String("multi-variants", strings.Join(multiVariants, ","),
		"layer 6: variants (fixed: one identity per client; proxy: a client moves to a new identity; client: a routine replaces its client, dropped and then closed)")
	multiRotate := fs.Int("multi-rotate", 10, "layer 6: requests per proxy identity (proxy variant) or per client (client variant)")
	multiProxyWindow := fs.Duration("multi-proxy-window", 30*time.Second, "layer 6: the proxy variant's measurement window, long enough for its memory to level off")
	multiProxyRoutes := fs.String("multi-proxy-routes", "h2-connect-auth",
		"layer 6: the proxy variant's routes, <proto>-<proxy> (h2-connect-auth, h1-connect-auth, h2-socks5, h3-socks5; all: every route)")
	multiProxyBodies := fs.String("multi-proxy-bodies", "1KB", "layer 6: the proxy variant's response sizes (1KB, 100KB; all: both)")
	multiClientWindow := fs.Duration("multi-client-window", 10*time.Second, "layer 6: the client variant's measurement window, long enough for growth to show")
	micro := fs.String("micro", "", "benchstat output of the micro benchmarks (layers 1 and 2) to embed")
	microRaw := fs.String("micro-raw", "", "the micro benchmarks' raw output, named in the report")
	profileDir := fs.String("profile", "", "write CPU and allocation profiles of mimicry's jobs here")
	date := fs.String("date", time.Now().Format("2006-01-02"), "the report's date")
	name := fs.String("name", "bench", "the report's file name before the date: <name>-<date>.md and .json")
	planOnly := fs.Bool("plan", false, "print what the run would measure and how long it should take, without measuring")

	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	idleNs := ints(*idleN)
	slices.Sort(idleNs)

	st := settings{
		Window:            *window,
		Warmup:            *warmup,
		MaxWindow:         *maxWindow,
		Layers:            ints(*layers),
		Clients:           names(*clientList, clientNames()),
		Servers:           names(*serverList, nil),
		Reduced:           names(*reduced, nil),
		Protos:            names(*protoList, nil),
		Concurrency:       ints(*concList),
		IdleNs:            idleNs,
		Reps:              *reps,
		RepsLoss:          *repsLoss,
		Netem:             *useNetem,
		LoadNetem:         names(*loadNetem, nil),
		Bodies:            subset(*bodyList),
		ProfileDir:        *profileDir,
		MultiN:            *multiN,
		MultiN2:           *multiN2,
		MultiN2Variants:   names(*multiN2Variants, nil),
		MultiVariants:     names(*multiVariantList, multiVariants),
		MultiRotate:       *multiRotate,
		MultiProxyWindow:  *multiProxyWindow,
		MultiProxyRoutes:  subset(*multiProxyRoutes),
		MultiProxyBodies:  subset(*multiProxyBodies),
		MultiClientWindow: *multiClientWindow,
	}

	if len(idleNs) == 0 {
		return errors.New("-idle-n: no client count")
	}

	st.IdleN = idleNs[len(idleNs)-1]

	if st.MultiN2 == st.MultiN {
		st.MultiN2 = 0
	}

	for _, c := range st.LoadNetem {
		if _, ok := conditionNamed(c); !ok {
			return fmt.Errorf("-load-netem: no condition %q", c)
		}
	}

	if err := st.check(); err != nil {
		return err
	}

	if *planOnly {
		return plan(ctx, st)
	}

	// netem needs CAP_NET_ADMIN, which the stock-buffer passes drop
	// (docker/compose.client.udpstock.yml).
	if na := capNetAdmin(); na != nil && !*na {
		if len(st.LoadNetem) > 0 || (slices.Contains(st.Layers, 5) && st.Netem) {
			return errors.New("netem (layer 5, -load-netem) needs NET_ADMIN, which this client container lacks " +
				"(BENCH_UDP_STOCK drops it): run layers 3 and 6, or layer 5 with -netem=false")
		}
	}

	if st.ProfileDir != "" {
		if err := os.MkdirAll(st.ProfileDir, 0o750); err != nil {
			return err
		}
	}

	r := &report{
		Date:     *date,
		Name:     *name,
		Settings: st,
		MicroRaw: *microRaw,
	}

	if *micro != "" {
		b, err := os.ReadFile(*micro)
		if err != nil {
			return err
		}

		r.Micro = string(b)
	}

	r.Env = environment(ctx, st.Clients)

	rn := &runner{
		st:  st,
		ctx: ctx,
	}

	if slices.Contains(st.Layers, 3) {
		r.Load = rn.load()
	}

	if slices.Contains(st.Layers, 4) {
		r.Idle = rn.idle()
	}

	if slices.Contains(st.Layers, 5) {
		r.Pages, r.Env.Netem = rn.pages()
	}

	if slices.Contains(st.Layers, 6) {
		r.Multi = rn.multi()
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	return r.write(*out)
}

type runner struct {
	st  settings
	ctx context.Context
	n   int
	// plan, for bench run -plan, counts and estimates the jobs instead of
	// running them.
	plan *planner
}

func (rn *runner) clients() []clients.Factory {
	var out []clients.Factory

	for _, name := range rn.st.Clients {
		f, err := clients.Named(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)

			continue
		}

		out = append(out, f)
	}

	return out
}

// loadPoint is one load point's outcome.
type loadPoint struct {
	Server string       `json:"server"`
	Proto  target.Proto `json:"proto"`
	Op     string       `json:"op"`
	Conc   int          `json:"conc"`
	Client string       `json:"client"`
	// Condition is the netem condition the point ran under, empty for
	// the network as it is.
	Condition string               `json:"condition,omitempty"`
	Result    *scenario.LoadResult `json:"result,omitempty"`
	NA        string               `json:"na,omitempty"`
	Error     string               `json:"error,omitempty"`
	// UDP is how the client container's UDP receive errors grew during
	// an HTTP/3 point.
	UDP *udpDelta `json:"udp,omitempty"`
	// Passes, in a report merged from passes (bench merge -passes), is
	// how many passes the point was valid in: Result holds the median of
	// each value over them and Ranges each value's lowest and highest, by
	// its JSON name.
	Passes int                   `json:"passes,omitempty"`
	Ranges map[string][2]float64 `json:"ranges,omitempty"`
}

func (rn *runner) load() []loadPoint {
	if len(rn.st.LoadNetem) == 0 {
		return rn.loadUnder(condition{})
	}

	// The plan only counts the jobs: no netem.
	var shaper *netem
	if rn.plan == nil {
		shaper = newNetem(rn.ctx, "eth0")
		defer shaper.restore(context.WithoutCancel(rn.ctx))
	}

	var out []loadPoint

	for _, name := range rn.st.LoadNetem {
		cond, _ := conditionNamed(name)
		if shaper != nil {
			if err := shaper.apply(rn.ctx, cond); err != nil {
				fmt.Fprintln(os.Stderr, "bench: netem:", err)

				continue
			}
		}

		out = append(out, rn.loadUnder(cond)...)
	}

	return out
}

// loadUnder runs the load points with the network under cond (already
// applied).
func (rn *runner) loadUnder(cond condition) []loadPoint {
	var out []loadPoint

	for _, srv := range rn.st.Servers {
		bodies, conc := site.Bodies, rn.st.Concurrency
		if slices.Contains(rn.st.Reduced, srv) {
			bodies = []int{site.KB, 100 * site.KB}
			conc = intersect(conc, []int{1, 64})
		}

		for _, p := range target.Protos {
			if !slices.Contains(rn.st.Protos, string(p)) {
				continue
			}

			for _, body := range bodies {
				if len(rn.st.Bodies) > 0 && !slices.Contains(rn.st.Bodies, scenario.SizeName(body)) {
					continue
				}

				op := scenario.GetBytes(body)

				for _, c := range conc {
					for _, f := range rn.clients() {
						if rn.ctx.Err() != nil {
							return out
						}

						j := job{
							Kind:        kindLoad,
							Client:      f.Name,
							Server:      srv,
							Proto:       p,
							Op:          op,
							Conc:        c,
							Warmup:      rn.st.Warmup,
							Window:      rn.st.Window,
							MaxWindow:   rn.st.MaxWindow,
							ProfileDir:  rn.st.ProfileDir,
							ProfileName: fmt.Sprintf("load-%s-%s-%s-c%d%s", srv, p, op.Name, c, cond.Name),
						}
						udp := readUDPErrors()
						// The warm-up stretches up to a minute, the window
						// to MaxWindow, the drain another.
						o := rn.do(j, cond, rn.st.Warmup+time.Minute+max(rn.st.Window, rn.st.MaxWindow)+3*time.Minute)

						lp := loadPoint{
							Server:    srv,
							Proto:     p,
							Op:        op.Name,
							Conc:      c,
							Client:    f.Name,
							Condition: cond.Name,
							Result:    o.Load,
							NA:        o.NA,
							Error:     o.Error,
						}

						msg := lp.summary()
						if cond.Netem {
							msg = cond.Name + ": " + msg
						}

						if p == target.H3 {
							d := readUDPErrors().since(udp)

							lp.UDP = &d

							msg += d.String()
						}

						rn.log(j, msg)

						out = append(out, lp)
					}
				}
			}
		}
	}

	return out
}

func (lp loadPoint) summary() string {
	switch {
	case lp.NA != "":
		return "n/a: " + lp.NA
	case lp.Error != "":
		return "error: " + lp.Error
	case lp.Result == nil:
		return "no result"
	case lp.Result.Discarded != "":
		return "discarded: " + lp.Result.Discarded
	}

	r := lp.Result

	s := fmt.Sprintf("%.0f req/s, %.1f MB/s, p50 %.2f ms, p99 %.2f ms, %.0f µs CPU/req, %.0f allocs/req, server CPU %s; "+
		"window %.1f s after %.1f s of warm-up, %d of %d requests started in it finished in it",
		r.RPS, r.MBps, r.P50, r.P99, r.CPUPerRequest, r.AllocsPerRequest, serverCell(r),
		r.Seconds, r.WarmupSeconds, r.Complete, r.Started)

	if r.ServerBound != "" {
		s += "; server-bound: " + r.ServerBound
	}

	if r.Capped != "" {
		s += "; " + r.Capped
	}

	return s
}

// idlePoint is one idle-memory measurement, with N clients.
type idlePoint struct {
	Server string               `json:"server"`
	Proto  target.Proto         `json:"proto"`
	Client string               `json:"client"`
	N      int                  `json:"n,omitempty"`
	Result *scenario.IdleResult `json:"result,omitempty"`
	NA     string               `json:"na,omitempty"`
	Error  string               `json:"error,omitempty"`
}

// idle runs layer 4 at each client count, clients interleaved within it.
func (rn *runner) idle() []idlePoint {
	var out []idlePoint

	srv := rn.st.Servers[0]

	for _, n := range rn.st.IdleNs {
		for _, p := range target.Protos {
			for _, f := range rn.clients() {
				if rn.ctx.Err() != nil {
					return out
				}

				j := job{
					Kind:        kindIdle,
					Client:      f.Name,
					Server:      srv,
					Proto:       p,
					N:           n,
					ProfileDir:  rn.st.ProfileDir,
					ProfileName: fmt.Sprintf("idle-%s-%s-n%d", srv, p, n),
				}
				o := rn.do(j, condition{}, 5*time.Minute)

				ip := idlePoint{
					Server: srv,
					Proto:  p,
					Client: f.Name,
					N:      n,
					Result: o.Idle,
					NA:     o.NA,
					Error:  o.Error,
				}

				msg := ip.NA + ip.Error
				if ip.Result != nil && msg == "" {
					msg = fmt.Sprintf("n=%d: %.1f KB heap, %.1f KB stack and %.1f goroutines per idle connection %s",
						n, ip.Result.HeapPerConnKB, ip.Result.StackPerConnKB, ip.Result.GoroutinesPerConn, ip.Result.Discarded)
				}

				rn.log(j, msg)

				out = append(out, ip)
			}
		}
	}

	return out
}

// pagePoint is one page-load setup under one network condition.
type pagePoint struct {
	Condition string               `json:"condition"`
	Proto     target.Proto         `json:"proto"`
	Proxy     target.ProxyKind     `json:"proxy,omitempty"`
	Client    string               `json:"client"`
	Result    *scenario.PageResult `json:"result,omitempty"`
	NA        string               `json:"na,omitempty"`
	Error     string               `json:"error,omitempty"`
}

// why says why a page-load setup does not count, or is empty.
func (pp pagePoint) why() string {
	switch {
	case pp.NA != "" || pp.Error != "":
		return pp.NA + pp.Error
	case pp.Result == nil:
		return "no result"
	case pp.Result.ProxyCheck != "":
		return "proxy check: " + pp.Result.ProxyCheck
	}

	return ""
}

// pageServer is the server for page loads: the Go server counts the
// connections each load opens.
const pageServer = "go"

// proxyCondition is the network condition for the proxy scenario.
const proxyCondition = "rtt20"

func (rn *runner) pages() ([]pagePoint, string) {
	var out []pagePoint

	conds := conditions[:1]
	note := "not used"

	var shaper *netem

	if rn.st.Netem {
		conds = conditions

		// The plan only counts the jobs: no netem.
		if rn.plan == nil {
			shaper = newNetem(rn.ctx, "eth0")

			defer func() {
				shaper.restore(context.WithoutCancel(rn.ctx))
			}()
		}
	}

	for _, cond := range conds {
		if shaper != nil {
			if err := shaper.apply(rn.ctx, cond); err != nil {
				fmt.Fprintln(os.Stderr, "bench: netem:", err)

				continue
			}
		}

		out = append(out, rn.pageSet(cond, "", rn.pageProtos(target.Protos))...)

		if cond.Name == proxyCondition || (!rn.st.Netem && cond.Name == "none") {
			for _, k := range target.ProxyKinds {
				out = append(out, rn.pageSet(cond, k, rn.pageProtos([]target.Proto{target.H1, target.H2}))...)
			}
		}
	}

	if shaper != nil {
		note = shaper.summary()
	}

	return out, note
}

// pageProtos is protos less those -protos leaves out.
func (rn *runner) pageProtos(protos []target.Proto) []target.Proto {
	var out []target.Proto

	for _, p := range protos {
		if slices.Contains(rn.st.Protos, string(p)) {
			out = append(out, p)
		}
	}

	return out
}

// pageSet loads the page for every client over each of protos, and again
// as a returning client where one has that mode, over HTTP/3 directly.
func (rn *runner) pageSet(cond condition, proxy target.ProxyKind, protos []target.Proto) []pagePoint {
	var out []pagePoint

	for _, p := range protos {
		for _, f := range rn.clients() {
			if rn.ctx.Err() != nil {
				return out
			}

			out = append(out, rn.pagePoint(cond, proxy, p, f, false))

			if f.Returning && p == target.H3 && proxy == "" {
				out = append(out, rn.pagePoint(cond, proxy, p, f, true))
			}
		}
	}

	return out
}

// pagePoint runs one page-load job; a returning client's is reported as
// "<name> (returning)".
func (rn *runner) pagePoint(cond condition, proxy target.ProxyKind, p target.Proto, f clients.Factory, returning bool) pagePoint {
	name, suffix := f.Name, ""
	if returning {
		name, suffix = f.Name+" (returning)", "-returning"
	}

	reps := rn.st.Reps
	if cond.Loss > 0 && rn.st.RepsLoss > 0 {
		reps = rn.st.RepsLoss
	}

	j := job{
		Kind:        kindPage,
		Client:      f.Name,
		Server:      pageServer,
		Proto:       p,
		Proxy:       proxy,
		Reps:        reps,
		Returning:   returning,
		Tag:         strconv.FormatInt(time.Now().UnixNano(), 36),
		ProfileDir:  rn.st.ProfileDir,
		ProfileName: fmt.Sprintf("page-%s-%s-%s%s", cond.Name, p, proxy, suffix),
	}
	o := rn.do(j, cond, 20*time.Minute)

	pp := pagePoint{
		Condition: cond.Name,
		Proto:     p,
		Proxy:     proxy,
		Client:    name,
		Result:    o.Page,
		NA:        o.NA,
		Error:     o.Error,
	}

	msg := pp.NA + pp.Error
	if pp.Result != nil && msg == "" {
		cold := pageStats(pp.Result.Cold)
		warm := pageStats(pp.Result.Warm)

		msg = fmt.Sprintf("cold %.0f ms (%s conns, %s), warm %.0f ms (%s conns, %s)",
			cold.medianMs, cold.conns, cold.protos, warm.medianMs, warm.conns, warm.protos)

		if pp.Result.ProxyCheck != "" {
			msg += "; proxy check failed: " + pp.Result.ProxyCheck
		}
	}

	if returning {
		msg = "returning, " + msg
	}

	rn.log(j, cond.Name+": "+msg)

	return pp
}

// do runs j, under cond (already applied), in a worker process of its own;
// for the plan, it only counts and estimates it.
func (rn *runner) do(j job, cond condition, timeout time.Duration) outcome {
	if rn.plan != nil {
		rn.plan.add(j, cond)

		return outcome{}
	}

	ctx, cancel := context.WithTimeout(rn.ctx, timeout)
	defer cancel()

	in, err := json.Marshal(j)
	if err != nil {
		return outcome{
			Error: err.Error(),
		}
	}

	self, err := os.Executable()
	if err != nil {
		return outcome{
			Error: err.Error(),
		}
	}

	var stdout bytes.Buffer

	cmd := exec.CommandContext(ctx, self, "worker") //nolint:gosec // this binary, run again as a worker

	cmd.Stdin = bytes.NewReader(in)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("worker timed out after %v: %w", timeout, err)
		}

		return outcome{
			Error: err.Error(),
		}
	}

	var o outcome
	if err := json.Unmarshal(stdout.Bytes(), &o); err != nil {
		return outcome{
			Error: "worker output: " + err.Error(),
		}
	}

	return o
}

func (rn *runner) log(j job, msg string) {
	if rn.plan != nil {
		return
	}

	rn.n++

	what := j.Kind + " " + j.Server + " " + string(j.Proto)
	if j.Proxy != "" {
		what += " via " + string(j.Proxy)
	}

	if j.Kind == kindLoad {
		what += fmt.Sprintf(" %s c=%d", j.Op.Name, j.Conc)
	}

	if j.Kind == kindMulti {
		what += fmt.Sprintf(" %s n=%d rotate=%d", j.Op.Name, j.N, j.Rotate)
		if j.Renew != "" {
			what += " clients " + string(j.Renew)
		}
	}

	fmt.Fprintf(os.Stderr, "%s [%d] %s %s: %s\n", time.Now().Format("15:04:05"), rn.n, what, j.Client, msg)
}

// udpErrors are the kernel's UDP receive error counters in this network
// namespace (/proc/net/snmp): datagrams dropped because a socket's receive
// buffer was full, and all receive errors. Zero where the file is missing.
type udpErrors struct {
	rcvbuf int64
	in     int64
}

func readUDPErrors() udpErrors {
	b, err := os.ReadFile("/proc/net/snmp")
	if err != nil {
		return udpErrors{}
	}

	var head []string

	for line := range strings.SplitSeq(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "Udp:" {
			continue
		}

		if head == nil {
			head = f

			continue
		}

		var u udpErrors

		for i, name := range head {
			if i >= len(f) {
				break
			}

			n, _ := strconv.ParseInt(f[i], 10, 64)

			switch name {
			case "RcvbufErrors":
				u.rcvbuf = n
			case "InErrors":
				u.in = n
			}
		}

		return u
	}

	return udpErrors{}
}

// udpDelta is how the client container's UDP receive error counters grew
// over a point (/proc/net/snmp): datagrams dropped on a full socket
// buffer, and all receive errors.
type udpDelta struct {
	RcvbufErrors int64 `json:"rcvbufErrors"`
	InErrors     int64 `json:"inErrors"`
}

// since is how the counters grew from before.
func (u udpErrors) since(before udpErrors) udpDelta {
	return udpDelta{
		RcvbufErrors: u.rcvbuf - before.rcvbuf,
		InErrors:     u.in - before.in,
	}
}

func (d udpDelta) String() string {
	return fmt.Sprintf(", UDP RcvbufErrors +%d InErrors +%d", d.RcvbufErrors, d.InErrors)
}

// env describes where the benchmark ran.
type env struct {
	Host          string       `json:"host"`
	HostCPU       string       `json:"hostCpu"`
	HostOS        string       `json:"hostOs"`
	Docker        string       `json:"docker"`
	Mode          string       `json:"mode"`
	Kernel        string       `json:"kernel"`
	ClientCPUs    int          `json:"clientCpus"`
	GOMAXPROCS    int          `json:"gomaxprocs"`
	GoVersion     string       `json:"goVersion"`
	MimicryCommit string       `json:"mimicryCommit"`
	Cpusets       string       `json:"cpusets"`
	Servers       []serverInfo `json:"servers"`
	Clients       []clientInfo `json:"clients"`
	Netem         string       `json:"netem"`
	ProxyDelay    string       `json:"proxyDelay"`
	// Power is the host's power source and Low Power Mode (macOS pmset).
	Power string `json:"power,omitempty"`
	// RmemMax and RmemDefault are the client container's
	// net.core.rmem_max and rmem_default: the most a UDP socket may ask
	// for (the quic-go clients ask for 7 MB) and what one gets without
	// asking (mimicry, UDPReadBufferSize 0). UDPStock says the run set
	// both to stock Linux's 212992 (BENCH_UDP_STOCK).
	RmemMax     string `json:"rmemMax,omitempty"`
	RmemDefault string `json:"rmemDefault,omitempty"`
	UDPStock    bool   `json:"udpStock,omitempty"`
	// NetAdmin says the client had CAP_NET_ADMIN (for netem), with which
	// quic-go and its forks force their 7 MB past rmem_max
	// (SO_RCVBUFFORCE); nil when unknown.
	NetAdmin *bool `json:"netAdmin,omitempty"`
}

// capNetAdmin reads whether this process has CAP_NET_ADMIN (bit 12 of
// CapEff in /proc/self/status); nil off Linux.
func capNetAdmin() *bool {
	for line := range strings.Lines(readFile("/proc/self/status")) {
		v, ok := strings.CutPrefix(line, "CapEff:")
		if !ok {
			continue
		}

		caps, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
		if err != nil {
			return nil
		}

		has := caps&(1<<12) != 0

		return &has
	}

	return nil
}

type serverInfo struct {
	Name     string `json:"name"`
	Software string `json:"software"`
	CPUs     int    `json:"cpus"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Module  string `json:"module"`
	Version string `json:"version"`
	Profile string `json:"profile"`
}

// environment describes this run's machine, servers and clients: every
// client, and the variants among selected.
func environment(ctx context.Context, selected []string) env {
	e := env{
		Host:          getenv("BENCH_HOST", "unknown"),
		HostCPU:       getenv("BENCH_HOST_CPU", "unknown"),
		HostOS:        getenv("BENCH_HOST_OS", "unknown"),
		Docker:        getenv("BENCH_DOCKER", "unknown"),
		Mode:          "local: both stacks on one Docker host, one Docker network",
		Kernel:        strings.TrimSpace(readFile("/proc/sys/kernel/osrelease")),
		ClientCPUs:    runtime.NumCPU(),
		GOMAXPROCS:    runtime.GOMAXPROCS(0),
		GoVersion:     runtime.Version(),
		MimicryCommit: getenv("BENCH_MIMICRY_COMMIT", "unknown"),
		Cpusets: fmt.Sprintf("client %s, servers %s, proxy %s",
			getenv("BENCH_CLIENT_CPUS", "?"), getenv("BENCH_SERVER_CPUS", "?"), getenv("BENCH_PROXY_CPUS", "?")),
		ProxyDelay:  getenv("BENCH_PROXY_DELAY", "unknown") + " on the proxy's outgoing packets",
		Power:       os.Getenv("BENCH_HOST_POWER"),
		RmemMax:     strings.TrimSpace(readFile("/proc/sys/net/core/rmem_max")),
		RmemDefault: strings.TrimSpace(readFile("/proc/sys/net/core/rmem_default")),
		UDPStock:    os.Getenv("BENCH_UDP_STOCK") != "",
		NetAdmin:    capNetAdmin(),
	}

	if s := os.Getenv("BENCH_SERVER"); s != "" {
		e.Mode = "remote: server stack on " + s + ", client here"
	}

	for _, s := range target.Servers {
		info := serverInfo{
			Name:     s.Name,
			Software: software(ctx, s),
		}

		if st, err := s.Stats(ctx); err == nil {
			info.CPUs = st.CPUs
		}

		e.Servers = append(e.Servers, info)
	}

	fs := clients.All()
	for _, f := range clients.Variants() {
		if slices.Contains(selected, f.Name) {
			fs = append(fs, f)
		}
	}

	for _, f := range fs {
		e.Clients = append(e.Clients, clientInfo{
			Name:    f.Name,
			Module:  f.Module,
			Version: f.Version(),
			Profile: f.Profile,
		})
	}

	return e
}

// software returns the server's Server header, or for the Go server what
// it is built from.
func software(ctx context.Context, s target.Server) string {
	if s.Name == "go" {
		return "net/http and quic-go " + clientsModuleVersion("github.com/quic-go/quic-go") + " (" + runtime.Version() + ")"
	}

	roots, err := target.RootCAs()
	if err != nil {
		return "unknown"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.URL(target.H2, "www", "/json"), nil)
	if err != nil {
		return "unknown"
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    roots,
			MinVersion: tls.VersionTLS12,
		},
	}
	defer tr.CloseIdleConnections()

	resp, err := tr.RoundTrip(req)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	return resp.Header.Get("Server")
}

func clientsModuleVersion(module string) string {
	f := clients.Factory{
		Module: module,
	}

	return f.Version()
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return def
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(b)
}

func ints(s string) []int {
	var out []int

	for f := range strings.SplitSeq(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err == nil {
			out = append(out, n)
		}
	}

	return out
}

func names(s string, def []string) []string {
	var out []string

	for f := range strings.SplitSeq(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}

	if len(out) == 0 {
		return def
	}

	return out
}

// subset is a comma-separated list of names; "all" (or none) is nil, which
// its flags read as every one.
func subset(s string) []string {
	if strings.TrimSpace(s) == "all" {
		return nil
	}

	return names(s, nil)
}

// check rejects settings that name a size, route or variant there is none
// of.
func (st settings) check() error {
	if err := checkSizes("-bodies", st.Bodies, site.Bodies); err != nil {
		return err
	}

	if err := checkSizes("-multi-proxy-bodies", st.MultiProxyBodies, multiBodies); err != nil {
		return err
	}

	for _, r := range st.MultiProxyRoutes {
		if len(routesNamed([]string{r})) == 0 {
			all := make([]string, 0, len(multiRoutes))
			for _, rt := range multiRoutes {
				all = append(all, rt.name())
			}

			return fmt.Errorf("-multi-proxy-routes: no route %q (%s)", r, strings.Join(all, ", "))
		}
	}

	for _, v := range st.MultiVariants {
		switch {
		case !slices.Contains(multiVariants, v):
			return fmt.Errorf("-multi-variants: no variant %q (%s)", v, strings.Join(multiVariants, ", "))
		case v != variantFixed && st.MultiRotate <= 0:
			return fmt.Errorf("-multi-variants %s needs -multi-rotate above 0", v)
		}
	}

	for _, v := range st.MultiN2Variants {
		if v != variantFixed && v != variantProxy {
			return fmt.Errorf("-multi-n2-variants: no variant %q (%s, %s)", v, variantFixed, variantProxy)
		}
	}

	return nil
}

// checkSizes rejects a size among given (scenario.SizeName) that sizes
// lacks; what is the flag that gave them.
func checkSizes(what string, given []string, sizes []int) error {
	all := make([]string, 0, len(sizes))
	for _, n := range sizes {
		all = append(all, scenario.SizeName(n))
	}

	for _, s := range given {
		if !slices.Contains(all, s) {
			return fmt.Errorf("%s: no size %q (%s)", what, s, strings.Join(all, ", "))
		}
	}

	return nil
}

func clientNames() []string {
	all := clients.All()
	out := make([]string, 0, len(all))

	for _, f := range all {
		out = append(out, f.Name)
	}

	return out
}

func intersect(a, b []int) []int {
	var out []int

	for _, v := range a {
		if slices.Contains(b, v) {
			out = append(out, v)
		}
	}

	return out
}

// write writes the report's JSON and Markdown to dir.
func (r *report) write(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	js, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}

	name := r.Name
	if name == "" {
		name = "bench"
	}

	base := filepath.Join(dir, name+"-"+r.Date)

	if err := os.WriteFile(base+".json", js, 0o644); err != nil { //nolint:gosec // a report, meant to be read
		return err
	}

	md := r.markdown()

	return errors.Join(os.WriteFile(base+".md", []byte(md), 0o644)) //nolint:gosec // a report, meant to be read
}

// conditionNamed is the netem condition called name.
func conditionNamed(name string) (condition, bool) {
	for _, c := range conditions {
		if c.Name == name {
			return c, true
		}
	}

	return condition{}, false
}
