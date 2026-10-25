package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	utls "github.com/roachadam/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

type testServer struct {
	addr     string
	pool     *x509.CertPool
	captures chan *capture.Capture
	infos    chan *tls.ClientHelloInfo
}

// startServer runs a capture server on a loopback port. Go's own parse of each
// ClientHello is delivered on infos, as an independent check of ours.
func startServer(t *testing.T) *testServer {
	t.Helper()
	hosts := []string{
		"localhost",
	}
	ca, err := LoadOrCreateCA(t.TempDir(), hosts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Issue(hosts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{
		addr:     ln.Addr().String(),
		pool:     x509.NewCertPool(),
		captures: make(chan *capture.Capture, 4),
		infos:    make(chan *tls.ClientHelloInfo, 4),
	}
	ts.pool.AddCert(ca.Cert)
	srv := &Server{
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
			GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
				ts.infos <- info
				return nil, nil //nolint:nilnil // crypto/tls: a nil config keeps the current one
			},
		},
		OnCapture: func(c *capture.Capture) {
			ts.captures <- c
		},
		Logf:        t.Logf,
		ConnTimeout: 10 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return ts
}

func (ts *testServer) dial(t *testing.T) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ts.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return conn
}

func (ts *testServer) dialTLS(t *testing.T, protos ...string) *tls.Conn {
	t.Helper()
	conn := tls.Client(ts.dial(t), &tls.Config{
		ServerName: "localhost",
		RootCAs:    ts.pool,
		NextProtos: protos,
	})
	if err := conn.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return conn
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the capture server")
		var zero T
		return zero
	}
}

func codes[T ~uint16](values []T) []profile.Code {
	out := make([]profile.Code, len(values))
	for i, v := range values {
		out[i] = code(uint16(v))
	}
	return out
}

func extension(c *capture.Capture, typ profile.Code) *profile.Extension {
	for i := range c.Profile.TLS.Extensions {
		if c.Profile.TLS.Extensions[i].Type == typ {
			return &c.Profile.TLS.Extensions[i]
		}
	}
	return &profile.Extension{}
}

// Our ClientHello parse must agree with crypto/tls's parse of the same bytes on
// every field both expose, and the result must be a valid profile TLS section.
func TestClientHelloAgreesWithStdlib(t *testing.T) {
	type client struct {
		name      string
		handshake func(t *testing.T, conn net.Conn) []byte // returns the ClientHello it sent, if known
	}
	ts := startServer(t)
	clients := make([]client, 0, 5)
	clients = append(clients, client{
		name: "crypto/tls",
		handshake: func(t *testing.T, conn net.Conn) []byte {
			c := tls.Client(conn, &tls.Config{
				ServerName: "localhost",
				RootCAs:    ts.pool,
				NextProtos: []string{
					"h2",
					"http/1.1",
				},
			})
			if err := c.HandshakeContext(t.Context()); err != nil {
				t.Fatalf("handshake: %v", err)
			}
			return nil
		},
	})
	for _, id := range []utls.ClientHelloID{
		utls.HelloChrome_155,
		utls.HelloFirefox_148,
		utls.HelloSafari_26_3,
		utls.HelloIOS_14,
	} {
		clients = append(clients, client{
			name: id.Str(),
			handshake: func(t *testing.T, conn net.Conn) []byte {
				u := utls.UClient(conn, &utls.Config{
					ServerName: "localhost",
					RootCAs:    ts.pool,
				}, id)
				if err := u.HandshakeContext(t.Context()); err != nil {
					t.Fatalf("handshake: %v", err)
				}
				return u.HandshakeState.Hello.Raw
			},
		})
	}
	for _, cl := range clients {
		t.Run(cl.name, func(t *testing.T) {
			conn := ts.dial(t)
			sent := cl.handshake(t, conn)
			_ = conn.Close()
			info := receive(t, ts.infos)
			c := receive(t, ts.captures)

			if sent != nil && !bytes.Equal(c.ClientHello, sent) {
				t.Error("recorded ClientHello differs from the bytes the client sent")
			}
			// Go and uTLS send the first ClientHello in TLS 1.0 records.
			if version, msg, err := ParseRecords(c.Records); err != nil || version != 0x0301 || !bytes.Equal(msg, c.ClientHello) {
				t.Errorf("recorded records: version %#04x, carry the ClientHello %v, error %v", version, bytes.Equal(msg, c.ClientHello), err)
			}
			if c.RecordVersion() != 0x0301 {
				t.Errorf("record version %#04x, want 0x0301", c.RecordVersion())
			}
			exts := make([]profile.Code, 0, len(c.Profile.TLS.Extensions))
			for _, e := range c.Profile.TLS.Extensions {
				exts = append(exts, e.Type)
			}
			checks := []struct {
				field     string
				got, want any
			}{
				{
					field: "extension order",
					got:   exts,
					want:  codes(info.Extensions),
				},
				{
					field: "cipher suites",
					got:   c.Profile.TLS.CipherSuites,
					want:  codes(info.CipherSuites),
				},
				{
					field: "supported_groups",
					got:   extension(c, profile.ExtSupportedGroups).Groups,
					want:  codes(info.SupportedCurves),
				},
				{
					field: "signature_algorithms",
					got:   extension(c, profile.ExtSignatureAlgorithms).SignatureAlgorithms,
					want:  codes(info.SignatureSchemes),
				},
				{
					field: "supported_versions",
					got:   extension(c, profile.ExtSupportedVersions).Versions,
					want:  codes(info.SupportedVersions),
				},
				{
					field: "alpn",
					got:   extension(c, profile.ExtALPN).ALPN,
					want:  info.SupportedProtos,
				},
				{
					field: "ec_point_formats",
					got:   []uint8(extension(c, profile.ExtECPointFormats).PointFormats),
					want:  info.SupportedPoints,
				},
			}
			for _, ch := range checks {
				if !equalOrBothEmpty(ch.got, ch.want) {
					t.Errorf("%s:\n got %v\nwant %v", ch.field, ch.got, ch.want)
				}
			}

			// Identity, headers and extension order come from elsewhere (the
			// user agent, a request, several samples); fill them in so only
			// the parsed TLS section is judged.
			p := c.Profile
			p.Name, p.Browser, p.Version, p.Platform = "x", "x", "x", "x"
			p.TLS.ExtensionOrder = profile.ExtensionOrderFixed
			p.Headers = []profile.Header{
				{
					Name: "accept",
				},
			}
			var verr *profile.ValidationError
			if errors.As(p.Validate(), &verr) {
				for _, problem := range verr.Problems {
					if strings.HasPrefix(problem.Path, "tls") {
						t.Errorf("captured TLS section is invalid: %s", problem)
					}
				}
			}
		})
	}
}

func equalOrBothEmpty(got, want any) bool {
	g, w := reflect.ValueOf(got), reflect.ValueOf(want)
	if g.Len() == 0 && w.Len() == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

// The server must record the HTTP/2 preface frames and first request exactly
// as sent, including a header block split across CONTINUATION frames, and
// answer requests so the client completes.
func TestHTTP2Recording(t *testing.T) {
	ts := startServer(t)
	conn := ts.dialTLS(t, "h2")
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	fr := http2.NewFramer(conn, conn)
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)

	settings := []http2.Setting{
		{
			ID:  http2.SettingHeaderTableSize,
			Val: 65536,
		},
		{
			ID:  http2.SettingEnablePush,
			Val: 0,
		},
		{
			ID:  http2.SettingInitialWindowSize,
			Val: 6291456,
		},
		{
			ID:  http2.SettingMaxHeaderListSize,
			Val: 262144,
		},
	}
	fields := []hpack.HeaderField{
		{
			Name:  ":method",
			Value: "GET",
		},
		{
			Name:  ":authority",
			Value: "localhost",
		},
		{
			Name:  ":scheme",
			Value: "https",
		},
		{
			Name:  ":path",
			Value: "/",
		},
		{
			Name:  "user-agent",
			Value: "test-agent/1",
		},
		{
			Name:  "accept",
			Value: "text/html",
		},
		{
			Name:  "accept-language",
			Value: "en-US",
		},
	}
	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	for _, f := range fields {
		if err := enc.WriteField(f); err != nil {
			t.Fatal(err)
		}
	}
	headerPriority := http2.PriorityParam{
		StreamDep: 0,
		Exclusive: true,
		Weight:    255,
	}
	split := block.Len() / 2
	writes := []func() error{
		func() error {
			return fr.WriteSettings(settings...)
		},
		func() error {
			return fr.WriteWindowUpdate(0, 15663105)
		},
		func() error {
			return fr.WritePriority(3, http2.PriorityParam{
				StreamDep: 0,
				Weight:    200,
			})
		},
		func() error {
			return fr.WritePriority(5, http2.PriorityParam{
				StreamDep: 3,
				Exclusive: true,
				Weight:    100,
			})
		},
		func() error {
			return fr.WriteHeaders(http2.HeadersFrameParam{
				StreamID:      1,
				BlockFragment: block.Bytes()[:split],
				EndStream:     true,
				Priority:      headerPriority,
			})
		},
		func() error {
			return fr.WriteContinuation(1, true, block.Bytes()[split:])
		},
	}
	for _, w := range writes {
		if err := w(); err != nil {
			t.Fatal(err)
		}
	}

	if status := readResponse(t, fr, 1); status != "200" {
		t.Errorf("first request status %s, want 200", status)
	}
	if err := fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      3,
		BlockFragment: block.Bytes(),
		EndStream:     true,
		EndHeaders:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if status := readResponse(t, fr, 3); status != "200" {
		t.Errorf("second request status %s, want 200: the server answers every request", status)
	}
	// A client that does not ask for /done completes its capture by closing.
	_ = conn.Close()
	c := receive(t, ts.captures)
	want := &profile.HTTP2{
		Settings: []profile.Setting{
			{
				ID:    1,
				Value: 65536,
			},
			{
				ID:    2,
				Value: 0,
			},
			{
				ID:    4,
				Value: 6291456,
			},
			{
				ID:    6,
				Value: 262144,
			},
		},
		WindowUpdate: 15663105,
		Priorities: []profile.Priority{
			{
				StreamID: 3,
				PriorityParam: profile.PriorityParam{
					DependsOn: 0,
					Weight:    200,
				},
			},
			{
				StreamID: 5,
				PriorityParam: profile.PriorityParam{
					DependsOn: 3,
					Exclusive: true,
					Weight:    100,
				},
			},
		},
		HeadersPriority: &profile.PriorityParam{
			DependsOn: 0,
			Exclusive: true,
			Weight:    255,
		},
		PseudoHeaderOrder: []string{
			":method",
			":authority",
			":scheme",
			":path",
		},
	}
	if !reflect.DeepEqual(c.Profile.HTTP2, want) {
		t.Errorf("HTTP/2:\n got %+v\nwant %+v", c.Profile.HTTP2, want)
	}
	wantHeaders := []profile.Header{
		{
			Name:  "user-agent",
			Value: "test-agent/1",
		},
		{
			Name:  "accept",
			Value: "text/html",
		},
		{
			Name:  "accept-language",
			Value: "en-US",
		},
	}
	if !slices.Equal(c.Profile.Headers, wantHeaders) {
		t.Errorf("headers:\n got %+v\nwant %+v", c.Profile.Headers, wantHeaders)
	}
	if c.Protocol != "h2" {
		t.Errorf("protocol %q, want h2", c.Protocol)
	}
}

// readResponse reads frames until the response on stream ends and returns its
// :status.
func readResponse(t *testing.T, fr *http2.Framer, stream uint32) string {
	t.Helper()
	status := ""
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		if f.Header().StreamID != stream {
			continue
		}
		switch f := f.(type) {
		case *http2.MetaHeadersFrame:
			status = f.PseudoValue("status")
			if f.StreamEnded() {
				return status
			}
		case *http2.DataFrame:
			if f.StreamEnded() {
				return status
			}
		}
	}
}

// HTTP/1.1 requests are recorded verbatim, casing and order intact, across
// keep-alive requests and connections; /done, on whichever connection,
// completes the capture with the whole page load and the first request's
// headers as the profile's.
func TestHTTP1Recording(t *testing.T) {
	ts := startServer(t)
	first := ts.dialTLS(t, "http/1.1")
	firstReader := bufio.NewReader(first)
	second := ts.dialTLS(t, "http/1.1")
	secondReader := bufio.NewReader(second)
	send := func(conn io.Writer, r *bufio.Reader, request string) {
		t.Helper()
		if _, err := io.WriteString(conn, request); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			line, _, _ := strings.Cut(request, "\r")
			t.Fatalf("%q: status %s", line, resp.Status)
		}
	}
	send(first, firstReader, "GET / HTTP/1.1\r\nHost: localhost\r\nUser-Agent: test-agent/1\r\nsec-ch-ua: x\r\n\r\n")
	// A chunked upload, then another request on the same connection.
	send(first, firstReader, "POST /upload HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n")
	send(first, firstReader, "GET /again HTTP/1.1\r\nHost: localhost\r\n\r\n")
	send(second, secondReader, "GET /done HTTP/1.1\r\nHost: localhost\r\n\r\n")

	c := receive(t, ts.captures)
	if !c.Finished {
		t.Fatal("capture not finished at /done")
	}
	got := make([]string, 0, len(c.H1))
	for _, r := range c.H1 {
		got = append(got, fmt.Sprintf("%d %s", r.Conn, r.Line))
	}
	want := []string{"1 GET / HTTP/1.1", "1 POST /upload HTTP/1.1", "1 GET /again HTTP/1.1", "2 GET /done HTTP/1.1"}
	if !slices.Equal(got, want) {
		t.Errorf("requests\n got %v\nwant %v", got, want)
	}
	if !slices.Equal(c.H1[0].Headers, []string{"Host: localhost", "User-Agent: test-agent/1", "sec-ch-ua: x"}) {
		t.Errorf("header lines not verbatim: %q", c.H1[0].Headers)
	}
	wantProfile := []profile.Header{
		{
			Name: "host",
		},
		{
			Name:  "user-agent",
			Value: "test-agent/1",
		},
		{
			Name:  "sec-ch-ua",
			Value: "x",
		},
	}
	if !slices.Equal(c.Profile.Headers, wantProfile) {
		t.Errorf("profile headers:\n got %+v\nwant %+v", c.Profile.Headers, wantProfile)
	}
	if c.Profile.HTTP2 != nil {
		t.Error("HTTP/1.1 capture has an HTTP/2 section")
	}
}
