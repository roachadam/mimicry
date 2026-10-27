package mimicry

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/roachadam/mimicry/internal/dial"
)

// ProxyError reports that a request never reached the server because of
// its proxy: unreachable, a non-2xx CONNECT answer, a failed SOCKS5
// handshake or login, or the deadline passing on the way. For a
// plain-HTTP request sent to an HTTP proxy whole, only a final 407 counts;
// other answers are the response. Failures inside an open tunnel are the
// server's, not a ProxyError.
//
// A proxy-rotation loop tests for it with errors.As (see decision 0033).
// It wraps the cause, so errors.Is(err, context.DeadlineExceeded) works.
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
