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
	Window      time.Duration `json:"window"`
	Warmup      time.Duration `json:"warmup"`
	Layers      []int         `json:"layers"`
	Clients     []string      `json:"clients"`
	Servers     []string      `json:"servers"`
	Reduced     []string      `json:"reduced"`
	Protos      []string      `json:"protos"`
	Concurrency []int         `json:"concurrency"`
	IdleN       int           `json:"idleN"`
	Reps        int           `json:"reps"`
	Netem       bool          `json:"netem"`
	// LoadNetem are the network conditions layer 3 runs under (none: the
	// network as it is), Bodies the response sizes it runs (none: all).
	LoadNetem  []string `json:"loadNetem,omitempty"`
	Bodies     []string `json:"bodies,omitempty"`
	ProfileDir string   `json:"profileDir,omitempty"`
	// MultiN is layer 6's clients (routines) per point, MultiVariants its
	// variants, MultiRotate the requests per proxy identity (or client)
	// in the rotating ones, MultiClientWindow the client-rotation
	// variant's window.
	MultiN            int           `json:"multiN,omitempty"`
	MultiVariants     []string      `json:"multiVariants,omitempty"`
	MultiRotate       int           `json:"multiRotate,omitempty"`
	MultiClientWindow time.Duration `json:"multiClientWindow,omitempty"`
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
	window := fs.Duration("window", 10*time.Second, "measurement window per load point")
	warmup := fs.Duration("warmup", 3*time.Second, "warm-up per load point, at full concurrency")
	layers := fs.String("layers", "3,4,5,6", "layers to run (3 load, 4 memory, 5 network, 6 many clients through proxies)")
	clientList := fs.String("clients", "", "clients to run (comma-separated; default all)")
	serverList := fs.String("servers", "nginx,go", "servers for layers 3 and 4")
	reduced := fs.String("reduced", "go", "servers that get the reduced load matrix (bodies 1KB and 100KB, concurrency 1 and 64)")
	protoList := fs.String("protos", "h1,h2,h3", "protocols for layers 3 and 5")
	concList := fs.String("conc", "1,16,64,256", "concurrency levels")
	idleN := fs.Int("idle-n", 50, "clients per idle-memory measurement")
	reps := fs.Int("reps", 5, "page loads per setup and network condition")
	useNetem := fs.Bool("netem", true, "shape the client's network for layer 5 (off: the network as it is, e.g. a remote server over Ethernet)")
	loadNetem := fs.String("load-netem", "", "layer 3: network conditions to run the load points under (e.g. rtt80-loss1; empty: the network as it is)")
	bodyList := fs.String("bodies", "", "layer 3: response sizes to run (e.g. 100KB,10MB; empty: all)")
	multiN := fs.Int("multi-n", 200, "layer 6: clients per point, each through a proxy identity of its own")
	multiVariantList := fs.String("multi-variants", strings.Join(multiVariants, ","),
		"layer 6: variants (fixed: one identity per client; proxy: a client moves to a new identity; client: a routine replaces its client, dropped and then closed)")
	multiRotate := fs.Int("multi-rotate", 10, "layer 6: requests per proxy identity (proxy variant) or per client (client variant)")
	multiClientWindow := fs.Duration("multi-client-window", 20*time.Second, "layer 6: the client variant's measurement window, long enough for growth to show")
	micro := fs.String("micro", "", "benchstat output of the micro benchmarks (layers 1 and 2) to embed")
	microRaw := fs.String("micro-raw", "", "the micro benchmarks' raw output, named in the report")
	profileDir := fs.String("profile", "", "write CPU and allocation profiles of mimicry's jobs here")
	date := fs.String("date", time.Now().Format("2006-01-02"), "the report's date")
	name := fs.String("name", "bench", "the report's file name before the date: <name>-<date>.md and .json")

	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st := settings{
		Window:            *window,
		Warmup:            *warmup,
		Layers:            ints(*layers),
		Clients:           names(*clientList, clientNames()),
		Servers:           names(*serverList, nil),
		Reduced:           names(*reduced, nil),
		Protos:            names(*protoList, nil),
		Concurrency:       ints(*concList),
		IdleN:             *idleN,
		Reps:              *reps,
		Netem:             *useNetem,
		LoadNetem:         names(*loadNetem, nil),
		Bodies:            names(*bodyList, nil),
		ProfileDir:        *profileDir,
		MultiN:            *multiN,
		MultiVariants:     names(*multiVariantList, multiVariants),
		MultiRotate:       *multiRotate,
		MultiClientWindow: *multiClientWindow,
	}

	for _, c := range st.LoadNetem {
		if _, ok := conditionNamed(c); !ok {
			return fmt.Errorf("-load-netem: no condition %q", c)
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
}

func (rn *runner) load() []loadPoint {
	if len(rn.st.LoadNetem) == 0 {
		return rn.loadUnder(condition{})
	}

	shaper := newNetem(rn.ctx, "eth0")
	defer shaper.restore(context.WithoutCancel(rn.ctx))

	var out []loadPoint

	for _, name := range rn.st.LoadNetem {
		cond, _ := conditionNamed(name)
		if err := shaper.apply(rn.ctx, cond); err != nil {
			fmt.Fprintln(os.Stderr, "bench: netem:", err)

			continue
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
							ProfileDir:  rn.st.ProfileDir,
							ProfileName: fmt.Sprintf("load-%s-%s-%s-c%d%s", srv, p, op.Name, c, cond.Name),
						}
						udp := readUDPErrors()
						o := rn.do(j, rn.st.Warmup+rn.st.Window+3*time.Minute)

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
							msg += readUDPErrors().since(udp)
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

	return fmt.Sprintf("%.0f req/s, %.1f MB/s, p50 %.2f ms, p99 %.2f ms, %.0f µs CPU/req, %.0f allocs/req, server CPU %.0f%%",
		r.RPS, r.MBps, r.P50, r.P99, r.CPUPerRequest, r.AllocsPerRequest, 100*r.ServerCPU)
}

// idlePoint is one idle-memory measurement.
type idlePoint struct {
	Server string               `json:"server"`
	Proto  target.Proto         `json:"proto"`
	Client string               `json:"client"`
	Result *scenario.IdleResult `json:"result,omitempty"`
	NA     string               `json:"na,omitempty"`
	Error  string               `json:"error,omitempty"`
}

func (rn *runner) idle() []idlePoint {
	var out []idlePoint

	srv := rn.st.Servers[0]

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
				N:           rn.st.IdleN,
				ProfileDir:  rn.st.ProfileDir,
				ProfileName: fmt.Sprintf("idle-%s-%s", srv, p),
			}
			o := rn.do(j, 5*time.Minute)

			ip := idlePoint{
				Server: srv,
				Proto:  p,
				Client: f.Name,
				Result: o.Idle,
				NA:     o.NA,
				Error:  o.Error,
			}

			msg := ip.NA + ip.Error
			if ip.Result != nil && msg == "" {
				msg = fmt.Sprintf("%.1f KB heap, %.1f KB stack and %.1f goroutines per idle connection %s",
					ip.Result.HeapPerConnKB, ip.Result.StackPerConnKB, ip.Result.GoroutinesPerConn, ip.Result.Discarded)
			}

			rn.log(j, msg)

			out = append(out, ip)
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
		shaper = newNetem(rn.ctx, "eth0")

		defer func() {
			shaper.restore(context.WithoutCancel(rn.ctx))
		}()
	}

	for _, cond := range conds {
		if shaper != nil {
			if err := shaper.apply(rn.ctx, cond); err != nil {
				fmt.Fprintln(os.Stderr, "bench: netem:", err)

				continue
			}
		}

		out = append(out, rn.pageSet(cond, "", rn.pageProtos(target.Protos))...)

		if cond.Name == proxyCondition || (shaper == nil && cond.Name == "none") {
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

	j := job{
		Kind:        kindPage,
		Client:      f.Name,
		Server:      pageServer,
		Proto:       p,
		Proxy:       proxy,
		Reps:        rn.st.Reps,
		Returning:   returning,
		ProfileDir:  rn.st.ProfileDir,
		ProfileName: fmt.Sprintf("page-%s-%s-%s%s", cond.Name, p, proxy, suffix),
	}
	o := rn.do(j, 10*time.Minute)

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
	}

	if returning {
		msg = "returning, " + msg
	}

	rn.log(j, cond.Name+": "+msg)

	return pp
}

// do runs j in a worker process of its own.
func (rn *runner) do(j job, timeout time.Duration) outcome {
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

// since says how the counters grew from before.
func (u udpErrors) since(before udpErrors) string {
	return fmt.Sprintf(", UDP RcvbufErrors +%d InErrors +%d", u.rcvbuf-before.rcvbuf, u.in-before.in)
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
		ProxyDelay: getenv("BENCH_PROXY_DELAY", "unknown") + " on the proxy's outgoing packets",
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
