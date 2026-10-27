package native

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	utls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/qpack"
	"github.com/roachadam/mimicry/internal/quic"
)

// h3Recorder is an HTTP/3 server that records, by path, the field names
// (pseudo-headers aside) of each request's HEADERS in the order the client
// encoded them, and answers 200 with no body.
type h3Recorder struct {
	mu    sync.Mutex
	paths map[string][]string
}

func (r *h3Recorder) get(path string) ([]string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	names, ok := r.paths[path]
	return names, ok
}

// startH3Recorder serves r over QUIC on every local address and returns
// its port. It offers no QPACK dynamic table, so each section decodes alone.
func startH3Recorder(t *testing.T, r *h3Recorder) uint16 {
	t.Helper()
	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	cert := serverCert.Load()
	ep, err := quic.NewEndpoint(pc, &quic.Config{
		TLSConfig: &utls.Config{
			Certificates: []utls.Certificate{{
				Certificate: cert.Certificate,
				PrivateKey:  cert.PrivateKey,
			}},
			NextProtos: []string{"h3"},
			MinVersion: utls.VersionTLS13,
		},
	})
	if err != nil {
		_ = pc.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = ep.Close(ctx)
	})
	go func() {
		for {
			c, err := ep.Accept(context.Background())
			if err != nil {
				return
			}
			go r.serve(c)
		}
	}()
	return ep.LocalAddr().Port()
}

func (r *h3Recorder) serve(c *quic.Conn) {
	ctx := context.Background()
	control, err := c.NewSendOnlyStream(ctx)
	if err != nil {
		return
	}
	// Stream type control, an empty SETTINGS.
	if _, err := control.Write([]byte{0x00, 0x04, 0x00}); err != nil {
		return
	}
	if err := control.Flush(); err != nil {
		return
	}
	for {
		s, err := c.AcceptStream(ctx)
		if err != nil {
			return
		}
		if s.IsReadOnly() {
			// The client's control and QPACK streams.
			go func() {
				_, _ = io.Copy(io.Discard, s)
			}()
			continue
		}
		go r.answer(s)
	}
}

// answer records the request on s and answers 200: HEADERS with
// ":status: 200" (QPACK static index 25), then FIN.
func (r *h3Recorder) answer(s *quic.Stream) {
	defer func() {
		_ = s.Close()
	}()
	for {
		typ, err := readQUICVarint(s)
		if err != nil {
			return
		}
		n, err := readQUICVarint(s)
		if err != nil {
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(s, payload); err != nil {
			return
		}
		if typ != 0x01 {
			continue
		}
		fields, _, err := qpack.NewDecoder(0).Decode(payload)
		if err != nil {
			return
		}
		var path string
		var names []string
		for _, f := range fields {
			switch {
			case f.Name == ":path":
				path = f.Value
			case f.Name[0] != ':':
				names = append(names, f.Name)
			}
		}
		r.mu.Lock()
		r.paths[path] = names
		r.mu.Unlock()
		if _, err := s.Write([]byte{0x01, 0x03, 0x00, 0x00, 0xc0 | 25}); err != nil {
			return
		}
		_ = s.CloseWrite()
		return
	}
}

// readQUICVarint reads a QUIC variable-length integer (RFC 9000, 16).
func readQUICVarint(r io.ByteReader) (uint64, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	v := uint64(b & 0x3f)
	for range 1<<(b>>6) - 1 {
		b, err = r.ReadByte()
		if err != nil {
			return 0, err
		}
		v = v<<8 | uint64(b)
	}
	return v, nil
}

// Header placement over HTTP/3: placed requests match their unplaced form
// with the placements applied after the pseudo-headers, and a missing
// header or anchor fails with nothing sent. TestHeaderPlacement in the root
// package covers HTTP/1.1 and HTTP/2.
func TestHeaderPlacementOverHTTP3(t *testing.T) {
	// Every TCP dial fails: requests go on the QUIC connection or fail.
	e := newTestEngine(t, options{
		dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("no TCP in this test")
		},
	})
	rec := &h3Recorder{
		paths: map[string][]string{},
	}
	port := startH3Recorder(t, rec)
	if _, a := dialQUIC(t, e, "https://localhost/", port); a.conn == nil {
		t.Fatal("the QUIC attempt failed")
	}
	send := func(path string, places []headerorder.Placement) error {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if places != nil {
			ctx = headerorder.WithPlacements(ctx, places)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://localhost"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Api-Key", "k")
		resp, err := e.RoundTrip(req)
		if err != nil {
			return err
		}
		if resp.ProtoMajor != 3 {
			t.Errorf("%s went over %s, not HTTP/3", path, resp.Proto)
		}
		return resp.Body.Close()
	}
	if err := send("/base", nil); err != nil {
		t.Fatal(err)
	}
	base, _ := rec.get("/base")

	tests := []struct {
		name   string
		places []headerorder.Placement
		// runs are names that must be sent consecutively.
		runs    [][]string
		wantErr bool
	}{
		{
			name: "after",
			places: []headerorder.Placement{
				{
					Header: "x-api-key",
					Anchor: "user-agent",
					After:  true,
				},
			},
			runs: [][]string{
				{"user-agent", "x-api-key"},
			},
		},
		{
			name: "config then request",
			places: []headerorder.Placement{
				{
					Header: "x-api-key",
					Anchor: "user-agent",
					After:  true,
				},
				{
					Header: "accept-language",
					Anchor: "accept",
				},
				{
					Header: "x-api-key",
					Anchor: "accept",
					After:  true,
				},
			},
			runs: [][]string{
				{"accept-language", "accept", "x-api-key"},
			},
		},
		{
			name: "pseudo-header anchor",
			places: []headerorder.Placement{
				{
					Header: "user-agent",
					Anchor: ":authority",
					After:  true,
				},
			},
			wantErr: true,
		},
		{
			name: "pseudo-header moved",
			places: []headerorder.Placement{
				{
					Header: ":path",
					Anchor: "user-agent",
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/" + strings.ReplaceAll(tt.name, " ", "-")
			err := send(path, tt.places)
			if tt.wantErr {
				if !errors.Is(err, headerorder.ErrPlacement) {
					t.Fatalf("error %v, want ErrPlacement", err)
				}
				if _, ok := rec.get(path); ok {
					t.Fatal("the request was sent")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := rec.get(path)
			others := func(names []string) []string {
				return slices.DeleteFunc(slices.Clone(names), func(n string) bool {
					return slices.ContainsFunc(tt.places, func(p headerorder.Placement) bool {
						return p.Header == n
					})
				})
			}
			if !slices.Equal(others(got), others(base)) {
				t.Errorf("fields that were not moved:\n%q\nwant\n%q", others(got), others(base))
			}
			for _, run := range tt.runs {
				if !slices.ContainsFunc(windows(got, len(run)), func(w []string) bool {
					return slices.Equal(w, run)
				}) {
					t.Errorf("sent %q, want the run %q", got, run)
				}
			}
		})
	}
}

// windows returns every run of n consecutive names in names.
func windows(names []string, n int) [][]string {
	var out [][]string
	for i := 0; i+n <= len(names); i++ {
		out = append(out, names[i:i+n])
	}
	return out
}
