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
// names, beside it: every number drawn is in the JSON or the micro file,
// medians with each value's range over the passes as whiskers. The output
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
			name: "relative.svg",
			draw: cs.relative,
		},
		{
			name: "proxies.svg",
			draw: cs.proxies,
		},
		{
			name: "rotation.svg",
			draw: cs.rotation,
		},
		{
			name: "micro.svg",
			draw: cs.microChart,
		},
		{
			name: "h3-bulk.svg",
			draw: cs.h3Bulk,
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
	// Text sizes: the title, subtitles and labels, small labels.
	sizeTitle = 19.0
	sizeText  = 13.0
	sizeLabel = 12.0
	sizeSmall = 10.5
)

// chartStyle colours the charts for light and dark pages: an SVG shown as
// an image cannot read the page's CSS, so it follows the reader's colour
// scheme itself, on a background of its own. mimicry is the one saturated
// colour (Okabe-Ito blue); the other impersonating clients are muted tints
// of Okabe-Ito's orange, green and purple; net/http, the reference, is
// hatched.
const chartStyle = `<style>
.bg{fill:#ffffff;stroke:#d1d9e0}
text{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans",Helvetica,Arial,sans-serif;fill:#1f2328}
.m{fill:#59636e}
.b{font-weight:600}
.grid{stroke:#d1d9e0;stroke-width:1}
.axis{stroke:#818b98;stroke-width:1}
.one{stroke:#1f2328;stroke-width:1.2;stroke-dasharray:4 3}
.wh{stroke:#1f2328;stroke-width:1.2}
.brk{stroke:#ffffff;stroke-width:3}
.mim{fill:#0072b2}
.tls{fill:#e9c27a}
.clk{fill:#8fcbb8}
.azr{fill:#dcadc8}
.oth{fill:#c3c9d0}
.ref{fill:url(#hatch);stroke:#6e7781;stroke-width:1.2}
.hl{stroke:#6e7781;stroke-width:1.6}
@media (prefers-color-scheme:dark){
.bg{fill:#0d1117;stroke:#3d444d}
text{fill:#f0f6fc}
.m{fill:#9198a1}
.grid{stroke:#2f353d}
.axis{stroke:#656c76}
.one{stroke:#f0f6fc}
.wh{stroke:#f0f6fc}
.brk{stroke:#0d1117}
.mim{fill:#56b4e9}
.tls{fill:#8f7034}
.clk{fill:#3f7d6b}
.azr{fill:#86587a}
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
		`<tspan font-size="%.1f" class="m" dx="10">%s</tspan></text>`+"\n",
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

// header draws a chart's title and subtitle and returns the y below them.
func header(c *canvas, title, subtitle string) float64 {
	c.text(chartPadX, 36, sizeTitle, "", "b", title)

	y := 60.0
	for _, l := range wrap(subtitle, 118) {
		c.text(chartPadX, y, sizeText, "", "m", l)

		y += 18
	}

	return y
}

// legend draws a swatch per client and returns the y below it.
func legend(c *canvas, y float64, clients []string) float64 {
	x := chartPadX

	for _, name := range clients {
		label := name
		if name == referenceClient {
			label += " (reference)"
		}

		c.rect(x, y-10, 12, 12, clientClass(name))
		c.text(x+17, y, sizeLabel, "", "", label)

		x += 17 + textWidth(label, sizeLabel) + 20
	}

	return y + 14
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

	y += 6
	for _, l := range lines {
		for _, w := range wrap(l, 135) {
			c.text(chartPadX, y, 11, "", "m", w)

			y += 15
		}
	}

	return y + 8
}

// passesNote says what a layer 3 or 6 value is.
func (cs *chartSet) passesNote() string {
	return fmt.Sprintf("Bars: each value's median over %d passes; whiskers: its range.", cs.r.Passes)
}

// wrap breaks s into lines of at most n characters, at spaces.
func wrap(s string, n int) []string {
	var (
		out  []string
		line string
	)

	for w := range strings.FieldsSeq(s) {
		if line != "" && len(line)+1+len(w) > n {
			out = append(out, line)
			line = ""
		}

		if line != "" {
			line += " "
		}

		line += w
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

// clientClass is a client's CSS class (its colour).
func clientClass(name string) string {
	switch name {
	case "mimicry":
		return "mim"
	case "tls-client":
		return "tls"
	case "httpcloak":
		return "clk"
	case "azuretls":
		return "azr"
	case referenceClient:
		return "ref"
	}

	return "oth"
}

// chartClients is the report's clients in the charts' order: mimicry, the
// other impersonating clients, then the reference.
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

// bar is one client's value in a group; na draws "n/a".
type bar struct {
	client string
	v      float64
	lo, hi float64
	ranged bool
	na     bool
}

// group is a cluster of bars under one label (lines).
type group struct {
	label []string
	bars  []bar
}

// panel is a bar chart of groups: a title, a unit, and the plot's height.
type panel struct {
	title, unit string
	h           float64
	groups      []group
}

// panelHeight is a panel's height around its plot: title, headroom for
// the value labels, and two lines of group labels.
const (
	panelTop    = 40.0
	panelBottom = 46.0
)

// draw draws p from y across the chart's width and returns the y below it.
// Values more than 5 times the panel's median are drawn cut, with their
// value, so one outlier does not flatten the rest.
func (p panel) draw(c *canvas, y float64) float64 {
	c.pair(chartPadX, y+14, 14, p.title, p.unit)

	var vals []float64

	for _, g := range p.groups {
		for _, b := range g.bars {
			if !b.na {
				vals = append(vals, b.v)
			}
		}
	}

	limit := 5 * median(vals)
	top := 0.0

	for _, v := range vals {
		if v <= limit {
			top = math.Max(top, v)
		}
	}

	scaleMax, step := axisScale(top)

	x0 := chartPadX + 44
	x1 := chartW - chartPadX
	y0 := y + panelTop
	y1 := y0 + p.h

	ypos := func(v float64) float64 {
		return y1 - math.Min(v, scaleMax)/scaleMax*p.h
	}

	for t := 0.0; t <= scaleMax+step/2; t += step {
		c.line(x0, ypos(t), x1, ypos(t), "grid")
		c.text(x0-6, ypos(t)+4, sizeSmall, "end", "m", tick(t, step))
	}

	c.line(x0, y1, x1, y1, "axis")

	gw := (x1 - x0) / float64(len(p.groups))

	for gi, g := range p.groups {
		gx := x0 + gw*float64(gi)
		bw := math.Min(32, (gw-24)/float64(len(g.bars))-4)
		left := gx + (gw-float64(len(g.bars))*(bw+4)+4)/2

		for bi, b := range g.bars {
			bx := left + float64(bi)*(bw+4)
			cx := bx + bw/2

			if b.na {
				c.text(cx, y1-5, sizeSmall, "middle", "m", "n/a")

				continue
			}

			c.rect(bx, ypos(b.v), bw, y1-ypos(b.v), clientClass(b.client))

			labelY := ypos(b.v) - 5

			switch {
			case b.v > scaleMax:
				// Cut: a gap across the bar near the top.
				cy := y0 + 10
				c.line(bx-2, cy+3, bx+bw+2, cy-3, "brk")
				c.line(bx-2, cy+9, bx+bw+2, cy+3, "brk")

			case b.ranged:
				c.line(cx, ypos(b.lo), cx, ypos(b.hi), "wh")
				c.line(cx-4, ypos(b.lo), cx+4, ypos(b.lo), "wh")
				c.line(cx-4, ypos(b.hi), cx+4, ypos(b.hi), "wh")

				labelY = math.Min(labelY, ypos(b.hi)-5)
			}

			c.text(cx, labelY, sizeSmall, "middle", "", figure(b.v))
		}

		for li, l := range g.label {
			c.text(gx+gw/2, y1+18+float64(li)*15, sizeLabel, "middle", "", l)
		}
	}

	return y1 + panelBottom
}

// axisScale picks an axis maximum at or above v and a tick step: 3 to 5
// ticks of 1, 2, 2.5 or 5 times a power of ten.
func axisScale(v float64) (scaleMax, step float64) {
	if v <= 0 {
		return 1, 0.25
	}

	raw := v / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))

	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		step = m * mag
		if step >= raw {
			break
		}
	}

	return math.Ceil(v/step-1e-9) * step, step
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

// relative draws mimicry's geometric mean ratios against each client per
// protocol, as the report's summary computes them: requests/s as mimicry's
// over the other's, CPU per request as the other's over mimicry's, so that
// right of 1 is mimicry ahead in both.
func (cs *chartSet) relative() (string, error) {
	if len(cs.r.Load) == 0 {
		return "", errors.New("no load points")
	}

	others := cs.chartClients()[1:]

	type row struct {
		other    string
		rps, cpu float64
	}

	type block struct {
		proto target.Proto
		n     int
		rows  []row
	}

	var (
		blocks []block
		lo, hi = 1.0, 1.0
	)

	for _, p := range target.Protos {
		bl := block{
			proto: p,
		}

		for _, o := range others {
			rps, n := ratio(cs.r.Load, p, o, func(l *scenario.LoadResult) float64 {
				return l.RPS
			}, false)
			cpu, _ := ratio(cs.r.Load, p, o, func(l *scenario.LoadResult) float64 {
				return l.CPUPerRequest
			}, false)

			if n == 0 || cpu == 0 {
				continue
			}

			bl.n = n
			bl.rows = append(bl.rows, row{
				other: o,
				rps:   rps,
				cpu:   1 / cpu,
			})
			lo = math.Min(lo, math.Min(rps, 1/cpu))
			hi = math.Max(hi, math.Max(rps, 1/cpu))
		}

		if len(bl.rows) > 0 {
			blocks = append(blocks, bl)
		}
	}

	lo = math.Min(0.75, lo/1.15)
	hi = math.Max(2, hi*1.3)

	var c canvas

	title := "mimicry against the other clients, per protocol"
	y := header(&c, title, fmt.Sprintf(
		"Geometric mean over each protocol's load points (nginx; %s bodies; %s requests at once) of mimicry's value against "+
			"the other client's, from each point's median over %d passes. Right of 1 is mimicry ahead. Server-bound points, "+
			"and HTTP/1.1 above %d at once (where mimicry queues, as Chrome does), are left out.",
		spacedAll(cs.r.Settings.Bodies, " and "), joinInts(cs.r.Settings.Concurrency, " and "), cs.r.Passes, mimicryH1Limit))

	const (
		labelX = chartPadX + 150
		panelW = 300.0
		gap    = 50.0
		rowH   = 21.0
		headH  = 26.0
	)

	px := []float64{labelX + 16, labelX + 16 + panelW + gap}
	titles := [][2]string{
		{"Requests per second", "mimicry ÷ other"},
		{"CPU per request", "other ÷ mimicry"},
	}

	y += 14
	for i, t := range titles {
		c.pair(px[i], y, 14, t[0], t[1])
	}

	y += 12

	xpos := func(i int, v float64) float64 {
		return px[i] + math.Log(v/lo)/math.Log(hi/lo)*panelW
	}

	rows := 0
	for _, bl := range blocks {
		rows += len(bl.rows)
	}

	plotTop := y
	plotBottom := y + float64(len(blocks))*headH + float64(rows)*rowH + 6

	var ticks []float64

	for _, t := range []float64{0.5, 0.75, 1, 1.5, 2, 3, 4, 5} {
		if t >= lo && t <= hi {
			ticks = append(ticks, t)
		}
	}

	for i := range px {
		for _, t := range ticks {
			class := "grid"
			if t == 1 {
				class = "one"
			}

			c.line(xpos(i, t), plotTop, xpos(i, t), plotBottom, class)
			c.text(xpos(i, t), plotBottom+15, sizeSmall, "middle", "m", strconv.FormatFloat(t, 'f', -1, 64)+"×")
		}

		c.text(xpos(i, 1)-6, plotBottom+32, sizeSmall, "end", "m", "◀ other ahead")
		c.text(xpos(i, 1)+6, plotBottom+32, sizeSmall, "", "m", "mimicry ahead ▶")
	}

	y = plotTop

	for _, bl := range blocks {
		y += headH
		c.pair(chartPadX, y-8, sizeText, protoName(bl.proto), fmt.Sprintf("%d load points", bl.n))

		for _, rw := range bl.rows {
			label := "vs " + rw.other
			if rw.other == referenceClient {
				label += " (ref.)"
			}

			c.text(labelX, y+14, sizeLabel, "end", "", label)

			for i, v := range []float64{rw.rps, rw.cpu} {
				a, b := xpos(i, 1), xpos(i, v)
				c.rect(math.Min(a, b), y+4, math.Abs(b-a), 13, barClass(rw.other))

				if v >= 1 {
					c.text(b+5, y+15, sizeSmall, "", "", fmt.Sprintf("%.2f×", v))
				} else {
					c.text(b-5, y+15, sizeSmall, "end", "", fmt.Sprintf("%.2f×", v))
				}
			}

			y += rowH
		}
	}

	h := cs.caption(&c, plotBottom+46, "Bars hatched: net/http, the reference (Go's own TLS; quic-go for HTTP/3).")

	return c.finish(h, title), nil
}

// barClass is the ratio bars' class: mimicry's colour, hatched against the
// reference.
func barClass(other string) string {
	if other == referenceClient {
		return "ref"
	}

	return "mim"
}

// tick formats an axis tick, with decimals only for a fractional step.
func tick(v, step float64) string {
	if step < 1 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}

	return thousands(v)
}

// unitSuffix splits a size or duration as the reports write it: 1KB, 5ms.
var unitSuffix = regexp.MustCompile(`^([0-9.]+)([A-Za-zµ]+)$`)

// spaced writes a size or duration with a space before its unit: 1 KB.
func spaced(s string) string {
	return unitSuffix.ReplaceAllString(s, "$1 $2")
}

func spacedAll(v []string, sep string) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = spaced(x)
	}

	return strings.Join(s, sep)
}

func joinInts(v []int, sep string) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}

	return strings.Join(s, sep)
}

// multiGroups builds a group per route from the first-count layer 6 points
// that match: get returns a value and its range key ("" for none).
func (cs *chartSet) multiGroups(match func(mp multiPoint) bool, get func(r *scenario.MultiResult) (float64, string)) []group {
	out := make([]group, 0, len(multiRoutes))

	for _, rt := range multiRoutes {
		g := group{
			label: []string{protoName(rt.Proto), proxyName(rt.Proxy)},
		}

		for _, name := range cs.chartClients() {
			b := bar{
				client: name,
				na:     true,
			}

			for _, mp := range cs.r.Multi {
				if mp.Proto != rt.Proto || mp.Proxy != rt.Proxy || mp.Client != name || mp.N != cs.r.Settings.MultiN ||
					mp.Run != "" || !mp.valid() || !match(mp) {
					continue
				}

				v, key := get(mp.Result)

				b.v = v
				b.na = false

				if r, ok := mp.Ranges[key]; ok && key != "" {
					b.lo, b.hi, b.ranged = r[0], r[1], true
				}
			}

			g.bars = append(g.bars, b)
		}

		out = append(out, g)
	}

	return out
}

// proxies draws layer 6 with a fixed identity per client at 1 KB: CPU per
// request and live heap per client.
func (cs *chartSet) proxies() (string, error) {
	fixed1KB := func(mp multiPoint) bool {
		return mp.Rotate == 0 && mp.Renew == "" && mp.Op == "bytes-1KB"
	}

	var c canvas

	title := fmt.Sprintf("%d clients, each through a proxy identity of its own", cs.r.Settings.MultiN)
	y := header(&c, title, fmt.Sprintf(
		"%d clients of one library in one process, each with its own proxy credentials, each sending GET 1 KB to nginx one "+
			"request at a time; the proxy delays everything it sends by %s. Requests per second are nearly the same for every "+
			"client (the proxy's delay sets the pace), so what differs is what each request and each client costs.",
		cs.r.Settings.MultiN, spaced(firstOr(strings.Fields(cs.r.Env.ProxyDelay), "?"))))

	y = legend(&c, y+10, cs.chartClients())

	y = panel{
		title: "CPU per request",
		unit:  "µs, the process's user + system time; lower is better",
		h:     150,
		groups: cs.multiGroups(fixed1KB, func(r *scenario.MultiResult) (float64, string) {
			return r.CPUPerRequest, "cpuPerRequest"
		}),
	}.draw(&c, y+14)

	y = panel{
		title: "Live heap per client",
		unit:  "KB at steady state; lower is better",
		h:     150,
		groups: cs.multiGroups(fixed1KB, func(r *scenario.MultiResult) (float64, string) {
			return r.LiveKBPerClient, "liveKbPerClient"
		}),
	}.draw(&c, y+4)

	h := cs.caption(&c, y, cs.passesNote()+" A cut bar is off the scale. net/http has no HTTP/3 through a proxy.")

	return c.finish(h, title), nil
}

// rotation draws layer 6's client rotation with the old clients dropped:
// what they leave open at the proxy and the process's memory.
func (cs *chartSet) rotation() (string, error) {
	dropped := func(mp multiPoint) bool {
		return mp.Renew == scenario.RenewDropped
	}

	var (
		c       canvas
		rotate  int
		window  = cs.r.Settings.MultiClientWindow
		routine = cs.r.Settings.MultiN
	)

	for _, mp := range cs.r.Multi {
		if mp.Renew == scenario.RenewDropped {
			rotate = mp.Rotate
		}
	}

	title := "A new client per identity, the old one dropped without closing it"
	y := header(&c, title, fmt.Sprintf(
		"%d routines, each building a new client (its library's constructor, new proxy credentials) every %d requests and "+
			"dropping the old one, as code that makes a client per proxy and never closes it does; GET 1 KB to nginx through "+
			"the proxy for %s. mimicry's pool closes what a dropped client left idle; the others keep it open until it times out.",
		routine, rotate, spaced(window.String())))

	y = legend(&c, y+10, cs.chartClients())

	y = panel{
		title: "Tunnels left open at the proxy",
		unit:  "per routine at the window's end (one is the current client's); lower is better",
		h:     140,
		groups: cs.multiGroups(dropped, func(r *scenario.MultiResult) (float64, string) {
			if r.End == nil || r.Clients == 0 {
				return 0, ""
			}

			return float64(r.End.OpenTunnels+r.End.OpenAssociations) / float64(r.Clients), ""
		}),
	}.draw(&c, y+14)

	y = panel{
		title: "Process memory",
		unit:  "MB resident at the window's end; lower is better",
		h:     140,
		groups: cs.multiGroups(dropped, func(r *scenario.MultiResult) (float64, string) {
			if r.End == nil {
				return 0, ""
			}

			return r.End.RSSMB, "end.rssMb"
		}),
	}.draw(&c, y+4)

	h := cs.caption(&c, y, cs.passesNote()+" Tunnels: UDP associations included, no whiskers. net/http has no HTTP/3 through a proxy.")

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
// whiskers their range, scaled by div; n/a when it never ran.
func (cs *chartSet) microBar(name, unit, client string, div float64) bar {
	short := client
	if client == referenceClient {
		short = "nethttp"
	}

	v := cs.micro[name+"/client="+short][unit]
	if len(v) == 0 {
		return bar{
			client: client,
			na:     true,
		}
	}

	return bar{
		client: client,
		v:      median(v) / div,
		lo:     slices.Min(v) / div,
		hi:     slices.Max(v) / div,
		ranged: true,
	}
}

// microChart draws layers 1 and 2: allocations per request on a warm
// connection, and the time to a first response on a new one.
func (cs *chartSet) microChart() (string, error) {
	groups := func(bench, unit string, div float64) []group {
		out := make([]group, 0, len(target.Protos))

		for _, p := range target.Protos {
			g := group{
				label: []string{protoName(p)},
			}

			for _, name := range cs.chartClients() {
				g.bars = append(g.bars, cs.microBar(fmt.Sprintf(bench, p), unit, name, div))
			}

			out = append(out, g)
		}

		return out
	}

	runs := 0
	for _, v := range cs.micro {
		runs = max(runs, len(v["ns/op"]))
	}

	if runs == 0 {
		return "", errors.New("no micro results")
	}

	var c canvas

	title := "One request, and one new connection"
	y := header(&c, title,
		"Go benchmarks against nginx, one client at a time. A warm connection: GET /json, the allocations the client makes "+
			"per request. A new connection: GET /json on a new client, the full TLS (or QUIC) handshake included. mimicry "+
			"has no HTTP/3 row there: a new client starts on TCP and moves to HTTP/3 after the server's Alt-Svc, as Chrome does.")

	y = legend(&c, y+10, cs.chartClients())

	y = panel{
		title:  "Allocations per request",
		unit:   "warm connection, GET; lower is better",
		h:      140,
		groups: groups("BenchmarkRequest/server=nginx/proto=%s/method=GET", "allocs/op", 1),
	}.draw(&c, y+14)

	y = panel{
		title:  "New connection, full handshake",
		unit:   "µs to the first response; lower is better",
		h:      140,
		groups: groups("BenchmarkConnect/server=nginx/proto=%s/handshake=full", "ns/op", 1000),
	}.draw(&c, y+4)

	h := cs.caption(&c, y, fmt.Sprintf(
		"Bars: the median of %d runs (-count 1 each, the clients in another order each run); whiskers: their range. A cut bar is off the scale.",
		runs))

	return c.finish(h, title), nil
}

// h3Bulk draws the weak spot: HTTP/3 with 100 KB bodies, one at a time and
// many at once.
func (cs *chartSet) h3Bulk() (string, error) {
	groups := make([]group, 0, len(cs.r.Settings.Concurrency))

	drops := ""

	for _, conc := range cs.r.Settings.Concurrency {
		g := group{
			label: []string{fmt.Sprintf("%d at once", conc)},
		}

		bound := false

		for _, name := range cs.chartClients() {
			b := bar{
				client: name,
				na:     true,
			}

			for _, lp := range cs.r.Load {
				if lp.Server != "nginx" || lp.Proto != target.H3 || lp.Op != "bytes-100KB" || lp.Conc != conc ||
					lp.Client != name || lp.Condition != "" || !lp.valid() {
					continue
				}

				b.v, b.na = lp.Result.RPS, false

				if r, ok := lp.Ranges["rps"]; ok {
					b.lo, b.hi, b.ranged = r[0], r[1], true
				}

				bound = bound || lp.Result.ServerBound != ""

				if name == "mimicry" && lp.UDP != nil && lp.UDP.RcvbufErrors > 0 {
					drops = fmt.Sprintf(" At %d at once mimicry's socket dropped %s datagrams on a full receive buffer; the others none.",
						conc, thousands(float64(lp.UDP.RcvbufErrors)))
				}
			}

			g.bars = append(g.bars, b)
		}

		if bound {
			g.label = append(g.label, "nginx's worker the limit")
		}

		groups = append(groups, g)
	}

	var c canvas

	title := "Where mimicry is slower: HTTP/3 bulk downloads, many at once"
	y := header(&c, title,
		"GET 100 KB from nginx over one HTTP/3 connection. mimicry asks for Chrome's 1 MB UDP receive buffer; the quic-go "+
			"clients ask for 7 MB and force it (the client had CAP_NET_ADMIN). nginx serves the connection from one worker, "+
			"which ran near a full core for every client at 64 at once."+drops)

	y = legend(&c, y+10, cs.chartClients())

	y = panel{
		title:  "Requests per second",
		unit:   "100 KB responses' worth of body bytes per second; higher is better",
		h:      170,
		groups: groups,
	}.draw(&c, y+14)

	h := cs.caption(&c, y, cs.passesNote())

	return c.finish(h, title), nil
}
