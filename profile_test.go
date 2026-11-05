package mimicry

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"runtime"
	"slices"
	"sync"
	"testing"
	"weak"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// Transports built from equal profiles share one checked profile: a
// Transport writing into it would corrupt every other one, racing with
// their requests. Many Transports send concurrently over HTTP/1.1 and
// HTTP/2 (navigations, fetches, redirects; run with -race); the shared copy
// must be byte for byte what it was, unaffected by later changes to a Spec
// the caller holds, an edit's included. Profiles of equal content share it
// whether With finds it by name or by fingerprint (an edit undone); a
// profile edited anywhere (deep in a map, a list or a pointer) gets a copy
// of its own holding the edit, never the unedited one, and keeps it while
// a Transport holds it; it goes once neither a Profile nor a Transport
// does.
func TestTransportsShareProfile(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/", http.StatusFound)

			return
		}

		_, _ = io.WriteString(w, "ok")
	}

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

	// A copy from Spec, changed once the Transports are built.
	read := profiles.ChromeMacOS().Spec()

	equal := []profile.Profile{
		profiles.ChromeMacOS(),
		// Equal to the last copy of its name: found by name.
		profiles.ChromeMacOS().With(func(*profile.Spec) {}),
		// Unequal to the last copy of its name, the edit's: found by
		// fingerprint.
		profiles.ChromeMacOS().With(func(s *profile.Spec) {
			s.Headers[0].Value = "changed"
		}).With(func(s *profile.Spec) {
			s.Headers[0].Value = read.Headers[0].Value
		}),
	}

	var wg sync.WaitGroup

	for i, s := range servers {
		for j := range 8 {
			tr, err := NewTransport(Config{
				Profile: equal[(i+j)%len(equal)],
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

	read.Headers[0].Value = "changed"

	wg.Wait()

	after, err := profile.Encode(shared)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(before, after) {
		t.Error("requests or the caller's changes wrote into the shared profile")
	}

	// Each edit is compared first with the last copy of its name, then by
	// fingerprint: back to Chrome's before each, both must see the edit.
	gone := make([]weak.Pointer[profile.Spec], 0, 3)

	for _, tc := range []struct {
		name string
		edit func(*profile.Spec)
	}{
		{
			name: "map entry",
			edit: func(s *profile.Spec) {
				s.Kinds["fetch"].Headers[0].Value = "changed"
			},
		},
		{
			name: "list order",
			edit: func(s *profile.Spec) {
				s.TLS.CipherSuites[1], s.TLS.CipherSuites[2] = s.TLS.CipherSuites[2], s.TLS.CipherSuites[1]
			},
		},
		{
			name: "pointer",
			edit: func(s *profile.Spec) {
				s.Redirects = nil
			},
		},
	} {
		if p := profiles.ChromeMacOS().With(nil); transportProfile(t, p) != shared {
			t.Fatal("an unedited profile did not get the shared copy")
		}

		want := profiles.ChromeMacOS().Spec()
		tc.edit(want)

		// The edit keeps its Spec and changes it once its copy is made.
		var kept *profile.Spec

		got := transportProfile(t, profiles.ChromeMacOS().With(func(s *profile.Spec) {
			tc.edit(s)

			kept = s
		}))

		kept.Headers[0].Value = "changed"

		if got == shared {
			t.Fatalf("a profile with a changed %s got the unchanged one's copy", tc.name)
		}

		if !bytes.Equal(encode(t, got), encode(t, want)) {
			t.Fatalf("the copy of a profile with a changed %s is not the edited profile", tc.name)
		}

		gone = append(gone, weak.Make(got))

		// A Transport keeps its copy shared, its Profile gone.
		tr, err := NewTransport(Config{
			Profile: profiles.ChromeMacOS().With(tc.edit),
		})
		if err != nil {
			t.Fatal(err)
		}

		runtime.GC()

		if again := transportProfile(t, profiles.ChromeMacOS().With(tc.edit)); again != tr.profile {
			t.Fatalf("a profile with a changed %s got a new copy while a Transport held one", tc.name)
		}
	}

	for range 100 {
		runtime.GC()
		runtime.Gosched()

		if gone[0].Value() == nil && gone[1].Value() == nil && gone[2].Value() == nil {
			return
		}
	}

	t.Error("a profile's shared copy outlived every Profile and Transport holding it")
}

// The zero Profile, and a profile With left invalid, build no Transport;
// the latter returns every problem found, and stays editable.
func TestNewTransportRejectsProfile(t *testing.T) {
	if _, err := NewTransport(Config{}); err == nil {
		t.Error("NewTransport built a Transport from the zero Profile")
	}

	broken := profiles.ChromeMacOS().With(func(s *profile.Spec) {
		s.Name = ""
		s.Browser = ""
	})

	_, err := NewTransport(Config{
		Profile: broken,
	})

	verr, ok := errors.AsType[*profile.ValidationError](err)
	if !ok {
		t.Fatalf("NewTransport error = %v, want a *ValidationError", err)
	}

	for _, path := range []string{"name", "browser"} {
		if !slices.ContainsFunc(verr.Problems, func(p profile.Problem) bool {
			return p.Path == path
		}) {
			t.Errorf("NewTransport error = %v, want a problem at %s", err, path)
		}
	}

	fixed := broken.With(func(s *profile.Spec) {
		s.Name = "fixed"
		s.Browser = "chrome"
	})
	if _, err := NewTransport(Config{
		Profile: fixed,
	}); err != nil {
		t.Fatal(err)
	}
}

// Config.ClientHints's platformVersion must match the profile's platform:
// empty with Chrome on Linux, which sends no version, set elsewhere.
func TestNewTransportRejectsClientHints(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile profile.Profile
		version string
		ok      bool
	}{
		{
			name:    "linux with a version",
			profile: profiles.ChromeLinux(),
			version: "6.8.0",
		},
		{
			name:    "linux without",
			profile: profiles.ChromeLinux(),
			ok:      true,
		},
		{
			name:    "macos without a version",
			profile: profiles.ChromeMacOS(),
		},
		{
			name:    "macos with",
			profile: profiles.ChromeMacOS(),
			version: "15.0.0",
			ok:      true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.profile.Spec().ClientHints.Machine

			m.PlatformVersion = tc.version

			_, err := NewTransport(Config{
				Profile:     tc.profile,
				ClientHints: &m,
			})
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}

				return
			}

			verr, ok := errors.AsType[*profile.ValidationError](err)
			if !ok || !slices.ContainsFunc(verr.Problems, func(p profile.Problem) bool {
				return p.Path == "machine.platformVersion"
			}) {
				t.Errorf("NewTransport error = %v, want a *ValidationError at machine.platformVersion", err)
			}
		})
	}
}

// transportProfile builds a Transport from p and returns its copy of p.
func transportProfile(t *testing.T, p profile.Profile) *profile.Spec {
	t.Helper()

	tr, err := NewTransport(Config{
		Profile: p,
	})
	if err != nil {
		t.Fatal(err)
	}

	return tr.profile
}

// encode is profile.Encode, failing t on an error.
func encode(t *testing.T, s *profile.Spec) []byte {
	t.Helper()

	b, err := profile.Encode(s)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// sharedRoundTrip sends a request of kind to url through tr and reads the
// response.
func sharedRoundTrip(t *testing.T, tr *Transport, url string, kind Kind) {
	t.Helper()

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
