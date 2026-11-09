package e2e

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/diff"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// mimicry uses an https:// proxy as the browser does, against its captures
// through the capture proxy behind TLS (decision 0065), with
// Config.BrowserProxyAuth:
//   - "h2" (proxy-https, the proxy offering h2): the ClientHello to the
//     proxy, to its name, and h2 picked; the connection's opening frames
//     and SETTINGS; the CONNECT's header fields, its header block's HPACK
//     shape, its HEADERS frame (flags, priority) and what follows it on
//     the stream (Firefox's top-up); the tunnel carries the profile's
//     direct hello.
//   - "h1" (proxy-https-h1, http/1.1 only): the hello and the CONNECT's
//     lines.
//   - "auth" (proxy-https-auth: the resume page through the proxy, with
//     credentials): the credential flow over HTTP/2 (challenge, retry on
//     a new stream, unasked after), proxy-authorization's encoding, every
//     tunnel a stream of one connection, the code the tunnels' streams are
//     reset with, and whether hellos through the tunnels resume.
//   - "h1-auth" (proxy-https-h1-auth): the credential flow over HTTP/1.1,
//     each tunnel a connection of its own, whose hellos resume (Chrome)
//     or do not (Firefox) as the browser's, full and resumed hellos alike.
//   - "auth-upfront": by default credentials go on every CONNECT, as the
//     browser places them, and no 407 is asked for (decision 0039).
//
// A profile without proxy.https refuses the proxy instead
// (TestUnmeasuredProxyIsAnError).
func TestHTTPSProxyMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			if p.Proxy == nil || p.Proxy.HTTPS == nil {
				t.Skipf("%s has no proxy.https: its https:// proxies are refused (TestUnmeasuredProxyIsAnError)", name)
			}

			// The browser's hellos to the proxy, full and resumed, from
			// every scenario: a scenario's tunnels may all resume
			// sessions the browser's own traffic left.
			hellos := map[bool][]*profile.TLS{}
			for _, sc := range []string{"proxy-https", "proxy-https-h1", "proxy-https-auth", "proxy-https-h1-auth"} {
				want := loadHTTPSProxy(t, p, sc)
				for i, sample := range want.reqs {
					for _, n := range tunnelConns(sample) {
						if c := proxyConn(want.conns[i], n); c != nil && c.Hello != nil {
							hellos[c.Resumed] = append(hellos[c.Resumed], c.Hello)
						}
					}
				}
			}

			t.Run("h2", func(t *testing.T) {
				want := loadHTTPSProxy(t, p, "proxy-https")
				s := startProxied(t, p, scenario(t, "proxy-https"), nil, true)
				s.get("https", server.CaptureHost, "/")

				got := s.proxy.Requests()
				if len(got) != 1 {
					t.Fatalf("%d proxy requests, want 1: %v", len(got), got)
				}

				checkConnectLines(t, got[0], want)

				conns := s.proxy.TLSConns()
				checkProxyHellos(t, p, conns, got, hellos, "h2")
				checkH2Connect(t, conns, got[0], want)
				s.checkTunnelledHello(p)
			})
			t.Run("h1", func(t *testing.T) {
				want := loadHTTPSProxy(t, p, "proxy-https-h1")
				s := startProxied(t, p, scenario(t, "proxy-https-h1"), nil, true)
				s.get("https", server.CaptureHost, "/")

				got := s.proxy.Requests()
				if len(got) != 1 {
					t.Fatalf("%d proxy requests, want 1: %v", len(got), got)
				}

				checkConnectLines(t, got[0], want)
				checkProxyHellos(t, p, s.proxy.TLSConns(), got, hellos, "http/1.1")
				s.checkTunnelledHello(p)
			})
			t.Run("auth", func(t *testing.T) {
				want := loadHTTPSProxy(t, p, "proxy-https-auth")
				sc := scenario(t, "proxy-https-auth")
				s := startProxied(t, p, sc, url.UserPassword(server.ProxyUser, server.ProxyPassword), true)
				s.resumePage(sc)

				got := s.proxy.Requests()
				conns := s.proxy.TLSConns()

				checkAuthFlow(t, connectFlow(got), want, collapse407)
				checkProxyHellos(t, p, conns, got, hellos, "h2")

				if tunnels := tunnelConns(got); len(tunnels) != 1 {
					t.Errorf("tunnels on proxy connections %v, want all on one, as the browser's", tunnels)
				}

				checkAuthEncoding(t, conns, got, want)
				checkResets(t, conns, got, want)
				checkTunnelledResumption(t, got, want)
			})
			t.Run("h1-auth", func(t *testing.T) {
				want := loadHTTPSProxy(t, p, "proxy-https-h1-auth")
				sc := scenario(t, "proxy-https-h1-auth")
				s := startProxied(t, p, sc, url.UserPassword(server.ProxyUser, server.ProxyPassword), true)
				s.resumePage(sc)

				got := s.proxy.Requests()
				conns := s.proxy.TLSConns()

				checkAuthFlow(t, connectFlow(got), want, nil)

				for n := range 2 {
					g, w := nthOK(got, n), nthOK(want.reqs[0], n)
					if !slices.Equal(portless(g.Lines), portless(w.Lines)) {
						t.Errorf("CONNECT %d with credentials:\n got %q\nwant %q", n+1, portless(g.Lines), portless(w.Lines))
					}
				}

				checkProxyHellos(t, p, conns, got, hellos, "http/1.1")
				checkProxyResumption(t, conns, got, want)
				checkTunnelledResumption(t, got, want)
			})
			t.Run("auth-upfront", func(t *testing.T) {
				want := loadHTTPSProxy(t, p, "proxy-https-auth")
				sc := scenario(t, "proxy-https-auth")
				hosts, _, ok := sc.MultiHost()

				if !ok || len(hosts) == 0 {
					t.Fatalf("scenario %s serves no hosts", sc.Name)
				}

				s := startProxied(t, p, sc, url.UserPassword(server.ProxyUser, server.ProxyPassword), false)
				// A tunnel each: the resume page closes every connection.
				for _, host := range hosts[:2] {
					s.get("https", host, "/k/r1.so")
				}

				got := s.proxy.Requests()
				if flow, w := connectFlow(got), []string{"conn 1 200 credentials", "conn 1 200 credentials"}; !slices.Equal(flow, w) {
					t.Errorf("CONNECT flow (connection, status, credentials):\n got %v\nwant %v", flow, w)
				}

				// Fields as the browser's first CONNECT with credentials,
				// the hosts aside.
				w := withoutAuthority(nthOK(want.reqs[0], 0).Lines)
				for i, r := range got {
					if g := withoutAuthority(r.Lines); !slices.Equal(g, w) {
						t.Errorf("CONNECT %d:\n got %q\nwant %q", i+1, g, w)
					}
				}

				s.checkTunnelledHello(p)
			})
		})
	}
}

// httpsProxyCapture is an https:// proxy scenario's capture: per sample,
// the CONNECTs to capture hosts and the connections to the proxy.
type httpsProxyCapture struct {
	reqs  [][]capture.ProxyRequest
	conns [][]capture.ProxyTLS
}

// loadHTTPSProxy reads p's capture of the https:// proxy scenario name.
func loadHTTPSProxy(t *testing.T, p *profile.Spec, name string) httpsProxyCapture {
	t.Helper()

	reqs := loadSamples[[]capture.ProxyRequest](t, p, name, ".proxytrace.json")

	path, file, rw := capturePath(t, p, name, ".proxytls.json.gz")
	skipIfMissing(t, path, file)

	if rw != nil {
		t.Fatalf("%s is inherited, and its hellos cannot be rewritten", file)
	}

	conns, err := capture.LoadProxyTLS(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(conns) != len(reqs) {
		t.Fatalf("%s: %d samples, its proxy trace %d", file, len(conns), len(reqs))
	}

	return httpsProxyCapture{
		reqs:  reqs,
		conns: conns,
	}
}

// conn is connection n of conns, or nil.
func proxyConn(conns []capture.ProxyTLS, n int) *capture.ProxyTLS {
	for i := range conns {
		if conns[i].Conn == n {
			return &conns[i]
		}
	}

	return nil
}

// tunnelConns are the proxy connections that carried an accepted CONNECT,
// in order.
func tunnelConns(reqs []capture.ProxyRequest) []int {
	var out []int

	for _, r := range reqs {
		if r.Status == http.StatusOK && !slices.Contains(out, r.Conn) {
			out = append(out, r.Conn)
		}
	}

	return out
}

// checkConnectLines checks a CONNECT's lines (over HTTP/2 its decoded
// fields, pseudo-header fields first) against the browser's first
// accepted one in some sample.
func checkConnectLines(t *testing.T, got capture.ProxyRequest, want httpsProxyCapture) {
	t.Helper()

	if !slices.ContainsFunc(want.reqs, func(sample []capture.ProxyRequest) bool {
		return slices.Equal(portless(nthOK(sample, 0).Lines), portless(got.Lines))
	}) {
		t.Errorf("CONNECT:\n got %q\nwant %q", portless(got.Lines), portless(nthOK(want.reqs[0], 0).Lines))
	}
}

// checkProxyHellos checks the connections to the proxy that carried
// mimicry's CONNECTs: each to the proxy's name, over proto, with the
// browser's hello to the proxy (browser, by whether it resumed), full or
// resumed as it is. GREASE ECH's choices are the profile's sets (tls,
// tls.resumption), which hellogen gathers from every connection, these
// included.
func checkProxyHellos(t *testing.T, p *profile.Spec, conns []capture.ProxyTLS, reqs []capture.ProxyRequest, browser map[bool][]*profile.TLS, proto string) {
	t.Helper()

	used := tunnelConns(reqs)
	if len(used) == 0 {
		t.Fatal("no tunnel through the proxy")
	}

	for _, n := range used {
		c := proxyConn(conns, n)
		switch {
		case c == nil:
			t.Errorf("proxy connection %d: no handshake recorded", n)

			continue

		case c.Host != server.ProxyHost:
			t.Errorf("proxy connection %d: server_name %q, want %q", n, c.Host, server.ProxyHost)
		case c.Protocol != proto:
			t.Errorf("proxy connection %d: protocol %q, want %q", n, c.Protocol, proto)
		}

		w := mergeHellos(browser[c.Resumed])
		if w == nil {
			t.Errorf("proxy connection %d resumed %v, the browser's never are", n, c.Resumed)

			continue
		}

		for i, e := range w.Extensions {
			if e.ECH == nil {
				continue
			}

			for _, pe := range p.TLS.Extensions {
				if pe.ECH != nil {
					ech := *pe.ECH
					if c.Resumed {
						ech.PayloadLengths = p.TLS.Resumption.ECHPayloadLengths
					}

					w.Extensions[i].ECH = &ech
				}
			}
		}

		ws, gs := profile.Spec{
			TLS: *w,
		}, profile.Spec{
			TLS: *c.Hello,
		}

		ws.TLS.ExtensionOrder, gs.TLS.ExtensionOrder = p.TLS.ExtensionOrder, p.TLS.ExtensionOrder

		for _, d := range diff.Compare(&ws, &gs) {
			t.Errorf("proxy connection %d's ClientHello (resumed %v): %s", n, c.Resumed, d)
		}
	}
}

// checkH2Connect checks mimicry's CONNECT g over HTTP/2 against the
// browser's first accepted one: the connection's opening and SETTINGS, the
// stream's HEADERS frame and the frame after it on the stream, and the
// connection's first header block's HPACK shape.
func checkH2Connect(t *testing.T, conns []capture.ProxyTLS, g capture.ProxyRequest, want httpsProxyCapture) {
	t.Helper()

	gc := proxyConn(conns, g.Conn)
	if gc == nil || gc.H2 == nil {
		t.Fatalf("proxy connection %d: no HTTP/2 trace", g.Conn)
	}

	openings := make([]string, 0, len(want.reqs))
	settings := make([]string, 0, len(want.reqs))
	streams := make([]string, 0, len(want.reqs))
	blocks := make([]string, 0, len(want.reqs))

	for i, sample := range want.reqs {
		r := nthOK(sample, 0)

		c := proxyConn(want.conns[i], r.Conn)
		if c == nil || c.H2 == nil {
			t.Fatalf("sample %d: proxy connection %d has no HTTP/2 trace", i+1, r.Conn)
		}

		openings = append(openings, strings.Join(h2Opening(c.H2), " "))
		settings = append(settings, fmt.Sprint(c.Settings))
		streams = append(streams, strings.Join(connectFrames(c.H2, r.Stream), " "))
		blocks = append(blocks, strings.Join(blockShape(c.H2.Requests[0].Instructions), " "))
	}

	for _, x := range []struct {
		what string
		got  string
		want []string
	}{
		{"opening (frames before the first HEADERS)", strings.Join(h2Opening(gc.H2), " "), openings},
		{"SETTINGS", fmt.Sprint(gc.Settings), settings},
		{"CONNECT stream (HEADERS and the frame after it)", strings.Join(connectFrames(gc.H2, g.Stream), " "), streams},
		{"first header block (HPACK)", strings.Join(blockShape(gc.H2.Requests[0].Instructions), " "), blocks},
	} {
		if !slices.Contains(x.want, x.got) {
			t.Errorf("%s:\n got %s\nwant one of %q", x.what, x.got, x.want)
		}
	}
}

// h2Opening describes the frames before a connection's first HEADERS,
// its SETTINGS acknowledgement aside (sent when the server's arrives).
func h2Opening(tr *h2trace.Trace) []string {
	var out []string

	for _, f := range tr.Frames {
		if f.Type == "HEADERS" {
			break
		}

		if f.Type == "SETTINGS" && f.Flags&0x1 != 0 {
			continue
		}

		s := f.Type
		if f.Type == "WINDOW_UPDATE" {
			s += fmt.Sprintf("(%d +%d)", f.Stream, f.Increment)
		}

		out = append(out, s)
	}

	return out
}

// connectFrames describes a CONNECT stream's HEADERS frame (flags, and
// priority: exclusive, weight, on stream 0 or another) and the frame that
// follows it on the stream, unless DATA.
func connectFrames(tr *h2trace.Trace, stream uint32) []string {
	var out []string

	for _, f := range tr.Frames {
		if f.Stream != stream {
			continue
		}

		if len(out) > 0 {
			if f.Type == "WINDOW_UPDATE" {
				out = append(out, fmt.Sprintf("WINDOW_UPDATE(+%d)", f.Increment))
			}

			break
		}

		if f.Type != "HEADERS" {
			continue
		}

		s := fmt.Sprintf("HEADERS(flags %#x", f.Flags)
		if pr := f.Priority; pr != nil {
			parent := "0"
			if pr.DependsOn != 0 {
				parent = "a stream"
			}

			s += fmt.Sprintf(" exclusive %t weight %d on %s", pr.Exclusive, pr.Weight, parent)
		}

		out = append(out, s+")")
	}

	return out
}

// blockShape is a header block's HPACK instructions without values or
// dynamic-table indexes (which hold what earlier blocks sent): the
// representation, the static table index, Huffman coding and the name.
func blockShape(ins []h2trace.Instruction) []string {
	out := make([]string, len(ins))

	for i, in := range ins {
		if in.Rep == h2trace.SizeUpdate {
			out[i] = "size_update(" + strconv.FormatUint(in.Size, 10) + ")"
			continue
		}

		index := "dynamic"
		if in.Index < 62 {
			index = strconv.FormatUint(in.Index, 10)
		}

		h := ""
		if in.HuffmanName {
			h += "h"
		}

		if in.HuffmanValue {
			h += "H"
		}

		out[i] = fmt.Sprintf("%s:%s%s:%s", in.Rep, index, h, in.Name)
	}

	return out
}

// collapse407 merges a flow's runs of CONNECTs answered 407 without
// credentials on one connection into one: over HTTP/2 the browser starts
// several tunnels (Chrome's preconnects, Edge's) before the challenge is
// answered, mimicry one.
func collapse407(flow []string) []string {
	return slices.CompactFunc(flow, func(a, b string) bool {
		return a == b && strings.HasSuffix(a, " 407 -")
	})
}

// checkAuthFlow checks mimicry's CONNECT flow (connectFlow) against each
// browser sample's, normalised by norm when set: its challenge, the retry
// and the first CONNECT sent unasked.
func checkAuthFlow(t *testing.T, got []string, want httpsProxyCapture, norm func([]string) []string) {
	t.Helper()

	if norm != nil {
		got = norm(got)
	}

	const steps = 3

	if len(got) < steps {
		t.Fatalf("CONNECT flow %v: fewer than %d steps", got, steps)
	}

	for i, sample := range want.reqs {
		w := connectFlow(sample)
		if norm != nil {
			w = norm(w)
		}

		if len(w) < steps || !slices.Equal(got[:steps], w[:steps]) {
			t.Errorf("CONNECT flow (connection, status, credentials) against sample %d:\n got %v\nwant %v", i+1, got[:steps], w[:min(steps, len(w))])
		}
	}
}

// withoutAuthority drops the :authority field of a CONNECT's lines.
func withoutAuthority(lines []string) []string {
	return slices.DeleteFunc(slices.Clone(lines), func(l string) bool {
		return strings.HasPrefix(l, ":authority:")
	})
}

// tunnelStreams are the accepted CONNECTs' streams, by connection.
func tunnelStreams(reqs []capture.ProxyRequest) [][2]uint32 {
	var out [][2]uint32

	for _, r := range reqs {
		if r.Status == http.StatusOK && r.Stream != 0 {
			out = append(out, [2]uint32{
				uint32(r.Conn),
				r.Stream,
			})
		}
	}

	return out
}

// resetCodes are the error codes of the RST_STREAM frames on accepted
// tunnels' streams.
func resetCodes(conns []capture.ProxyTLS, reqs []capture.ProxyRequest) []string {
	var out []string

	for _, s := range tunnelStreams(reqs) {
		c := proxyConn(conns, int(s[0]))
		if c == nil || c.H2 == nil {
			continue
		}

		for _, f := range c.H2.Frames {
			if f.Type == "RST_STREAM" && f.Stream == s[1] {
				out = append(out, f.ErrCode)
			}
		}
	}

	return out
}

// checkResets checks that mimicry reset the tunnels it closed with the
// code the browser reset its with.
func checkResets(t *testing.T, conns []capture.ProxyTLS, reqs []capture.ProxyRequest, want httpsProxyCapture) {
	t.Helper()

	browser := make([]string, 0, len(want.reqs))
	for i, sample := range want.reqs {
		browser = append(browser, resetCodes(want.conns[i], sample)...)
	}

	got := resetCodes(conns, reqs)
	if len(got) == 0 {
		t.Fatal("mimicry reset none of its tunnels' streams")
	}

	for _, code := range got {
		if !slices.Contains(browser, code) {
			t.Errorf("a tunnel's stream reset with %s; the browser's with %v", code, slices.Compact(slices.Sorted(slices.Values(browser))))
		}
	}
}

// checkAuthEncoding checks how the first CONNECT with credentials encodes
// proxy-authorization in HPACK against the browser's.
func checkAuthEncoding(t *testing.T, conns []capture.ProxyTLS, reqs []capture.ProxyRequest, want httpsProxyCapture) {
	t.Helper()

	g := authInstruction(conns, reqs)

	w := make([]string, 0, len(want.reqs))

	for i, sample := range want.reqs {
		w = append(w, authInstruction(want.conns[i], sample))
	}

	if !slices.Contains(w, g) {
		t.Errorf("proxy-authorization in the first CONNECT with credentials:\n got %s\nwant one of %q", g, w)
	}
}

// authInstruction is the shape of proxy-authorization's instruction in
// the first CONNECT with credentials over HTTP/2.
func authInstruction(conns []capture.ProxyTLS, reqs []capture.ProxyRequest) string {
	for _, r := range reqs {
		c := proxyConn(conns, r.Conn)
		if r.Stream == 0 || c == nil || c.H2 == nil {
			continue
		}

		for _, block := range c.H2.Requests {
			if block.Stream != r.Stream {
				continue
			}

			for _, in := range block.Instructions {
				if in.Name == "proxy-authorization" {
					return blockShape([]h2trace.Instruction{
						in,
					})[0]
				}
			}
		}
	}

	return "none"
}

// checkTunnelledResumption checks that hellos through the tunnels offer a
// session where the browser's do (Chrome), and none where its never do
// (Firefox: proxy.connectNoResume).
func checkTunnelledResumption(t *testing.T, reqs []capture.ProxyRequest, want httpsProxyCapture) {
	t.Helper()

	browser := 0
	for _, sample := range want.reqs {
		browser += offeredSessions(t, sample)
	}

	if got := offeredSessions(t, reqs); (got > 0) != (browser > 0) {
		t.Errorf("%d hellos through the tunnels offered a session; the browser's %d over %d samples", got, browser, len(want.reqs))
	}
}

// offeredSessions counts the tunnels whose first ClientHello offered a
// session (pre_shared_key).
func offeredSessions(t *testing.T, reqs []capture.ProxyRequest) int {
	t.Helper()

	n := 0

	for _, r := range reqs {
		if r.ClientHello == "" {
			continue
		}

		records, err := hex.DecodeString(r.ClientHello)
		if err != nil {
			t.Fatal(err)
		}

		_, msg, err := server.ParseRecords(records)
		if err != nil {
			t.Fatal(err)
		}

		h, err := server.ParseClientHello(msg)
		if err != nil {
			t.Fatal(err)
		}

		if slices.ContainsFunc(h.Extensions, func(e profile.Extension) bool {
			return e.Type == profile.ExtPreSharedKey
		}) {
			n++
		}
	}

	return n
}

// checkProxyResumption checks, over HTTP/1.1 where each tunnel is a TLS
// connection to the proxy of its own, that those after the first resume
// as the browser's do: most of them where every sample resumed at least
// half (Chrome), none where every sample resumed at most a quarter
// (Firefox, whose few resumed ones are not modelled: EVIDENCE.md).
func checkProxyResumption(t *testing.T, conns []capture.ProxyTLS, reqs []capture.ProxyRequest, want httpsProxyCapture) {
	t.Helper()

	var shares []float64

	for i, sample := range want.reqs {
		if share, ok := resumedShare(want.conns[i], sample); ok {
			shares = append(shares, share)
		}
	}

	got, ok := resumedShare(conns, reqs)
	if !ok {
		t.Fatalf("fewer than two tunnels: %v", tunnelConns(reqs))
	}

	switch {
	case len(shares) == 0:
		t.Fatal("no browser sample with two tunnels")
	case slices.Min(shares) >= 0.5 && got < 0.5:
		t.Errorf("%.2f of the tunnel connections after the first resumed TLS to the proxy; the browser's %v", got, shares)
	case slices.Max(shares) <= 0.25 && got != 0:
		t.Errorf("%.2f of the tunnel connections after the first resumed TLS to the proxy; the browser's %v, which mimicry does not resume", got, shares)
	}
}

// resumedShare is the share of the tunnel connections after the first
// that offered a session; false with fewer than two.
func resumedShare(conns []capture.ProxyTLS, reqs []capture.ProxyRequest) (float64, bool) {
	tunnels := tunnelConns(reqs)
	if len(tunnels) < 2 {
		return 0, false
	}

	resumed := 0

	for _, n := range tunnels[1:] {
		if c := proxyConn(conns, n); c != nil && c.Resumed {
			resumed++
		}
	}

	return float64(resumed) / float64(len(tunnels)-1), true
}

// resumePage walks sc's resume page through the proxy as the browser
// loads it (runResumeCapture): the page, each step from it with
// credentials (the parallel step's requests together), then /done.
func (s *proxySession) resumePage(sc *server.Scenario) {
	s.t.Helper()

	hosts, _, ok := sc.MultiHost()
	if !ok || len(hosts) < 3 {
		s.t.Fatalf("scenario %s serves no resume page", sc.Name)
	}

	hostOf := map[string]string{
		"so": hosts[0],
		"ss": hosts[1],
		"h1": hosts[2],
	}
	s.get("https", hosts[0], "/")

	page, err := url.Parse("https://" + hosts[0] + ":" + s.port + "/")
	if err != nil {
		s.t.Fatal(err)
	}

	fetch := mimicry.Request{
		Kind:        mimicry.Fetch,
		From:        page,
		Credentials: mimicry.CredentialsInclude,
	}

	for _, step := range server.ResumeSteps {
		labels := strings.Split(step, "|")
		errs := make([]error, len(labels))

		var wg sync.WaitGroup

		for i, label := range labels {
			_, suffix, _ := strings.Cut(label, ".")
			target := "https://" + hostOf[suffix] + ":" + s.port + "/k/" + label

			wg.Go(func() {
				req, err := http.NewRequestWithContext(s.t.Context(), http.MethodGet, target, nil)
				if err != nil {
					errs[i] = err
					return
				}

				resp, err := s.tr.RoundTrip(mimicry.With(req, fetch))
				if err != nil {
					errs[i] = err
					return
				}

				_, _ = io.Copy(io.Discard, resp.Body)
				errs[i] = resp.Body.Close()
			})
		}

		wg.Wait()

		for _, err := range errs {
			if err != nil {
				s.t.Fatal(err)
			}
		}
	}

	// Every tunnel closes, its stream reset, and then the connection to
	// the proxy left idle; once the proxy has read to its end, its record
	// is complete.
	s.get("https", hosts[0], "/done")
	s.tr.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(s.t.Context(), 10*time.Second)
	defer cancel()

	if err := s.proxy.WaitH2Ended(ctx); err != nil {
		s.t.Fatalf("the proxy's HTTP/2 connections did not end: %v", err)
	}
}
