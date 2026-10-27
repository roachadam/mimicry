package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/hkdf"
)

// Long header packet types of QUIC version 1 (RFC 9000, section 17.2).
const (
	typeInitial   = 0
	typeZeroRTT   = 1
	typeHandshake = 2
	typeRetry     = 3
)

// impairment is the client's half of the runner's network simulator,
// applied in both directions. Draws are seeded, so a rerun with the same
// seed and packets drops the same ones.
type impairment struct {
	// Drop is the probability a datagram is dropped, each direction.
	Drop float64
	// Corrupt is the probability one byte of a datagram is flipped.
	Corrupt float64
	// Delay holds every datagram the client sends this long (the round
	// trip the link adds).
	Delay time.Duration
	// BlackholeAfter starts an outage of BlackholeFor, both directions,
	// once the client has received this many bytes; zero for none.
	BlackholeAfter int64
	BlackholeFor   time.Duration
}

// tap wraps the client's UDP socket: it counts packets, sees versions,
// Retry and Version Negotiation, keeps 1-RTT headers to find key updates,
// traces datagrams and applies an impairment.
type tap struct {
	net.PacketConn
	start time.Time
	imp   impairment
	// scidLen is the client's connection ID length: the Destination
	// Connection ID length of the server's short header packets.
	scidLen int

	mu  sync.Mutex
	rng *rand.Rand
	st  tapStats
	// serverSCIDLen is the server's connection ID length, learnt from
	// its first long header packet: the length of the client's short
	// header Destination Connection IDs.
	serverSCIDLen int
	sawServer     bool
	sent, recv    []shortHeader
	trace         []traceEntry
	blackholeEnd  time.Time
	keys          keyLog
}

type tapStats struct {
	SentDatagrams int      `json:"sent_datagrams"`
	SentBytes     int64    `json:"sent_bytes"`
	RecvDatagrams int      `json:"recv_datagrams"`
	RecvBytes     int64    `json:"recv_bytes"`
	Versions      []uint32 `json:"versions,omitempty"`
	// Sent and Received count long header packets by type name.
	Sent     map[string]int `json:"sent"`
	Received map[string]int `json:"received"`
	Retries  int            `json:"retries"`
	VN       int            `json:"version_negotiations"`
	// Dropped and Corrupted count what the impairment did.
	Dropped   int `json:"dropped"`
	Corrupted int `json:"corrupted"`
}

// shortHeader is a 1-RTT packet's first byte and header protection
// sample, enough to read its key phase once the keys are known.
type shortHeader struct {
	first  byte
	sample [16]byte
}

type traceEntry struct {
	Millis  float64  `json:"ms"`
	Dir     string   `json:"dir"`
	Size    int      `json:"size"`
	Packets []string `json:"packets"`
	Fate    string   `json:"fate,omitempty"`
}

func newTap(pc net.PacketConn, imp impairment, scidLen int, seed uint64) *tap {
	return &tap{
		PacketConn: pc,
		start:      time.Now(),
		imp:        imp,
		scidLen:    scidLen,
		rng:        rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), //nolint:gosec // impairment draws, reproducible by seed
		st: tapStats{
			Sent:     map[string]int{},
			Received: map[string]int{},
		},
	}
}

// WriteTo sends b as the impairment allows.
func (t *tap) WriteTo(b []byte, addr net.Addr) (int, error) {
	return t.writeTo(t.PacketConn, b, addr)
}

// ReadFrom returns the next datagram the impairment lets through.
func (t *tap) ReadFrom(p []byte) (int, net.Addr, error) {
	return t.readFrom(t.PacketConn, p)
}

// tapPath is another socket of a tap's connection, which moved to a new
// local port (quic.DialConfig.NewPath: Chrome's migration): watched,
// counted and impaired as the first.
type tapPath struct {
	net.PacketConn
	t *tap
}

// newPath opens a socket of the tap's network for its connection's next
// path.
func (t *tap) newPath(ctx context.Context) (net.PacketConn, error) {
	addr, ok := t.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil, fmt.Errorf("tap socket address %v is not UDP", t.LocalAddr())
	}
	network, local := "udp4", "0.0.0.0:0"
	if addr.IP.To4() == nil {
		network, local = "udp6", "[::]:0"
	}
	pc, err := (&net.ListenConfig{}).ListenPacket(ctx, network, local)
	if err != nil {
		return nil, err
	}

	return &tapPath{
		PacketConn: pc,
		t:          t,
	}, nil
}

func (p *tapPath) WriteTo(b []byte, addr net.Addr) (int, error) {
	return p.t.writeTo(p.PacketConn, b, addr)
}

func (p *tapPath) ReadFrom(b []byte) (int, net.Addr, error) {
	return p.t.readFrom(p.PacketConn, b)
}

func (t *tap) writeTo(pc net.PacketConn, b []byte, addr net.Addr) (int, error) {
	now := time.Now()
	t.mu.Lock()
	pkts := t.parse(b, true)
	fate := t.fate(now)
	t.st.SentDatagrams++
	t.st.SentBytes += int64(len(b))
	t.record(now, "out", len(b), pkts, fate)
	delay := t.imp.Delay
	t.mu.Unlock()
	switch fate {
	case "dropped":
		return len(b), nil
	case "corrupted":
		b = t.corrupt(b)
	}
	if delay > 0 {
		c := bytes.Clone(b)
		time.AfterFunc(delay, func() {
			_, _ = pc.WriteTo(c, addr)
		})

		return len(b), nil
	}

	return pc.WriteTo(b, addr)
}

// readFrom returns the next datagram from pc that the impairment passes.
func (t *tap) readFrom(pc net.PacketConn, p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := pc.ReadFrom(p)
		if err != nil {
			return n, addr, err
		}
		now := time.Now()
		t.mu.Lock()
		t.st.RecvDatagrams++
		t.st.RecvBytes += int64(n)
		fate := t.fate(now)
		var pkts []string
		if fate != "dropped" {
			if fate == "corrupted" {
				t.flip(p[:n])
			}
			pkts = t.parse(p[:n], false)
		}
		t.record(now, "in", n, pkts, fate)
		if t.imp.BlackholeAfter > 0 && t.blackholeEnd.IsZero() && t.st.RecvBytes >= t.imp.BlackholeAfter {
			t.blackholeEnd = now.Add(t.imp.BlackholeFor)
		}
		t.mu.Unlock()
		if fate == "dropped" {
			continue
		}

		return n, addr, nil
	}
}

// fate draws what happens to a datagram; t.mu must be held.
func (t *tap) fate(now time.Time) string {
	switch {
	case !t.blackholeEnd.IsZero() && now.Before(t.blackholeEnd):
		t.st.Dropped++
		return "dropped"
	case t.imp.Drop > 0 && t.rng.Float64() < t.imp.Drop:
		t.st.Dropped++
		return "dropped"
	case t.imp.Corrupt > 0 && t.rng.Float64() < t.imp.Corrupt:
		t.st.Corrupted++
		return "corrupted"
	}

	return ""
}

// corrupt returns a copy of b with one byte flipped.
func (t *tap) corrupt(b []byte) []byte {
	c := bytes.Clone(b)
	t.mu.Lock()
	t.flip(c)
	t.mu.Unlock()

	return c
}

// flip inverts one random byte of b; t.mu must be held.
func (t *tap) flip(b []byte) {
	if len(b) > 0 {
		b[t.rng.IntN(len(b))] ^= 0xff
	}
}

// record appends a datagram to the trace; t.mu must be held.
func (t *tap) record(now time.Time, dir string, size int, pkts []string, fate string) {
	t.trace = append(t.trace, traceEntry{
		Millis:  float64(now.Sub(t.start).Microseconds()) / 1000,
		Dir:     dir,
		Size:    size,
		Packets: pkts,
		Fate:    fate,
	})
}

// parse walks the packets coalesced in datagram b, counting them and
// keeping 1-RTT headers; sent says who sent it. t.mu must be held.
func (t *tap) parse(b []byte, sent bool) []string {
	var names []string
	for len(b) > 0 {
		if b[0]&0x40 == 0 {
			// No fixed bit: what follows the packets is padding
			// (Firefox fills its Initial datagrams with zeros).
			return append(names, "padding")
		}
		if b[0]&0x80 == 0 {
			names = append(names, "1rtt")
			t.keepShort(b, sent)

			return names
		}
		if len(b) < 7 {
			return append(names, "short-long")
		}
		version := binary.BigEndian.Uint32(b[1:5])
		dcidLen := int(b[5])
		off := 6 + dcidLen
		if off >= len(b) {
			return append(names, "truncated")
		}
		scidLen := int(b[off])
		off += 1 + scidLen
		if version == 0 {
			if !sent {
				t.st.VN++
			}

			return append(names, "vn")
		}
		if !sent {
			t.noteVersion(version)
			if !t.sawServer {
				t.sawServer = true
				t.serverSCIDLen = scidLen
			}
		}
		typ := int(b[0]>>4) & 3
		if version == 0x6b3343cf {
			// QUIC version 2 rotates the types (RFC 9369, section 3.2).
			typ = (typ + 3) & 3
		}
		name := [4]string{"initial", "0rtt", "handshake", "retry"}[typ]
		names = append(names, name)
		if sent {
			t.st.Sent[name]++
		} else {
			t.st.Received[name]++
		}
		if typ == typeRetry {
			if !sent {
				t.st.Retries++
			}

			return names
		}
		if typ == typeInitial {
			n, l := readVarint(b[min(off, len(b)):])
			if l == 0 {
				return names
			}
			off += l + int(n)
		}
		n, l := readVarint(b[min(off, len(b)):])
		if l == 0 {
			return names
		}
		end := off + l + int(n)
		if end > len(b) {
			return names
		}
		b = b[end:]
	}

	return names
}

// noteVersion records a version the server used; t.mu must be held.
func (t *tap) noteVersion(v uint32) {
	if slices.Contains(t.st.Versions, v) {
		return
	}
	t.st.Versions = append(t.st.Versions, v)
}

// keepShort keeps a 1-RTT packet's protected first byte and sample;
// t.mu must be held.
func (t *tap) keepShort(b []byte, sent bool) {
	dcidLen := t.scidLen
	if sent {
		dcidLen = t.serverSCIDLen
	}
	pnOff := 1 + dcidLen
	if len(b) < pnOff+4+16 {
		return
	}
	h := shortHeader{
		first: b[0],
	}
	copy(h.sample[:], b[pnOff+4:pnOff+20])
	if sent {
		t.sent = append(t.sent, h)
	} else {
		t.recv = append(t.recv, h)
	}
}

func (t *tap) stats() tapStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.st
	st.Versions = append([]uint32(nil), t.st.Versions...)
	st.Sent = map[string]int{}
	st.Received = map[string]int{}
	maps.Copy(st.Sent, t.st.Sent)
	maps.Copy(st.Received, t.st.Received)

	return st
}

// keyUpdates counts key phase changes in the server's and client's 1-RTT
// packets, using header protection keys from the key log; ok is false
// when the log lacks the suite's secrets.
func (t *tap) keyUpdates(suite uint16) (server, client int, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// A connection that ends in v2 protects its 1-RTT headers with v2's
	// label (RFC 9369, section 3.3.2).
	label := "quic hp"
	if slices.Contains(t.st.Versions, 0x6b3343cf) {
		label = "quicv2 hp"
	}
	sk, err1 := hpMask(suite, label, t.keys.server)
	ck, err2 := hpMask(suite, label, t.keys.client)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}

	return phaseChanges(t.recv, sk), phaseChanges(t.sent, ck), true
}

// phaseChanges counts the key phase changes in hs, a packet whose phase
// differs from the one before it and the one after it (reordering
// around a key update) not counted.
func phaseChanges(hs []shortHeader, mask func([16]byte) byte) int {
	phases := make([]byte, len(hs))
	for i, h := range hs {
		phases[i] = ((h.first ^ (mask(h.sample) & 0x1f)) >> 2) & 1
	}
	n := 0
	cur := byte(0)
	for i, p := range phases {
		if p == cur {
			continue
		}
		if i+1 < len(phases) && phases[i+1] != p {
			continue
		}
		cur = p
		n++
	}

	return n
}

// keyLogWriter is the TLS key log writer for the tap's connection:
// every line goes to w (when set), and the 1-RTT secrets are kept.
func (t *tap) keyLogWriter(w io.Writer) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		t.mu.Lock()
		t.keys.add(p)
		t.mu.Unlock()
		if w != nil {
			// A key log error would fail the handshake: the file is a
			// convenience, never a reason to.
			_, _ = w.Write(p)
		}

		return len(p), nil
	})
}

func (t *tap) writeTrace(path string) error {
	t.mu.Lock()
	data, err := json.Marshal(t.trace)
	t.mu.Unlock()
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) {
	return f(p)
}

// keyLog keeps the 1-RTT traffic secrets of an NSS key log.
type keyLog struct {
	client, server []byte
}

func (k *keyLog) add(p []byte) {
	sc := bufio.NewScanner(bytes.NewReader(p))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		secret, err := hex.DecodeString(f[2])
		if err != nil {
			continue
		}
		switch f[0] {
		case "CLIENT_TRAFFIC_SECRET_0":
			k.client = secret
		case "SERVER_TRAFFIC_SECRET_0":
			k.server = secret
		}
	}
}

// hpMask returns the header protection mask function of a 1-RTT secret
// for a TLS 1.3 cipher suite (RFC 9001, section 5.4), with the version's
// hp label.
func hpMask(suite uint16, label string, secret []byte) (func([16]byte) byte, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("no secret")
	}
	var newHash func() hash.Hash
	keyLen := 16
	switch suite {
	case 0x1301:
		newHash = sha256.New
	case 0x1302:
		newHash, keyLen = sha512.New384, 32
	case 0x1303:
		newHash, keyLen = sha256.New, 32
	default:
		return nil, fmt.Errorf("cipher suite %#04x", suite)
	}
	key, err := expandLabel(newHash, secret, label, keyLen)
	if err != nil {
		return nil, err
	}
	if suite == 0x1303 {
		return func(sample [16]byte) byte {
			c, err := chacha20.NewUnauthenticatedCipher(key, sample[4:16])
			if err != nil {
				return 0
			}
			c.SetCounter(binary.LittleEndian.Uint32(sample[:4]))
			var m [1]byte
			c.XORKeyStream(m[:], m[:])

			return m[0]
		}, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	return func(sample [16]byte) byte {
		var m [16]byte
		block.Encrypt(m[:], sample[:])

		return m[0]
	}, nil
}

// expandLabel is TLS 1.3's HKDF-Expand-Label with an empty context.
func expandLabel(newHash func() hash.Hash, secret []byte, label string, n int) ([]byte, error) {
	full := "tls13 " + label
	info := make([]byte, 0, 4+len(full))
	info = binary.BigEndian.AppendUint16(info, uint16(n))
	info = append(info, byte(len(full)))
	info = append(info, full...)
	info = append(info, 0)
	out := make([]byte, n)
	if _, err := io.ReadFull(hkdf.Expand(newHash, secret, info), out); err != nil {
		return nil, err
	}

	return out, nil
}

// readVarint reads a QUIC variable-length integer, returning it and its
// length (0 when b is too short).
func readVarint(b []byte) (uint64, int) {
	if len(b) == 0 {
		return 0, 0
	}
	l := 1 << (b[0] >> 6)
	if len(b) < l {
		return 0, 0
	}
	v := uint64(b[0] & 0x3f)
	for _, c := range b[1:l] {
		v = v<<8 | uint64(c)
	}

	return v, l
}
