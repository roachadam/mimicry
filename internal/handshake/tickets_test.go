package handshake

import (
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profiles"
)

// ticketFrom does a full handshake for key against a TLS 1.3 server with a
// certificate valid for validity, and reads until the server's ticket is
// in tickets. The server's tickets live 7 days (crypto/tls).
func ticketFrom(t *testing.T, tickets *Tickets, key string, validity time.Duration) {
	t.Helper()
	cert, roots := testutil.CertificateValidFor(t, validity, "localhost")
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
	conn, _, err := Client(t.Context(), client, fingerprint.Compile(profiles.Chrome()).Hello(), Config{
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
	tickets.mu.Lock()
	_, ok := tickets.m[key]
	tickets.mu.Unlock()
	if !ok {
		t.Fatal("the server's ticket was not kept")
	}
}

func newTickets() (*Tickets, *clockwork.FakeClock) {
	// uTLS times the ticket by the real clock: start there.
	clock := clockwork.NewFakeClockAt(time.Now())
	return NewTickets("", clock), clock
}

func TestTicketPastItsLifetimeIsNotOffered(t *testing.T) {
	tickets, clock := newTickets()
	ticketFrom(t, tickets, "k", 30*24*time.Hour)
	clock.Advance(8 * 24 * time.Hour)
	if tk := tickets.Take("k"); tk != nil {
		t.Fatal("a ticket past its 7-day lifetime was offered: the hello would take the resumed shape uTLS then empties")
	}
}
