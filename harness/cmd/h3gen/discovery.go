package main

import (
	"fmt"
	"slices"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// raceWindow bounds how long after a TCP dial a QUIC dial to the same host
// still counts as racing it.
const raceWindowMillis = 1000

// sent is one request of a discovery page, when it was sent and how.
type sent struct {
	millis int64
	label  string
	quic   bool
}

// discovery derives when the browser moves an origin to HTTP/3, from the
// h3-discovery samples where QUIC completed: the switch rule, GOAWAY on
// replaced HTTP/2 connections, and what it dials after a QUIC GOAWAY.
func discovery(s *captureSet, e *collector) profile.Discovery {
	var out profile.Discovery
	if len(s.discovery) == 0 {
		e.add(s.conns[0], "no h3-discovery sample with a completed QUIC connection")
		return out
	}
	goaway := ""
	for _, st := range server.H3DiscoverySteps() {
		if st.CloseAfter {
			goaway = st.Label
		}
	}
	var switches []string
	var delays []int
	raced, alone := 0, 0
	goAways, replaced := 0, 0
	for _, dp := range s.discovery {
		page := dp.page
		for _, host := range hosts(page) {
			sw, err := switchRule(page, host, goaway)
			if err != nil {
				e.errs = append(e.errs, fmt.Errorf("%s, %s: %w", dp.at, host, err))
				continue
			}
			switches = append(switches, sw)
		}
		g, r := tcpGoAways(page)
		goAways += g
		replaced += r
		for _, q := range page.QUIC {
			if q.H3 == nil || q.Close == nil || q.Close.By != "server" {
				continue
			}
			tcp, quic := nextDials(page, q.Host, q.Close.Millis)
			switch {
			case quic < 0:
				e.errs = append(e.errs, fmt.Errorf("%s: no QUIC connection to %s after the server closed one", dp.at, q.Host))
			case tcp >= 0 && tcp <= quic && quic-tcp < raceWindowMillis:
				raced++
				delays = append(delays, int(quic-tcp))
			default:
				alone++
			}
		}
	}
	out.Switch = agreed(switches, s, e)
	switch goAways {
	case 0:
	case replaced:
		out.GoAwayTCP = true
	default:
		e.add(s.conns[0], "GOAWAY on %d of %d HTTP/2 connections QUIC replaced", goAways, replaced)
	}
	switch {
	case raced > 0 && alone > 0:
		e.add(s.conns[0], "raced TCP after %d server GOAWAYs, not after %d", raced, alone)
	case raced > 0:
		out.RaceAfterGoAway = true
		out.RaceDelayMillis = span(delays)
	}

	return out
}

// agreed is the one switch rule every host of every sample shows; a host
// without QUIC is consistent only with SwitchNewConnection.
func agreed(switches []string, s *captureSet, e *collector) string {
	rule := ""
	for _, sw := range switches {
		if sw != "" && rule == "" {
			rule = sw
		}
	}
	for _, sw := range switches {
		if sw == "" && rule != profile.SwitchNewConnection || sw != "" && sw != rule {
			e.add(s.conns[0], "hosts switch to HTTP/3 by different rules: %v", switches)
			break
		}
	}

	return rule
}

// hosts are the hosts of a page's TCP connections, in order.
func hosts(page *capture.Page) []string {
	var out []string
	for _, c := range page.Connections {
		if !slices.Contains(out, c.Host) {
			out = append(out, c.Host)
		}
	}

	return out
}

// requests lists host's requests in the page, TCP and QUIC, in the order
// sent. A TCP request's time is its connection's ClientHello's plus its
// HEADERS frame's, on the connection's trace clock.
func requests(page *capture.Page, host string) []sent {
	var out []sent
	conns := tracedConns(page)
	for i, tr := range page.H2 {
		if tr.Host != host || i >= len(conns) {
			continue
		}
		at := map[uint32]int64{}
		for _, f := range tr.Frames {
			if f.Type == "HEADERS" {
				if _, ok := at[f.Stream]; !ok {
					at[f.Stream] = f.Millis
				}
			}
		}
		for _, r := range tr.Requests {
			out = append(out, sent{
				millis: conns[i].Millis + at[r.Stream],
				label:  label(r.Path),
			})
		}
	}
	for _, q := range page.QUIC {
		if q.Host != host || q.H3 == nil {
			continue
		}
		for _, r := range q.H3.Requests {
			out = append(out, sent{
				millis: r.Millis,
				label:  label(r.Path),
				quic:   true,
			})
		}
	}
	slices.SortStableFunc(out, func(a, b sent) int {
		return int(a.millis - b.millis)
	})

	return out
}

// tracedConns are the page's TCP connections that carry an HTTP/2 trace,
// in the traces' order: those with requests (a preconnect that never sent
// its preface has none).
func tracedConns(page *capture.Page) []capture.Connection {
	var out []capture.Connection
	for _, c := range page.Connections {
		if c.Protocol == "h2" && c.Requests > 0 {
			out = append(out, c)
		}
	}

	return out
}

// switchRule is how host moved to HTTP/3 in page: on_alt_svc,
// next_request (QUIC within 100 ms of the estimated TCP request time),
// new_connection (only after GOAWAY), or "" with no QUIC.
func switchRule(page *capture.Page, host, goaway string) (string, error) {
	reqs := requests(page, host)
	if len(reqs) < 2 || reqs[0].quic {
		return "", fmt.Errorf("requests %v do not start on TCP", reqs)
	}
	start := int64(-1)
	for _, q := range page.QUIC {
		if q.Host == host && q.H3 != nil {
			start = q.Millis
			break
		}
	}
	if start < 0 {
		return "", nil
	}
	next := reqs[1]
	switch {
	case next.quic && start < next.millis:
		return profile.SwitchOnAltSvc, nil
	case !next.quic && start-next.millis < 100 && next.millis-start < 100:
		return profile.SwitchNextRequest, nil
	}
	// Only once its TCP connection is gone: the goaway step went on TCP,
	// and nothing after the QUIC connection started.
	sawGoAway := false
	for _, r := range reqs {
		if r.label == goaway && !r.quic && r.millis < start {
			sawGoAway = true
		}
		if !r.quic && r.millis > start {
			return "", fmt.Errorf("%s went on TCP after QUIC started at %d ms", r.label, start)
		}
	}
	if !sawGoAway {
		return "", fmt.Errorf("QUIC started at %d ms, after %s on TCP, not after the server's GOAWAY", start, next.label)
	}

	return profile.SwitchNewConnection, nil
}

// tcpGoAways counts the HTTP/2 connections that sent GOAWAY, and those a
// QUIC connection to their host replaced (one started while they were
// open: after their first request).
func tcpGoAways(page *capture.Page) (goAways, replaced int) {
	conns := tracedConns(page)
	for i, tr := range page.H2 {
		for _, f := range tr.Frames {
			if f.Type == "GOAWAY" {
				goAways++
				break
			}
		}
		if i >= len(conns) {
			continue
		}
		for _, q := range page.QUIC {
			if q.Host == tr.Host && q.H3 != nil && q.Millis >= conns[i].Millis {
				replaced++
				break
			}
		}
	}

	return goAways, replaced
}

// nextDials are when the first TCP and the first QUIC connection to host
// after after started, -1 for none.
func nextDials(page *capture.Page, host string, after int64) (tcp, quic int64) {
	tcp, quic = -1, -1
	for _, c := range page.Connections {
		if c.Host == host && c.Millis > after && tcp < 0 {
			tcp = c.Millis
		}
	}
	for _, q := range page.QUIC {
		if q.Host == host && q.Millis > after && quic < 0 {
			quic = q.Millis
		}
	}

	return tcp, quic
}
