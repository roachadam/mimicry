//go:build !windows

package main

import (
	"context"
	"errors"
	"log"
	"net"
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

// startDNSStub answers *.test lookups until ctx ends. Safari resolves
// through the system: the stub answers its *.test lookups (Chrome and
// Firefox map the names themselves).
func startDNSStub(ctx context.Context) {
	dns, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", dnsstub.Addr)
	if err != nil {
		log.Printf("DNS stub for *.test on %s: %v; Safari will not reach the capture hosts", dnsstub.Addr, err)
	} else {
		go func() {
			if err := dnsstub.Serve(ctx, dns); err != nil {
				log.Printf("DNS stub: %v", err)
			}
		}()
	}
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
