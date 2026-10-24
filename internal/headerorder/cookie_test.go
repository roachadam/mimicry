package headerorder

import (
	"slices"
	"testing"
)

// A Cookie header splits into its cookies in the order given, spaces inside
// values kept, empty pieces dropped.
func TestCrumbs(t *testing.T) {
	got := Crumbs("sid=1; k_space=a b c;; p20=7 ;knoeq")
	if want := []string{"sid=1", "k_space=a b c", "p20=7", "knoeq"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
