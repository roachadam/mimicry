package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/textproto"
	"strconv"
	"strings"

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
	tp := textproto.NewReader(br)
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return err
		}
		method, rest, ok := strings.Cut(line, " ")
		target, _, ok2 := strings.Cut(rest, " ")
		if !ok || !ok2 {
			return fmt.Errorf("bad request line %q", line)
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
		for {
			h, err := tp.ReadLine()
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
			}
		}
		log.add(req)
		// After /done the capture belongs to its receiver.
		if !c.Finished {
			c.H1 = append(c.H1, req)
			if len(c.Profile.Headers) == 0 {
				c.Profile.Headers = profileHeaders(req.Headers)
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
				t, err := tp.ReadLine()
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
		r := sc.answer(ask)
		if target == "/done" && !c.Finished {
			if sc.replay != nil {
				c.Page = log.page()
				c.H1 = nil
				pageProfile(c)
			} else {
				c.H1 = log.snapshot()
				c.Profile.Headers = profileHeaders(c.H1[0].Headers)
			}
			c.Finished = true
			emit()
		}
		if err := writeResponse(conn, r); err != nil {
			return err
		}
	}
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
	for _, h := range r.headers {
		_, _ = fmt.Fprintf(bw, "%s: %s\r\n", h.Name, h.Value)
	}
	_, _ = bw.WriteString("\r\n")
	_, _ = bw.Write(r.body)
	return bw.Flush()
}
