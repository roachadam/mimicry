package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// aioquicScript is the h3-v2 scenario's HTTP/3 server.
//
//go:embed aioquic_h3.py
var aioquicScript []byte

// NeedsAioquic reports a scenario whose HTTP/3 is aioquic's (h3-v2).
func (sc *Scenario) NeedsAioquic() bool {
	return sc.h3 != nil && sc.h3.aioquic
}

// AioquicAvailable reports why the h3-v2 scenario cannot run here, if it
// cannot: it needs python3 with aioquic (pip install aioquic).
func AioquicAvailable(ctx context.Context) error {
	if out, err := exec.CommandContext(ctx, "python3", "-c", "import aioquic").CombinedOutput(); err != nil {
		return fmt.Errorf("python3 with aioquic (pip install aioquic): %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// serveAioquic serves sc's HTTP/3 with aioquic behind s.PacketConn, which
// relays and records every datagram until ctx is canceled. aioquic logs TLS
// secrets for quicConns; its connections have no H3 record.
func (s *Server) serveAioquic(ctx context.Context, log *pageLog, issue func([]string) (tls.Certificate, error)) error {
	pc := s.PacketConn
	defer func() {
		_ = pc.Close()
	}()

	dir, err := os.MkdirTemp("", "mimicry-aioquic-")
	if err != nil {
		return err
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	cert, err := issue([]string{
		h3Host,
	})
	if err != nil {
		return err
	}

	var certPEM []byte
	for _, der := range cert.Certificate {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: der,
		})...)
	}

	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return err
	}

	files := map[string][]byte{
		"cert.pem": certPEM,
		"key.pem": pem.EncodeToMemory(&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: key,
		}),
		"server.py": aioquicScript,
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return err
		}
	}

	keyLog := filepath.Join(dir, "keys.log")

	log.quic.mu.Lock()

	log.quic.keyLogFile = keyLog
	log.quic.mu.Unlock()

	cmd := exec.CommandContext(ctx, "python3", filepath.Join(dir, "server.py"), //nolint:gosec // our own embedded script
		"--certificate", filepath.Join(dir, "cert.pem"),
		"--private-key", filepath.Join(dir, "key.pem"),
		"--secrets-log", keyLog)

	cmd.Stderr = os.Stderr

	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("aioquic: %w (h3-v2 needs python3 with aioquic: pip install aioquic)", err)
	}

	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		return fmt.Errorf("aioquic did not start: %w (h3-v2 needs python3 with aioquic: pip install aioquic)", err)
	}

	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return fmt.Errorf("aioquic: port %q: %w", line, err)
	}

	upstream := &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: port,
	}

	if s.OnQUICReady != nil {
		s.OnQUICReady()
	}

	return relayUDP(ctx, pc, upstream, &log.quic)
}

// relayUDP relays datagrams between every client address that sends to pc
// and upstream, each client on a socket of its own (so the server sees
// one address per client), recording both directions in q, until ctx is
// canceled.
func relayUDP(ctx context.Context, pc net.PacketConn, upstream *net.UDPAddr, q *quicLog) error {
	stop := context.AfterFunc(ctx, func() {
		_ = pc.Close()
	})
	defer stop()

	var mu sync.Mutex

	ups := map[string]*net.UDPConn{}

	var wg sync.WaitGroup

	defer func() {
		mu.Lock()

		for _, u := range ups {
			_ = u.Close()
		}

		mu.Unlock()
		wg.Wait()
	}()

	buf := make([]byte, 1<<16)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}

			return err
		}

		client := addr.String()
		if q.received(client, time.Now(), buf[:n]) {
			continue
		}

		mu.Lock()

		up, ok := ups[client]
		if !ok {
			up, err = net.DialUDP("udp", nil, upstream)
			if err != nil {
				mu.Unlock()

				return err
			}

			ups[client] = up
			wg.Go(func() {
				b := make([]byte, 1<<16)
				for {
					m, err := up.Read(b)
					if err != nil {
						return
					}

					if !q.sent(client, time.Now(), b[:m]) {
						_, _ = pc.WriteTo(b[:m], addr)
					}
				}
			})
		}

		mu.Unlock()

		_, _ = up.Write(buf[:n])
	}
}
