package mimicry_test

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profiles"
)

// sentNames records, by path, the header names each request arrived with,
// lowercase and in order.
type sentNames struct {
	mu    sync.Mutex
	paths map[string][]string
}

func (s *sentNames) record(path string, lines []string) {
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		name, _, _ := strings.Cut(line, ":")
		names = append(names, strings.ToLower(name))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths[path] = names
}

func (s *sentNames) get(path string) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, ok := s.paths[path]
	return names, ok
}

// startH2Recorder starts a raw HTTP/2 server for the shop's names that
// records each request's fields (pseudo-headers aside) in s and answers
// 200; Go's own server keeps no order. It returns the shop to dial it.
func startH2Recorder(t *testing.T, s *sentNames) *shop {
	t.Helper()
	cert, pool := shopCertificate()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	tl := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{
			cert,
		},
		NextProtos: []string{
			"h2",
		},
	})
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			go serveH2(c, s)
		}
	}()
	return &shop{
		addr:    ln.Addr().String(),
		RootCAs: pool,
	}
}

func serveH2(c net.Conn, s *sentNames) {
	defer func() {
		_ = c.Close()
	}()
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(c, preface); err != nil {
		return
	}
	fr := http2.NewFramer(c, c)
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if err := fr.WriteSettings(); err != nil {
		return
	}
	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				if err := fr.WriteSettingsAck(); err != nil {
					return
				}
			}
		case *http2.MetaHeadersFrame:
			var lines []string
			for _, hf := range f.RegularFields() {
				lines = append(lines, hf.Name+": "+hf.Value)
			}
			s.record(f.PseudoValue("path"), lines)
			block.Reset()
			if err := enc.WriteField(hpack.HeaderField{
				Name:  ":status",
				Value: "200",
			}); err != nil {
				return
			}
			if err := fr.WriteHeaders(http2.HeadersFrameParam{
				StreamID:      f.StreamID,
				BlockFragment: block.Bytes(),
				EndStream:     true,
				EndHeaders:    true,
			}); err != nil {
				return
			}
		}
	}
}

// Header placement over HTTP/1.1 and HTTP/2: each placed request against
// the same request sent without placements. A placed request differs only
// where its runs say (the moved headers, directly where they were put);
// an invalid Place fails NewTransport or the request, and a header or
// anchor the request does not send fails it with nothing sent. HTTP/3 is
// covered in internal/engine/native (TestHeaderPlacementOverHTTP3), where
// a QUIC server can be reached.
func TestHeaderPlacement(t *testing.T) {
	sent := &sentNames{
		paths: map[string][]string{},
	}
	h1 := startShopHTTP1(func(_ http.ResponseWriter, r *http.Request) {
		sent.record(r.URL.Path, headerLines(r))
	})
	t.Cleanup(h1.Close)
	h2 := startH2Recorder(t, sent)
	apiKey := http.Header{
		"X-Api-Key": {"k"},
	}

	tests := []struct {
		name string
		h2   bool
		// headers are Config.Headers; config Config.HeaderPlacement.
		headers http.Header
		config  []mimicry.Place
		// header is the request's own; request its HeaderPlacement.
		header  http.Header
		request []mimicry.Place
		exact   bool
		// runs are names that must be sent consecutively; unchanged: sent
		// as without placements.
		runs      [][]string
		unchanged bool
		// configErr fails NewTransport, sendErr the request.
		configErr bool
		sendErr   bool
	}{
		{
			name:    "config after, HTTP/1.1",
			headers: apiKey,
			config: []mimicry.Place{
				{
					Header: "x-api-key",
					After:  "User-Agent",
				},
			},
			runs: [][]string{
				{"user-agent", "x-api-key"},
			},
		},
		{
			name:    "config after, HTTP/2",
			h2:      true,
			headers: apiKey,
			config: []mimicry.Place{
				{
					Header: "X-Api-Key",
					After:  "user-agent",
				},
			},
			runs: [][]string{
				{"user-agent", "x-api-key"},
			},
		},
		{
			name: "before Host, HTTP/1.1",
			request: []mimicry.Place{
				{
					Header: "Accept-Language",
					Before: "Host",
				},
			},
			runs: [][]string{
				{"accept-language", "host"},
			},
		},
		{
			name: "every value moves, HTTP/1.1",
			header: http.Header{
				"X-Multi": {"1", "2"},
			},
			request: []mimicry.Place{
				{
					Header: "X-Multi",
					After:  "Accept",
				},
			},
			runs: [][]string{
				{"accept", "x-multi", "x-multi"},
			},
		},
		{
			name:    "request after config, HTTP/2",
			h2:      true,
			headers: apiKey,
			config: []mimicry.Place{
				{
					Header: "X-Api-Key",
					After:  "User-Agent",
				},
				{
					Header: "Accept-Language",
					Before: "Accept",
				},
			},
			request: []mimicry.Place{
				{
					Header: "X-Api-Key",
					After:  "Accept",
				},
			},
			runs: [][]string{
				{"accept-language", "accept", "x-api-key"},
			},
		},
		{
			name:    "HeaderOrderKey wins over config, HTTP/2",
			h2:      true,
			headers: apiKey,
			config: []mimicry.Place{
				{
					Header: "X-Api-Key",
					After:  "User-Agent",
				},
			},
			header: http.Header{
				mimicry.HeaderOrderKey: {"x-api-key", "accept", "user-agent"},
			},
			unchanged: true,
		},
		{
			name:    "Exact ignores config, HTTP/1.1",
			headers: apiKey,
			config: []mimicry.Place{
				{
					Header: "X-Api-Key",
					After:  "Host",
				},
			},
			exact:     true,
			unchanged: true,
		},
		{
			name: "both After and Before",
			config: []mimicry.Place{
				{
					Header: "X-Api-Key",
					After:  "User-Agent",
					Before: "Accept",
				},
			},
			configErr: true,
		},
		{
			name: "neither After nor Before",
			config: []mimicry.Place{
				{
					Header: "X-Api-Key",
				},
			},
			configErr: true,
		},
		{
			name: "empty header",
			config: []mimicry.Place{
				{
					After: "User-Agent",
				},
			},
			configErr: true,
		},
		{
			name: "pseudo-header",
			config: []mimicry.Place{
				{
					Header: ":path",
					After:  "User-Agent",
				},
			},
			configErr: true,
		},
		{
			name: "its own anchor",
			config: []mimicry.Place{
				{
					Header: "X-Api-Key",
					Before: "x-api-key",
				},
			},
			configErr: true,
		},
		{
			name: "request's pseudo-header anchor",
			h2:   true,
			request: []mimicry.Place{
				{
					Header: "User-Agent",
					After:  ":authority",
				},
			},
			sendErr: true,
		},
		{
			name: "header not sent, HTTP/2",
			h2:   true,
			request: []mimicry.Place{
				{
					Header: "X-Api-Key",
					After:  "User-Agent",
				},
			},
			sendErr: true,
		},
		{
			name: "suppressed anchor, HTTP/1.1",
			header: http.Header{
				"Accept-Language": nil,
			},
			request: []mimicry.Place{
				{
					Header: "User-Agent",
					After:  "Accept-Language",
				},
			},
			sendErr: true,
		},
		{
			name:    "Connection is not sent over HTTP/2",
			h2:      true,
			headers: apiKey,
			config: []mimicry.Place{
				{
					Header: "X-Api-Key",
					After:  "Connection",
				},
			},
			sendErr: true,
		},
		{
			name: "request placements with HeaderOrderKey",
			header: http.Header{
				mimicry.HeaderOrderKey: {"accept", "user-agent"},
			},
			request: []mimicry.Place{
				{
					Header: "Accept",
					After:  "User-Agent",
				},
			},
			sendErr: true,
		},
		{
			name: "request placements with Exact",
			header: http.Header{
				"Accept": {"*/*"},
			},
			request: []mimicry.Place{
				{
					Header: "Accept",
					Before: "Host",
				},
			},
			exact:   true,
			sendErr: true,
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := h1
			if tt.h2 {
				s = h2
			}
			send := func(path string, config, request []mimicry.Place) error {
				tr, err := mimicry.NewTransport(mimicry.Config{
					Profile:         profiles.Chrome(),
					Headers:         tt.headers,
					HeaderPlacement: config,
					RootCAs:         s.RootCAs,
					DialContext:     s.DialContext,
					DisableHTTP3:    true,
				})
				if err != nil {
					return err
				}
				defer tr.CloseIdleConnections()
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://www.shop.test"+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				for k, v := range tt.header {
					req.Header[k] = v
				}
				req = mimicry.With(req, mimicry.Request{
					HeaderPlacement: request,
				})
				if tt.exact {
					req = mimicry.Exact(req)
				}
				resp, err := tr.RoundTrip(req)
				if err != nil {
					return err
				}
				return resp.Body.Close()
			}
			path := fmt.Sprintf("/placed/%d", i)
			err := send(path, tt.config, tt.request)
			if tt.configErr || tt.sendErr {
				if !errors.Is(err, mimicry.ErrPlacement) {
					t.Fatalf("error %v, want ErrPlacement", err)
				}
				if _, ok := sent.get(path); ok {
					t.Fatal("the request was sent")
				}
				if tt.configErr && !strings.Contains(err.Error(), "Config.HeaderPlacement[0]") {
					t.Errorf("error %q does not name Config.HeaderPlacement[0]", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			base := fmt.Sprintf("/base/%d", i)
			if err := send(base, nil, nil); err != nil {
				t.Fatal(err)
			}
			got, _ := sent.get(path)
			want, _ := sent.get(base)
			if tt.unchanged {
				if !slices.Equal(got, want) {
					t.Fatalf("sent\n%q\nwant, as without placements,\n%q", got, want)
				}
				return
			}
			var moved []string
			for _, p := range slices.Concat(tt.config, tt.request) {
				moved = append(moved, strings.ToLower(p.Header))
			}
			others := func(names []string) []string {
				return slices.DeleteFunc(slices.Clone(names), func(n string) bool {
					return slices.Contains(moved, n)
				})
			}
			if !slices.Equal(others(got), others(want)) {
				t.Errorf("headers that were not moved:\n%q\nwant\n%q", others(got), others(want))
			}
			for _, run := range tt.runs {
				if !containsRun(got, run) {
					t.Errorf("sent %q, want the run %q", got, run)
				}
			}
		})
	}
}

// containsRun reports whether run is in names, consecutively.
func containsRun(names, run []string) bool {
	for i := range names {
		if len(names)-i >= len(run) && slices.Equal(names[i:i+len(run)], run) {
			return true
		}
	}
	return false
}
