package server

import (
	"io"
	"net"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// A request line or header line ending in a bare LF is refused, not recorded
// as if it ended in CRLF, so a client that regresses to LF fails visibly.
func TestHTTP1RejectsBareLF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request string
	}{
		{
			name:    "request line",
			request: "GET / HTTP/1.1\nHost: x.test\r\n\r\n",
		},
		{
			name:    "header line",
			request: "GET / HTTP/1.1\r\nHost: x.test\nUser-Agent: u\r\n\r\n",
		},
		{
			name:    "end of header section",
			request: "GET / HTTP/1.1\r\nHost: x.test\r\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, srv := net.Pipe()
			// Before the fix the request was answered and the server then
			// waited for the next one: read the answer and end the
			// connection after the request so the test ends either way.
			go func() {
				_, _ = io.Copy(io.Discard, client)
			}()
			go func() {
				_, _ = io.WriteString(client, tc.request)
				_ = client.Close()
			}()
			c := &capture.Capture{}
			err := serveHTTP1(srv, "x.test", c, DefaultScenario, &pageLog{}, func() {})
			_ = srv.Close()
			if err == nil || !strings.Contains(err.Error(), "CRLF") {
				t.Errorf("serveHTTP1 error = %v, want a CRLF error", err)
			}
			if len(c.H1) != 0 {
				t.Errorf("recorded %+v", c.H1)
			}
		})
	}
}
