package server

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2/hpack"
)

// multiHost is a page served across several hostnames from one listener,
// each with its own certificate. The capture is the whole page
// (capture.Page).
type multiHost interface {
	answer(r request) response
	// hosts are the page's hostnames; the first is the one it starts on,
	// at Scenario.StartPath.
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

// fixedHosts is the host side of a multiHost page with a fixed set of
// hosts, each on a certificate of its own.
type fixedHosts struct {
	// names are the page's hostnames, the one it starts on first.
	names []string
	// http1 are the hosts offered only http/1.1.
	http1 []string
	// runFor is how long the page runs.
	runFor time.Duration
}

func (f fixedHosts) hosts() []string {
	return f.names
}

func (f fixedHosts) certNames(host string) ([]string, bool) {
	if !slices.Contains(f.names, host) {
		return nil, false
	}

	return []string{
		host,
	}, true
}

func (f fixedHosts) h1Only(host string) bool {
	return slices.Contains(f.http1, host)
}

func (f fixedHosts) length() time.Duration {
	return f.runFor
}

// splitAuthority splits a request's authority into host and port; an
// authority without a port is on 443.
func splitAuthority(authority string) (host, port string) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return authority, "443"
	}

	return host, port
}

// originOn returns the origin of a host on port, with scheme ("https://"
// or "http://").
func originOn(scheme, port string) func(host string) string {
	return func(host string) string {
		return scheme + net.JoinHostPort(host, port)
	}
}

// corsAllow lets origin read a response, credentials included.
func corsAllow(origin string) []hpack.HeaderField {
	return []hpack.HeaderField{
		{
			Name:  "access-control-allow-origin",
			Value: origin,
		},
		{
			Name:  "access-control-allow-credentials",
			Value: "true",
		},
	}
}

// preflightAllow is a preflight's answer headers: allow, then methods, then
// the requested headers (Access-Control-Request-Headers) unless empty.
func preflightAllow(allow []hpack.HeaderField, methods, requested string) []hpack.HeaderField {
	out := slices.Concat(allow, []hpack.HeaderField{
		{
			Name:  "access-control-allow-methods",
			Value: methods,
		},
	})
	if requested != "" {
		out = append(out, hpack.HeaderField{
			Name:  "access-control-allow-headers",
			Value: requested,
		})
	}

	return out
}

// cookieDeletions deletes each cookie in the form it was set (Path, Domain,
// Secure, HttpOnly kept). A nameless cookie keeps its whole pair: browsers
// ignore a Set-Cookie whose name and value are both empty.
func cookieDeletions(set []string) []string {
	out := make([]string, 0, len(set))
	for _, c := range set {
		pair, _, _ := strings.Cut(c, ";")
		d := pair

		if name := setCookieName(c); name != "" {
			d = name + "="
		}

		for _, a := range strings.Split(c, "; ")[1:] {
			if strings.HasPrefix(a, "Path=") || strings.HasPrefix(a, "Domain=") || a == "Secure" || a == "HttpOnly" {
				d += "; " + a
			}
		}

		out = append(out, d+"; Max-Age=0")
	}

	return out
}

// mustJSON is v as JSON for a page's script. The pages marshal values their
// own code builds (strings, numbers, slices and structs of them), which
// cannot fail; a failure is a bug in the page.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("server: page JSON: " + err.Error())
	}

	return b
}

// htmlPage is a page titled "mimicry <title>" with head and body as given.
func htmlPage(title, head, body string) []byte {
	return []byte("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>mimicry " + title +
		"</title><link rel=\"icon\" href=\"data:,\">" + head + "</head><body>\n" + body + "</body></html>\n")
}
