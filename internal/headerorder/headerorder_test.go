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

// Header-Order: is the one Build behaviour no capture exercises; no
// capture has a caller's own Content-Length either, which a body's length
// (a Set) must replace in every spelling: two would let a server and a
// proxy disagree on where the body ends.
func TestBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.Header
		sets []Set
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
		{
			name: "a set replaces every spelling of the request's",
			h: http.Header{
				"Content-Length": {"5"},
				"content-length": {"7"},
			},
			sets: []Set{
				{
					Name:  "content-length",
					Value: "3",
				},
			},
			want: []string{
				"user-agent=agent/1",
				"accept=*/*",
				"accept-language=en-US",
				"content-length=3",
			},
		},
		{
			name: "an empty set sends nothing, not even the default",
			h: http.Header{
				"Accept": {"text/html"},
			},
			sets: []Set{
				{
					Name: "accept",
				},
			},
			want: []string{
				"user-agent=agent/1",
				"accept-language=en-US",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(Build(nil, prof, tc.h, tc.sets, never, nil)); !slices.Equal(got, tc.want) {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
