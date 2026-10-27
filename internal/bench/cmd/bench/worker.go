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
	kindLoad = "load"
	kindIdle = "idle"
	kindPage = "page"
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
	N      int              `json:"n,omitempty"`
	Reps   int              `json:"reps,omitempty"`
	// ProfileDir, for mimicry's jobs, receives ProfileName.cpu.pprof (the
	// measurement window, or the whole job) and ProfileName.mem.pprof.
	ProfileDir  string `json:"profileDir,omitempty"`
	ProfileName string `json:"profileName,omitempty"`
}

// outcome is a worker's answer.
type outcome struct {
	Load  *scenario.LoadResult `json:"load,omitempty"`
	Idle  *scenario.IdleResult `json:"idle,omitempty"`
	Page  *scenario.PageResult `json:"page,omitempty"`
	NA    string               `json:"na,omitempty"`
	Error string               `json:"error,omitempty"`
}

func worker(in io.Reader, out io.Writer) error {
	var j job
	if err := json.NewDecoder(in).Decode(&j); err != nil {
		return err
	}

	o := runJob(j)

	return json.NewEncoder(out).Encode(o)
}

func runJob(j job) outcome {
	f, err := clients.Named(j.Client)
	if err != nil {
		return outcome{
			Error: err.Error(),
		}
	}

	srv, err := target.ServerNamed(j.Server)
	if err != nil {
		return outcome{
			Error: err.Error(),
		}
	}

	su := scenario.Setup{
		Client: f,
		Server: srv,
		Proto:  j.Proto,
		Proxy:  j.Proxy,
	}
	if err := su.Supported(); err != nil {
		return outcome{
			NA: err.Error(),
		}
	}

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

// profiler writes a CPU profile of the window it is given and a heap
// profile at its end, when dir is set.
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

		return
	}

	if p.cpu != nil {
		pprof.StopCPUProfile()

		p.err = errors.Join(p.err, p.cpu.Close())
		p.cpu = nil
	}

	f, err := os.Create(filepath.Join(p.dir, p.name+".mem.pprof"))
	if err != nil {
		p.err = errors.Join(p.err, err)

		return
	}

	runtime.GC()

	p.err = errors.Join(p.err, pprof.Lookup("allocs").WriteTo(f, 0), f.Close())
}
