package capture

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// Source names the profile whose captures of scenario a profile is checked
// against: the one its capturesFrom names (decision 0035), or the profile
// itself. cmd/profilegen refuses chains, so this never needs following.
func Source(p *profile.Profile, scenario string) string {
	if from, ok := p.CapturesFrom[scenario]; ok {
		return from
	}

	return p.Name
}

// Inherited returns Source's answer for p's scenario and, when it is another
// profile, the Rewriter that brings that profile's captures to p's version.
// The other profile is a built-in one; its captures were taken under its
// version.
func Inherited(p *profile.Profile, scenario string) (string, *Rewriter, error) {
	source := Source(p, scenario)
	if source == p.Name {
		return source, nil, nil
	}
	from, err := profiles.ByName(source)
	if err != nil {
		return "", nil, fmt.Errorf("%s inherits %s: %w", p.Name, scenario, err)
	}

	return source, NewRewriter(from, p), nil
}

// suffixes are the kinds of capture file, longest first where one ends
// another.
var suffixes = []string{
	".pagetrace.json.gz",
	".h2trace.json",
	".h1trace.json",
	".proxytrace.json",
	".clienthello",
	".json",
}

// localRoots marks a capture taken with the capture CA among the browser's
// local roots (cmd/capture's -h3-local-roots), the same scenario otherwise.
const localRoots = ".local-roots"

// ScenarioOf returns the scenario of a capture file of the profile name
// (<name>[.<scenario>][.local-roots]<suffix>, DefaultScenario without a
// scenario part), and false for a file that is not one.
func ScenarioOf(name, file string) (string, bool) {
	sc, _, ok := splitFile(name, file)
	return sc, ok
}

// splitFile returns a capture file's scenario and kind (its suffix).
func splitFile(name, file string) (string, string, bool) {
	rest, ok := strings.CutPrefix(filepath.Base(file), name+".")
	if !ok {
		return "", "", false
	}
	rest = "." + rest
	for _, s := range suffixes {
		if sc, ok := strings.CutSuffix(rest, s); ok {
			sc = strings.TrimSuffix(sc, localRoots)
			if sc == "" {
				return DefaultScenario, s, true
			}

			return strings.TrimPrefix(sc, "."), s, true
		}
	}

	return "", "", false
}

// File is one capture file a profile is checked against.
type File struct {
	Path     string
	Scenario string
	// Source is the profile that took it: the profile itself or the one
	// it inherits the scenario from. Rewriter is nil for its own.
	Source   string
	Rewriter *Rewriter
}

// Files lists every capture file in dir of the kind suffix (".h2trace.json")
// that p is checked against: its own for the scenarios it does not inherit,
// and its sources' for those it does, sorted by path.
func Files(dir string, p *profile.Profile, suffix string) ([]File, error) {
	names := []string{p.Name}
	for _, from := range p.CapturesFrom {
		if !slices.Contains(names, from) {
			names = append(names, from)
		}
	}
	var out []File
	for _, name := range names {
		matches, err := filepath.Glob(filepath.Join(dir, name+".*"+strings.TrimPrefix(suffix, ".")))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			sc, kind, ok := splitFile(name, path)
			if !ok || kind != suffix {
				continue
			}
			source, rw, err := Inherited(p, sc)
			if err != nil {
				return nil, err
			}
			if source != name {
				continue
			}
			out = append(out, File{
				Path:     path,
				Scenario: sc,
				Source:   source,
				Rewriter: rw,
			})
		}
	}
	slices.SortFunc(out, func(a, b File) int {
		return strings.Compare(a.Path, b.Path)
	})

	return out, nil
}

// Rewriter brings captures taken under one browser version to another's by
// changing only the version tokens in header values, so any other
// difference still shows. A nil Rewriter changes nothing.
type Rewriter struct {
	tokens   *regexp.Regexp
	replace  map[string]string
	chFrom   string
	chTo     string
	fromName string
	toName   string
}

// versionTokens are the parts of each browser's user agent that carry its
// version: Chrome/155.0.0.0, rv:158.0 and Firefox/158.0, Version/27.0.1.
var versionTokens = map[string][]string{
	"chrome":  {`Chrome/[0-9][0-9.]*`},
	"firefox": {`rv:[0-9][0-9.]*`, `Firefox/[0-9][0-9.]*`},
	"safari":  {`Version/[0-9][0-9.]*`},
}

// NewRewriter returns the Rewriter from the captures of from to to's
// version, nil if from is nil or the same profile. Tokens come from each
// profile's user-agent; sec-ch-ua, whose brand order also changes with the
// major version, is mapped whole.
func NewRewriter(from, to *profile.Profile) *Rewriter {
	if from == nil || from.Name == to.Name {
		return nil
	}
	r := &Rewriter{
		replace:  make(map[string]string),
		chFrom:   headerValue(from, "sec-ch-ua"),
		chTo:     headerValue(to, "sec-ch-ua"),
		fromName: from.Name,
		toName:   to.Name,
	}
	fromUA, toUA := headerValue(from, "user-agent"), headerValue(to, "user-agent")
	var patterns []string
	for _, pattern := range versionTokens[to.Browser] {
		re := regexp.MustCompile(pattern)
		old, cur := re.FindString(fromUA), re.FindString(toUA)
		if old != "" && cur != "" && old != cur {
			r.replace[old] = cur
			patterns = append(patterns, pattern)
		}
	}
	if len(patterns) > 0 {
		r.tokens = regexp.MustCompile(strings.Join(patterns, "|"))
	}

	return r
}

// NormalizeVersion rewrites value, a header value or line captured under
// from's version, to to's (NewRewriter).
func NormalizeVersion(value string, from, to *profile.Profile) string {
	return NewRewriter(from, to).Value(value)
}

// headerValue returns p's navigation's value of the header name, from its
// HTTP/2 list or else its HTTP/1.1 one; "" if it sends none.
func headerValue(p *profile.Profile, name string) string {
	lists := [][]profile.Header{p.Headers}
	if p.HTTP1 != nil {
		lists = append(lists, p.HTTP1.Headers)
	}
	for _, list := range lists {
		for _, h := range list {
			if strings.EqualFold(h.Name, name) && h.Value != "" {
				return h.Value
			}
		}
	}

	return ""
}

// String describes the rewrite for logs: "chrome-155-macos → chrome-156-macos".
func (r *Rewriter) String() string {
	if r == nil {
		return "none"
	}

	return r.fromName + " → " + r.toName
}

// Value rewrites one header value, or a header line, request line or
// CONNECT line containing one.
func (r *Rewriter) Value(s string) string {
	if r == nil {
		return s
	}
	if r.chFrom != "" && r.chTo != "" && r.chFrom != r.chTo {
		s = strings.ReplaceAll(s, r.chFrom, r.chTo)
	}
	if r.tokens != nil {
		s = r.tokens.ReplaceAllStringFunc(s, func(m string) string {
			if to, ok := r.replace[m]; ok {
				return to
			}

			return m
		})
	}

	return s
}

// Lines rewrites header lines in place.
func (r *Rewriter) Lines(lines []string) {
	if r == nil {
		return
	}
	for i := range lines {
		lines[i] = r.Value(lines[i])
	}
}

// Traces rewrites each decoded header value of HTTP/2 traces in place. The
// encoded blocks keep their captured bytes, which HPACK reproduction reads.
func (r *Rewriter) Traces(traces []*h2trace.Trace) {
	if r == nil {
		return
	}
	for _, t := range traces {
		for i := range t.Requests {
			ins := t.Requests[i].Instructions
			for j := range ins {
				ins[j].Value = r.Value(ins[j].Value)
			}
		}
	}
}

// H1 rewrites HTTP/1.1 requests in place.
func (r *Rewriter) H1(reqs []H1Request) {
	if r == nil {
		return
	}
	for i := range reqs {
		reqs[i].Line = r.Value(reqs[i].Line)
		r.Lines(reqs[i].Headers)
	}
}

// Proxy rewrites what reached a proxy in place: CONNECT lines.
func (r *Rewriter) Proxy(reqs []ProxyRequest) {
	if r == nil {
		return
	}
	for i := range reqs {
		r.Lines(reqs[i].Lines)
	}
}

// Pages rewrites page captures in place: HTTP/2 traces, HTTP/1.1 requests,
// and each QUIC connection's HTTP/3 field sections and QPACK encoder
// inserts. Encoded blocks keep their captured bytes (see Traces).
func (r *Rewriter) Pages(pages []*Page) {
	if r == nil {
		return
	}
	for _, p := range pages {
		r.Traces(p.H2)
		r.H1(p.H1)
		for _, c := range p.QUIC {
			if c.H3 == nil {
				continue
			}
			for i := range c.H3.Streams {
				enc := c.H3.Streams[i].Encoder
				for j := range enc {
					enc[j].Value = r.Value(enc[j].Value)
				}
			}
			for i := range c.H3.Requests {
				req := &c.H3.Requests[i]
				for j := range req.Section.Fields {
					req.Section.Fields[j].Value = r.Value(req.Section.Fields[j].Value)
				}
				if req.Trailers != nil {
					for j := range req.Trailers.Fields {
						req.Trailers.Fields[j].Value = r.Value(req.Trailers.Fields[j].Value)
					}
				}
			}
		}
	}
}
