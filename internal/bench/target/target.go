// Package target names what the benchmark talks to: the two servers (nginx
// and the Go server), their hosts and ports, the proxy, the protocols under
// test, and the shared CA. Names resolve through Docker network aliases (both
// stacks on one host) or the client's extra_hosts (BENCH_SERVER, a remote
// server host); the ports are the same either way.
package target

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Proto is a protocol under test.
type Proto string

// The protocols.
const (
	H1 Proto = "h1"
	H2 Proto = "h2"
	H3 Proto = "h3"
)

// Protos are the protocols under test, in report order.
var Protos = []Proto{
	H1,
	H2,
	H3,
}

// Wire returns the protocol as a response's Proto field names it.
func (p Proto) Wire() string {
	switch p {
	case H1:
		return "HTTP/1.1"
	case H2:
		return "HTTP/2.0"
	case H3:
		return "HTTP/3.0"
	}

	return string(p)
}

// FromWire returns the Proto a response's Proto field names, or "" for one
// that is none of them.
func FromWire(proto string) Proto {
	switch proto {
	case "HTTP/1.1", "HTTP/1.0":
		return H1
	case "HTTP/2.0", "HTTP/2":
		return H2
	case "HTTP/3.0", "HTTP/3":
		return H3
	}

	return ""
}

// Server is one of the two servers. Both serve the same endpoints (package
// site): TLSPort speaks HTTP/2 and HTTP/1.1 over TCP (ALPN) and HTTP/3 over
// UDP, advertised with Alt-Svc; H1Port offers only http/1.1 in ALPN and no
// Alt-Svc; StatsPort is plain HTTP with /_stats, the server's CPU use.
type Server struct {
	Name      string
	Domain    string
	TLSPort   int
	H1Port    int
	StatsPort int
}

// Servers are the servers, in report order.
var Servers = []Server{
	{
		Name:      "nginx",
		Domain:    "nginx.bench.test",
		TLSPort:   8443,
		H1Port:    8444,
		StatsPort: 8080,
	},
	{
		Name:      "go",
		Domain:    "go.bench.test",
		TLSPort:   9443,
		H1Port:    9444,
		StatsPort: 9080,
	},
}

// ServerNamed returns the server called name.
func ServerNamed(name string) (Server, error) {
	for _, s := range Servers {
		if s.Name == name {
			return s, nil
		}
	}

	return Server{}, fmt.Errorf("target: no server %q", name)
}

// Port returns the port a protocol is tested on.
func (s Server) Port(p Proto) int {
	if p == H1 {
		return s.H1Port
	}

	return s.TLSPort
}

// Host returns the full name of one of the server's hosts (www, static, api).
func (s Server) Host(label string) string {
	return label + "." + s.Domain
}

// URL returns https://<label>.<domain>:<port for p><path>.
func (s Server) URL(p Proto, label, path string) string {
	return "https://" + s.Host(label) + ":" + strconv.Itoa(s.Port(p)) + path
}

// Proxy is the benchmark's proxy: CONNECT on two ports (one asks for Basic
// credentials) and SOCKS5 (with username and password when the client
// offers them, else without; CONNECT and UDP ASSOCIATE). It accepts
// ProxyUser and every ProxyUser-<anything> with ProxyPassword
// (ProxyCredentialsValid), so each of many clients can have an identity
// of its own, and counts what it relays per user (FetchProxyUsage).
const (
	ProxyHost      = "proxy.bench.test"
	ProxyPort      = 3128
	ProxyAuthPort  = 3129
	ProxySOCKSPort = 1080
	ProxyStatsPort = 9081
	ProxyUser      = "bench"
	ProxyPassword  = "bench-secret"
)

// ProxyCredentialsValid reports whether the proxy accepts user and
// password: ProxyUser or ProxyUser-<anything>, with ProxyPassword.
func ProxyCredentialsValid(user, password string) bool {
	return password == ProxyPassword && (user == ProxyUser || strings.HasPrefix(user, ProxyUser+"-"))
}

// OffloadsPort is where the offloads sidecar (`bench offloads`, on the
// client's Docker host's network) listens, at the bench network's gateway:
// layer 5 turns the segmentation offloads of the host's end of the client's
// veth off under netem through it.
const OffloadsPort = 9082

// ProxyKind is one way of going through the proxy.
type ProxyKind string

// The proxy kinds.
const (
	ProxyConnect     ProxyKind = "connect"
	ProxyConnectAuth ProxyKind = "connect-auth"
	ProxySOCKS5      ProxyKind = "socks5"
)

// ProxyKinds are the proxy kinds, in report order.
var ProxyKinds = []ProxyKind{
	ProxyConnect,
	ProxyConnectAuth,
	ProxySOCKS5,
}

// ProxyURL returns the proxy URL for a kind, credentials included.
func ProxyURL(k ProxyKind) string {
	switch k {
	case ProxyConnect:
		return "http://" + ProxyHost + ":" + strconv.Itoa(ProxyPort)
	case ProxyConnectAuth:
		return "http://" + ProxyUser + ":" + ProxyPassword + "@" + ProxyHost + ":" + strconv.Itoa(ProxyAuthPort)
	case ProxySOCKS5:
		return "socks5://" + ProxyHost + ":" + strconv.Itoa(ProxySOCKSPort)
	}

	return ""
}

// ProxyUserURL returns the URL of a proxy kind that carries credentials,
// as user (ProxyCredentialsValid): connect-auth, or socks5 as socks5h (the
// proxy resolves the host). "" for connect, which takes no credentials.
func ProxyUserURL(k ProxyKind, user string) string {
	creds := url.UserPassword(user, ProxyPassword).String()

	switch k {
	case ProxyConnectAuth:
		return "http://" + creds + "@" + ProxyHost + ":" + strconv.Itoa(ProxyAuthPort)
	case ProxySOCKS5:
		return "socks5h://" + creds + "@" + ProxyHost + ":" + strconv.Itoa(ProxySOCKSPort)
	case ProxyConnect:
	}

	return ""
}

// ProxyUse is what the proxy relayed for one user (or for every user of a
// prefix, ProxyUsage.Total): TCP tunnels opened (CONNECT, SOCKS5 CONNECT),
// SOCKS5 UDP associations, the tunnels and associations still open when
// read, datagrams relayed to the client, and bytes relayed each way,
// tunnels and datagrams together (datagrams without their SOCKS5 header).
type ProxyUse struct {
	Tunnels          int64 `json:"tunnels"`
	Associations     int64 `json:"associations"`
	OpenTunnels      int64 `json:"openTunnels"`
	OpenAssociations int64 `json:"openAssociations"`
	Datagrams        int64 `json:"datagrams"`
	BytesUp          int64 `json:"bytesUp"`
	BytesDown        int64 `json:"bytesDown"`
}

// Add adds o to u.
func (u *ProxyUse) Add(o ProxyUse) {
	u.Tunnels += o.Tunnels
	u.Associations += o.Associations
	u.OpenTunnels += o.OpenTunnels
	u.OpenAssociations += o.OpenAssociations
	u.Datagrams += o.Datagrams
	u.BytesUp += o.BytesUp
	u.BytesDown += o.BytesDown
}

// ProxyUsage is the proxy's /_proxy answer for the users with a prefix:
// their total and, when asked for, each user's.
type ProxyUsage struct {
	Total ProxyUse            `json:"total"`
	Users map[string]ProxyUse `json:"users,omitempty"`
}

// ProxyUsagePath is the proxy's per-user usage on its stats port:
// ?prefix=<users' prefix>, &users=1 for each user's, &forget=1 to drop
// them from the proxy's memory once read.
const ProxyUsagePath = "/_proxy"

// FetchProxyUsage returns what the proxy relayed for the users whose name
// starts with prefix (each user's too with users), forgetting them with
// forget.
func FetchProxyUsage(ctx context.Context, prefix string, users, forget bool) (ProxyUsage, error) {
	var pu ProxyUsage

	q := url.Values{
		"prefix": {prefix},
	}
	if users {
		q.Set("users", "1")
	}

	if forget {
		q.Set("forget", "1")
	}

	u := "http://" + ProxyHost + ":" + strconv.Itoa(ProxyStatsPort) + ProxyUsagePath + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return pu, err
	}

	resp, err := statsClient.Do(req)
	if err != nil {
		return pu, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return pu, fmt.Errorf("target: %s: %s", ProxyUsagePath, resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&pu)

	return pu, err
}

// CADir is where the shared CA and the server certificate live: the
// bench-ca volume, mounted at /data/ca, unless BENCH_CA_DIR says otherwise.
func CADir() string {
	if d := os.Getenv("BENCH_CA_DIR"); d != "" {
		return d
	}

	return "/data/ca"
}

// Files in CADir.
const (
	CAFile   = "ca.pem"
	CertFile = "server.pem"
	KeyFile  = "server-key.pem"
)

// RootCAs returns a pool holding only the benchmark's CA.
func RootCAs() (*x509.CertPool, error) {
	pem, err := os.ReadFile(filepath.Join(CADir(), CAFile))
	if err != nil {
		return nil, fmt.Errorf("target: reading the CA (run the server stack's setup first): %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("target: no certificate in " + CAFile)
	}

	return pool, nil
}

// Stats is a server's /_stats answer.
type Stats struct {
	// CPUMicros is the container's CPU time (cgroup cpu.stat usage_usec).
	CPUMicros int64 `json:"cpuMicros"`
	// CPUs is how many CPUs the container may use.
	CPUs int `json:"cpus"`
	// TCPConns and QUICConns count connections accepted since the start
	// (the Go server only; nginx reports 0).
	TCPConns  int64 `json:"tcpConns"`
	QUICConns int64 `json:"quicConns"`
	// Workers is each worker process's CPU time in microseconds, by PID
	// (nginx: one process per CPU, each serving its connections alone);
	// empty for a server without worker processes (the Go server).
	Workers map[string]int64 `json:"workers,omitempty"`
}

// statsClient never shares connections with a client under test.
var statsClient = &http.Client{
	Timeout: 10 * time.Second,
}

// FetchStats returns a server's stats from host:port.
func FetchStats(ctx context.Context, host string, port int) (Stats, error) {
	var st Stats

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+":"+strconv.Itoa(port)+"/_stats", nil)
	if err != nil {
		return st, err
	}

	resp, err := statsClient.Do(req)
	if err != nil {
		return st, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return st, fmt.Errorf("target: /_stats: %s", resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&st)

	return st, err
}

// Stats returns the server's stats.
func (s Server) Stats(ctx context.Context) (Stats, error) {
	return FetchStats(ctx, s.Host("www"), s.StatsPort)
}

// Utilisation returns the share of the server's CPUs used between two
// samples taken d apart.
func Utilisation(before, after Stats, d time.Duration) float64 {
	if after.CPUs == 0 || d <= 0 {
		return 0
	}

	used := time.Duration(after.CPUMicros-before.CPUMicros) * time.Microsecond

	return used.Seconds() / (d.Seconds() * float64(after.CPUs))
}

// BusiestWorker returns the CPU use of the busiest worker process between
// two samples taken d apart, in cores (1 is one core's worth: a worker
// that serves its connections on one core is the limit there), or -1 when
// the server reports no workers.
func BusiestWorker(before, after Stats, d time.Duration) float64 {
	if len(after.Workers) == 0 || d <= 0 {
		return -1
	}

	var busiest int64

	for pid, a := range after.Workers {
		b, ok := before.Workers[pid]
		if !ok {
			continue
		}

		busiest = max(busiest, a-b)
	}

	return (time.Duration(busiest) * time.Microsecond).Seconds() / d.Seconds()
}
