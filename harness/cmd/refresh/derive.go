package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// fromCapture copies prev into a profile named name at version, taking from
// the captures the default scenario's ClientHello, HTTP/2 preface and
// navigation headers, and the http1 navigation (h1; nil keeps prev's lines
// with version tokens rewritten). Everything else stays prev's, to be
// regenerated or copied.
func fromCapture(prev *profile.Profile, name, version string, captured *profile.Profile, h1 []string, capturesFrom map[string]string) (*profile.Profile, error) {
	p, err := clone(prev)
	if err != nil {
		return nil, err
	}
	p.Name = name
	p.Version = version
	p.Deprecated = false
	p.CapturesFrom = capturesFrom

	t := &p.TLS
	ct := captured.TLS
	t.RecordVersion = ct.RecordVersion
	t.LegacyVersion = ct.LegacyVersion
	t.SessionIDLength = ct.SessionIDLength
	t.CipherSuites = ct.CipherSuites
	t.CompressionMethods = ct.CompressionMethods
	t.ExtensionOrder = ct.ExtensionOrder
	// A shuffling browser's capture holds one of its orders: when it has
	// the same extension types, its values go in prev's order, so only a
	// real change shows.
	t.Extensions = ct.Extensions
	if ct.ExtensionOrder == profile.ExtensionOrderShuffled {
		if ordered, ok := inOrderOf(prev.TLS.Extensions, ct.Extensions); ok {
			t.Extensions = ordered
		}
	}

	if captured.HTTP2 == nil || p.HTTP2 == nil {
		return nil, fmt.Errorf("the default capture or %s has no http2", prev.Name)
	}
	h, ch := p.HTTP2, captured.HTTP2
	h.Settings = ch.Settings
	h.WindowUpdate = ch.WindowUpdate
	h.Priorities = ch.Priorities
	h.HeadersPriority = ch.HeadersPriority
	h.PseudoHeaderOrder = ch.PseudoHeaderOrder
	h.FirstStreamID = ch.FirstStreamID
	h.StreamWindowUpdate = ch.StreamWindowUpdate

	p.Headers = withSlots(prev.Headers, captured.Headers)

	if prev.HTTP1 != nil {
		if h1 == nil {
			rw := capture.NewRewriter(prev, p)
			for i := range p.HTTP1.Headers {
				p.HTTP1.Headers[i].Value = rw.Value(p.HTTP1.Headers[i].Value)
			}
		} else {
			lines := make([]profile.Header, 0, len(h1))
			for _, l := range h1 {
				name, value, ok := strings.Cut(l, ":")
				if !ok {
					return nil, fmt.Errorf("http1: header line %q has no colon", l)
				}
				lines = append(lines, profile.Header{
					Name:  name,
					Value: strings.TrimLeft(value, " \t"),
				})
			}
			p.HTTP1.Headers = withSlots(prev.HTTP1.Headers, lines)
		}
	}
	return p, nil
}

// withSlots is the captured header list with old's slots (valueless headers
// the request fills in) kept after the same preceding header as in old.
func withSlots(old, captured []profile.Header) []profile.Header {
	isSlot := func(name string) bool {
		return slices.ContainsFunc(old, func(h profile.Header) bool {
			return h.Value == "" && strings.EqualFold(h.Name, name)
		})
	}
	index := func(list []profile.Header, name string) int {
		return slices.IndexFunc(list, func(h profile.Header) bool {
			return strings.EqualFold(h.Name, name)
		})
	}
	out := make([]profile.Header, len(captured))
	for i, h := range captured {
		if isSlot(h.Name) {
			h.Value = ""
		}
		out[i] = h
	}
	for i, h := range old {
		if h.Value != "" || index(out, h.Name) >= 0 {
			continue
		}
		at := 0
		for j := i - 1; j >= 0; j-- {
			if k := index(out, old[j].Name); k >= 0 {
				at = k + 1
				break
			}
		}
		out = slices.Insert(out, at, profile.Header{
			Name: h.Name,
		})
	}
	return out
}

// inOrderOf returns captured's extensions in order's order of types, false
// if the two do not hold the same types as often (GREASE twice).
func inOrderOf(order, captured []profile.Extension) ([]profile.Extension, bool) {
	if len(order) != len(captured) {
		return nil, false
	}
	used := make([]bool, len(captured))
	out := make([]profile.Extension, 0, len(captured))
	for _, x := range order {
		i := slices.IndexFunc(captured, func(y profile.Extension) bool {
			return y.Type == x.Type
		})
		for i >= 0 && used[i] {
			next := slices.IndexFunc(captured[i+1:], func(y profile.Extension) bool {
				return y.Type == x.Type
			})
			if next < 0 {
				i = -1
			} else {
				i += 1 + next
			}
		}
		if i < 0 {
			return nil, false
		}
		used[i] = true
		out = append(out, captured[i])
	}
	return out, true
}

func clone(p *profile.Profile) (*profile.Profile, error) {
	data, err := profile.Encode(p)
	if err != nil {
		return nil, err
	}
	return profile.Decode(data)
}

// http1Navigation is the header lines of the first request, a GET of /, in
// the first sample of an http1 capture.
func http1Navigation(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a staged capture
	if err != nil {
		return nil, err
	}
	var samples [][]capture.H1Request
	if err := json.Unmarshal(data, &samples); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("%s: no samples", path)
	}
	for _, req := range samples[0] {
		if req.Line == "GET / HTTP/1.1" {
			return req.Headers, nil
		}
	}
	return nil, fmt.Errorf("%s: no GET / in its first sample", path)
}

// derive builds p's profile in staging: fromCapture on the previous
// profile and the staged captures, then hellogen, h3gen and kindsgen over
// an overlay of the committed captures and the staged ones.
func (r *refreshRun) derive(ctx context.Context, p *plan, rep *report) (string, error) {
	builtins, err := loadProfiles(profilesDir)
	if err != nil {
		return "", err
	}
	prev := builtins[p.Previous]
	if prev == nil {
		return "", fmt.Errorf("no profile %s to derive from", p.Previous)
	}
	rep.previous = prev
	staged := r.stagedCaptures(p)
	data, err := os.ReadFile(filepath.Join(staged, p.Name+".json")) //nolint:gosec // a staged capture
	if err != nil {
		return "", fmt.Errorf("the default scenario's capture: %w", err)
	}
	captured, err := profile.Decode(data)
	if err != nil {
		return "", err
	}
	var h1 []string
	if path := filepath.Join(staged, p.Name+".http1.h1trace.json"); exists(path) {
		if h1, err = http1Navigation(path); err != nil {
			return "", err
		}
	}
	derived, err := fromCapture(prev, p.Name, p.Version, captured, h1, p.CapturesFrom)
	if err != nil {
		return "", err
	}
	out, err := profile.Encode(derived)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(r.Dir, p.Target)
	path := filepath.Join(dir, p.Name+".json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return "", err
	}
	overlay := filepath.Join(dir, "overlay")
	if err := buildOverlay(overlay, staged, p); err != nil {
		return "", err
	}
	logFile := filepath.Join(dir, "generators.log")
	for _, gen := range []string{"hellogen", "h3gen", "kindsgen"} {
		log.Printf("%s: %s", p.Target, gen)
		if err := runLogged(ctx, logFile, ".", "go", "run", "./cmd/"+gen, "-captures", overlay, "-profile", path); err != nil {
			return "", fmt.Errorf("%s refused (the sample it names is in %s; pin a gap or fix the rule, then -resume %s): %w", gen, overlay, r.Dir, err)
		}
	}
	return path, nil
}

// buildOverlay fills dir with the committed captures, linked, and p's
// staged ones, copied (hellogen rewrites the default scenario's): the
// committed files of the scenarios p recaptured under its own name are left
// out, so a refresh in place reads only the new ones.
func buildOverlay(dir, staged string, p *plan) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	recaptured := map[string]bool{}
	for _, c := range p.Captures {
		recaptured[c.Scenario] = true
	}
	committed, err := os.ReadDir(capturesDir)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(capturesDir)
	if err != nil {
		return err
	}
	for _, e := range committed {
		if sc, ok := capture.ScenarioOf(p.Name, e.Name()); ok && recaptured[sc] {
			continue
		}
		if err := os.Symlink(filepath.Join(abs, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	files, err := os.ReadDir(staged)
	if err != nil {
		return err
	}
	for _, e := range files {
		if err := copyFile(filepath.Join(staged, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from) //nolint:gosec // the tool's own staging
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()
	_ = os.Remove(to)
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the tool's own staging
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runLogged runs a command in dir, appending its output to logFile and, if
// it fails, returning the output's tail with the error.
func runLogged(ctx context.Context, logFile, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the tool's own commands
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	f, ferr := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) //nolint:gosec // the tool's own log
	if ferr == nil {
		_, _ = fmt.Fprintf(f, "$ %s %s\n%s\n", name, strings.Join(args, " "), out)
		_ = f.Close()
	}
	if err != nil {
		return fmt.Errorf("%w:\n%s", err, tail(string(out), 40))
	}
	return errors.Join(ferr)
}

// tail is the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
