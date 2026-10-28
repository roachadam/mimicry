package mimicry

import (
	"bytes"
	"crypto/sha256"
	"io"
	"net/http"
	"runtime"
	"sync"
	"testing"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// Transports built from equal profiles share one decoded profile: a
// Transport writing into it would corrupt every other one, racing with
// their requests. Many Transports send concurrently over HTTP/1.1 and
// HTTP/2 (navigations, fetches, redirects; run with -race); the shared copy
// must be byte for byte what it was, unaffected by later changes to the
// caller's profile, and dropped once no Transport holds it.
func TestTransportsShareProfile(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/", http.StatusFound)

			return
		}

		_, _ = io.WriteString(w, "ok")
	}

	p := profiles.Chrome()
	// Chrome's profile measures fetches and redirect hops over HTTP/2 only.
	servers := []struct {
		srv   testServer
		paths []string
		kinds []Kind
	}{
		{
			srv:   server(t, false, handler),
			paths: []string{"/"},
			kinds: []Kind{Navigate},
		},
		{
			srv:   server(t, true, handler),
			paths: []string{"/", "/redirect"},
			kinds: []Kind{Navigate, Fetch},
		},
	}

	shared := servers[0].srv.tr.profile

	before, err := profile.Encode(shared)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	for _, s := range servers {
		for range 8 {
			tr, err := NewTransport(Config{
				Profile: p,
				RootCAs: s.srv.pool,
			})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(tr.CloseIdleConnections)

			if tr.profile != shared {
				t.Fatal("Transports built from equal profiles hold different copies")
			}

			for _, path := range s.paths {
				for _, kind := range s.kinds {
					wg.Go(func() {
						sharedRoundTrip(t, tr, s.srv.url+path, kind)
					})
				}
			}
		}
	}

	// The caller's later changes reach no Transport.
	p.Headers[0].Value = "changed"

	wg.Wait()

	after, err := profile.Encode(shared)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(before, after) {
		t.Error("requests or the caller's changes wrote into the shared profile")
	}

	// A changed profile gets its own copy, which goes with the last
	// Transport holding it.
	data, err := profile.Encode(p)
	if err != nil {
		t.Fatal(err)
	}

	key := sha256.Sum256(data)

	if changedProfile(t, p) == shared {
		t.Fatal("a changed profile got the unchanged one's copy")
	}

	for range 100 {
		runtime.GC()
		runtime.Gosched()

		if _, ok := snapshots.Load(key); !ok {
			return
		}
	}

	t.Error("a profile's shared copy outlived every Transport holding it")
}

// changedProfile builds a Transport from p, whose first header's value is
// "changed", and returns the Transport's copy of p.
func changedProfile(t *testing.T, p *profile.Profile) *profile.Profile {
	t.Helper()

	tr, err := NewTransport(Config{
		Profile: p,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := tr.profile.Headers[0].Value; got != "changed" {
		t.Fatalf("the Transport's copy has %q, not the profile's change", got)
	}

	return tr.profile
}

// sharedRoundTrip sends a request of kind to url through tr and reads the
// response.
func sharedRoundTrip(t *testing.T, tr *Transport, url string, kind Kind) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Error(err)

		return
	}

	client := &http.Client{
		Transport:     tr,
		CheckRedirect: followRedirects,
	}

	resp, err := client.Do(As(req, kind))
	if err != nil {
		t.Error(err)

		return
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Error(err)
	}
}
