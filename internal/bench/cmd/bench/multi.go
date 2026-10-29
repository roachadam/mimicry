package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
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

// name is the route as -multi-proxy-routes names it: h2-connect-auth.
func (rt multiRoute) name() string {
	return string(rt.Proto) + "-" + string(rt.Proxy)
}

// routesNamed are the routes among names, in report order; none named is
// all of them.
func routesNamed(names []string) []multiRoute {
	if len(names) == 0 {
		return multiRoutes
	}

	var out []multiRoute

	for _, rt := range multiRoutes {
		if slices.Contains(names, rt.name()) {
			out = append(out, rt)
		}
	}

	return out
}

// multiServer is layer 6's server.
const multiServer = "nginx"

// multiBodies are layer 6's response bodies.
var multiBodies = []int{
	site.KB,
	100 * site.KB,
}

// bodiesNamed are the sizes among bodies that names name (scenario.SizeName);
// none named is all of them.
func bodiesNamed(bodies []int, names []string) []int {
	if len(names) == 0 {
		return bodies
	}

	var out []int

	for _, b := range bodies {
		if slices.Contains(names, scenario.SizeName(b)) {
			out = append(out, b)
		}
	}

	return out
}

// proxyScope is the routes and sizes the proxy variant runs: h2-connect-auth
// at 1KB.
func proxyScope(st settings) string {
	rts := routesNamed(st.MultiProxyRoutes)
	routes := make([]string, 0, len(rts))

	for _, rt := range rts {
		routes = append(routes, rt.name())
	}

	bodies := bodiesNamed(multiBodies, st.MultiProxyBodies)
	sizes := make([]string, 0, len(bodies))

	for _, b := range bodies {
		sizes = append(sizes, scenario.SizeName(b))
	}

	return strings.Join(routes, ", ") + " at " + strings.Join(sizes, ", ")
}

// multiPoint is one multi-client point's outcome. Run labels the build it
// measured in a report that merges several (bench merge). N is its client
// count: the run's -multi-n, or -multi-n2 for the second count's points.
type multiPoint struct {
	Proto  target.Proto          `json:"proto"`
	Proxy  target.ProxyKind      `json:"proxy"`
	Op     string                `json:"op"`
	Rotate int                   `json:"rotate,omitempty"`
	Renew  scenario.Renew        `json:"renew,omitempty"`
	Run    string                `json:"run,omitempty"`
	N      int                   `json:"n,omitempty"`
	Client string                `json:"client"`
	Result *scenario.MultiResult `json:"result,omitempty"`
	NA     string                `json:"na,omitempty"`
	Error  string                `json:"error,omitempty"`
	// UDP is how the client container's UDP receive errors grew during an
	// HTTP/3 point; Passes and Ranges as loadPoint's.
	UDP    *udpDelta             `json:"udp,omitempty"`
	Passes int                   `json:"passes,omitempty"`
	Ranges map[string][2]float64 `json:"ranges,omitempty"`
}

func (mp multiPoint) valid() bool {
	return mp.NA == "" && mp.Error == "" && mp.Result != nil && mp.Result.Discarded == ""
}

// Layer 6's variants (-multi-variants).
const (
	// variantFixed: one identity per client.
	variantFixed = "fixed"
	// variantProxy: each client moves to a new identity every
	// -multi-rotate requests.
	variantProxy = "proxy"
	// variantClient: each routine replaces its client with a new one (a new
	// identity) every -multi-rotate requests, dropping the old one, then
	// closing it; 1 KB only, over -multi-client-window.
	variantClient = "client"
)

// multiVariants are the variants in report order.
var multiVariants = []string{
	variantFixed,
	variantProxy,
	variantClient,
}

// multiSetting is one run of its routes under a variant, each route and
// body in every renew mode ("" for the variants that keep their clients).
type multiSetting struct {
	variant string
	routes  []multiRoute
	rotate  int
	renews  []scenario.Renew
	bodies  []int
	window  time.Duration
	// n is the clients per point; 0 is -multi-n.
	n int
}

// keepClients is renews for the variants that keep their clients.
var keepClients = []scenario.Renew{
	"",
}

// multiSettings returns the selected variants' settings, in report order.
func (rn *runner) multiSettings() []multiSetting {
	var out []multiSetting

	st := rn.st

	for _, v := range st.MultiVariants {
		switch v {
		case variantFixed:
			out = append(out, multiSetting{
				variant: v,
				routes:  multiRoutes,
				renews:  keepClients,
				bodies:  multiBodies,
				window:  st.Window,
			})

		case variantProxy:
			out = append(out, multiSetting{
				variant: v,
				routes:  routesNamed(st.MultiProxyRoutes),
				rotate:  st.MultiRotate,
				renews:  keepClients,
				bodies:  bodiesNamed(multiBodies, st.MultiProxyBodies),
				window:  max(st.Window, st.MultiProxyWindow),
			})

		case variantClient:
			out = append(out, multiSetting{
				variant: v,
				routes:  multiRoutes,
				rotate:  st.MultiRotate,
				renews: []scenario.Renew{
					scenario.RenewDropped,
					scenario.RenewClosed,
				},
				bodies: []int{
					site.KB,
				},
				window: st.MultiClientWindow,
			})
		}
	}

	// The second client count: the 1 KB points of the variants
	// -multi-n2-variants names again, for memory per client as a slope
	// and an intercept.
	if st.MultiN2 > 0 {
		for _, ms := range slices.Clone(out) {
			if slices.Contains(st.n2Variants(), ms.variant) && slices.Contains(ms.bodies, site.KB) {
				ms.bodies = []int{
					site.KB,
				}
				ms.n = st.MultiN2
				out = append(out, ms)
			}
		}
	}

	return out
}

// multi runs layer 6: for each variant's settings, route and body, every
// client.
func (rn *runner) multi() []multiPoint {
	var out []multiPoint

	for _, ms := range rn.multiSettings() {
		for _, rt := range ms.routes {
			if !slices.Contains(rn.st.Protos, string(rt.Proto)) {
				continue
			}

			for _, body := range ms.bodies {
				op := scenario.GetBytes(body)

				for _, renew := range ms.renews {
					for _, f := range rn.clients() {
						if rn.ctx.Err() != nil {
							return out
						}

						n := rn.st.MultiN
						if ms.n > 0 {
							n = ms.n
						}

						j := job{
							Kind:   kindMulti,
							Client: f.Name,
							Server: multiServer,
							Proto:  rt.Proto,
							Proxy:  rt.Proxy,
							Op:     op,
							N:      n,
							Warmup: rn.st.Warmup,
							Window: ms.window,
							Rotate: ms.rotate,
							Renew:  renew,
							// A new mimicry client knows nothing of HTTP/3: the
							// routines' clients share an Alt-Svc cache, as a
							// caller who wants HTTP/3 from each new one does.
							Returning:   renew != "" && rt.Proto == target.H3 && f.Returning,
							Tag:         strconv.FormatInt(time.Now().UnixNano(), 36),
							ProfileDir:  rn.st.ProfileDir,
							ProfileName: fmt.Sprintf("multi-%s-%s-%s-r%d%s", rt.Proto, rt.Proxy, op.Name, ms.rotate, renew),
						}

						mp := multiPoint{
							Proto:  rt.Proto,
							Proxy:  rt.Proxy,
							Op:     op.Name,
							Rotate: ms.rotate,
							Renew:  renew,
							N:      n,
							Client: f.Name,
						}

						udp := readUDPErrors()

						if rt.Proto == target.H3 && os.Getenv("BENCH_SERVER") != "" {
							mp.NA = "HTTP/3 through SOCKS5 needs the proxy's UDP relay ports, which a remote server stack does not publish"
						} else {
							o := rn.do(j, condition{}, rn.st.Warmup+ms.window+5*time.Minute)

							mp.Result, mp.NA, mp.Error = o.Multi, o.NA, o.Error
						}

						msg := mp.summary()
						if rt.Proto == target.H3 {
							d := readUDPErrors().since(udp)

							mp.UDP = &d

							msg += d.String()
						}

						rn.log(j, msg)

						out = append(out, mp)
					}
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
		"%.1f goroutines/client, %d errors, %d on another protocol, %d stuck, proxy CPU %s, server CPU %s",
		r.RPS, r.P50, r.P99, r.CPUPerRequest, r.AllocsPerRequest, r.LiveKBPerClient, r.PeakKBPerClient,
		r.GoroutinesPerClient, r.Errors, r.Mismatches, r.Stuck, pct(r.ProxyCPU), pct(r.ServerCPU))

	if mp.Rotate > 0 {
		s += fmt.Sprintf("; %.0f new identities/s, %.0f tunnels/s, %.0f associations/s, new-connection p50 %.2f ms p99 %.2f ms, %d of %d resumed",
			r.RotationsPerSec, r.TunnelsPerSec, r.AssociationsPerSec, r.NewP50, r.NewP99, r.NewResumed, r.NewConns)
	}

	if p := r.Plateau; p != nil {
		s += fmt.Sprintf("; plateau: live %.0f KB/client, %.1f goroutines/client, %.2f open/client, slope %+.2f KB/client/s over the second half",
			p.LiveKBPerClient, p.GoroutinesPerClient, p.OpenPerClient, p.SlopeKBPerClient)
	}

	if mp.Renew != "" {
		s += fmt.Sprintf("; constructor p50 %.3f ms p99 %.3f ms", r.BuildP50, r.BuildP99)

		if c := r.Cost; c != nil {
			s += fmt.Sprintf("; alone: construct %.0f µs, first request %.2f ms, %.0f KB, %.0f allocs, %d of %d on the protocol",
				c.ConstructUs, c.FirstMs, c.KB, c.Allocs, c.OnProto, c.Clients)
		}

		if e := r.End; e != nil {
			s += fmt.Sprintf("; end: live %.0f KB/routine, %.1f goroutines/routine, RSS %.0f MB, %d tunnels and %d associations open",
				e.LiveKBPerRoutine, e.GoroutinesPerRoutine, e.RSSMB, e.OpenTunnels, e.OpenAssociations)
		}

		if g := r.Growth; g != nil {
			s += fmt.Sprintf("; growth/s: %.1f KB/routine, %.2f goroutines/routine, %.1f MB RSS, %.0f open",
				g.LiveKBPerRoutine, g.GoroutinesPerRoutine, g.RSSMB, g.Open)
		}
	}

	return s
}
