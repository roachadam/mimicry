// Command interop runs mimicry's QUIC and HTTP/3, as each built-in profile
// speaks them, against other QUIC servers and public HTTP/3 sites. It is
// opt-in and never part of go test: it needs Docker and the network.
//
//	go run ./cmd/interop -local                  # every runner server image
//	go run ./cmd/interop -public                 # every public site
//	go run ./cmd/interop -local -servers quic-go,ngtcp2 -cases handshake,retry \
//	    -profiles chrome-155-macos
//
// Local runs start the QUIC interop runner's server images standalone
// (pulled beforehand) and run each case a browser client can take part in.
// Public runs walk each site through the public API, then probe it
// directly. Results go to OUT/results.json, OUT/results.md, and per-row
// key logs, datagram traces and server logs under OUT/local or OUT/public.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/roachadam/mimicry/profiles"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "interop:", err)
		os.Exit(1)
	}
}

func run() error {
	local := flag.Bool("local", false, "run the cases against the runner's server images")
	public := flag.Bool("public", false, "run against the public HTTP/3 sites")
	serverList := flag.String("servers", "", "comma-separated server names (default all)")
	caseList := flag.String("cases", "", "comma-separated case names (default all)")
	siteList := flag.String("sites", "", "comma-separated site hosts (default all)")
	profileList := flag.String("profiles", "", "comma-separated built-in profile names (default every supported one)")
	out := flag.String("out", "", "output directory (default a new one under the system temp directory)")
	parallel := flag.Int("parallel", 4, "servers or sites run at once")
	seed := flag.Uint64("seed", 1, "seed of the impairment draws")
	addr := flag.Uint("addr", 0, "with -local: run the cases against a server already listening on this UDP port of 127.0.0.1, instead of the images")
	setupDir := flag.String("setup", "", "with -local: certificates and files directory, reused when it has them (default OUT/setup)")
	flag.Parse()
	if !*local && !*public {
		flag.Usage()
		os.Exit(2)
	}
	nps, err := selectProfiles(*profileList)
	if err != nil {
		return err
	}
	if *out == "" {
		if *out, err = os.MkdirTemp("", "mimicry-interop-"); err != nil {
			return err
		}
	}
	if *out, err = filepath.Abs(*out); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("writing to %s", *out)
	var results []result
	if *local {
		srvs, err := selectNamed(servers, *serverList, func(s server) string {
			return s.name
		})
		if err != nil {
			return err
		}
		cs, err := selectNamed(cases, *caseList, func(c caseDef) string {
			return c.name
		})
		if err != nil {
			return err
		}
		if *setupDir == "" {
			*setupDir = filepath.Join(*out, "setup")
		}
		if *setupDir, err = filepath.Abs(*setupDir); err != nil {
			return err
		}
		setup, err := newLocalSetup(ctx, *setupDir)
		if err != nil {
			return err
		}
		if *addr != 0 {
			results = append(results, setup.runAddr(ctx, uint16(*addr), cs, nps, *out, *seed)...)
		} else {
			results = append(results, fanOut(srvs, *parallel, func(s server) []result {
				return setup.runServer(ctx, s, cs, nps, *out, *seed)
			})...)
		}
	}
	if *public {
		sts, err := selectNamed(sites, *siteList, func(s site) string {
			return s.host
		})
		if err != nil {
			return err
		}
		results = append(results, fanOut(sts, *parallel, func(st site) []result {
			rs := make([]result, 0, len(nps))
			for _, np := range nps {
				r := runSite(ctx, st, np, *out, *seed)
				log.Printf("%-22s %-17s %-4s %s %s", st.host, np.name, r.Outcome, r.Note, r.Err)
				rs = append(rs, r)
			}
			return rs
		})...)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "results.json"), data, 0o600); err != nil {
		return err
	}
	md := matrices(results, nps)
	if err := os.WriteFile(filepath.Join(*out, "results.md"), []byte(md), 0o600); err != nil {
		return err
	}
	fmt.Print(md)
	return ctx.Err()
}

// fanOut runs f on every item, n at a time, and returns the results in
// the items' order.
func fanOut[T any](items []T, n int, f func(T) []result) []result {
	out := make([][]result, len(items))
	sem := make(chan struct{}, max(n, 1))
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() {
				<-sem
			}()
			out[i] = f(it)
		})
	}
	wg.Wait()
	return slices.Concat(out...)
}

// selectProfiles returns the named built-in profiles, or every supported
// one.
func selectProfiles(list string) ([]namedProfile, error) {
	names := profiles.Supported()
	if list != "" {
		names = strings.Split(list, ",")
	}
	var nps []namedProfile
	for _, n := range names {
		p, err := profiles.ByName(n)
		if err != nil {
			return nil, err
		}
		if p.QUIC == nil || p.HTTP3 == nil {
			continue
		}
		nps = append(nps, namedProfile{
			name: n,
			p:    p,
		})
	}
	if len(nps) == 0 {
		return nil, errors.New("no profile with quic and http3 sections")
	}
	return nps, nil
}

// selectNamed returns the items list names (comma-separated), or all.
func selectNamed[T any](all []T, list string, name func(T) string) ([]T, error) {
	if list == "" {
		return all, nil
	}
	var out []T
	for n := range strings.SplitSeq(list, ",") {
		i := slices.IndexFunc(all, func(t T) bool {
			return name(t) == n
		})
		if i < 0 {
			return nil, fmt.Errorf("unknown name %q", n)
		}
		out = append(out, all[i])
	}
	return out, nil
}

// matrices renders the results as Markdown: per local case a target ×
// profile matrix of outcomes, then a row per public site and profile.
func matrices(results []result, nps []namedProfile) string {
	var b strings.Builder
	var local, public []result
	for _, r := range results {
		if r.Case == "site" {
			public = append(public, r)
		} else {
			local = append(local, r)
		}
	}
	if len(local) > 0 {
		b.WriteString("## Local servers\n\n")
		b.WriteString(localMatrix(local, nps))
		b.WriteString("\n### Failures and notes\n\n")
		for _, r := range local {
			if r.Outcome == pass && r.Note == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s %s %s: **%s** %s %s\n", r.Target, r.Case, r.Profile, r.Outcome, r.Note, r.Err)
		}
	}
	if len(public) > 0 {
		b.WriteString("\n## Public sites\n\n")
		b.WriteString("| Site | Profile | Result | QUIC version | Handshake | h3 (engine) | Large | 2nd conn | 0-RTT | First request TCP | Handshake ms | Error |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")
		for _, r := range public {
			b.WriteString(siteRow(r))
		}
	}
	return b.String()
}

// localMatrix is one table: a row per server, a column per case, each
// cell the profiles' outcomes in order (P pass, F fail, - n/a).
func localMatrix(rs []result, nps []namedProfile) string {
	var targets, caseNames []string
	cell := map[[2]string]string{}
	for _, r := range rs {
		if !slices.Contains(targets, r.Target) {
			targets = append(targets, r.Target)
		}
		if !slices.Contains(caseNames, r.Case) {
			caseNames = append(caseNames, r.Case)
		}
		mark := map[string]string{
			pass: "P",
			fail: "F",
			na:   "-",
		}[r.Outcome]
		cell[[2]string{r.Target, r.Case}] += mark
	}
	var b strings.Builder
	short := make([]string, len(nps))
	for i, np := range nps {
		short[i] = np.name
	}
	fmt.Fprintf(&b, "Cells: one mark per profile (%s): P pass, F fail, - n/a.\n\n", strings.Join(short, ", "))
	b.WriteString("| Server | " + strings.Join(caseNames, " | ") + " |\n")
	b.WriteString("|---" + strings.Repeat("|---", len(caseNames)) + "|\n")
	for _, t := range targets {
		b.WriteString("| " + t)
		for _, c := range caseNames {
			b.WriteString(" | " + cell[[2]string{t, c}])
		}
		b.WriteString(" |\n")
	}
	return b.String()
}

// siteRow is a public site's table row.
func siteRow(r result) string {
	version, handshake, hsMS, zero, second := "", "no", "", "", ""
	if len(r.Conns) > 0 {
		c := r.Conns[0]
		version = c.Version
		if c.Handshake {
			handshake = "yes"
		}
		hsMS = fmt.Sprintf("%.0f", c.HandshakeMS)
	}
	if len(r.Conns) > 1 {
		c := r.Conns[1]
		switch {
		case c.Sent0RTT == 0:
			zero = "not sent"
		case c.EarlyData:
			zero = "accepted"
		default:
			zero = "rejected"
		}
		second = "fresh"
		if c.Resumed {
			second = "resumed"
		}
	}
	h3 := "no"
	if r.EngineSummary.H3 {
		h3 = fmt.Sprintf("from request %d", r.EngineSummary.SwitchedAfter)
	}
	tcp := ""
	if len(r.Engine) > 0 {
		tcp = fmt.Sprintf("%s %.0f ms", r.Engine[0].Proto, r.Engine[0].MS)
	}
	large := r.EngineSummary.Large
	for _, s := range r.Engine {
		if s.Step == "large" {
			large = fmt.Sprintf("%s %.1f MB/s", s.Proto, float64(s.Bytes)/max(s.MS, 1)/1000)
		}
	}
	errText := r.Err
	if r.Note != "" && r.Outcome != pass {
		errText = strings.TrimSpace(r.Note + " " + errText)
	}
	return fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
		r.Target, r.Profile, r.Outcome, version, handshake, h3, large, second, zero, tcp, hsMS, strings.ReplaceAll(errText, "|", "/"))
}
