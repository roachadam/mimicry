package e2e

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// Over HTTP/1.1, mimicry cuts a request with a body into TLS records as
// the browser does: two 10 MiB fetch() uploads, the first on a new
// connection, then bodies from 1 byte to 64 KiB, across Chrome's
// 1400-byte limit and a record's end. Each request's cut (one record,
// the head apart, or one stream: capture.H1Upload.Cut, which also wants
// every record full but the head's or body's last) is the one the
// browser's captures show at mimicry's own head and body length
// (capture.UploadRecords): mimicry's head is not the browser's byte for
// byte.
func TestUploadRecordsMatchBrowser(t *testing.T) {
	sc := scenario(t, capture.UploadScenario)

	sizes := []int{1, 100, 500, 1000, 4096, 15000, 16000, 16384, 16385, 32768, 65536}
	for n := 700; n <= 900; n++ {
		sizes = append(sizes, n)
	}

	for n := 15800; n <= 16000; n += 4 {
		sizes = append(sizes, n)
	}

	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			want, err := capture.UploadRecords(captureDir, p)
			if err != nil {
				t.Fatal(err)
			}

			if want == nil {
				file := capture.Source(p, capture.UploadScenario) + "." + capture.UploadScenario + ".h1trace.json.gz"
				skipIfMissing(t, captureDir+file, file)
				t.Fatalf("no capture %s", file)
			}

			base, tr, captures := captureClient(t, p, sc)
			client := &http.Client{
				Transport: tr,
			}

			page, err := url.Parse(base + "/")
			if err != nil {
				t.Fatal(err)
			}

			send := func(method, path string, body []byte) {
				t.Helper()

				var r io.Reader
				if body != nil {
					r = bytes.NewReader(body)
				}

				req, err := http.NewRequestWithContext(t.Context(), method, base+path, r)
				if err != nil {
					t.Fatal(err)
				}

				resp, err := client.Do(mimicry.With(req, mimicry.Request{
					Kind: mimicry.Fetch,
					From: page,
				}))
				if err != nil {
					t.Fatal(err)
				}

				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}

			big := make([]byte, server.UploadSize)
			send(http.MethodPost, "/up/buffer", big)
			send(http.MethodPost, "/up/buffer-again", big)

			for _, n := range sizes {
				send(http.MethodPost, "/up/sweep-"+strconv.Itoa(n), make([]byte, n))
			}

			send(http.MethodGet, "/done", nil)

			c := <-captures
			if c.Protocol != "http/1.1" {
				t.Fatalf("negotiated %q, want http/1.1", c.Protocol)
			}

			seen := map[string]bool{}

			for _, r := range c.H1 {
				u := r.Upload
				if u == nil {
					t.Fatalf("%s: no records kept", r.Line)
				}

				if u.Body == 0 {
					continue
				}

				got, err := u.Cut()
				if err != nil {
					t.Errorf("%s: %v", r.Line, err)

					continue
				}

				seen[got] = true

				if w := cutFor(want, u.Head+u.Body); got != w {
					t.Errorf("%s (%d-byte head, %d-byte body): cut %s, the browser's %s", r.Line, u.Head, u.Body, got, w)
				}
			}

			// The sizes crossed every limit the rule has.
			for _, w := range []string{cutFor(want, 1), cutFor(want, profile.MaxRecord+1)} {
				if !seen[w] {
					t.Errorf("no request was cut %s", w)
				}
			}
		})
	}
}

// cutFor is the cut u gives a request of total bytes, head and body.
func cutFor(u *profile.UploadRecords, total int) string {
	switch {
	case u.HeadApart && total > u.JoinUpTo:
		return capture.CutApart
	case u.HeadApart || total <= profile.MaxRecord:
		return capture.CutOne
	}

	return capture.CutStream
}
