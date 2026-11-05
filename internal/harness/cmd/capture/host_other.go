//go:build !windows && !linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/dnsstub"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// prepareHost readies the machine for a run and returns what undoes it:
// here nothing, since the capture CA is trusted once by hand.
func prepareHost(context.Context, *server.CA) (func(), error) {
	return func() {}, nil
}

// cleanUp is -cleanup, which only Windows runs need.
func cleanUp(string) error {
	return errors.New("-cleanup is for Windows, where a run trusts the capture CA and registers scheduled tasks; nothing here needs it")
}

// startDNSStub answers *.test lookups with addr until ctx ends. Safari here
// resolves through the system: the loopback stub answers its *.test lookups
// (Chrome and Firefox map the names themselves). With clients (-listen), a
// second stub on port 53 is a device's DNS server, forwarding other names
// to this Mac's resolver for clients only; an error says it could not
// start. It listens on every IPv4 address: macOS lets a user bind a port
// below 1024 only there, not on addr alone.
func startDNSStub(ctx context.Context, addr netip.Addr, clients netip.Prefix) error {
	dns, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", dnsstub.Addr)
	if err != nil {
		log.Printf("DNS stub for *.test on %s: %v; Safari will not reach the capture hosts", dnsstub.Addr, err)
	} else {
		go func() {
			if err := dnsstub.Serve(ctx, dns, dnsstub.Config{
				Answer: addr,
			}); err != nil {
				log.Printf("DNS stub: %v", err)
			}
		}()
	}

	if !clients.IsValid() {
		return nil
	}

	upstream, err := dnsstub.SystemResolver()
	if err != nil {
		return fmt.Errorf("the DNS stub for the network forwards to this Mac's resolver: %w", err)
	}

	lanAddr := netip.AddrPortFrom(netip.IPv4Unspecified(), dnsstub.LANPort).String()

	pc, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp4", lanAddr)
	if err != nil {
		return fmt.Errorf("DNS stub for the network: %w", err)
	}

	log.Printf("DNS stub on %s: *.test is %s, other names go to %s, for %s only", netip.AddrPortFrom(addr, dnsstub.LANPort), addr, upstream, clients)

	go func() {
		if err := dnsstub.Serve(ctx, pc, dnsstub.Config{
			Answer:   addr,
			Upstream: upstream,
			Clients:  clients,
		}); err != nil {
			log.Printf("DNS stub on %s: %v", lanAddr, err)
		}
	}()

	return nil
}

// consoleUser is the user owning /dev/console: whoever is logged in at the
// Mac's screen ("root" at the login window).
func consoleUser(context.Context) (string, error) {
	fi, err := os.Stat("/dev/console")
	if err != nil {
		return "", err
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("/dev/console: no owner")
	}

	u, err := user.LookupId(strconv.FormatUint(uint64(st.Uid), 10))
	if err != nil {
		return "", err
	}

	return u.Username, nil
}

// describeExecutable fills in what preflight reports of a browser found as
// an executable rather than an application bundle: nothing here.
func describeExecutable(context.Context, *browser.Browser, *preflightBrowser) error {
	return nil
}

// preflightPlatform adds the platform's own checks to r: none here.
func preflightPlatform(context.Context, *preflightReport) error {
	return nil
}
