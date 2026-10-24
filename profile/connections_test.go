package profile

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateConnections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(p *Profile)
		path   string
		want   string
	}{
		{
			name: "no connections section",
			mutate: func(p *Profile) {
				p.Connections = nil
			},
			path: "connections",
			want: "is required",
		},
		{
			name: "no HTTP/1.1 connections",
			mutate: func(p *Profile) {
				p.Connections.HTTP1PerHost = 0
			},
			path: "connections.http1_per_host",
			want: "must be at least 1",
		},
		{
			name: "no coalescing rule",
			mutate: func(p *Profile) {
				p.Connections.Coalesce = ""
			},
			path: "connections.coalesce",
			want: `must be "lookup" or "connect"`,
		},
		{
			name: "unknown coalescing rule",
			mutate: func(p *Profile) {
				p.Connections.Coalesce = "never"
			},
			path: "connections.coalesce",
			want: `must be "lookup" or "connect"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := parseValid(t)
			tc.mutate(p)
			var verr *ValidationError
			if err := p.Validate(); !errors.As(err, &verr) {
				t.Fatalf("Validate() = %v, want a *ValidationError", err)
			}
			for _, problem := range verr.Problems {
				if problem.Path == tc.path && strings.Contains(problem.Message, tc.want) {
					return
				}
			}
			t.Fatalf("no problem at %s containing %q; got:\n%v", tc.path, tc.want, verr)
		})
	}
}

func TestValidateAcceptsBothCoalescingRules(t *testing.T) {
	for _, rule := range []Coalescing{
		CoalesceOnLookup,
		CoalesceOnConnect,
	} {
		p := parseValid(t)
		p.Connections.Coalesce = rule
		if err := p.Validate(); err != nil {
			t.Errorf("coalesce %q: %v", rule, err)
		}
	}
}
