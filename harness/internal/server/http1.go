package server

import (
	"bufio"
	"fmt"
	"net"
	"net/textproto"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// serveHTTP1 records the first HTTP/1.1 request's headers into c and answers
// it, closing the connection afterwards. Header names are lowercased because
// the profile schema does not record HTTP/1.1 casing yet.
func serveHTTP1(conn net.Conn, c *capture.Capture, recorded func()) error {
	r := textproto.NewReader(bufio.NewReader(conn))
	line, err := r.ReadLine()
	if err != nil {
		return err
	}
	if _, _, ok := strings.Cut(line, " "); !ok {
		return fmt.Errorf("bad request line %q", line)
	}
	for {
		line, err := r.ReadLine()
		if err != nil {
			return err
		}
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return fmt.Errorf("bad header line %q", line)
		}
		c.Profile.Headers = append(c.Profile.Headers, profile.Header{
			Name:  strings.ToLower(strings.TrimSpace(name)),
			Value: strings.TrimSpace(value),
		})
	}
	recorded()
	const body = "mimicry capture ok\n"
	_, err = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		len(body), body)
	return err
}
