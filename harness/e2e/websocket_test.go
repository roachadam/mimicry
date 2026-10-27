package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/proxy"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// Every socket of the websocket pages, opened through mimicry from its page,
// is the browser's against every sample: transport (own HTTP/1.1 connection
// or extended CONNECT stream), handshake, every client frame (compressed
// bytes included), the pong, HTTP/2 DATA packing, close and transport end.
// Where samples differ any sample's form passes; Chrome's fragmented 70 KiB
// message also passes joined. The proxy pages also check what reached the
// proxy for each socket.
func TestWebSocketsMatchBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		for _, sc := range []*server.Scenario{server.WebSocket, server.WebSocketH2, server.ProxyWebSocket, server.ProxyWebSocketAuth, server.ProxyWebSocketSOCKS5} {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				t.Parallel()
				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}
				// A listed missing capture is skipped, an existing listed one fails.
				want := loadSamples[*capture.Page](t, p, sc.Name, ".pagetrace.json.gz")
				sockets, steps, ok := sc.WebSocketPage()
				if !ok {
					t.Fatalf("scenario %s serves no websocket page", sc.Name)
				}
				var s *pageSession
				var px *proxy.Proxy
				var wantProxy [][]capture.ProxyRequest
				if sc.Proxy == "" {
					s = startPage(t, p, sc)
				} else {
					wantProxy = loadSamples[[]capture.ProxyRequest](t, p, sc.Name, ".proxytrace.json")
					var user *url.Userinfo
					if sc.ProxyAuth {
						user = url.UserPassword(server.ProxyUser, server.ProxyPassword)
					}
					s, px = startProxiedPage(t, p, sc, user)
				}
				got, seen := runWebSocketPage(t, s, sockets, steps)
				for _, st := range sockets {
					t.Run(st.Label, func(t *testing.T) {
						checkSocket(t, p, st, steps, got, want, seen[st.Label])
						if px != nil {
							checkProxyRoute(t, st, px.Requests(), got, wantProxy, want)
						}
					})
				}
			})
		}
	}
}

// socketRun is what mimicry's API reported of one socket step.
type socketRun struct {
	dialErr, writeErr error
	protocol          string
	extensions        string
	echoes            int
	// closed is the close ReadMessage returned.
	closed *mimicry.CloseError
}

// runWebSocketPage walks a websocket page through mimicry on s: each
// page step (navigations by script from the page before, credentialed
// fetches from the page), then the page's sockets one after another as
// its script runs them, then /done.
func runWebSocketPage(t *testing.T, s *pageSession, sockets []server.WebSocketStep, steps []server.WebSocketPageStep) (*capture.Page, map[string]*socketRun) {
	t.Helper()
	tr, ok := s.client.Transport.(*mimicry.Transport)
	if !ok {
		t.Fatal("the page session's client has no mimicry Transport")
	}
	runs := map[string]*socketRun{}
	var page *url.URL
	label := ""
	for i, st := range steps {
		target := s.wsURL(st.Plain, "http", st.Host, st.Path)
		if st.Nav {
			var h http.Header
			if page != nil {
				// The page navigates by script, without user activation.
				h = http.Header{
					"Sec-Fetch-User": nil,
				}
			}
			s.send(http.MethodGet, target, mimicry.Request{
				From: page,
			}, h, "")
			u, err := url.Parse(target)
			if err != nil {
				t.Fatal(err)
			}
			page, label = u, st.Label
		} else {
			s.send(http.MethodGet, target, mimicry.Request{
				Kind:        mimicry.Fetch,
				From:        page,
				Credentials: mimicry.CredentialsInclude,
			}, nil, "")
		}
		if i+1 < len(steps) && !steps[i+1].Nav {
			continue
		}
		// The page's fetches are done: its sockets.
		for _, ws := range sockets {
			if ws.Page == label {
				runs[ws.Label] = runSocket(t, s, tr, page, ws)
			}
		}
	}
	return s.finish(s.url(steps[0].Host, "/done")), runs
}

// startProxiedPage is startPage behind the capture proxy, as sc
// configures it: every host and port reached through the proxy at the
// proxy URL's scheme, with user's credentials. The server's ports are only
// named, never dialled.
func startProxiedPage(t *testing.T, p *profile.Profile, sc *server.Scenario, user *url.Userinfo) (*pageSession, *proxy.Proxy) {
	t.Helper()
	ca, err := server.LoadOrCreateCA(t.TempDir(), server.CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := &pageSession{
		t:        t,
		scheme:   "https",
		port:     "8443",
		captures: make(chan *capture.Capture, 64),
	}
	px := &proxy.Proxy{
		Allow: func(host string) bool {
			return strings.HasSuffix(host, ".test")
		},
		Port: 8443,
		Logf: t.Logf,
	}
	if sc.ProxyAuth {
		px.Auth = server.ProxyAuthorization()
	}
	srv := &server.Server{
		Scenario:  sc,
		TLSConfig: &tls.Config{},
		IssueCert: func(hosts []string) (tls.Certificate, error) {
			return ca.Issue(hosts, time.Now())
		},
		OnCapture: func(c *capture.Capture) {
			s.captures <- c
		},
		Logf: t.Logf,
	}
	if sc.PlainPort() {
		s.plainPort = "8080"
		px.PlainPort = 8080
		srv.PlainListener = px.PlainListener()
	}
	pln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served, proxied := make(chan error, 1), make(chan error, 1)
	go func() {
		proxied <- px.Serve(ctx, pln)
	}()
	go func() {
		served <- srv.Serve(ctx, px.Listener())
	}()
	t.Cleanup(func() {
		cancel()
		for _, ch := range []chan error{served, proxied} {
			if err := <-ch; err != nil && !errors.Is(err, net.ErrClosed) {
				t.Error(err)
			}
		}
	})
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	tr, err := mimicry.NewTransport(mimicry.Config{
		Profile: p,
		RootCAs: pool,
		Cookies: newJar(t, p),
		Proxy: http.ProxyURL(&url.URL{
			Scheme: sc.Proxy,
			Host:   pln.Addr().String(),
			User:   user,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	s.client = &http.Client{
		Transport: tr,
	}
	return s, px
}

// checkProxyRoute compares what reached the proxy for mimicry's socket st
// (got, its proxy's requests reqs) with every sample's (wantReqs).
func checkProxyRoute(t *testing.T, st server.WebSocketStep, reqs []capture.ProxyRequest, got *capture.Page, wantReqs [][]capture.ProxyRequest, want []*capture.Page) {
	t.Helper()
	g := socketOf(got, st)
	if g == nil {
		return // checkSocket said why
	}
	if len(wantReqs) != len(want) {
		t.Fatalf("%d samples of proxy requests, %d of pages", len(wantReqs), len(want))
	}
	anyForm(t, "proxy", proxyRoute(reqs, g), func(i int) string {
		return proxyRoute(wantReqs[i], socketOf(want[i], st))
	}, len(want))
}

// proxyRoute describes what reached the proxy for ws: the requests (each
// CONNECT's status and lines, or SOCKS5 messages, ports elided) on the
// proxy connection whose tunnel carried it, or that it was sent whole.
func proxyRoute(reqs []capture.ProxyRequest, ws *capture.WebSocket) string {
	if ws == nil {
		return "no socket"
	}
	var out []string
	for _, r := range reqs {
		if r.Conn != ws.ProxyConn {
			continue
		}
		switch r.Kind {
		case capture.ProxyConnect:
			out = append(out, fmt.Sprintf("CONNECT answered %d: %s", r.Status, strings.Join(portless(r.Lines), " | ")))
		case capture.ProxySOCKS5:
			out = append(out, "SOCKS5 "+r.Greeting+" "+socksPortless(r.Request))
		}
	}
	if len(out) == 0 {
		return "sent to the proxy whole"
	}
	return strings.Join(out, "\n")
}

// wsURL is a URL on the session's server: scheme+"s" on its TLS port, or
// scheme on its plain port.
func (s *pageSession) wsURL(plain bool, scheme, host, path string) string {
	if plain {
		return scheme + "://" + net.JoinHostPort(host, s.plainPort) + path
	}
	return scheme + "s://" + net.JoinHostPort(host, s.port) + path
}

// runSocket runs one socket step as the page's script does: opens it,
// sends its messages, reads their echoes, and closes it with 1000 "bye"
// (the server's close first, where it starts one).
func runSocket(t *testing.T, s *pageSession, tr *mimicry.Transport, page *url.URL, st server.WebSocketStep) *socketRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	run := &socketRun{}
	c, _, err := tr.DialWebSocket(ctx, s.wsURL(st.Plain, "ws", st.Host, st.Path()), mimicry.WebSocketOptions{
		From:      page,
		Protocols: st.Protocols,
	})
	if err != nil {
		run.dialErr = err
		return run
	}
	run.protocol, run.extensions = c.Protocol(), c.Extensions()
	var sent [][]byte
	for _, m := range st.Sends {
		typ := mimicry.TextMessage
		if m.Binary {
			typ = mimicry.BinaryMessage
		}
		if run.writeErr = c.WriteMessage(ctx, typ, m.Data()); run.writeErr != nil {
			break
		}
		sent = append(sent, m.Data())
	}
	read := func() {
		_, data, err := c.ReadMessage(ctx)
		switch {
		case errors.As(err, &run.closed):
		case err != nil:
			t.Errorf("%s: reading: %v", st.Label, err)
		case run.echoes >= len(sent) || !bytes.Equal(data, sent[run.echoes]):
			t.Errorf("%s: echo %d is not the message sent", st.Label, run.echoes+1)
		default:
			run.echoes++
		}
	}
	for run.echoes < len(sent) && run.closed == nil {
		read()
	}
	if st.ServerClose && run.closed == nil {
		read()
	}
	if err := c.Close(1000, "bye"); err != nil {
		t.Errorf("%s: closing: %v", st.Label, err)
	}
	if run.closed == nil {
		read()
	}
	return run
}

// checkSocket compares mimicry's socket st (got, run) with every sample's.
func checkSocket(t *testing.T, p *profile.Profile, st server.WebSocketStep, steps []server.WebSocketPageStep, got *capture.Page, want []*capture.Page, run *socketRun) {
	if run == nil {
		t.Fatal("the step did not run")
	}
	if st.Label == "mixed" {
		// Chrome's constructor throws; no browser's handshake reached the
		// server.
		if run.dialErr == nil {
			t.Error("a ws:// socket from the https:// page opened")
		}
		for _, page := range append([]*capture.Page{got}, want...) {
			if socketOf(page, st) != nil {
				t.Error("a ws:// socket from the https:// page reached the server")
			}
		}
		return
	}
	if skippedIn(st, want) {
		// The step hangs this browser, whose page skipped it: mimicry's
		// socket must not open either (the profile fails it).
		if run.dialErr == nil {
			t.Error("the step hangs the browser (its page skipped it); mimicry's socket opened")
		}
		return
	}
	g := socketOf(got, st)
	if g == nil {
		t.Fatalf("mimicry's socket did not reach the server (opening: %v)", run.dialErr)
	}
	samples := make([]*capture.WebSocket, len(want))
	failed := true
	for i, page := range want {
		if samples[i] = socketOf(page, st); samples[i] == nil {
			t.Fatalf("sample %d has no socket", i+1)
		}
		failed = failed && len(clientFrames(samples[i], false)) == 0
	}
	// A socket the browser failed after its handshake, sending nothing (a
	// compression window its library refuses): mimicry's dial fails, and
	// its transport ends as the browser's did.
	switch {
	case failed && run.dialErr == nil:
		t.Fatal("the browser failed the socket after its handshake; mimicry's opened")
	case !failed && run.dialErr != nil:
		t.Fatalf("opening: %v", run.dialErr)
	case !failed:
		checkResult(t, st, want, run)
		if run.writeErr != nil {
			t.Errorf("sending: %v", run.writeErr)
		}
	}

	anyForm(t, "transport", socketTransport(got, g), func(i int) string {
		return socketTransport(want[i], samples[i])
	}, len(want))
	if g.Protocol == "h2" {
		checkStreamConnection(t, steps, got, g, want, samples)
	}
	anyForm(t, "handshake", handshakeLines(got, g), func(i int) string {
		return handshakeLines(want[i], samples[i])
	}, len(want))
	if g.Protocol == "http/1.1" && !g.Plain {
		checkSocketHello(t, p, got, g, want, samples)
	}
	checkMasks(t, g)
	// Chrome splits a large message into a frame and a continuation in some
	// samples, by chance (backlog: Chrome's fragmented large message); mimicry
	// sends it whole, which each sample also offers with its split messages
	// joined, so one sample is enough.
	forms := len(want)
	if p.Browser == "chrome" {
		forms *= 2
	}
	matched := anyForm(t, "frames", strings.Join(clientFrames(g, false), "\n"), func(i int) string {
		if i >= len(want) {
			return strings.Join(clientFrames(joinFragments(samples[i-len(want)]), false), "\n")
		}
		return strings.Join(clientFrames(samples[i], false), "\n")
	}, forms)
	if matched >= 0 && g.Protocol == "h2" {
		checkDATA(t, p, got, g, want, samples)
	}
	anyForm(t, "pongs", strings.Join(clientFrames(g, true), "\n"), func(i int) string {
		return strings.Join(clientFrames(samples[i], true), "\n")
	}, len(want))
	checkEnd(t, g, samples)
}

// joinFragments returns ws with each fragmented client message (a data frame
// without FIN and its continuations) as one frame: the first's header bits,
// FIN set, every fragment's payload, the length field sized for the total.
func joinFragments(ws *capture.WebSocket) *capture.WebSocket {
	out := *ws
	out.Frames = nil
	open := -1
	for _, f := range ws.Frames {
		switch {
		case f.Server || f.Opcode >= 8:
			out.Frames = append(out.Frames, f)
		case f.Opcode == 0 && open >= 0:
			m := &out.Frames[open]
			m.Payload = append(slices.Clip(m.Payload), f.Payload...)
			m.Length += f.Length
			if f.Fin {
				m.Fin = true
				open = -1
			}
			switch {
			case m.Length < 126:
				m.LengthBits = 7
			case m.Length < 1<<16:
				m.LengthBits = 16
			default:
				m.LengthBits = 64
			}
		default:
			out.Frames = append(out.Frames, f)
			if !f.Fin {
				open = len(out.Frames) - 1
			}
		}
	}
	return &out
}

// skippedIn reports that every sample's page skipped step st, which
// hangs its browser (server.WebSocketStep.Hangs).
func skippedIn(st server.WebSocketStep, want []*capture.Page) bool {
	for _, page := range want {
		i := slices.IndexFunc(page.WSResults, func(r capture.WSResult) bool {
			return r.Label == st.Label
		})
		if i < 0 || !page.WSResults[i].Skipped {
			return false
		}
	}
	return len(want) > 0
}

// socketOf returns page's socket for st, or nil.
func socketOf(page *capture.Page, st server.WebSocketStep) *capture.WebSocket {
	for i, ws := range page.WebSockets {
		if ws.Path == st.Path() && ws.Plain == st.Plain {
			return &page.WebSockets[i]
		}
	}
	return nil
}

// anyForm checks that got is one of the forms the browser's n samples
// (sample(i)) sent, logging variance, and returns the first matching
// sample, or -1.
func anyForm(t *testing.T, what, got string, sample func(i int) string, n int) int {
	t.Helper()
	match := -1
	var forms []string
	for i := range n {
		w := sample(i)
		if !slices.Contains(forms, w) {
			forms = append(forms, w)
		}
		if w == got && match < 0 {
			match = i
		}
	}
	if len(forms) > 1 {
		t.Logf("%s: the browser's samples differ (%d forms)", what, len(forms))
	}
	if match < 0 {
		t.Errorf("%s:\n got %s\nwant %s", what, got, strings.Join(forms, "\n  or "))
	}
	return match
}

// checkResult compares mimicry's API result with what the page's script saw
// (capture.WSResult), but Extensions is compared with the server's answer:
// Firefox's attribute drops its parameters.
func checkResult(t *testing.T, st server.WebSocketStep, want []*capture.Page, run *socketRun) {
	t.Helper()
	g := capture.WSResult{
		Open:       true,
		Protocol:   run.protocol,
		Extensions: run.extensions,
		Echoes:     run.echoes,
		Closed:     run.closed != nil,
	}
	if run.closed != nil {
		g.Code, g.Reason = run.closed.Code, run.closed.Reason
	}
	for i, page := range want {
		var w *capture.WSResult
		for k := range page.WSResults {
			if page.WSResults[k].Label == st.Label {
				w = &page.WSResults[k]
			}
		}
		if w == nil {
			t.Fatalf("sample %d has no result", i+1)
		}
		b := capture.WSResult{
			Open:       w.Open,
			Protocol:   w.Protocol,
			Extensions: socketOf(page, st).Extensions,
			Echoes:     w.Echoes,
			Closed:     w.Closed,
			Code:       w.Code,
			Reason:     w.Reason,
		}
		if g != b {
			t.Errorf("sample %d: the socket as reported:\n got %+v\nwant %+v", i+1, g, b)
		}
	}
}

// socketTransport describes what carried ws on page: an HTTP/2 stream, or
// an HTTP/1.1 connection and what else it carried.
func socketTransport(page *capture.Page, ws *capture.WebSocket) string {
	switch {
	case ws.Protocol == "h2":
		return "an h2 stream"
	case ws.Plain:
		conn := page.H1[ws.Request].Conn
		others := 0
		for i, r := range page.H1 {
			if r.Conn == conn && i != ws.Request {
				others++
			}
		}
		return fmt.Sprintf("plain http/1.1 with %d other requests on its connection", others)
	}
	c := page.Connections[ws.Connection-1]
	return fmt.Sprintf("%s, %d requests on its connection", c.Protocol, c.Requests)
}

// streamPaths returns the page steps' paths that went on ws's HTTP/2
// connection.
func streamPaths(steps []server.WebSocketPageStep, page *capture.Page, ws *capture.WebSocket) []string {
	var on []string
	for _, r := range page.H2[ws.Trace].Requests {
		for _, st := range steps {
			if r.Path == st.Path && !slices.Contains(on, r.Path) {
				on = append(on, r.Path)
			}
		}
	}
	return on
}

// checkStreamConnection checks mimicry's stream used the connection the
// browser's did. mimicry's may carry more: its pool is not partitioned by
// top-level site (logged).
func checkStreamConnection(t *testing.T, steps []server.WebSocketPageStep, got *capture.Page, g *capture.WebSocket, want []*capture.Page, samples []*capture.WebSocket) {
	t.Helper()
	on := streamPaths(steps, got, g)
	for i, page := range want {
		w := streamPaths(steps, page, samples[i])
		for _, path := range w {
			if !slices.Contains(on, path) {
				t.Errorf("sample %d: the stream's connection carried %q, mimicry's %q", i+1, w, on)
				break
			}
		}
		if i == 0 && len(on) > len(w) {
			t.Logf("mimicry's stream connection also carried %q (no top-level-site partition); the browser's only %q", on, w)
		}
	}
}

var (
	wsKey   = regexp.MustCompile(`(?i)^(sec-websocket-key: ).*`)
	wsPorts = regexp.MustCompile(`\.test:\d+`)
)

// handshakeLines is ws's handshake: its request and header lines
// (HTTP/1.1, key elided), or its fields in order with how cookies are
// represented, and its HEADERS flags, priority and the stream's
// WINDOW_UPDATE (HTTP/2). Ports are elided.
func handshakeLines(page *capture.Page, ws *capture.WebSocket) string {
	var lines []string
	if ws.Protocol != "h2" {
		r := page.H1[ws.Request]
		lines = append(lines, r.Line)
		for _, l := range r.Headers {
			lines = append(lines, wsKey.ReplaceAllString(l, "${1}<key>"))
		}
	} else {
		tr := page.H2[ws.Trace]
		for _, ins := range tr.Requests[ws.Request].Instructions {
			if ins.Rep == h2trace.SizeUpdate {
				continue
			}
			l := ins.Name + ": " + ins.Value
			if ins.Rep == h2trace.Never {
				l += " (never indexed)"
			}
			lines = append(lines, l)
		}
		for _, f := range tr.Frames {
			switch {
			case f.Stream != ws.Stream:
			case f.Type == "HEADERS":
				l := fmt.Sprintf("HEADERS flags %#x", f.Flags)
				if f.Priority != nil {
					l += fmt.Sprintf(" priority %+v", *f.Priority)
				}
				lines = append(lines, l)
			case f.Type == "WINDOW_UPDATE":
				lines = append(lines, fmt.Sprintf("WINDOW_UPDATE %d", f.Increment))
			}
		}
	}
	for i, l := range lines {
		lines[i] = wsPorts.ReplaceAllString(l, ".test:PORT")
	}
	return strings.Join(lines, "\n")
}

// checkSocketHello compares the socket connection's TLS hello with the
// browser's, merging GREASE ECH choices across samples: those are random
// draws, checked by TestProfilesMatchCaptures and TestResumptionMatchesBrowser.
func checkSocketHello(t *testing.T, p *profile.Profile, got *capture.Page, g *capture.WebSocket, want []*capture.Page, samples []*capture.WebSocket) {
	t.Helper()
	hellos := make([]*profile.TLS, 0, len(want))
	for i, page := range want {
		hellos = append(hellos, page.Connections[samples[i].Connection-1].Hello)
	}
	w := profile.Profile{
		TLS: *mergeHellos(hellos),
	}
	var ech *profile.ECH
	for _, e := range p.TLS.Extensions {
		if e.ECH != nil {
			ech = e.ECH
		}
	}
	for _, e := range w.TLS.Extensions {
		if e.ECH == nil || ech == nil {
			continue
		}
		e.ECH.CipherSuites = slices.CompactFunc(slices.SortedFunc(slices.Values(append(slices.Clone(e.ECH.CipherSuites), ech.CipherSuites...)), profile.HPKESuite.Compare), func(a, b profile.HPKESuite) bool {
			return a.Compare(b) == 0
		})
		lengths := ech.PayloadLengths
		if r := p.TLS.Resumption; r != nil && got.Connections[g.Connection-1].Resumed {
			lengths = r.ECHPayloadLengths
		}
		e.ECH.PayloadLengths = slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(e.ECH.PayloadLengths), lengths...))))
	}
	h := profile.Profile{
		TLS: *got.Connections[g.Connection-1].Hello,
	}
	w.TLS.ExtensionOrder, h.TLS.ExtensionOrder = p.TLS.ExtensionOrder, p.TLS.ExtensionOrder
	for _, d := range diff.Compare(&w, &h) {
		t.Errorf("the socket connection's ClientHello: %s", d)
	}
}

// clientFrames describes ws's client frames, as on the wire: the pongs
// only, or every other frame (a pong's place among them depends on when
// the ping arrives).
func clientFrames(ws *capture.WebSocket, pongs bool) []string {
	var out []string
	for _, f := range ws.Frames {
		if f.Server || (f.Opcode == capture.WSPong) != pongs {
			continue
		}
		sum := sha256.Sum256(f.Payload)
		out = append(out, fmt.Sprintf("fin=%v rsv=%d op=%d masked=%v len=%d/%d payload=%s", f.Fin, f.RSV, f.Opcode, f.Masked, f.LengthBits, f.Length, hex.EncodeToString(sum[:8])))
	}
	return out
}

// checkMasks checks that every client frame of g is masked, each with a
// key of its own, none zero.
func checkMasks(t *testing.T, g *capture.WebSocket) {
	t.Helper()
	seen := map[string]bool{}
	for i, f := range g.Frames {
		switch {
		case f.Server:
		case !f.Masked || f.Mask == "00000000":
			t.Errorf("frame %d: masked %v, key %s", i+1, f.Masked, f.Mask)
		case seen[f.Mask]:
			t.Errorf("frame %d: key %s used twice", i+1, f.Mask)
		default:
			seen[f.Mask] = true
		}
	}
}

// frameKey names a client frame across samples: data frames by message
// and fragment, control frames by opcode, each with its header's size
// (mask included) and its size on the wire.
type frameKey struct {
	opcode            uint8
	message, fragment int
	head, size        uint32
}

// dataCuts maps ws's client frames to the DATA frames carrying them on
// page, in order: each frame's DATA lengths. Frames from the first whose
// DATA also carries the next one (Chrome sometimes merges writes) are left
// out. ended reports END_STREAM on the stream's last DATA frame.
func dataCuts(page *capture.Page, ws *capture.WebSocket) (cuts map[frameKey][]uint32, order []frameKey, ended bool) {
	var data []h2trace.Frame
	for _, f := range page.H2[ws.Trace].Frames {
		if f.Stream == ws.Stream && f.Type == "DATA" {
			data = append(data, f)
		}
	}
	if len(data) > 0 {
		ended = data[len(data)-1].Flags&0x1 != 0
	}
	cuts = map[frameKey][]uint32{}
	message, fragment := -1, 0
	for _, f := range ws.Frames {
		if f.Server {
			continue
		}
		k := frameKey{
			opcode: f.Opcode,
			head:   headSize(f),
		}
		k.size = k.head + uint32(f.Length)
		switch f.Opcode {
		case capture.WSText, capture.WSBinary:
			message, fragment = message+1, 0
			k.message = message
		case capture.WSContinuation:
			fragment++
			k.message, k.fragment = message, fragment
		}
		var c []uint32
		for left := k.size; left > 0; {
			if len(data) == 0 || data[0].Length > left {
				return cuts, order, ended
			}
			c = append(c, data[0].Length)
			left -= data[0].Length
			data = data[1:]
		}
		cuts[k] = c
		order = append(order, k)
	}
	return cuts, order, ended
}

// headSize is a frame's header size on the wire, mask included.
func headSize(f capture.WSFrame) uint32 {
	n := uint32(2)
	switch f.LengthBits {
	case 16:
		n += 2
	case 64:
		n += 8
	}
	if f.Masked {
		n += 4
	}
	return n
}

// checkDATA compares how HTTP/2 DATA frames carry the socket's frames. A
// frame fitting one DATA frame must be cut as every sample cut it; a larger
// one must stay within the cap, header alone where every sample does so.
// END_STREAM on the last DATA frame as in every sample.
func checkDATA(t *testing.T, p *profile.Profile, got *capture.Page, g *capture.WebSocket, want []*capture.Page, samples []*capture.WebSocket) {
	t.Helper()
	gc, order, gEnd := dataCuts(got, g)
	if len(order) != len(clientFrames(g, false))+len(clientFrames(g, true)) {
		t.Fatalf("mimicry's DATA frames do not carry its frames one after another: %v", gc)
	}
	wc := make([]map[frameKey][]uint32, 0, len(want))
	for i, page := range want {
		c, _, end := dataCuts(page, samples[i])
		if end != gEnd {
			t.Errorf("END_STREAM on the last DATA frame: got %v, sample %d %v", gEnd, i+1, end)
		}
		wc = append(wc, c)
	}
	limit := p.HTTP2.MaxDataFrame
	for _, k := range order {
		var forms [][]uint32
		for _, c := range wc {
			if cut, ok := c[k]; ok {
				forms = append(forms, cut)
			}
		}
		if len(forms) == 0 {
			continue
		}
		cut := gc[k]
		if k.size <= limit {
			if !slices.Equal(cut, forms[0]) {
				t.Errorf("frame %+v in DATA frames of %v, want %v", k, cut, forms[0])
			}
			continue
		}
		alone := 0
		for _, f := range forms {
			if f[0] == k.head {
				alone++
			}
		}
		switch {
		case alone == len(forms) && cut[0] != k.head:
			t.Errorf("frame %+v in DATA frames of %v: the browser's header goes alone", k, cut)
		case alone == 0 && cut[0] == k.head:
			t.Errorf("frame %+v in DATA frames of %v: the browser's header goes with the payload", k, cut)
		}
		for _, n := range cut {
			if n > limit {
				t.Errorf("frame %+v: a DATA frame of %d bytes, over the cap %d", k, n, limit)
			}
		}
	}
}

// checkEnd checks who ended mimicry's transport and how, against every
// sample: the client, as long after its last frame as the browser (a
// little later allowed, for a loaded machine), or, where no sample shows
// the client ending it (Firefox), never the client.
func checkEnd(t *testing.T, g *capture.WebSocket, samples []*capture.WebSocket) {
	t.Helper()
	client := 0
	low, high := int64(-1), int64(0)
	var ends []string
	for _, w := range samples {
		if !slices.Contains(ends, w.StreamEnd) {
			ends = append(ends, w.StreamEnd)
		}
		if w.ClosedBy != "client" {
			continue
		}
		client++
		d := endDelay(w)
		if low < 0 || d < low {
			low = d
		}
		high = max(high, d)
	}
	if !slices.Contains(ends, g.StreamEnd) {
		t.Errorf("the stream ended with %q, want %q", g.StreamEnd, ends)
	}
	switch client {
	case 0:
		if g.ClosedBy == "client" {
			t.Errorf("mimicry ended the transport %d ms after its last frame; the browser never did", endDelay(g))
		}
	case len(samples):
		if d := endDelay(g); g.ClosedBy != "client" || d < low-50 || d > high+250 {
			t.Errorf("transport ended by %q %d ms after the client's last frame, want the client after %d to %d ms", g.ClosedBy, d, low, high)
		}
	default:
		t.Errorf("the browser's samples disagree on who ends the transport")
	}
}

// endDelay is how long after its last frame ws's transport ended.
func endDelay(ws *capture.WebSocket) int64 {
	last := int64(0)
	for _, f := range ws.Frames {
		if !f.Server {
			last = f.Millis
		}
	}
	return ws.ClosedMillis - last
}
