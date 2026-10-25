package main

import (
	"errors"
	"fmt"
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
// browser state. Each key is the connection (as conn.at names it) and the
// feature (the profile field), each value why.
var sampleGaps = map[string]string{ //nolint:gosec // pinned capture gaps, not credentials
	"chrome-155-macos.h3-discovery.local-roots: sample 2, QUIC connection 2: ack_initial": "Chrome acknowledged the server's Initial in 1 of its 12 completed full handshakes: quiche drops its Initial keys when it sends its first Handshake packet, and acknowledges an Initial only if one arrives before it has Handshake keys to send with (timing)",
	"chrome-155-macos.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":      "Chrome's reconnection to www.h3-b.test after the 35 s idle carried no NEW_TOKEN token in 2 of 3 samples, though resumed with 0-RTT; its first connection there had received one (Chrome's token cache, not modelled)",
	"chrome-155-macos.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":      "as sample 2, connection 5",
}

// conn is one captured QUIC connection.
type conn struct {
	// at names it: "<name>.<scenario>[.local-roots]: sample N, QUIC
	// connection M".
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
	name  string
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

func load(dir, name string) (*captureSet, error) {
	paths, err := server.H3ProfileCaptures(dir, name)
	if err != nil {
		return nil, err
	}
	set := &captureSet{
		name:     name,
		usedGaps: map[string]bool{},
	}
	for _, path := range paths {
		base := strings.TrimSuffix(filepath.Base(path), ".pagetrace.json.gz")
		scenario := strings.TrimPrefix(base, name+".")
		scenario, _, _ = strings.Cut(scenario, ".")
		pages, err := capture.LoadPages(path)
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

// unusedGaps fails for this profile's pins that no capture showed.
func (s *captureSet) unusedGaps() error {
	var errs []error
	for key := range sampleGaps {
		if strings.HasPrefix(key, s.name+".") && !s.usedGaps[key] {
			errs = append(errs, fmt.Errorf("sampleGaps pins %q, which no longer appears: remove it", key))
		}
	}
	slices.SortFunc(errs, func(a, b error) int {
		return strings.Compare(a.Error(), b.Error())
	})
	return errors.Join(errs...)
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
