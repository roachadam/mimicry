package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/clientcert"
)

// autoSelectKeys are Chrome's and Edge's AutoSelectCertificateForUrls
// policy keys of the current user: a string value per entry, named 1, 2,
// and so on.
var autoSelectKeys = map[string]string{
	"chrome": `HKCU:\Software\Policies\Google\Chrome\AutoSelectCertificateForUrls`,
	"edge":   `HKCU:\Software\Policies\Microsoft\Edge\AutoSelectCertificateForUrls`,
	"brave":  `HKCU:\Software\Policies\BraveSoftware\Brave\AutoSelectCertificateForUrls`,
}

// clientCertRefusal says why b cannot take a client-certificate scenario
// here, "" when it can: every browser here reads the user's store.
func clientCertRefusal(*browser.Browser) string {
	return ""
}

// installClientCert imports the identity in the PKCS#12 file p12 into the
// user's store (Cert:\CurrentUser\My, its CA among the user's intermediate
// CAs), where the Chromium browsers read identities, and Firefox through its
// osclientcerts module (browser.Target.ClientCert); for the Chromium browsers it
// adds AutoSelectCertificateForUrls entries for hosts to the user's
// policies after any there.
func installClientCert(ctx context.Context, b *browser.Browser, p12 string, hosts []string) (func(context.Context) error, error) {
	// The passphrase goes in a file beside p12, never in the script, which
	// an error message could show.
	pass := p12 + ".passphrase"
	if err := os.WriteFile(pass, []byte(clientcert.Password), 0o600); err != nil {
		return nil, err
	}

	defer func() {
		_ = os.Remove(pass)
	}()

	script := `$p = ConvertTo-SecureString -String (Get-Content -Raw -LiteralPath ` + browser.PSQuote(pass) + `) -AsPlainText -Force
Import-PfxCertificate -FilePath ` + browser.PSQuote(p12) + ` -CertStoreLocation Cert:\CurrentUser\My -Password $p | Out-Null
`

	if key, ok := autoSelectKeys[b.Name]; ok && capture.Chromium(b.Name) {
		quoted := make([]string, len(hosts))
		for i, e := range clientcert.AutoSelectEntries(hosts) {
			quoted[i] = browser.PSQuote(e)
		}

		script += `$k = ` + browser.PSQuote(key) + `
if (-not (Test-Path $k)) { New-Item -Path $k -Force | Out-Null }
$names = @((Get-Item $k).GetValueNames())
foreach ($e in @(` + strings.Join(quoted, ", ") + `)) {
	$n = 1
	while ($names -contains "$n") { $n++ }
	New-ItemProperty -Path $k -Name "$n" -Value $e -PropertyType String | Out-Null
	$names += "$n"
}
`
	}

	if _, err := browser.PowerShell(ctx, script); err != nil {
		return nil, errors.Join(fmt.Errorf("importing the client identity: %w", err), removeClientCerts(context.WithoutCancel(ctx)))
	}

	return removeClientCerts, nil
}

// removeClientCerts removes every identity a run's client CA issued, and
// the CA, from the user's store, and the policy entries naming it (with
// keys left empty by that); the user's own entries stay.
func removeClientCerts(ctx context.Context) error {
	keys := make([]string, 0, len(autoSelectKeys))
	for _, k := range autoSelectKeys {
		keys = append(keys, browser.PSQuote(k))
	}

	ca := browser.PSQuote("CN=" + clientcert.CACommonName)
	name := browser.PSQuote("*" + clientcert.CACommonName + "*")

	_, err := browser.PowerShell(ctx, `Get-ChildItem Cert:\CurrentUser\My, Cert:\CurrentUser\CA | Where-Object { $_.Issuer -eq `+ca+` } | ForEach-Object {
	if ($_.HasPrivateKey) { Remove-Item -Path $_.PSPath -DeleteKey } else { Remove-Item -Path $_.PSPath }
}
foreach ($k in @(`+strings.Join(keys, ", ")+`)) {
	if (-not (Test-Path $k)) { continue }
	$i = Get-Item $k
	foreach ($n in $i.GetValueNames()) {
		if ($i.GetValue($n) -like `+name+`) { Remove-ItemProperty -Path $k -Name $n }
	}
	# Remove the keys left empty, up to the policies' root.
	while ($k -notlike 'HKCU:\Software\Policies' -and (Test-Path $k)) {
		$i = Get-Item $k
		if ($i.ValueCount -gt 0 -or $i.SubKeyCount -gt 0) { break }
		Remove-Item -Path $k
		$k = Split-Path $k -Parent
	}
}`)
	if err != nil {
		return fmt.Errorf("removing the client identities and their policy entries: %w", err)
	}

	return nil
}
