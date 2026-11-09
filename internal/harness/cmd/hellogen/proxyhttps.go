package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// proxyHTTPS derives proxy.https from the https:// proxy scenarios'
// captures (Scenario.Proxy server.ProxyHTTPS), every sample of each, and
// refuses captures it cannot be:
//   - Every connection to the proxy sent the profile's ClientHello
//     (resumed as tls.resumption says), server_name the proxy's host,
//     with the profile's ALPN offer: the hello has no field of its own.
//   - Over HTTP/1.1, every CONNECT is proxy.connect's or connectAuth's.
//   - Over HTTP/2, each connection carrying a CONNECT to a capture host
//     opened with http2.settings and windowUpdate; each such CONNECT has
//     :method and :authority in http2.pseudoHeaderOrder's order, then the
//     fields connect (without credentials) or connectAuth (with) list,
//     the same in every one; its HEADERS frame does not end the stream,
//     carries the same weight in every one, exclusive and dependent as
//     http2.headersPriority and chainPriorities say, and is followed on
//     its stream by http2.streamWindowUpdate's top-up where the profile
//     has one; every tunnel stream the browser reset was reset with the
//     same code (reset).
//   - resume: in each sample of a scenario whose proxy offers only
//     HTTP/1.1, where every tunnel takes a connection of its own, the
//     tunnel connections after the first offered a session: at least
//     half of them in every sample with two or more, true; at most a
//     quarter in every one, false; anything else, the samples disagree.
//   - proxy.connectNoResume, hand-written from the http:// proxy's
//     captures, must hold through the https:// proxy's tunnels too: no
//     tunnelled hello offers a session where it is set, some does where
//     it is not.
//
// Nil without a capture: not measured.
func proxyHTTPS(dir string, p *profile.Spec) (*profile.ProxyHTTPS, error) {
	var (
		out     *profile.ProxyHTTPS
		errs    []error
		shares  []float64
		offered int
	)

	connectSeen, authSeen := false, false

	for _, sc := range server.Scenarios {
		if sc.Proxy != server.ProxyHTTPS {
			continue
		}

		source, rw, err := capture.Inherited(p, sc.Name)
		if err != nil {
			return nil, err
		}

		prefix := filepath.Join(dir, source+"."+sc.Name)

		traces, err := capture.LoadProxyRequests(prefix + ".proxytrace.json")
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		conns, err := capture.LoadProxyTLS(prefix + ".proxytls.json.gz")
		if err != nil {
			return nil, err
		}

		if len(conns) != len(traces) {
			return nil, fmt.Errorf("%s: %d samples of connections, %d of CONNECTs", prefix, len(conns), len(traces))
		}

		if out == nil {
			out = &profile.ProxyHTTPS{}
		}

		h1Only := !slices.Contains(sc.ProxyNextProtos(), "h2")

		for si, sample := range traces {
			if rw != nil {
				rw.Proxy(sample)
			}

			at := fmt.Sprintf("%s sample %d", filepath.Base(prefix), si+1)
			s := &proxySample{
				p:     p,
				at:    at,
				out:   out,
				conns: conns[si],
				reqs:  sample,
			}

			s.checkHellos()
			s.checkConnects(&connectSeen, &authSeen)

			if h1Only {
				if share, ok := s.resumedShare(); ok {
					shares = append(shares, share)
				}
			}

			offered += s.tunnelledResumptions()

			errs = append(errs, s.errs...)
		}
	}

	if out == nil {
		return nil, nil //nolint:nilnil // nil: no capture, not measured
	}

	switch {
	case !connectSeen:
		errs = append(errs, errors.New("no CONNECT over HTTP/2 without credentials"))
	case out.Reset == "":
		errs = append(errs, errors.New("no tunnel stream reset by the browser"))
	case len(shares) == 0:
		errs = append(errs, errors.New("no sample of two tunnels over HTTP/1.1, to tell whether TLS to the proxy resumes"))
	}

	if !authSeen {
		out.ConnectAuth = nil
	}

	switch {
	case !slices.ContainsFunc(shares, func(s float64) bool { return s < 0.5 }):
		out.Resume = true
	case !slices.ContainsFunc(shares, func(s float64) bool { return s > 0.25 }):
		out.Resume = false
	default:
		errs = append(errs, fmt.Errorf("the samples disagree on resuming TLS to the proxy: shares of tunnel connections resumed %v", shares))
	}

	if p.Proxy.ConnectNoResume != (offered == 0) {
		errs = append(errs, fmt.Errorf("proxy.connectNoResume is %t, but %d hellos through the https:// proxy's tunnels offered a session", p.Proxy.ConnectNoResume, offered))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return out, nil
}

// proxySample is one sample of an https:// proxy scenario: the
// connections to the proxy and the CONNECTs to capture hosts. Errors
// gather in errs; what it derives goes in out.
type proxySample struct {
	p     *profile.Spec
	at    string
	out   *profile.ProxyHTTPS
	conns []capture.ProxyTLS
	reqs  []capture.ProxyRequest
	errs  []error
}

func (s *proxySample) fail(format string, args ...any) {
	s.errs = append(s.errs, fmt.Errorf("%s: %w", s.at, fmt.Errorf(format, args...)))
}

// checkHellos checks that every connection that carried a tunnel to a
// capture host sent the profile's ClientHello, to the proxy's host. The
// browser's own traffic to the proxy (refused, so not in the trace) is
// left out: Firefox's background requests send another hello, with no
// session ID (EVIDENCE.md, Not covered).
func (s *proxySample) checkHellos() {
	for _, c := range s.conns {
		if !slices.ContainsFunc(s.reqs, func(r capture.ProxyRequest) bool {
			return r.Conn == c.Conn
		}) {
			continue
		}

		h := c.Hello
		if h == nil || h.LegacyVersion == 0 {
			s.fail("connection %d: its hello was not recorded", c.Conn)

			continue
		}

		if c.Host != server.ProxyHost {
			s.fail("connection %d: server_name %q, not the proxy's %q", c.Conn, c.Host, server.ProxyHost)
		}

		got, err := socketHello(&s.p.TLS, h)
		if err != nil {
			s.fail("connection %d: %w", c.Conn, err)

			continue
		}

		if want := extension(&s.p.TLS, profile.ExtALPN); want == nil || !slices.Equal(got.ALPN, want.ALPN) || len(got.Drop) > 0 {
			s.fail("connection %d: ALPN %v without %v, not the profile's hello", c.Conn, got.ALPN, got.Drop)
		}
	}
}

// conn is the connection numbered n.
func (s *proxySample) conn(n int) *capture.ProxyTLS {
	for i := range s.conns {
		if s.conns[i].Conn == n {
			return &s.conns[i]
		}
	}

	return nil
}

// checkConnects checks every CONNECT to a capture host, and derives
// proxy.https's lists, weight and reset from those over HTTP/2.
func (s *proxySample) checkConnects(connectSeen, authSeen *bool) {
	opened := map[int]bool{}

	for _, r := range s.reqs {
		if r.Kind != capture.ProxyConnect {
			s.fail("a %s request", r.Kind)

			continue
		}

		c := s.conn(r.Conn)
		if c == nil {
			s.fail("CONNECT %s on connection %d, which was not recorded", r.Target, r.Conn)

			continue
		}

		if r.Stream == 0 {
			s.checkH1Connect(r)

			continue
		}

		if c.H2 == nil {
			s.fail("CONNECT %s on stream %d of connection %d, which has no HTTP/2 trace", r.Target, r.Stream, r.Conn)

			continue
		}

		if !opened[r.Conn] {
			opened[r.Conn] = true

			s.checkOpening(c)
		}

		auth := s.h2Fields(r)
		if auth {
			*authSeen = true
		} else {
			*connectSeen = true
		}

		s.checkStream(c.H2, r)
	}
}

// checkH1Connect checks a CONNECT over HTTP/1.1: proxy.connect's or
// connectAuth's lines.
func (s *proxySample) checkH1Connect(r capture.ProxyRequest) {
	px := s.p.Proxy
	list := px.Connect

	if slices.ContainsFunc(r.Lines, func(l string) bool {
		return strings.HasPrefix(strings.ToLower(l), "proxy-authorization:")
	}) {
		list = px.ConnectAuth
	}

	want := make([]string, 1, 1+len(list))

	want[0] = "CONNECT " + r.Target + " HTTP/1.1"

	for _, h := range list {
		value := h.Value

		switch strings.ToLower(h.Name) {
		case "host":
			value = r.Target
		case "user-agent":
			if value == "" {
				value = userAgent(s.p)
			}

		case "proxy-authorization":
			value = server.ProxyAuthorization()
		}

		want = append(want, h.Name+": "+value)
	}

	if !slices.Equal(r.Lines, want) {
		s.fail("CONNECT over HTTP/1.1 on connection %d:\n got %q\nwant %q (proxy.connect, connectAuth)", r.Conn, r.Lines, want)
	}
}

// checkOpening checks that an HTTP/2 connection to the proxy opened as
// the profile's http2 section says: its SETTINGS, then its connection
// WINDOW_UPDATE before any HEADERS.
func (s *proxySample) checkOpening(c *capture.ProxyTLS) {
	h2 := s.p.HTTP2
	if !slices.Equal(c.Settings, h2.Settings) {
		s.fail("connection %d: SETTINGS %v, http2.settings %v", c.Conn, c.Settings, h2.Settings)
	}

	var update uint32

	for _, f := range c.H2.Frames {
		if f.Type == "HEADERS" {
			break
		}

		if f.Type == "WINDOW_UPDATE" && f.Stream == 0 {
			update = f.Increment

			break
		}
	}

	if update != h2.WindowUpdate {
		s.fail("connection %d: connection WINDOW_UPDATE %d before the first HEADERS, http2.windowUpdate %d", c.Conn, update, h2.WindowUpdate)
	}
}

// h2Fields checks a CONNECT's decoded fields over HTTP/2 and folds them
// into connect or connectAuth; it reports whether it carried
// credentials.
func (s *proxySample) h2Fields(r capture.ProxyRequest) bool {
	var pseudo []string

	for _, name := range s.p.HTTP2.PseudoHeaderOrder {
		switch name {
		case ":method":
			pseudo = append(pseudo, name+": "+http.MethodConnect)
		case ":authority":
			pseudo = append(pseudo, name+": "+r.Target)
		}
	}

	if len(r.Lines) < len(pseudo) || !slices.Equal(r.Lines[:len(pseudo)], pseudo) {
		s.fail("CONNECT on stream %d of connection %d: %q, want it to start %q (http2.pseudoHeaderOrder)", r.Stream, r.Conn, r.Lines, pseudo)

		return false
	}

	var (
		fields []profile.Header
		auth   bool
	)

	for _, l := range r.Lines[len(pseudo):] {
		name, value, _ := strings.Cut(l, ": ")
		switch name {
		case "user-agent":
			if value != userAgent(s.p) {
				s.fail("CONNECT on stream %d of connection %d: user-agent %q, the profile's %q", r.Stream, r.Conn, value, userAgent(s.p))
			}

			value = ""

		case "proxy-authorization":
			auth, value = true, ""
		}

		fields = append(fields, profile.Header{
			Name:  name,
			Value: value,
		})
	}

	list := &s.out.Connect
	if auth {
		list = &s.out.ConnectAuth
	}

	switch {
	case *list == nil:
		*list = fields
	case !slices.Equal(*list, fields):
		s.fail("CONNECT on stream %d of connection %d: fields %v, another CONNECT's %v", r.Stream, r.Conn, fields, *list)
	}

	return auth
}

// checkStream checks a CONNECT stream's frames: its HEADERS frame's flags
// and priority, the stream's top-up, and how the browser reset it.
func (s *proxySample) checkStream(tr *h2trace.Trace, r capture.ProxyRequest) {
	h2 := s.p.HTTP2

	var (
		headers *h2trace.Frame
		next    *h2trace.Frame
	)

	for i := range tr.Frames {
		f := &tr.Frames[i]
		if f.Stream != r.Stream {
			continue
		}

		switch {
		case headers == nil && f.Type == "HEADERS":
			headers = f
		case headers != nil && next == nil:
			next = f
		}

		if f.Type != "RST_STREAM" || r.Status != http.StatusOK {
			continue
		}

		switch {
		case s.out.Reset == "":
			s.out.Reset = f.ErrCode
		case f.ErrCode != s.out.Reset:
			s.fail("stream %d of connection %d reset with %s, another tunnel's with %s", r.Stream, r.Conn, f.ErrCode, s.out.Reset)
		}
	}

	if headers == nil || headers.Priority == nil {
		s.fail("stream %d of connection %d: no HEADERS frame with a priority", r.Stream, r.Conn)

		return
	}

	const endStream = 0x1

	if headers.Flags&endStream != 0 {
		s.fail("stream %d of connection %d: the CONNECT's HEADERS ends the stream", r.Stream, r.Conn)
	}

	pri := headers.Priority

	switch {
	case s.out.Weight == 0:
		s.out.Weight = pri.Weight
	case pri.Weight != s.out.Weight:
		s.fail("stream %d of connection %d: weight %d, another CONNECT's %d", r.Stream, r.Conn, pri.Weight, s.out.Weight)
	}

	switch hp := h2.HeadersPriority; {
	case hp == nil:
		s.fail("the profile's HEADERS carry no priority (http2.headersPriority), a CONNECT's do")
	case h2.ChainPriorities && !pri.Exclusive:
		s.fail("stream %d of connection %d: not exclusive, where http2.chainPriorities chains every stream", r.Stream, r.Conn)
	case !h2.ChainPriorities && (pri.Exclusive != hp.Exclusive || pri.DependsOn != hp.DependsOn):
		s.fail("stream %d of connection %d: depends on %d (exclusive %t), http2.headersPriority on %d (%t)", r.Stream, r.Conn, pri.DependsOn, pri.Exclusive, hp.DependsOn, hp.Exclusive)
	}

	if inc := h2.StreamWindowUpdate; inc > 0 && (next == nil || next.Type != "WINDOW_UPDATE" || next.Increment != inc) {
		s.fail("stream %d of connection %d: no WINDOW_UPDATE of %d after its HEADERS (http2.streamWindowUpdate)", r.Stream, r.Conn, inc)
	}
}

// resumedShare is the share of the sample's tunnel connections after the
// first that offered a session, in the order the proxy accepted them;
// false with fewer than two tunnel connections.
func (s *proxySample) resumedShare() (float64, bool) {
	var tunnels []int

	for _, r := range s.reqs {
		if r.Status == http.StatusOK && !slices.Contains(tunnels, r.Conn) {
			tunnels = append(tunnels, r.Conn)
		}
	}

	if len(tunnels) < 2 {
		return 0, false
	}

	resumed := 0

	for _, n := range tunnels[1:] {
		if c := s.conn(n); c != nil && c.Resumed {
			resumed++
		}
	}

	return float64(resumed) / float64(len(tunnels)-1), true
}

// tunnelledResumptions counts the tunnels whose first ClientHello, to the
// capture host, offered a session.
func (s *proxySample) tunnelledResumptions() int {
	n := 0

	for _, r := range s.reqs {
		if r.ClientHello == "" {
			continue
		}

		records, err := hex.DecodeString(r.ClientHello)
		if err != nil {
			s.fail("tunnel to %s on connection %d: %w", r.Target, r.Conn, err)

			continue
		}

		_, msg, err := server.ParseRecords(records)
		if err != nil {
			s.fail("tunnel to %s on connection %d: %w", r.Target, r.Conn, err)

			continue
		}

		h, err := server.ParseClientHello(msg)
		if err != nil {
			s.fail("tunnel to %s on connection %d: %w", r.Target, r.Conn, err)

			continue
		}

		if extension(&h, profile.ExtPreSharedKey) != nil {
			n++
		}
	}

	return n
}

// userAgent is the user agent in p's headers.
func userAgent(p *profile.Spec) string {
	for _, h := range p.Headers {
		if h.Name == "user-agent" {
			return h.Value
		}
	}

	return ""
}
