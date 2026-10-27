// Command proxy is the benchmark's proxy: HTTP CONNECT without credentials
// (target.ProxyPort), HTTP CONNECT asking for Basic credentials
// (target.ProxyAuthPort; a request without them, or with wrong ones, gets a
// 407 and the connection stays open for the retry, as browsers expect), and
// SOCKS5 without authentication (target.ProxySOCKSPort, CONNECT only). It
// only relays bytes; its container gets its own netem delay
// (../../docker/proxy-entrypoint.sh).
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/roachadam/mimicry/internal/bench/stats"
	"github.com/roachadam/mimicry/internal/bench/target"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx)

	stop()

	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(target.ProxyUser+":"+target.ProxyPassword))
	errs := make(chan error, 4)

	serve := func(port int, handle func(net.Conn)) {
		var lc net.ListenConfig

		ln, err := lc.Listen(ctx, "tcp", ":"+strconv.Itoa(port))
		if err != nil {
			errs <- err

			return
		}

		go func() {
			<-ctx.Done()

			_ = ln.Close()
		}()

		for {
			c, err := ln.Accept()
			if err != nil {
				errs <- err

				return
			}

			go handle(c)
		}
	}

	go serve(target.ProxyPort, func(c net.Conn) {
		connect(c, "")
	})
	go serve(target.ProxyAuthPort, func(c net.Conn) {
		connect(c, auth)
	})
	go serve(target.ProxySOCKSPort, socks5)

	statsSrv := &http.Server{
		Addr:              ":" + strconv.Itoa(target.ProxyStatsPort),
		Handler:           stats.Handler(nil),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		errs <- statsSrv.ListenAndServe()
	}()

	log.Printf("proxy: CONNECT :%d, CONNECT with Basic auth :%d, SOCKS5 :%d, stats :%d",
		target.ProxyPort, target.ProxyAuthPort, target.ProxySOCKSPort, target.ProxyStatsPort)

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		return nil
	}
}

// connect serves HTTP CONNECT on c. With auth set, a request without that
// Proxy-Authorization gets a 407 and the connection waits for the next one.
func connect(c net.Conn, auth string) {
	defer func() {
		_ = c.Close()
	}()

	br := bufio.NewReader(c)

	for {
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))

		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}

		_ = c.SetReadDeadline(time.Time{})

		if req.Method != http.MethodConnect {
			_, _ = io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")

			return
		}

		if auth != "" && req.Header.Get("Proxy-Authorization") != auth {
			_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\n"+
				"Proxy-Authenticate: Basic realm=\"bench\"\r\nContent-Length: 0\r\n\r\n")

			continue
		}

		up, err := dial(req.Host)
		if err != nil {
			_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")

			return
		}

		if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			_ = up.Close()

			return
		}

		relay(c, br, up)

		return
	}
}

// socks5 serves a SOCKS5 CONNECT without authentication (RFC 1928).
func socks5(c net.Conn) {
	defer func() {
		_ = c.Close()
	}()

	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))

	br := bufio.NewReader(c)

	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil || hdr[0] != 5 {
		return
	}

	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}

	// 0x00: no authentication required.
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}

	var req [4]byte
	if _, err := io.ReadFull(br, req[:]); err != nil || req[0] != 5 {
		return
	}

	if req[1] != 1 { // CONNECT only
		_, _ = c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})

		return
	}

	host, err := socksAddr(br, req[3])
	if err != nil {
		return
	}

	var port [2]byte
	if _, err := io.ReadFull(br, port[:]); err != nil {
		return
	}

	_ = c.SetReadDeadline(time.Time{})

	up, err := dial(net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))))
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})

		return
	}

	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		_ = up.Close()

		return
	}

	relay(c, br, up)
}

func socksAddr(br *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 1:
		var ip [4]byte
		if _, err := io.ReadFull(br, ip[:]); err != nil {
			return "", err
		}

		return net.IP(ip[:]).String(), nil

	case 3:
		n, err := br.ReadByte()
		if err != nil {
			return "", err
		}

		name := make([]byte, n)
		if _, err := io.ReadFull(br, name); err != nil {
			return "", err
		}

		return string(name), nil

	case 4:
		var ip [16]byte
		if _, err := io.ReadFull(br, ip[:]); err != nil {
			return "", err
		}

		return net.IP(ip[:]).String(), nil
	}

	return "", errors.New("unknown address type")
}

func dial(addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var d net.Dialer

	return d.DialContext(ctx, "tcp", addr)
}

// relay copies both ways until either side closes. br holds what the
// client sent after its request.
func relay(c net.Conn, br *bufio.Reader, up net.Conn) {
	done := make(chan struct{}, 2)

	go func() {
		_, _ = br.WriteTo(up)

		if tc, ok := up.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}

		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(c, up)

		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}

		done <- struct{}{}
	}()

	<-done
	<-done

	_ = up.Close()
}
