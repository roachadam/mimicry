package capture

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
)

// H1Request is one HTTP/1.1 request as the client sent it, casing and order
// intact.
type H1Request struct {
	// Conn numbers the connection it came on, from 1 in order of arrival:
	// browsers spread an HTTP/1.1 page over several.
	Conn int `json:"conn"`
	// Host is the connection's server name, recorded when a page spans
	// several hosts (replay scenarios).
	Host string `json:"host,omitempty"`
	// Line is the request line ("GET / HTTP/1.1").
	Line string `json:"line"`
	// Headers are the header lines, verbatim ("User-Agent: ...").
	Headers []string `json:"headers"`
}

// Page is a whole page load across every connection and host it used:
// each HTTP/2 connection's trace and every HTTP/1.1 request.
type Page struct {
	H2 []*h2trace.Trace `json:"h2"`
	H1 []H1Request      `json:"h1,omitempty"`
}

// LoadPages reads a replayed page capture written by Write
// (<name>.<scenario>.pagetrace.json.gz): every sample's page.
func LoadPages(path string) ([]*Page, error) {
	f, err := os.Open(path) //nolint:gosec // a capture file the caller names
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()
	r, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var pages []*Page
	if err := json.NewDecoder(r).Decode(&pages); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return pages, nil
}

func gzipped(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
