package mimicry_test

import (
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profiles"
)

// Content-Length and Transfer-Encoding are built from the request, never
// taken from Request.Header or Config.Header; over HTTP/1.1 a caller's
// Connection: close is honoured in the profile's slot and closes the
// connection, other Connection values give the profile's (issue #3).
func TestFramingHeadersFromRequest(t *testing.T) {
	var mu sync.Mutex

	lines := map[string][]string{}
	remotes := map[string][]string{}
	h1 := startShopHTTP1(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		lines[r.URL.Path] = headerLines(r)
		remotes[r.URL.Path] = []string{r.RemoteAddr}
	})
	t.Cleanup(h1.Close)

	bodies := map[string][]string{}
	h2 := startShop(func(_ http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}

		mu.Lock()
		defer mu.Unlock()

		bodies[r.URL.Path] = []string{strconv.FormatInt(r.ContentLength, 10), string(b)}
	})
	t.Cleanup(h2.Close)

	tests := []struct {
		name       string
		h2         bool
		config     bool
		connection string
		as         func(*http.Request) *http.Request
		// body makes the request a POST with it.
		body string
	}{
		{"fetch, request header, HTTP/1.1", false, false, "close", fetch, ""},
		{"fetch, config header, HTTP/1.1", false, true, "close", fetch, ""},
		{"navigate, HTTP/1.1", false, false, "close", navigate, ""},
		{"exact, HTTP/1.1", false, false, "close", mimicry.Exact, ""},
		{"fetch, Connection: Upgrade, HTTP/1.1", false, false, "Upgrade", fetch, ""},
		{"exact, Connection: Upgrade, HTTP/1.1", false, false, "Upgrade", mimicry.Exact, ""},
		{"fetch, request header, HTTP/2", true, false, "close", fetch, ""},
		{"fetch, config header, HTTP/2", true, true, "close", fetch, ""},
		{"navigate, HTTP/2", true, false, "close", navigate, ""},
		{"exact, HTTP/2", true, false, "close", mimicry.Exact, ""},
		{"fetch, body, HTTP/2", true, false, "close", fetch, "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := h1
			if tt.h2 {
				s = h2
			}

			framing := http.Header{
				"Content-Length":    {"5"},
				"Connection":        {tt.connection},
				"Transfer-Encoding": {"chunked"},
			}
			config := mimicry.Config{
				Profile:      profiles.Chrome(),
				RootCAs:      s.RootCAs,
				DialContext:  s.DialContext,
				DisableHTTP3: true,
			}

			if tt.config {
				config.Header = framing
			}

			tr, err := mimicry.NewTransport(config)
			if err != nil {
				t.Fatal(err)
			}
			defer tr.CloseIdleConnections()

			c := &http.Client{
				Transport: tr,
				Timeout:   10 * time.Second,
			}
			get := func(path string, header http.Header) {
				method, body := http.MethodGet, io.Reader(nil)
				if tt.body != "" {
					method, body = http.MethodPost, strings.NewReader(tt.body)
				}

				req, err := http.NewRequestWithContext(t.Context(), method, "https://www.shop.test"+path, body)
				if err != nil {
					t.Fatal(err)
				}

				maps.Copy(req.Header, header)

				resp, err := c.Do(tt.as(req))
				if err != nil {
					t.Fatal(err)
				}

				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}

			path := "/framing/" + strings.ReplaceAll(tt.name, " ", "")
			if tt.config {
				get(path, nil)
			} else {
				get(path, framing)
			}

			if tt.h2 {
				// The body's own length, not the caller's 5.
				if got := sentLines(&mu, bodies, path); tt.body != "" && !slices.Equal(got, []string{strconv.Itoa(len(tt.body)), tt.body}) {
					t.Errorf("server read length and body %q, want %d and %q", got, len(tt.body), tt.body)
				}

				return
			}

			want := "keep-alive"
			if tt.connection == "close" {
				want = "close"
			}

			for _, l := range sentLines(&mu, lines, path) {
				name, value, _ := strings.Cut(strings.ToLower(l), ": ")
				switch {
				case name == "content-length", name == "transfer-encoding":
					t.Errorf("sent %q on a GET with no body", l)
				case name == "connection" && value != want:
					t.Errorf("sent %q, want Connection: %s", l, want)
				}
			}

			// A closing request's connection is not reused (with
			// Config.Header, every request closes its own).
			get(path+"/next", nil)

			reused := slices.Equal(sentLines(&mu, remotes, path), sentLines(&mu, remotes, path+"/next"))

			if reused != (want == "keep-alive") {
				t.Errorf("connection reused %v, want %v", reused, want == "keep-alive")
			}
		})
	}
}

// Placements still anchor on the framing lines the connection builds, the
// caller's own framing headers aside.
func TestFramingPlacementAnchors(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)

	h1 := startShopHTTP1(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		got = headerLines(r)
	})
	t.Cleanup(h1.Close)

	tr, err := mimicry.NewTransport(mimicry.Config{
		Profile:      profiles.Chrome(),
		RootCAs:      h1.RootCAs,
		DialContext:  h1.DialContext,
		DisableHTTP3: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.CloseIdleConnections()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://www.shop.test/anchor", strings.NewReader("ab"))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Connection", "close")
	req.Header.Set("Content-Length", "9")
	req.Header.Set("X-Api-Key", "k")

	req = mimicry.With(req, mimicry.Request{
		Kind: mimicry.Fetch,
		HeaderPlacement: []mimicry.Place{
			{
				Header: "X-Api-Key",
				After:  "Content-Length",
			},
		},
	})

	closed := func(name string, req *http.Request) {
		plan, err := tr.Plan(req)
		if err != nil {
			t.Fatal(err)
		}

		if !slices.ContainsFunc(plan.Overrides, func(o mimicry.Override) bool {
			return o.Name == "connection" && o.Mimicry == "keep-alive" && slices.Equal(o.Caller, []string{"close"})
		}) {
			t.Errorf("%s: Plan.Overrides %+v, want connection keep-alive replaced by close", name, plan.Overrides)
		}
	}

	closed("Connection: close", req)

	// req.Close, and an Exact request (no HTTP/1.1 list: keep-alive).
	byClose := req.Clone(req.Context())
	byClose.Header.Del("Connection")

	byClose.Close = true
	closed("req.Close", byClose)

	exact, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://www.shop.test/exact", nil)
	if err != nil {
		t.Fatal(err)
	}

	exact.Header.Set("Connection", "close")
	closed("Exact", mimicry.Exact(exact))

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	i := slices.Index(got, "Content-Length: 2")
	if i < 0 || i+1 >= len(got) || got[i+1] != "x-api-key: k" {
		t.Errorf("sent %q, want x-api-key: k right after Content-Length: 2", got)
	}

	// Chrome's slot for Connection is right after Host.
	if len(got) < 2 || got[1] != "Connection: close" {
		t.Errorf("sent %q, want Connection: close in the profile's slot", got)
	}
}

func fetch(req *http.Request) *http.Request {
	return mimicry.As(req, mimicry.Fetch)
}

func navigate(req *http.Request) *http.Request {
	return mimicry.As(req, mimicry.Navigate)
}

func sentLines(mu *sync.Mutex, lines map[string][]string, path string) []string {
	mu.Lock()
	defer mu.Unlock()

	return lines[path]
}
