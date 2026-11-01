package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// listenAddr parses -listen: "" is loopback; otherwise lan, the captures
// serve a device on the network from this address.
func listenAddr(s string) (netip.Addr, bool, error) {
	if s == "" {
		return netip.MustParseAddr(browser.Loopback), false, nil
	}

	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() || a.IsLoopback() || a.IsUnspecified() {
		return netip.Addr{}, false, fmt.Errorf("-listen %q: want this Mac's IPv4 address on the device's network", s)
	}

	return a, true, nil
}

// lanPrefix is the network of this Mac's interface holding addr (-listen):
// the devices its DNS stub and the iPhone's proxy answer, refusing the rest
// (both pass names and hosts through to the network).
func lanPrefix(addr netip.Addr) (netip.Prefix, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return netip.Prefix{}, err
	}

	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}

		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok || ip.Unmap() != addr {
			continue
		}

		ones, _ := n.Mask.Size()

		return netip.PrefixFrom(addr, ones).Masked(), nil
	}

	return netip.Prefix{}, fmt.Errorf("-listen %s is no address of this Mac's network interfaces", addr)
}

// iphoneProxyAddr is where the proxy for the iPhone listens: this Mac's
// address on its network (-listen), browser.IOSProxyPort.
func iphoneProxyAddr(addr string) string {
	return net.JoinHostPort(addr, strconv.Itoa(browser.IOSProxyPort))
}

// iphoneProxy asks the maintainer, and waits for Enter, whenever the next
// scenario needs the iPhone's Wi-Fi proxy set otherwise (Manual, this
// Mac's proxy, for the proxy scenarios; Off for the others): a setting of
// the phone the tool cannot change. A run starts with it off. Nil for any
// other browser.
type iphoneProxy struct {
	addr  string
	on    bool
	lines chan error
}

func newIPhoneProxy(b *browser.Browser, addr string) *iphoneProxy {
	if b.Name != browser.IOSSafariName {
		return nil
	}

	return &iphoneProxy{
		addr: addr,
	}
}

// set has the iPhone's proxy set as sc needs, asking for it if it is not.
func (p *iphoneProxy) set(ctx context.Context, sc *server.Scenario) error {
	if p == nil {
		return nil
	}

	want := sc.Proxy != ""
	if want == p.on {
		return nil
	}

	setting := "    Off"
	if want {
		setting = fmt.Sprintf("    Manual\n    Server: %s\n    Port: %d\n    Authentication: off", p.addr, browser.IOSProxyPort)
	}

	log.Printf("%s: scenario %s needs another proxy setting on the iPhone. Settings > Wi-Fi > (i) on this network > Configure Proxy:\n\n%s\n\nSave, then press Enter here.",
		browser.IOSSafariName, sc.Name, setting)

	if err := p.enter(ctx); err != nil {
		return err
	}

	p.on = want

	return nil
}

// finish asks for the proxy off after a run that left it on, without
// waiting: the iPhone has no network through a proxy nobody serves.
func (p *iphoneProxy) finish() {
	if p == nil || !p.on {
		return
	}

	log.Printf("%s: done with the proxy: on the iPhone, Settings > Wi-Fi > (i) on this network > Configure Proxy > Off, and Save", browser.IOSSafariName)
}

// enter waits for a line on standard input, or for ctx to end.
func (p *iphoneProxy) enter(ctx context.Context) error {
	if p.lines == nil {
		p.lines = make(chan error)

		go func() {
			in := bufio.NewReader(os.Stdin)
			for {
				_, err := in.ReadString('\n')
				if errors.Is(err, io.EOF) {
					err = errors.New("standard input closed while waiting for Enter: run the capture tool where someone can press Enter")
				}

				p.lines <- err

				if err != nil {
					return
				}
			}
		}()
	}

	select {
	case err := <-p.lines:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// iosCAProfilePort is where -ios-ca-profile serves the profile with
// -listen.
const iosCAProfilePort = 15357

// writeIOSCAProfile writes the capture CA in caDir as an iOS configuration
// profile to path and, with lan, serves it on addr until ctx ends, for
// Safari on the iPhone to download.
func writeIOSCAProfile(ctx context.Context, caDir, path string, addr netip.Addr, lan bool) error {
	ca, err := server.LoadOrCreateCA(caDir, server.CADomains, time.Now())
	if err != nil {
		return err
	}

	data := mobileConfig(ca.Cert)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}

	log.Printf("wrote %s: the capture CA %s as an iOS configuration profile", path, ca.CertPath)

	if !lan {
		return nil
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", netip.AddrPortFrom(addr, iosCAProfilePort).String())
	if err != nil {
		return err
	}

	name := filepath.Base(path)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/"+name {
				http.NotFound(w, r)

				return
			}

			w.Header().Set("Content-Type", "application/x-apple-aspen-config")

			_, _ = w.Write(data)

			log.Printf("served the profile to %s", r.RemoteAddr) //nolint:gosec // the peer's address as the kernel reports it, not request content
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		_ = srv.Close()
	}()

	log.Printf("open this URL in Safari on the iPhone and allow the download; then Settings > General > VPN & Device Management > %s > Install, "+
		"and Settings > General > About > Certificate Trust Settings: turn on full trust for %s. Ctrl-C to stop serving.\n\n    http://%s/%s\n",
		mobileConfigName, server.CACommonName, ln.Addr(), url.PathEscape(name))

	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// mobileConfigName is the profile's name in the iPhone's settings.
const mobileConfigName = "mimicry capture CA"

// mobileConfig is an iOS configuration profile installing cert as a root
// (com.apple.security.root). Its UUIDs come from the certificate, so the
// profile for a CA replaces an older copy of itself.
func mobileConfig(cert *x509.Certificate) []byte {
	sum := sha256.Sum256(cert.Raw)

	return fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>mimicry-capture-ca.cer</string>
			<key>PayloadContent</key>
			<data>%s</data>
			<key>PayloadDescription</key>
			<string>Adds the capture CA as a root certificate.</string>
			<key>PayloadDisplayName</key>
			<string>%s</string>
			<key>PayloadIdentifier</key>
			<string>io.github.roachadam.mimicry.capture-ca.root</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>%s</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>The mimicry capture tool's CA, for capturing Safari's traffic to the capture server. It can vouch only for names under .test and localhost.</string>
	<key>PayloadDisplayName</key>
	<string>%s</string>
	<key>PayloadIdentifier</key>
	<string>io.github.roachadam.mimicry.capture-ca</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>%s</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`, base64.StdEncoding.EncodeToString(cert.Raw), server.CACommonName, uuidOf(sum[:16]), mobileConfigName, uuidOf(sum[16:]))
}

// uuidOf formats 16 bytes as a version 4 (random-format) UUID.
func uuidOf(b []byte) string {
	var u [16]byte

	copy(u[:], b)

	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80

	return fmt.Sprintf("%X-%X-%X-%X-%X", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
