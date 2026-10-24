package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// plainPortServer serves a PlainPort scenario on two listeners and returns
// a client that reaches every *.test name on the port its URL names, the
// ports, the CA's pool and the capture channel.
func plainPortServer(t *testing.T, sc *Scenario) (*http.Client, portPair, *x509.CertPool, <-chan *capture.Capture) {
	t.Helper()
	ca, err := LoadOrCreateCA(t.TempDir(), CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	listen := func() net.Listener {
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return ln
	}
	ln, plain := listen(), listen()
	captures := make(chan *capture.Capture, 64)
	ctx, cancel := context.WithCancel(t.Context())
	srv := &Server{
		Scenario:  sc,
		TLSConfig: &tls.Config{},
		IssueCert: func(hosts []string) (tls.Certificate, error) {
			return ca.Issue(hosts, time.Now())
		},
		OnCapture: func(c *capture.Capture) {
			captures <- c
		},
		Logf:          t.Logf,
		PlainListener: plain,
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: pool,
		},
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		},
	}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	port := func(ln net.Listener) string {
		return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	}
	return client, portPair{tls: port(ln), plain: port(plain)}, pool, captures
}

// The cookies-edge2 page answers each step with its cookies on the port
// its scheme names, chains its pages with the watchdog armed before the
// plain port, refuses TLS on the plain port, deletes every cookie in the
// form it was set, and hands over the page, plain requests included, at
// /done.
func TestCookiesEdge2(t *testing.T) {
	client, ports, pool, captures := plainPortServer(t, CookiesEdge2)
	steps := CookiesEdge2Steps()
	if hosts, _, _ := CookiesEdge2.MultiHost(); hosts[0] != steps[0].Host || steps[0].Path != "/" || !steps[0].Nav {
		t.Fatalf("the page starts at %s%s, its first host is %s", steps[0].Host, steps[0].Path, hosts[0])
	}
	seen := map[string]bool{}
	for i, s := range steps {
		if key := s.Host + s.Path; seen[key] {
			t.Errorf("%s: %s%s is another step's too", s.Label, s.Host, s.Path)
		} else {
			seen[key] = true
		}
		resp, body := fetch(t, client, "GET", edge2URL(s, ports), nil)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %s", s.Label, resp.Status)
			continue
		}
		if s.Plain != (resp.TLS == nil) {
			t.Errorf("%s: served over TLS %v, want plain %v", s.Label, resp.TLS != nil, s.Plain)
		}
		if got := resp.Header.Values("Set-Cookie"); !slices.Equal(got, s.Sets) {
			t.Errorf("%s: sets %q, want %q", s.Label, got, s.Sets)
		}
		// The step is not answered on the other port.
		other := s
		other.Plain = !s.Plain
		if resp, _ := fetch(t, client, "GET", edge2URL(other, ports), nil); resp.StatusCode != 404 {
			t.Errorf("%s: on the other port: %s", s.Label, resp.Status)
		}
		if !s.Nav {
			continue
		}
		// The page fetches the steps up to the next navigation, in order,
		// then goes there; the last one asks for /done.
		_, rest, ok := strings.Cut(body, "(async () => {\n")
		if !ok {
			t.Fatalf("%s: no script:\n%s", s.Label, body)
		}
		next := `fetch("/done")`
		for _, f := range steps[i+1:] {
			if f.Nav {
				next = `location.href = "` + edge2URL(f, ports) + `"`
				break
			}
			_, after, ok := strings.Cut(rest, `await get("`+f.Path+`")`)
			if !ok {
				t.Errorf("%s: page does not fetch %s next:\n%s", s.Label, f.Path, body)
				break
			}
			rest = after
		}
		if !strings.Contains(rest, next) {
			t.Errorf("%s: page does not end with %s:\n%s", s.Label, next, body)
		}
		// The watchdog: registered by the first page, armed before the
		// plain port, disarmed on coming back, ended before leaving.
		want := map[string][]string{
			"doc":          {"await register();", "await arm(15000);"},
			"sch-to-https": {"await arm(0);", "await end();"},
		}[s.Label]
		var calls []string
		for line := range strings.SplitSeq(body, "\n") {
			if strings.HasPrefix(line, "await register(") || strings.HasPrefix(line, "await arm(") || strings.HasPrefix(line, "await end(") {
				calls = append(calls, line)
			}
		}
		if !slices.Equal(calls, want) {
			t.Errorf("%s: watchdog calls %q, want %q", s.Label, calls, want)
		}
	}
	if last := steps[len(steps)-1]; last.Label != "n-cleanup" {
		t.Errorf("last step %s, want n-cleanup", last.Label)
	}

	sets := func(label string) []string {
		i := slices.IndexFunc(steps, func(s CookiesEdgeStep) bool {
			return s.Label == label
		})
		return steps[i].Sets
	}
	cleanup := slices.Concat(sets("sch-cleanup"), sets("n-cleanup"))
	for _, d := range []string{
		"e2_sch_strict=; Path=/; Max-Age=0",
		"e2_sch_secure=; Path=/; Secure; Max-Age=0",
		// Nameless: the whole pair, since "=" alone is ignored.
		"=x; Path=/n/1/2/3/4; Max-Age=0",
		"= __Host-x; Path=/n/1/2; Secure; Max-Age=0",
		"__Http-e2_nosec=; Path=/; HttpOnly; Max-Age=0",
		"__Host-Http-e2_nosec=; Path=/; HttpOnly; Max-Age=0",
	} {
		if !slices.Contains(cleanup, d) {
			t.Errorf("cleanup lacks %q: %q", d, cleanup)
		}
	}
	if want := len(edge2SchemeCookies) + len(edge2NamelessCookies) + len(edge2HTTPCookies); len(cleanup) != want {
		t.Errorf("cleanup deletes %d cookies, want %d", len(cleanup), want)
	}
	// One nameless cookie per path, none with a second "=".
	paths := map[string]bool{}
	for _, c := range edge2NamelessCookies {
		pair, attrs, _ := strings.Cut(c, ";")
		if setCookieName(c) != "" || strings.Count(pair, "=") != 1 {
			t.Errorf("%q is not nameless with one \"=\"", c)
		}
		path, _, _ := strings.Cut(strings.TrimPrefix(attrs, " Path="), ";")
		if paths[path] {
			t.Errorf("%q: path %s is another's", c, path)
		}
		paths[path] = true
	}

	// The plain port refuses TLS: the handshake fails at once.
	d := &tls.Dialer{
		Config: &tls.Config{
			ServerName: edge2Host,
			RootCAs:    pool,
		},
	}
	if conn, err := d.DialContext(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", ports.plain)); err == nil {
		_ = conn.Close()
		t.Error("TLS handshake on the plain port succeeded")
	}

	if resp, body := fetch(t, client, "GET", "https://"+net.JoinHostPort(edge2Host, ports.tls)+watchdogPath, nil); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/javascript" || body != watchdogWorker {
		t.Errorf("watchdog script: %s %q", resp.Status, resp.Header.Get("Content-Type"))
	}

	fetch(t, client, "GET", "https://"+net.JoinHostPort(edge2NamelessHost, ports.tls)+"/done", nil)
	var page *capture.Page
	for page == nil {
		if c := receive(t, captures); c.Finished {
			page = c.Page
		}
	}
	plain := make([]string, 0, len(page.H1))
	for _, r := range page.H1 {
		plain = append(plain, r.Host+" "+r.Line)
	}
	for _, s := range steps {
		if want := s.Host + " GET " + s.Path + " HTTP/1.1"; s.Plain && !slices.Contains(plain, want) {
			t.Errorf("page lacks plain request %q: %q", want, plain)
		}
	}
}

// A PlainPort scenario cannot be served without its second listener.
func TestPlainPortNeedsListener(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Scenario:  CookiesEdge2,
		TLSConfig: &tls.Config{},
		IssueCert: func([]string) (tls.Certificate, error) {
			return tls.Certificate{}, nil
		},
	}
	if err := srv.Serve(t.Context(), ln); err == nil || !strings.Contains(err.Error(), "PlainListener") {
		t.Errorf("Serve without PlainListener: %v", err)
	}
}
