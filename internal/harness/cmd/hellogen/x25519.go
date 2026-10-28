package main

import (
	"errors"
	"fmt"
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// checkX25519Reuse checks tls.x25519Reuse against every ClientHello in
// p's page captures that kept its bytes and sends both key shares: resumed
// TCP hellos and QUIC hellos, full and resumed.
func checkX25519Reuse(dir string, p *profile.Profile) error {
	files, err := capture.Files(dir, p, ".pagetrace.json.gz")
	if err != nil {
		return err
	}

	var errs []error

	check := func(at string, hello []byte) {
		t, err := server.ParseClientHello(hello)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", at, err))

			return
		}

		ks := extension(&t, profile.ExtKeyShare)
		if ks == nil || !slices.Contains(ks.KeyShares, 0x11ec) || !slices.Contains(ks.KeyShares, 0x001d) {
			return
		}

		if t.X25519Reuse != p.TLS.X25519Reuse {
			errs = append(errs, fmt.Errorf("%s: reuses the X25519 key across key shares: %v, the full TCP hellos: %v", at, t.X25519Reuse, p.TLS.X25519Reuse))
		}
	}

	for _, f := range files {
		pages, err := capture.LoadPages(f.Path)
		if err != nil {
			return err
		}

		for i, page := range pages {
			for j, c := range page.Connections {
				if b := c.ClientHelloBytes(); b != nil {
					check(fmt.Sprintf("%s: sample %d, connection %d", f.Path, i+1, j+1), b)
				}
			}

			for j, c := range page.QUIC {
				if b := c.ClientHelloBytes(); b != nil {
					check(fmt.Sprintf("%s: sample %d, QUIC connection %d", f.Path, i+1, j+1), b)
				}
			}
		}
	}

	return errors.Join(errs...)
}
