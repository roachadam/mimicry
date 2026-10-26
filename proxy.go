package mimicry

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/roachadam/mimicry/internal/dial"
)

// ProxyError is a request's failure to reach its proxy (Config.Proxy) or
// to open a tunnel through it, so nothing reached the server: the proxy
// could not be connected to, answered CONNECT with other than 2xx (a 407
// once the credentials in its URL were given as the browser gives them,
// or when it has none), refused or failed a SOCKS5 handshake or its
// username and password, or the deadline passed on the way. A plain-HTTP
// request sent to an HTTP proxy whole returns one when the proxy cannot be
// connected to or answers it 407 after the browser's credential flow; any
// other answer is the response, since only a 407 is surely the proxy's.
// What fails once the tunnel is up (the TLS handshake with the server, the
// request) is the server's, not a ProxyError.
//
// It is what a rotation loop tests for with errors.As: this proxy is
// dead, unreachable or refusing, as against the site refusing the
// request. It wraps the cause: errors.Is(err, context.DeadlineExceeded)
// still reports a timeout.
type ProxyError struct {
	// Proxy is the proxy's URL with its password removed (the username
	// stays).
	Proxy *url.URL
	// StatusCode is the proxy's answer to CONNECT (or to the plain-HTTP
	// request), or 0 for a SOCKS5 proxy or when it gave none.
	StatusCode int
	// Header is that answer's header, or nil.
	Header http.Header
	// Err is the cause.
	Err error
}

func (e *ProxyError) Error() string {
	return fmt.Sprintf("mimicry: proxy %s: %v", e.Proxy, e.Err)
}

func (e *ProxyError) Unwrap() error {
	return e.Err
}

// publicError returns err with a proxy failure made a *ProxyError, which
// replaces the errors wrapping it (attempt counts), since it says all
// they do; any other err as it is.
func publicError(err error) error {
	var pe *dial.ProxyError
	if !errors.As(err, &pe) {
		return err
	}
	p := *pe.Proxy
	if p.User != nil {
		p.User = url.User(p.User.Username())
	}
	return &ProxyError{
		Proxy:      &p,
		StatusCode: pe.StatusCode,
		Header:     pe.Header,
		Err:        pe.Err,
	}
}
