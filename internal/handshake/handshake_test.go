package handshake

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profiles"
)

func TestHandshakeFailsOnUntrustedCertificate(t *testing.T) {
	cert, _ := testutil.Certificate(t, "localhost")

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = ln.Close()
	})

	go func() {
		server, err := ln.Accept()
		if err != nil {
			return
		}

		_ = server.SetDeadline(time.Now().Add(10 * time.Second))
		_ = tls.Server(server, &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
		}).HandshakeContext(t.Context())
		_ = server.Close()
	}()

	client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	plan := fingerprint.Compile(profiles.Chrome())

	_, _, err = Client(t.Context(), client, plan.Hello(), Config{
		ServerName: "localhost",
	})
	if err == nil {
		t.Fatal("handshake succeeded with a certificate nobody trusts")
	}
}
