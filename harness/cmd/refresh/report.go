package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// report is harness/reports/refresh-<name>-<date>.md for one plan, or
// report.md in its staging directory when nothing was installed.
type report struct {
	plan     *plan
	run      *refreshRun
	previous *profile.Profile
	// derived is the derived profile's path in staging.
	derived    string
	installed  bool
	deprecated []string
	tests      *result
	realcheck  *result
}

// write writes the report and returns its path.
func (rep *report) write() (string, error) {
	p := rep.plan
	var b strings.Builder
	date := time.Now().Format("2006-01-02")
	fmt.Fprintf(&b, "# Refresh of %s, %s\n\n", p.Name, date)
	how := "new profile, copied from " + p.Previous
	if p.InPlace {
		how = "refreshed in place"
	}
	fmt.Fprintf(&b, "%s %s on %s, %s mode: %s. Made by `go run ./cmd/refresh` (staging `%s`).\n\n",
		p.Browser, p.Installed, rep.run.machine(p), rep.run.Mode, how, rep.run.Dir)

	b.WriteString("## Captures\n\n")
	fmt.Fprintf(&b, "Every capture names %s with one user agent, and the browser's version was %s before and after the run.\n\n", p.Name, p.Installed)
	if len(p.Measured) > 0 {
		fmt.Fprintf(&b, "User agent: `%s`\n\n", p.Measured[0].UserAgent)
	}
	b.WriteString("| Scenario | Samples | Measured | Estimated |\n|---|---|---|---|\n")
	measured := map[string]summaryLine{}
	var total float64
	for _, l := range p.Measured {
		sc := l.Scenario
		if sc == "" {
			sc = capture.DefaultScenario
		}
		measured[sc] = l
		total += l.Seconds
	}
	var estimated time.Duration
	for _, c := range p.Captures {
		m := "not captured"
		if l, ok := measured[c.label()]; ok {
			m = (time.Duration(l.Seconds) * time.Second).String()
		}
		estimated += c.Estimate
		fmt.Fprintf(&b, "| %s | %d | %s | %s |\n", c.label(), c.Samples, m, c.Estimate.Round(time.Second))
	}
	fmt.Fprintf(&b, "| total | | %s | %s |\n\n", (time.Duration(total) * time.Second).String(), estimated.Round(time.Second))
	if !p.Started.IsZero() && !p.Finished.IsZero() {
		fmt.Fprintf(&b, "Wall time of the capture run (launches, copies and checks included): %s.\n\n", p.Finished.Sub(p.Started).Round(time.Second))
	}

	b.WriteString("## Inherited scenarios\n\n")
	if len(p.CapturesFrom) == 0 {
		b.WriteString("None: the profile is checked against its own captures only.\n\n")
	} else {
		b.WriteString("Checked against these profiles' captures, version tokens rewritten (`captures_from`, decision 0035):\n\n")
		for _, sc := range slices.Sorted(maps.Keys(p.CapturesFrom)) {
			fmt.Fprintf(&b, "- %s: %s\n", sc, p.CapturesFrom[sc])
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Changes from %s\n\n", p.Previous)
	if err := rep.writeDiff(&b); err != nil {
		return "", err
	}

	b.WriteString("## Deprecation\n\n")
	if len(rep.deprecated) == 0 {
		fmt.Fprintf(&b, "No %s profile beyond the newest %d supported ones; none deprecated.\n\n", p.Browser, supported)
	}
	for _, name := range rep.deprecated {
		fmt.Fprintf(&b, "- %s marked `deprecated`: more than %d supported %s versions.\n", name, supported, p.Browser)
	}
	if len(rep.deprecated) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("## Verification\n\n")
	for _, s := range []struct {
		title string
		res   *result
	}{
		{"Tests", rep.tests},
		{"realcheck", rep.realcheck},
	} {
		if s.res == nil {
			fmt.Fprintf(&b, "%s: not run.\n\n", s.title)
			continue
		}
		status := "passed"
		if s.res.err != nil {
			status = "failed: " + s.res.err.Error()
		}
		if s.title == "realcheck" && s.res.err == nil {
			status = "ran"
		}
		fmt.Fprintf(&b, "%s (`%s`, %s): %s. Output: `%s`.\n\n", s.title, s.res.command, s.res.duration.Round(time.Second), status, s.res.output)
		if strings.TrimSpace(s.res.summary) != "" {
			fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.TrimSpace(s.res.summary))
		}
	}

	b.WriteString("## To do by hand\n\n")
	b.WriteString("- Review the changes above; a hand-written section copied from the previous profile is unverified until the e2e tests pass against its inherited or new captures.\n")
	b.WriteString("- `profiles/EVIDENCE.md`: add the profile's column (inherited rows marked as decision 0035 says), and `docs/backlog.md` for any new gap pinned.\n")
	if !p.InPlace {
		b.WriteString("- Gap tables keyed by profile name (generators' `sampleGaps`, the e2e tests' gap maps) apply to inherited scenarios through `captures_from`; a scenario recaptured for this version needs its own entry if the oddity recurs.\n")
	}

	path := filepath.Join("reports", "refresh-"+p.Name+"-"+date+".md")
	if !rep.installed {
		path = filepath.Join(rep.run.Dir, p.Target, "report.md")
	}
	return path, os.WriteFile(path, []byte(b.String()), 0o600)
}

// writeDiff lists every value of the new profile that differs from the
// previous one with the previous one's version tokens rewritten to the new
// version (capture.NewRewriter), by where it comes from.
func (rep *report) writeDiff(b *strings.Builder) error {
	data, err := os.ReadFile(rep.derived) //nolint:gosec // the derived profile in staging
	if err != nil {
		return err
	}
	cur, err := profile.Decode(data)
	if err != nil {
		return err
	}
	prevData, err := profile.Encode(rep.previous)
	if err != nil {
		return err
	}
	var prevTree, curTree any
	if err := json.Unmarshal(prevData, &prevTree); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &curTree); err != nil {
		return err
	}
	rw := capture.NewRewriter(rep.previous, cur)
	b.WriteString("Version strings, rewritten in the previous profile before comparing (`capture.NewRewriter`):\n\n")
	for _, name := range []string{"user-agent", "sec-ch-ua"} {
		if old, now := navigationValue(rep.previous, name), navigationValue(cur, name); old != "" || now != "" {
			fmt.Fprintf(b, "- %s: `%s` → `%s`\n", name, old, now)
		}
	}
	b.WriteString("\n")
	prevTree = rewriteStrings(prevTree, rw.Value)
	var diffs []difference
	diffTrees("", prevTree, curTree, &diffs)
	groups := map[string][]difference{}
	for _, d := range diffs {
		groups[sectionOf(d.path)] = append(groups[sectionOf(d.path)], d)
	}
	for _, section := range sections {
		ds := groups[section.name]
		fmt.Fprintf(b, "### %s\n\n", section.title)
		if len(ds) == 0 {
			b.WriteString("No change.\n\n")
			continue
		}
		for _, d := range ds {
			fmt.Fprintf(b, "- `%s`: %s → %s\n", d.path, d.old, d.cur)
		}
		b.WriteString("\n")
	}
	return nil
}

// navigationValue is p's navigation's value of the header name, "" if it
// sends none.
func navigationValue(p *profile.Profile, name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// sections are where the values of a profile come from.
var sections = []struct {
	name  string
	title string
}{
	{"identity", "Name, version, inheritance"},
	{"captured", "From the new captures (cmd/refresh: tls base, http2 preface, headers, http1.headers)"},
	{"generated", "Generated (hellogen: the hello's shape, tls.websocket_h1, tls.quic; h3gen: quic, http3; kindsgen: kinds, context, variants, http3 headers and priorities)"},
	{"hand", "Hand-written, copied from the previous profile"},
}

// capturedHTTP2 are the http2 fields cmd/refresh takes from the capture.
var capturedHTTP2 = []string{"settings", "window_update", "priorities", "headers_priority", "pseudo_header_order", "first_stream_id", "stream_window_update"}

// sectionOf names the section of a JSON path ("tls.cipher_suites[2]").
func sectionOf(path string) string {
	top, rest, _ := strings.Cut(path, ".")
	top, _, _ = strings.Cut(top, "[")
	second, _, _ := strings.Cut(rest, ".")
	second, _, _ = strings.Cut(second, "[")
	switch top {
	case "name", "version", "browser", "platform", "deprecated", "captures_from":
		return "identity"
	case "kinds", "context", "variants", "quic", "http3":
		return "generated"
	case "headers":
		return "captured"
	case "http1":
		if second == "headers" {
			return "captured"
		}
	case "http2":
		if slices.Contains(capturedHTTP2, second) {
			return "captured"
		}
	case "tls":
		switch second {
		case "websocket_h1", "quic":
			return "generated"
		case "resumption":
			if strings.HasPrefix(rest, "resumption.ech_payload_lengths") {
				return "generated"
			}
		default:
			if strings.Contains(rest, "key_share_lengths") || strings.Contains(rest, ".ech") {
				return "generated"
			}
			return "captured"
		}
	}
	return "hand"
}

type difference struct {
	path     string
	old, cur string
}

// diffTrees appends where a and b, decoded JSON, differ: objects key by
// key, arrays of the same length element by element, anything else whole.
func diffTrees(path string, a, b any, out *[]difference) {
	switch av := a.(type) {
	case map[string]any:
		if bv, ok := b.(map[string]any); ok {
			keys := slices.Sorted(maps.Keys(av))
			for k := range bv {
				if _, ok := av[k]; !ok {
					keys = append(keys, k)
				}
			}
			slices.Sort(keys)
			for _, k := range keys {
				child := k
				if path != "" {
					child = path + "." + k
				}
				diffTrees(child, av[k], bv[k], out)
			}
			return
		}
	case []any:
		if bv, ok := b.([]any); ok && len(av) == len(bv) {
			for i := range av {
				diffTrees(path+"["+strconv.Itoa(i)+"]", av[i], bv[i], out)
			}
			return
		}
	}
	ja, jb := compact(a), compact(b)
	if ja != jb {
		*out = append(*out, difference{
			path: path,
			old:  ja,
			cur:  jb,
		})
	}
}

// compact is v as short JSON for the report.
func compact(v any) string {
	if v == nil {
		return "(none)"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := string(data)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}

// rewriteStrings applies f to every string in a decoded JSON tree.
func rewriteStrings(v any, f func(string) string) any {
	switch t := v.(type) {
	case string:
		return f(t)
	case map[string]any:
		for k, x := range t {
			t[k] = rewriteStrings(x, f)
		}
	case []any:
		for i, x := range t {
			t[i] = rewriteStrings(x, f)
		}
	}
	return v
}
