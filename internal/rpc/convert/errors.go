package convert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/profile"
)

// InvalidError is a call the core cannot carry out as given: an unknown
// object, a malformed argument, a call before Hello (CODE_INVALID).
type InvalidError struct {
	Message string
}

func (e *InvalidError) Error() string {
	return "mimicry-core: " + e.Message
}

// Invalid returns an *InvalidError with a formatted message.
func Invalid(format string, args ...any) error {
	return &InvalidError{
		Message: fmt.Sprintf(format, args...),
	}
}

// VerifyError is the SDK's answer to a VerifyConnection reverse call
// failing the connection (CODE_VERIFY): its error's message.
type VerifyError struct {
	Message string
}

func (e *VerifyError) Error() string {
	return e.Message
}

// codes are the codes in the order Error picks its code by: the typed
// errors, whose fields the SDK reads, before the sentinels they may wrap,
// and the context's before NET, which a deadline on a dial also is.
var codes = []struct {
	code pb.Code
	is   func(error) bool
}{
	{pb.Code_CODE_PROXY, as[*mimicry.ProxyError]},
	{pb.Code_CODE_CORS, as[*mimicry.CORSError]},
	{pb.Code_CODE_PIN, as[*mimicry.PinError]},
	{pb.Code_CODE_CLOSE, as[*mimicry.CloseError]},
	{pb.Code_CODE_VALIDATION, as[*profile.ValidationError]},
	{pb.Code_CODE_VERIFY, as[*VerifyError]},
	{pb.Code_CODE_NOT_MEASURED, is(mimicry.ErrNotMeasured)},
	{pb.Code_CODE_BLOCKED, is(mimicry.ErrBlocked)},
	{pb.Code_CODE_PLACEMENT, is(mimicry.ErrPlacement)},
	{pb.Code_CODE_INVALID, as[*InvalidError]},
	{pb.Code_CODE_CANCELED, is(context.Canceled)},
	{pb.Code_CODE_DEADLINE, deadline},
	{pb.Code_CODE_NET, network},
}

func as[T error](err error) bool {
	_, ok := errors.AsType[T](err)

	return ok
}

func is(target error) func(error) bool {
	return func(err error) bool {
		return errors.Is(err, target)
	}
}

// deadline reports a deadline passing: the context's, a connection's, or
// any error that says it timed out (http.Client's Timeout).
func deadline(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}

	ne, ok := errors.AsType[net.Error](err)

	return ok && ne.Timeout()
}

// network reports a failure of the network or of TLS. It does not take
// net.Error, which *url.Error, wrapping every error http.Client returns,
// implements.
func network(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}

	return as[*net.OpError](err) || as[*net.DNSError](err) || as[*net.AddrError](err) ||
		as[syscall.Errno](err) || as[tls.AlertError](err) || as[tls.RecordHeaderError](err) ||
		as[*tls.CertificateVerificationError](err) || as[*tls.ECHRejectionError](err) ||
		as[x509.UnknownAuthorityError](err) || as[x509.HostnameError](err) || as[x509.CertificateInvalidError](err)
}

// Error returns err as data: its code, the most specific of those its
// chain matches (codes' order); every code it matches; its message; and
// the fields of its typed errors.
func Error(err error) *pb.Error {
	out := &pb.Error{
		Message: Text(err.Error()),
	}

	for _, c := range codes {
		if c.is(err) {
			out.Matches = append(out.Matches, c.code)
		}
	}

	if len(out.Matches) == 0 {
		out.Matches = []pb.Code{
			pb.Code_CODE_INTERNAL,
		}
	}

	out.Code = out.Matches[0]

	if e, ok := errors.AsType[*mimicry.ProxyError](err); ok {
		out.Proxy = &pb.ProxyError{
			StatusCode: int32(e.StatusCode),
			Header:     HeaderValues(e.Header),
		}
		if e.Proxy != nil {
			out.Proxy.Proxy = e.Proxy.String()
		}
	}

	if e, ok := errors.AsType[*mimicry.CORSError](err); ok {
		out.Cors = &pb.CORSError{
			Redirect: e.Redirect,
			Reason:   Text(e.Reason),
			Status:   int32(e.Status),
			Header:   HeaderValues(e.Header),
		}
		if e.URL != nil {
			out.Cors.Url = e.URL.String()
		}
	}

	if e, ok := errors.AsType[*mimicry.PinError](err); ok {
		out.Pin = &pb.PinError{
			Host:      e.Host,
			Presented: e.Presented,
		}
	}

	if e, ok := errors.AsType[*mimicry.CloseError](err); ok {
		out.Close = &pb.CloseError{
			Code:   int32(e.Code),
			Reason: Text(e.Reason),
		}
	}

	if e, ok := errors.AsType[*profile.ValidationError](err); ok {
		out.Validation = &pb.ValidationError{}
		for _, p := range e.Problems {
			out.Validation.Problems = append(out.Validation.Problems, &pb.Problem{
				Path:    Text(p.Path),
				Message: Text(p.Message),
			})
		}
	}

	return out
}
