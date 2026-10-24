package headerorder

import (
	"slices"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// The hashes agree with a port of the browsers' sources: Blink's
// rapidhash over case-folded UTF-16 (short, mid-length and over 48 bytes)
// and CFString's hash, in any case.
func TestHashes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		blink uint32
		cf    uint64
	}{
		{name: "a", blink: 0x64635e, cf: 0x426},
		{name: "ab", blink: 0x6ecb20, cf: 0xbfcd9},
		{name: "abc", blink: 0x5aa933, cf: 0x1ec49f71},
		{name: "X-Upper", blink: 0x51a9bf, cf: 0x4c59b18eda8a39d8},
		{name: "user-agent", blink: 0xc21178, cf: 0xc8e7a8d4a8ed7205},
		{name: "sec-ch-ua-platform", blink: 0xffbec3, cf: 0x7dc269684cd988da},
		{name: "accept-encoding", blink: 0x8da457, cf: 0x5eb08a2a1f5c00f3},
		{name: "x-a-very-long-header-name-for-hashing-purposes-0123456789", blink: 0x7cd5e1, cf: 0xb5388df7672d2dde},
	} {
		if got := blinkHash(tc.name); got != tc.blink {
			t.Errorf("blinkHash(%q) = %#x, want %#x", tc.name, got, tc.blink)
		}
		if got := cfHash(tc.name); got != tc.cf {
			t.Errorf("cfHash(%q) = %#x, want %#x", tc.name, got, tc.cf)
		}
	}
}

var letters = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}

// TableOrder puts the page's headers where the browsers' captures do
// (headers-order, every sample alike): Chrome's Blink table, the page's
// first; Safari's CFNetwork table, the page's after its own as the list
// lays them out, and content-length after both (cors, maxage.cs PUT).
func TestTableOrderMatchesCaptures(t *testing.T) {
	chrome := &profile.HeaderTable{
		Hash:      profile.HashBlink,
		Own:       []string{"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "sec-ch-ua-mobile"},
		PageFirst: true,
		AsPage:    []string{"content-type"},
	}
	safari := &profile.HeaderTable{
		Hash: profile.HashCF,
		Own:  []string{"sec-fetch-dest", "user-agent", "accept", "referer", "sec-fetch-site", "sec-fetch-mode", "origin", "content-type"},
		Late: []string{"content-length"},
	}
	chromeOwn := []string{"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "sec-ch-ua-mobile"}
	safariSO := []string{"sec-fetch-dest", "user-agent", "accept", "referer", "sec-fetch-site", "sec-fetch-mode"}
	safariCS := []string{"sec-fetch-site", "accept", "origin", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest"}
	around := []string{"0", "a", "accept-a", "accept-f", "accept-m", "b", "cache-controlz", "co", "connectionz", "content-m", "content-z", "cookiez", "h", "hostz", "o", "originz", "p", "pragmaz", "priorityz", "r", "refererz", "s", "sec", "secz", "t", "tez", "u", "user-agentz", "v", "x", "zzz"}
	aroundRev := slices.Clone(around)
	slices.Reverse(aroundRev)
	for _, tc := range []struct {
		name    string
		table   *profile.HeaderTable
		members []string
		page    []string
		want    []string
	}{
		{
			name:    "chrome letters.fetch.so",
			table:   chrome,
			members: chromeOwn,
			page:    letters,
			want:    []string{"sec-ch-ua-platform", "j", "o", "e", "d", "k", "b", "sec-ch-ua", "p", "sec-ch-ua-mobile", "f", "v", "u", "z", "w", "a", "q", "l", "r", "s", "c", "x", "y", "i", "m", "n", "g", "user-agent", "t", "h"},
		},
		{
			name:    "chrome around.fetch.so",
			table:   chrome,
			members: chromeOwn,
			page:    around,
			want:    []string{"content-m", "hostz", "o", "p", "sec-ch-ua", "sec-ch-ua-mobile", "v", "originz", "pragmaz", "accept-f", "content-z", "r", "0", "s", "co", "connectionz", "accept-a", "secz", "t", "user-agentz", "sec-ch-ua-platform", "b", "zzz", "u", "sec", "a", "refererz", "x", "tez", "priorityz", "cache-controlz", "cookiez", "user-agent", "accept-m", "h"},
		},
		{
			name:    "chrome pair-ab.fetch.so",
			table:   chrome,
			members: chromeOwn,
			page:    []string{"x-a", "x-b"},
			want:    []string{"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "x-a", "sec-ch-ua-mobile", "x-b"},
		},
		{
			// XHR: the page's order, which shows where names collide.
			name:    "chrome around-rev.xhr.so",
			table:   chrome,
			members: chromeOwn,
			page:    aroundRev,
			want:    []string{"content-m", "hostz", "o", "p", "sec-ch-ua", "sec-ch-ua-mobile", "v", "originz", "pragmaz", "accept-f", "s", "r", "0", "content-z", "co", "connectionz", "secz", "accept-a", "t", "user-agentz", "sec-ch-ua-platform", "b", "zzz", "u", "sec", "a", "refererz", "x", "tez", "priorityz", "cookiez", "cache-controlz", "user-agent", "accept-m", "h"},
		},
		{
			name:    "chrome case.fetch.cs",
			table:   chrome,
			members: chromeOwn,
			page:    []string{"x-dup", "x-lower", "x-mixed", "x-upper"},
			want:    []string{"sec-ch-ua-platform", "sec-ch-ua", "x-lower", "sec-ch-ua-mobile", "x-mixed", "user-agent", "x-dup", "x-upper"},
		},
		{
			// kinds, fetch-json.so: the page's content-type, no other.
			name:    "chrome content-type",
			table:   chrome,
			members: []string{"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "content-type", "sec-ch-ua-mobile"},
			want:    []string{"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "content-type", "sec-ch-ua-mobile"},
		},
		{
			name:    "safari letters.fetch.so",
			table:   safari,
			members: safariSO,
			page:    letters,
			want:    []string{"sec-fetch-mode", "referer", "c", "p", "user-agent", "d", "q", "r", "e", "s", "f", "t", "g", "sec-fetch-dest", "u", "h", "v", "i", "sec-fetch-site", "w", "j", "x", "k", "y", "l", "z", "m", "accept", "n", "a", "o", "b"},
		},
		{
			name:    "safari single-a.fetch.so",
			table:   safari,
			members: safariSO,
			page:    []string{"a"},
			want:    []string{"accept", "sec-fetch-site", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest", "a"},
		},
		{
			name:    "safari around.fetch.cs",
			table:   safari,
			members: safariCS,
			page:    around,
			want:    []string{"accept-f", "b", "priorityz", "refererz", "accept", "content-z", "tez", "user-agentz", "content-m", "user-agent", "secz", "h", "co", "sec-fetch-site", "pragmaz", "accept-m", "hostz", "cache-controlz", "referer", "o", "origin", "originz", "sec-fetch-mode", "p", "connectionz", "zzz", "r", "s", "cookiez", "t", "sec", "u", "v", "sec-fetch-dest", "0", "accept-a", "x", "a"},
		},
		{
			name:    "safari case.fetch.cs",
			table:   safari,
			members: safariCS,
			page:    []string{"x-dup", "x-lower", "x-mixed", "x-upper"},
			want:    []string{"sec-fetch-site", "accept", "origin", "x-dup", "x-mixed", "x-upper", "x-lower", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest"},
		},
		{
			name:    "safari around-rev.xhr.so",
			table:   safari,
			members: safariSO,
			page:    aroundRev,
			want:    []string{"b", "accept-f", "priorityz", "refererz", "tez", "user-agentz", "content-z", "accept", "content-m", "user-agent", "secz", "co", "h", "sec-fetch-site", "pragmaz", "accept-m", "hostz", "cache-controlz", "referer", "o", "p", "originz", "connectionz", "sec-fetch-mode", "zzz", "r", "s", "cookiez", "t", "sec", "u", "v", "sec-fetch-dest", "0", "accept-a", "x", "a"},
		},
		{
			name:    "safari maxage.cs PUT",
			table:   safari,
			members: []string{"sec-fetch-site", "accept", "origin", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest", "content-length"},
			page:    []string{"x-mimicry"},
			want:    []string{"sec-fetch-site", "accept", "origin", "content-length", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest", "x-mimicry"},
		},
		{
			name:    "safari without page headers keeps the list",
			table:   safari,
			members: []string{"accept", "content-type", "origin", "sec-fetch-site", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest", "content-length"},
			want:    []string{"accept", "content-type", "origin", "sec-fetch-site", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest", "content-length"},
		},
	} {
		got, ok := TableOrder(tc.table, tc.members, tc.page)
		if !ok || !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q (ok %v)\nwant %q", tc.name, got, ok, tc.want)
		}
	}
}

// A list order the table cannot hold, and more names than it models, are
// refused rather than guessed.
func TestTableOrderRefuses(t *testing.T) {
	safari := &profile.HeaderTable{
		Hash: profile.HashCF,
		Own:  []string{"accept", "sec-fetch-site", "origin"},
	}
	// accept, sec-fetch-site and origin share a bucket in 13: the first
	// one placed takes it.
	if got, ok := TableOrder(safari, []string{"sec-fetch-site", "accept", "origin", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest"}, nil); !ok {
		t.Errorf("a list the table holds refused: %q", got)
	}
	if got, ok := TableOrder(safari, []string{"origin", "accept", "sec-fetch-site", "a"}, nil); ok {
		t.Errorf("a list the table cannot hold gave %q", got)
	}
	many := make([]string, 0, 2000)
	for i := range 2000 {
		many = append(many, "x-"+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676)))
	}
	if got, ok := TableOrder(safari, nil, many); ok {
		t.Errorf("%d page headers gave %d names", len(many), len(got))
	}
}
