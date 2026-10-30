package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// pair is a driven browser's answers and the built-in profile they are
// compared with: browser-<name>-<major>-<platform> with
// <name>-<major>-<platform>.
type pair struct {
	browser string
	profile string
}

// headline are the fields fingerprinting vendors key on, shown with their
// values in the summary.
var headline = []struct {
	endpoint string
	field    string
	label    string
}{
	{"peet", "tls.ja4", "JA4"},
	{"browserleaks-tls", "ja3n_hash", "JA3 hash, extensions sorted (JA3N)"},
	{"peet", "tls.ja3_hash", "JA3 hash, as sent"},
	{"peet", "tls.peetprint_hash", "peetprint hash"},
	{"peet", "http2.akamai_fingerprint", "Akamai HTTP/2"},
	{"peet", "h2 header order", "HTTP/2 header order"},
	{"impersonate-h2", "http2.headers_frame", "HTTP/2 HEADERS flags and priority"},
	{"impersonate-h3", "protocol", "impersonate.pro /api/http3 protocol"},
	{"impersonate-h3", "tls.ja3n.hash", "QUIC JA3N"},
	{"impersonate-h3", "http3.perk_text_normalized", "HTTP/3 fingerprint (perk, normalized)"},
	{"impersonate-h3", "http3 header order", "HTTP/3 header order"},
}

// sortedTwin names, for a field that carries the extension order, the
// field of the same answer that has it sorted: when the twin matches,
// a difference in the field is the browser's per-connection shuffle.
var sortedTwin = map[string]string{
	"tls.ja3":      "tls.ja3 (extensions sorted)",
	"tls.ja3_hash": "tls.ja3 (extensions sorted)",
	"ja3_text":     "ja3n_text",
	"ja3_hash":     "ja3n_hash",
	"ja4_o":        "ja4",
	"ja4_ro":       "ja4_r",
	"tls.ja3.text": "tls.ja3n.text",
	"tls.ja3.hash": "tls.ja3n.hash",
}

// row is one field compared.
type row struct {
	endpoint string
	name     string
	browser  string
	mimicry  string
	// result: "same", "differs", "random" (a per-connection draw the
	// profile models, from the choices the browser's value is among; note
	// says which), "browser only", "mimicry only".
	result string
	note   string
}

// randomness is what a profile draws per connection.
type randomness struct {
	// shuffled and quicShuffled: the TCP and QUIC hellos' extension
	// orders are shuffled per connection.
	shuffled     bool
	quicShuffled bool
	// echLengths and echSuites are every ECH payload length and cipher
	// suite ("0x0001/0x0001") the profile's hellos choose from.
	echLengths []string
	echSuites  []string
	// tpOrder is quic.transportParameterOrder; pnRandom a random first
	// packet number.
	tpOrder  string
	pnRandom bool
}

func randomnessOf(p *profile.Profile) (randomness, error) {
	r := randomness{
		shuffled: p.TLS.ExtensionOrder == profile.ExtensionOrderShuffled,
	}
	if p.TLS.QUIC != nil {
		r.quicShuffled = p.TLS.QUIC.ExtensionOrder == profile.ExtensionOrderShuffled
	}

	if p.QUIC != nil {
		r.tpOrder = p.QUIC.TransportParameterOrder
		r.pnRandom = p.QUIC.PacketNumbers == profile.PacketNumbersRandomInitial
	}

	data, err := profile.Encode(p)
	if err != nil {
		return r, err
	}

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return r, err
	}

	var walk func(v any)

	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if ech, ok := t["ech"].(map[string]any); ok {
				for _, l := range anyList(ech["payloadLengths"]) {
					r.echLengths = append(r.echLengths, fmt.Sprint(l))
				}

				for _, s := range anyList(ech["cipherSuites"]) {
					m, _ := s.(map[string]any)

					r.echSuites = append(r.echSuites, fmt.Sprintf("%v/%v", m["kdf"], m["aead"]))
				}
			}

			for _, x := range t {
				walk(x)
			}

		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)

	return r, nil
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

// byDesign returns why a field's difference is a per-connection draw the
// profile models with the browser's value among its choices, or "".
func (r randomness) byDesign(endpoint, name string, bv, mv map[string]string) string {
	shuffled := r.shuffled
	if strings.HasSuffix(endpoint, "-h3") {
		shuffled = r.quicShuffled
	}

	if twin, ok := sortedTwin[name]; ok && shuffled && bv[twin] != "" && bv[twin] == mv[twin] {
		return "extension order, shuffled per connection"
	}

	switch name {
	case "ext ECH payload length":
		if slices.Contains(r.echLengths, bv[name]) && slices.Contains(r.echLengths, mv[name]) {
			return "GREASE ECH payload length, one of " + strings.Join(slices.Compact(slices.Sorted(slices.Values(r.echLengths))), "/")
		}

	case "ext ECH cipher suite":
		if slices.Contains(r.echSuites, bv[name]) && slices.Contains(r.echSuites, mv[name]) {
			return "GREASE ECH cipher suite, one of " + strings.Join(slices.Compact(slices.Sorted(slices.Values(r.echSuites))), ", ")
		}

	case "ext quic_transport_parameters order", "http3.perk_text", "http3.perk_hash":
		same := bv["ext quic_transport_parameters"] == mv["ext quic_transport_parameters"]
		if name != "ext quic_transport_parameters order" {
			same = same && bv["http3.perk_text_normalized"] == mv["http3.perk_text_normalized"]
		}

		if same && (r.tpOrder == profile.TransportParameterOrderShuffled || r.tpOrder == profile.TransportParameterOrderRotated) {
			return "QUIC transport parameter order, " + r.tpOrder + " per connection"
		}

	case "quic.initial_packet_number":
		if r.pnRandom {
			return "random first packet number"
		}

	case "http3.perk_text_normalized", "http3.perk_hash_normalized":
		// The echo sorts version_information's versions by value, GREASE
		// (a random value) included, so its place in the list is random.
		if perkVersionsSorted(bv["http3.perk_text_normalized"]) == perkVersionsSorted(mv["http3.perk_text_normalized"]) {
			return "the random GREASE version's place in the echo's sorted version list"
		}
	}

	return ""
}

// perkVersionsSorted is a perk text with the version list of its
// version_information entry ("17:<chosen>@<versions>") in a fixed order.
func perkVersionsSorted(perk string) string {
	parts := strings.Split(perk, ";")
	for i, p := range parts {
		if head, list, ok := strings.Cut(p, "@"); ok && strings.HasPrefix(head, "17:") {
			// The list may end in the section separator: "a,b|3,8".
			list, rest, _ := strings.Cut(list, "|")
			vs := strings.Split(list, ",")
			slices.Sort(vs)

			parts[i] = head + "@" + strings.Join(vs, ",")
			if rest != "" {
				parts[i] += "|" + rest
			}
		}
	}

	return strings.Join(parts, ";")
}

// compare reads every answer under out and writes compare.md and
// compare-full.md.
func compare(out string) error {
	echoes, err := loadEchoes(out)
	if err != nil {
		return err
	}

	var summary, full strings.Builder
	summary.WriteString("# Echo endpoints: browser vs mimicry\n")
	full.WriteString("# Echo endpoints: every field\n")

	var ps []pair
	for c := range echoes {
		if after, ok := strings.CutPrefix(c, "browser-"); ok {
			ps = append(ps, pair{
				browser: c,
				profile: after,
			})
		}
	}

	slices.SortFunc(ps, func(a, b pair) int {
		return strings.Compare(a.browser, b.browser)
	})

	for _, p := range ps {
		b, m := echoes[p.browser], echoes[p.profile]
		if m == nil {
			fmt.Fprintf(&summary, "\n## %s: no answers from profile %s\n", p.browser, p.profile)

			continue
		}

		prof, err := profiles.ByName(p.profile)
		if err != nil {
			return err
		}

		rnd, err := randomnessOf(prof)
		if err != nil {
			return err
		}

		fmt.Fprintf(&summary, "\n## %s vs %s\n\n", p.browser, p.profile)
		fmt.Fprintf(&full, "\n## %s vs %s\n", p.browser, p.profile)

		rows := map[string][]row{}

		summary.WriteString("| Endpoint | Browser proto (tries) | mimicry proto (tries) | Fields | Same | Random by design | Differ | One side only |\n|---|---|---|---|---|---|---|---|\n")

		for _, e := range endpoints {
			br, mr := b[e.id], m[e.id]
			if br == nil || mr == nil {
				continue
			}

			if br.Proto != mr.Proto {
				fmt.Fprintf(&summary, "| %s | %s (%d)%s | %s (%d)%s | not compared: different protocols | | | | |\n", e.id,
					br.Proto, br.Tries, errNote(br.Err), mr.Proto, mr.Tries, errNote(mr.Err))

				continue
			}

			rs, err := compareRecords(e.id, br, mr, rnd)
			if err != nil {
				return fmt.Errorf("%s %s: %w", p.browser, e.id, err)
			}

			rows[e.id] = rs

			count := map[string]int{}

			for _, r := range rs {
				count[r.result]++
			}

			fmt.Fprintf(&summary, "| %s | %s (%d)%s | %s (%d)%s | %d | %d | %d | %d | %d |\n", e.id,
				br.Proto, br.Tries, errNote(br.Err), mr.Proto, mr.Tries, errNote(mr.Err),
				len(rs), count["same"], count["random"], count["differs"], count["browser only"]+count["mimicry only"])
			fmt.Fprintf(&full, "\n### %s\n\n| Field | Browser | mimicry | Result |\n|---|---|---|---|\n", e.id)

			for _, r := range rs {
				fmt.Fprintf(&full, "| %s | %s | %s | %s |\n", r.name, cell(r.browser, 0), cell(r.mimicry, 0), mark(r))
			}
		}

		summary.WriteString("\n| Field | Browser | mimicry | |\n|---|---|---|---|\n")

		for _, h := range headline {
			for _, r := range rows[h.endpoint] {
				if r.name == h.field {
					fmt.Fprintf(&summary, "| %s | %s | %s | %s |\n", h.label, cell(r.browser, 90), cell(r.mimicry, 90), mark(r))
				}
			}
		}

		summary.WriteString("\nRandom by design (the browser's value among the profile's choices):\n\n")

		notes := map[string][]string{}

		var order []string

		for _, e := range endpoints {
			for _, r := range rows[e.id] {
				if r.result != "random" {
					continue
				}

				if notes[r.note] == nil {
					order = append(order, r.note)
				}

				notes[r.note] = append(notes[r.note], e.id+" "+r.name)
			}
		}

		for _, n := range order {
			fmt.Fprintf(&summary, "- %s: %s\n", n, strings.Join(notes[n], ", "))
		}

		summary.WriteString("\nDifferences:\n\n")

		n := 0
		for _, e := range endpoints {
			for _, r := range rows[e.id] {
				if r.result == "same" || r.result == "random" {
					continue
				}

				n++

				fmt.Fprintf(&summary, "- %s `%s` (%s): browser %s, mimicry %s\n", e.id, r.name, r.result, cell(r.browser, 400), cell(r.mimicry, 400))
			}
		}

		if n == 0 {
			summary.WriteString("- none\n")
		}
	}

	summary.WriteString("\n")
	summary.WriteString(sitesTable(out))

	if err := os.WriteFile(filepath.Join(out, "compare.md"), []byte(summary.String()), 0o600); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(out, "compare-full.md"), []byte(full.String()), 0o600)
}

// compareRecords compares one endpoint's answers field by field, in the
// browser's field order, then fields only mimicry's answer has.
func compareRecords(endpoint string, b, m *echoRecord, rnd randomness) ([]row, error) {
	if len(b.Body) == 0 || len(m.Body) == 0 {
		return nil, nil
	}

	bf, err := fieldsOf(endpoint, b.Body)
	if err != nil {
		return nil, err
	}

	mf, err := fieldsOf(endpoint, m.Body)
	if err != nil {
		return nil, err
	}

	bv, mv := values(bf), values(mf)

	var rows []row

	for _, f := range bf {
		r := row{
			endpoint: endpoint,
			name:     f.name,
			browser:  f.value,
		}
		v, ok := mv[f.name]

		switch {
		case !ok:
			r.result = "browser only"
		case v == f.value:
			r.mimicry, r.result = v, "same"
		default:
			r.mimicry, r.result = v, "differs"
			if note := rnd.byDesign(endpoint, f.name, bv, mv); note != "" {
				r.result, r.note = "random", note
			}
		}

		rows = append(rows, r)
	}

	for _, f := range mf {
		if _, ok := bv[f.name]; !ok {
			rows = append(rows, row{
				endpoint: endpoint,
				name:     f.name,
				mimicry:  f.value,
				result:   "mimicry only",
			})
		}
	}

	return rows, nil
}

func values(fs []field) map[string]string {
	m := make(map[string]string, len(fs))
	for _, f := range fs {
		m[f.name] = f.value
	}

	return m
}

// loadEchoes reads OUT/echo/<client>/<endpoint>.json.
func loadEchoes(out string) (map[string]map[string]*echoRecord, error) {
	all := map[string]map[string]*echoRecord{}
	err := filepath.WalkDir(filepath.Join(out, "echo"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}

		data, err := os.ReadFile(path) //nolint:gosec // our own output directory
		if err != nil {
			return err
		}

		var r echoRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		if all[r.Client] == nil {
			all[r.Client] = map[string]*echoRecord{}
		}

		all[r.Client][r.Endpoint] = &r

		return nil
	})

	if errors.Is(err, fs.ErrNotExist) {
		return all, nil
	}

	return all, err
}

// sitesTable is the protected sites' results, one row per site and
// client.
func sitesTable(out string) string {
	files, _ := filepath.Glob(filepath.Join(out, "sites", "*.json"))
	bySite := map[string][]siteRecord{}

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		var rs []siteRecord
		if json.Unmarshal(data, &rs) != nil {
			continue
		}

		for _, r := range rs {
			bySite[r.URL] = append(bySite[r.URL], r)
		}
	}

	var b strings.Builder
	b.WriteString("# Protected sites\n\n| Site (vendor) | Client | Status | Proto | Markers | Vendor cookies | Title | Bytes | Settled (browser) |\n|---|---|---|---|---|---|---|---|---|\n")

	for _, s := range sites {
		rs := bySite[s.url]
		slices.SortFunc(rs, func(a, b siteRecord) int {
			return strings.Compare(clientOrder(a.Client), clientOrder(b.Client))
		})

		for _, r := range rs {
			var vc []string
			for _, m := range r.Markers {
				if c, ok := strings.CutPrefix(m, "cookie:"); ok {
					vc = append(vc, c)
				}
			}

			var ms []string
			for _, m := range r.Markers {
				if !strings.HasPrefix(m, "cookie:") {
					ms = append(ms, m)
				}
			}

			for _, c := range r.Cookies {
				for _, m := range markers(0, nil, []string{c}, "") {
					if c2, ok := strings.CutPrefix(m, "cookie:"); ok && !slices.Contains(vc, c2) {
						vc = append(vc, c2)
					}
				}
			}

			settledCell := ""
			if r.Settled != nil {
				var sm []string
				for _, m := range r.Settled.Markers {
					if !strings.HasPrefix(m, "cookie:") {
						sm = append(sm, m)
					}
				}

				settledCell = cell(r.Settled.Title, 40) + " " + strings.Join(sm, " ")
			}

			status := fmt.Sprint(r.Status)
			if r.Status == 0 {
				status = "–"
			}

			if r.Err != "" {
				status += " error: " + cell(r.Err, 80)
			}

			fmt.Fprintf(&b, "| %s (%s) | %s | %s | %s | %s | %s | %s | %d | %s |\n", s.url, s.vendor, r.Client, status, r.Proto,
				strings.Join(ms, " "), strings.Join(vc, " "), cell(r.Title, 50), r.Bytes, settledCell)
		}
	}

	return b.String()
}

// clientOrder sorts browsers first, then profiles, then Go.
func clientOrder(c string) string {
	switch {
	case strings.HasPrefix(c, "browser-"):
		return "0" + c
	case c == "go-net-http":
		return "2"
	}

	return "1" + c
}

// cell is a value for a table cell: code, pipes escaped, cut at n
// characters (0: whole).
func cell(s string, n int) string {
	if s == "" {
		return "–"
	}

	if n > 0 && len(s) > n {
		s = s[:n] + "…"
	}

	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "`", "'")

	return "`" + s + "`"
}

func mark(r row) string {
	switch r.result {
	case "same":
		return "✓"
	case "random":
		return "✓ random: " + r.note
	}

	return "✗ " + r.result
}

func errNote(err string) string {
	if err == "" {
		return ""
	}

	return " " + cell(err, 60)
}
