package native

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// A store written by one engine and read into another's resumes the first
// connection of the second, as one browser that stayed open would.
func TestSavedTicketsResume(t *testing.T) {
	var (
		mu      sync.Mutex
		resumed []bool
	)

	record := func(didResume bool) {
		mu.Lock()
		defer mu.Unlock()

		resumed = append(resumed, didResume)
	}

	cert, roots := testutil.Certificate(t, "localhost")

	// One configuration for every connection, so that its ticket keys
	// decrypt the tickets it issued.
	srvCfg := &tls.Config{
		Certificates: []tls.Certificate{
			cert,
		},
		NextProtos: []string{
			"h2",
		},
	}

	addr := tcpServer(t, func(ctx context.Context, conn net.Conn) {
		tc := tls.Server(conn, srvCfg)
		if tc.HandshakeContext(ctx) != nil {
			return
		}

		record(tc.ConnectionState().DidResume)

		serveH2On(ctx, tc, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
	})

	request := func(store *handshake.Store) {
		t.Helper()

		e := newTestEngine(t, options{
			dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
			tickets: store,
			roots:   roots,
		})

		resp, err := get(t, e, "https://localhost/")
		if err != nil {
			t.Fatal(err)
		}

		readAll(t, resp)
		e.CloseIdleConnections()
	}

	first := handshake.NewStore()
	request(first)

	// The server's ticket follows its first response: wait for it.
	deadline := time.Now().Add(5 * time.Second)
	for !first.Tickets("", clockwork.NewRealClock()).Has("https://localhost:443") {
		if time.Now().After(deadline) {
			t.Fatal("no ticket kept from the first connection")
		}

		time.Sleep(time.Millisecond)
	}

	var saved bytes.Buffer
	if err := WriteTickets(&saved, first, time.Now()); err != nil {
		t.Fatal(err)
	}

	second := handshake.NewStore()
	if err := ReadTickets(bytes.NewReader(saved.Bytes()), second, time.Now()); err != nil {
		t.Fatal(err)
	}

	request(second)

	mu.Lock()
	defer mu.Unlock()

	if want := []bool{false, true}; len(resumed) != 2 || resumed[0] != want[0] || resumed[1] != want[1] {
		t.Errorf("connections resumed %v, want %v: the saved ticket resumes the new engine's first connection", resumed, want)
	}
}

// A saved ticket keeps its origin, partition, proxy and protocol apart,
// and names its proxy by a hash: no proxy URL or credentials in the file,
// and it resumes only through the same proxy URL. A session with an https
// proxy itself is saved by the proxy's hash alone, its address left out.
func TestSavedTicketKeys(t *testing.T) {
	const (
		secret = "s3cret-pass"
		proxy  = "http://user:" + secret + "@proxy.example:8080"
	)

	key := "https://localhost:443 frame https://site.example via " + proxy + " h3"

	store := handshake.NewStore()
	clock := clockwork.NewRealClock()
	tickets := store.Tickets("", clock)
	ticketInto(t, tickets, ticketKey(key), tls.VersionTLS13)
	ticketInto(t, tickets, ticketKey(proxyKey("https://user:"+secret+"@proxy.example:8443")), tls.VersionTLS13)

	var saved bytes.Buffer
	if err := WriteTickets(&saved, store, time.Now()); err != nil {
		t.Fatal(err)
	}

	// No base64 has ":" or "@": these cannot appear by chance.
	for _, leak := range []string{secret, "proxy.example", "user:", "@"} {
		if strings.Contains(saved.String(), leak) {
			t.Errorf("saved tickets contain %q:\n%s", leak, saved.String())
		}
	}

	loaded := handshake.NewStore()
	if err := ReadTickets(bytes.NewReader(saved.Bytes()), loaded, time.Now()); err != nil {
		t.Fatal(err)
	}

	view := loaded.Tickets("", clock)
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{key, true},
		{"https://localhost:443 frame https://site.example via http://user:other@proxy.example:8080 h3", false},
		{"https://localhost:443 frame https://site.example via " + proxy, false},
		{"https://localhost:443 via " + proxy + " h3", false},
		{"https://localhost:443 frame https://site.example h3", false},
		{proxyKey("https://user:" + secret + "@proxy.example:8443"), true},
		{proxyKey("https://user:other@proxy.example:8443"), false},
		{"https://proxy.example:8443 via https://user:" + secret + "@proxy.example:8443", false},
	} {
		if got := view.Has(ticketKey(tc.key)); got != tc.want {
			t.Errorf("loaded ticket for %q: %v, want %v", tc.key, got, tc.want)
		}
	}
}

// ticketInto does a full handshake in version whose ticket tickets keeps
// under key.
func ticketInto(t *testing.T, tickets *handshake.Tickets, key string, version uint16) {
	t.Helper()

	cert, roots := testutil.Certificate(t, "localhost")
	client, server := net.Pipe()

	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	go func() {
		srv := tls.Server(server, &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
			MinVersion: version,
			MaxVersion: version,
		})
		if srv.HandshakeContext(t.Context()) != nil {
			return
		}

		// Application data follows the session tickets.
		_, _ = srv.Write([]byte("x"))
	}()

	conn, _, err := handshake.Client(t.Context(), client, fingerprint.Compile(profiles.ChromeMacOS().Spec()).Hello(), handshake.Config{
		ServerName: "localhost",
		RootCAs:    roots,
		Tickets:    tickets,
		TicketKey:  key,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}

	if !tickets.Has(key) {
		t.Fatal("the server's ticket was not kept")
	}
}

// Through a proxy with credentials, a saved ticket names the proxy by a
// hash only, and resumes in another engine through the same proxy URL,
// not through the same proxy with another password.
func TestSavedTicketsThroughProxy(t *testing.T) {
	cert, roots := testutil.Certificate(t, "localhost")
	serverCert.Store(&cert)

	proxies := newIdentityProxies(t, false)
	proxyURL := func(password string) *url.URL {
		return &url.URL{
			Scheme: "http",
			User:   url.UserPassword("user", password),
			Host:   proxyAddr(0),
		}
	}

	through := func(store *handshake.Store, password string) []string {
		t.Helper()

		e := newTestEngine(t, options{
			dial: proxies.dial,
			proxy: func(*http.Request) (*url.URL, error) {
				return proxyURL(password), nil
			},
			tickets: store,
			roots:   roots,
		})

		resp, err := get(t, e, "https://localhost/")
		if err != nil {
			t.Fatal(err)
		}

		readAll(t, resp)
		e.CloseIdleConnections()

		return proxies.take(proxyAddr(0))
	}

	first := handshake.NewStore()
	through(first, "s3cret-pass")

	var saved bytes.Buffer

	deadline := time.Now().Add(5 * time.Second)
	for {
		saved.Reset()

		if err := WriteTickets(&saved, first, time.Now()); err != nil {
			t.Fatal(err)
		}

		if strings.Contains(saved.String(), `"proxy"`) {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("no ticket through the proxy saved:\n%s", saved.String())
		}

		time.Sleep(time.Millisecond)
	}

	for _, leak := range []string{"s3cret-pass", "user:", proxyAddr(0)} {
		if strings.Contains(saved.String(), leak) {
			t.Errorf("saved tickets contain %q:\n%s", leak, saved.String())
		}
	}

	for _, tc := range []struct {
		password string
		resumed  bool
	}{
		{"other-pass", false},
		{"s3cret-pass", true},
	} {
		loaded := handshake.NewStore()
		if err := ReadTickets(bytes.NewReader(saved.Bytes()), loaded, time.Now()); err != nil {
			t.Fatal(err)
		}

		if got := slices.Contains(through(loaded, tc.password), "TLS resumed"); got != tc.resumed {
			t.Errorf("through the proxy with password %q: resumed %v, want %v", tc.password, got, tc.resumed)
		}
	}
}

// TLS 1.2 tickets save and load like TLS 1.3 ones, for views that keep
// them, and never reach a view that does not (QUIC's); tickets expired by
// the time of saving or loading are left out.
func TestSavedTicketVersionsAndExpiry(t *testing.T) {
	const key = "https://localhost:443"

	clock := clockwork.NewRealClock()

	for _, tc := range []struct {
		name    string
		version uint16
		tls12   string
	}{
		{"TLS 1.3", tls.VersionTLS13, ""},
		{"TLS 1.2, reuse", tls.VersionTLS12, profile.TLS12Reuse},
		{"TLS 1.2, single use", tls.VersionTLS12, profile.TLS12SingleUse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := handshake.NewStore()
			ticketInto(t, store.Tickets(tc.tls12, clock), key, tc.version)

			var saved bytes.Buffer
			if err := WriteTickets(&saved, store, time.Now()); err != nil {
				t.Fatal(err)
			}

			loaded := handshake.NewStore()
			if err := ReadTickets(bytes.NewReader(saved.Bytes()), loaded, time.Now()); err != nil {
				t.Fatal(err)
			}

			if !loaded.Tickets(tc.tls12, clock).Has(key) {
				t.Error("the loaded ticket is not offered")
			}

			if tc.version == tls.VersionTLS12 && loaded.Tickets("", clock).Take(key) != nil {
				t.Error("a view keeping no TLS 1.2 tickets took one")
			}

			// The server certificate lives a day (testutil.Certificate).
			later := time.Now().Add(48 * time.Hour)

			var expired bytes.Buffer
			if err := WriteTickets(&expired, store, later); err != nil {
				t.Fatal(err)
			}

			if got := strings.TrimSpace(expired.String()); got != "[]" {
				t.Errorf("saved after expiry: %s, want []", got)
			}

			late := handshake.NewStore()
			if err := ReadTickets(bytes.NewReader(saved.Bytes()), late, later); err != nil {
				t.Fatal(err)
			}

			if late.Tickets(tc.tls12, clockwork.NewFakeClockAt(later)).Has(key) {
				t.Error("a ticket loaded after its expiry is offered")
			}
		})
	}
}

// A malformed entry fails ReadTickets before anything is added, valid
// entries before it included.
func TestReadTicketsRefusesMalformed(t *testing.T) {
	store := handshake.NewStore()
	clock := clockwork.NewRealClock()
	ticketInto(t, store.Tickets("", clock), "https://localhost:443", tls.VersionTLS13)

	var good bytes.Buffer
	if err := WriteTickets(&good, store, time.Now()); err != nil {
		t.Fatal(err)
	}

	var entries []map[string]any
	if err := json.Unmarshal(good.Bytes(), &entries); err != nil || len(entries) != 1 {
		t.Fatalf("saved %s: %v", good.String(), err)
	}

	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"plain http origin", "origin", "http://localhost:80"},
		{"origin with a path", "origin", "https://localhost:443/x"},
		{"proxy URL, not a hash", "proxy", "http://user:pass@proxy.example:8080"},
		{"short proxy hash", "proxy", "sha256:abcd"},
		{"a session with a proxy that names an origin", "toProxy", true},
		{"no ticket", "ticket", ""},
		{"state that does not parse", "state", "AAAA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := maps.Clone(entries[0])

			bad[tc.field] = tc.value

			b, err := json.Marshal([]map[string]any{entries[0], bad})
			if err != nil {
				t.Fatal(err)
			}

			loaded := handshake.NewStore()
			if err := ReadTickets(bytes.NewReader(b), loaded, time.Now()); err == nil {
				t.Fatal("no error")
			}

			if loaded.Tickets("", clock).Has("https://localhost:443") {
				t.Error("the valid entry before the malformed one was added")
			}
		})
	}
}

// A store belongs to one browser: an engine of another browser, or a file
// of another browser's tickets, is refused; another version of the same
// browser is not.
func TestTicketStoreIsOneBrowsers(t *testing.T) {
	newEngineWith := func(p *profile.Spec, store *handshake.Store) error {
		e, err := newEngine(Config{
			Profile: p,
			Tickets: store,
		}, clockwork.NewRealClock(), nil)
		if err == nil {
			e.CloseIdleConnections()
		}

		return err
	}

	chrome := profiles.Chrome156MacOS().Spec()

	store := handshake.NewStore()
	if err := newEngineWith(chrome, store); err != nil {
		t.Fatal(err)
	}

	if err := newEngineWith(profiles.Chrome155MacOS().Spec(), store); err != nil {
		t.Errorf("another Chrome version: %v", err)
	}

	if err := newEngineWith(profiles.FirefoxMacOS().Spec(), store); err == nil {
		t.Error("a Firefox engine shared a Chrome store")
	}

	ticketInto(t, store.Tickets("", clockwork.NewRealClock()), "https://localhost:443", tls.VersionTLS13)

	var saved bytes.Buffer
	if err := WriteTickets(&saved, store, time.Now()); err != nil {
		t.Fatal(err)
	}

	firefox := handshake.NewStore()
	if err := newEngineWith(profiles.FirefoxMacOS().Spec(), firefox); err != nil {
		t.Fatal(err)
	}

	if err := ReadTickets(bytes.NewReader(saved.Bytes()), firefox, time.Now()); err == nil {
		t.Error("Chrome's saved tickets were read into a Firefox store")
	}

	if firefox.Tickets("", clockwork.NewRealClock()).Has("https://localhost:443") {
		t.Error("a refused file added a ticket")
	}

	// An unbound store takes the file's browser.
	fresh := handshake.NewStore()
	if err := ReadTickets(bytes.NewReader(saved.Bytes()), fresh, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := newEngineWith(profiles.FirefoxMacOS().Spec(), fresh); err == nil {
		t.Error("a Firefox engine took a store loaded with Chrome's tickets")
	}
}
