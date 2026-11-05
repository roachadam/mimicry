package mimicry_test

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// A URL goes out as a browser's URL parser (the WHATWG URL standard, as
// the urls capture measures it) leaves it, whoever spelled it and wherever
// it came from (the request, a redirect's Location, a page): the host
// lowercase, an internationalized one in its ASCII form, the scheme's
// default port dropped, the query percent-encoded. SNI, the connection
// pool, Host and :authority, cookies, Origin and Referer all see that form
// (issues #48, #49, #50, #71). The plan shows the lines over every
// protocol, HTTP/3 included; the shop, which speaks no HTTP/3, what
// reaches a server over HTTP/1.1 and HTTP/2.
func TestURLsGoOutCanonical(t *testing.T) {
	const (
		// spelled is a query a page may write; encoded is what browsers
		// send for it.
		spelled = `a=b c&q="x"<y>'z&u=ü&e=%41`
		encoded = `a=b%20c&q=%22x%22%3Cy%3E%27z&u=%C3%BC&e=%41`
		idn     = "xn--bcher-kva.shop.test"
	)

	t.Run("plan", func(t *testing.T) {
		tr, err := mimicry.NewTransport(mimicry.Config{
			Profile: profiles.ChromeMacOS(),
		})
		if err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			url, authority, path string
		}{
			{
				url:       "https://WWW.Shop.TEST:443/a?" + spelled,
				authority: "www.shop.test",
				path:      "/a?" + encoded,
			},
			{
				url:       "https://Bücher.shop.test:0443/",
				authority: idn,
				path:      "/",
			},
			{
				url:       "https://www.shop.test:8443/",
				authority: "www.shop.test:8443",
				path:      "/",
			},
		} {
			p, err := tr.Plan(request(t, tc.url))
			if err != nil {
				t.Fatal(err)
			}

			if p.HTTP3 == nil {
				t.Fatal("no HTTP/3 lines")
			}

			for name, l := range map[string]mimicry.Lines{"HTTP/2": p.HTTP2, "HTTP/3": *p.HTTP3} {
				if l.Err != nil {
					t.Errorf("%s over %s: %v", tc.url, name, l.Err)
				}

				if got := pseudo(l, ":authority") + " " + pseudo(l, ":path"); got != tc.authority+" "+tc.path {
					t.Errorf("%s over %s: :authority :path %q, want %q", tc.url, name, got, tc.authority+" "+tc.path)
				}
			}

			if p.HTTP1.Err != nil {
				t.Errorf("%s over HTTP/1.1: %v", tc.url, p.HTTP1.Err)
			}

			if want := "GET " + tc.path + " HTTP/1.1"; p.HTTP1.RequestLine != want || pseudo(p.HTTP1, "Host") != tc.authority {
				t.Errorf("%s over HTTP/1.1: %q, Host %q, want %q, Host %q", tc.url, p.HTTP1.RequestLine, pseudo(p.HTTP1, "Host"), want, tc.authority)
			}
		}
	})

	for _, h2 := range []bool{false, true} {
		name := "HTTP/1.1"
		if h2 {
			name = "HTTP/2"
		}

		t.Run(name, func(t *testing.T) {
			var (
				mu    sync.Mutex
				seen  []string
				conns []string
			)

			handler := func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()

				conn := slices.Index(conns, r.RemoteAddr)
				if conn < 0 {
					conn = len(conns)
					conns = append(conns, r.RemoteAddr)
				}

				seen = append(seen, fmt.Sprintf("%s conn %d sni %s host %s cookie %q origin %q referer %q", r.RequestURI, conn, serverName(r), r.Host, r.Header.Get("Cookie"), r.Header.Get("Origin"), r.Header.Get("Referer")))
				mu.Unlock()

				switch r.URL.Path {
				case "/live":
					echoWebSocket(w, r)
				case "/redirect":
					http.Redirect(w, r, "https://WWW.Shop.TEST:443/landed", http.StatusFound)
				case "/set":
					w.Header().Add("Set-Cookie", "host=1")
					w.Header().Add("Set-Cookie", "domain=2; Domain=Bücher.shop.test")

				case "/to-idn":
					// Location as a server writes it, raw UTF-8.
					w.Header().Set("Location", "https://bücher.SHOP.test:443/back?"+spelled)
					w.WriteHeader(http.StatusFound)
				}
			}

			s := startShopHTTP1(handler)
			if h2 {
				s = startShop(handler)
			}

			t.Cleanup(s.Close)

			client, err := mimicry.NewClient(mimicry.Config{
				Profile:     profiles.ChromeMacOS(),
				RootCAs:     s.RootCAs,
				DialContext: s.DialContext,
				Resolver:    loopbackDNS(t),
				// One connection per host, to see each one's SNI.
				DisableCoalescing: true,
			})
			if err != nil {
				t.Fatal(err)
			}

			fetch := mimicry.With(request(t, "https://www.shop.test/api"), mimicry.Request{
				Kind: mimicry.Fetch,
				From: &url.URL{
					Scheme: "https",
					Host:   "Bücher.shop.test:443",
					Path:   "/page",
				},
			})
			back := ` conn 1 sni ` + idn + ` host ` + idn + ` cookie "host=1; domain=2" origin "" referer ""`

			var want []string
			for _, step := range []struct {
				req *http.Request
				// redirected: Chrome's redirected navigation was
				// measured over HTTP/2 only.
				redirected bool
				want       []string
			}{
				{
					req:  request(t, "https://www.shop.test/"),
					want: []string{`/ conn 0 sni www.shop.test host www.shop.test cookie "" origin "" referer ""`},
				},
				{
					req:  request(t, "https://WWW.Shop.TEST:443/a?"+spelled),
					want: []string{`/a?` + encoded + ` conn 0 sni www.shop.test host www.shop.test cookie "" origin "" referer ""`},
				},
				{
					req:        request(t, "https://WWW.shop.test:443/redirect"),
					redirected: true,
					want: []string{
						`/redirect conn 0 sni www.shop.test host www.shop.test cookie "" origin "" referer ""`,
						`/landed conn 0 sni www.shop.test host www.shop.test cookie "" origin "" referer ""`,
					},
				},
				{
					req:  request(t, "https://Bücher.shop.test/set"),
					want: []string{`/set conn 1 sni ` + idn + ` host ` + idn + ` cookie "" origin "" referer ""`},
				},
				{
					req:        request(t, "https://www.Shop.test/to-idn"),
					redirected: true,
					want: []string{
						`/to-idn conn 0 sni www.shop.test host www.shop.test cookie "" origin "" referer ""`,
						`/back?` + encoded + back,
					},
				},
				{
					req:  request(t, "https://bücher.shop.TEST:443/back?"+spelled),
					want: []string{`/back?` + encoded + back},
				},
				{
					// Without credentials (cross-origin), on a connection
					// of its own.
					req:  fetch,
					want: []string{`/api conn 2 sni www.shop.test host www.shop.test cookie "" origin "https://` + idn + `" referer "https://` + idn + `/"`},
				},
			} {
				if step.redirected && !h2 {
					continue
				}

				resp, err := client.Do(step.req)
				if err != nil {
					t.Fatal(err)
				}

				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()

				want = append(want, step.want...)
			}

			if !h2 {
				// A WebSocket's URL and page, over its own connection
				// (the shop's HTTP/2 offers no extended CONNECT).
				tr, _ := client.Transport.(*mimicry.Transport)

				sock, resp, err := tr.DialWebSocket(t.Context(), "wss://WWW.Shop.TEST:443/live", mimicry.WebSocketOptions{
					From: &url.URL{
						Scheme: "https",
						Host:   "Bücher.shop.test",
						Path:   "/",
					},
				})
				if err != nil {
					t.Fatal(err)
				}

				_ = sock.Close(1000, "")
				_ = resp.Body.Close()

				want = append(want, `/live conn 3 sni www.shop.test host www.shop.test cookie "" origin "https://`+idn+`" referer ""`)
			}

			mu.Lock()
			defer mu.Unlock()

			if !slices.Equal(seen, want) {
				t.Errorf("the shop saw\n%s\nwant\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// The URL standard's path and host parsing at the edges the urls capture
// does not reach: dot segments at the root and at the end, IPv4 forms past
// their limits, and the two rules browsers differ on ('|' in a path, an
// invalid "xn--" label), each as its profile has it, or ErrNotMeasured for a
// profile without URL rules.
func TestURLParserEdges(t *testing.T) {
	plan := func(t *testing.T, p profile.Profile, rawURL string) (string, error) {
		t.Helper()

		tr, err := mimicry.NewTransport(mimicry.Config{
			Profile: p,
		})
		if err != nil {
			t.Fatal(err)
		}

		pl, err := tr.Plan(request(t, rawURL))
		if err != nil {
			return "", err
		}

		return pseudo(pl.HTTP2, ":authority") + pseudo(pl.HTTP2, ":path"), nil
	}

	for _, tc := range []struct {
		url, want string
	}{
		{"https://a.test/b/..", "a.test/"},
		{"https://a.test/b/c/%2E%2e", "a.test/b/"},
		{"https://a.test/../../b", "a.test/b"},
		{"https://a.test/b/./", "a.test/b/"},
		{"https://a.test/b/.?c", "a.test/b/?c"},
		{"https://1.256/", "1.0.1.0/"},
		{"https://0x7f.0.0.0x1/", "127.0.0.1/"},
		{"https://4294967295/", "255.255.255.255/"},
		{"https://xn--bcher-kva.test/", "xn--bcher-kva.test/"},
	} {
		if got, err := plan(t, profiles.ChromeMacOS(), tc.url); err != nil || got != tc.want {
			t.Errorf("%s: %q, %v; want %q", tc.url, got, err, tc.want)
		}
	}

	for _, bad := range []string{
		"https://4294967296/",
		"https://1.2.3.4.5/",
		"https://08.1/",
		"https://a.08/",
		"https://1.256.1/",
	} {
		if got, err := plan(t, profiles.ChromeMacOS(), bad); err == nil || errors.Is(err, mimicry.ErrNotMeasured) {
			t.Errorf("%s: %q, %v; want the URL refused", bad, got, err)
		}
	}

	for _, tc := range []struct {
		p         profile.Profile
		bar, puny string
	}{
		{profiles.ChromeMacOS(), "a.test/a%7Cb", "xn--a.test/"},
		{profiles.FirefoxMacOS(), "a.test/a|b", "refused"},
		{profiles.SafariMacOS(), "a.test/a|b", "xn--a.test/"},
		{profiles.Safari26MacOS(), "a.test/a|b", "refused"},
		{profiles.Chrome155MacOS(), "not measured", "not measured"},
	} {
		name := tc.p.Spec().Name

		for _, c := range []struct{ url, want string }{
			{"https://a.test/a|b", tc.bar},
			{"https://xn--a.test/", tc.puny},
		} {
			got, err := plan(t, tc.p, c.url)

			switch {
			case errors.Is(err, mimicry.ErrNotMeasured):
				got = "not measured"
			case err != nil:
				got = "refused"
			}

			if got != c.want {
				t.Errorf("%s, %s: %q (%v), want %q", name, c.url, got, err, c.want)
			}
		}
	}
}

// request is a GET of rawURL on t's context.
func request(t *testing.T, rawURL string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	return req
}

// serverName is the SNI r's connection was opened with, on a shop of
// either kind.
func serverName(r *http.Request) string {
	if r.TLS != nil {
		return r.TLS.ServerName
	}

	c, ok := r.Context().Value(connKey{}).(*recordingConn)
	if !ok {
		return ""
	}

	tc, ok := c.Conn.(*tls.Conn)
	if !ok {
		return ""
	}

	return tc.ConnectionState().ServerName
}

// pseudo returns the value of the header line name in l, "" without one.
func pseudo(l mimicry.Lines, name string) string {
	for _, h := range l.Headers {
		if h.Name == name {
			return h.Value
		}
	}

	return ""
}
