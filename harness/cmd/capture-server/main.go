// Command capture-server records what TLS clients send: the ClientHello, the
// HTTP/2 connection preface frames and the first request's headers. Point a
// browser at it and each new browser identity is written to the output
// directory as a profile-format capture.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "capture-server:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8443", "address to listen on")
	out := flag.String("out", "captures", "directory to write captures to")
	certFile := flag.String("cert", "", "certificate file (default: a fresh self-signed certificate)")
	keyFile := flag.String("key", "", "key file for -cert")
	flag.Parse()

	cert, err := loadCert(*certFile, *keyFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *addr)
	if err != nil {
		return err
	}
	srv := &server.Server{
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
		},
		OnCapture: func(c *capture.Capture) {
			save(*out, c)
		},
		Logf: log.Printf,
	}
	log.Printf("capturing on https://%s/ and writing to %s", ln.Addr(), *out)
	return srv.Serve(ctx, ln)
}

func loadCert(certFile, keyFile string) (tls.Certificate, error) {
	if certFile != "" || keyFile != "" {
		return tls.LoadX509KeyPair(certFile, keyFile)
	}
	return server.SelfSigned([]string{
		"localhost",
		"127.0.0.1",
		"::1",
	}, time.Now())
}

func save(dir string, c *capture.Capture) {
	if !c.Complete() {
		log.Printf("ClientHello only (no request; ALPN %q, %d extensions)", c.Protocol, len(c.Profile.TLS.Extensions))
		return
	}
	c.Identify()
	path, err := capture.Write(dir, c)
	switch {
	case errors.Is(err, capture.ErrExists):
		log.Printf("%s already captured; skipping this connection", c.Profile.Name)
	case err != nil:
		log.Printf("writing %s: %v", c.Profile.Name, err)
	default:
		log.Printf("captured %s (%s, %d extensions) to %s", c.Profile.Name, c.Protocol, len(c.Profile.TLS.Extensions), path)
	}
}
