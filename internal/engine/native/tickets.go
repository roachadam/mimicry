package native

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/roachadam/mimicry/internal/handshake"
)

const (
	// viaSep starts a pool key's proxy (route).
	viaSep = " via "
	// h3Suffix ends an HTTP/3 pool key (h3PoolKey).
	h3Suffix = " h3"
	// proxyHashPrefix starts a proxy's hash in a ticket key.
	proxyHashPrefix = "sha256:"
)

// ticketKey is the session ticket key of the pool key key: key with its
// proxy URL, credentials included, replaced by a hash of it. Tickets stay
// apart per proxy, and a saved store (Config.Tickets) carries no proxy
// credentials. Direct keys are returned as they are.
func ticketKey(key string) string {
	base, rest, ok := strings.Cut(key, viaSep)
	if !ok {
		return key
	}

	via, suffix, _ := strings.Cut(rest, " ")
	if suffix != "" {
		suffix = " " + suffix
	}

	return base + viaSep + proxyHash(via) + suffix
}

// proxyHash names the proxy URL via in a ticket key: the first 16 bytes
// of its SHA-256, in hex.
func proxyHash(via string) string {
	sum := sha256.Sum256([]byte(via))

	return proxyHashPrefix + hex.EncodeToString(sum[:16])
}

// savedTicket is a session ticket as WriteTickets writes it.
type savedTicket struct {
	// Browser is the browser whose engines received it (the store's,
	// handshake.Store.Bind), if known.
	Browser string `json:"browser,omitempty"`
	// Origin is "https://host:port".
	Origin string `json:"origin"`
	// Partition is the connection partition it was received in, if any.
	Partition string `json:"partition,omitempty"`
	// Proxy is the proxy it was received through, as a hash of its URL
	// (proxyHash), if any.
	Proxy string `json:"proxy,omitempty"`
	// QUIC: received on an HTTP/3 connection.
	QUIC    bool      `json:"quic,omitempty"`
	Expires time.Time `json:"expires,omitzero"`
	// ClientCert: a client certificate was sent in the session, so a
	// connection resuming it carries that identity and serves its own
	// host alone.
	ClientCert bool   `json:"clientCert,omitempty"`
	Ticket     []byte `json:"ticket"`
	State      []byte `json:"state"`
}

// WriteTickets writes the tickets of s usable at now as a JSON array, by
// origin, partition, proxy and protocol: what ReadTickets reads back.
func WriteTickets(w io.Writer, s *handshake.Store, now time.Time) error {
	saved, err := s.Export(now)
	if err != nil {
		return fmt.Errorf("tickets: %w", err)
	}

	browser := s.Browser()

	entries := make([]savedTicket, 0, len(saved))
	for _, sv := range saved {
		e := savedTicket{
			Browser:    browser,
			Expires:    sv.Expires,
			ClientCert: sv.Identified,
			Ticket:     sv.Ticket,
			State:      sv.State,
		}

		key, quic := strings.CutSuffix(sv.Key, h3Suffix)

		e.QUIC = quic

		key, e.Proxy, _ = strings.Cut(key, viaSep)
		e.Origin, e.Partition, _ = strings.Cut(key, " ")

		entries = append(entries, e)
	}

	slices.SortFunc(entries, func(a, b savedTicket) int {
		return cmp.Or(
			cmp.Compare(a.Origin, b.Origin),
			cmp.Compare(a.Partition, b.Partition),
			cmp.Compare(a.Proxy, b.Proxy),
			compareBool(a.QUIC, b.QUIC),
		)
	})

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(entries)
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

// ReadTickets adds to s the tickets of a JSON array WriteTickets wrote
// that are still usable at now, replacing those under the same key. A
// malformed entry, entries of two browsers, or entries of another browser
// than s's fail it before anything is added.
func ReadTickets(r io.Reader, s *handshake.Store, now time.Time) error {
	var entries []savedTicket
	if err := json.NewDecoder(r).Decode(&entries); err != nil {
		return fmt.Errorf("tickets: %w", err)
	}

	var browser string

	saved := make([]handshake.Saved, len(entries))
	for i, e := range entries {
		key, err := e.key()
		if err != nil {
			return fmt.Errorf("tickets: entry %d: %w", i, err)
		}

		switch {
		case browser == "":
			browser = e.Browser
		case e.Browser != "" && e.Browser != browser:
			return fmt.Errorf("tickets: entry %d: browser %q, entries before it %q: one identity's tickets are one browser's", i, e.Browser, browser)
		}

		saved[i] = handshake.Saved{
			Key:        key,
			Expires:    e.Expires,
			Ticket:     e.Ticket,
			State:      e.State,
			Identified: e.ClientCert,
		}
	}

	if err := s.Import(saved, browser, now); err != nil {
		return fmt.Errorf("tickets: %w", err)
	}

	return nil
}

// key is the ticket key e was saved under, or what makes e unusable: an
// origin that is not "https://host:port", or a proxy that is not a hash.
func (e savedTicket) key() (string, error) {
	hostPort, ok := strings.CutPrefix(e.Origin, "https://")
	if !ok {
		return "", fmt.Errorf("origin %q is not https://host:port", e.Origin)
	}

	host, port, err := net.SplitHostPort(hostPort)
	if err != nil || host == "" || port == "" || strings.ContainsAny(hostPort, " /") {
		return "", fmt.Errorf("origin %q is not https://host:port", e.Origin)
	}

	key, _ := poolKey("https", host, port, e.Partition)

	if e.Proxy != "" {
		h, ok := strings.CutPrefix(e.Proxy, proxyHashPrefix)
		if _, err := hex.DecodeString(h); !ok || err != nil || len(h) != 32 {
			return "", fmt.Errorf("proxy %q is not a hash of its URL", e.Proxy)
		}

		key += viaSep + e.Proxy
	}

	if e.QUIC {
		key += h3Suffix
	}

	return key, nil
}
