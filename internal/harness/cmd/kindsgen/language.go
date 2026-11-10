package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

// acceptLanguageQ derives profile.Context.AcceptLanguageQ for a browser
// that farbles its Accept-Language (capture.Farbles: Brave): the q values
// its last language took on every request of every capture p is checked
// against, as sent, sorted, but on CORS preflights, which must all carry
// capture.UnfarbledQ (the value its lists carry). Nil for other browsers.
func acceptLanguageQ(dir string, p *profile.Spec) ([]string, error) {
	if !capture.Farbles(p.Browser) {
		return nil, nil
	}

	var (
		qs   []string
		errs []string
	)

	// note records one request's accept-language: a CORS preflight is an
	// OPTIONS with Access-Control-Request-Method (a page's own OPTIONS
	// fetch is farbled like any other request).
	note := func(at string, preflight bool, value string) {
		q, _, ok := capture.FarbledQ(value)

		switch {
		case !ok:
			errs = append(errs, fmt.Sprintf("%s: accept-language %q has no q value", at, value))
		case preflight && q != capture.UnfarbledQ:
			errs = append(errs, fmt.Sprintf("%s: a CORS preflight's accept-language %q, not q=%s", at, value, capture.UnfarbledQ))
		case !preflight && !slices.Contains(qs, q):
			qs = append(qs, q)
		}
	}

	h2 := func(at string, tr *h2trace.Trace) {
		for _, r := range tr.Requests {
			method, value, acrm := "", "", false

			for _, in := range r.Instructions {
				switch in.Name {
				case ":method":
					method = in.Value
				case "accept-language":
					value = in.Value
				case "access-control-request-method":
					acrm = true
				}
			}

			if value != "" {
				note(at+" "+r.Path, method == http.MethodOptions && acrm, value)
			}
		}
	}

	h1 := func(at string, reqs []capture.H1Request) {
		for _, r := range reqs {
			method, _, _ := strings.Cut(r.Line, " ")
			value, acrm := "", false

			for _, l := range r.Headers {
				name, v, _ := strings.Cut(l, ": ")

				switch strings.ToLower(name) {
				case "accept-language":
					value = v
				case "access-control-request-method":
					acrm = true
				}
			}

			if value != "" {
				note(at+" "+r.Line, method == http.MethodOptions && acrm, value)
			}
		}
	}

	pages, err := capture.Files(dir, p, ".pagetrace.json.gz")
	if err != nil {
		return nil, err
	}

	for _, f := range pages {
		got, err := capture.LoadPages(f.Path)
		if err != nil {
			return nil, err
		}

		for _, page := range got {
			for _, tr := range page.H2 {
				h2(f.Path, tr)
			}

			h1(f.Path, page.H1)

			for _, c := range page.QUIC {
				if c.H3 == nil {
					continue
				}

				for _, r := range c.H3.Requests {
					value, acrm := "", false

					for _, fl := range r.Section.Fields {
						switch fl.Name {
						case "accept-language":
							value = fl.Value
						case "access-control-request-method":
							acrm = true
						}
					}

					if value != "" {
						note(f.Path+" "+r.Path, r.Method == http.MethodOptions && acrm, value)
					}
				}
			}
		}
	}

	traces, err := capture.Files(dir, p, ".h2trace.json")
	if err != nil {
		return nil, err
	}

	for _, f := range traces {
		got, err := h2trace.Load(f.Path)
		if err != nil {
			return nil, err
		}

		for _, tr := range got {
			h2(f.Path, tr)
		}
	}

	h1Traces, err := capture.Files(dir, p, ".h1trace.json")
	if err != nil {
		return nil, err
	}

	for _, f := range h1Traces {
		data, err := capture.ReadTrace(f.Path)
		if err != nil {
			return nil, err
		}

		var samples [][]capture.H1Request
		if err := json.Unmarshal(data, &samples); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}

		for _, s := range samples {
			h1(f.Path, s)
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("context.acceptLanguageQ:\n  %s", strings.Join(errs, "\n  "))
	}

	if len(qs) < 2 {
		return nil, fmt.Errorf("context.acceptLanguageQ: %s farbles its accept-language, but its captures show q %v only", p.Browser, qs)
	}

	slices.Sort(qs)

	return qs, nil
}
