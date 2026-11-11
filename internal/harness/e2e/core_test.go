package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/diff"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/proxy"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/internal/rpc/client"
	"github.com/roachadam/mimicry/internal/rpc/convert"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/profile"
)

// Requests sent through mimicry-core (decision 0069) are mimicry's own on
// the wire: the kinds page (a typed navigation that sets cookies, every
// request kind from the page with its options, CORS preflights, 302s the
// client follows, a form POST, uploads inline and streamed, a caller's
// header order, an Exact request and a placed header) sent once by
// mimicry's Go client and once through the core binary, over HTTP/2,
// HTTP/1.1 and plain HTTP, compares equal as the server recorded it
// (coreView: every connection's ClientHello, every HTTP/2 frame and
// header block, every HTTP/1.1 request's lines).
func TestCoreMatchesGoKinds(t *testing.T) {
	for _, name := range testProfiles(t) {
		for _, sc := range []*server.Scenario{server.Kinds, server.KindsHTTP1, server.KindsPlain} {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				t.Parallel()

				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}

				routes := directRoutes(t, p, sc)

				var pages [2]*capture.Page
				for i, r := range routes {
					pages[i] = runCoreKinds(t, p, sc, r)
				}

				compareCorePages(t, p, pages)
			})
		}
	}
}

// mimicry-core's WebSockets are mimicry's on the wire: the websocket
// pages' navigations and fetches, then their sockets (text and binary
// messages of every length encoding, subprotocols, permessage-deflate
// with its parameters), each closed with 1000 "", sent once by mimicry's
// Go client and once through the core binary, over HTTP/1.1 and HTTP/2
// extended CONNECT, directly and through the capture proxy (HTTP with
// credentials and the browser's 407 flow, plain ws:// in absolute form,
// and SOCKS5). The server's records compare equal as coreView renders
// them (each client frame's flags, opcode, length encoding and unmasked
// payload), and so do the proxy's (each CONNECT's lines, each SOCKS5
// greeting and request).
func TestCoreMatchesGoWebSockets(t *testing.T) {
	for _, name := range testProfiles(t) {
		for _, sc := range []*server.Scenario{server.WebSocket, server.WebSocketH2, server.ProxyWebSocketAuth, server.ProxyWebSocketSOCKS5} {
			t.Run(name+"/"+sc.Name, func(t *testing.T) {
				t.Parallel()

				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}

				var (
					routes [2]coreRoute
					w      *profile.ProxyWebSocket
				)

				if p.Proxy != nil {
					w = p.Proxy.WebSocket
				}

				switch {
				case sc.Proxy == "":
					routes = directRoutes(t, p, sc)
				case w == nil || sc.ProxyAuth && !w.Credentials:
					// Both ways refuse it (ErrNotMeasured).
					t.Skipf("%s was not measured opening a WebSocket through this proxy", name)
				default:
					routes = proxiedRoutes(t, p, sc)
				}

				var pages [2]*capture.Page
				for i, r := range routes {
					pages[i] = runCoreSockets(t, sc, r)
				}

				compareCorePages(t, p, pages)

				if sc.Proxy != "" {
					compareLines(t, "the proxy's requests", proxyView(routes[0].px.Requests()), proxyView(routes[1].px.Requests()))
				}
			})
		}
	}
}

// coreRoute is a page's server and the way its requests go out: mimicry's
// Go client, or mimicry-core. px is the capture proxy of a proxied page.
type coreRoute struct {
	s  *pageSession
	v  via
	px *proxy.Proxy
}

// via sends a page's requests one way.
type via interface {
	send(q pageReq) error
	dial(target string, opts mimicry.WebSocketOptions) (wsConn, error)
}

// pageReq is one request of a page: what a caller of mimicry's Go API
// sets, which an SDK sends mimicry-core as its Request.
type pageReq struct {
	method string
	target string
	r      mimicry.Request
	header http.Header
	body   string
	// stream sends body as a stream of unknown length (the core's
	// stream_body; in Go a reader whose length the request does not
	// know), exact as mimicry.Exact.
	stream bool
	exact  bool
}

// wsConn is an open WebSocket of either way.
type wsConn interface {
	write(binary bool, data []byte) error
	read() ([]byte, error)
	// close closes the socket with 1000 "", returning once the server
	// answered.
	close() error
}

// noDial has the Go client reach a page as mimicry-core does, which has no
// DialContext: hosts resolve through the DNS stub, ports are the URL's.
func noDial(cfg *mimicry.Config) {
	cfg.DialContext = nil
}

// directRoutes serves sc twice for p: to mimicry's Go client and to
// mimicry-core, both resolving the page's hosts through a DNS stub. The
// core's server listens on a port whose digits have the HPACK Huffman
// lengths of the Go client's (listenLike), so that every header block,
// frame and record naming it has the same length; the digits themselves
// are replaced before comparing.
func directRoutes(t *testing.T, p *profile.Spec, sc *server.Scenario) [2]coreRoute {
	t.Helper()

	gs := startPageWith(t, p, sc, noDial)
	cs := startPageOn(t, p, sc, noDial, listenLike(t, gs.port), nil)

	return [2]coreRoute{
		{
			s: gs,
			v: goVia{
				s: gs,
			},
		},
		{
			s: cs,
			v: startCore(t, p, cs, stubDNSFor(t, nil), false),
		},
	}
}

// proxiedRoutes serves sc twice for p behind the capture proxy, as
// startProxiedPage does (whose ports are fixed, so equal): to mimicry's
// Go client and to mimicry-core, with the proxy's credentials given the
// browser's way where p measured it (Config.BrowserProxyAuth), else
// upfront.
func proxiedRoutes(t *testing.T, p *profile.Spec, sc *server.Scenario) [2]coreRoute {
	t.Helper()

	var user *url.Userinfo

	if sc.ProxyAuth {
		user = url.UserPassword(server.ProxyUser, server.ProxyPassword)
	}

	browserAuth := p.Proxy != nil && p.Proxy.ConnectCredentials != ""
	gs, gpx := startProxiedPage(t, p, sc, user, browserAuth)
	cs, cpx := startProxiedPage(t, p, sc, user, browserAuth)

	return [2]coreRoute{
		{
			s: gs,
			v: goVia{
				s: gs,
			},
			px: gpx,
		},
		{
			s:  cs,
			v:  startCore(t, p, cs, "", browserAuth),
			px: cpx,
		},
	}
}

// listenLike listens on a loopback port with as many digits as port,
// taking as many bits in HPACK's Huffman code (RFC 7541, Appendix B: 0, 1
// and 2 take 5, the other digits 6): the first free one counting up from
// port (the system hands out ports in sequence, so asking it for any
// rarely finds one).
func listenLike(t *testing.T, port string) net.Listener {
	t.Helper()

	bits := func(p string) int {
		n := 0

		for _, c := range p {
			n += 6
			if c <= '2' {
				n--
			}
		}

		return n
	}

	from, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}

	for n := from + 1; n < from+20000; n++ {
		p := strconv.Itoa(n%65536 + 1024*(n/65536))
		if len(p) != len(port) || bits(p) != bits(port) {
			continue
		}

		if ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:"+p); err == nil {
			return ln
		}
	}

	t.Fatalf("no loopback port like %s", port)

	return nil
}

// send sends q, failing the test if it fails.
func send(t *testing.T, v via, q pageReq) {
	t.Helper()

	if err := v.send(q); err != nil {
		t.Fatalf("%s %s (streamed %v): %v", q.method, q.target, q.stream, err)
	}
}

// runCoreKinds sends the kinds page's requests (runKindsPage's), and those
// the page cannot show, through r, and returns the server's capture.
func runCoreKinds(t *testing.T, p *profile.Spec, sc *server.Scenario, r coreRoute) *capture.Page {
	t.Helper()

	hosts, _, _ := sc.MultiHost()
	page := r.s.url(hosts[0], "/")
	send(t, r.v, pageReq{
		method: http.MethodGet,
		target: page,
	})

	from, err := url.Parse(page)
	if err != nil {
		t.Fatal(err)
	}

	steps := map[string]server.KindsStep{}
	for _, st := range slices.Concat(server.KindsMarkup(), server.KindsSteps()) {
		steps[st.Label] = st
	}

	for _, k := range kindsRequests() {
		host := hosts[0]
		if st, ok := steps[k.label]; ok {
			host = st.Host
		}

		k.req.From = from
		if strings.HasPrefix(k.label, "font-css.") {
			k.req.Stylesheet = from.JoinPath("k", "style-fonts.so")
		}

		q := pageReq{
			method: k.method,
			target: r.s.url(host, "/k/"+k.label),
			r:      k.req,
			header: k.headers,
			body:   k.body,
		}

		if blockedBeacon(p, k.label) {
			if err := r.v.send(q); err == nil {
				t.Errorf("%s: sent a cross-site beacon its profile blocks", k.label)
			}

			continue
		}

		send(t, r.v, q)
	}

	fetch := mimicry.Request{
		Kind:        mimicry.Fetch,
		From:        from,
		Credentials: mimicry.CredentialsInclude,
	}
	placed := fetch

	placed.HeaderPlacement = []mimicry.Place{
		{
			Header: "x-mimicry",
			Before: "user-agent",
		},
	}

	upload := strings.Repeat("mimicry-core upload ", 5000)

	for _, q := range []pageReq{
		{
			method: http.MethodPost,
			target: r.s.url(hosts[0], "/k/fetch-json.so"),
			r:      fetch,
			header: http.Header{
				"Content-Type": {"application/octet-stream"},
			},
			body: upload,
		},
		{
			method: http.MethodPost,
			target: r.s.url(hosts[0], "/k/fetch-json.so"),
			r:      fetch,
			header: http.Header{
				"Content-Type": {"application/octet-stream"},
			},
			body:   upload,
			stream: true,
		},
		{
			method: http.MethodGet,
			target: r.s.url(hosts[0], "/k/fetch-custom.so"),
			r:      fetch,
			header: http.Header{
				"X-B":                  {"2"},
				"X-A":                  {"1"},
				mimicry.HeaderOrderKey: {"x-a", "accept", "user-agent", "x-b"},
			},
		},
		{
			method: http.MethodGet,
			target: r.s.url(hosts[0], "/k/fetch.so"),
			r:      fetch,
			header: http.Header{
				"Accept":  {"*/*"},
				"X-Exact": {"1"},
			},
			exact: true,
		},
		{
			method: http.MethodGet,
			target: r.s.url(hosts[0], "/k/fetch-custom.so"),
			r:      placed,
			header: http.Header{
				"X-Mimicry": {"1"},
			},
		},
	} {
		send(t, r.v, q)
	}

	send(t, r.v, pageReq{
		method: http.MethodGet,
		target: r.s.url(hosts[0], "/done"),
	})

	return r.s.awaitPage().Page
}

// coreSockets are the websocket pages' sockets the core is checked with.
// Each takes the browser's wait for the server to end the transport after
// the close (Chrome's 2 s), so of the websocket page's own only so-basic
// (text and binary, 7-, 16- and 64-bit lengths, a server ping),
// cs-protocols (cross-site, subprotocols) and so-deflate-nct
// (permessage-deflate with a parameter, empty and incompressible
// messages); the proxy pages' all. Left out: on a page reached
// directly, plain ws:// to the plain port, whose digits listenLike does
// not match.
func coreSockets(sc *server.Scenario, direct bool) ([]server.WebSocketStep, []server.WebSocketPageStep) {
	sockets, steps, _ := sc.WebSocketPage()

	keep := func(plain bool) bool {
		return !plain || !direct
	}
	wanted := func(label string) bool {
		return sc.Proxy != "" || slices.Contains([]string{"so-basic", "cs-protocols", "so-deflate-nct"}, label)
	}

	sockets = slices.DeleteFunc(slices.Clone(sockets), func(st server.WebSocketStep) bool {
		return !wanted(st.Label) || !keep(st.Plain)
	})
	steps = slices.DeleteFunc(slices.Clone(steps), func(st server.WebSocketPageStep) bool {
		return !keep(st.Plain)
	})

	return sockets, steps
}

// runCoreSockets runs the websocket page as runWebSocketPage does,
// through r: each page's navigation and fetches, then its sockets, each
// sending its messages, reading their echoes and closing with 1000 "".
func runCoreSockets(t *testing.T, sc *server.Scenario, r coreRoute) *capture.Page {
	t.Helper()

	sockets, steps := coreSockets(sc, r.px == nil)

	var page *url.URL

	label := ""

	for i, st := range steps {
		q := pageReq{
			method: http.MethodGet,
			target: r.s.wsURL(st.Plain, "http", st.Host, st.Path),
			r: mimicry.Request{
				Kind:        mimicry.Fetch,
				From:        page,
				Credentials: mimicry.CredentialsInclude,
			},
		}

		if st.Nav {
			q.r = mimicry.Request{
				From: page,
			}
			if page != nil {
				q.header = http.Header{
					"Sec-Fetch-User": nil,
				}
			}
		}

		send(t, r.v, q)

		if st.Nav {
			u, err := url.Parse(q.target)
			if err != nil {
				t.Fatal(err)
			}

			page, label = u, st.Label
		}

		if i+1 < len(steps) && !steps[i+1].Nav {
			continue
		}

		for _, ws := range sockets {
			if ws.Page != label {
				continue
			}

			c, err := r.v.dial(r.s.wsURL(ws.Plain, "ws", ws.Host, ws.Path()), mimicry.WebSocketOptions{
				From:      page,
				Protocols: ws.Protocols,
			})
			if err != nil {
				t.Fatalf("%s: %v", ws.Label, err)
			}

			exchange(t, ws.Label, c, ws.Sends)
		}
	}

	send(t, r.v, pageReq{
		method: http.MethodGet,
		target: r.s.url(steps[0].Host, "/done"),
	})

	return r.s.awaitPage().Page
}

// exchange sends msgs on c, reads their echoes (as a page's onmessage
// does, from the start) and closes c.
func exchange(t *testing.T, label string, c wsConn, msgs []server.WSMessage) {
	t.Helper()

	type read struct {
		data []byte
		err  error
	}

	reads := make(chan read, len(msgs)+1)

	go func() {
		for {
			data, err := c.read()

			reads <- read{
				data: data,
				err:  err,
			}

			if err != nil {
				return
			}
		}
	}()

	for i, m := range msgs {
		if err := c.write(m.Binary, m.Data()); err != nil {
			t.Fatalf("%s: message %d: %v", label, i+1, err)
		}
	}

	for i, m := range msgs {
		r := <-reads
		if r.err != nil {
			t.Fatalf("%s: echo %d: %v", label, i+1, r.err)
		}

		if !bytes.Equal(r.data, m.Data()) {
			t.Errorf("%s: echo %d is not the message sent", label, i+1)
		}
	}

	if err := c.close(); err != nil {
		t.Errorf("%s: closing: %v", label, err)
	}

	if r := <-reads; r.err == nil {
		t.Errorf("%s: a message after the close", label)
	}
}

// goVia sends a page's requests through mimicry's Go client.
type goVia struct {
	s *pageSession
}

func (g goVia) send(q pageReq) error {
	body := q.body
	if q.stream {
		body = ""
	}

	req, err := g.s.request(q.method, q.target, q.r, q.header, body)
	if err != nil {
		return err
	}

	if q.stream {
		req.Body = io.NopCloser(strings.NewReader(q.body))
		req.ContentLength = -1
	}

	if q.exact {
		req = mimicry.Exact(req)
	}

	resp, err := g.s.client.Do(req)
	if err != nil {
		return err
	}

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.Body.Close()
}

func (g goVia) dial(target string, opts mimicry.WebSocketOptions) (wsConn, error) {
	tr, ok := g.s.client.Transport.(*mimicry.Transport)
	if !ok {
		return nil, errors.New("the page session's client has no mimicry Transport")
	}

	ctx := g.s.t.Context()

	ws, _, err := tr.DialWebSocket(ctx, target, opts)
	if err != nil {
		return nil, err
	}

	return goSocket{
		ctx: ctx,
		ws:  ws,
	}, nil
}

// goSocket is a WebSocket of mimicry's Go client.
type goSocket struct {
	ctx context.Context //nolint:containedctx // the test's, for each call
	ws  *mimicry.WebSocket
}

func (g goSocket) write(binary bool, data []byte) error {
	typ := mimicry.TextMessage
	if binary {
		typ = mimicry.BinaryMessage
	}

	return g.ws.WriteMessage(g.ctx, typ, data)
}

func (g goSocket) read() ([]byte, error) {
	_, data, err := g.ws.ReadMessage(g.ctx)

	return data, err
}

func (g goSocket) close() error {
	return g.ws.Close(1000, "")
}

// coreVia sends a page's requests through mimicry-core, as an SDK does:
// a client (NewClient, so redirects are followed in the core) whose
// Config is the Go client's as data.
type coreVia struct {
	t  *testing.T
	c  *client.Client
	id uint64
}

// startCore starts mimicry-core for p's page s, with a client reaching
// it as the Go client does: s's CA, its proxy (credentials given the
// browser's way when browserAuth), else the DNS stub at dns.
func startCore(t *testing.T, p *profile.Spec, s *pageSession, dns string, browserAuth bool) *coreVia {
	t.Helper()

	var stderr bytes.Buffer

	cmd := exec.Command(coreBinary(t)) //nolint:gosec,noctx // the core this test built; closing its connection stops it

	cmd.Stderr = &stderr

	rwc, err := client.Spawn(cmd)
	if err != nil {
		t.Fatal(err)
	}

	c, err := client.New(t.Context(), rwc, client.Config{})
	if err != nil {
		_ = rwc.Close()
		_ = cmd.Wait()

		t.Fatalf("mimicry-core: %v: %s", err, stderr.String())
	}

	t.Cleanup(func() {
		c.Close()

		if err := cmd.Wait(); err != nil {
			t.Errorf("mimicry-core: %v: %s", err, stderr.String())
		}
	})

	roots, err := os.ReadFile(s.ca.CertPath)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &pb.Config{
		Profile:          coreProfile(t, c, p),
		RootCas:          roots,
		DnsServer:        dns,
		BrowserProxyAuth: browserAuth,
	}
	if s.proxy != nil {
		cfg.Proxy = s.proxy.String()
	}

	r, err := c.Call(t.Context(), &pb.Call{
		Method: &pb.Call_NewClient{
			NewClient: &pb.NewClient{
				Config: cfg,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return &coreVia{
		t:  t,
		c:  c,
		id: r.GetNewClient().GetClient(),
	}
}

// coreProfile gives the core p as JSON (ParseProfile): the Go client's
// profile exactly, its farbling off as loadProfile leaves it.
func coreProfile(t *testing.T, c *client.Client, p *profile.Spec) *pb.ProfileRef {
	t.Helper()

	data, err := profile.Encode(p)
	if err != nil {
		t.Fatal(err)
	}

	k, err := c.Open(t.Context(), &pb.Call{
		Method: &pb.Call_ParseProfile{
			ParseProfile: &pb.ParseProfile{},
		},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()

	if _, err := k.Write(data); err != nil {
		t.Fatal(err)
	}

	if err := k.CloseWrite(); err != nil {
		t.Fatal(err)
	}

	r, err := k.Reply()
	if err != nil {
		t.Fatal(err)
	}

	if e := r.GetError(); e != nil {
		t.Fatalf("ParseProfile: %s: %s", e.GetCode(), e.GetMessage())
	}

	return &pb.ProfileRef{
		Profile: &pb.ProfileRef_Id{
			Id: r.GetProfile().GetId(),
		},
	}
}

func (cv *coreVia) send(q pageReq) error {
	h := q.header.Clone()
	order := h[mimicry.HeaderOrderKey]

	delete(h, mimicry.HeaderOrderKey)

	req := &pb.Request{
		Method:      q.method,
		Url:         q.target,
		Header:      convert.HeaderValues(h),
		Options:     coreOptions(q.r),
		Exact:       q.exact,
		HeaderOrder: order,
	}

	var body io.Reader

	if q.stream {
		body = strings.NewReader(q.body)
	} else {
		req.Body = []byte(q.body)
	}

	_, _, err := cv.c.Do(cv.t.Context(), cv.id, req, body)

	return err
}

// coreOptions is r as mimicry-core's Options.
func coreOptions(r mimicry.Request) *pb.Options {
	o := &pb.Options{
		Kind:        pb.Kind(r.Kind),
		Priority:    pb.Priority(r.Priority),
		CrossOrigin: pb.CrossOrigin(r.CrossOrigin),
		Credentials: pb.Credentials(r.Credentials),
		NoCors:      r.NoCORS,
		IgnoreCors:  r.IgnoreCORS,
		ClientHints: pb.HintDelegation(r.ClientHints),
	}

	if r.From != nil {
		o.Page = r.From.String()
	}

	if r.Stylesheet != nil {
		o.Stylesheet = r.Stylesheet.String()
	}

	for _, pl := range r.HeaderPlacement {
		o.HeaderPlacement = append(o.HeaderPlacement, &pb.Place{
			Header: pl.Header,
			After:  pl.After,
			Before: pl.Before,
		})
	}

	return o
}

func (cv *coreVia) dial(target string, opts mimicry.WebSocketOptions) (wsConn, error) {
	o := &pb.WebSocketOptions{
		Protocols: opts.Protocols,
		Header:    convert.HeaderValues(opts.Header),
	}
	if opts.From != nil {
		o.Page = opts.From.String()
	}

	ws, _, err := cv.c.DialWebSocket(cv.t.Context(), &pb.DialWebSocket{
		Transport: cv.id,
		Url:       target,
		Options:   o,
	})
	if err != nil {
		return nil, err
	}

	return coreSocket{
		cv: cv,
		ws: ws,
	}, nil
}

// coreSocket is a WebSocket of mimicry-core.
type coreSocket struct {
	cv *coreVia
	ws *client.WebSocket
}

func (c coreSocket) write(binary bool, data []byte) error {
	typ := byte(pb.MessageType_MESSAGE_TYPE_TEXT)
	if binary {
		typ = byte(pb.MessageType_MESSAGE_TYPE_BINARY)
	}

	return c.ws.WriteMessage(c.cv.t.Context(), typ, data)
}

func (c coreSocket) read() ([]byte, error) {
	_, data, err := c.ws.ReadMessage()

	return data, err
}

func (c coreSocket) close() error {
	if _, err := c.cv.c.Call(c.cv.t.Context(), &pb.Call{
		Method: &pb.Call_CloseWebsocket{
			CloseWebsocket: &pb.CloseWebSocket{
				Websocket: c.ws.ID(),
				Code:      1000,
			},
		},
	}); err != nil {
		return err
	}

	return c.ws.CloseWrite()
}

// The mimicry-core binary, built once per test binary (coreBinary) and
// removed after the tests (removeCore).
var coreBuild struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// coreBinary builds cmd/mimicry-core as the SDKs ship it (CGO_ENABLED=0)
// once, and returns its path; a failed build fails every test asking.
func coreBinary(t *testing.T) string {
	t.Helper()

	coreBuild.once.Do(func() {
		// Not t.TempDir: the build outlives the first test asking for it,
		// and TestMain removes it (removeCore).
		dir, err := os.MkdirTemp("", "mimicry-core") //nolint:usetesting // shared by every test, see above
		if err != nil {
			coreBuild.err = err

			return
		}

		coreBuild.dir = dir

		path := filepath.Join(dir, "mimicry-core")

		if runtime.GOOS == "windows" {
			path += ".exe"
		}

		cmd := exec.CommandContext(t.Context(), "go", "build", "-o", path, "github.com/roachadam/mimicry/cmd/mimicry-core") //nolint:gosec // the test's own build

		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

		if out, err := cmd.CombinedOutput(); err != nil {
			coreBuild.err = fmt.Errorf("building mimicry-core: %w\n%s", err, out)

			return
		}

		coreBuild.path = path
	})

	if coreBuild.err != nil {
		t.Fatal(coreBuild.err)
	}

	return coreBuild.path
}

// removeCore removes coreBinary's build.
func removeCore() {
	if coreBuild.dir != "" {
		_ = os.RemoveAll(coreBuild.dir)
	}
}

// compareCorePages compares the server's records of the Go client's page
// (pages[0]) and the core's (pages[1]): their ClientHellos as
// compareCoreHello does, the rest as coreView renders it.
func compareCorePages(t *testing.T, p *profile.Spec, pages [2]*capture.Page) {
	t.Helper()

	g, c := pages[0], pages[1]
	for i := range min(len(g.Connections), len(c.Connections)) {
		gc, cc := g.Connections[i], c.Connections[i]
		if gc.Hello != nil && cc.Hello != nil {
			compareCoreHello(t, p, fmt.Sprintf("connection %d (%s)", i+1, gc.Host), gc.Hello, cc.Hello, cc.Resumed)
		}
	}

	compareLines(t, "the page", coreView(g, p.HTTP2.MaxDataFrame), coreView(c, p.HTTP2.MaxDataFrame))
}

// compareCoreHello compares the core's ClientHello (got) with the Go
// client's (want) exactly but for what the profile draws per connection:
// a shuffled extension order, whose pinned places alone are compared,
// and GREASE ECH's cipher suite and payload length, which must be among
// the profile's (of its resumed hellos when resumed).
func compareCoreHello(t *testing.T, p *profile.Spec, what string, want, got *profile.TLS, resumed bool) {
	t.Helper()

	w := profile.Spec{
		TLS: *want,
	}
	g := profile.Spec{
		TLS: *got,
	}

	w.TLS.Extensions = slices.Clone(want.Extensions)

	for _, e := range p.TLS.Extensions {
		if e.ECH == nil {
			continue
		}

		for i, we := range w.TLS.Extensions {
			if we.ECH == nil {
				continue
			}

			ech := *we.ECH

			ech.CipherSuites = e.ECH.CipherSuites
			ech.PayloadLengths = e.ECH.PayloadLengths

			if r := p.TLS.Resumption; r != nil && resumed {
				ech.PayloadLengths = r.ECHPayloadLengths
			}

			w.TLS.Extensions[i].ECH = &ech
		}
	}

	w.TLS.ExtensionOrder, g.TLS.ExtensionOrder = p.TLS.ExtensionOrder, p.TLS.ExtensionOrder

	for _, d := range diff.Compare(&w, &g) {
		t.Errorf("%s's ClientHello through the core: %s", what, d)
	}
}

// compareLines reports where the core's lines (got) first part from the
// Go client's (want), and how many differ.
func compareLines(t *testing.T, what string, want, got []string) {
	t.Helper()

	line := func(lines []string, i int) string {
		if i < len(lines) {
			return lines[i]
		}

		return "(none)"
	}

	first, n := -1, 0

	for i := range max(len(want), len(got)) {
		if line(want, i) == line(got, i) {
			continue
		}

		if first < 0 {
			first = i
		}

		n++
	}

	if first < 0 {
		return
	}

	from := max(first-3, 0)
	t.Errorf("%s: %d of %d lines differ, from line %d:\n Go client's:\n   %s\n core's:\n   %s", what, n, max(len(want), len(got)), first+1,
		strings.Join(want[from:min(first+4, len(want))], "\n   "),
		strings.Join(got[from:min(first+4, len(got))], "\n   "))
}

// coreView renders what the server recorded of a page as lines: every
// connection (host, protocol, resumption, requests carried, record
// version, X25519 key reuse), every HTTP/2 frame (type, stream, flags,
// length, increment, error code, PING data, priority) and header block
// (each HPACK instruction: representation, index, table size, name,
// value, Huffman coding) and TLS record, every HTTP/1.1 request's lines
// and upload cut, and every WebSocket (where it ran, the server's answer,
// how it ended, and each client frame's FIN, RSV bits, opcode, mask bit,
// length encoding, length, unmasked payload and, over HTTP/2, the DATA
// frames that carried it, as dataCut renders them against the profile's
// limit). Left out is what is random by design or timing: when anything
// arrived, ClientHello bytes (key shares, GREASE, shuffles:
// compareCoreHello compares them parsed), ticket and PSK hashes, masking
// keys and Sec-WebSocket-Key, where a pong falls among a socket's frames
// (when the server's ping arrived: pongs are listed after the others),
// where the stream's flow-control credit cut a WebSocket frame's DATA
// frames, the increments of the connection's credit returns after its
// opening flight: a timer-driven one (Chrome's 5 s) returns what was read
// by then, and the frames after a connection's last request when its
// HEADERS ended the stream (trailing): the server answers it at its
// header block, and /done, on another connection, stops this one's trace
// whether or not its reader got to them (Firefox's stream WINDOW_UPDATE,
// sent in the same write). Ports after .test hosts are replaced
// (directRoutes keeps their lengths equal).
func coreView(page *capture.Page, limit uint32) []string {
	var out []string

	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf(format, args...))
	}

	for i, c := range page.Connections {
		reused, both := x25519Reuse(c.ClientHelloBytes())
		add("connection %d: %s %s resumed=%v/%v psk=%d ticket=%v requests=%d record=%#04x retry=%v refused=%q ech=%v x25519reuse=%v/%v",
			i+1, c.Host, c.Protocol, c.Resumed, c.Accepted, c.PSKIdentities, c.Ticket != "", c.Requests, c.RecordVersion, c.Retry != nil, c.Refused, c.ECH != nil, reused, both)
	}

	add("quic connections: %d", len(page.QUIC))

	// A WebSocket's DATA frames are listed with its frames (their cuts),
	// where a pong's place does not move them.
	sockets := map[[2]uint32]bool{}

	for _, ws := range page.WebSockets {
		if ws.Protocol == "h2" {
			sockets[[2]uint32{uint32(ws.Trace), ws.Stream}] = true
		}
	}

	for i, tr := range page.H2 {
		add("h2 connection %d: %s", i+1, tr.Host)

		var acks []string

		opening, firstRequest := true, len(tr.Frames)
		end := trailing(tr)

		for fi, f := range tr.Frames {
			if f.Type == "DATA" && sockets[[2]uint32{uint32(i), f.Stream}] || fi >= end && !acknowledges(f) {
				continue
			}

			increment := strconv.Itoa(int(f.Increment))
			if f.Type == "HEADERS" && opening {
				opening, firstRequest = false, fi
			}

			if !opening && f.Type == "WINDOW_UPDATE" && f.Stream == 0 {
				increment = "(credit)"
			}

			l := fmt.Sprintf("  %s stream=%d flags=%#x length=%d increment=%s err=%s data=%s priority=%s",
				f.Type, f.Stream, f.Flags, f.Length, increment, f.ErrCode, f.Data, framePriority(f.Priority))
			if acknowledges(f) {
				acks = append(acks, l)

				continue
			}

			out = append(out, l)
		}

		out = append(out, acks...)

		for _, r := range tr.Requests {
			add("  request stream=%d %s", r.Stream, normalisePort(r.Path))

			for _, ins := range r.Instructions {
				add("    %s index=%d size=%d %q: %q huffman=%v/%v", ins.Rep, ins.Index, ins.Size, ins.Name, coreValue(ins.Name, ins.Value), ins.HuffmanName, ins.HuffmanValue)
			}
		}

		// The records of the opening flight and first request: later ones
		// follow each write, and records holding only acknowledgements
		// fall where the server's frames arrived.
		for _, rec := range tr.Records {
			own := slices.ContainsFunc(rec.Frames, func(fi int) bool {
				return !acknowledges(tr.Frames[fi])
			})
			early := slices.ContainsFunc(rec.Frames, func(fi int) bool {
				return fi <= firstRequest
			})

			if !rec.Preface && (!own || !early) {
				continue
			}

			var types []string
			for _, fi := range rec.Frames {
				if fi < end {
					types = append(types, tr.Frames[fi].Type)
				}
			}

			add("  opening record %d preface=%v frames=%v", rec.Size, rec.Preface, types)
		}
	}

	for _, r := range page.H1 {
		add("h1 connection %d (%s): %s", r.Conn, r.Host, normalisePort(r.Line))

		for _, l := range r.Headers {
			name, value, _ := strings.Cut(l, ": ")
			add("  %s: %s", name, coreValue(name, value))
		}

		if u := r.Upload; u != nil {
			add("  upload offset=%d head=%d body=%d lead=%d trail=%d", u.Offset, u.Head, u.Body, u.Lead, u.Trail)

			for _, run := range u.Records {
				add("    %d records of %d", run.Count, run.Size)
			}
		}
	}

	for _, ws := range page.WebSockets {
		add("websocket %s%s %s plain=%v connection=%d proxy=%d request=%d trace=%d stream=%d status=%d subprotocol=%q extensions=%q closedBy=%s end=%q error=%q",
			ws.Host, ws.Path, ws.Protocol, ws.Plain, ws.Connection, ws.ProxyConn, ws.Request, ws.Trace, ws.Stream, ws.Status, ws.Subprotocol, ws.Extensions, ws.ClosedBy, ws.StreamEnd, ws.Error)

		var pongs []string

		var (
			cuts  map[frameKey][]uint32
			ended bool
		)

		if ws.Protocol == "h2" && ws.Trace >= 0 && ws.Trace < len(page.H2) {
			cuts, _, ended = dataCuts(page, &ws)
		}

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

			payload := sha256.Sum256(f.Payload)
			inflated := sha256.Sum256(f.Inflated)
			l := fmt.Sprintf("  fin=%v rsv=%d op=%d masked=%v length=%d/%d payload=%s compressed=%v inflated=%s/%q close=%d/%q data=%s",
				f.Fin, f.RSV, f.Opcode, f.Masked, f.LengthBits, f.Length, hex.EncodeToString(payload[:8]), f.Compressed, hex.EncodeToString(inflated[:8]), f.InflateError, f.CloseCode, f.CloseReason, dataCut(cuts[k], k.head, limit))

			if f.Opcode == capture.WSPong {
				pongs = append(pongs, l)

				continue
			}

			out = append(out, l)
		}

		out = append(out, pongs...)

		add("  END_STREAM on its last DATA: %v", ended)
	}

	return out
}

// trailing is where the frames after tr's last request start, when the
// request's HEADERS ended its stream (len(tr.Frames) otherwise): its
// header block's end, after any CONTINUATION.
func trailing(tr *h2trace.Trace) int {
	const endStream = 0x1

	for fi, f := range slices.Backward(tr.Frames) {
		if f.Type != "HEADERS" {
			continue
		}

		if f.Flags&endStream == 0 {
			break
		}

		end := fi + 1
		for end < len(tr.Frames) && tr.Frames[end].Type == "CONTINUATION" {
			end++
		}

		return end
	}

	return len(tr.Frames)
}

// acknowledges reports a frame acknowledging the server's SETTINGS or
// PING: it goes out when the server's frame arrives.
func acknowledges(f h2trace.Frame) bool {
	const ack = 0x1

	return (f.Type == "SETTINGS" || f.Type == "PING") && f.Flags&ack != 0
}

// dataCut describes the DATA frames carrying a WebSocket frame whose
// header is head bytes long (mask included): whether one ended before
// the header's mask or after the header, where the writer cuts (Firefox
// writes a large frame's header on its own, Safari a control reply's
// header, mask and payload apart), their total, and whether each kept
// within limit. Their other ends are left out: they fall where the
// stream's flow-control credit ran out, which is when the server's
// WINDOW_UPDATE arrived, so a frame can start in a DATA frame as short as
// the credit left, and no DATA frame need reach the client's write size.
func dataCut(cut []uint32, head, limit uint32) string {
	if len(cut) == 0 {
		return "-"
	}

	var ends []uint32

	total := uint32(0)
	for _, c := range cut {
		total += c
		if total+4 == head || total == head {
			ends = append(ends, total)
		}
	}

	if largest := slices.Max(cut); largest > limit {
		return fmt.Sprintf("header cut at %v, total %d, largest %d over the limit %d", ends, total, largest, limit)
	}

	return fmt.Sprintf("header cut at %v, total %d, each within the limit", ends, total)
}

func framePriority(p *h2trace.Priority) string {
	if p == nil {
		return "-"
	}

	return fmt.Sprintf("%d/%v/%d", p.DependsOn, p.Exclusive, p.Weight)
}

// coreValue is a header's value with ports replaced and a WebSocket key,
// random by design, hidden.
func coreValue(name, value string) string {
	if strings.EqualFold(name, "sec-websocket-key") {
		return "(key)"
	}

	return normalisePort(value)
}

// proxyView renders what the capture proxy recorded, one line per
// request: how it was asked for (CONNECT lines, or SOCKS5's greeting and
// request bytes) on which connection and stream, and its answer.
func proxyView(reqs []capture.ProxyRequest) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, fmt.Sprintf("%s conn=%d stream=%d status=%d target=%s lines=%q greeting=%s request=%s",
			r.Kind, r.Conn, r.Stream, r.Status, r.Target, r.Lines, r.Greeting, r.Request))
	}

	return out
}
