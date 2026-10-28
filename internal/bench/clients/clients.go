// Package clients puts each HTTP client under test behind one small
// interface: built for its newest Chrome profile, trusting the benchmark's
// CA, held to the protocol under test where it has a switch for it, and
// reporting the protocol each response actually came over, so the caller
// can discard a measurement that ran on the wrong one.
//
// Each adapter uses its library's ordinary request API: net/http, mimicry
// and tls-client stream the body, httpcloak and azuretls return it buffered,
// as their Do does.
package clients

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// Client is one client under test. Do is safe for concurrent use.
type Client interface {
	// Do sends r and reads its whole response body.
	Do(ctx context.Context, r *Request) (Response, error)
	// CloseIdle closes the client's connections and keeps its TLS session
	// caches, so the next request makes a new (resumed, where the client
	// resumes) connection.
	CloseIdle()
	// SetProxy moves a client built with a proxy to another proxy URL (the
	// next identity of a rotating proxy) the way the library's users do:
	// mimicry and net/http through the Proxy function the Transport
	// consults per request, the others through their SetProxy. Not safe
	// for use while a request is in flight.
	SetProxy(proxy string) error
	// Close releases everything.
	Close()
}

// Request is one request.
type Request struct {
	Method string
	URL    string
	// Body, with ContentType, is sent as the request body.
	Body        []byte
	ContentType string
	// Kind is how a browser would make it (site.Resource.Kind; "" for the
	// API calls of the micro and load scenarios, "document" for a page's
	// HTML) and Page the page making it. Only mimicry uses them.
	Kind string
	Page string
}

// Response is what Do reports.
type Response struct {
	Status int
	// Proto is the protocol the response came over; "" if unrecognised.
	Proto target.Proto
	// Resumed is the server's x-tls-resumed header: the connection's TLS
	// session was resumed.
	Resumed bool
	// Bytes is the body's length as read.
	Bytes int64
}

// Options configure a client.
type Options struct {
	// Proto is the protocol under test.
	Proto target.Proto
	// RootCAs trusts the benchmark's CA (target.RootCAs).
	RootCAs *x509.CertPool
	// Proxy is a proxy URL (target.ProxyURL), or "".
	Proxy string
	// Returning makes the client a returning browser: it starts with what
	// its browser keeps across restarts, shared by every client of the
	// process that sets it (mimicry: one AltSvcCache). Only for a
	// Factory with Returning.
	Returning bool
}

// Factory builds one kind of client.
type Factory struct {
	// Name is how reports and benchmarks name the client.
	Name string
	// Module is the Go module whose version the report records.
	Module string
	// Profile is the browser profile the adapter uses.
	Profile string
	// New builds a client.
	New func(Options) (Client, error)
	// Returning: the client has a returning-browser mode
	// (Options.Returning), which page loads over HTTP/3 measure besides
	// the cold one.
	Returning bool
	// NoH3SOCKS5 says why the client cannot carry HTTP/3 through a SOCKS5
	// proxy (UDP ASSOCIATE), or is "" when it can.
	NoH3SOCKS5 string
}

// ErrUnsupported marks a protocol or feature the client does not have.
var ErrUnsupported = errors.New("not supported")

// All are the clients under test, in report order.
func All() []Factory {
	return []Factory{
		netHTTP(),
		mimicryClient(),
		tlsClient(),
		httpcloakClient(),
		azuretlsClient(),
	}
}

// Named returns the client called name, one of All or Variants.
func Named(name string) (Factory, error) {
	for _, f := range append(All(), Variants()...) {
		if f.Name == name {
			return f, nil
		}
	}

	return Factory{}, fmt.Errorf("clients: no client %q", name)
}

// Version returns the version of f's module in this binary.
func (f Factory) Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	if f.Module == "std" {
		return info.GoVersion
	}

	for _, d := range info.Deps {
		if d.Path == f.Module {
			if d.Replace != nil {
				if d.Replace.Version == "" {
					return "local (" + d.Replace.Path + ")"
				}

				return d.Replace.Version
			}

			return d.Version
		}
	}

	return "unknown"
}

// Check returns an error unless resp came over p.
func Check(resp Response, p target.Proto) error {
	if resp.Proto != p {
		return fmt.Errorf("negotiated %s, not %s", protoName(resp.Proto), p)
	}

	return nil
}

func protoName(p target.Proto) string {
	if p == "" {
		return "an unknown protocol"
	}

	return string(p)
}

// Supports returns nil, or why the client cannot run a protocol through a
// proxy kind ("": none) (an n/a in the report). Every client has every
// protocol directly; through a proxy HTTP/3 needs UDP, which only SOCKS5
// carries (UDP ASSOCIATE), and only for the clients that use it.
func (f Factory) Supports(p target.Proto, proxy target.ProxyKind) error {
	switch {
	case proxy == "" || p != target.H3:
		return nil
	case proxy != target.ProxySOCKS5:
		return fmt.Errorf("%w: HTTP/3 through an HTTP proxy (CONNECT carries no UDP)", ErrUnsupported)
	case f.NoH3SOCKS5 != "":
		return fmt.Errorf("%w: HTTP/3 through SOCKS5: %s", ErrUnsupported, f.NoH3SOCKS5)
	}

	return nil
}

// resumedHeader reads the servers' x-tls-resumed value.
func resumedHeader(v string) bool {
	return strings.TrimSpace(v) == "r"
}

// protoOf returns the protocol a response came over: the server's x-proto
// header (the request's protocol as the server saw it), else what the
// client reports.
func protoOf(server, client string) target.Proto {
	if p := target.FromWire(server); p != "" {
		return p
	}

	if p := target.FromWire(client); p != "" {
		return p
	}

	return target.Proto(client)
}

// chromeHeaders are Chrome's navigation headers in its order, for the
// clients whose callers set headers themselves (tls-client, azuretls): what
// their users pass. net/http, the baseline, sends Go's own; mimicry and
// httpcloak send their profiles'.
var chromeHeaders = [][2]string{
	{"sec-ch-ua", `"Chromium";v="156", "Google Chrome";v="156", "Not:A-Brand";v="99"`},
	{"sec-ch-ua-mobile", "?0"},
	{"sec-ch-ua-platform", `"macOS"`},
	{"upgrade-insecure-requests", "1"},
	{"user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36"},
	{"accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
	{"sec-fetch-site", "none"},
	{"sec-fetch-mode", "navigate"},
	{"sec-fetch-user", "?1"},
	{"sec-fetch-dest", "document"},
	{"accept-encoding", "gzip, deflate, br, zstd"},
	{"accept-language", "en-US,en;q=0.9"},
	{"priority", "u=0, i"},
}
