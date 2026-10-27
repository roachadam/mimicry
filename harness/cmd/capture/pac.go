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
// which needs an administrator to set. The user points it once at
// safariPACURL; the script never changes, so a cached copy is never stale.
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

// safariProxy is how the system sends Safari to the capture proxy.
type safariProxy int

const (
	// safariProxyNone: it does not.
	safariProxyNone safariProxy = iota
	// safariProxyPAC: through safariPACURL, which sends SOCKS5 scenarios'
	// URLs to safariSOCKSProxy and the rest to safariHTTPProxy.
	safariProxyPAC
	// safariProxyManual: through manual proxies at safariHTTPProxy. Only a
	// manual proxy can carry saved credentials on macOS, but it sends every
	// URL to the HTTP proxy, so SOCKS5 scenarios cannot run.
	safariProxyManual
)

// safariProxyConfig reports how the system sends Safari to the capture
// proxy.
func safariProxyConfig(ctx context.Context) safariProxy {
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return safariProxyNone
	}
	s := string(out)
	if strings.Contains(s, "ProxyAutoConfigEnable : 1") {
		if strings.Contains(s, "ProxyAutoConfigURLString : "+safariPACURL) {
			return safariProxyPAC
		}
		return safariProxyNone
	}
	host, port, _ := net.SplitHostPort(safariHTTPProxy)
	if strings.Contains(s, "HTTPSEnable : 1") && strings.Contains(s, "HTTPSProxy : "+host) && strings.Contains(s, "HTTPSPort : "+port) {
		return safariProxyManual
	}
	return safariProxyNone
}

// safariProxyListen returns where the proxy for sc listens when Safari is
// captured, and the port its capture URL names.
func safariProxyListen(sc *server.Scenario, port int) (string, int) {
	if sc.Proxy == server.ProxySOCKS5 {
		return safariSOCKSProxy, safariSOCKSPort
	}
	return safariHTTPProxy, port
}
