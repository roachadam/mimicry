package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/h3"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/profile"
)

// prober dials QUIC and speaks HTTP/3 as the engine does (native
// connectH3), but on a tap with a key log so a run sees what the engine
// hides: version, Retry, 0-RTT acceptance, cipher, key updates, errors.
// One prober holds one origin's state.
type prober struct {
	p     *profile.Profile
	plan  *fingerprint.Plan
	roots *x509.CertPool
	// tickets are QUIC session tickets, kept under ticketKey.
	tickets *handshake.Tickets
	// dir, when set, gets each connection's key log and datagram trace.
	dir string
	// seed seeds the impairment draws; each connection takes the next.
	seed atomic.Uint64
	n    atomic.Int64

	mu    sync.Mutex
	token []byte
	rtt   time.Duration
	// held is a ticket waitTicket took from tickets, for the next dial.
	held *handshake.Ticket
}

// ticketKey is the key the prober's tickets are kept under: one origin.
const ticketKey = "interop h3"

func newProber(p *profile.Profile, roots *x509.CertPool, dir string, seed uint64) *prober {
	pr := &prober{
		p:       p,
		plan:    fingerprint.Compile(p),
		roots:   roots,
		tickets: handshake.NewTickets("", clockwork.NewRealClock()),
		dir:     dir,
	}
	pr.seed.Store(seed)

	return pr
}

// resumes reports whether the profile resumes QUIC sessions (with 0-RTT):
// Chrome and Firefox, not Safari.
func (pr *prober) resumes() bool {
	return pr.plan.Resumes() && pr.p.QUIC.ZeroRTT
}

type probeConn struct {
	qc   *quic.Conn
	hc   *h3.Conn
	tap  *tap
	name string
	// offeredPSK: the hello resumed a ticket.
	offeredPSK bool
	// dialed is how long quic.Dial took: the handshake, or nothing for a
	// 0-RTT connection, which returns at once.
	dialed time.Duration
	host   string
	port   string
	keys   *os.File
}

// dial opens a connection to peer for host, resuming the last ticket
// where the profile resumes. Like the engine's, every dial is for a
// pending request (h3.Options.Pending).
func (pr *prober) dial(ctx context.Context, host string, peer netip.AddrPort, imp impairment) (*probeConn, error) {
	network, local := "udp4", "0.0.0.0:0"
	if peer.Addr().Is6() && !peer.Addr().Is4In6() {
		network, local = "udp6", "[::]:0"
	}
	pc, err := (&net.ListenConfig{}).ListenPacket(ctx, network, local)
	if err != nil {
		return nil, err
	}
	seed := pr.seed.Add(1)
	t := newTap(pc, imp, pr.p.QUIC.SCIDLength, seed)
	c := &probeConn{
		tap:  t,
		name: fmt.Sprintf("conn%02d", pr.n.Add(1)),
		host: host,
		port: strconv.Itoa(int(peer.Port())),
	}
	cfg := handshake.Config{
		ServerName: host,
		RootCAs:    pr.roots,
	}
	hello := pr.plan.QUICHello()
	pr.mu.Lock()
	rtt, token := pr.rtt, pr.token
	held := pr.held
	pr.held = nil
	pr.mu.Unlock()
	if pr.resumes() {
		cfg.Tickets, cfg.TicketKey = pr.tickets, ticketKey
		if cfg.Ticket = held; cfg.Ticket == nil {
			cfg.Ticket = pr.tickets.Take(ticketKey)
		}
		if cfg.Ticket != nil {
			hello = pr.plan.ResumedQUICHello()
			c.offeredPSK = true
		} else {
			rtt = 0
		}
	}
	spec, err := handshake.Spec(hello)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	tlsConfig := handshake.QUICConfig(cfg)
	var keyLog io.Writer
	if pr.dir != "" {
		f, err := os.Create(filepath.Join(pr.dir, c.name+".keys"))
		if err == nil {
			keyLog, c.keys = f, f
		}
	}
	tlsConfig.KeyLogWriter = t.keyLogWriter(keyLog)
	dc := quic.DialConfig{
		Profile:   pr.p.QUIC,
		Hello:     spec,
		TLS:       tlsConfig,
		CachedRTT: rtt,
		NewPath:   t.newPath,
		OnToken: func(b []byte) {
			pr.mu.Lock()
			defer pr.mu.Unlock()
			pr.token = b
		},
	}
	if pr.p.QUIC.Tokens {
		dc.Token = token
	}
	if cfg.Ticket != nil {
		dc.Version = quic.TicketVersion(cfg.Ticket.Extra())
	}
	start := time.Now()
	qc, err := quic.Dial(ctx, t, peer, dc)
	c.dialed = time.Since(start)
	if err != nil {
		pr.save(c)
		return c, err
	}
	c.qc = qc
	hc, err := h3.New(qc, pr.p, h3.Options{
		AltHost: host,
		AltPort: c.port,
		Pending: true,
	})
	if err != nil {
		pr.save(c)
		return c, err
	}
	c.hc = hc

	return c, nil
}

// close closes c and keeps its smoothed RTT for the next connection,
// as the engine does.
func (pr *prober) close(c *probeConn) {
	if c == nil {
		return
	}
	if c.qc != nil {
		select {
		case <-c.qc.Done():
		default:
			rtt := c.qc.SmoothedRTT()
			pr.mu.Lock()
			pr.rtt = rtt
			pr.mu.Unlock()
		}
	}
	if c.hc != nil {
		_ = c.hc.Close()
	} else if c.qc != nil {
		_ = c.qc.Close()
	}
	pr.save(c)
}

// save writes c's datagram trace when the prober keeps files.
func (pr *prober) save(c *probeConn) {
	if pr.dir == "" || c == nil {
		return
	}
	_ = c.tap.writeTrace(filepath.Join(pr.dir, c.name+".trace.json"))
	if c.keys != nil {
		_ = c.keys.Close()
		c.keys = nil
	}
}

// connFacts is what a connection showed.
type connFacts struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Handshake   bool     `json:"handshake"`
	HandshakeMS float64  `json:"handshakeMs"`
	Cipher      string   `json:"cipher,omitempty"`
	ALPN        string   `json:"alpn,omitempty"`
	OfferedPSK  bool     `json:"offeredPsk"`
	Resumed     bool     `json:"resumed"`
	Sent0RTT    int      `json:"sent0rttPackets"`
	EarlyData   bool     `json:"earlyData"`
	Retries     int      `json:"retries"`
	VN          int      `json:"versionNegotiations"`
	ServerKU    int      `json:"serverKeyUpdates"`
	ClientKU    int      `json:"clientKeyUpdates"`
	KUKnown     bool     `json:"keyUpdatesKnown"`
	Stats       tapStats `json:"stats"`
	CloseErr    string   `json:"closeError,omitempty"`
}

// facts reads what c showed so far.
func (c *probeConn) facts() connFacts {
	st := c.tap.stats()
	f := connFacts{
		Name:        c.name,
		HandshakeMS: ms(c.dialed),
		OfferedPSK:  c.offeredPSK,
		Sent0RTT:    st.Sent["0rtt"],
		Retries:     st.Retries,
		VN:          st.VN,
		Stats:       st,
	}
	if len(st.Versions) > 0 {
		f.Version = fmt.Sprintf("%#08x", st.Versions[len(st.Versions)-1])
	}
	if c.qc == nil {
		return f
	}
	cs := c.qc.ConnectionState()
	f.Handshake = cs.HandshakeComplete
	f.Resumed = cs.DidResume
	f.ALPN = cs.NegotiatedProtocol
	if cs.CipherSuite != 0 {
		f.Cipher = fmt.Sprintf("%#04x", cs.CipherSuite)
	}
	f.EarlyData = c.qc.EarlyData()
	f.ServerKU, f.ClientKU, f.KUKnown = c.tap.keyUpdates(cs.CipherSuite)
	select {
	case <-c.qc.Done():
		if err := c.qc.Err(); err != nil {
			f.CloseErr = err.Error()
		}
	default:
	}

	return f
}

type fetched struct {
	Path   string  `json:"path"`
	Status int     `json:"status"`
	Proto  string  `json:"proto,omitempty"`
	Bytes  int64   `json:"bytes"`
	MS     float64 `json:"ms"`
	Err    string  `json:"error,omitempty"`
	sum    [32]byte
}

// get sends a GET for path on c with the profile's navigation header
// values and reads the whole body.
func (pr *prober) get(ctx context.Context, c *probeConn, path string) (out fetched) {
	out = fetched{
		Path: path,
	}
	start := time.Now()
	defer func() {
		out.MS = ms(time.Since(start))
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+authority(c.host, c.port)+path, nil)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	for _, h := range pr.p.Headers {
		if h.Value != "" {
			req.Header.Set(h.Name, h.Value)
		}
	}
	resp, err := c.hc.RoundTrip(req)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	out.Status, out.Proto = resp.StatusCode, resp.Proto
	h := sha256.New()
	out.Bytes, err = io.Copy(h, resp.Body)
	if err != nil {
		out.Err = err.Error()
	}
	copy(out.sum[:], h.Sum(nil))

	return out
}

// authority is host:port without the port when it is 443.
func authority(host, port string) string {
	if port == "443" {
		return host
	}

	return net.JoinHostPort(host, port)
}

// waitTicket waits up to d for the server's session ticket to be stored,
// as a browser's connection lives long enough to receive it.
func (pr *prober) waitTicket(ctx context.Context, d time.Duration) bool {
	if !pr.resumes() {
		return false
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if t := pr.tickets.Take(ticketKey); t != nil {
			pr.mu.Lock()
			pr.held = t
			pr.mu.Unlock()

			return true
		}
		time.Sleep(20 * time.Millisecond)
	}

	return false
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// errString is err's text, or "".
func errString(err error) string {
	if err == nil {
		return ""
	}
	if ce, ok := errors.AsType[*quic.ConnectionCloseError](err); ok {
		return fmt.Sprintf("peer application close %#x: %s", ce.Code, ce.Reason)
	}

	return err.Error()
}
