package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/dnsstub"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// resolving is the capture server of the sample under way when its
// scenario has the browser look the hosts up (Scenario.ResolvesHosts): it
// records the lookups the DNS stub and the DoH endpoint answer.
var resolving atomic.Pointer[server.Server]

// stubConfig is what the capture tool's DNS answers, over UDP and DoH:
// addr for every name under .test, and the ech page's HTTPS records.
func stubConfig(addr netip.Addr) dnsstub.Config {
	return dnsstub.Config{
		Answer: addr,
		HTTPS:  server.ECHRecord,
		OnQuery: func(name string, typ dnsmessage.Type, via string) {
			if s := resolving.Load(); s != nil {
				s.DNSQuery(name, typ, via)
			}
		},
	}
}

// resolveRefusal says why b cannot look a ResolvesHosts scenario's hosts
// up through the capture tool, "" when it can: the Chromium browsers, and
// Firefox off macOS, ask its DoH endpoint (Browser.UsesDoH); Firefox and
// Safari on macOS the system's resolver, which sends *.test to its DNS
// stub once ResolverFile is installed.
func resolveRefusal(b *browser.Browser) string {
	switch {
	case b.UsesDoH():
		return ""
	case runtime.GOOS != "darwin":
		return b.Name + " looks the hosts up through the system's resolver, which sends *.test to the capture tool's DNS stub on macOS only (" + dnsstub.ResolverFile + ")"
	case !dnsstub.ResolverInstalled():
		return dnsstub.ResolverFile + " does not send *.test lookups to the capture tool, so " + b.Name + " cannot look the hosts up. Create it once:\n  " + dnsstub.InstallCommand
	}

	return ""
}

// serveDoH serves the DoH endpoint (dnsstub.DoH) on addr until ctx ends,
// with a certificate from opts.ca for browser.DoHHost, and returns its URL
// template (browser.Target.DoH).
func serveDoH(ctx context.Context, opts options) (string, error) {
	addr, err := netip.ParseAddr(opts.addr)
	if err != nil {
		return "", err
	}

	cert, err := opts.ca.Issue([]string{browser.DoHHost}, time.Now())
	if err != nil {
		return "", err
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(opts.addr, "0"))
	if err != nil {
		return "", err
	}

	port, err := tcpPort(ln)
	if err != nil {
		_ = ln.Close()
		return "", err
	}

	srv := &http.Server{
		Handler: dnsstub.DoH(stubConfig(addr)),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
			MinVersion: tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
		// Chrome races connections to its DoH server and drops the
		// losers: their handshake errors say nothing.
		ErrorLog: log.New(io.Discard, "", 0),
	}

	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("DoH on %s: %v", ln.Addr(), err)
		}
	}()

	context.AfterFunc(ctx, func() {
		_ = srv.Close()
	})

	return "https://" + net.JoinHostPort(browser.DoHHost, strconv.Itoa(port)) + "/dns-query{?dns}", nil
}
