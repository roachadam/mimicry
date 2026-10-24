package headerorder

import (
	"strings"
)

// Crumbs splits a Cookie header value into its cookies ("name=value"), in
// the order given: the jar has already put them in the browser's order, and
// a caller's own header is sent as written. Empty pieces are dropped.
func Crumbs(value string) []string {
	var out []string
	for crumb := range strings.SplitSeq(value, ";") {
		if crumb = strings.TrimSpace(crumb); crumb != "" {
			out = append(out, crumb)
		}
	}
	return out
}
