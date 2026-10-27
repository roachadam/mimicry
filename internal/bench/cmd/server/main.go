// Command server is the benchmark's Go server: net/http for HTTP/1.1 and
// HTTP/2, quic-go's http3 for HTTP/3, serving package site's endpoints with
// the same ports, headers and bytes as the nginx configuration in
// ../../docker/nginx.
//
//	/bytes/{n}     n fixed incompressible bytes
//	/json          a small JSON document
//	POST /echo     the request body back
//	/page/...      the page's HTML and subresources
//
// Every response says whether its TLS session was resumed (x-tls-resumed:
// r for resumed, . for a full handshake, as nginx's $ssl_session_reused).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/roachadam/mimicry/internal/bench/site"
	"github.com/roachadam/mimicry/internal/bench/stats"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// maxStreams is the concurrent-stream limit over HTTP/2 and HTTP/3, above
// the highest concurrency the runner uses (nginx is configured the same).
const maxStreams = 1000

func main() {
	dir := flag.String("ca", target.CADir(), "directory with the server certificate and key")

	flag.Parse()

	srv, err := target.ServerNamed("go")
	if err != nil {
		log.Fatal(err)
	}

	cert, err := tls.LoadX509KeyPair(filepath.Join(*dir, target.CertFile), filepath.Join(*dir, target.KeyFile))
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err = run(ctx, srv, cert)

	stop()

	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, srv target.Server, cert tls.Certificate) error {
	var counters stats.Counters

	altSvc := `h3=":` + strconv.Itoa(srv.TLSPort) + `"; ma=86400`
	content := handler()

	main := &http.Server{
		Addr:              ":" + strconv.Itoa(srv.TLSPort),
		Handler:           withAltSvc(content, altSvc),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
		HTTP2: &http.HTTP2Config{
			MaxConcurrentStreams: maxStreams,
		},
		ConnState: countNew(&counters),
	}

	h1 := &http.Server{
		Addr:              ":" + strconv.Itoa(srv.H1Port),
		Handler:           content,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
		Protocols: http1Only(),
		ConnState: countNew(&counters),
	}

	h3 := &http3.Server{
		Addr:    ":" + strconv.Itoa(srv.TLSPort),
		Handler: content,
		TLSConfig: http3.ConfigureTLSConfig(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		}),
		QUICConfig: &quic.Config{
			Allow0RTT:          true,
			MaxIncomingStreams: maxStreams,
		},
		ConnContext: func(ctx context.Context, _ *quic.Conn) context.Context {
			counters.QUIC.Add(1)

			return ctx
		},
	}

	statsSrv := &http.Server{
		Addr:              ":" + strconv.Itoa(srv.StatsPort),
		Handler:           stats.Handler(&counters),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errs := make(chan error, 4)

	go func() {
		errs <- main.ListenAndServeTLS("", "")
	}()
	go func() {
		errs <- h1.ListenAndServeTLS("", "")
	}()
	go func() {
		errs <- h3.ListenAndServe()
	}()
	go func() {
		errs <- statsSrv.ListenAndServe()
	}()

	log.Printf("go server: TLS :%d (h2, h1, h3 over UDP), h1-only :%d, stats :%d", srv.TLSPort, srv.H1Port, srv.StatsPort)

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return errors.Join(main.Shutdown(shutdown), h1.Shutdown(shutdown), h3.Shutdown(shutdown), statsSrv.Shutdown(shutdown))
}

func http1Only() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)

	return p
}

func countNew(c *stats.Counters) func(net.Conn, http.ConnState) {
	return func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			c.TCP.Add(1)
		}
	}
}

// withAltSvc advertises HTTP/3 on the TLS port's TCP responses.
func withAltSvc(h http.Handler, altSvc string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", altSvc)
		h.ServeHTTP(w, r)
	})
}

// handler serves package site's endpoints.
func handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /bytes/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n < 0 || n > site.MaxPayload {
			http.Error(w, "bad size", http.StatusBadRequest)

			return
		}

		write(w, r, "application/octet-stream", site.Payload(n))
	})

	mux.HandleFunc("GET /json", func(w http.ResponseWriter, r *http.Request) {
		write(w, r, "application/json", []byte(site.JSON))
	})

	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, site.MaxPayload))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		ct := r.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/octet-stream"
		}

		write(w, r, ct, body)
	})

	for _, res := range site.Resources() {
		body := res.Body()
		ct := res.ContentType()

		mux.HandleFunc("GET "+res.Path, func(w http.ResponseWriter, r *http.Request) {
			write(w, r, ct, body)
		})
	}

	return mux
}

func write(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cache-Control", "no-store")

	resumed := "."
	if r.TLS != nil && r.TLS.DidResume {
		resumed = "r"
	}

	h.Set("X-Tls-Resumed", resumed)
	h.Set("X-Proto", r.Proto)

	_, _ = w.Write(body)
}
