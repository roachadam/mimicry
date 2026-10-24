package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/harness/internal/server"
)

// Safari takes its proxy from the system's automatic proxy configuration,
// which only an administrator can set. The user points it once at
// safariPACURL, served here: a script that never changes, so a cached copy
// is never stale. It proxies only *.test URLs and sends everything else
// direct.
const (
	safariPACAddr    = "127.0.0.1:15356"
	safariPACURL     = "http://" + safariPACAddr + "/mimicry.pac"
	safariHTTPProxy  = "127.0.0.1:15354"
	safariSOCKSProxy = "127.0.0.1:15355"
	// safariSOCKSPort is the port SOCKS5 scenarios put in the capture URL,
	// which the script sends to the SOCKS proxy (other *.test URLs go to the
	// HTTP proxy). Behind a proxy the browser never dials it.
	safariSOCKSPort = 15443
)

var safariPAC = `function FindProxyForURL(url, host) {
	if (!dnsDomainIs(host, ".test")) return "DIRECT";
	if (shExpMatch(url, "*:` + strconv.Itoa(safariSOCKSPort) + `/*")) return "SOCKS ` + safariSOCKSProxy + `";
	return "PROXY ` + safariHTTPProxy + `";
}
`

// servePAC serves the script until ctx ends.
func servePAC(ctx context.Context) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", safariPACAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
			_, _ = w.Write([]byte(safariPAC))
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// pacConfigured reports whether the system uses safariPACURL.
func pacConfigured(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "ProxyAutoConfigEnable : 1") && strings.Contains(s, "ProxyAutoConfigURLString : "+safariPACURL)
}

// safariProxyListen returns where the proxy for sc listens when Safari is
// captured, and the port its capture URL names.
func safariProxyListen(sc *server.Scenario, port int) (string, int) {
	if sc.Proxy == server.ProxySOCKS5 {
		return safariSOCKSProxy, safariSOCKSPort
	}
	return safariHTTPProxy, port
}
