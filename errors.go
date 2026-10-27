package mimicry

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/roachadam/mimicry/internal/dial"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/ws"
)

// ErrNotMeasured reports a request the profile cannot describe: one its
// browser was never captured making. RoundTrip returns it (wrapped, with
// what is missing) rather than guess. Test for it with errors.Is.
var ErrNotMeasured = headerorder.ErrNotMeasured

// ErrPlacement reports a header placement that cannot be applied: an
// invalid Place, a header or anchor the request does not send, or
// Request.HeaderPlacement on a request with its own order (Exact,
// HeaderOrderKey). Test for it with errors.Is.
var ErrPlacement = headerorder.ErrPlacement

// CORSError is a request the browser would not send: its CORS preflight's
// answer does not allow it, or it follows a redirect that fails the CORS
// check (Redirect). RoundTrip did not send it (Request.IgnoreCORS sends it
// anyway).
type CORSError struct {
	// URL is the refused request's: the preflighted URL, or the one that
	// answered with the redirect.
	URL *url.URL
	// Redirect is true when the refused response is URL's redirect, not
	// its preflight's answer.
	Redirect bool
	// Reason is the CORS rule the response breaks.
	Reason string
	// Status and Header are the refused response's.
	Status int
	Header http.Header
}

func (e *CORSError) Error() string {
	if e.Redirect {
		return fmt.Sprintf("mimicry: CORS check of the %d redirect from %s refused it: %s", e.Status, e.URL.Redacted(), e.Reason)
	}

	return fmt.Sprintf("mimicry: CORS preflight for %s refused the request: %s", e.URL.Redacted(), e.Reason)
}

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
	if err == nil {
		return nil
	}

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

// PinError is a connection to a pinned host whose verified chain matched
// none of its pins. Nothing was sent on it.
type PinError struct {
	// Host is the request's host.
	Host string
	// Presented are the chain's keys as "sha256/<base64>" pins, leaf
	// first.
	Presented []string
}

func (e *PinError) Error() string {
	return fmt.Sprintf("mimicry: no pin for %s matches its certificate chain (%s)", e.Host, strings.Join(e.Presented, ", "))
}

// CloseError is what ReadMessage returns once the socket has closed with
// a close handshake: the server's close frame (its code and reason, which
// the socket echoed) or its echo of the socket's own. Code is 1005 when
// the frame had none.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("mimicry: WebSocket closed: %d %q", e.Code, e.Reason)
}

// closeError turns the socket's close error into a *CloseError.
func closeError(err error) error {
	if ce, ok := errors.AsType[*ws.CloseError](err); ok {
		return &CloseError{
			Code:   ce.Code,
			Reason: ce.Reason,
		}
	}

	return err
}
