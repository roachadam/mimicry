package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/diff"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// helloRetry derives tls.helloRetry from the hrr capture, where every
// connection to one host is answered with a HelloRetryRequest carrying a
// cookie. Each second ClientHello must be its connection's first with the
// cookie added and one key share, for the group the server asked for;
// anything else that differs is an error. With a fixed extension order
// every cookie must follow the same extension; with a shuffled one it must
// lie among the extensions the shuffle moves. Nil without an hrr capture:
// not measured.
func helloRetry(dir string, p *profile.Spec) (*profile.HelloRetry, error) {
	source, _, err := capture.Inherited(p, server.HRR.Name)
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, source+"."+server.HRR.Name+".pagetrace.json.gz")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) { //nolint:gosec // a capture of the profile named on the command line
		return nil, nil //nolint:nilnil // nil: no capture, not measured
	}

	pages, err := capture.LoadPages(path)
	if err != nil {
		return nil, err
	}

	shuffled := p.TLS.ExtensionOrder == profile.ExtensionOrderShuffled

	var (
		after *profile.Code
		n     int
		errs  []error
	)

	for si, page := range pages {
		for ci, conn := range page.Connections {
			if conn.Retry == nil {
				continue
			}

			at := fmt.Sprintf("%s: sample %d, connection %d", path, si+1, ci+1)

			i, err := retryCookie(conn.Hello, conn.Retry)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", at, err))
				continue
			}

			n++

			exts := conn.Retry.Hello.Extensions
			if shuffled {
				if pinnedRun(exts, i) {
					errs = append(errs, fmt.Errorf("%s: the cookie at %d is among the pinned extensions, which a shuffle does not move", at, i))
				}

				continue
			}

			prev := exts[i-1].Type
			if slices.IndexFunc(exts, func(e profile.Extension) bool { return e.Type == prev }) != i-1 {
				errs = append(errs, fmt.Errorf("%s: the cookie follows a second %s", at, prev))
				continue
			}

			if after != nil && *after != prev {
				errs = append(errs, fmt.Errorf("%s: the cookie follows %s, an earlier one %s", at, prev, *after))
				continue
			}

			after = &prev
		}
	}

	if n == 0 && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("%s: no connection sent a second ClientHello", path))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &profile.HelloRetry{
		CookieAfter:    after,
		CookieShuffled: shuffled,
	}, nil
}

// retryCookie checks r's hello against first, the connection's first, and
// returns the cookie's index in it.
func retryCookie(first *profile.TLS, r *capture.Retry) (int, error) {
	if first == nil || r.Hello == nil {
		return 0, errors.New("its hellos' shapes were not recorded")
	}

	if extension(first, profile.ExtCookie) != nil {
		return 0, errors.New("the first ClientHello carries a cookie")
	}

	second := r.Hello

	i := slices.IndexFunc(second.Extensions, func(e profile.Extension) bool {
		return e.Type == profile.ExtCookie
	})
	if i <= 0 {
		return 0, fmt.Errorf("the second ClientHello's cookie is at %d", i)
	}

	// The first hello, with the cookie inserted and the one key share the
	// HelloRetryRequest asked for.
	want := *first

	want.X25519Reuse = false
	want.Extensions = slices.Clone(first.Extensions)

	for j := range want.Extensions {
		if want.Extensions[j].Type == profile.ExtKeyShare {
			ks := extension(second, profile.ExtKeyShare)
			if ks == nil {
				return 0, errors.New("the second ClientHello has no key_share")
			}

			want.Extensions[j].KeyShares = []profile.Code{
				r.Group,
			}
			want.Extensions[j].KeyShareLengths = ks.KeyShareLengths
		}
	}

	want.Extensions = slices.Insert(want.Extensions, i, second.Extensions[i])

	got := *second

	got.X25519Reuse = false

	if diffs := diff.Compare(&profile.Spec{TLS: want}, &profile.Spec{TLS: got}); len(diffs) > 0 {
		lines := make([]string, len(diffs))
		for k, d := range diffs {
			lines[k] = d.String()
		}

		return 0, fmt.Errorf("the second ClientHello is not the first with a cookie and a key share for %s:\n%s", r.Group, strings.Join(lines, "\n"))
	}

	return i, nil
}

// pinnedRun reports whether index i of exts lies in the leading or
// trailing run of pinned extensions, which a shuffle leaves in place.
func pinnedRun(exts []profile.Extension, i int) bool {
	lead := 0
	for lead < len(exts) && exts[lead].Pinned() {
		lead++
	}

	trail := len(exts)
	for trail > lead && exts[trail-1].Pinned() {
		trail--
	}

	return i < lead || i >= trail
}
