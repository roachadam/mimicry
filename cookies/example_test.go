package cookies_test

import (
	"bytes"
	"fmt"
	"log"
	"net/url"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/profiles"
)

// Seed a cookie whose attributes you know (a saved session's, another
// tool's export), then ask which requests the browser would send it with.
// For a Cookie header string ("name=value; ..."), SetString does this for
// each pair.
func ExampleJar_Set() {
	jar, err := cookies.New(cookies.Config{
		Rules: profiles.Chrome155MacOS().Cookies,
	})
	if err != nil {
		log.Fatal(err)
	}
	// For every host of shop.test, over HTTPS only. SameSite is left
	// unset, which Chrome treats as Lax.
	if err := jar.Set(cookies.Cookie{
		Name:   "session",
		Value:  "s1",
		Domain: "shop.test",
		Path:   "/",
		Secure: true,
	}); err != nil {
		log.Fatal(err)
	}

	shop := mustParse("https://www.shop.test/cart")
	outlet := mustParse("https://www.outlet.test/")
	// Typed into the address bar: sent.
	fmt.Printf("%q\n", cookies.Header(jar.Cookies(cookies.Request{
		URL:        shop,
		Method:     "GET",
		Navigation: true,
	})))
	// A link from another site: Lax cookies go on a top-level GET.
	fmt.Printf("%q\n", cookies.Header(jar.Cookies(cookies.Request{
		URL:        shop,
		Method:     "GET",
		Navigation: true,
		Page:       outlet,
	})))
	// A fetch() from another site's page: not sent.
	fmt.Printf("%q\n", cookies.Header(jar.Cookies(cookies.Request{
		URL:    shop,
		Method: "GET",
		Page:   outlet,
	})))
	// Plain HTTP: not sent, the cookie is Secure.
	fmt.Printf("%q\n", cookies.Header(jar.Cookies(cookies.Request{
		URL:        mustParse("http://www.shop.test/"),
		Method:     "GET",
		Navigation: true,
	})))
	// Output:
	// "session=s1"
	// "session=s1"
	// ""
	// ""
}

// Start from a Cookie header string kept from an earlier session (another
// tool's, a database row), browse, then keep the site's cookies as a
// string again.
func ExampleJar_SetString() {
	// A host or URL of the site does as well as "shop.test".
	jar, err := cookies.FromString("https://www.shop.test/", "session=s1; cart=c9", cookies.Config{
		Rules: profiles.Chrome155MacOS().Cookies,
	})
	if err != nil {
		log.Fatal(err)
	}
	// Every host of shop.test gets them; no other site does.
	for _, u := range []string{"https://www.shop.test/cart", "https://api.shop.test/v1/stock", "https://www.outlet.test/"} {
		fmt.Printf("%s %q\n", u, cookies.Header(jar.Cookies(cookies.Request{
			URL:        mustParse(u),
			Method:     "GET",
			Navigation: true,
		})))
	}

	// The site's responses set more (mimicry's Transport stores them;
	// here by hand). Its own Domain cookie named cart replaces the seeded
	// one.
	jar.SetCookies(cookies.Request{
		URL:        mustParse("https://www.shop.test/cart"),
		Method:     "GET",
		Navigation: true,
	}, []string{
		"cart=c10; Domain=shop.test; Path=/",
		"seen=1; Path=/; Max-Age=3600",
	})
	fmt.Printf("%q\n", jar.String("shop.test"))
	// Output:
	// https://www.shop.test/cart "session=s1; cart=c9"
	// https://api.shop.test/v1/stock "session=s1; cart=c9"
	// https://www.outlet.test/ ""
	// "session=s1; cart=c10; seen=1"
}

// A cookie for every site, such as a flag you want every server to see,
// is not something a browser has: ask for it explicitly with AnySite.
// Every site the jar talks to receives it, trackers and CDNs included,
// and a site's own cookie of the same name goes as well.
func ExampleJar_SetString_anySite() {
	jar, err := cookies.New(cookies.Config{
		Rules: profiles.Chrome155MacOS().Cookies,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := jar.SetString(cookies.AnySite, "debug=1"); err != nil {
		log.Fatal(err)
	}
	if err := jar.SetString("shop.test", "debug=0"); err != nil {
		log.Fatal(err)
	}
	for _, u := range []string{"https://www.shop.test/", "https://cdn.tracker.test/pixel.gif"} {
		fmt.Printf("%s %q\n", u, cookies.Header(jar.Cookies(cookies.Request{
			URL:        mustParse(u),
			Method:     "GET",
			Navigation: true,
		})))
	}
	fmt.Printf("%q\n", jar.String(cookies.AnySite))
	// Output:
	// https://www.shop.test/ "debug=1; debug=0"
	// https://cdn.tracker.test/pixel.gif "debug=1"
	// "debug=1"
}

// Save a jar between runs and restore it, every attribute kept.
func ExampleJar_WriteJSON() {
	rules := profiles.Chrome155MacOS().Cookies
	jar, err := cookies.New(cookies.Config{
		Rules: rules,
	})
	if err != nil {
		log.Fatal(err)
	}
	home := mustParse("https://www.shop.test/")
	jar.SetCookies(cookies.Request{
		URL:        home,
		Method:     "GET",
		Navigation: true,
	}, []string{
		"session=s1; Path=/; Secure; HttpOnly; SameSite=Lax",
		"cart=c9; Domain=shop.test; Path=/; Max-Age=86400",
	})

	var saved bytes.Buffer // a file, or a row in a database
	if err := jar.WriteJSON(&saved); err != nil {
		log.Fatal(err)
	}

	restored, err := cookies.New(cookies.Config{
		Rules: rules,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := restored.ReadJSON(&saved); err != nil {
		log.Fatal(err)
	}
	for _, c := range restored.All() {
		fmt.Printf("%s=%s domain=%s host-only=%v same-site=%q\n", c.Name, c.Value, c.Domain, c.HostOnly, c.SameSite)
	}
	// Output:
	// session=s1 domain=www.shop.test host-only=true same-site="Lax"
	// cart=c9 domain=shop.test host-only=false same-site=""
}

func mustParse(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		log.Fatal(err)
	}
	return u
}
