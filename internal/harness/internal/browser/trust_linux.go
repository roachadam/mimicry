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
// profile for this run, so nothing outside the run is changed: Chrome and
// Edge read their roots from $HOME/.pki/nssdb and run with HOME set to dir
// (browserEnv); Firefox from its profile's own database, only when it
// checks a certificate from the CA (Target.trustsCA), as elsewhere it reads
// the system roots only then (firefoxProfile). Both need NSS's
// certutil (libnss3-tools).
func trustInProfile(ctx context.Context, b *Browser, dir string, t Target) error {
	if t.CAFile == "" {
		return nil
	}

	var db string

	switch {
	case chromium(b.Name):
		db = filepath.Join(dir, ".pki", "nssdb")
	case b.Name == "firefox" && t.trustsCA():
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
// Chromium's HOME is dir, so its certificate database is the run's own
// (trustInProfile), and its XAUTHORITY the real home's .Xauthority unless
// set, so a display that wants its cookie (ssh -X, a desktop session)
// still lets it in; nil keeps the tool's.
func browserEnv(b *Browser, dir string) []string {
	if !chromium(b.Name) {
		return nil
	}

	env := append(os.Environ(), "HOME="+dir)

	if os.Getenv("XAUTHORITY") == "" {
		if home, err := os.UserHomeDir(); err == nil {
			xauth := filepath.Join(home, ".Xauthority")
			if _, err := os.Stat(xauth); err == nil {
				env = append(env, "XAUTHORITY="+xauth)
			}
		}
	}

	return env
}
