package main

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// trailingDotSNI derives tls.trailingDotSni from p's urls captures
// (server.URLsCaptures), or, without one, its testing-build capture
// (capture.TestingPages: the server_name is the network stack's, which a
// testing build shares with the brand): the server_name its browser sent
// for the "h-dot" case's host, which ends in a dot, in every sample,
// fetched and navigated to. Empty without a capture.
func trailingDotSNI(dir string, p *profile.Spec) (string, error) {
	files, err := server.URLsCaptures(dir, p)
	if err != nil {
		return "", err
	}

	if len(files) == 0 {
		path, err := capture.TestingPages(dir, p, server.URLs.Name, "")
		if err != nil || path == "" {
			return "", err
		}

		files = []capture.File{{Path: path, Scenario: server.URLs.Name}}
	}

	var got, from string

	for _, f := range files {
		pages, err := capture.LoadPages(f.Path)
		if err != nil {
			return "", err
		}

		for i, page := range pages {
			at := fmt.Sprintf("%s sample %d", filepath.Base(f.Path), i+1)

			sni, err := pageTrailingDotSNI(page)
			if err != nil {
				return "", fmt.Errorf("%s: %w", at, err)
			}

			if got != "" && sni != got {
				return "", fmt.Errorf("%s (%s) and %s (%s) disagree", at, sni, from, got)
			}

			got, from = sni, at
		}
	}

	return got, nil
}

// pageTrailingDotSNI reads one urls sample's "h-dot" case, fetched and
// navigated to: profile.TrailingDotKeep when the capture server refused a
// ClientHello naming the host with its dot (Go's crypto/tls refuses one),
// or TrailingDotOmit when the request came on a TLS connection with no
// server_name. Anything else (the dot stripped, nothing sent) is an
// error: no rule describes it.
func pageTrailingDotSNI(page *capture.Page) (string, error) {
	const id = "h-dot"

	cases := server.URLCases()

	i := slices.IndexFunc(cases, func(c server.URLCase) bool {
		return c.ID == id
	})
	if i < 0 {
		return "", fmt.Errorf("%s: no such urls case", id)
	}

	u, err := url.Parse(cases[i].On("f", "1", "2"))
	if err != nil {
		return "", fmt.Errorf("%s: %w", id, err)
	}

	host := strings.ToLower(u.Hostname())
	refused := slices.ContainsFunc(page.Connections, func(c capture.Connection) bool {
		return c.Refused != "" && c.Host == host
	})
	snis := urlsServerNames(page)

	var got []string

	for _, kind := range []string{"f", "n"} {
		sni, arrived := snis[server.URLCasePrefix(kind, id)]

		switch {
		case arrived && sni == "":
			got = append(got, profile.TrailingDotOmit)
		case arrived:
			return "", fmt.Errorf("%s: %s sent server_name %q for %s", id, kind, sni, host)
		case refused:
			got = append(got, profile.TrailingDotKeep)
		default:
			return "", fmt.Errorf("%s: %s sent nothing, and no ClientHello naming %s was refused", id, kind, host)
		}
	}

	if got[0] != got[1] {
		return "", errors.New(id + ": fetched " + got[0] + ", navigated to " + got[1])
	}

	return got[0], nil
}

// urlsServerNames maps each case prefix the page's TLS requests start with
// to the server_name of the connection its first request came on ("" for
// none).
func urlsServerNames(page *capture.Page) map[string]string {
	out := map[string]string{}
	add := func(path, sni string) {
		rest, ok := strings.CutPrefix(path, "/c/")
		if !ok {
			return
		}

		key, _, ok := strings.Cut(rest, "/")
		if !ok {
			return
		}

		if _, seen := out["/c/"+key+"/"]; !seen {
			out["/c/"+key+"/"] = sni
		}
	}

	for _, tr := range page.H2 {
		for _, r := range tr.Requests {
			add(r.Path, tr.Host)
		}
	}

	for _, r := range page.H1 {
		_, rest, _ := strings.Cut(r.Line, " ")
		target, _, _ := strings.Cut(rest, " ")

		add(target, r.Host)
	}

	return out
}
