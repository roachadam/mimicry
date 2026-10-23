package headerorder

import (
	"slices"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

func TestCookies(t *testing.T) {
	// The server-set jar of the set-cookie capture, in creation order.
	jar := "sid=1; bid=2; geoloc=3; aka_a2=A; _abck=4; ni_d=5; p19=6;; p20=7 ;p21=8"
	tests := []struct {
		order string
		want  []string
	}{
		{
			// Chrome and Firefox keep creation order.
			order: profile.CookieOrderCreation,
			want:  []string{"sid=1", "bid=2", "geoloc=3", "aka_a2=A", "_abck=4", "ni_d=5", "p19=6", "p20=7", "p21=8"},
		},
		{
			// Safari's order in the same capture.
			order: profile.CookieOrderName,
			want:  []string{"_abck=4", "aka_a2=A", "bid=2", "geoloc=3", "ni_d=5", "p19=6", "p20=7", "p21=8", "sid=1"},
		},
	}
	for _, tt := range tests {
		if got := Cookies(jar, tt.order); !slices.Equal(got, tt.want) {
			t.Errorf("order %q:\n got %v\nwant %v", tt.order, got, tt.want)
		}
	}
	// Same names keep their relative order when sorted (sort is stable).
	if got := Cookies("b=2; a=1; a=0", profile.CookieOrderName); !slices.Equal(got, []string{"a=1", "a=0", "b=2"}) {
		t.Errorf("duplicate names reordered: %v", got)
	}
}
