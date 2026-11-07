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
	"github.com/roachadam/mimicry/internal/weburl"
	"github.com/roachadam/mimicry/profile"
)

// deriveURLs derives urls from the urls captures (server.URLsCaptures);
// without one it stays nil. Every sample, fetched and navigated, must
// agree.
func deriveURLs(p *profile.Spec, dir string) error {
	p.URLs = nil

	files, err := server.URLsCaptures(dir, p)
	if err != nil {
		return err
	}

	var (
		got  *profile.URLs
		from string
	)

	for _, f := range files {
		pages, err := loadFile(f)
		if err != nil {
			return err
		}

		for i, page := range pages {
			at := fmt.Sprintf("%s sample %d", filepath.Base(f.Path), i)

			u, err := urlsOf(page)
			if err != nil {
				return fmt.Errorf("%s: %w", at, err)
			}

			if got != nil && *u != *got {
				return fmt.Errorf("%s (%+v) and %s (%+v) disagree", at, *u, from, *got)
			}

			got, from = u, at
		}
	}

	p.URLs = got

	return nil
}

// urlsOf reads one urls sample: the characters its browser encodes in a
// path beyond weburl.InPathSet (the "p-" cases), whether it refused the
// malformed punycode host ("h-badpuny"), and the server_name it sent for
// a host ending in a dot ("h-dot"). Its fetches and navigations must
// agree, and a character the set encodes must be encoded.
func urlsOf(page *capture.Page) (*profile.URLs, error) {
	targets := urlsTargets(page)

	var (
		u    profile.URLs
		errs []error
	)

	for _, ch := range server.URLChars() {
		if len(ch.Char) != 1 || ch.Char[0] <= ' ' || ch.Char[0] > '~' || strings.ContainsAny(ch.Char, `%/\`) {
			continue
		}

		c := ch.Char[0]
		encoded := fmt.Sprintf("a%%%02Xb", c)

		var sent []string

		for _, kind := range []string{"f", "n"} {
			t, ok := targets[server.URLCasePrefix(kind, "p-"+ch.ID)]
			if !ok {
				errs = append(errs, fmt.Errorf("p-%s: no %s request", ch.ID, kind))
				continue
			}

			sent = append(sent, t)
		}

		if len(sent) != 2 {
			continue
		}

		switch {
		case sent[0] != sent[1]:
			errs = append(errs, fmt.Errorf("p-%s: fetched as %q, navigated to as %q", ch.ID, sent[0], sent[1]))
		case strings.EqualFold(sent[0], encoded) && !weburl.InPathSet(c):
			u.PathEncodes += ch.Char
		case sent[0] == "a"+ch.Char+"b" && weburl.InPathSet(c):
			errs = append(errs, fmt.Errorf("p-%s: %q sent as written, which every other browser measured encodes", ch.ID, ch.Char))
		case !strings.EqualFold(sent[0], encoded) && sent[0] != "a"+ch.Char+"b":
			errs = append(errs, fmt.Errorf("p-%s: sent as %q", ch.ID, sent[0]))
		}
	}

	_, fetched := targets[server.URLCasePrefix("f", "h-badpuny")]
	_, navigated := targets[server.URLCasePrefix("n", "h-badpuny")]

	if fetched != navigated {
		errs = append(errs, fmt.Errorf("h-badpuny: fetched %t, navigated to %t", fetched, navigated))
	}

	u.CheckPunycode = !fetched

	sni, err := trailingDotSNI(page)
	if err != nil {
		errs = append(errs, err)
	}

	u.TrailingDotSNI = sni

	return &u, errors.Join(errs...)
}

// trailingDotSNI reads the "h-dot" case, fetched and navigated to:
// profile.TrailingDotKeep when the capture server refused a ClientHello
// naming the host with its dot (Go's crypto/tls refuses one), or
// TrailingDotOmit when the request came on a TLS connection with no
// server_name. Anything else (the dot stripped, nothing sent) is an error:
// no rule describes it.
func trailingDotSNI(page *capture.Page) (string, error) {
	const id = "h-dot"

	i := slices.IndexFunc(server.URLCases(), func(c server.URLCase) bool {
		return c.ID == id
	})
	if i < 0 {
		return "", fmt.Errorf("%s: no such urls case", id)
	}

	u, err := url.Parse(server.URLCases()[i].On("f", "1", "2"))
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
		return "", fmt.Errorf("%s: fetched %s, navigated to %s", id, got[0], got[1])
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

// urlsTargets maps each case prefix ("/c/f-p-bar/") the page's requests
// start with to the rest of its first request's target, over any
// protocol.
func urlsTargets(page *capture.Page) map[string]string {
	var paths []string

	for _, tr := range page.H2 {
		for _, r := range tr.Requests {
			paths = append(paths, r.Path)
		}
	}

	for _, r := range page.H1 {
		_, rest, _ := strings.Cut(r.Line, " ")
		target, _, _ := strings.Cut(rest, " ")

		paths = append(paths, target)
	}

	for _, qc := range page.QUIC {
		if qc.H3 == nil {
			continue
		}

		for _, r := range qc.H3.Requests {
			paths = append(paths, r.Path)
		}
	}

	out := map[string]string{}

	for _, path := range paths {
		rest, ok := strings.CutPrefix(path, "/c/")
		if !ok {
			continue
		}

		key, target, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}

		prefix := "/c/" + key + "/"
		if _, seen := out[prefix]; !seen {
			out[prefix] = target
		}
	}

	return out
}
