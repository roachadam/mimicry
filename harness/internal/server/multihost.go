package server

import (
	"crypto/tls"
	"fmt"
	"slices"
	"sync"
	"time"
)

// multiHost is a page served across several hostnames from one listener,
// each with its own certificate: the replay of a real page, or the kinds
// page. The capture is the whole page (capture.Page).
type multiHost interface {
	answer(r request) response
	// hosts are the page's hostnames; the first is the one it starts on,
	// at "/".
	hosts() []string
	// certNames are the names on host's certificate (host first, then
	// those whose requests may share its connections); ok is false for a
	// host the page does not have.
	certNames(host string) (names []string, ok bool)
	// h1Only reports a host offered only http/1.1.
	h1Only(host string) bool
	// length is how long the page runs, for the capture tool's wait.
	length() time.Duration
}

// MultiHost reports, for a scenario serving a page across several hosts,
// its hostnames (the first being where the page starts) and how long the
// page runs; ok is false for single-host scenarios.
func (sc *Scenario) MultiHost() (hosts []string, length time.Duration, ok bool) {
	if sc.multi == nil {
		return nil, 0, false
	}
	return slices.Clone(sc.multi.hosts()), sc.multi.length(), true
}

// multiHostTLS returns base answering each of m's hosts with its own
// certificate and ALPN.
func multiHostTLS(m multiHost, base *tls.Config, issue func([]string) (tls.Certificate, error)) *tls.Config {
	var mu sync.Mutex
	configs := map[string]*tls.Config{}
	cfg := base.Clone()
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		mu.Lock()
		defer mu.Unlock()
		if c, ok := configs[hello.ServerName]; ok {
			return c, nil
		}
		names, ok := m.certNames(hello.ServerName)
		if !ok {
			return nil, fmt.Errorf("no host %q here", hello.ServerName)
		}
		cert, err := issue(names)
		if err != nil {
			return nil, err
		}
		c := base.Clone()
		c.Certificates = []tls.Certificate{
			cert,
		}
		if m.h1Only(hello.ServerName) {
			c.NextProtos = []string{
				"http/1.1",
			}
		}
		configs[hello.ServerName] = c
		return c, nil
	}
	return cfg
}
