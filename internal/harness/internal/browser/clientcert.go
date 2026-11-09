package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ClientIdentity is a client certificate a browser holds (Target.ClientCert).
type ClientIdentity struct {
	// P12 is a PKCS#12 file holding the key, the certificate and its CA;
	// Password opens it.
	P12      string
	Password string
}

// firefoxClientCertPrefs are the prefs with which Firefox answers a
// CertificateRequest from the identities it holds without asking: the
// first matching one ("Select Automatically", the dialog's default
// otherwise). On Windows the identity is in the user's store, which
// Firefox reads through its osclientcerts module; elsewhere it is in the
// profile's own database, and the module (on by default on macOS, reading
// the keychain) is off so no identity of the user's is offered.
func firefoxClientCertPrefs() []string {
	return []string{
		`user_pref("security.default_personal_cert", "Select Automatically");`,
		`user_pref("security.osclientcerts.autoload", ` + fmt.Sprint(runtime.GOOS == "windows") + `);`,
	}
}

// importClientCert puts t's client identity into the certificate database
// of b's profile dir, where b reads it from: Firefox's own (but on
// Windows), and on Linux Chrome's and Edge's ($HOME/.pki/nssdb, HOME
// being dir: browserEnv). Elsewhere the capture tool put it in the
// system's store. It needs NSS's certutil and pk12util.
func importClientCert(ctx context.Context, b *Browser, dir string, t Target) error {
	if t.ClientCert == nil {
		return nil
	}

	var db string

	switch {
	case b.Name == "firefox" && runtime.GOOS != "windows":
		db = dir
	case chromium(b.Name) && runtime.GOOS == "linux":
		db = filepath.Join(dir, ".pki", "nssdb")
	default:
		return nil
	}

	if err := os.MkdirAll(db, 0o700); err != nil {
		return err
	}

	// The passphrase goes in a file (-w), never on the command line, where
	// an error message or a process listing would show it.
	pass := filepath.Join(dir, "p12-passphrase")
	if err := os.WriteFile(pass, []byte(t.ClientCert.Password), 0o600); err != nil {
		return err
	}

	defer func() {
		_ = os.Remove(pass)
	}()

	type step struct {
		what string
		args []string
	}

	steps := []step{
		{"pk12util -i", []string{"pk12util", "-i", t.ClientCert.P12, "-d", "sql:" + db, "-w", pass, "-K", ""}},
	}

	if _, err := os.Stat(filepath.Join(db, "cert9.db")); errors.Is(err, os.ErrNotExist) {
		steps = append([]step{{"certutil -N", []string{"certutil", "-N", "-d", "sql:" + db, "--empty-password"}}}, steps...)
	}

	for _, s := range steps {
		if out, err := exec.CommandContext(ctx, s.args[0], s.args[1:]...).CombinedOutput(); err != nil { //nolint:gosec // fixed tools; the files are the run's own
			return fmt.Errorf("%s importing the client identity (%s): %w: %s", b.Name, s.what, err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}
