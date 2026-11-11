package convert

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"syscall"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/rpc/pb"
	"github.com/roachadam/mimicry/profile"
)

// Each code comes from its Go error, wrapped as RoundTrip and
// http.Client wrap them: the code is the most specific one, matches lists
// every code the chain matches (a proxy failure that is a deadline is
// both), and the typed fields are filled in. Every code has a row.
func TestErrorCodes(t *testing.T) {
	proxyURL, _ := url.Parse("http://user@proxy.test:8080")
	pageURL, _ := url.Parse("https://api.test/cart")

	wrap := func(err error) error {
		return &url.Error{
			Op:  "Get",
			URL: "https://shop.test/",
			Err: err,
		}
	}

	tested := map[pb.Code]bool{}

	for _, tc := range []struct {
		name    string
		err     error
		code    pb.Code
		matches []pb.Code
		check   func(*pb.Error) bool
	}{
		{
			name: "not measured",
			err:  wrap(fmt.Errorf("%w: profile x has no websocket section", mimicry.ErrNotMeasured)),
			code: pb.Code_CODE_NOT_MEASURED,
		},
		{
			name: "CORS",
			err: wrap(&mimicry.CORSError{
				URL:      pageURL,
				Redirect: true,
				Reason:   "no Access-Control-Allow-Origin",
				Status:   http.StatusFound,
				Header: http.Header{
					"Location": {"/x"},
				},
			}),
			code: pb.Code_CODE_CORS,
			check: func(e *pb.Error) bool {
				c := e.GetCors()

				return c.GetUrl() == pageURL.String() && c.GetRedirect() && c.GetReason() == "no Access-Control-Allow-Origin" &&
					c.GetStatus() == http.StatusFound && string(c.GetHeader()[0].GetValues()[0]) == "/x"
			},
		},
		{
			name: "proxy, at its deadline",
			err: wrap(&mimicry.ProxyError{
				Proxy:      proxyURL,
				StatusCode: http.StatusProxyAuthRequired,
				Err:        context.DeadlineExceeded,
			}),
			code:    pb.Code_CODE_PROXY,
			matches: []pb.Code{pb.Code_CODE_PROXY, pb.Code_CODE_DEADLINE},
			check: func(e *pb.Error) bool {
				return e.GetProxy().GetProxy() == "http://user@proxy.test:8080" && e.GetProxy().GetStatusCode() == http.StatusProxyAuthRequired
			},
		},
		{
			name: "pin",
			err: wrap(&mimicry.PinError{
				Host:      "shop.test",
				Presented: []string{"sha256/AAAA"},
			}),
			code: pb.Code_CODE_PIN,
			check: func(e *pb.Error) bool {
				return e.GetPin().GetHost() == "shop.test" && slices.Equal(e.GetPin().GetPresented(), []string{"sha256/AAAA"})
			},
		},
		{
			name: "blocked",
			err:  wrap(fmt.Errorf("beacon to tracker.test: %w", mimicry.ErrBlocked)),
			code: pb.Code_CODE_BLOCKED,
		},
		{
			name: "placement",
			err:  fmt.Errorf("%w: Config.HeaderPlacement[0]: no anchor", mimicry.ErrPlacement),
			code: pb.Code_CODE_PLACEMENT,
		},
		{
			name: "close",
			err: &mimicry.CloseError{
				Code:   4001,
				Reason: "bye",
			},
			code: pb.Code_CODE_CLOSE,
			check: func(e *pb.Error) bool {
				return e.GetClose().GetCode() == 4001 && e.GetClose().GetReason() == "bye"
			},
		},
		{
			name: "profile validation",
			err: &profile.ValidationError{
				Problems: []profile.Problem{
					{
						Path:    "tls.extensions[3]",
						Message: "unknown",
					},
				},
			},
			code: pb.Code_CODE_VALIDATION,
			check: func(e *pb.Error) bool {
				p := e.GetValidation().GetProblems()

				return len(p) == 1 && p[0].GetPath() == "tls.extensions[3]" && p[0].GetMessage() == "unknown"
			},
		},
		{
			name: "canceled",
			err:  wrap(context.Canceled),
			code: pb.Code_CODE_CANCELED,
		},
		{
			name: "deadline",
			err:  wrap(context.DeadlineExceeded),
			code: pb.Code_CODE_DEADLINE,
		},
		{
			name:    "a connection's deadline",
			err:     wrap(&net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}),
			code:    pb.Code_CODE_DEADLINE,
			matches: []pb.Code{pb.Code_CODE_DEADLINE, pb.Code_CODE_NET},
		},
		{
			name: "network",
			err:  wrap(&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}),
			code: pb.Code_CODE_NET,
		},
		{
			name: "invalid call",
			err:  Invalid("no object %d", 7),
			code: pb.Code_CODE_INVALID,
		},
		{
			name: "rejected by VerifyConnection",
			err: wrap(&VerifyError{
				Message: "not our certificate",
			}),
			code: pb.Code_CODE_VERIFY,
			check: func(e *pb.Error) bool {
				return e.GetMessage() == `Get "https://shop.test/": not our certificate`
			},
		},
		{
			name: "anything else",
			err:  wrap(errors.New("http2: server sent GOAWAY")),
			code: pb.Code_CODE_INTERNAL,
		},
		{
			name: "not UTF-8",
			err:  errors.New("bad header \xff"),
			code: pb.Code_CODE_INTERNAL,
			check: func(e *pb.Error) bool {
				return e.GetMessage() == "bad header \uFFFD"
			},
		},
	} {
		tested[tc.code] = true

		t.Run(tc.name, func(t *testing.T) {
			got := Error(tc.err)

			matches := tc.matches
			if matches == nil {
				matches = []pb.Code{tc.code}
			}

			if got.GetCode() != tc.code || !slices.Equal(got.GetMatches(), matches) {
				t.Errorf("code %v, matches %v; want %v, %v", got.GetCode(), got.GetMatches(), tc.code, matches)
			}

			if got.GetMessage() != Text(tc.err.Error()) {
				t.Errorf("message %q", got.GetMessage())
			}

			if tc.check != nil && !tc.check(got) {
				t.Errorf("fields %v", got)
			}
		})
	}

	for v, name := range pb.Code_name {
		if !tested[pb.Code(v)] {
			t.Errorf("%s has no row", name)
		}
	}
}
