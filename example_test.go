package mimicry_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/profiles"
)

// The examples run against a local stand-in shop (shop_test.go). Its handler
// prints what the server received, prefixed "server:"; everything else is
// what the client sees. Config.RootCAs and Config.DialContext point the
// client at the stand-in; leave them nil for real sites.

// Create a client for a browser profile and load a page.
func ExampleNewClient() {
	shop := startShop(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("server:", r.Proto, r.Method, r.URL.Path)
		fmt.Println("server: user-agent:", r.Header.Get("User-Agent"))
		fmt.Println("server: sec-fetch-mode:", r.Header.Get("Sec-Fetch-Mode"), "sec-fetch-site:", r.Header.Get("Sec-Fetch-Site"))
		_, _ = io.WriteString(w, "<title>Shop</title>")
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	// A deadline for the whole exchange, as with any http.Client (or the
	// request's context). Config.DialTimeout bounds connecting alone.
	client.Timeout = 30 * time.Second

	// A request with no kind is a navigation: the URL typed into the
	// address bar, with the browser's headers, in its order.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://www.shop.test/", nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	body, err := io.ReadAll(resp.Body) // decompressed, as the browser would
	_ = resp.Body.Close()
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Proto, resp.Status, string(body))
	// Output:
	// server: HTTP/2.0 GET /
	// server: user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36
	// server: sec-fetch-mode: navigate sec-fetch-site: none
	// HTTP/2.0 200 OK <title>Shop</title>
}

// Load a product page, then call the shop's API from it as the page's
// script would, with fetch().
func ExampleAs() {
	shop := startShop(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("server: %s %s sec-fetch-mode=%s referer=%q cookie=%q\n", r.Method, r.URL.Path,
			r.Header.Get("Sec-Fetch-Mode"), r.Header.Get("Referer"), r.Header.Get("Cookie"))
		switch r.URL.Path {
		case "/p/42":
			w.Header().Set("Set-Cookie", "session=s1; Path=/; Secure; HttpOnly")
			_, _ = io.WriteString(w, "<title>Kettle</title>")
		case "/api/stock/42":
			_, _ = io.WriteString(w, `{"sku":42,"inStock":true}`)
		}
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	page, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.test/p/42", nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(page)
	if err != nil {
		panic(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// The API call is a fetch() from the page the client last navigated
	// to: sec-fetch-*, Referer, Origin and cookies follow from that page.
	api, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.test/api/stock/42", nil)
	if err != nil {
		panic(err)
	}
	resp, err = client.Do(mimicry.As(api, mimicry.Fetch))
	if err != nil {
		panic(err)
	}
	var stock struct {
		SKU     int  `json:"sku"`
		InStock bool `json:"inStock"`
	}
	err = json.NewDecoder(resp.Body).Decode(&stock)
	_ = resp.Body.Close()
	if err != nil {
		panic(err)
	}
	fmt.Printf("sku %d in stock: %v\n", stock.SKU, stock.InStock)
	// Output:
	// server: GET /p/42 sec-fetch-mode=navigate referer="" cookie=""
	// server: GET /api/stock/42 sec-fetch-mode=cors referer="https://www.shop.test/p/42" cookie="session=s1"
	// sku 42 in stock: true
}

// Add to cart: a JSON POST from the product page, carrying the CSRF token
// the page holds.
func ExampleAs_addToCart() {
	shop := startShop(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/42":
			_, _ = io.WriteString(w, `<meta name="csrf-token" content="t0k3n">`)
		case "/api/cart":
			body, _ := io.ReadAll(r.Body)
			fmt.Println("server:", r.Method, r.URL.Path, string(body))
			fmt.Println("server: origin:", r.Header.Get("Origin"), "content-type:", r.Header.Get("Content-Type"),
				"x-csrf-token:", r.Header.Get("X-CSRF-Token"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"items":1}`)
		}
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	page, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.test/p/42", nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(page)
	if err != nil {
		panic(err)
	}
	html, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		panic(err)
	}
	token := regexp.MustCompile(`name="csrf-token" content="([^"]+)"`).FindSubmatch(html)[1]

	// Headers the page's script sets go on the request; the profile adds
	// the browser's own around them, in the browser's order, and Origin
	// for a POST. Same-origin, so no CORS preflight.
	add, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.shop.test/api/cart", strings.NewReader(`{"sku":42,"qty":1}`))
	if err != nil {
		panic(err)
	}
	add.Header.Set("Content-Type", "application/json")
	add.Header.Set("X-CSRF-Token", string(token))
	resp, err = client.Do(mimicry.As(add, mimicry.Fetch))
	if err != nil {
		panic(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Status, string(body))
	// Output:
	// server: POST /api/cart {"sku":42,"qty":1}
	// server: origin: https://www.shop.test content-type: application/json x-csrf-token: t0k3n
	// 200 OK {"items":1}
}

// Check out: a form POST navigation from the cart page, answered with a 303
// to the order page, and the redirect chain it took.
func ExampleWith() {
	shop := startShop(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("server: %s %s sec-fetch-site=%s origin=%q referer=%q\n", r.Method, r.URL.Path,
			r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Origin"), r.Header.Get("Referer"))
		switch r.URL.Path {
		case "/cart":
			_, _ = io.WriteString(w, `<form method="post" action="/checkout">`)
		case "/checkout":
			_ = r.ParseForm()
			fmt.Println("server: form:", r.PostForm.Encode())
			http.Redirect(w, r, "/orders/1001", http.StatusSeeOther)
		case "/orders/1001":
			_, _ = io.WriteString(w, "<title>Order 1001</title>")
		}
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	cart, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.test/cart", nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(cart)
	if err != nil {
		panic(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// A form POST is a navigation from the page holding the form: From is
	// that page. Its Content-Type is the request's, as the form's enctype.
	form := url.Values{
		"shipping": {"standard"},
		"agree":    {"on"},
	}
	checkout, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.shop.test/checkout", strings.NewReader(form.Encode()))
	if err != nil {
		panic(err)
	}
	checkout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(mimicry.With(checkout, mimicry.Request{
		Kind: mimicry.Navigate,
		From: cart.URL,
	}))
	if err != nil {
		panic(err)
	}
	_ = resp.Body.Close()

	// The client followed the 303 with a GET, as the browser does. Each
	// hop's request links to the response that redirected to it. (To stop
	// at the 303 instead, set client.CheckRedirect to return
	// http.ErrUseLastResponse.)
	var chain []string
	for r := resp.Request; r != nil; {
		chain = append(chain, r.Method+" "+r.URL.Path)
		if r.Response == nil {
			break
		}
		chain = append(chain, r.Response.Status)
		r = r.Response.Request
	}
	for i := len(chain) - 1; i >= 0; i-- {
		fmt.Println(chain[i])
	}
	fmt.Println("now on", resp.Request.URL)
	// Output:
	// server: GET /cart sec-fetch-site=none origin="" referer=""
	// server: POST /checkout sec-fetch-site=same-origin origin="https://www.shop.test" referer="https://www.shop.test/cart"
	// server: form: agree=on&shipping=standard
	// server: GET /orders/1001 sec-fetch-site=same-origin origin="" referer="https://www.shop.test/cart"
	// POST /checkout
	// 303 See Other
	// GET /orders/1001
	// now on https://www.shop.test/orders/1001
}

// Cookies live in the Transport's jar: read them, seed some obtained
// elsewhere, delete one, and save the site's as a string.
func ExampleConfig_cookies() {
	shop := startShop(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("server: %s cookie=%q\n", r.URL.Path, r.Header.Get("Cookie"))
		if r.URL.Path == "/" {
			w.Header().Add("Set-Cookie", "session=s1; Path=/; Secure; HttpOnly")
			w.Header().Add("Set-Cookie", "region=eu; Path=/; Max-Age=3600")
		}
	})
	defer shop.Close()

	// NewClient makes a jar with the profile's rules; Transport.Cookies
	// returns it. Pass Config.Cookies to share one between Transports.
	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	jar := client.Transport.(*mimicry.Transport).Cookies()
	get := func(rawURL string) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
		if err != nil {
			panic(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	get("https://www.shop.test/")
	for _, c := range jar.All() {
		fmt.Printf("jar: %s=%s domain=%s secure=%v session=%v\n", c.Name, c.Value, c.Domain, c.Secure, c.Expires.IsZero())
	}

	// Cookies from elsewhere (another tool, a saved session) as a Cookie
	// header string, for shop.test and every name under it.
	if err := jar.SetString("shop.test", "consent=yes"); err != nil {
		panic(err)
	}
	get("https://www.shop.test/cart")

	jar.Delete(func(c *cookies.Cookie) bool {
		return c.Name == "region"
	})
	get("https://www.shop.test/cart")
	fmt.Printf("saved: %q\n", jar.String("shop.test"))
	// Output:
	// server: / cookie=""
	// jar: session=s1 domain=www.shop.test secure=true session=true
	// jar: region=eu domain=www.shop.test secure=false session=false
	// server: /cart cookie="session=s1; region=eu; consent=yes"
	// server: /cart cookie="session=s1; consent=yes"
	// saved: "session=s1; consent=yes"
}

// One Transport per site, all sharing one jar: each site gets its own
// cookies, and the jar holds every site's.
func ExampleConfig_sharedJar() {
	shop := startShop(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("server: %s cookie=%q\n", r.Host, r.Header.Get("Cookie"))
		site, _, _ := strings.Cut(strings.TrimPrefix(r.Host, "www."), ".")
		w.Header().Set("Set-Cookie", site+"_visitor=1; Path=/; Secure")
	})
	defer shop.Close()

	p := profiles.Chrome155MacOS()
	jar, err := cookies.New(cookies.Config{
		Rules: p.Cookies,
	})
	if err != nil {
		panic(err)
	}
	newClient := func() *http.Client {
		client, err := mimicry.NewClient(mimicry.Config{
			Profile: p,
			Cookies: jar,
			// Only to reach the local stand-in shop; nil for real sites.
			RootCAs:     shop.RootCAs,
			DialContext: shop.DialContext,
		})
		if err != nil {
			panic(err)
		}
		return client
	}
	// A Transport is one browsing entity, like one tab: its last
	// navigation is the page its other requests come from, and its
	// connections are not split by site. Give each site (and each
	// parallel task) its own.
	sites := map[string]*http.Client{
		"https://www.shop.test/":   newClient(),
		"https://www.outlet.test/": newClient(),
	}
	for _, u := range []string{"https://www.shop.test/", "https://www.outlet.test/", "https://www.shop.test/", "https://www.outlet.test/"} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		if err != nil {
			panic(err)
		}
		resp, err := sites[u].Do(req)
		if err != nil {
			panic(err)
		}
		_ = resp.Body.Close()
	}
	for _, c := range jar.All() {
		fmt.Println("jar:", c.Domain, c.Name)
	}
	// Output:
	// server: www.shop.test cookie=""
	// server: www.outlet.test cookie=""
	// server: www.shop.test cookie="shop_visitor=1"
	// server: www.outlet.test cookie="outlet_visitor=1"
	// jar: www.shop.test shop_visitor
	// jar: www.outlet.test outlet_visitor
}

// Headers of your own and where they go: Config.Headers on every request,
// the request's own on one, and HeaderOrderKey to set the order yourself.
// (This shop speaks HTTP/1.1 so that its handler can print the header lines
// in the order they arrived; over HTTP/2 the order is the same, without
// Host and Connection.)
func ExampleHeaderOrderKey() {
	shop := startShopHTTP1(func(_ http.ResponseWriter, r *http.Request) {
		lines := headerLines(r)
		if r.URL.Path == "/" {
			return
		}
		fmt.Println("server:", r.Method, r.URL.Path)
		for _, line := range lines {
			fmt.Println("server:  ", line)
		}
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// On every request, in the place of the profile's own value.
		Headers: http.Header{
			"Accept-Language": {"de-DE,de;q=0.9"},
		},
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	newRequest := func(rawURL string) *http.Request {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
		if err != nil {
			panic(err)
		}
		return req
	}
	send := func(req *http.Request) {
		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		_ = resp.Body.Close()
	}

	send(newRequest("https://www.shop.test/"))

	// A header the page's script sets goes where the browser would put
	// it, its name as fetch() sends it.
	api := newRequest("https://www.shop.test/api/cart")
	api.Header.Set("X-CSRF-Token", "t0k3n")
	send(mimicry.As(api, mimicry.Fetch))

	// HeaderOrderKey replaces the order outright: a profile header it does
	// not name is not sent, and a header of the request's it does not name
	// goes last. So name them all, Host and Connection included over
	// HTTP/1.1. Needed only to copy an order the profile does not have.
	custom := newRequest("https://www.shop.test/api/cart")
	custom.Header.Set("X-CSRF-Token", "t0k3n")
	custom.Header[mimicry.HeaderOrderKey] = []string{
		"host", "connection", "sec-ch-ua-platform", "user-agent", "sec-ch-ua", "sec-ch-ua-mobile", "accept",
		"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer", "accept-encoding", "accept-language",
		"x-csrf-token",
	}
	send(mimicry.As(custom, mimicry.Fetch))
	// Output:
	// server: GET /api/cart
	// server:   Host: www.shop.test
	// server:   Connection: keep-alive
	// server:   sec-ch-ua-platform: "macOS"
	// server:   x-csrf-token: t0k3n
	// server:   User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36
	// server:   sec-ch-ua: "Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"
	// server:   sec-ch-ua-mobile: ?0
	// server:   Accept: */*
	// server:   Sec-Fetch-Site: same-origin
	// server:   Sec-Fetch-Mode: cors
	// server:   Sec-Fetch-Dest: empty
	// server:   Referer: https://www.shop.test/
	// server:   Accept-Encoding: gzip, deflate, br, zstd
	// server:   Accept-Language: de-DE,de;q=0.9
	// server: GET /api/cart
	// server:   Host: www.shop.test
	// server:   Connection: keep-alive
	// server:   sec-ch-ua-platform: "macOS"
	// server:   User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36
	// server:   sec-ch-ua: "Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"
	// server:   sec-ch-ua-mobile: ?0
	// server:   Accept: */*
	// server:   Sec-Fetch-Site: same-origin
	// server:   Sec-Fetch-Mode: cors
	// server:   Sec-Fetch-Dest: empty
	// server:   Referer: https://www.shop.test/
	// server:   Accept-Encoding: gzip, deflate, br, zstd
	// server:   Accept-Language: de-DE,de;q=0.9
	// server:   x-csrf-token: t0k3n
}

// Proxies: Config.Proxy picks one per request, so rotating is changing what
// it returns. A *ProxyError says the proxy failed (unreachable, refused the
// tunnel, rejected its credentials), not the site: take the next one. Each
// proxy has its own connections; the cookies and the last page stay the
// Transport's.
func ExampleConfig_proxy() {
	shop := startShop(func(_ http.ResponseWriter, r *http.Request) {
		fmt.Println("server:", r.URL.Path)
	})
	defer shop.Close()
	first := startProxy(shop, "alice", "secret")
	defer first.Close()
	second := startProxy(shop, "", "")
	defer second.Close()
	// The first proxy again, with a password it no longer takes.
	expired := *first.URL
	expired.User = url.UserPassword("alice", "expired")

	proxies := []*url.URL{&expired, first.URL, second.URL}
	var current atomic.Pointer[url.URL]
	current.Store(proxies[0])
	rotate := func() {
		proxies = proxies[1:]
		current.Store(proxies[0])
	}
	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		Proxy: func(*http.Request) (*url.URL, error) {
			return current.Load(), nil
		},
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	get := func(rawURL string) {
		for {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
			if err != nil {
				panic(err)
			}
			resp, err := client.Do(req)
			var pe *mimicry.ProxyError
			if errors.As(err, &pe) {
				fmt.Printf("proxy failed: %d as %s (password in the error: %t)\n",
					pe.StatusCode, pe.Proxy.User.Username(), strings.Contains(err.Error(), "expired"))
				rotate()
				continue
			}
			if err != nil {
				panic(err)
			}
			_ = resp.Body.Close()
			return
		}
	}

	// The credentials go as Chrome sends them: not until the proxy asks.
	get("https://www.shop.test/")
	get("https://www.shop.test/cart") // the same tunnel
	rotate()                          // say, after a 403 from the site
	get("https://www.shop.test/cart") // a new tunnel, through the second proxy

	for _, line := range first.Log() {
		fmt.Println("first proxy:", line)
	}
	for _, line := range second.Log() {
		fmt.Println("second proxy:", line)
	}
	// Output:
	// proxy failed: 407 as alice (password in the error: false)
	// server: /
	// server: /cart
	// server: /cart
	// first proxy: CONNECT www.shop.test:443 without credentials: 407
	// first proxy: CONNECT www.shop.test:443 as alice: 407
	// first proxy: CONNECT www.shop.test:443 without credentials: 407
	// first proxy: CONNECT www.shop.test:443 as alice: 200
	// second proxy: CONNECT www.shop.test:443 without credentials: 200
}

// HTTP/3 is on by default: once a site advertises it (Alt-Svc), requests
// move to it as the profile's browser moves them. DisableHTTP3 keeps
// everything on TCP, for networks that block UDP. (Requests through a
// proxy never use HTTP/3.)
func ExampleConfig_disableHTTP3() {
	shop := startShop(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Alt-Svc", `h3=":443"; ma=86400`)
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile:      profiles.Chrome155MacOS(),
		DisableHTTP3: true,
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	for range 3 {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://www.shop.test/", nil)
		if err != nil {
			panic(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		_ = resp.Body.Close()
		fmt.Println(resp.Proto)
	}
	// Output:
	// HTTP/2.0
	// HTTP/2.0
	// HTTP/2.0
}

// Certificate pinning: FetchPins lists a site's verified chain (cmd/pins
// prints it), and Config.Pins holds a host to keys from it, the
// intermediate's being the usual choice (it outlives the leaf's
// renewals). A connection whose chain has none of them fails with a
// *PinError before anything is sent; every response carries its
// connection's TLS state.
func ExampleConfig_pins() {
	shop := startShop(func(http.ResponseWriter, *http.Request) {})
	defer shop.Close()
	cfg := mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	chain, err := mimicry.FetchPins(ctx, cfg, "https://www.shop.test/")
	if err != nil {
		panic(err)
	}
	var intermediate string
	for _, c := range chain {
		fmt.Println(c.Role, c.Subject)
		if c.Role == mimicry.CertIntermediate {
			intermediate = c.SPKI // "sha256/<base64>"
		}
	}

	get := func(pin string) {
		cfg.Pins = []mimicry.Pin{
			{
				Host:              "shop.test",
				IncludeSubdomains: true,
				SPKI: []string{
					pin,
				},
			},
		}
		client, err := mimicry.NewClient(cfg)
		if err != nil {
			panic(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.test/", nil)
		if err != nil {
			panic(err)
		}
		resp, err := client.Do(req)
		var pe *mimicry.PinError
		if errors.As(err, &pe) {
			fmt.Println("refused:", pe.Host, "presented", len(pe.Presented), "keys")
			return
		}
		if err != nil {
			panic(err)
		}
		_ = resp.Body.Close()
		fmt.Println(resp.Status, "over", resp.TLS.NegotiatedProtocol, "from", resp.TLS.VerifiedChains[0][1].Subject.CommonName)
	}
	get(intermediate)
	get("sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=") // someone else's key
	// Output:
	// leaf CN=shop.test
	// intermediate CN=Shop Test Issuing CA
	// root CN=Shop Test Root CA
	// 200 OK over h2 from Shop Test Issuing CA
	// refused: www.shop.test presented 3 keys
}

// A request the profile's browser was never captured making is an error
// wrapping ErrNotMeasured, and nothing is sent: mimicry does not guess.
// React by dropping what was not measured, or by choosing a profile that
// has it.
func ExampleErrNotMeasured() {
	shop := startShop(func(_ http.ResponseWriter, r *http.Request) {
		fmt.Println("server:", r.URL.Path, "priority:", r.Header.Get("Priority"))
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}

	// <img fetchpriority="high">: Chrome's captures have no such image.
	hero := mimicry.Request{
		Kind: mimicry.Image,
		From: &url.URL{
			Scheme: "https",
			Host:   "www.shop.test",
			Path:   "/",
		},
		Priority: mimicry.PriorityHigh,
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://www.shop.test/hero.avif", nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(mimicry.With(req, hero))
	if errors.Is(err, mimicry.ErrNotMeasured) {
		fmt.Println(err)
		hero.Priority = mimicry.PriorityAuto
		resp, err = client.Do(mimicry.With(req, hero))
	}
	if err != nil {
		panic(err)
	}
	_ = resp.Body.Close()
	fmt.Println(resp.Status)
	// Output:
	// Get "https://www.shop.test/hero.avif": mimicry: not measured for this profile: profile chrome-155-macos has no measured fetchpriority high for image requests
	// server: /hero.avif priority: u=2, i
	// 200 OK
}

// Debugging a fetch() ported from tls-client: Plan shows what a request
// would send, line by line, over the protocol it would take, without
// sending it, and catches the header literals the port copied over the
// browser's own values. (Plan.String prints all of it for a log.)
func ExampleTransport_Plan() {
	shop := startShop(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("server:", r.Method, r.URL.Path, "sec-fetch-site:", r.Header.Get("Sec-Fetch-Site"))
		if r.URL.Path == "/p/42" {
			w.Header().Set("Set-Cookie", "session=s1; Path=/; Secure; HttpOnly")
		}
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	tr := client.Transport.(*mimicry.Transport)
	ctx := context.Background()
	page, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.test/p/42", nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(page)
	if err != nil {
		panic(err)
	}
	_ = resp.Body.Close()

	// The stock API call as ported, with a header literal of the old
	// code's left in.
	api, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.test/api/stock/42", nil)
	if err != nil {
		panic(err)
	}
	api.Header.Set("Sec-Fetch-Site", "none")
	plan, err := tr.Plan(mimicry.As(api, mimicry.Fetch))
	if err != nil {
		panic(err)
	}
	fmt.Println("variant:", plan.Variant, plan.Condition)
	for _, o := range plan.Overrides {
		fmt.Printf("override: %s: %q replaced by %q\n", o.Name, o.Mimicry, o.Caller)
	}

	// Drop it, look again, and send what was planned.
	api.Header.Del("Sec-Fetch-Site")
	plan, err = tr.Plan(mimicry.As(api, mimicry.Fetch))
	if err != nil {
		panic(err)
	}
	fmt.Println("overrides:", len(plan.Overrides), "protocol:", plan.Protocol)
	for _, h := range plan.HTTP2.Headers {
		fmt.Printf("  %s: %s\n", h.Name, h.Value)
	}
	resp, err = client.Do(mimicry.As(api, mimicry.Fetch))
	if err != nil {
		panic(err)
	}
	_ = resp.Body.Close()
	// Output:
	// server: GET /p/42 sec-fetch-site: none
	// variant: kinds.fetch.headers [method GET site same-origin]
	// override: sec-fetch-site: "same-origin" replaced by ["none"]
	// overrides: 0 protocol: HTTP/2.0
	//   :method: GET
	//   :authority: www.shop.test
	//   :scheme: https
	//   :path: /api/stock/42
	//   sec-ch-ua-platform: "macOS"
	//   user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36
	//   sec-ch-ua: "Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"
	//   sec-ch-ua-mobile: ?0
	//   accept: */*
	//   sec-fetch-site: same-origin
	//   sec-fetch-mode: cors
	//   sec-fetch-dest: empty
	//   referer: https://www.shop.test/p/42
	//   accept-encoding: gzip, deflate, br, zstd
	//   accept-language: en-US,en;q=0.9
	//   cookie: session=s1
	//   priority: u=1, i
	// server: GET /api/stock/42 sec-fetch-site: same-origin
}

// A WebSocket from the page, opened as the browser opens one: its
// handshake, cookies and frames.
func ExampleTransport_DialWebSocket() {
	shop := startShop(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live" {
			fmt.Println("server: websocket from", r.Header.Get("Origin"), "cookie:", r.Header.Get("Cookie"))
			echoWebSocket(w, r)
			return
		}
		w.Header().Set("Set-Cookie", "session=s1; Path=/; Secure")
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.shop.test/queue", nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	_ = resp.Body.Close()

	// The socket goes through the client's Transport, from its last page.
	tr := client.Transport.(*mimicry.Transport)
	ws, _, err := tr.DialWebSocket(ctx, "wss://www.shop.test/live", mimicry.WebSocketOptions{})
	if err != nil {
		panic(err)
	}
	if err := ws.WriteMessage(ctx, mimicry.TextMessage, []byte("position?")); err != nil {
		panic(err)
	}
	_, msg, err := ws.ReadMessage(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(msg))
	if err := ws.Close(1000, ""); err != nil {
		panic(err)
	}
	// Output:
	// server: websocket from https://www.shop.test cookie: session=s1
	// echo: position?
}

// Header placement: the browser's order with one deliberate change, where
// a server expects a header somewhere the browser would not put it.
// Config.HeaderPlacement applies to every request, Request.HeaderPlacement
// to one, after Config's; every other line stays where the browser puts
// it. (This shop speaks HTTP/1.1 so that its handler can print the header
// lines in the order they arrived.)
func ExampleConfig_headerPlacement() {
	shop := startShopHTTP1(func(_ http.ResponseWriter, r *http.Request) {
		lines := headerLines(r)
		if r.URL.Path == "/" {
			return
		}
		fmt.Println("server:", r.Method, r.URL.Path)
		for _, line := range lines {
			fmt.Println("server:  ", line)
		}
	})
	defer shop.Close()

	client, err := mimicry.NewClient(mimicry.Config{
		Profile: profiles.Chrome155MacOS(),
		Headers: http.Header{
			"X-Api-Key": {"k3y"},
		},
		// Without it the key would go where Chrome puts a page's own
		// headers; with it, directly after User-Agent, on every request.
		HeaderPlacement: []mimicry.Place{
			{
				Header: "X-Api-Key",
				After:  "User-Agent",
			},
		},
		// Only to reach the local stand-in shop; nil for real sites.
		RootCAs:     shop.RootCAs,
		DialContext: shop.DialContext,
	})
	if err != nil {
		panic(err)
	}
	newRequest := func(rawURL string) *http.Request {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
		if err != nil {
			panic(err)
		}
		req.Header.Set("X-CSRF-Token", "t0k3n")
		return req
	}
	send := func(req *http.Request) error {
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	if err := send(newRequest("https://www.shop.test/")); err != nil {
		panic(err)
	}

	// The page's own header where Chrome puts it.
	if err := send(mimicry.As(newRequest("https://www.shop.test/api/cart"), mimicry.Fetch)); err != nil {
		panic(err)
	}

	// The same request with its token moved to just before Accept.
	if err := send(mimicry.With(newRequest("https://www.shop.test/api/cart"), mimicry.Request{
		Kind: mimicry.Fetch,
		HeaderPlacement: []mimicry.Place{
			{
				Header: "X-CSRF-Token",
				Before: "Accept",
			},
		},
	})); err != nil {
		panic(err)
	}

	// An anchor the request does not send (this shop set no cookies) is
	// an error, and nothing is sent.
	err = send(mimicry.With(newRequest("https://www.shop.test/api/cart"), mimicry.Request{
		Kind: mimicry.Fetch,
		HeaderPlacement: []mimicry.Place{
			{
				Header: "X-CSRF-Token",
				After:  "Cookie",
			},
		},
	}))
	fmt.Println("placement error:", errors.Is(err, mimicry.ErrPlacement))
	// Output:
	// server: GET /api/cart
	// server:   Host: www.shop.test
	// server:   Connection: keep-alive
	// server:   sec-ch-ua-platform: "macOS"
	// server:   x-csrf-token: t0k3n
	// server:   User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36
	// server:   x-api-key: k3y
	// server:   sec-ch-ua: "Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"
	// server:   sec-ch-ua-mobile: ?0
	// server:   Accept: */*
	// server:   Sec-Fetch-Site: same-origin
	// server:   Sec-Fetch-Mode: cors
	// server:   Sec-Fetch-Dest: empty
	// server:   Referer: https://www.shop.test/
	// server:   Accept-Encoding: gzip, deflate, br, zstd
	// server:   Accept-Language: en-US,en;q=0.9
	// server: GET /api/cart
	// server:   Host: www.shop.test
	// server:   Connection: keep-alive
	// server:   sec-ch-ua-platform: "macOS"
	// server:   User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36
	// server:   x-api-key: k3y
	// server:   sec-ch-ua: "Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"
	// server:   sec-ch-ua-mobile: ?0
	// server:   x-csrf-token: t0k3n
	// server:   Accept: */*
	// server:   Sec-Fetch-Site: same-origin
	// server:   Sec-Fetch-Mode: cors
	// server:   Sec-Fetch-Dest: empty
	// server:   Referer: https://www.shop.test/
	// server:   Accept-Encoding: gzip, deflate, br, zstd
	// server:   Accept-Language: en-US,en;q=0.9
	// placement error: true
}
