package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/internal/bench/scenario"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// charts writes the SVG charts of docs/performance.md from a report merged
// from passes (bench merge -passes) and the raw micro benchmark output it
// names, beside it: every number drawn is in the JSON or the micro file, a
// median over the passes or runs. Each panel compares the clients at one
// point in natural units, a horizontal bar per client sorted best first, so
// that the eye reads who is ahead without decoding a ratio. The output
// depends on the input alone, so the committed charts change only with the
// report.
func charts(args []string) error {
	fs := flag.NewFlagSet("charts", flag.ExitOnError)
	in := fs.String("in", "", "a merged report's JSON (reports/<dir>/<name>-<date>.json)")
	out := fs.String("out", "../../docs/perf", "directory for the SVGs")

	_ = fs.Parse(args)

	b, err := os.ReadFile(*in)
	if err != nil {
		return err
	}

	var r report
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}

	if r.MicroRaw == "" {
		return errors.New("the report names no raw micro output (microRaw)")
	}

	micro, err := readMicro(filepath.Join(filepath.Dir(*in), r.MicroRaw))
	if err != nil {
		return err
	}

	cs := chartSet{
		r:      &r,
		micro:  micro,
		source: filepath.Base(filepath.Dir(*in)) + "/" + filepath.Base(*in),
	}

	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}

	for _, c := range []struct {
		name string
		draw func() (string, error)
	}{
		{
			name: "throughput.svg",
			draw: cs.throughput,
		},
		{
			name: "cpu.svg",
			draw: cs.cpu,
		},
		{
			name: "proxies.svg",
			draw: cs.proxies,
		},
		{
			name: "dropped-clients.svg",
			draw: cs.dropped,
		},
		{
			name: "weak-spots.svg",
			draw: cs.weakSpots,
		},
	} {
		svg, err := c.draw()
		if err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}

		if err := os.WriteFile(filepath.Join(*out, c.name), []byte(svg), 0o644); err != nil { //nolint:gosec // a chart, meant to be read
			return err
		}
	}

	return nil
}

// chartSet draws one report's charts.
type chartSet struct {
	r      *report
	micro  microSet
	source string
}

// Chart geometry, in SVG user units (about pixels at GitHub's README width).
const (
	chartW    = 860.0
	chartPadX = 24.0
	// Text sizes: the title, subtitles, row headings, labels, small labels.
	sizeTitle   = 19.0
	sizeText    = 13.0
	sizeHeading = 14.0
	sizeLabel   = 12.0
	sizeSmall   = 11.0
	// A bar's height and the distance from one bar to the next.
	barH     = 13.0
	barPitch = 18.0
	// The gap between a grid's columns.
	colGap = 28.0
)

// chartStyle colours the charts for light and dark pages: an SVG shown as
// an image cannot read the page's CSS, so it follows the reader's colour
// scheme itself, on a background of its own. mimicry is the one saturated
// colour (Okabe-Ito blue); the other impersonating clients are one muted
// grey; net/http, the reference, is hatched.
const chartStyle = `<style>
.bg{fill:#ffffff;stroke:#d1d9e0}
text{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans",Helvetica,Arial,sans-serif;fill:#1f2328}
.m{fill:#59636e}
.b{font-weight:600}
.a{fill:#0072b2}
.base{stroke:#818b98;stroke-width:1}
.sep{stroke:#d1d9e0;stroke-width:1}
.brk{stroke:#ffffff;stroke-width:3}
.mim{fill:#0072b2}
.oth{fill:#bcc3cb}
.ref{fill:url(#hatch);stroke:#6e7781;stroke-width:1}
.hl{stroke:#6e7781;stroke-width:1.5}
@media (prefers-color-scheme:dark){
.bg{fill:#0d1117;stroke:#3d444d}
text{fill:#f0f6fc}
.m{fill:#9198a1}
.a{fill:#56b4e9}
.base{stroke:#656c76}
.sep{stroke:#2f353d}
.brk{stroke:#0d1117}
.mim{fill:#56b4e9}
.oth{fill:#4b535d}
.ref{stroke:#8b949e}
.hl{stroke:#8b949e}
}
</style>`

// canvas collects an SVG's elements; finish wraps them once the height
// is known.
type canvas struct {
	b strings.Builder
}

// text draws s at x, y; anchor "" is start, class "" is the plain text
// colour.
func (c *canvas) text(x, y, size float64, anchor, class, s string) {
	fmt.Fprintf(&c.b, `<text x="%.1f" y="%.1f" font-size="%.1f"`, x, y, size)

	if anchor != "" {
		fmt.Fprintf(&c.b, ` text-anchor="%s"`, anchor)
	}

	if class != "" {
		fmt.Fprintf(&c.b, ` class="%s"`, class)
	}

	fmt.Fprintf(&c.b, ">%s</text>\n", html.EscapeString(s))
}

func (c *canvas) rect(x, y, w, h float64, class string) {
	fmt.Fprintf(&c.b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" class="%s"/>`+"\n", x, y, w, h, class)
}

func (c *canvas) line(x1, y1, x2, y2 float64, class string) {
	fmt.Fprintf(&c.b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" class="%s"/>`+"\n", x1, y1, x2, y2, class)
}

// pair draws a bold heading of size and a muted note after it, in one text
// element so that the browser spaces them.
func (c *canvas) pair(x, y, size float64, heading, note string) {
	fmt.Fprintf(&c.b, `<text x="%.1f" y="%.1f"><tspan font-size="%.1f" class="b">%s</tspan>`+
		`<tspan font-size="%.1f" class="m" dx="8">%s</tspan></text>`+"\n",
		x, y, size, html.EscapeString(heading), sizeLabel, html.EscapeString(note))
}

// finish returns the SVG: h high, titled for screen readers.
func (c *canvas) finish(h float64, title string) string {
	var b strings.Builder

	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img" aria-label="%s">`+"\n",
		chartW, h, chartW, h, html.EscapeString(title))
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(title))
	b.WriteString(chartStyle + "\n")
	b.WriteString(`<defs><pattern id="hatch" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">` +
		`<line x1="0" y1="0" x2="0" y2="6" class="hl"/></pattern></defs>` + "\n")
	fmt.Fprintf(&b, `<rect x="0.5" y="0.5" width="%.0f" height="%.0f" rx="8" class="bg"/>`+"\n", chartW-1, h-1)
	b.WriteString(c.b.String())
	b.WriteString("</svg>\n")

	return b.String()
}

// header draws a chart's title, subtitle and key, and returns the y below
// them.
func (cs *chartSet) header(c *canvas, title, subtitle string) float64 {
	c.text(chartPadX, 36, sizeTitle, "", "b", title)

	y := 60.0
	for _, l := range wrap(subtitle, chartW-2*chartPadX, sizeText) {
		c.text(chartPadX, y, sizeText, "", "m", l)

		y += 18
	}

	return cs.key(c, y+8)
}

// key draws the colours' meaning and returns the y below it.
func (cs *chartSet) key(c *canvas, y float64) float64 {
	var others []string

	for _, name := range cs.chartClients() {
		if name != "mimicry" && name != referenceClient {
			others = append(others, name)
		}
	}

	x := chartPadX

	for _, k := range []struct {
		class, label string
	}{
		{
			class: "mim",
			label: "mimicry",
		},
		{
			class: "oth",
			label: strings.Join(others, ", "),
		},
		{
			class: "ref",
			label: referenceClient + ", Go's standard client, as the reference",
		},
	} {
		c.rect(x, y-10, 12, 12, k.class)
		c.text(x+17, y, sizeLabel, "", "", k.label)

		x += 17 + textWidth(k.label, sizeLabel) + 22
	}

	return y + 16
}

// caption draws the machine, the run and the source under a chart and
// returns the chart's height.
func (cs *chartSet) caption(c *canvas, y float64, what string) float64 {
	e := cs.r.Env

	nginx := ""
	for _, s := range e.Servers {
		if s.Name == "nginx" {
			nginx = "; " + s.Software
		}
	}

	// The host label (BENCH_HOST_LABEL) names the CPU model already; the
	// rest of hostCpu is the core count (and on a Mac the memory).
	machine := e.Host
	if _, cores, ok := strings.Cut(e.HostCPU, ", "); ok {
		machine += ", " + cores
	}

	lines := []string{
		fmt.Sprintf("%s, %s; the client on %d CPUs (GOMAXPROCS %d)%s.",
			machine, e.HostOS, e.ClientCPUs, e.GOMAXPROCS, nginx),
		fmt.Sprintf("mimicry %s, %s. %s", e.MimicryCommit, cs.r.Date, what),
		"Source: internal/bench/reports/" + cs.source + ".",
	}

	c.line(chartPadX, y, chartW-chartPadX, y, "sep")

	y += 18
	for _, l := range lines {
		for _, w := range wrap(l, chartW-2*chartPadX, sizeSmall) {
			c.text(chartPadX, y, sizeSmall, "", "m", w)

			y += 15
		}
	}

	return y + 6
}

// passesNote says what a layer 3 or 6 value is.
func (cs *chartSet) passesNote() string {
	return fmt.Sprintf("Each bar: the median of %d passes. Ranges overlap: the two values' lowest-to-highest over the passes do "+
		"(the report gives every range).", cs.r.Passes)
}

// wrap breaks s into lines that fit w at size, at spaces. Prose runs
// narrower than textWidth's estimate, which is a label's (digits).
func wrap(s string, w, size float64) []string {
	var (
		out  []string
		line string
	)

	for word := range strings.FieldsSeq(s) {
		if line != "" && float64(len([]rune(line+" "+word)))*size*0.5 > w {
			out = append(out, line)
			line = ""
		}

		if line != "" {
			line += " "
		}

		line += word
	}

	if line != "" {
		out = append(out, line)
	}

	return out
}

// textWidth estimates a label's width in the charts' sans-serif font.
func textWidth(s string, size float64) float64 {
	return float64(len([]rune(s))) * size * 0.56
}

// clientClass is a client's bar class (its colour).
func clientClass(name string) string {
	switch name {
	case "mimicry":
		return "mim"
	case referenceClient:
		return "ref"
	}

	return "oth"
}

// chartClients is the report's clients in the charts' order: mimicry, the
// other impersonating clients, then the reference. Bars are sorted by
// value; this order breaks ties.
func (cs *chartSet) chartClients() []string {
	out := []string{"mimicry"}

	for _, name := range cs.r.others() {
		if name != referenceClient {
			out = append(out, name)
		}
	}

	if slices.Contains(cs.r.clientNames(), referenceClient) {
		out = append(out, referenceClient)
	}

	return out
}

// protoName is a protocol as the charts name it.
func protoName(p target.Proto) string {
	switch p {
	case target.H1:
		return "HTTP/1.1"
	case target.H2:
		return "HTTP/2"
	case target.H3:
		return "HTTP/3"
	}

	return string(p)
}

// thousands formats v rounded to an integer, with thousands separators.
func thousands(v float64) string {
	s := strconv.FormatInt(int64(math.Round(v)), 10)

	neg := strings.HasPrefix(s, "-")

	s = strings.TrimPrefix(s, "-")

	var b strings.Builder

	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}

		b.WriteRune(r)
	}

	if neg {
		return "-" + b.String()
	}

	return b.String()
}

// figure formats a bar's value: one decimal under 10, whole numbers above.
func figure(v float64) string {
	if v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}

	return thousands(v)
}

// median is the middle of v, the mean of the two middle values for an even
// count (benchstat's median).
func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}

	s := slices.Clone(v)
	slices.Sort(s)

	if len(s)%2 == 1 {
		return s[len(s)/2]
	}

	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// unitSuffix splits a size or duration as the reports write it: 1KB, 5ms.
var unitSuffix = regexp.MustCompile(`^([0-9.]+)([A-Za-zµ]+)$`)

// spaced writes a size or duration with a space before its unit: 1 KB.
func spaced(s string) string {
	return unitSuffix.ReplaceAllString(s, "$1 $2")
}

// atOnce names a concurrency.
func atOnce(conc int) string {
	if conc == 1 {
		return "one request at a time"
	}

	return fmt.Sprintf("%d requests at once", conc)
}

// metric is how a panel's values read: the unit after each value, which
// direction is better, and the words that compare mimicry with another
// client.
type metric struct {
	unit  string
	lower bool
	// more, less and times are formats of mimicry against another client:
	// a percentage or a factor, then the other client's name.
	more, less, times string
}

var (
	requestsPerSecond = metric{
		more:  "%s more than %s",
		less:  "%s fewer than %s",
		times: "%s times as many as %s",
	}
	cpuMicros = metric{
		unit:  " µs",
		lower: true,
		more:  "%s more CPU than %s",
		less:  "%s less CPU than %s",
		times: "%s times the CPU of %s",
	}
	memoryKB = metric{
		unit:  " KB",
		lower: true,
		more:  "%s more memory than %s",
		less:  "%s less memory than %s",
		times: "%s times the memory of %s",
	}
	memoryMB = metric{
		unit:  " MB",
		lower: true,
		more:  "%s more memory than %s",
		less:  "%s less memory than %s",
		times: "%s times the memory of %s",
	}
	tunnels = metric{
		lower: true,
		more:  "%s more than %s",
		less:  "%s fewer than %s",
		times: "%s times as many as %s",
	}
	buildMicros = metric{
		unit:  " µs",
		lower: true,
		more:  "%s slower than %s",
		less:  "%s faster than %s",
		times: "%s times as long as %s",
	}
)

// hbar is one client's value in a panel: a median and, from merged passes,
// its range; na, when set, is shown instead of a bar.
type hbar struct {
	client string
	v      float64
	lo, hi float64
	ranged bool
	na     string
}

// cell is one panel: a title with a note after it, a bar per client, and
// notes under the comparison.
type cell struct {
	title, note string
	m           metric
	bars        []hbar
	foot        []string
}

// better says whether a ranks before b.
func (m metric) better(a, b hbar) bool {
	if m.lower {
		return a.v < b.v
	}

	return a.v > b.v
}

// sorted is the cell's bars best first, those without a value last.
func (cl cell) sorted() []hbar {
	out := slices.Clone(cl.bars)

	slices.SortStableFunc(out, func(a, b hbar) int {
		switch {
		case a.na != "" && b.na != "":
			return 0
		case a.na != "":
			return 1
		case b.na != "":
			return -1
		case cl.m.better(a, b):
			return -1
		case cl.m.better(b, a):
			return 1
		}

		return 0
	})

	return out
}

// summary compares mimicry with the best of the other impersonating
// clients in words: "mimicry: 26% less CPU than tls-client", with "(ranges
// overlap)" when the two values' ranges over the passes or runs do.
func (cl cell) summary() string {
	var me, best *hbar

	for i := range cl.bars {
		b := &cl.bars[i]

		switch {
		case b.na != "":
		case b.client == "mimicry":
			me = b
		case b.client == referenceClient:
		case best == nil || cl.m.better(*b, *best):
			best = b
		}
	}

	if me == nil || best == nil || best.v <= 0 {
		return ""
	}

	r := me.v / best.v
	pct := strconv.FormatFloat(math.Round(math.Abs(r-1)*100), 'f', 0, 64) + "%"

	var s string

	switch {
	case pct == "0%":
		s = "level with " + best.client
	case r >= 2:
		s = fmt.Sprintf(cl.m.times, factor(r), best.client)
	case r > 1:
		s = fmt.Sprintf(cl.m.more, pct, best.client)
	default:
		s = fmt.Sprintf(cl.m.less, pct, best.client)
	}

	if me.ranged && best.ranged && me.lo <= best.hi && best.lo <= me.hi {
		s += " (ranges overlap)"
	}

	return "mimicry: " + s
}

// factor writes a ratio of 2 or more in words' precision: 2.6, 13, 740.
func factor(r float64) string {
	if r < 10 {
		return strconv.FormatFloat(r, 'f', 1, 64)
	}

	return thousands(r)
}

// cutAt is how far a cut bar's break sits along the plot, and how far the
// next-longest bar reaches.
const (
	cutAt      = 0.86
	cutNextMax = 0.72
)

// scale returns the value at the plot's full width and the client whose
// bar is cut: another client's value more than 4 times every other one's
// is drawn past a break, so that it does not squash the rest. mimicry's
// bar is never cut.
func (cl cell) scale(bars []hbar) (top float64, cut string) {
	var vals []hbar

	for _, b := range bars {
		if b.na == "" {
			vals = append(vals, b)
		}
	}

	slices.SortStableFunc(vals, func(a, b hbar) int {
		switch {
		case a.v > b.v:
			return -1
		case a.v < b.v:
			return 1
		}

		return 0
	})

	if len(vals) == 0 {
		return 1, ""
	}

	if len(vals) > 1 && vals[0].client != "mimicry" && vals[1].v > 0 && vals[0].v > 4*vals[1].v {
		return vals[1].v / cutNextMax, vals[0].client
	}

	if vals[0].v <= 0 {
		return 1, ""
	}

	return vals[0].v, ""
}

// draw draws the cell at x, y, w wide, and returns the y below it.
func (cl cell) draw(c *canvas, x, y, w float64) float64 {
	if textWidth(cl.title, sizeHeading)*1.05+8+textWidth(cl.note, sizeLabel) <= w {
		c.pair(x, y+14, sizeHeading, cl.title, cl.note)

		y += 26
	} else {
		c.text(x, y+14, sizeHeading, "", "b", cl.title)
		c.text(x, y+31, sizeLabel, "", "m", cl.note)

		y += 42
	}

	bars := cl.sorted()
	top, cut := cl.scale(bars)

	labels := make([]string, len(bars))
	nameW, valueW := 0.0, 0.0

	for i, b := range bars {
		nameW = math.Max(nameW, textWidth(b.client, sizeLabel))

		switch {
		case b.na != "":
			labels[i] = b.na
		case b.client == cut:
			labels[i] = figure(b.v) + cl.m.unit + " (cut)"
		default:
			labels[i] = figure(b.v) + cl.m.unit
		}

		if b.na == "" {
			valueW = math.Max(valueW, textWidth(labels[i], sizeSmall))
		}
	}

	x0 := x + nameW + 8
	span := w - (x0 - x) - valueW - 8

	for i, b := range bars {
		by := y + float64(i)*barPitch
		ty := by + barH - 3

		nameClass, valueClass := "", ""
		if b.client == "mimicry" {
			nameClass, valueClass = "b a", "b"
		}

		c.text(x0-6, ty, sizeLabel, "end", nameClass, b.client)

		if b.na != "" {
			c.text(x0+6, ty, sizeSmall, "", "m", b.na)

			continue
		}

		bw := math.Max(1, span*b.v/top)
		if b.client == cut {
			bw = span
		}

		c.rect(x0, by, bw, barH, clientClass(b.client))

		if b.client == cut {
			bx := x0 + span*cutAt
			c.line(bx-3, by+barH+2, bx+1, by-2, "brk")
			c.line(bx+3, by+barH+2, bx+7, by-2, "brk")
		}

		c.text(x0+bw+5, ty, sizeSmall, "", valueClass, labels[i])
	}

	c.line(x0, y-3, x0, y+float64(len(bars)-1)*barPitch+barH+3, "base")

	y += float64(len(bars)-1)*barPitch + barH + 18

	if s := cl.summary(); s != "" {
		for _, l := range wrap(s, w, sizeLabel) {
			c.text(x, y, sizeLabel, "", "b", l)

			y += 16
		}
	}

	for _, f := range cl.foot {
		for _, l := range wrap(f, w, sizeSmall) {
			c.text(x, y, sizeSmall, "", "m", l)

			y += 14
		}
	}

	return y
}

// gridRow is a row of cells under a heading ("" for none).
type gridRow struct {
	heading string
	cells   []cell
}

// grid draws rows of cols cells across the chart from y, a line between
// rows, and returns the y below them.
func grid(c *canvas, y float64, cols int, rows []gridRow) float64 {
	w := (chartW - 2*chartPadX - colGap*float64(cols-1)) / float64(cols)

	for i, r := range rows {
		if i > 0 {
			c.line(chartPadX, y, chartW-chartPadX, y, "sep")

			y += 14
		}

		if r.heading != "" {
			c.text(chartPadX, y+16, sizeHeading+1, "", "b", r.heading)

			y += 30
		}

		bottom := y

		for ci, cl := range r.cells {
			bottom = math.Max(bottom, cl.draw(c, chartPadX+float64(ci)*(w+colGap), y, w))
		}

		y = bottom + 8
	}

	return y
}

// loadCell is one layer 3 point (nginx, the network as it is) as a cell:
// get returns a value of a result and key names its range.
func (cs *chartSet) loadCell(title string, p target.Proto, op string, conc int, m metric,
	get func(*scenario.LoadResult) float64, key string,
) cell {
	cl := cell{
		title: title,
		note:  direction(m),
		m:     m,
	}

	bound := serverBoundKeys(cs.r.Load)
	isBound := false

	for _, name := range cs.chartClients() {
		b := hbar{
			client: name,
			na:     "n/a",
		}

		for _, lp := range cs.r.Load {
			if lp.Server != "nginx" || lp.Proto != p || lp.Op != "bytes-"+op || lp.Conc != conc || lp.Client != name ||
				lp.Condition != "" || !lp.valid() {
				continue
			}

			b.v, b.na = get(lp.Result), ""

			if r, ok := lp.Ranges[key]; ok {
				b.lo, b.hi, b.ranged = r[0], r[1], true
			}

			isBound = isBound || bound[lp.key()]
		}

		cl.bars = append(cl.bars, b)
	}

	if p == target.H1 && conc > mimicryH1Limit {
		cl.foot = append(cl.foot, fmt.Sprintf("mimicry keeps Chrome's %d connections per host and queues the rest.", mimicryH1Limit))
	}

	if isBound {
		cl.foot = append(cl.foot, "nginx was the limit here, not the clients.")
	}

	return cl
}

// direction is a panel's note: which way is better.
func direction(m metric) string {
	if m.lower {
		return "lower is better"
	}

	return "higher is better"
}

// loadGrid is layer 3 as rows of cells, a row per body size and
// concurrency, a cell per protocol.
func (cs *chartSet) loadGrid(m metric, get func(*scenario.LoadResult) float64, key string) []gridRow {
	rows := make([]gridRow, 0, len(cs.r.Settings.Bodies)*len(cs.r.Settings.Concurrency))

	for _, op := range cs.r.Settings.Bodies {
		for _, conc := range cs.r.Settings.Concurrency {
			row := gridRow{
				heading: "GET " + spaced(op) + ", " + atOnce(conc),
			}

			for _, p := range target.Protos {
				row.cells = append(row.cells, cs.loadCell(protoName(p), p, op, conc, m, get, key))
			}

			rows = append(rows, row)
		}
	}

	return rows
}

// throughput draws layer 3's requests per second.
func (cs *chartSet) throughput() (string, error) {
	if len(cs.r.Load) == 0 {
		return "", errors.New("no load points")
	}

	var c canvas

	title := "Requests per second, one client: higher is better"
	y := cs.header(&c, title,
		"One warm client sends GET to nginx over each protocol. In each panel the clients are sorted best first; under it, "+
			"mimicry against the best of the other impersonating clients.")

	y = grid(&c, y+12, len(target.Protos), cs.loadGrid(requestsPerSecond, func(r *scenario.LoadResult) float64 {
		return r.RPS
	}, "rps"))

	h := cs.caption(&c, y, cs.passesNote())

	return c.finish(h, title), nil
}

// cpu draws layer 3's CPU per request.
func (cs *chartSet) cpu() (string, error) {
	if len(cs.r.Load) == 0 {
		return "", errors.New("no load points")
	}

	var c canvas

	title := "CPU per request, one client: lower is better"
	y := cs.header(&c, title,
		"The client process's user and system time per response, in microseconds, at the same points as the requests per "+
			"second. In each panel the clients are sorted best first; under it, mimicry against the best of the other "+
			"impersonating clients.")

	y = grid(&c, y+12, len(target.Protos), cs.loadGrid(cpuMicros, func(r *scenario.LoadResult) float64 {
		return r.CPUPerRequest
	}, "cpuPerRequest"))

	h := cs.caption(&c, y, cs.passesNote())

	return c.finish(h, title), nil
}

// multiCell is one layer 6 route's points that match as a cell: get
// returns a value of a result and its range key ("" for none).
func (cs *chartSet) multiCell(title string, m metric, rt multiRoute, match func(mp multiPoint) bool,
	get func(r *scenario.MultiResult) (float64, string),
) cell {
	cl := cell{
		title: title,
		note:  direction(m),
		m:     m,
	}

	for _, name := range cs.chartClients() {
		b := hbar{
			client: name,
			na:     "n/a",
		}

		for _, mp := range cs.r.Multi {
			if mp.Proto != rt.Proto || mp.Proxy != rt.Proxy || mp.Client != name || mp.N != cs.r.Settings.MultiN ||
				mp.Run != "" || !match(mp) {
				continue
			}

			if !mp.valid() {
				if mp.NA != "" && rt.Proto == target.H3 {
					b.na = "no HTTP/3 through a proxy"
				}

				continue
			}

			v, key := get(mp.Result)

			b.v, b.na = v, ""

			if r, ok := mp.Ranges[key]; ok && key != "" {
				b.lo, b.hi, b.ranged = r[0], r[1], true
			}
		}

		cl.bars = append(cl.bars, b)
	}

	return cl
}

// routeName is a layer 6 route as a row heading.
func routeName(rt multiRoute) string {
	return protoName(rt.Proto) + " through " + proxyName(rt.Proxy)
}

// proxies draws layer 6 with a fixed identity per client at 1 KB: CPU per
// request and live heap per client.
func (cs *chartSet) proxies() (string, error) {
	fixed1KB := func(mp multiPoint) bool {
		return mp.Rotate == 0 && mp.Renew == "" && mp.Op == "bytes-1KB"
	}

	var c canvas

	title := fmt.Sprintf("%d clients, each through a proxy identity of its own: lower is better", cs.r.Settings.MultiN)
	y := cs.header(&c, title, fmt.Sprintf(
		"%d clients of one library in one process, each with its own proxy credentials, each sending GET 1 KB to nginx one "+
			"request at a time; the proxy delays everything it sends by %s, so requests per second are nearly the same for "+
			"every client and what differs is what each request and each client costs.",
		cs.r.Settings.MultiN, spaced(firstOr(strings.Fields(cs.r.Env.ProxyDelay), "?"))))

	rows := make([]gridRow, 0, len(multiRoutes))

	for _, rt := range multiRoutes {
		rows = append(rows, gridRow{
			heading: routeName(rt),
			cells: []cell{
				cs.multiCell("CPU per request", cpuMicros, rt, fixed1KB, func(r *scenario.MultiResult) (float64, string) {
					return r.CPUPerRequest, "cpuPerRequest"
				}),
				cs.multiCell("Live heap per client", memoryKB, rt, fixed1KB, func(r *scenario.MultiResult) (float64, string) {
					return r.LiveKBPerClient, "liveKbPerClient"
				}),
			},
		})
	}

	y = grid(&c, y+12, 2, rows)

	h := cs.caption(&c, y, cs.passesNote()+
		" A bar with a gap, marked (cut), is too long for the panel: its value is printed. net/http's HTTP/3 (quic-go) has no proxy setting.")

	return c.finish(h, title), nil
}

// dropped draws layer 6's client rotation with the old clients dropped:
// what they leave open at the proxy and the process's memory.
func (cs *chartSet) dropped() (string, error) {
	dropped := func(mp multiPoint) bool {
		return mp.Renew == scenario.RenewDropped
	}

	rotate := 0

	for _, mp := range cs.r.Multi {
		if mp.Renew == scenario.RenewDropped {
			rotate = mp.Rotate
		}
	}

	var c canvas

	title := "Clients dropped without closing them: lower is better"
	y := cs.header(&c, title, fmt.Sprintf(
		"%d routines, each building a new client (its library's constructor, new proxy credentials) every %d requests and "+
			"dropping the old one without closing it, for %s, GET 1 KB to nginx through the proxy. mimicry's pool closes what "+
			"a dropped client left idle; the others keep it open until it times out.",
		cs.r.Settings.MultiN, rotate, spaced(cs.r.Settings.MultiClientWindow.String())))

	rows := make([]gridRow, 0, len(multiRoutes))

	for _, rt := range multiRoutes {
		rows = append(rows, gridRow{
			heading: routeName(rt),
			cells: []cell{
				cs.multiCell("Tunnels open, per routine", tunnels, rt, dropped, func(r *scenario.MultiResult) (float64, string) {
					if r.End == nil || r.Clients == 0 {
						return 0, ""
					}

					return float64(r.End.OpenTunnels+r.End.OpenAssociations) / float64(r.Clients), ""
				}),
				cs.multiCell("Process memory", memoryMB, rt, dropped, func(r *scenario.MultiResult) (float64, string) {
					if r.End == nil {
						return 0, ""
					}

					return r.End.RSSMB, "end.rssMb"
				}),
			},
		})
	}

	y = grid(&c, y+12, 2, rows)

	h := cs.caption(&c, y, cs.passesNote()+
		" At the window's end: tunnels and UDP associations open at the proxy (one is the current client's), and resident memory."+
		" net/http's HTTP/3 (quic-go) has no proxy setting.")

	return c.finish(h, title), nil
}

// microSet is the raw micro benchmark output: each benchmark's values by
// unit, one per run.
type microSet map[string]map[string][]float64

// procSuffix is the GOMAXPROCS suffix go test adds to a benchmark's name.
var procSuffix = regexp.MustCompile(`-\d+$`)

// readMicro reads go test -bench output: every result line's values.
func readMicro(path string) (microSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	out := microSet{}

	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") {
			continue
		}

		name := procSuffix.ReplaceAllString(f[0], "")

		if out[name] == nil {
			out[name] = map[string][]float64{}
		}

		for i := 2; i+1 < len(f); i += 2 {
			v, err := strconv.ParseFloat(f[i], 64)
			if err != nil {
				return nil, fmt.Errorf("%s: %q: %w", path, line, err)
			}

			out[name][f[i+1]] = append(out[name][f[i+1]], v)
		}
	}

	return out, nil
}

// microBar is a client's bar for a benchmark: the median over its runs,
// its range, scaled by div; n/a when it never ran.
func (cs *chartSet) microBar(name, unit, client string, div float64) hbar {
	short := client
	if client == referenceClient {
		short = "nethttp"
	}

	v := cs.micro[name+"/client="+short][unit]
	if len(v) == 0 {
		return hbar{
			client: client,
			na:     "n/a",
		}
	}

	return hbar{
		client: client,
		v:      median(v) / div,
		lo:     slices.Min(v) / div,
		hi:     slices.Max(v) / div,
		ranged: true,
	}
}

// weakSpots draws where mimicry is slower: HTTP/3 bulk downloads many at
// once (one at a time beside it), HTTP/1.1 many at once to one host, and
// building a client.
func (cs *chartSet) weakSpots() (string, error) {
	rps := func(r *scenario.LoadResult) float64 {
		return r.RPS
	}

	bulk := cs.r.Settings.Bodies[len(cs.r.Settings.Bodies)-1]
	small := cs.r.Settings.Bodies[0]
	many := cs.r.Settings.Concurrency[len(cs.r.Settings.Concurrency)-1]

	h3One := cs.loadCell(fmt.Sprintf("HTTP/3, %s, one at a time", spaced(bulk)), target.H3, bulk, 1, requestsPerSecond, rps, "rps")
	h3Many := cs.loadCell(fmt.Sprintf("HTTP/3, %s, %d at once", spaced(bulk), many), target.H3, bulk, many, requestsPerSecond, rps, "rps")
	h1Many := cs.loadCell(fmt.Sprintf("HTTP/1.1, %s, %d at once", spaced(small), many), target.H1, small, many, requestsPerSecond, rps, "rps")

	for _, cl := range []*cell{&h3One, &h3Many, &h1Many} {
		cl.note = "requests/s, higher is better"
	}

	h1Many.foot = append(h1Many.foot, "Config.MaxConnsPerHost lifts the limit.")

	for _, lp := range cs.r.Load {
		if lp.Server == "nginx" && lp.Proto == target.H3 && lp.Op == "bytes-"+bulk && lp.Conc == many && lp.Client == "mimicry" &&
			lp.Condition == "" && lp.valid() && lp.UDP != nil && lp.UDP.RcvbufErrors > 0 {
			h3Many.foot = append([]string{fmt.Sprintf(
				"mimicry's socket dropped %s datagrams on a full receive buffer (Chrome's 1 MB, which it asks for); "+
					"the quic-go clients force 7 MB and dropped none.", thousands(float64(lp.UDP.RcvbufErrors)))}, h3Many.foot...)
		}
	}

	const build = "BenchmarkNewClient/proto=h2/proxy=none"

	runs := len(cs.micro[build+"/client=mimicry"]["ns/op"])
	if runs == 0 {
		return "", errors.New("no BenchmarkNewClient results")
	}

	ctor := cell{
		title: "Building a client",
		note:  "µs, lower is better",
		m:     buildMicros,
		foot: []string{fmt.Sprintf(
			"The constructor and Close in a loop, no request (HTTP/2, no proxy); the median of %d runs. "+
				"Build clients up front or one per identity, not one per request.", runs)},
	}

	for _, name := range cs.chartClients() {
		ctor.bars = append(ctor.bars, cs.microBar(build, "ns/op", name, 1000))
	}

	var c canvas

	title := "Where mimicry is slower"
	y := cs.header(&c, title,
		"Three points from the requests per second, and Go's benchmark of building a client. One bulk HTTP/3 download at "+
			"a time, mimicry leads; with many at once, it trails.")

	y = grid(&c, y+12, 2, []gridRow{
		{
			cells: []cell{
				h3One,
				h3Many,
			},
		},
		{
			cells: []cell{
				h1Many,
				ctor,
			},
		},
	})

	h := cs.caption(&c, y, cs.passesNote())

	return c.finish(h, title), nil
}
