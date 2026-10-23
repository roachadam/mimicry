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
	"github.com/roachadam/mimicry/profile"
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
	certFile := flag.String("cert", "", "certificate file (default: one issued by the capture CA)")
	keyFile := flag.String("key", "", "key file for -cert")
	caDir := flag.String("ca-dir", "", "capture CA directory (default: <user config dir>/mimicry/capture-ca)")
	flag.Parse()

	cert, err := loadCert(*certFile, *keyFile, *caDir)
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
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	log.Printf("capturing on https://localhost:%s/ (or %s with the browser mapping it to 127.0.0.1), writing to %s", port, server.CaptureHost, *out)
	return srv.Serve(ctx, ln)
}

func loadCert(certFile, keyFile, caDir string) (tls.Certificate, error) {
	if certFile != "" || keyFile != "" {
		return tls.LoadX509KeyPair(certFile, keyFile)
	}
	if caDir == "" {
		dir, err := server.DefaultCADir()
		if err != nil {
			return tls.Certificate{}, err
		}
		caDir = dir
	}
	ca, err := server.LoadOrCreateCA(caDir, server.CADomains, time.Now())
	if err != nil {
		return tls.Certificate{}, err
	}
	return ca.Issue(server.CAHosts, time.Now())
}

func save(dir string, c *capture.Capture) {
	if !c.Complete() {
		log.Printf("ClientHello only (no request; ALPN %q, %d extensions, server_name %t)",
			c.Protocol, len(c.Profile.TLS.Extensions), c.HasExtension(profile.ExtServerName))
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
