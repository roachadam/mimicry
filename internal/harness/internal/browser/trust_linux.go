package browser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// caNickname names the capture CA in a browser's certificate database.
const caNickname = "mimicry capture CA"

// trustInProfile has b trust the capture CA (t.CAFile) through dir, its
// profile for this run, so nothing outside the run is changed: Chrome
// reads its roots from $HOME/.pki/nssdb and runs with HOME set to dir
// (browserEnv); Firefox from its profile's own database, only when the
// server gives each host its own certificate (HostCerts), as elsewhere
// it reads the system roots only then (firefoxProfile). Both need NSS's
// certutil (libnss3-tools).
func trustInProfile(ctx context.Context, b *Browser, dir string, t Target) error {
	if t.CAFile == "" {
		return nil
	}

	var db string

	switch {
	case b.Name == "chrome":
		db = filepath.Join(dir, ".pki", "nssdb")
	case b.Name == "firefox" && t.HostCerts:
		db = dir
	default:
		return nil
	}

	if err := os.MkdirAll(db, 0o700); err != nil {
		return err
	}

	for _, args := range [][]string{
		{"-N", "-d", "sql:" + db, "--empty-password"},
		{"-A", "-d", "sql:" + db, "-n", caNickname, "-t", "C,,", "-i", t.CAFile},
	} {
		if out, err := exec.CommandContext(ctx, "certutil", args...).CombinedOutput(); err != nil { //nolint:gosec // fixed arguments; dir is our own
			return fmt.Errorf("%s trusting the capture CA (certutil %s): %w: %s", b.Name, args[0], err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}

// browserEnv is the environment b runs in on profile directory dir:
// Chrome's HOME is dir, so its certificate database is the run's own
// (trustInProfile); nil keeps the tool's.
func browserEnv(b *Browser, dir string) []string {
	if b.Name != "chrome" {
		return nil
	}

	return append(os.Environ(), "HOME="+dir)
}
