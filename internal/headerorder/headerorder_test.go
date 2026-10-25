package headerorder

import (
	"net/http"
	"slices"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

var prof = []profile.Header{
	{
		Name:  "user-agent",
		Value: "agent/1",
	},
	{
		Name:  "accept",
		Value: "*/*",
	},
	{
		Name: "cookie",
	},
	{
		Name:  "accept-language",
		Value: "en-US",
	},
}

func never(string) bool {
	return false
}

func names(fields []Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Name + "=" + f.Value
	}
	return out
}

// Header-Order: is the one Build behaviour no capture exercises.
func TestBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.Header
		want []string
	}{
		{
			name: "per-request order override",
			h: http.Header{
				Key:   {"Accept", "X-A", "User-Agent"},
				"X-A": {"1"},
			},
			want: []string{
				"accept=*/*",
				"x-a=1",
				"user-agent=agent/1",
			},
		},
		{
			name: "override key in any case, never sent",
			h: http.Header{
				"header-order:": {"accept", "USER-AGENT"},
			},
			want: []string{
				"accept=*/*",
				"user-agent=agent/1",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(Build(prof, tc.h, never, nil)); !slices.Equal(got, tc.want) {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
