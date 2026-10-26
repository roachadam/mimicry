package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// serveHTTP1 answers HTTP/1.1 requests on conn as sc says, keeping the
// connection open, and records each request verbatim into c.H1 and the
// page log. The first request's headers become c's profile headers
// (lowercased; the verbatim lines keep the casing). When the client asks for
// /done, c gets the whole page load from the log, its profile headers come
// from the page's first request, and emit hands it over. host is the
// connection's server name.
func serveHTTP1(conn net.Conn, host string, c *capture.Capture, sc *Scenario, log *pageLog, emit func()) error {
	id := log.newConn()
	br := bufio.NewReader(conn)
	for {
		line, err := readLine(br)
		if err != nil {
			return err
		}
		method, rest, ok := strings.Cut(line, " ")
		target, _, ok2 := strings.Cut(rest, " ")
		if !ok || !ok2 {
			return fmt.Errorf("bad request line %q", line)
		}
		// A request to a proxy names the whole URL (absolute form). Through
		// the capture proxy a browser also sends its own background requests
		// (update checks, captive-portal probes): those are answered 404 and
		// not recorded.
		foreign := false
		// absolute: a request sent to a proxy whole, which a ProxyAuth
		// scenario's proxy challenges (the capture server answers for it).
		absolute := false
		if u, err := url.Parse(target); err == nil && u.IsAbs() {
			target = u.RequestURI()
			foreign = !strings.HasSuffix(u.Hostname(), ".test")
			absolute = true
		}
		req := capture.H1Request{
			Conn: id,
			Host: host,
			Line: line,
		}
		ask := request{
			method: method,
			path:   target,
		}
		var length int64
		chunked := false
		proxyAuth := ""
		for {
			h, err := readLine(br)
			if err != nil {
				return err
			}
			if h == "" {
				break
			}
			name, value, ok := strings.Cut(h, ":")
			if !ok {
				return fmt.Errorf("bad header line %q", h)
			}
			req.Headers = append(req.Headers, h)
			value = strings.TrimSpace(value)
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "content-length":
				length, _ = strconv.ParseInt(value, 10, 64)
			case "transfer-encoding":
				chunked = strings.Contains(strings.ToLower(value), "chunked")
			case "host":
				ask.host = value
			case "origin":
				ask.origin = value
			case "access-control-request-headers":
				ask.preflightHeaders = value
			case "proxy-authorization":
				proxyAuth = value
			case "upgrade":
				ask.upgrade = value
			case "sec-websocket-key":
				ask.wsKey = value
			case "sec-websocket-protocol":
				ask.wsProtocols = joinValues(ask.wsProtocols, value)
			case "sec-websocket-extensions":
				ask.wsExtensions = joinValues(ask.wsExtensions, value)
			}
		}
		// A plain connection has no server name: the Host header names
		// the host.
		if req.Host == "" && (sc.Plain || sc.mixedScheme || sc.plainPort) {
			req.Host, _, _ = strings.Cut(ask.host, ":")
		}
		index := -1
		if !foreign {
			index = log.add(req)
			log.request(c)
			// After /done the capture belongs to its receiver.
			if !c.Finished {
				c.H1 = append(c.H1, req)
				if len(c.Profile.Headers) == 0 {
					c.Profile.Headers = profileHeaders(req.Headers)
				}
			}
		}

		var uploaded int64
		switch {
		case chunked:
			if uploaded, err = io.Copy(io.Discard, httputil.NewChunkedReader(br)); err != nil {
				return err
			}
			// The chunked reader stops at the last chunk; the trailer
			// section ends with an empty line.
			for {
				t, err := readLine(br)
				if err != nil {
					return err
				}
				if t == "" {
					break
				}
			}
		case length > 0:
			if uploaded, err = io.CopyN(io.Discard, br, length); err != nil {
				return err
			}
		}

		ask.uploaded = int(uploaded)
		if !foreign {
			sc.noteWSResult(log, ask, host)
		}
		r := sc.answer(ask)
		challenged := false
		switch {
		case foreign:
			r = response{
				status:      http.StatusNotFound,
				contentType: "text/plain",
			}
		case sc.ProxyAuth && absolute && proxyAuth != ProxyAuthorization():
			challenged = true
			r = response{
				status:      http.StatusProxyAuthRequired,
				contentType: "text/plain",
				headers: []hpack.HeaderField{
					{
						Name:  "Proxy-Authenticate",
						Value: `Basic realm="mimicry capture"`,
					},
				},
			}
		}
		if r.websocket != nil && !foreign && !challenged {
			// The connection is the socket's from here on.
			number := log.connectionNumber(c)
			return serveWebSocketH1(conn, br, r.websocket, ask.wsKey, capture.WebSocket{
				Host:       req.Host,
				Path:       target,
				Protocol:   "http/1.1",
				Plain:      number == 0,
				Connection: number,
				ProxyConn:  c.ProxyConn,
				Request:    index,
			}, log)
		}
		if target == "/done" && !c.Finished && !foreign && !challenged {
			if sc.multi != nil {
				c.Page = log.page()
				c.H1 = nil
				log.pageProfile(c)
			} else {
				c.H1 = log.snapshot()
				c.Profile.Headers = profileHeaders(c.H1[0].Headers)
			}
			c.Finished = true
			emit()
		}
		time.Sleep(r.delay)
		if err := writeResponse(conn, r); err != nil {
			return err
		}
		if r.closeAfter {
			return nil
		}
	}
}

// readLine reads one line, which must end in CRLF, and returns it without
// the CRLF. A line ending in a bare LF is an error rather than recorded as if
// it were CRLF, so a client that sends one fails visibly.
func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	text, ok := strings.CutSuffix(line, "\r\n")
	if !ok {
		return "", fmt.Errorf("line %q does not end in CRLF", line)
	}
	return text, nil
}

// profileHeaders turns verbatim header lines into profile headers. Host is
// kept as a slot without its value: its position matters, while the port
// changes with every capture server.
func profileHeaders(lines []string) []profile.Header {
	out := make([]profile.Header, 0, len(lines))
	for _, l := range lines {
		name, value, _ := strings.Cut(l, ":")
		h := profile.Header{
			Name:  strings.ToLower(strings.TrimSpace(name)),
			Value: strings.TrimSpace(value),
		}
		if h.Name == "host" {
			h.Value = ""
		}
		out = append(out, h)
	}
	return out
}

// writeResponse writes r as an HTTP/1.1 response. The buffered writer keeps
// its first error and returns it from Flush, so only that is checked.
func writeResponse(w io.Writer, r response) error {
	bw := bufio.NewWriter(w)
	_, _ = fmt.Fprintf(bw, "HTTP/1.1 %d %s\r\nContent-Type: %s\r\nContent-Length: %d\r\nCache-Control: no-store\r\n",
		r.status, http.StatusText(r.status), r.contentType, len(r.body))
	if r.closeAfter {
		_, _ = bw.WriteString("Connection: close\r\n")
	}
	for _, h := range r.headers {
		_, _ = fmt.Fprintf(bw, "%s: %s\r\n", h.Name, h.Value)
	}
	_, _ = bw.WriteString("\r\n")
	_, _ = bw.Write(r.body)
	return bw.Flush()
}
