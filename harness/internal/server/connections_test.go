package server

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Each connections host presents its own certificate, naming the hosts it
// shares with; the HTTP/1.1 host offers only HTTP/1.1 and holds its
// answers back; the page requests every step's labels, step by step, and
// ends with /done.
func TestConnections(t *testing.T) {
	client, port, _ := replayServer(t, Connections)
	url := func(host, path string) string {
		return "https://" + host + ":" + port + path
	}
	_, html := fetch(t, client, "GET", url(connHost, "/"), nil)
	rest := html
	for _, s := range ConnectionsSteps() {
		var quoted []string
		for _, l := range s.Labels {
			quoted = append(quoted, `"`+url(s.Host, "/c/"+l)+`"`)
		}
		_, after, ok := strings.Cut(rest, "["+strings.Join(quoted, ", ")+"]")
		if !ok {
			t.Fatalf("page does not request %s %v next:\n%s", s.Host, s.Labels, html)
		}
		rest = after
	}
	if !strings.Contains(rest, `fetch("/done")`) {
		t.Error("page does not end with /done")
	}

	for _, s := range ConnectionsSteps() {
		start := time.Now()
		resp, _ := fetch(t, client, "GET", url(s.Host, "/c/"+s.Labels[0]), nil)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %s", s.Labels[0], resp.Status)
		}
		names := resp.TLS.PeerCertificates[0].DNSNames
		if want := connCerts[s.Host]; !slices.Equal(names, want) {
			t.Errorf("%s: certificate names %q, want %q", s.Host, names, want)
		}
		wantProto := "h2"
		if s.Host == connH1 {
			wantProto = "http/1.1"
			if len(s.Labels) != connParallel {
				t.Errorf("%d parallel requests, want %d", len(s.Labels), connParallel)
			}
			if d := time.Since(start); d < connParallelDelay {
				t.Errorf("HTTP/1.1 answer after %s, want at least %s", d, connParallelDelay)
			}
		}
		if got := resp.TLS.NegotiatedProtocol; got != wantProto {
			t.Errorf("%s: %s, want %s", s.Host, got, wantProto)
		}
	}
	// The pairs: each certificate names the other host.
	for a, b := range map[string]string{connHost: connSameSite, connPairX: connPairY} {
		if !slices.Contains(connCerts[a], b) || !slices.Contains(connCerts[b], a) {
			t.Errorf("%s and %s do not share a certificate", a, b)
		}
	}
	if resp, _ := fetch(t, client, "GET", url(connSolo, "/c/pair.x"), nil); resp.StatusCode != 404 {
		t.Errorf("another host's label: %s", resp.Status)
	}
}
