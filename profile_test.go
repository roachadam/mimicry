package mimicry

import (
	"bytes"
	"io"
	"net/http"
	"reflect"
	"runtime"
	"slices"
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
// caller's profile, and dropped once no Transport holds it. Equal profiles
// from separate accessor calls share it; a profile changed anywhere (in
// place after its Transports were built, or deep in a map, a list or a
// pointer) gets a copy of its own holding the change, never the stale one.
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
	key := fingerprint(p)

	if changedProfile(t, p) == shared {
		t.Fatal("a profile changed in place got the copy built before the change")
	}

	for name, change := range map[string]func(*profile.Profile){
		"map entry": func(c *profile.Profile) {
			c.Kinds["fetch"].Headers[0].Value = "changed"
		},
		"list order": func(c *profile.Profile) {
			c.TLS.CipherSuites[1], c.TLS.CipherSuites[2] = c.TLS.CipherSuites[2], c.TLS.CipherSuites[1]
		},
		"pointer": func(c *profile.Profile) {
			c.Redirects = nil
		},
	} {
		c := profiles.Chrome()
		change(c)

		if changedProfile(t, c) == shared {
			t.Errorf("a profile with a changed %s got the unchanged one's copy", name)
		}
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

// changedProfile builds a Transport from p, a changed profile, and returns
// the Transport's copy of p, which must be p as changed.
func changedProfile(t *testing.T, p *profile.Profile) *profile.Profile {
	t.Helper()

	tr, err := NewTransport(Config{
		Profile: p,
	})
	if err != nil {
		t.Fatal(err)
	}

	want, err := profile.Encode(p)
	if err != nil {
		t.Fatal(err)
	}

	got, err := profile.Encode(tr.profile)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatal("the Transport's copy is not the changed profile")
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

// fingerprint panics on a kind it cannot write and never ends on a cyclic
// value: a field of such a type added to a profile would crash NewTransport
// for every caller whose profile sets it, which the built-in profiles,
// leaving many pointers nil, may never reach. Every type a profile holds
// must be one fingerprint writes, its maps keyed by strings or integers,
// and no type may contain itself.
func TestFingerprintCoversProfile(t *testing.T) {
	var check func(reflect.Type, []reflect.Type)

	check = func(typ reflect.Type, path []reflect.Type) {
		if slices.Contains(path, typ) {
			t.Errorf("%v contains itself", typ)

			return
		}

		path = append(path, typ)

		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			check(typ.Elem(), path)
		case reflect.Map:
			switch typ.Key().Kind() {
			case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			default:
				t.Errorf("%v is keyed by a %v", typ, typ.Key().Kind())
			}

			check(typ.Elem(), path)

		case reflect.Struct:
			for f := range typ.Fields() {
				check(f.Type, path)
			}

		case reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
			t.Errorf("a profile holds a %v (%v)", typ.Kind(), path)
		default:
		}
	}

	check(reflect.TypeFor[profile.Profile](), nil)
}
