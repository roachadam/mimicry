package main

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/roachadam/mimicry"
)

// site is a public origin that serves HTTP/3. Every request is an
// ordinary GET of a public page or asset.
type site struct {
	host string
	// home is the first request's path; large, when set, a larger
	// download on the same origin.
	home  string
	large string
}

var sites = []site{
	{
		host: "www.google.com",
		home: "/",
	},
	{
		host: "www.youtube.com",
		home: "/",
	},
	{
		host: "cloudflare-quic.com",
		home: "/",
	},
	{
		host: "www.cloudflare.com",
		home: "/",
	},
	{
		host: "www.facebook.com",
		home: "/",
	},
	{
		host: "www.instagram.com",
		home: "/",
	},
	{
		host: "quic.nginx.org",
		home: "/",
	},
	{
		host: "www.litespeedtech.com",
		home: "/",
	},
	{
		host: "www.fastly.com",
		home: "/",
	},
	{
		host: "quic.tech",
		home: "/",
	},
	{
		host:  "cdn.jsdelivr.net",
		home:  "/npm/three@0.160.0/package.json",
		large: "/npm/three@0.160.0/build/three.module.js",
	},
	{
		host: "discord.com",
		home: "/",
	},
	{
		host: "www.whatsapp.com",
		home: "/",
	},
	{
		host: "www.akamai.com",
		home: "/",
	},
	{
		host: "quic.aiortc.org",
		home: "/",
	},
	{
		host: "nghttp2.org",
		home: "/",
	},
	{
		host: "interop.seemann.io",
		home: "/",
	},
	{
		host: "http3check.net",
		home: "/",
	},
}

// runSite runs the engine flow against st, then probes it directly for
// what the engine hides: a fresh connection and a 0-RTT resumption where
// the profile resumes.
func runSite(ctx context.Context, st site, np namedProfile, out string, seed uint64) (r result) {
	dir := filepath.Join(out, "public", st.host, np.name)
	_ = os.MkdirAll(dir, 0o750)
	r = result{
		Target:  st.host,
		Case:    "site",
		Profile: np.name,
		Outcome: pass,
		Dir:     dir,
	}
	start := time.Now()
	defer func() {
		r.MS = ms(time.Since(start))
	}()
	origin := "https://" + st.host
	large := ""
	if st.large != "" {
		large = origin + st.large
	}
	steps, sum := engineFlow(ctx, mimicry.Config{
		Profile:     np.p,
		DialTimeout: 15 * time.Second,
	}, origin+st.home, large)
	r.Engine, r.EngineSummary = steps, sum
	for _, s := range steps {
		if s.Err != "" {
			r.failf("engine %s: %s", s.Step, s.Err)
		}
	}
	port := h3Port(sum.AltSvc)
	if port == 0 {
		r.Outcome, r.Note = na, "no Alt-Svc h3 on the first response"
		return r
	}
	if !sum.H3 {
		r.failf("engine: no request went over HTTP/3")
	}
	probeSite(ctx, st, np, dir, seed, port, &r)

	return r
}

// probeSite opens the two direct connections.
func probeSite(ctx context.Context, st site, np namedProfile, dir string, seed uint64, port uint16, r *result) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", st.host)
	if err != nil || len(addrs) == 0 {
		r.failf("probe: lookup: %v", err)
		return
	}
	peer := netip.AddrPortFrom(addrs[0].Unmap(), port)
	pr := newProber(np.p, nil, dir, seed)
	paths := []string{st.home}
	if st.large != "" {
		paths = append(paths, st.large)
	}
	for i := range 2 {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		c, err := pr.dial(cctx, st.host, peer, impairment{})
		if err != nil {
			if c != nil {
				r.Conns = append(r.Conns, c.facts())
			}
			r.failf("probe connection %d: %s", i+1, errString(err))
			pr.close(c)
			cancel()

			return
		}
		fs := getAll(cctx, pr, c, paths, false)
		if i == 0 {
			pr.waitTicket(cctx, 2*time.Second)
		}
		r.Conns = append(r.Conns, c.facts())
		pr.close(c)
		cancel()
		r.Fetches = append(r.Fetches, fs...)
		for _, f := range fs {
			if f.Err != "" {
				r.failf("probe connection %d: GET %s: %s", i+1, f.Path, f.Err)
			}
		}
		paths = []string{st.home}
	}
	second := r.Conns[len(r.Conns)-1]
	switch {
	case !pr.resumes():
		r.Note = "the profile never resumes QUIC"
	case !second.OfferedPSK:
		r.Note = "no session ticket from the first connection"
	case !second.Resumed:
		r.Note = "the second connection did not resume"
	case second.EarlyData:
		r.Note = "0-RTT accepted"
	default:
		r.Note = "0-RTT rejected"
	}
}
