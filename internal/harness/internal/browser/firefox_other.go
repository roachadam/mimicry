//go:build !windows

package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// firefoxProfile writes prefs that map the capture hosts to 127.0.0.1 and
// silence first-run pages, plus a certificate exception for the server (or,
// with HostCerts, trust in the system roots).
func firefoxProfile(dir string, t Target, headless bool) ([]string, error) {
	prefs := []string{
		`user_pref("network.dns.localDomains", "` + strings.Join(append([]string{t.Host}, t.Hosts...), ",") + `");`,
		`user_pref("network.dns.disableIPv6", true);`,
		`user_pref("browser.shell.checkDefaultBrowser", false);`,
		`user_pref("browser.startup.homepage_override.mstone", "ignore");`,
		`user_pref("browser.aboutwelcome.enabled", false);`,
		`user_pref("datareporting.policy.dataSubmissionEnabled", false);`,
		`user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);`,
	}
	if t.HostCerts {
		prefs = append(prefs, `user_pref("security.enterprise_roots.enabled", true);`)
	}

	if t.H3LocalRoots {
		prefs = append(prefs, `user_pref("network.http.http3.disable_when_third_party_roots_found", false);`)
	}

	if t.Proxy != nil {
		host, port := t.Proxy.Hostname(), t.Proxy.Port()

		prefs = append(prefs, `user_pref("network.proxy.type", 1);`)

		switch t.Proxy.Scheme {
		case "socks5":
			prefs = append(prefs,
				`user_pref("network.proxy.socks", "`+host+`");`,
				`user_pref("network.proxy.socks_port", `+port+`);`,
				`user_pref("network.proxy.socks_version", 5);`,
			)

		default:
			prefs = append(prefs,
				`user_pref("network.proxy.http", "`+host+`");`,
				`user_pref("network.proxy.http_port", `+port+`);`,
				`user_pref("network.proxy.ssl", "`+host+`");`,
				`user_pref("network.proxy.ssl_port", `+port+`);`,
			)
		}
	}

	if t.debugPort != 0 {
		// Otherwise the remote agent applies automation prefs that change
		// networking (speculative connections, captive-portal checks).
		prefs = append(prefs, `user_pref("remote.prefs.recommended", false);`)
	}

	if err := os.WriteFile(filepath.Join(dir, "user.js"), []byte(strings.Join(prefs, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}

	if err := os.WriteFile(filepath.Join(dir, "cert_override.txt"), []byte(firefoxOverride(t)), 0o600); err != nil {
		return nil, err
	}

	args := []string{
		"--profile", dir,
		"--new-instance",
	}
	if t.debugPort != 0 {
		args = append(args, "--remote-debugging-port", strconv.Itoa(t.debugPort), "--remote-allow-origins", debugOrigin)
	}

	if headless {
		args = append(args, "--headless")
	}

	return args, nil
}

// firefoxOverride is a cert_override.txt accepting the server certificate for
// the target's host and port.
func firefoxOverride(t Target) string {
	pairs := make([]string, len(t.CertSHA256))
	for i, b := range t.CertSHA256 {
		pairs[i] = fmt.Sprintf("%02X", b)
	}

	return "# PSM Certificate Override Settings file\n" +
		"# This is a generated file!  Do not edit.\n" +
		t.Host + ":" + strconv.Itoa(t.Port) + ":\tOID.2.16.840.1.101.3.4.2.1\t" + strings.Join(pairs, ":") + "\t\n"
}
