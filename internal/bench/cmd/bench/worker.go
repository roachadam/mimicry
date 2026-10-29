package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/roachadam/mimicry/internal/bench/clients"
	"github.com/roachadam/mimicry/internal/bench/scenario"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// Job kinds.
const (
	kindLoad  = "load"
	kindIdle  = "idle"
	kindPage  = "page"
	kindMulti = "multi"
)

// job is one worker's measurement.
type job struct {
	Kind   string           `json:"kind"`
	Client string           `json:"client"`
	Server string           `json:"server"`
	Proto  target.Proto     `json:"proto"`
	Proxy  target.ProxyKind `json:"proxy,omitempty"`
	Op     scenario.Op      `json:"op"`
	Conc   int              `json:"conc,omitempty"`
	Warmup time.Duration    `json:"warmup,omitempty"`
	Window time.Duration    `json:"window,omitempty"`
	// MaxWindow caps a load job's stretched window (scenario.LoadSpec).
	MaxWindow time.Duration `json:"maxWindow,omitempty"`
	N         int           `json:"n,omitempty"`
	Reps      int           `json:"reps,omitempty"`
	// Rotate: a multi-client job's requests per proxy identity (0: one
	// identity per client), Renew how it rotates clients instead of
	// identities ("": it does not); Tag makes its proxy users unique.
	Rotate int            `json:"rotate,omitempty"`
	Renew  scenario.Renew `json:"renew,omitempty"`
	Tag    string         `json:"tag,omitempty"`
	// Returning: page loads by returning clients (scenario.Setup).
	Returning bool `json:"returning,omitempty"`
	// ProfileDir, for mimicry's jobs, receives ProfileName.cpu.pprof (the
	// measurement window, or the whole job) and ProfileName.mem.pprof.
	ProfileDir  string `json:"profileDir,omitempty"`
	ProfileName string `json:"profileName,omitempty"`
}

// outcome is a worker's answer.
type outcome struct {
	Load  *scenario.LoadResult  `json:"load,omitempty"`
	Idle  *scenario.IdleResult  `json:"idle,omitempty"`
	Page  *scenario.PageResult  `json:"page,omitempty"`
	Multi *scenario.MultiResult `json:"multi,omitempty"`
	NA    string                `json:"na,omitempty"`
	Error string                `json:"error,omitempty"`
}

func worker(in io.Reader, out io.Writer) error {
	var j job
	if err := json.NewDecoder(in).Decode(&j); err != nil {
		return err
	}

	o := runJob(j)

	return json.NewEncoder(out).Encode(o)
}

// setup is j's client, server, protocol and proxy; the error names a
// client or server there is none of.
func (j job) setup() (scenario.Setup, error) {
	f, err := clients.Named(j.Client)
	if err != nil {
		return scenario.Setup{}, err
	}

	srv, err := target.ServerNamed(j.Server)
	if err != nil {
		return scenario.Setup{}, err
	}

	su := scenario.Setup{
		Client:    f,
		Server:    srv,
		Proto:     j.Proto,
		Proxy:     j.Proxy,
		Returning: j.Returning,
	}

	if j.Kind == kindPage && j.Tag != "" {
		// Its own user (connect-auth), so the proxy check counts its
		// tunnels alone.
		su.ProxyUser = target.ProxyUser + "-page-" + j.Tag
	}

	return su, nil
}

func runJob(j job) outcome {
	su, err := j.setup()
	if err != nil {
		return outcome{
			Error: err.Error(),
		}
	}

	if err := su.Supported(); err != nil {
		return outcome{
			NA: err.Error(),
		}
	}

	f := su.Client

	prof := profiler{
		dir:  "",
		name: j.ProfileName,
	}
	if j.ProfileDir != "" && f.Name == "mimicry" {
		prof.dir = j.ProfileDir
	}

	ctx := context.Background()

	var o outcome

	switch j.Kind {
	case kindLoad:
		var r scenario.LoadResult

		r, err = scenario.Load(ctx, su, scenario.LoadSpec{
			Op:          j.Op,
			Concurrency: j.Conc,
			Warmup:      j.Warmup,
			Duration:    j.Window,
			MaxWindow:   j.MaxWindow,
			OnWindow:    prof.window,
		})
		o.Load = &r

	case kindIdle:
		var r scenario.IdleResult

		prof.window(true)

		r, err = scenario.Idle(ctx, su, j.N)

		prof.window(false)

		o.Idle = &r

	case kindPage:
		var r scenario.PageResult

		prof.window(true)

		r, err = scenario.Pages(ctx, su, j.Reps)

		prof.window(false)

		o.Page = &r

	case kindMulti:
		var r scenario.MultiResult

		r, err = scenario.Multi(ctx, su, scenario.MultiSpec{
			Op:       j.Op,
			Clients:  j.N,
			Warmup:   j.Warmup,
			Duration: j.Window,
			Rotate:   j.Rotate,
			Renew:    j.Renew,
			Tag:      j.Tag,
			OnWindow: prof.window,
		})
		o.Multi = &r

	default:
		err = fmt.Errorf("unknown job kind %q", j.Kind)
	}

	if err == nil {
		err = prof.err
	}

	if err != nil {
		o.Error = err.Error()
	}

	return o
}

// profiler writes a CPU profile of the window it is given, and mutex,
// block and allocation profiles at its end, when dir is set. Mutex and
// block profiling are sampled (profileMutexFraction, profileBlockRate) so
// they cost the measurement little.
type profiler struct {
	dir  string
	name string
	cpu  *os.File
	err  error
}

func (p *profiler) window(open bool) {
	if p.dir == "" {
		return
	}

	if open {
		f, err := os.Create(filepath.Join(p.dir, p.name+".cpu.pprof"))
		if err != nil {
			p.err = err

			return
		}

		if err := pprof.StartCPUProfile(f); err != nil {
			p.err = errors.Join(err, f.Close())

			return
		}

		p.cpu = f

		runtime.SetMutexProfileFraction(profileMutexFraction)
		runtime.SetBlockProfileRate(profileBlockRate)

		return
	}

	if p.cpu != nil {
		pprof.StopCPUProfile()

		p.err = errors.Join(p.err, p.cpu.Close())
		p.cpu = nil
	}

	runtime.SetMutexProfileFraction(0)
	runtime.SetBlockProfileRate(0)

	for _, kind := range []string{"mutex", "block"} {
		p.err = errors.Join(p.err, p.write(kind))
	}

	runtime.GC()

	p.err = errors.Join(p.err, p.write("allocs"))
}

// write writes the named runtime profile to <name>.<kind>.pprof ("allocs"
// goes to .mem.pprof).
func (p *profiler) write(kind string) error {
	suffix := kind
	if kind == "allocs" {
		suffix = "mem"
	}

	f, err := os.Create(filepath.Join(p.dir, p.name+"."+suffix+".pprof"))
	if err != nil {
		return err
	}

	return errors.Join(pprof.Lookup(kind).WriteTo(f, 0), f.Close())
}

const (
	// profileMutexFraction samples one contention event in this many.
	profileMutexFraction = 10
	// profileBlockRate samples blocking events of about this many
	// nanoseconds or more; shorter ones proportionally.
	profileBlockRate = 10_000
)
