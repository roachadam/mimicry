package h3

import (
	"testing"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profiles"
)

// Firefox writes alt-used without the port when the alternative is on 443
// (nsHttpChannel::BeginConnect), and with it otherwise. Every capture
// runs on an unprivileged port, so no capture reaches the first form;
// realcheck saw it from Firefox 158 at fp.impersonate.pro over HTTP/3
// (harness/reports/realcheck-2026-10-25.md), where mimicry sent ":443".
func TestAltUsedPort(t *testing.T) {
	tests := []struct {
		port string
		want string
	}{
		{
			port: "443",
			want: "www.example.com",
		},
		{
			port: "8443",
			want: "www.example.com:8443",
		},
	}
	for _, tt := range tests {
		fields := placeAltUsed(profiles.Firefox(), altUsedValue("www.example.com", tt.port), []headerorder.Field{
			{
				Name:  "user-agent",
				Value: "x",
			},
		}, nil)
		got := ""
		for _, f := range fields {
			if f.Name == "alt-used" {
				got = f.Value
			}
		}
		if got != tt.want {
			t.Errorf("port %s: alt-used %q, want %q", tt.port, got, tt.want)
		}
	}
}
