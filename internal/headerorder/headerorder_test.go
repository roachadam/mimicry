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

func TestBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.Header
		skip func(string) bool
		want []string
	}{
		{
			name: "defaults in profile order",
			h:    http.Header{},
			want: []string{
				"user-agent=agent/1",
				"accept=*/*",
				"accept-language=en-US",
			},
		},
		{
			name: "request values win and keep the profile's position",
			h: http.Header{
				"Accept-Language": {"de"},
				"Cookie":          {"a=1"},
			},
			want: []string{
				"user-agent=agent/1",
				"accept=*/*",
				"cookie=a=1",
				"accept-language=de",
			},
		},
		{
			name: "a key with no values suppresses the default",
			h: http.Header{
				"Accept": nil,
			},
			want: []string{
				"user-agent=agent/1",
				"accept-language=en-US",
			},
		},
		{
			name: "unknown headers follow, sorted",
			h: http.Header{
				"X-B": {"2"},
				"X-A": {"1"},
			},
			want: []string{
				"user-agent=agent/1",
				"accept=*/*",
				"accept-language=en-US",
				"x-a=1",
				"x-b=2",
			},
		},
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
			name: "repeated values stay separate fields",
			h: http.Header{
				"Cookie": {"a=1", "b=2"},
			},
			want: []string{
				"user-agent=agent/1",
				"accept=*/*",
				"cookie=a=1",
				"cookie=b=2",
				"accept-language=en-US",
			},
		},
		{
			name: "skipped headers are never sent",
			h: http.Header{
				"Connection": {"close"},
			},
			skip: func(name string) bool {
				return name == "connection" || name == "accept"
			},
			want: []string{
				"user-agent=agent/1",
				"accept-language=en-US",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skip := tc.skip
			if skip == nil {
				skip = never
			}
			if got := names(Build(prof, tc.h, skip, nil)); !slices.Equal(got, tc.want) {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// With a browser table, the page's headers and the table's names the list
// sends go out as one run in the table's order (Safari's single-a.fetch.so),
// where the list has its first table name; a list with an OtherHeaders slot,
// a Key order or no page header keeps the list's order.
func TestBuildTable(t *testing.T) {
	safari := &profile.HeaderTable{
		Hash: profile.HashCF,
		Own:  []string{"sec-fetch-dest", "user-agent", "accept", "referer", "sec-fetch-site", "sec-fetch-mode", "origin"},
	}
	list := make([]profile.Header, 0, 8)
	for _, nv := range [][2]string{
		{"sec-fetch-dest", "empty"},
		{"user-agent", "agent/1"},
		{"accept", "*/*"},
		{"referer", ""},
		{"sec-fetch-site", "same-origin"},
		{"sec-fetch-mode", "cors"},
		{"origin", ""},
		{"accept-language", "en-US"},
	} {
		list = append(list, profile.Header{
			Name:  nv[0],
			Value: nv[1],
		})
	}
	withSlot := slices.Insert(slices.Clone(list), 6, profile.Header{
		Name: profile.OtherHeaders,
	})
	for _, tc := range []struct {
		name string
		list []profile.Header
		h    http.Header
		want []string
	}{
		{
			// origin, a slot without a value, is not in the table.
			name: "page header",
			list: list,
			h: http.Header{
				"A":       {"1"},
				"Referer": {"https://a.test/"},
			},
			want: []string{"accept=*/*", "sec-fetch-site=same-origin", "sec-fetch-mode=cors", "user-agent=agent/1", "referer=https://a.test/", "sec-fetch-dest=empty", "a=1", "accept-language=en-US"},
		},
		{
			name: "no page header",
			list: list,
			h: http.Header{
				"Referer": {"https://a.test/"},
			},
			want: []string{"sec-fetch-dest=empty", "user-agent=agent/1", "accept=*/*", "referer=https://a.test/", "sec-fetch-site=same-origin", "sec-fetch-mode=cors", "accept-language=en-US"},
		},
		{
			name: "a suppressed default is no page header",
			list: list,
			h: http.Header{
				"Referer": {"https://a.test/"},
				"X-None":  nil,
			},
			want: []string{"sec-fetch-dest=empty", "user-agent=agent/1", "accept=*/*", "referer=https://a.test/", "sec-fetch-site=same-origin", "sec-fetch-mode=cors", "accept-language=en-US"},
		},
		{
			name: "slot",
			list: withSlot,
			h: http.Header{
				"A":       {"1"},
				"Referer": {"https://a.test/"},
			},
			want: []string{"sec-fetch-dest=empty", "user-agent=agent/1", "accept=*/*", "referer=https://a.test/", "sec-fetch-site=same-origin", "sec-fetch-mode=cors", "a=1", "accept-language=en-US"},
		},
		{
			name: "key",
			list: list,
			h: http.Header{
				"A": {"1"},
				Key: {"accept", "user-agent"},
			},
			want: []string{"accept=*/*", "user-agent=agent/1", "a=1"},
		},
	} {
		if got := names(Build(tc.list, tc.h, never, safari)); !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}
