package profile

import (
	"regexp"
	"slices"
	"strings"
)

// qValue is a q value as Accept-Language writes one (RFC 9110, section
// 12.4.2), below 1.
var qValue = regexp.MustCompile(`^0\.[0-9]{1,3}$`)

// checkAcceptLanguageQ holds Context.AcceptLanguageQ to q values, sorted
// and distinct, for a navigation whose Accept-Language ends in one.
func (c *checker) checkAcceptLanguageQ(p *Spec) {
	if p.Context == nil || len(p.Context.AcceptLanguageQ) == 0 {
		return
	}

	qs := p.Context.AcceptLanguageQ
	for _, q := range qs {
		if !qValue.MatchString(q) {
			c.add("context.acceptLanguageQ", "%q is not a q value below 1", q)
		}
	}

	if !slices.IsSorted(qs) || len(slices.Compact(slices.Clone(qs))) != len(qs) {
		c.add("context.acceptLanguageQ", "must be sorted, each once")
	}

	i := slices.IndexFunc(p.Headers, func(h Header) bool {
		return strings.EqualFold(h.Name, "accept-language")
	})
	if i < 0 || !strings.Contains(p.Headers[i].Value, ";q=") {
		c.add("context.acceptLanguageQ", "the navigation's accept-language has no q value to replace")
	}
}
