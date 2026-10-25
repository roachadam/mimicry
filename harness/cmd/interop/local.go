package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/profile"
)

// server is a QUIC interop runner endpoint image in the server role.
type server struct {
	name  string
	image string
	// platform, when set, is the image platform to run (docker
	// --platform), where the native one does not work.
	platform string
	// overrides change how the server starts for a TESTCASE whose mode
	// offers no h3, where its run_endpoint.sh has a way to get the mode's
	// feature (Retry, ChaCha20, 0-RTT) with h3.
	overrides map[string]override
}

// override starts a server for a TESTCASE differently.
type override struct {
	// testcase replaces TESTCASE (usually http3, the mode with h3).
	testcase string
	// env adds environment variables (SERVER_PARAMS).
	env []string
	// prelude is a shell command run in the container before the image's
	// entrypoint (to patch a configuration file).
	prelude string
	// why says what the override does, for the report.
	why string
}

// servers are the runner's endpoints with a server role
// (quic-interop-runner implementations_quic.json).
var servers = []server{
	{
		name:  "quic-go",
		image: "martenseemann/quic-go-interop:latest",
	},
	{
		name:  "ngtcp2",
		image: "ghcr.io/ngtcp2/ngtcp2-interop:latest",
		// Its hq modes use wsslhqserver; the h3 server takes the same
		// flags through SERVER_PARAMS.
		overrides: map[string]override{
			"retry": {
				testcase: "http3",
				env:      []string{"SERVER_PARAMS=-V"},
				why:      "http3 with -V (address validation by Retry)",
			},
			"chacha20": {
				testcase: "http3",
				env:      []string{"SERVER_PARAMS=--ciphers=TLS_CHACHA20_POLY1305_SHA256"},
				why:      "http3 with --ciphers=TLS_CHACHA20_POLY1305_SHA256",
			},
		},
	},
	{
		name:  "mvfst",
		image: "ghcr.io/facebook/proxygen/mvfst-interop:latest",
		// The arm64 build's libevent calls epoll_wait, which arm64
		// Linux does not have ("Function not implemented").
		platform: "linux/amd64",
	},
	{
		name:  "quiche",
		image: "cloudflare/quiche-qns:latest",
	},
	{
		name:  "kwik",
		image: "peterdoornbosch/kwik_n_flupke-interop",
		overrides: map[string]override{
			"retry": {
				testcase: "http3",
				prelude:  `sed -i "s/kwik-interop-with-flupke -s --noRetry/kwik-interop-with-flupke -s/" /run_endpoint.sh`,
				why:      "http3 without --noRetry",
			},
		},
	},
	{
		name:  "picoquic",
		image: "privateoctopus/picoquic:latest",
	},
	{
		name:  "aioquic",
		image: "aiortc/aioquic-qns:latest",
	},
	{
		name:  "neqo",
		image: "ghcr.io/mozilla/neqo-qns:latest",
	},
	{
		name:  "nginx",
		image: "ghcr.io/nginx/nginx-quic-qns:latest",
		overrides: map[string]override{
			// Its h3 responses carry Alt-Svc h3=":443", which moves the
			// origin's next connection to local port 443, where nothing
			// listens (only the container's 443 is mapped, elsewhere).
			"http3": {
				prelude: `sed -i "/add_header Alt-Svc/d" /etc/nginx/nginx.conf.http3`,
				why:     "no Alt-Svc on h3 responses (it names the container's port 443)",
			},
		},
	},
	{
		name:  "msquic",
		image: "ghcr.io/microsoft/msquic/qns:main",
	},
	{
		name:  "xquic",
		image: "ghcr.io/alibaba/xquic/xquic-interop:latest",
	},
	{
		name:      "lsquic",
		image:     "litespeedtech/lsquic-qir:latest",
		overrides: lsquicOverrides(),
	},
	{
		name:  "haproxy",
		image: "haproxytech/haproxy-qns:latest",
		overrides: map[string]override{
			"chacha20": {
				prelude: `sed -i 's/HAP_EXTRA_ARGS="" /HAP_EXTRA_ARGS="ciphersuites TLS_CHACHA20_POLY1305_SHA256" /' /run_endpoint.sh`,
				why:     "bind ciphersuites TLS_CHACHA20_POLY1305_SHA256",
			},
		},
	},
	{
		name:  "quinn",
		image: "stammw/quinn-interop:latest",
	},
	{
		name:  "s2n-quic",
		image: "ghcr.io/aws/s2n-quic/s2n-quic-qns:latest",
		// The qns binary reads TESTCASE itself; only its ALPN is
		// hq-interop outside http3.
		overrides: map[string]override{
			"retry": {
				env: []string{"SERVER_PARAMS=--application-protocols h3"},
				why: "--application-protocols h3",
			},
			"resumption": {
				env: []string{"SERVER_PARAMS=--application-protocols h3"},
				why: "--application-protocols h3",
			},
			"zerortt": {
				testcase: "resumption",
				env:      []string{"SERVER_PARAMS=--application-protocols h3"},
				why:      "resumption (it has no zerortt mode) with --application-protocols h3",
			},
			"chacha20": {
				env: []string{"SERVER_PARAMS=--application-protocols h3"},
				why: "--application-protocols h3",
			},
		},
	},
	{
		name:  "go-x-net",
		image: "us-central1-docker.pkg.dev/golang-interop-testing/quic/go-x-net:latest",
	},
}

// lsquicOverrides runs lsquic as http3 in every mode: its other modes
// add hq-interop to ALPN and then answer the h3 they negotiate as
// HTTP/0.9 (the request stream read raw, closed with an empty FIN). Its
// retry mode's LSQUIC_FORCE_RETRY is patched into the http3 branch.
func lsquicOverrides() map[string]override {
	why := "http3: its hq modes answer negotiated h3 as HTTP/0.9"
	ovs := map[string]override{}
	for _, tc := range []string{"handshake", "transfer", "multiconnect", "resumption", "zerortt", "chacha20"} {
		ovs[tc] = override{
			testcase: "http3",
			why:      why,
		}
	}
	ovs["retry"] = override{
		testcase: "http3",
		prelude:  `sed -i "s/^\( *VERSIONS='-o version=h3-29 -o version=h3'\)$/\1; FORCE_RETRY=1/" /run_endpoint.sh`,
		why:      "http3 with LSQUIC_FORCE_RETRY=1",
	}
	return ovs
}

// localHost is the servers' name: the runner's (lsquic picks its
// certificate by it), the certificate's, the SNI's and the TCP front's,
// resolved to 127.0.0.1 by loopbackResolver.
const localHost = "server"

// localSetup is what every local run shares: certificates, the served
// files and the DNS stub.
type localSetup struct {
	dir      string
	roots    *x509.CertPool
	cert     tls.Certificate
	files    map[string][32]byte
	resolver *net.Resolver
}

// newLocalSetup writes, or reuses when dir has them, the CA and the
// server's certificate (ECDSA P-256, as the runner's certs.sh), the
// served files, and a setup.sh that replaces the image's: the runner's
// routes through its simulator would cut the container off docker's own
// network. Reuse lets a container started by hand (to reproduce a
// failure, with -addr) keep its certificate across runs.
func newLocalSetup(ctx context.Context, dir string) (*localSetup, error) {
	s := &localSetup{
		dir:   dir,
		files: map[string][32]byte{},
	}
	for _, d := range []string{"certs", "www"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			return nil, err
		}
	}
	if err := s.loadCerts(); err != nil {
		if err := s.writeCerts(); err != nil {
			return nil, err
		}
	}
	if err := s.writeFiles(); err != nil {
		return nil, err
	}
	// The simulator's routes would cut the container off docker's own
	// network; the runner's addresses (lsquic binds server4's) are put
	// on the loopback interface instead.
	setup := "#!/bin/sh\n# mimicry interop: no simulator routes.\n" +
		"ip addr add 193.167.100.100/32 dev lo 2>/dev/null\n" +
		"ip -6 addr add fd00:cafe:cafe:100::100/128 dev lo 2>/dev/null\n" +
		"mkdir -p /logs/qlog\n"
	if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte(setup), 0o755); err != nil { //nolint:gosec // the containers execute it
		return nil, err
	}
	var err error
	if s.resolver, err = loopbackResolver(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// loadCerts loads the certificates an earlier run wrote.
func (s *localSetup) loadCerts() error {
	read := func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(s.dir, "certs", name))
	}
	caPEM, err := read("ca.pem")
	if err != nil {
		return err
	}
	certPEM, err := read("cert.pem")
	if err != nil {
		return err
	}
	keyPEM, err := read("priv.key")
	if err != nil {
		return err
	}
	if s.cert, err = tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return err
	}
	if time.Now().After(s.cert.Leaf.NotAfter) {
		return errors.New("expired")
	}
	s.roots = x509.NewCertPool()
	if !s.roots.AppendCertsFromPEM(caPEM) {
		return errors.New("no CA")
	}
	return nil
}

func (s *localSetup) writeCerts() error {
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"mimicry interop CA"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: localHost,
		},
		DNSNames:    []string{localHost, "server", "server4", "server6", "localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(10 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})
	caPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caDER,
	})
	for name, data := range map[string][]byte{
		"cert.pem": certPEM,
		"priv.key": keyPEM,
		"ca.pem":   caPEM,
	} {
		if err := os.WriteFile(filepath.Join(s.dir, "certs", name), data, 0o644); err != nil { //nolint:gosec // the containers' users read them
			return err
		}
	}
	s.roots = x509.NewCertPool()
	s.roots.AddCert(ca)
	s.cert, err = tls.X509KeyPair(certPEM, keyPEM)
	return err
}

func (s *localSetup) writeFiles() error {
	sizes := map[string]int{
		fileSmall: 1 << 10,
		fileTwo:   2 << 20,
		fileThree: 3 << 20,
		fileFive:  5 << 20,
		fileTen:   10 << 20,
	}
	for i := range multiplexFiles {
		sizes[multiplexPath(i)] = multiplexSize
	}
	for p, n := range sizes {
		path := filepath.Join(s.dir, "www", p)
		b, err := os.ReadFile(path)
		if err != nil || len(b) != n {
			b = make([]byte, n)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			if err := os.WriteFile(path, b, 0o644); err != nil { //nolint:gosec // the containers' users read them
				return err
			}
		}
		s.files[p] = sha256.Sum256(b)
	}
	return nil
}

// container is a running server.
type container struct {
	name string
	port uint16
	// front is the TCP front: the same files over HTTP/2 with Alt-Svc.
	front *http.Server
	logs  string
	// ready: the server answered a Version Negotiation probe.
	ready bool
	// testcase is the TESTCASE it runs with; override is the reason of
	// the server's override that changed how it starts ("" for none).
	testcase string
	override string
}

// startServer runs srv's image with TESTCASE testcase (or as its
// override for testcase says), its UDP port 443 mapped to a free local
// port, and a TCP front on the same port number.
func (s *localSetup) startServer(ctx context.Context, srv server, testcase, logs string) (*container, error) {
	if err := os.MkdirAll(logs, 0o750); err != nil {
		return nil, err
	}
	ln, port, err := listenPair(ctx)
	if err != nil {
		return nil, err
	}
	ov := srv.overrides[testcase]
	c := &container{
		name:     fmt.Sprintf("mimicry-interop-%s-%s-%d", srv.name, testcase, port),
		port:     port,
		logs:     logs,
		testcase: testcase,
		override: ov.why,
	}
	if ov.testcase != "" {
		c.testcase = ov.testcase
	}
	args := []string{
		"run", "-d", "--name", c.name, "--cap-add", "NET_ADMIN",
		"-e", "REQUESTS=",
		"-p", fmt.Sprintf("127.0.0.1:%d:443/udp", port),
		"-v", filepath.Join(s.dir, "www") + ":/www:ro",
		"-v", filepath.Join(s.dir, "certs") + ":/certs:ro",
		"-v", logs + ":/logs",
		"-v", filepath.Join(s.dir, "setup.sh") + ":/setup.sh:ro",
		"-e", "ROLE=server",
		"-e", "TESTCASE=" + c.testcase,
		"-e", "SSLKEYLOGFILE=/logs/keys.log",
		"-e", "QLOGDIR=/logs/qlog",
		"-e", "SERVER_LOGS=/logs",
		"-e", "CLIENT_LOGS=/logs",
	}
	for _, kv := range ov.env {
		args = append(args, "-e", kv)
	}
	if srv.platform != "" {
		args = append(args, "--platform", srv.platform)
	}
	if ov.prelude != "" {
		out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", `{{join .Config.Entrypoint " "}}`, srv.image).Output() //nolint:gosec // an image from this tool's own list
		if err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("docker inspect %s: %w", srv.image, err)
		}
		args = append(args, "--entrypoint", "/bin/bash", srv.image, "-c", ov.prelude+" && exec "+strings.TrimSpace(string(out)))
	} else {
		args = append(args, srv.image)
	}
	if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil { //nolint:gosec // arguments from this tool's own server list
		_ = ln.Close()
		return nil, fmt.Errorf("docker run %s: %w: %s", srv.image, err, bytes.TrimSpace(out))
	}
	c.front = s.startFront(ln, port)
	peer := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)
	for range 15 {
		if c.ready = waitReady(ctx, peer, 2*time.Second); c.ready || c.exited(ctx) != "" {
			break
		}
	}
	return c, nil
}

// startFront serves the files on ln over TLS (HTTP/2), every response
// with Alt-Svc: h3 on port.
func (s *localSetup) startFront(ln net.Listener, port uint16) *http.Server {
	front := &http.Server{
		Handler:           altSvcHandler(port, http.FileServer(http.Dir(filepath.Join(s.dir, "www")))),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{s.cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	go func() {
		_ = front.ServeTLS(ln, "", "")
	}()
	return front
}

// exited returns the container's last output lines when it is no longer
// running (a mode its run_endpoint.sh does not support), or "".
func (c *container) exited(ctx context.Context) string {
	if c.name == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}} {{.State.ExitCode}}", c.name).Output() //nolint:gosec // a container this tool named
	if err != nil || strings.HasPrefix(string(out), "true") {
		return ""
	}
	logs, _ := exec.CommandContext(ctx, "docker", "logs", "--tail", "2", c.name).CombinedOutput() //nolint:gosec // a container this tool named
	return fmt.Sprintf("exit %s: %s", strings.Fields(string(out))[1], strings.Join(strings.Fields(string(logs)), " "))
}

// stop saves the container's output to its logs directory and removes it.
func (c *container) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if c.name != "" {
		out, _ := exec.CommandContext(ctx, "docker", "logs", c.name).CombinedOutput() //nolint:gosec // a container this tool named
		_ = os.WriteFile(filepath.Join(c.logs, "docker.log"), out, 0o600)
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", c.name).Run() //nolint:gosec // a container this tool named
	}
	if c.front != nil {
		_ = c.front.Close()
	}
}

// altSvcHandler adds Alt-Svc: h3 on port to every response.
func altSvcHandler(port uint16, h http.Handler) http.Handler {
	alt := fmt.Sprintf(`h3=":%d"; ma=86400`, port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", alt)
		h.ServeHTTP(w, r)
	})
}

// listenPair listens on a TCP port of 127.0.0.1 whose UDP twin is free.
func listenPair(ctx context.Context) (net.Listener, uint16, error) {
	var lc net.ListenConfig
	for range 20 {
		ln, err := lc.Listen(ctx, "tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, 0, err
		}
		port := ln.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // tcp4 listener
		pc, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			_ = ln.Close()
			continue
		}
		_ = pc.Close()
		return ln, uint16(port), nil //nolint:gosec // a port
	}
	return nil, 0, errors.New("no free TCP/UDP port pair")
}

// waitReady sends Initial-sized datagrams of a version no server speaks
// (0x?a?a?a?a, RFC 9000 section 15) until the server answers with
// Version Negotiation, or d passes.
func waitReady(ctx context.Context, peer netip.AddrPort, d time.Duration) bool {
	pc, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return false
	}
	defer func() {
		_ = pc.Close()
	}()
	probe := make([]byte, 1200)
	probe[0] = 0xc0
	copy(probe[1:5], []byte{0x1a, 0x2a, 0x3a, 0x4a})
	probe[5] = 8
	copy(probe[6:14], "mimicry!")
	buf := make([]byte, 1500)
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		_, _ = pc.WriteToUDPAddrPort(probe, peer)
		_ = pc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, _, err := pc.ReadFromUDPAddrPort(buf)
		if err == nil && n >= 5 && buf[0]&0x80 != 0 && bytes.Equal(buf[1:5], []byte{0, 0, 0, 0}) {
			return true
		}
	}
	return false
}

// localGroups groups the cases by the TESTCASE their server runs with,
// in the order of cases.
func localGroups(selected []caseDef) [][]caseDef {
	var groups [][]caseDef
	for _, c := range selected {
		i := slices.IndexFunc(groups, func(g []caseDef) bool {
			return g[0].testcase == c.testcase
		})
		if i < 0 {
			groups = append(groups, []caseDef{c})
			continue
		}
		groups[i] = append(groups[i], c)
	}
	return groups
}

// runServer runs every selected case against srv, for each profile, one
// container per TESTCASE.
func (s *localSetup) runServer(ctx context.Context, srv server, selected []caseDef, profiles []namedProfile, out string, seed uint64) []result {
	results := make([]result, 0, len(selected)*len(profiles))
	for _, group := range localGroups(selected) {
		results = append(results, s.runGroup(ctx, srv, group, profiles, out, seed)...)
	}
	return results
}

// runGroup starts srv in the group's mode and runs its cases. When the
// mode offers no h3 (the first handshake ends in a TLS alert: ALPN) or
// the server does not run in it, and no override says how to get it with
// h3, the server runs as http3 instead: a case whose server feature
// (Retry, ChaCha20, 0-RTT) that mode lacks is then n/a. Each profile's
// cases start with a plain handshake; when it fails, so do the profile's
// cases in the group, with its error, unrun.
func (s *localSetup) runGroup(ctx context.Context, srv server, group []caseDef, profiles []namedProfile, out string, seed uint64) []result {
	testcase := group[0].testcase
	logs := filepath.Join(out, "local", srv.name, testcase)
	c, err := s.startServer(ctx, srv, testcase, filepath.Join(logs, "server"))
	if err != nil {
		return groupResults(srv, group, profiles, testcase, fail, err.Error())
	}
	fallback := ""
	if _, overridden := srv.overrides[testcase]; c.testcase != "http3" && !overridden {
		why := ""
		if exit := c.exited(ctx); exit != "" {
			why = "does not run (" + exit + ")"
		} else if r := s.check(ctx, c, profiles[0], filepath.Join(logs, "alpn-check"), seed); strings.Contains(r.Err, "CRYPTO_ERROR(") {
			why = "offers no h3 (" + r.Err + ")"
		}
		if why != "" {
			c.stop() //nolint:contextcheck // cleanup runs after the run's context ends too
			log.Printf("%s: %s mode %s; running it as http3", srv.name, testcase, why)
			fallback = testcase
			logs = filepath.Join(out, "local", srv.name, fallback+"-as-http3")
			if c, err = s.startServer(ctx, srv, "http3", filepath.Join(logs, "server")); err != nil {
				return groupResults(srv, group, profiles, "http3", fail, err.Error())
			}
		}
	}
	defer c.stop() //nolint:contextcheck // cleanup runs after the run's context ends too
	mode := c.testcase
	if c.override != "" {
		mode += " (override: " + c.override + ")"
	}
	if exit := c.exited(ctx); exit != "" {
		return groupResults(srv, group, profiles, mode, na, "the server does not run in "+c.testcase+" mode ("+exit+")")
	}
	if !c.ready {
		log.Printf("%s/%s: no Version Negotiation reply within 30 s; running anyway", srv.name, c.testcase)
	}
	return s.runCases(ctx, c, srv.name, mode, fallback, group, profiles, logs, seed)
}

// runAddr runs the selected cases against a server already listening on
// 127.0.0.1:port (started by hand to reproduce a failure, with this
// run's -setup certificates and files), with a TCP front on the same
// port when it is free.
func (s *localSetup) runAddr(ctx context.Context, port uint16, selected []caseDef, profiles []namedProfile, out string, seed uint64) []result {
	c := &container{
		port: port,
	}
	var lc net.ListenConfig
	if ln, err := lc.Listen(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))); err == nil {
		c.front = s.startFront(ln, port)
	}
	defer c.stop() //nolint:contextcheck // cleanup runs after the run's context ends too
	logs := filepath.Join(out, "local", "addr")
	return s.runCases(ctx, c, "addr", "", "", selected, profiles, logs, seed)
}

// runCases runs the cases against c for each profile, starting with a
// plain handshake per profile: when it fails, so do the profile's cases,
// with its error, unrun.
func (s *localSetup) runCases(ctx context.Context, c *container, target, mode, fallback string, group []caseDef, profiles []namedProfile, logs string, seed uint64) []result {
	results := make([]result, 0, len(group)*len(profiles))
	for _, np := range profiles {
		first := s.check(ctx, c, np, filepath.Join(logs, "check", np.name), seed)
		for _, cd := range group {
			dir := filepath.Join(logs, cd.name, np.name)
			_ = os.MkdirAll(dir, 0o750)
			r := result{
				Target:         target,
				Case:           cd.name,
				Profile:        np.name,
				Outcome:        pass,
				ServerTestcase: mode,
				Dir:            dir,
			}
			if first.Outcome != pass {
				r.Outcome, r.Err, r.Conns = fail, "first handshake: "+first.Err, first.Conns
				if strings.Contains(first.Err, "CRYPTO_ERROR(120)") {
					r.Outcome, r.Err, r.Note = na, "", "the server offers no h3 in any mode"
				}
			} else {
				env := &caseEnv{
					profile:  np.p,
					host:     localHost,
					peer:     netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), c.port),
					roots:    s.roots,
					files:    s.files,
					origin:   "https://" + net.JoinHostPort(localHost, strconv.Itoa(int(c.port))),
					resolver: s.resolver,
					dir:      dir,
					seed:     seed,
					fallback: fallback,
					front:    s.startFront,
				}
				cctx, cancel := context.WithTimeout(ctx, cd.timeout)
				start := time.Now()
				cd.run(cctx, env, &r)
				r.MS = ms(time.Since(start))
				if r.Outcome == fail && cctx.Err() != nil && !strings.Contains(r.Err, "deadline") {
					r.Err += fmt.Sprintf(" (case timed out after %s)", cd.timeout)
				}
				cancel()
			}
			log.Printf("%-9s %-20s %-17s %-4s %s %s", target, cd.name, np.name, r.Outcome, r.Note, r.Err)
			results = append(results, r)
		}
	}
	return results
}

// check is a plain handshake and GET of the small file.
func (s *localSetup) check(ctx context.Context, c *container, np namedProfile, dir string, seed uint64) result {
	_ = os.MkdirAll(dir, 0o750)
	r := result{
		Outcome: pass,
	}
	env := &caseEnv{
		profile: np.p,
		host:    localHost,
		peer:    netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), c.port),
		roots:   s.roots,
		files:   s.files,
		dir:     dir,
		seed:    seed,
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	oneConn(cctx, env, &r, impairment{}, false, fileSmall)
	return r
}

// groupResults is a row per case and profile, all with one outcome.
func groupResults(srv server, group []caseDef, profiles []namedProfile, testcase, outcome, note string) []result {
	results := make([]result, 0, len(group)*len(profiles))
	for _, cd := range group {
		for _, np := range profiles {
			r := result{
				Target:         srv.name,
				Case:           cd.name,
				Profile:        np.name,
				Outcome:        outcome,
				ServerTestcase: testcase,
			}
			if outcome == fail {
				r.Err = note
			} else {
				r.Note = note
			}
			results = append(results, r)
		}
	}
	return results
}

// namedProfile is a built-in profile and its name.
type namedProfile struct {
	name string
	p    *profile.Profile
}
