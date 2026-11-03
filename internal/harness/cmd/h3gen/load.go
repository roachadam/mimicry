package main

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// sampleGaps pin connections that differ from the rest of their browser's
// for timing or state reasons. Keys are conn.at name plus profile field,
// or an h3-discovery sample, host and "switch" for its switch rule, so a
// pin applies to every inheriting profile; values say why.
var sampleGaps = map[string]string{ //nolint:gosec // pinned capture gaps, not credentials
	"chrome-155-macos.h3-discovery.local-roots: sample 2, QUIC connection 2: ackInitial":                         "Chrome acknowledged the server's Initial in 1 of its 12 completed full handshakes: quiche drops its Initial keys when it sends its first Handshake packet, and acknowledges an Initial only if one arrives before it has Handshake keys to send with (timing)",
	"chrome-155-macos.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":                             "Chrome's reconnection to www.h3-b.test after the 35 s idle carried no NEW_TOKEN token in 2 of 3 samples, though resumed with 0-RTT; its first connection there had received one (Chrome's token cache, not modelled)",
	"chrome-155-macos.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":                             "as sample 2, connection 5",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 1: ackInitial":                         "as chrome-155's sample 2, connection 2: 1 of 21 completed full handshakes acknowledged the server's Initial (timing)",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 1: ackBeforeFinished":                  "the same connection's ACK of the server's Initial left before its Finished (ackInitial)",
	"chrome-156-macos.h3-discovery.local-roots: sample 1, QUIC connection 4: priorityUpdate /h3/after-idle.so":   "on the second Mac the server read this PRIORITY_UPDATE 1 ms after its request's HEADERS (another datagram), where every other one, and all of a 2-sample check of Chrome 156 on another Mac, came no later: Chrome's packet flushing on the slower Mac (timing)",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 1: priorityUpdate /h3/second.so":       "as sample 1, connection 4",
	"chrome-156-macos.h3-discovery.local-roots: sample 3, QUIC connection 4: priorityUpdate /h3/after-idle.so":   "as sample 1, connection 4",
	"chrome-156-macos.h3-discovery.local-roots: sample 1, QUIC connection 3: tokens":                             "Chrome 156 presented the NEW_TOKEN token on 3 of 9 reconnections whose host had given it one (Chrome 155: 7 of 9), on this capture from the second Mac as on a second check on another Mac (3 of 6): which ones varies between samples (Chrome's token cache, not modelled)",
	"chrome-156-macos.h3-discovery.local-roots: sample 1, QUIC connection 5: tokens":                             "as sample 1, connection 3",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":                             "as sample 1, connection 3",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 4: tokens":                             "as sample 1, connection 3",
	"chrome-156-macos.h3-discovery.local-roots: sample 3, QUIC connection 4: tokens":                             "as sample 1, connection 3",
	"chrome-156-macos.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":                             "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 1, QUIC connection 3: tokens":                           "on Windows as on macOS, Chrome 156 presented the NEW_TOKEN token on 3 of 9 reconnections whose host had given it one (h3-early: 15 of 27, h3-too-early: 12 of 27), which ones varying between samples (Chrome's token cache, not modelled)",
	"chrome-156-windows.h3-discovery.local-roots: sample 1, QUIC connection 5: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 3, QUIC connection 3: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 3, QUIC connection 4: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 1, QUIC connection 4: priorityUpdate /h3/after-idle.so": "as chrome-156-macos's sample 1, connection 4: on Windows too the server read one PRIORITY_UPDATE 1 ms after its request's HEADERS (another datagram), where every other in these captures came no later (timing)",
	"firefox-158-macos.h3-broken.local-roots: sample 3, QUIC connection 4: loss.cachedRtt":                       "Firefox's resumed attempt after the server's close first probed after 306 ms, the initial PTO, where the other two probed after 3 ms from their cached RTT; the same attempt also gave up later (loss.resumed): another path through necko/neqo, cause not captured",
	"firefox-158-macos.h3-broken.local-roots: sample 3, QUIC connection 4: loss.resumed":                         "Firefox closed this resumed attempt 9.2 s in, not 5.0 s; a TCP connection opened at 5.0 s as in the other samples but took a GOAWAY at once, and the held requests waited for the close (see loss.cachedRtt)",
	"firefox-158-macos.h3-broken.local-roots: sample 2: keepAlive":                                               "Firefox's keep-alive PING came 15.25 s after its idle timer restarted, 254 ms late (15.00–15.01 s in the other samples): a timer wake-up, not captured",
	"firefox-158-macos.h3-early.local-roots: sample 2: earlyData.heldWithFinished pair-post.so":                  "Firefox's held requests left in the datagram with its Finished in 32 of 36 cases; this one 0.17 ms after it, made 2.8 ms before: when necko hands a held request to neqo against when neqo writes the Finished (thread scheduling, not captured)",
	"firefox-158-macos.h3-early.local-roots: sample 3: earlyData.heldWithFinished form.so":                       "as sample 2's pair-post.so: 1.7 ms after the Finished (a form navigation, made 36 ms before)",
	"firefox-158-macos.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished post.so":                   "as h3-early sample 2's pair-post.so: 18.6 ms after the Finished, made 35 ms before",
	"firefox-158-macos.h3-too-early.local-roots: sample 2: earlyData.heldWithFinished form.so":                   "as h3-early sample 2's pair-post.so: 1.1 ms after the Finished (a form navigation, made 54 ms before)",
	"firefox-158-windows.h3-broken.local-roots: sample 1, QUIC connection 19: loss.cachedRtt":                    "in this sample Firefox on Windows opened 14 QUIC connections beside the page's and closed each 1–5 ms in (H3_NO_ERROR), and its resumed attempt after the server's close probed after 4.6 ms from a cached RTT, as on macOS; the other two (and 5 of 6 further samples, 2026-11-01) first probed 1.7–1.9 s after their first flight: necko/neqo state not captured",
	"firefox-158-windows.h3-broken.local-roots: sample 1, QUIC connection 19: loss.resumed":                      "the same attempt (see loss.cachedRtt) ended silently 4.5 s in, with no TCP connection to the host after the close; the others closed 9.8–10.2 s in (1 of 6 further samples ended silently the same way, one closed at 5.0 s)",
	"firefox-158-windows.h3-broken.local-roots: sample 1: hold":                                                  "the sample of loss.cachedRtt: no TCP connection to the closed host followed the server's close, so nothing held closed1.cs; the other two held it as on macOS",
	"firefox-158-windows.h3-broken.local-roots: sample 1: keepAlive":                                             "Firefox's keep-alive PING came 15.10 s after its idle timer restarted, 97 ms late (15.01 s in the other two; in 6 further samples 15.00–15.39 s, 3 beyond 50 ms): Firefox's timers on Windows wake late, not modelled",
	"firefox-158-windows.h3-broken.local-roots: sample 3, QUIC connection 4: loss.resumed":                       "Firefox closed this resumed attempt 10.2 s in, sample 2's at 9.8 s (6 further samples: 9.8, 9.8, 10.2, 10.3, 5.0 s and one silent): two give-up moments, cause not captured",
	"firefox-158-windows.h3-download.local-roots: sample 3: credit":                                              "one MAX_STREAM_DATA raised the limit by 3,221,633 bytes, 2.4% above 3 MiB, in the sample where Firefox opened and at once closed a QUIC connection beside the page's for each request after the download (7); 1 of 3 further samples raised one by 3.3%: when necko reads, not captured",
	"safari-26-macos.h3-download: sample 1: credit":                                                              "Safari returns stream credit as its application reads (limit = bytes read + 2 MiB), at irregular points: increments 0.81–1.92 MB, median 1.60 MB, on this slower Mac (1 s transfer)",
	"safari-26-macos.h3-download: sample 2: credit":                                                              "Safari returns stream credit as its application reads, at irregular points: increments 0.95–1.32 MB, median 1.05 MB (window / 2)",
	"safari-26-macos.h3-download: sample 3: credit":                                                              "as sample 2: increments 0.90–1.36 MB, median 1.05 MB",
	"safari-27-macos.h3-download: sample 1: credit":                                                              "Safari returns stream credit as its application reads, at irregular points: increments 1.4 KB–2.10 MB, median 0.96 MB (window / 2.2)",
	"safari-27-macos.h3-download: sample 2: credit":                                                              "as sample 1: increments 62 B–2.10 MB, median 0.91 MB",
	"safari-27-macos.h3-download: sample 3: credit":                                                              "as sample 1: increments 63 B–2.10 MB, median 0.98 MB",
	"safari-27-macos.h3-vn: sample 1: tcpDelayMs vn-3.so":                                                        "after an immediate Version Negotiation Safari 27 dialled TCP 202 ms after its QUIC attempt, beyond the 100–129 ms of its races elsewhere (Safari 26: 92–106 ms, within its range); three samples cannot say why",
	"safari-27-macos.h3-vn: sample 2: tcpDelayMs vn-3.so":                                                        "as sample 1: 139 ms",
	"safari-27-ios.h3-discovery: sample 3, www.h3-b.test: switch":                                                "opened by hand, the page's idle spell lasted 69 s instead of 35 s (samples 1 and 2: 49 s and 35 s; timers held back on the phone, timing), so www.h3-b.test's HTTP/2 connection had been idle 78 s and was gone [INFERENCE: closed by the phone, unrecorded], and xafter-idle.cs went on a new connection, over QUIC: the new-connection rule, without the GOAWAY this check looks for; at 52 s idle (sample 1) the connection was still used",
	"chrome-157-ios.h3-broken: sample 2: keepAlive":                                                              "the server stopped answering before the 7.5 s PING, so the first PING this check counts came 4069 ms after the last datagram received; the PINGs' cadence is about 2000 ms in all three samples (5.5, 7.6, 9.5, 11.5, 13.5 s), and samples 1 and 3 give 2014 and 2016 ms",
	"chrome-157-ios.h3-blackhole: abandonOnTcp":                                                                  "abandonOnTcp stays true, as copied from safari-27-ios: h3gen counts an attempt abandoned only if it sends no probe at all, and Chrome on the iPhone sent one or two retransmitted Initial pairs (a 1038-byte unpadded datagram and a 1200-byte one) 28-137 ms into every unanswered attempt (6 of 6), the last between 29 ms before and 8 ms after the raced TCP connection's ClientHello reached the server, and nothing after, where a probing attempt goes on for seconds; Safari 26 on the iPhone sent none or one such pair",
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
	p     *profile.Spec
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

// load reads the h3 captures p is checked against, as captured: h3gen
// measures the browser's field encoding, so nothing is rewritten.
func load(dir string, p *profile.Spec) (*captureSet, error) {
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
	return s.pinned(c.at + ": " + feature)
}

// pinned reports whether sampleGaps pins key, and marks the pin used.
func (s *captureSet) pinned(key string) bool {
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
