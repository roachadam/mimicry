package cookies_test

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/profiles"
)

// Seed a jar with cookies handed over as a Cookie header ("name=value;
// ..."), as another tool or a saved session might give them, then ask which
// of them the browser would send, and where.
func ExampleJar_Set() {
	jar, err := cookies.New(cookies.Config{
		Rules: profiles.Chrome155MacOS().Cookies,
	})
	if err != nil {
		log.Fatal(err)
	}
	// A header holds names and values only: the domain, path and
	// attributes are yours to choose. SameSite is left unset, which
	// Chrome treats as Lax.
	for pair := range strings.SplitSeq("session=s1; cart=c9", "; ") {
		name, value, _ := strings.Cut(pair, "=")
		if err := jar.Set(cookies.Cookie{
			Name:   name,
			Value:  value,
			Domain: "shop.test",
			Path:   "/",
			Secure: true,
		}); err != nil {
			log.Fatal(err)
		}
	}

	shop := mustParse("https://www.shop.test/cart")
	outlet := mustParse("https://www.outlet.test/")
	// Typed into the address bar: every cookie.
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
	// A fetch() from another site's page: none.
	fmt.Printf("%q\n", cookies.Header(jar.Cookies(cookies.Request{
		URL:    shop,
		Method: "GET",
		Page:   outlet,
	})))
	// Output:
	// "session=s1; cart=c9"
	// "session=s1; cart=c9"
	// ""
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
