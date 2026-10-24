package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// In Plain mode an absolute-form request for a host Allow refuses is
// answered 403, not handed to the capture server.
func TestPlainRefusesHostsAllowRefuses(t *testing.T) {
	p := &Proxy{
		Allow: func(host string) bool {
			return strings.HasSuffix(host, ".test")
		},
		Plain: true,
	}
	client, srv := net.Pipe()
	defer func() { _ = client.Close() }()
	type result struct {
		pass io.Reader
		err  error
	}
	done := make(chan result, 1)
	go func() {
		pass, err := p.http(srv, bufio.NewReader(srv), 1)
		done <- result{pass, err}
	}()
	go func() {
		_, _ = io.WriteString(client, "GET http://update.example.com/check HTTP/1.1\r\nHost: update.example.com\r\n\r\n")
	}()
	if err := client.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status %s, want 403", resp.Status)
	}
	if r := <-done; r.pass != nil {
		t.Error("the refused request was passed on")
	}
}
