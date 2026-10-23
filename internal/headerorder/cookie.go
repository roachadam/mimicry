package headerorder

import (
	"slices"
	"strings"

	"github.com/roachadam/mimicry/profile"
)

// Cookies splits a Cookie header value into its cookies ("name=value"), in
// the order the profile sends them: as given (creation order, which Go's
// cookie jar produces as Chrome and Firefox do) or sorted by name, as Safari
// does. Empty pieces are dropped.
func Cookies(value, order string) []string {
	var out []string
	for crumb := range strings.SplitSeq(value, ";") {
		if crumb = strings.TrimSpace(crumb); crumb != "" {
			out = append(out, crumb)
		}
	}
	if order == profile.CookieOrderName {
		slices.SortStableFunc(out, func(a, b string) int {
			return strings.Compare(cookieName(a), cookieName(b))
		})
	}
	return out
}

func cookieName(crumb string) string {
	name, _, _ := strings.Cut(crumb, "=")
	return name
}
