package diff

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/profile"
)

// Difference is one place where got does not match want.
type Difference struct {
	// Path locates the value, for example "tls.extensions[0x0010]" or
	// "http2.settings".
	Path string
	Want string
	Got  string
}

func (d Difference) String() string {
	return fmt.Sprintf("%s:\n  want %s\n   got %s", d.Path, d.Want, d.Got)
}

type differ struct {
	diffs []Difference
}

func (d *differ) check(path string, want, got any) {
	w, g := render(want), render(got)
	if w != g {
		d.diffs = append(d.diffs, Difference{
			Path: path,
			Want: w,
			Got:  g,
		})
	}
}

func render(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// Compare reports every difference between the parts of want and got that a
// capture of one connection records: the ClientHello (tls, without
// resumption), the HTTP/2 preface (settings, window update, priorities,
// pseudo-header order, first stream) and the first request's headers. The
// rest of a profile (http1, kinds, variants, context, cookies, proxy, hpack,
// flow control) is derived from page traces and not compared. Extension
// order is compared as want.TLS.ExtensionOrder says: exactly when fixed, and
// only for pinned extensions when shuffled. Profile identity (name, browser,
// version, platform) is not compared; the user-agent header covers it.
func Compare(want, got *profile.Profile) []Difference {
	d := &differ{}
	d.compareTLS(&want.TLS, &got.TLS)
	d.compareHTTP2(want.HTTP2, got.HTTP2)
	d.compareHeaders(want.Headers, got.Headers)
	return d.diffs
}

func (d *differ) compareTLS(want, got *profile.TLS) {
	d.check("tls.cipher_suites", want.CipherSuites, got.CipherSuites)
	d.check("tls.compression_methods", []uint8(want.CompressionMethods), []uint8(got.CompressionMethods))
	d.check("tls.extension_order", want.ExtensionOrder, got.ExtensionOrder)

	if want.ExtensionOrder == profile.ExtensionOrderShuffled {
		d.check("tls.extensions (pinned positions)", pinned(want.Extensions), pinned(got.Extensions))
	} else {
		d.check("tls.extensions (order)", types(want.Extensions), types(got.Extensions))
	}

	// Contents, matched by type. GREASE may appear more than once; its
	// occurrences are matched in order.
	wantByType, gotByType := byType(want.Extensions), byType(got.Extensions)
	var all []profile.Code
	for _, e := range want.Extensions {
		if !slices.Contains(all, e.Type) {
			all = append(all, e.Type)
		}
	}
	for _, e := range got.Extensions {
		if !slices.Contains(all, e.Type) {
			all = append(all, e.Type)
		}
	}
	for _, typ := range all {
		w, g := wantByType[typ], gotByType[typ]
		for i := range max(len(w), len(g)) {
			path := "tls.extensions[" + typ.String() + "]"
			if len(w) > 1 || len(g) > 1 {
				path += fmt.Sprintf("#%d", i+1)
			}
			d.check(path, extensionOrMissing(w, i), extensionOrMissing(g, i))
		}
	}
}

// pinned lists the pinned extensions with their positions, e.g. "0:GREASE".
func pinned(exts []profile.Extension) []string {
	var out []string
	for i := range exts {
		if exts[i].Pinned() {
			out = append(out, fmt.Sprintf("%d:%s", i, exts[i].Type))
		}
	}
	return out
}

func types(exts []profile.Extension) []string {
	out := make([]string, len(exts))
	for i, e := range exts {
		out[i] = e.Type.String()
	}
	return out
}

func byType(exts []profile.Extension) map[profile.Code][]profile.Extension {
	m := map[profile.Code][]profile.Extension{}
	for _, e := range exts {
		m[e.Type] = append(m[e.Type], e)
	}
	return m
}

func extensionOrMissing(exts []profile.Extension, i int) string {
	if i >= len(exts) {
		return "(missing)"
	}
	return render(exts[i])
}

func (d *differ) compareHTTP2(want, got *profile.HTTP2) {
	if want == nil || got == nil {
		if want != got {
			d.check("http2", presence(want != nil), presence(got != nil))
		}
		return
	}
	d.check("http2.settings", want.Settings, got.Settings)
	d.check("http2.window_update", want.WindowUpdate, got.WindowUpdate)
	d.check("http2.priorities", want.Priorities, got.Priorities)
	d.check("http2.headers_priority", want.HeadersPriority, got.HeadersPriority)
	d.check("http2.pseudo_header_order", strings.Join(want.PseudoHeaderOrder, ","), strings.Join(got.PseudoHeaderOrder, ","))
	d.check("http2.first_stream_id", firstStream(want), firstStream(got))
	d.check("http2.stream_window_update", want.StreamWindowUpdate, got.StreamWindowUpdate)
}

// firstStream is the first request's stream ID; zero means the default 1.
func firstStream(h *profile.HTTP2) uint32 {
	if h.FirstStreamID == 0 {
		return 1
	}
	return h.FirstStreamID
}

func presence(present bool) string {
	if present {
		return "(present)"
	}
	return "(missing)"
}

func (d *differ) compareHeaders(want, got []profile.Header) {
	names := func(hs []profile.Header) string {
		out := make([]string, len(hs))
		for i, h := range hs {
			out[i] = h.Name
		}
		return strings.Join(out, ", ")
	}
	d.check("headers (order)", names(want), names(got))
	// Values are matched by name, and a repeated name's occurrences in order,
	// so a name sent twice is compared value by value.
	values := func(hs []profile.Header) map[string][]string {
		m := make(map[string][]string, len(hs))
		for _, h := range hs {
			m[h.Name] = append(m[h.Name], h.Value)
		}
		return m
	}
	wv, gv := values(want), values(got)
	var all []string
	for _, h := range slices.Concat(want, got) {
		if !slices.Contains(all, h.Name) {
			all = append(all, h.Name)
		}
	}
	for _, name := range all {
		w, g := wv[name], gv[name]
		for i := range max(len(w), len(g)) {
			path := "headers[" + name + "]"
			if len(w) > 1 || len(g) > 1 {
				path += fmt.Sprintf("#%d", i+1)
			}
			d.check(path, valueOrMissing(w, i), valueOrMissing(g, i))
		}
	}
}

func valueOrMissing(values []string, i int) string {
	if i >= len(values) {
		return "(missing)"
	}
	return values[i]
}
