package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// deriveResumed derives http3.qpack.resumed from the h3-resume-table
// captures (server.H3ResumeTableCaptures), or, without one, the
// testing-build capture (capture.TestingPages: QPACK is the network
// stack's): what a connection resumed with 0-RTT does with the server's
// SETTINGS remembered with its ticket. Its encoder stream must open with
// Set Dynamic Table Capacity to the server's capacity, in 0-RTT on at
// least one connection (the capacity is the remembered one), and every
// connection whose encoder wrote again in 1-RTT, once the server's
// SETTINGS had arrived, must agree on whether the capacity was set a
// second time there (QPACKResumedRepeat) or not (QPACKResumedRemembered).
// Without a capture it stays empty.
func deriveResumed(dir string, p *profile.Spec) error {
	files, err := server.H3ResumeTableCaptures(dir, p)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		path, err := capture.TestingPages(dir, p, server.H3ResumeTable.Name, ".local-roots")
		if err != nil || path == "" {
			return err
		}

		files = []capture.File{{Path: path, Scenario: server.H3ResumeTable.Name}}
	}

	var capacity uint64

	for _, s := range server.H3ResumeTable.H3Settings() {
		if s[0] == quictrace.SettingQPACKMaxTableCapacity {
			capacity = s[1]
		}
	}

	var (
		errs                  []error
		inZeroRTT             bool
		repeats, sets, before []string
	)

	for _, f := range files {
		pages, err := capture.LoadPages(f.Path)
		if err != nil {
			return err
		}

		base := strings.TrimSuffix(filepath.Base(f.Path), ".pagetrace.json.gz")

		for si, page := range pages {
			for ci, c := range page.QUIC {
				at := fmt.Sprintf("%s: sample %d, QUIC connection %d", base, si+1, ci+1)

				r, ok := resumedEncoder(c)
				if !ok {
					continue
				}

				switch {
				case len(r.ops) == 0 || r.ops[0].Op != quictrace.OpSetCapacity:
					errs = append(errs, fmt.Errorf("%s: resumed with 0-RTT, its encoder stream does not open with Set Dynamic Table Capacity", at))

					continue

				case r.ops[0].Capacity != capacity:
					errs = append(errs, fmt.Errorf("%s: resumed with 0-RTT, it set the capacity to %d, the server's is %d", at, r.ops[0].Capacity, capacity))

					continue
				}

				inZeroRTT = inZeroRTT || r.firstEarly

				if !r.after {
					before = append(before, at)

					continue
				}

				switch r.capacities {
				case 1:
					sets = append(sets, at)
				case 2:
					repeats = append(repeats, at)
				default:
					errs = append(errs, fmt.Errorf("%s: set the capacity %d times", at, r.capacities))
				}
			}
		}
	}

	switch {
	case !inZeroRTT:
		errs = append(errs, errors.New("h3-resume-table: no resumed connection's encoder stream opened in 0-RTT: whether the capacity is the remembered one is not shown"))
	case len(repeats) > 0 && len(sets) > 0:
		errs = append(errs, fmt.Errorf("h3-resume-table: the capacity was set again once the server's SETTINGS arrived on %s, but not on %s", strings.Join(repeats, "; "), strings.Join(sets, "; ")))
	case len(repeats) == 0 && len(sets) == 0:
		errs = append(errs, fmt.Errorf("h3-resume-table: no resumed connection's encoder wrote once the server's SETTINGS arrived (%d wrote only before), so whether the capacity is set again is not shown", len(before)))
	}

	if err := errors.Join(errs...); err != nil {
		return err
	}

	p.HTTP3.QPACK.Resumed = profile.QPACKResumedRemembered
	if len(repeats) > 0 {
		p.HTTP3.QPACK.Resumed = profile.QPACKResumedRepeat
	}

	return nil
}

// resumedQPACK is a resumed connection's encoder stream: its
// instructions, whether its first bytes went in 0-RTT, whether it wrote in
// 1-RTT too (after the server's SETTINGS, which come with the server's
// first flight), and how many Set Dynamic Table Capacity it sent.
type resumedQPACK struct {
	ops        []quictrace.EncoderInstruction
	firstEarly bool
	after      bool
	capacities int
}

// resumedEncoder reads c's encoder stream if c resumed with 0-RTT and
// opened one; ok is false otherwise.
func resumedEncoder(c *quictrace.Conn) (r resumedQPACK, ok bool) {
	if c.H3 == nil {
		return r, false
	}

	var enc *quictrace.H3Stream

	for i := range c.H3.Streams {
		if c.H3.Streams[i].Type == quictrace.StreamQPACKEncoder {
			enc = &c.H3.Streams[i]
		}
	}

	if enc == nil {
		return r, false
	}

	early, first := false, true

	for _, d := range c.Datagrams {
		for _, p := range d.Packets {
			early = early || p.Type == quictrace.Packet0RTT

			for _, f := range p.Frames {
				if f.Type != quictrace.FrameStream || f.Stream == nil || *f.Stream != enc.ID {
					continue
				}

				if first {
					r.firstEarly = p.Type == quictrace.Packet0RTT
					first = false
				}

				if p.Type == quictrace.Packet1RTT {
					r.after = true
				}
			}
		}
	}

	if !early {
		return r, false
	}

	r.ops = enc.Encoder
	for _, op := range enc.Encoder {
		if op.Op == quictrace.OpSetCapacity {
			r.capacities++
		}
	}

	return r, true
}
