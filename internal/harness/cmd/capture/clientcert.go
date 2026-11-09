package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/clientcert"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// A client-certificate scenario (server.Scenario.ClientCert) has the
// browser hold one identity from the run's test client CA, which the
// capture server names in its CertificateRequest. Where the browser reads
// its identities from a store outside its throwaway profile, the tool puts
// the identity there for the scenario and takes it out after
// (installClientCert, per platform): Chrome and Edge on macOS a temporary
// keychain in the user's search list, on Windows the user's certificate
// store, as Firefox on Windows; with, for Chrome and Edge, the
// AutoSelectCertificateForUrls policy entries that have them send it
// without asking. Firefox elsewhere, and Chrome and Edge on Linux, import
// it into the profile (browser.Target.ClientCert); Chrome and Edge on
// Linux read the policy from a root-owned file (clientcert.LinuxPolicyFiles)
// that cmd/refresh writes in WSL. -cleanup removes whatever a killed run
// left, finding it by the client CA's name.

// clientCerts is a run's test client identities and their PKCS#12 files.
type clientCerts struct {
	set *clientcert.Set
	// dir holds the files, files[key type] each identity's.
	dir   string
	files map[string]string
}

// newClientCerts makes the identities when a scenario of scenarios asks
// for one; nil otherwise.
func newClientCerts(scenarios []*server.Scenario) (*clientCerts, error) {
	if !slices.ContainsFunc(scenarios, func(sc *server.Scenario) bool {
		return sc.ClientCert() != ""
	}) {
		return nil, nil //nolint:nilnil // nil: no scenario needs them
	}

	set, err := clientcert.New(time.Now())
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "mimicry-client-cert-")
	if err != nil {
		return nil, err
	}

	c := &clientCerts{
		set:   set,
		dir:   dir,
		files: map[string]string{},
	}

	for _, key := range []string{server.ClientKeyEC, server.ClientKeyRSA} {
		path := filepath.Join(dir, key+".p12")
		if err := os.WriteFile(path, set.Identity(key).P12, 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}

		c.files[key] = path
	}

	return c, nil
}

// close removes the PKCS#12 files.
func (c *clientCerts) close() {
	if c != nil {
		_ = os.RemoveAll(c.dir)
	}
}

// target is the identity a browser holds for sc, nil when sc asks for
// none.
func (c *clientCerts) target(sc *server.Scenario) *browser.ClientIdentity {
	if c == nil || sc.ClientCert() == "" {
		return nil
	}

	return &browser.ClientIdentity{
		P12:      c.files[sc.ClientCert()],
		Password: clientcert.Password,
	}
}

// install puts sc's identity where b reads it for the scenario and
// returns what takes it out again, which also runs on failure; a no-op
// for a scenario asking for no client certificate.
func (c *clientCerts) install(ctx context.Context, b *browser.Browser, sc *server.Scenario) (func(), error) {
	if c == nil || sc.ClientCert() == "" {
		return func() {}, nil
	}

	remove, err := installClientCert(ctx, b, c.files[sc.ClientCert()], server.ClientCertHosts())
	if err != nil {
		return nil, fmt.Errorf("giving %s the %s client identity: %w", b.Name, sc.ClientCert(), err)
	}

	return func() {
		if err := remove(context.WithoutCancel(ctx)); err != nil {
			log.Printf("%s: taking the client identity out again: %v; capture -cleanup removes it", b.Name, err)
		}
	}, nil
}
