package server

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/pageshape"
)

// Replay returns a scenario that loads the shape of a recorded real page
// (cmd/pageshape) with synthetic content, each real host served under .test
// (see ReplayHost). Requests go out at their recorded times with the recorded
// destination, mode, status, size and cookie shapes; the capture is the whole
// page (capture.Page).
func Replay(p *pageshape.Page) (*Scenario, error) {
	if len(p.Requests) == 0 {
		return nil, fmt.Errorf("page %s has no requests", p.Site)
	}
	rp := newReplay(p)
	return &Scenario{
		Name:  "replay-" + p.Site,
		Doc:   rp.stats.String(),
		multi: rp,
	}, nil
}

// maxBody caps a replayed response body: what matters is the flow-control
// pattern, which a few MiB already shows.
const maxBody = 8 << 20

// maxLength caps how long a replayed page keeps going.
const maxLength = 30 * time.Second

type replay struct {
	page *pageshape.Page
	// names are the replay hostnames, by host; byName reverses them.
	names  []string
	byName map[string]int
	// top is the request that loads the top document: the end of the
	// navigation's redirect chain.
	top int
	// docs are the requests whose responses are generated documents (top
	// and iframes), and jobs the requests each one schedules.
	docs map[int]bool
	jobs map[int][]int
	// next follows redirects: request -> the request its redirect leads to.
	next map[int]int
	// onlyH1 marks hosts the real page reached only over HTTP/1.1.
	onlyH1 []bool
	// cookies lists, per host, the cookies set from it: /cleanup deletes
	// them.
	cookies map[int][]pageshape.Cookie
	runFor  time.Duration
	stats   replayStats
}

// replayStats says how faithfully a page is replayed.
type replayStats struct {
	site       string
	requests   int
	scheduled  int
	redirects  int
	preflights int
	// approximated are requests sent with fetch() although the page used
	// another API (video, manifest, worker); skipped ones the browser
	// makes by itself or replay cannot make (service workers, reports,
	// other top-level documents); reparented ones came from a frame whose
	// document replay does not have and run in the top document instead.
	approximated map[string]int
	skipped      map[string]int
	reparented   int
}

func (s replayStats) String() string {
	return fmt.Sprintf("replay of %s: %d requests: %d scheduled, %d redirect hops and %d preflights left to the browser; approximated with fetch %v; skipped %v; %d moved to the top document",
		s.site, s.requests, s.scheduled, s.redirects, s.preflights, s.approximated, s.skipped, s.reparented)
}

// ReplayHost maps a real host under .test with its site kept apart:
// "www.nike.com" (site "nike.com") becomes "www.nike-com.test", whose site
// is nike-com.test.
func ReplayHost(h pageshape.Host) string {
	site := h.Site
	if site == "" || !strings.HasSuffix(h.Name, site) {
		return strings.ReplaceAll(h.Name, ".", "-") + ".test"
	}
	return strings.TrimSuffix(h.Name, site) + strings.ReplaceAll(site, ".", "-") + ".test"
}

func newReplay(p *pageshape.Page) *replay {
	rp := &replay{
		page:    p,
		byName:  map[string]int{},
		docs:    map[int]bool{},
		jobs:    map[int][]int{},
		next:    map[int]int{},
		onlyH1:  make([]bool, len(p.Hosts)),
		cookies: map[int][]pageshape.Cookie{},
		stats: replayStats{
			site:         p.Site,
			requests:     len(p.Requests),
			approximated: map[string]int{},
			skipped:      map[string]int{},
		},
	}
	for i, h := range p.Hosts {
		name := ReplayHost(h)
		rp.names = append(rp.names, name)
		rp.byName[name] = i
	}
	h2 := make([]bool, len(p.Hosts))
	var last int
	for i, r := range p.Requests {
		if r.RedirectFrom >= 0 {
			rp.next[r.RedirectFrom] = i
		}
		if r.Protocol != "h1" {
			h2[r.Host] = true
		}
		if len(r.SetCookies) > 0 {
			rp.cookies[r.Host] = append(rp.cookies[r.Host], r.SetCookies...)
		}
		last = max(last, r.At)
	}
	for i := range p.Hosts {
		rp.onlyH1[i] = !h2[i]
	}
	rp.runFor = min(time.Duration(last)*time.Millisecond, maxLength)
	rp.top = rp.end(0)
	rp.docs[rp.top] = true
	for i, r := range p.Requests {
		if r.Dest == "iframe" && r.RedirectFrom < 0 {
			rp.docs[rp.end(i)] = true
		}
	}
	for i, r := range p.Requests {
		switch {
		case i == 0:
		case r.RedirectFrom >= 0:
			rp.stats.redirects++
		case r.Method == "OPTIONS":
			rp.stats.preflights++
		case r.Dest == "serviceworker" || r.Dest == "report" || r.Dest == "document":
			rp.stats.skipped[r.Dest]++
		default:
			doc := rp.frameOf(i)
			rp.jobs[doc] = append(rp.jobs[doc], i)
			rp.stats.scheduled++
			if !elementDests[r.Dest] && r.Dest != "empty" {
				rp.stats.approximated[r.Dest]++
			}
		}
	}
	return rp
}

// elementDests are the destinations replay loads with their own element or
// API; others go through fetch().
var elementDests = map[string]bool{
	"script": true, "style": true, "image": true, "font": true, "iframe": true,
}

// end follows i's redirects to the request that got a final response.
func (rp *replay) end(i int) int {
	for {
		j, ok := rp.next[i]
		if !ok {
			return i
		}
		i = j
	}
}

// frameOf finds the document that made request i: the latest document of
// i's initiator requested before i, or the top document. A frame nested in
// a frame of its own host is a document of that host too, but never its
// own parent.
func (rp *replay) frameOf(i int) int {
	r := rp.page.Requests[i]
	initiator := r.Initiator
	topHost := rp.page.Requests[rp.top].Host
	if initiator < 0 || initiator == topHost {
		return rp.top
	}
	best := -1
	for d := range rp.docs {
		if d != rp.top && d < i && rp.page.Requests[d].Host == initiator && d > best {
			best = d
		}
	}
	if best < 0 {
		rp.stats.reparented++
		return rp.top
	}
	return best
}

// hosts lists the replay hostnames, the navigation's first.
func (rp *replay) hosts() []string {
	start := rp.names[rp.page.Requests[0].Host]
	out := []string{
		start,
	}
	for _, n := range rp.names {
		if n != start {
			out = append(out, n)
		}
	}
	return out
}

// certNames puts on a host's certificate the hosts whose requests the
// real page sent on that host's connections.
func (rp *replay) certNames(host string) ([]string, bool) {
	i, ok := rp.byName[host]
	if !ok {
		return nil, false
	}
	names := []string{
		host,
	}
	for _, s := range rp.page.Hosts[i].Shared {
		names = append(names, rp.names[s])
	}
	return names, true
}

// h1Only: hosts the page reached only over HTTP/1.1 are offered nothing
// else.
func (rp *replay) h1Only(host string) bool {
	i, ok := rp.byName[host]
	return ok && rp.onlyH1[i]
}

func (rp *replay) length() time.Duration {
	return rp.runFor
}

// url is where request i is replayed.
func (rp *replay) url(i int, port string) string {
	return "https://" + net.JoinHostPort(rp.names[rp.page.Requests[i].Host], port) + rp.path(i)
}

// path is request i's path: "/" for the navigation, else "/r<i>" padded to
// the recorded length, with a query of the recorded length.
func (rp *replay) path(i int) string {
	if i == 0 {
		return "/"
	}
	r := rp.page.Requests[i]
	p := "/r" + strconv.Itoa(i)
	if r.PathLen > len(p)+1 {
		p += "/" + synthetic("alnum", r.PathLen-len(p)-1, "path "+strconv.Itoa(i))
	}
	if r.QueryLen > 0 {
		p += "?" + synthetic("text", r.QueryLen, "query "+strconv.Itoa(i))
	}
	return p
}

// request finds the recorded request an incoming one replays.
func (rp *replay) request(host, path string) (int, bool) {
	h, ok := rp.byName[host]
	if !ok {
		return 0, false
	}
	path, _, _ = strings.Cut(path, "?")
	if path == "/" {
		return 0, h == rp.page.Requests[0].Host
	}
	rest, ok := strings.CutPrefix(path, "/r")
	if !ok {
		return 0, false
	}
	rest, _, _ = strings.Cut(rest, "/")
	i, err := strconv.Atoi(rest)
	if err != nil || i <= 0 || i >= len(rp.page.Requests) || rp.page.Requests[i].Host != h {
		return 0, false
	}
	return i, true
}

func (rp *replay) answer(req request) response {
	host, port := splitAuthority(req.host)
	var cors []hpack.HeaderField
	if req.origin != "" {
		cors = corsAllow(req.origin)
	}
	if req.method == "OPTIONS" {
		return response{status: 204, contentType: "text/plain", headers: preflightAllow(cors, "GET, POST, PUT, PATCH, DELETE, OPTIONS", req.preflightHeaders)}
	}
	switch req.path {
	case "/done", "/cleanup":
		r := response{status: 200, contentType: "text/plain", body: []byte("ok\n"), headers: cors}
		if h, ok := rp.byName[host]; ok {
			r.headers = append(r.headers, rp.deleteCookies(h)...)
		}
		return r
	}
	i, ok := rp.request(host, req.path)
	if !ok {
		return response{status: 404, contentType: "text/plain", body: []byte("not found\n"), headers: cors}
	}
	rec := rp.page.Requests[i]
	r := response{status: rec.Status, contentType: rec.ContentType, headers: cors}
	if r.status < 200 || r.status == 304 {
		r.status = 200
	}
	if r.status >= 300 && r.status < 400 {
		if j, ok := rp.next[i]; ok {
			r.headers = append(r.headers, hpack.HeaderField{
				Name:  "location",
				Value: rp.url(j, port),
			})
		} else {
			r.status = 200
		}
	}
	for k, c := range rec.SetCookies {
		r.headers = append(r.headers, hpack.HeaderField{
			Name:  "set-cookie",
			Value: rp.setCookie(rec.Host, c, "cookie "+strconv.Itoa(i)+" "+strconv.Itoa(k)),
		})
	}
	switch {
	case rp.docs[i]:
		r.contentType = "text/html; charset=utf-8"
		r.body = rp.document(i, port)
	case r.status == 204 || (r.status >= 300 && r.status < 400):
	default:
		if r.contentType == "" {
			r.contentType = "application/octet-stream"
		}
		r.body = filler(rec.Dest, r.contentType, min(rec.Size, maxBody))
	}
	return r
}

// cookieName is cookie c's synthetic name: "c<ID>", then "_" and padding up
// to the recorded length where that leaves room. The "_" keeps names
// unique: c1_2 can never be c12.
func cookieName(c pageshape.Cookie) string {
	name := "c" + strconv.Itoa(c.ID)
	if c.NameLen >= len(name)+1 {
		name += "_" + synthetic("alnum", c.NameLen-len(name)-1, "cookie name "+strconv.Itoa(c.ID))
	}
	return name
}

// setCookie is the Set-Cookie value replaying c from host. Every cookie is
// Secure (the replay is HTTPS) and lives on "/", since replay paths are not
// the real ones; persistent cookies live 10 minutes, and /cleanup deletes
// them sooner.
func (rp *replay) setCookie(host int, c pageshape.Cookie, seed string) string {
	v := cookieName(c) + "=" + synthetic(c.Class, c.ValueLen, seed) + "; Path=/; Secure"
	if c.SiteWide {
		v += "; Domain=" + rp.siteName(host)
	}
	if c.HTTPOnly {
		v += "; HttpOnly"
	}
	if c.SameSite != "" {
		v += "; SameSite=" + c.SameSite
	}
	switch {
	case c.Delete:
		v += "; Max-Age=0"
	case c.Persistent:
		v += "; Max-Age=600"
	}
	return v
}

// siteName is the replay name of host's site.
func (rp *replay) siteName(host int) string {
	h := rp.page.Hosts[host]
	return ReplayHost(pageshape.Host{
		Name: h.Site,
		Site: h.Site,
	})
}

// deleteCookies deletes every cookie set from host, in each form it was set.
func (rp *replay) deleteCookies(host int) []hpack.HeaderField {
	var out []hpack.HeaderField
	seen := map[string]bool{}
	for _, c := range rp.cookies[host] {
		v := cookieName(c) + "=; Path=/; Secure; Max-Age=0"
		if c.SiteWide {
			v += "; Domain=" + rp.siteName(host)
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, hpack.HeaderField{
				Name:  "set-cookie",
				Value: v,
			})
		}
	}
	return out
}

// job is one scheduled request, as the document's script reads it.
type job struct {
	URL         string            `json:"u"`
	Dest        string            `json:"d"`
	Mode        string            `json:"m"`
	Method      string            `json:"x"`
	Credentials bool              `json:"c,omitempty"`
	Headers     map[string]string `json:"h,omitempty"`
	Body        int               `json:"b,omitempty"`
	// At is when to start it, in milliseconds after the document's
	// script started.
	At int `json:"t"`
}

// document is the HTML replaying request doc's page: a script running its
// jobs and, for the top document, cleaning up and asking for /done once the
// page's recorded length has passed. It is padded to the recorded size.
func (rp *replay) document(doc int, port string) []byte {
	start := rp.page.Requests[doc].At
	jobs := make([]job, 0, len(rp.jobs[doc]))
	for _, i := range rp.jobs[doc] {
		r := rp.page.Requests[i]
		j := job{
			URL:         rp.url(i, port),
			Dest:        r.Dest,
			Mode:        r.Mode,
			Method:      r.Method,
			Credentials: r.CookieCrumbs > 0,
			Body:        r.Body,
			At:          max(0, r.At-start),
		}
		for k, h := range r.Headers {
			if j.Headers == nil {
				j.Headers = map[string]string{}
			}
			v := h.Value
			if v == "" {
				v = synthetic(h.Class, h.Len, "header "+strconv.Itoa(i)+" "+strconv.Itoa(k))
			}
			j.Headers[h.Name] = v
		}
		jobs = append(jobs, j)
	}
	slices.SortStableFunc(jobs, func(a, b job) int {
		return a.At - b.At
	})
	jobsJSON, _ := json.Marshal(jobs)
	var cleanup []string
	if doc == rp.top {
		for h := range rp.cookies {
			cleanup = append(cleanup, "https://"+net.JoinHostPort(rp.names[h], port)+"/cleanup")
		}
		slices.Sort(cleanup)
	}
	cleanupJSON, _ := json.Marshal(cleanup)
	script := strings.NewReplacer(
		"__JOBS__", string(jobsJSON),
		"__TOP__", strconv.FormatBool(doc == rp.top),
		"__END__", strconv.FormatInt(rp.runFor.Milliseconds()-int64(start), 10),
		"__CLEANUP__", string(cleanupJSON),
	).Replace(replayScript)
	body := "<script>" + script + "</script>\n"
	html := htmlPage("replay", "", body)
	// Padded with a comment to the recorded size.
	if n := min(rp.page.Requests[doc].Size, maxBody) - len(html) - len("<!---->\n"); n > 0 {
		html = htmlPage("replay", "", body+"<!--"+synthetic("text", n, "document "+strconv.Itoa(doc))+"-->\n")
	}
	return html
}

// replayScript runs a document's jobs. Each destination gets the element or
// API a page uses for it, which sets sec-fetch-dest and the mode; the rest
// go through fetch(). The top document waits for its jobs and the page's
// recorded length, deletes every cookie, and asks for /done.
const replayScript = `(() => {
const jobs = __JOBS__, top = __TOP__, end = Math.min(__END__ + 5000, 35000), t0 = performance.now();
const sleep = ms => new Promise(r => setTimeout(r, ms));
function load(j) {
  return new Promise(done => {
    const fin = () => done(), cors = j.m === 'cors', xo = j.c ? 'use-credentials' : 'anonymous';
    let el;
    switch (j.d) {
    case 'script':
      el = document.createElement('script'); el.async = true;
      if (cors) el.crossOrigin = xo;
      el.onload = el.onerror = fin; el.src = j.u; document.head.appendChild(el); return;
    case 'style':
      el = document.createElement('link'); el.rel = 'stylesheet';
      if (cors) el.crossOrigin = xo;
      el.onload = el.onerror = fin; el.href = j.u; document.head.appendChild(el); return;
    case 'image':
      el = new Image();
      if (cors) el.crossOrigin = xo;
      el.onload = el.onerror = fin; el.src = j.u; return;
    case 'font':
      new FontFace('f' + Math.random().toString(36).slice(2), 'url(' + j.u + ')').load().then(fin, fin); return;
    case 'iframe':
      el = document.createElement('iframe'); el.onload = fin; el.src = j.u; document.body.appendChild(el);
      setTimeout(fin, 30000); return;
    }
    const init = {method: j.x, credentials: j.c ? 'include' : 'same-origin', headers: j.h || {}};
    if (j.m === 'no-cors') init.mode = 'no-cors';
    if (j.b > 0 && j.x !== 'GET' && j.x !== 'HEAD') init.body = 'x'.repeat(j.b);
    fetch(j.u, init).then(r => r.arrayBuffer()).then(fin, fin);
  });
}
const runs = jobs.map(j => sleep(j.t - (performance.now() - t0)).then(() => load(j)));
if (top) (async () => {
  await Promise.race([Promise.all(runs), sleep(end)]);
  await sleep(end - (performance.now() - t0));
  await Promise.all(__CLEANUP__.map(u => fetch(u, {credentials: 'include', mode: 'no-cors'}).catch(() => {})));
  fetch('/done');
})();
})();`

// filler is a body of n bytes that parses as the type it claims, so the
// browser treats it as the real one would (a script runs, an image
// decodes).
func filler(dest, contentType string, n int) []byte {
	switch {
	case dest == "image" || strings.HasPrefix(contentType, "image/"):
		// Decoders ignore bytes after the PNG's end.
		return append(slices.Clip(png), make([]byte, max(0, n-len(png)))...)
	case strings.Contains(contentType, "javascript") || strings.Contains(contentType, "css") || dest == "script" || dest == "style":
		if n < 4 {
			return []byte(strings.Repeat(" ", n))
		}
		return []byte("/*" + strings.Repeat("x", n-4) + "*/")
	case strings.Contains(contentType, "html"):
		if n < 7 {
			return []byte(strings.Repeat(" ", n))
		}
		return []byte("<!--" + strings.Repeat("x", n-7) + "-->")
	}
	return make([]byte, n)
}

// synthetic is a deterministic value of n characters of the given class
// (pageshape.Classify), so its Huffman length is like the real one's. The
// same seed gives the same value.
func synthetic(class string, n int, seed string) string {
	const (
		digits = "0123456789"
		hex    = "0123456789abcdef"
		alnum  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	)
	chars := alnum
	switch class {
	case "digits":
		chars = digits
	case "hex":
		chars = hex
	case "base64":
		chars = alnum + "+/"
	case "base64url":
		chars = alnum + "-_"
	case "urlencoded":
		chars = alnum + "%"
	case "text":
		chars = alnum + ".:|-_"
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	x := h.Sum64() | 1
	b := make([]byte, n)
	for i := range b {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b[i] = chars[x%uint64(len(chars))]
	}
	return string(b)
}
