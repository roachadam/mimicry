package native

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/testutil"
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
// and it resumes only through the same proxy URL.
func TestSavedTicketKeys(t *testing.T) {
	const (
		secret = "s3cret-pass"
		proxy  = "http://user:" + secret + "@proxy.example:8080"
	)

	key := "https://localhost:443 frame https://site.example via " + proxy + " h3"

	store := handshake.NewStore()
	clock := clockwork.NewRealClock()
	tickets := store.Tickets("", clock)
	ticketInto(t, tickets, ticketKey(key))

	var saved bytes.Buffer
	if err := WriteTickets(&saved, store, time.Now()); err != nil {
		t.Fatal(err)
	}

	for _, leak := range []string{secret, "proxy.example", "user"} {
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
	} {
		if got := view.Has(ticketKey(tc.key)); got != tc.want {
			t.Errorf("loaded ticket for %q: %v, want %v", tc.key, got, tc.want)
		}
	}
}

// ticketInto does a full TLS 1.3 handshake whose ticket tickets keeps
// under key.
func ticketInto(t *testing.T, tickets *handshake.Tickets, key string) {
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
			MinVersion: tls.VersionTLS13,
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
