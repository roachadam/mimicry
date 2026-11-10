package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/clientcert"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// clientCertRefusal says why b cannot take a client-certificate scenario
// here, "" when it can: every browser imports the identity into its
// profile with NSS's pk12util, and Chrome, Edge and Brave send it unasked only
// under the managed policy file, which only root may write
// (clientcert.LinuxPolicyFiles; cmd/refresh writes it in WSL).
func clientCertRefusal(b *browser.Browser) string {
	if _, err := exec.LookPath("pk12util"); err != nil {
		return "NSS's pk12util is not on PATH, with which the browser's profile gets the client identity: install libnss3-tools (Debian, Ubuntu) or nss-tools (Fedora)"
	}

	if !capture.Chromium(b.Name) {
		return ""
	}

	path := clientcert.LinuxPolicyFiles[b.Name]
	want := clientcert.LinuxPolicy(server.ClientCertHosts())

	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
		return "without the AutoSelectCertificateForUrls policy " + b.Name + " asks which identity to send; as root, for the run:\n" +
			"  install -D -m 644 /dev/stdin " + path + " <<'EOF'\n" + string(want) + "EOF\n" +
			"and remove it after (cmd/refresh does both in WSL)"
	}

	return ""
}

// installClientCert does nothing: each browser's profile gets the identity
// (browser.Target.ClientCert), and Chrome's and Edge's policy is a file
// root wrote (clientCertRefusal).
func installClientCert(context.Context, *browser.Browser, string, []string) (func(context.Context) error, error) {
	return func(context.Context) error {
		return nil
	}, nil
}
