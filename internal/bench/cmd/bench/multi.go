package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/roachadam/mimicry/internal/bench/scenario"
	"github.com/roachadam/mimicry/internal/bench/site"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// multiRoute is how layer 6's clients reach the server: a protocol through
// a proxy kind that carries credentials.
type multiRoute struct {
	Proto target.Proto
	Proxy target.ProxyKind
}

// multiRoutes are layer 6's routes, in report order: HTTP/2 and HTTP/1.1
// through CONNECT with Basic credentials, HTTP/2 and HTTP/3 through SOCKS5
// (socks5h, with username and password).
var multiRoutes = []multiRoute{
	{
		Proto: target.H2,
		Proxy: target.ProxyConnectAuth,
	},
	{
		Proto: target.H1,
		Proxy: target.ProxyConnectAuth,
	},
	{
		Proto: target.H2,
		Proxy: target.ProxySOCKS5,
	},
	{
		Proto: target.H3,
		Proxy: target.ProxySOCKS5,
	},
}

// multiServer is layer 6's server.
const multiServer = "nginx"

// multiBodies are layer 6's response bodies.
var multiBodies = []int{
	site.KB,
	100 * site.KB,
}

// multiPoint is one multi-client point's outcome.
type multiPoint struct {
	Proto  target.Proto          `json:"proto"`
	Proxy  target.ProxyKind      `json:"proxy"`
	Op     string                `json:"op"`
	Rotate int                   `json:"rotate,omitempty"`
	Client string                `json:"client"`
	Result *scenario.MultiResult `json:"result,omitempty"`
	NA     string                `json:"na,omitempty"`
	Error  string                `json:"error,omitempty"`
}

func (mp multiPoint) valid() bool {
	return mp.NA == "" && mp.Error == "" && mp.Result != nil && mp.Result.Discarded == ""
}

// multi runs layer 6: for each rotation setting (none, then every
// -multi-rotate requests), route and body, every client.
func (rn *runner) multi() []multiPoint {
	var out []multiPoint

	rotations := []int{0}
	if rn.st.MultiRotate > 0 {
		rotations = append(rotations, rn.st.MultiRotate)
	}

	for _, rotate := range rotations {
		for _, rt := range multiRoutes {
			if !slices.Contains(rn.st.Protos, string(rt.Proto)) {
				continue
			}

			for _, body := range multiBodies {
				op := scenario.GetBytes(body)

				for _, f := range rn.clients() {
					if rn.ctx.Err() != nil {
						return out
					}

					j := job{
						Kind:        kindMulti,
						Client:      f.Name,
						Server:      multiServer,
						Proto:       rt.Proto,
						Proxy:       rt.Proxy,
						Op:          op,
						N:           rn.st.MultiN,
						Warmup:      rn.st.Warmup,
						Window:      rn.st.Window,
						Rotate:      rotate,
						Tag:         strconv.FormatInt(time.Now().UnixNano(), 36),
						ProfileDir:  rn.st.ProfileDir,
						ProfileName: fmt.Sprintf("multi-%s-%s-%s-r%d", rt.Proto, rt.Proxy, op.Name, rotate),
					}

					mp := multiPoint{
						Proto:  rt.Proto,
						Proxy:  rt.Proxy,
						Op:     op.Name,
						Rotate: rotate,
						Client: f.Name,
					}

					udp := readUDPErrors()

					if rt.Proto == target.H3 && os.Getenv("BENCH_SERVER") != "" {
						mp.NA = "HTTP/3 through SOCKS5 needs the proxy's UDP relay ports, which a remote server stack does not publish"
					} else {
						o := rn.do(j, rn.st.Warmup+rn.st.Window+5*time.Minute)

						mp.Result, mp.NA, mp.Error = o.Multi, o.NA, o.Error
					}

					msg := mp.summary()
					if rt.Proto == target.H3 {
						msg += readUDPErrors().since(udp)
					}

					rn.log(j, msg)

					out = append(out, mp)
				}
			}
		}
	}

	return out
}

func (mp multiPoint) summary() string {
	switch {
	case mp.NA != "":
		return "n/a: " + mp.NA
	case mp.Error != "":
		return "error: " + mp.Error
	case mp.Result == nil:
		return "no result"
	case mp.Result.Discarded != "":
		return "discarded: " + mp.Result.Discarded
	}

	r := mp.Result

	s := fmt.Sprintf("%.0f req/s, p50 %.2f ms, p99 %.2f ms, %.0f µs CPU/req, %.0f allocs/req, live %.0f KB/client, peak %.0f KB/client, "+
		"%.1f goroutines/client, %d errors, proxy CPU %s, server CPU %s",
		r.RPS, r.P50, r.P99, r.CPUPerRequest, r.AllocsPerRequest, r.LiveKBPerClient, r.PeakKBPerClient,
		r.GoroutinesPerClient, r.Errors, pct(r.ProxyCPU), pct(r.ServerCPU))

	if mp.Rotate > 0 {
		s += fmt.Sprintf("; %.0f new identities/s, %.0f tunnels/s, %.0f associations/s, new-connection p50 %.2f ms p99 %.2f ms, %d of %d resumed",
			r.RotationsPerSec, r.TunnelsPerSec, r.AssociationsPerSec, r.NewP50, r.NewP99, r.NewResumed, r.NewConns)
	}

	return s
}
