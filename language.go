package mimicry

import (
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
)

// topPage is the top-level page whose farbled Accept-Language a request
// takes (profile.Context.AcceptLanguageQ): a navigation's own first URL,
// its redirects keeping it, as Brave's do; a frame's and any other
// request's page, from.
func topPage(req *http.Request, r Request, from *url.URL) *url.URL {
	if r.Kind != Navigate {
		return from
	}

	if chain := redirectsBefore(req); len(chain) > 0 {
		return chain[0]
	}

	return req.URL
}

// languageQ is the q value the requests of top-level page top put on
// Accept-Language's last language, "" where the profile farbles none
// (profile.Context.AcceptLanguageQ): drawn on the page origin's first
// request and kept for the Transport's life, as Brave keeps one per origin
// for its session.
func (t *Transport) languageQ(top *url.URL) string {
	ctx := t.profile.Context
	if ctx == nil || len(ctx.AcceptLanguageQ) == 0 || top == nil {
		return ""
	}

	key := origin(top)

	v, ok := t.languageQs.Load(key)
	if !ok {
		qs := ctx.AcceptLanguageQ

		v, _ = t.languageQs.LoadOrStore(key, qs[rand.IntN(len(qs))]) //nolint:gosec // farbling needs no secrecy
	}

	q, _ := v.(string)

	return q
}

// withQ is an Accept-Language value with its last q value replaced by q;
// the value itself when it has none.
func withQ(value, q string) string {
	before, _, ok := strings.CutLast(value, ";q=")
	if !ok {
		return value
	}

	return before + ";q=" + q
}
