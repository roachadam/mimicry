package browser

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// firefoxProfile writes prefs that map the capture hosts to 127.0.0.1 and
// silence first-run pages, plus a certificate exception for the server (or,
// when it trusts the capture CA (trustsCA), trust in the system roots: the
// capture CA in the macOS keychain, or in the Windows machine's Root store
// for the run). The same profile serves macOS and Windows. A Resolve
// target's hosts are looked up instead: on macOS through the system's
// resolver, elsewhere through t.DoH (firefoxTRR).
// network.dns.localDomains maps to loopback only, so t.Addr must be
// Loopback.
func firefoxProfile(dir string, t Target, headless bool) ([]string, error) {
	if t.Addr != Loopback {
		return nil, fmt.Errorf("firefox maps the capture hosts to loopback only (network.dns.localDomains), not %s: capture it without -listen", t.Addr)
	}

	local := t.mapped()
	if h := t.proxyHost(); h != "" {
		local = append(local, h)
	}

	prefs := []string{
		`user_pref("network.dns.localDomains", "` + strings.Join(local, ",") + `");`,
		`user_pref("network.dns.disableIPv6", true);`,
		`user_pref("browser.shell.checkDefaultBrowser", false);`,
		`user_pref("browser.startup.homepage_override.mstone", "ignore");`,
		`user_pref("browser.aboutwelcome.enabled", false);`,
		`user_pref("datareporting.policy.dataSubmissionEnabled", false);`,
		`user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);`,
	}
	if t.trustsCA() {
		prefs = append(prefs, `user_pref("security.enterprise_roots.enabled", true);`)
	}

	if t.H3LocalRoots {
		prefs = append(prefs, `user_pref("network.http.http3.disable_when_third_party_roots_found", false);`)
	}

	if t.Resolve && runtime.GOOS != "darwin" {
		if t.DoH == "" {
			return nil, fmt.Errorf("firefox looks the hosts up through the capture tool's DoH endpoint on %s, but the target has none", runtime.GOOS)
		}

		prefs = append(prefs, firefoxTRR(t)...)
	}

	if t.Proxy != nil {
		host, port := t.Proxy.Hostname(), t.Proxy.Port()

		switch t.Proxy.Scheme {
		case "socks5":
			prefs = append(prefs,
				`user_pref("network.proxy.type", 1);`,
				`user_pref("network.proxy.socks", "`+host+`");`,
				`user_pref("network.proxy.socks_port", `+port+`);`,
				`user_pref("network.proxy.socks_version", 5);`,
			)

		case "https":
			// Firefox's manual settings take no proxy reached over TLS;
			// a proxy script names one.
			prefs = append(prefs,
				`user_pref("network.proxy.type", 2);`,
				`user_pref("network.proxy.autoconfig_url", "`+firefoxPAC("HTTPS "+t.Proxy.Host)+`");`,
			)

		default:
			prefs = append(prefs,
				`user_pref("network.proxy.type", 1);`,
				`user_pref("network.proxy.http", "`+host+`");`,
				`user_pref("network.proxy.http_port", `+port+`);`,
				`user_pref("network.proxy.ssl", "`+host+`");`,
				`user_pref("network.proxy.ssl_port", `+port+`);`,
			)
		}
	}

	if t.ClientCert != nil {
		prefs = append(prefs, firefoxClientCertPrefs()...)
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

// firefoxTRR are the prefs having Firefox resolve every name through its
// own DoH client (TRR) only, at t.DoH (Browser.Conditions says why).
// Firefox asks for HTTPS records only through TRR or a system resolver
// that answers them; network.dns.echconfig.enabled, on by default, has it
// use their ECH configs.
func firefoxTRR(t Target) []string {
	return []string{
		`user_pref("network.trr.mode", 3);`,
		`user_pref("network.trr.uri", "` + firefoxTRRURI(t) + `");`,
		`user_pref("network.trr.bootstrapAddr", "` + t.Addr + `");`,
		`user_pref("network.trr.allow-rfc1918", true);`,
		`user_pref("network.trr.confirmationNS", "skip");`,
	}
}

// firefoxTRRURI is t.DoH as network.trr.uri takes it: without the URI
// template's {?dns}, since TRR sends its queries by POST.
func firefoxTRRURI(t Target) string {
	return strings.TrimSuffix(t.DoH, "{?dns}")
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

// firefoxPAC is a data: URL of a proxy script sending every URL to proxy
// (a PAC result, "HTTPS host:port").
func firefoxPAC(proxy string) string {
	script := "function FindProxyForURL(url, host) { return '" + proxy + "'; }"

	return "data:application/x-ns-proxy-autoconfig," + url.PathEscape(script)
}
