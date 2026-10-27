package main

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// sampleGaps pin the connections whose behaviour differs from what the
// rest of their browser's show, in a way no rule explains: timing or
// browser state. Each key is the connection (as conn.at names it: by the
// capture file, so a pin holds for every profile that inherits that
// capture) and the feature (the profile field), each value why.
var sampleGaps = map[string]string{ //nolint:gosec // pinned capture gaps, not credentials
	"chrome-155-macos.h3-discovery.local-roots: sample 2, QUIC connection 2: ack_initial":          "Chrome acknowledged the server's Initial in 1 of its 12 completed full handshakes: quiche drops its Initial keys when it sends its first Handshake packet, and acknowledges an Initial only if one arrives before it has Handshake keys to send with (timing)",
	"chrome-155-macos.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":               "Chrome's reconnection to www.h3-b.test after the 35 s idle carried no NEW_TOKEN token in 2 of 3 samples, though resumed with 0-RTT; its first connection there had received one (Chrome's token cache, not modelled)",
	"chrome-155-macos.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":               "as sample 2, connection 5",
	"firefox-158-macos.h3-broken.local-roots: sample 3, QUIC connection 4: loss.cached_rtt":        "Firefox's resumed attempt after the server's close first probed after 306 ms, the initial PTO, where the other two probed after 3 ms from their cached RTT; the same attempt also gave up later (loss.resumed): another path through necko/neqo, cause not captured",
	"firefox-158-macos.h3-broken.local-roots: sample 3, QUIC connection 4: loss.resumed":           "Firefox closed this resumed attempt 9.2 s in, not 5.0 s; a TCP connection opened at 5.0 s as in the other samples but took a GOAWAY at once, and the held requests waited for the close (see loss.cached_rtt)",
	"firefox-158-macos.h3-broken.local-roots: sample 2: keep_alive":                                "Firefox's keep-alive PING came 15.25 s after its idle timer restarted, 254 ms late (15.00–15.01 s in the other samples): a timer wake-up, not captured",
	"firefox-158-macos.h3-early.local-roots: sample 2: early_data.held_with_finished pair-post.so": "Firefox's held requests left in the datagram with its Finished in 32 of 36 cases; this one 0.17 ms after it, made 2.8 ms before: when necko hands a held request to neqo against when neqo writes the Finished (thread scheduling, not captured)",
	"firefox-158-macos.h3-early.local-roots: sample 3: early_data.held_with_finished form.so":      "as sample 2's pair-post.so: 1.7 ms after the Finished (a form navigation, made 36 ms before)",
	"firefox-158-macos.h3-too-early.local-roots: sample 1: early_data.held_with_finished post.so":  "as h3-early sample 2's pair-post.so: 18.6 ms after the Finished, made 35 ms before",
	"firefox-158-macos.h3-too-early.local-roots: sample 2: early_data.held_with_finished form.so":  "as h3-early sample 2's pair-post.so: 1.1 ms after the Finished (a form navigation, made 54 ms before)",
	"safari-26-macos.h3-download: sample 1: credit":                                                "Safari returns stream credit as its application reads (limit = bytes read + 2 MiB), at irregular points: increments 0.81–1.92 MB, median 1.60 MB, on this slower Mac (1 s transfer)",
	"safari-26-macos.h3-download: sample 2: credit":                                                "Safari returns stream credit as its application reads, at irregular points: increments 0.95–1.32 MB, median 1.05 MB (window / 2)",
	"safari-26-macos.h3-download: sample 3: credit":                                                "as sample 2: increments 0.90–1.36 MB, median 1.05 MB",
	"safari-27-macos.h3-download: sample 1: credit":                                                "Safari returns stream credit as its application reads, at irregular points: increments 1.4 KB–2.10 MB, median 0.96 MB (window / 2.2)",
	"safari-27-macos.h3-download: sample 2: credit":                                                "as sample 1: increments 62 B–2.10 MB, median 0.91 MB",
	"safari-27-macos.h3-download: sample 3: credit":                                                "as sample 1: increments 63 B–2.10 MB, median 0.98 MB",
	"safari-27-macos.h3-vn: sample 1: tcp_delay_ms vn-3.so":                                        "after an immediate Version Negotiation Safari 27 dialled TCP 202 ms after its QUIC attempt, beyond the 100–129 ms of its races elsewhere (Safari 26: 92–106 ms, within its range); three samples cannot say why",
	"safari-27-macos.h3-vn: sample 2: tcp_delay_ms vn-3.so":                                        "as sample 1: 139 ms",
}

// conn is one captured QUIC connection.
type conn struct {
	// at names it: "<source>.<scenario>[.local-roots]: sample N, QUIC
	// connection M", source the profile whose capture it is.
	at string
	// scenario is "h3-discovery" or "h3-page"; sample counts from 0.
	scenario string
	sample   int
	page     *capture.Page
	c        *quictrace.Conn
	resumed  bool
	// done: the handshake completed (H3 is set).
	done bool
}

// captureSet is every QUIC connection of a profile's h3 captures, and the
// pinned gaps used so far.
type captureSet struct {
	// name is the profile's, p the profile.
	name  string
	p     *profile.Profile
	conns []*conn
	// pages are the h3-discovery captures' samples in which some QUIC
	// connection completed its handshake, with their file's name.
	discovery []discoveryPage
	usedGaps  map[string]bool
}

type discoveryPage struct {
	at   string
	page *capture.Page
}

// load reads the h3 captures p is checked against (its own, or those it
// inherits: server.H3ProfileCaptures). They are read as captured: h3gen
// measures how the browser encoded its fields (QPACK, Huffman), so no
// value is rewritten to p's version.
func load(dir string, p *profile.Profile) (*captureSet, error) {
	files, err := server.H3ProfileCaptures(dir, p)
	if err != nil {
		return nil, err
	}
	set := &captureSet{
		name:     p.Name,
		p:        p,
		usedGaps: map[string]bool{},
	}
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f.Path), ".pagetrace.json.gz")
		scenario := f.Scenario
		pages, err := capture.LoadPages(f.Path)
		if err != nil {
			return nil, err
		}
		for si, page := range pages {
			anyDone := false
			for ci, c := range page.QUIC {
				cn := &conn{
					at:       fmt.Sprintf("%s: sample %d, QUIC connection %d", base, si+1, ci+1),
					scenario: scenario,
					sample:   si,
					page:     page,
					c:        c,
					resumed:  c.Resumed,
					done:     c.H3 != nil,
				}
				anyDone = anyDone || cn.done
				set.conns = append(set.conns, cn)
			}
			if scenario == "h3-discovery" && anyDone {
				set.discovery = append(set.discovery, discoveryPage{
					at:   fmt.Sprintf("%s: sample %d", base, si+1),
					page: page,
				})
			}
		}
	}
	return set, nil
}

// gap reports whether c's difference in feature is pinned, and marks the
// pin used.
func (s *captureSet) gap(c *conn, feature string) bool {
	key := c.at + ": " + feature
	if _, ok := sampleGaps[key]; !ok {
		return false
	}
	s.usedGaps[key] = true
	return true
}

// unusedGaps fails for the pins of the captures this profile is checked
// against that no capture showed.
func (s *captureSet) unusedGaps() error {
	var errs []error
	for key := range sampleGaps {
		if s.pins(key) && !s.usedGaps[key] {
			errs = append(errs, fmt.Errorf("sampleGaps pins %q, which no longer appears: remove it", key))
		}
	}
	slices.SortFunc(errs, func(a, b error) int {
		return strings.Compare(a.Error(), b.Error())
	})
	return errors.Join(errs...)
}

// pins reports whether the sampleGaps key is about a capture the profile
// is checked against: one of the profile its scenario comes from
// (capture.Source).
func (s *captureSet) pins(key string) bool {
	file, _, _ := strings.Cut(key, ":")
	for _, name := range append([]string{s.name}, slices.Collect(maps.Values(s.p.CapturesFrom))...) {
		if !strings.HasPrefix(file, name+".") {
			continue
		}
		if sc, ok := capture.ScenarioOf(name, file+".pagetrace.json.gz"); ok && capture.Source(s.p, sc) == name {
			return true
		}
	}
	return false
}

// done are the connections that completed their handshake.
func (s *captureSet) done() []*conn {
	var out []*conn
	for _, c := range s.conns {
		if c.done {
			out = append(out, c)
		}
	}
	return out
}

// packet is one packet with its datagram.
type packet struct {
	datagram int
	d        *quictrace.Datagram
	p        *quictrace.Packet
}

// packets lists c's packets in wire order.
func (c *conn) packets() []packet {
	var out []packet
	for di := range c.c.Datagrams {
		d := &c.c.Datagrams[di]
		for pi := range d.Packets {
			out = append(out, packet{
				datagram: di,
				d:        d,
				p:        &d.Packets[pi],
			})
		}
	}
	return out
}

// hasFrame reports whether p carries a frame of type typ.
func hasFrame(p *quictrace.Packet, typ string) bool {
	return slices.ContainsFunc(p.Frames, func(f quictrace.Frame) bool {
		return f.Type == typ
	})
}

// cryptoInitials are c's Initial packets that carry CRYPTO frames: its
// ClientHello's.
func (c *conn) cryptoInitials() []packet {
	var out []packet
	for _, p := range c.packets() {
		if p.p.Type == quictrace.PacketInitial && hasFrame(p.p, quictrace.FrameCrypto) {
			out = append(out, p)
		}
	}
	return out
}

// helloLength is the length of c's ClientHello.
func (c *conn) helloLength() int {
	return len(c.c.ClientHelloBytes())
}

// collector gathers errors, each naming its connection.
type collector struct {
	errs []error
}

func (e *collector) add(c *conn, format string, args ...any) {
	e.errs = append(e.errs, fmt.Errorf("%s: %s", c.at, fmt.Sprintf(format, args...)))
}

func (e *collector) err() error {
	return errors.Join(e.errs...)
}

// tpID is a transport parameter's profile ID: GREASE for an unnamed
// reserved one.
func tpID(tp quictrace.TransportParameter) profile.TransportParameterID {
	if tp.Grease {
		return profile.GREASETransportParameter
	}
	return profile.TransportParameterID(tp.ID)
}
