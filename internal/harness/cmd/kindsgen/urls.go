package main

import (
	"errors"
	"fmt"
	"path/filepath"
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
// path beyond weburl.InPathSet (the "p-" cases), and whether it refused the
// malformed punycode host ("h-badpuny"). Its fetches and navigations must
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

	return &u, errors.Join(errs...)
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
