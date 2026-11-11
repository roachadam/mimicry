package convert

import (
	"errors"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/internal/rpc/pb"
)

// A URL field that fails to parse is refused (CODE_INVALID) with what is
// wrong with it, never the URL itself: url.Parse's error quotes the URL,
// and a proxy's or a page's may hold credentials. Every URL field the
// core parses has a row, each tried with a bad port and a control
// character after the credentials.
func TestURLErrorsHideCredentials(t *testing.T) {
	for _, tc := range []struct {
		field string
		parse func(bad string) error
	}{
		{
			field: "config.proxy",
			parse: func(bad string) error {
				_, err := Config(&pb.Config{
					Proxy: bad,
				})

				return err
			},
		},
		{
			field: "config.connect_udp_proxy key",
			parse: func(bad string) error {
				_, err := Config(&pb.Config{
					ConnectUdpProxy: map[string]string{
						bad: "https://masque.test/",
					},
				})

				return err
			},
		},
		{
			field: "config.connect_udp_proxy value",
			parse: func(bad string) error {
				_, err := Config(&pb.Config{
					ConnectUdpProxy: map[string]string{
						"http://user:secret@proxy.test:8080": bad,
					},
				})

				return err
			},
		},
		{
			field: "request.url",
			parse: func(bad string) error {
				_, err := Request(t.Context(), &pb.Request{
					Url: bad,
				}, nil)

				return err
			},
		},
		{
			field: "request.proxy",
			parse: func(bad string) error {
				_, err := Request(t.Context(), &pb.Request{
					Url:   "https://shop.test/",
					Proxy: &bad,
				}, nil)

				return err
			},
		},
		{
			field: "options.page",
			parse: func(bad string) error {
				_, err := Options(&pb.Options{
					Page: bad,
				})

				return err
			},
		},
		{
			field: "options.stylesheet",
			parse: func(bad string) error {
				_, err := Options(&pb.Options{
					Stylesheet: bad,
				})

				return err
			},
		},
		{
			field: "WebSocket options.page",
			parse: func(bad string) error {
				_, err := WebSocketOptions(&pb.WebSocketOptions{
					Page: bad,
				})

				return err
			},
		},
		{
			field: "cookie request.url",
			parse: func(bad string) error {
				_, err := CookieRequest(&pb.CookieRequest{
					Url: bad,
				})

				return err
			},
		},
		{
			field: "cookie request.page",
			parse: func(bad string) error {
				_, err := CookieRequest(&pb.CookieRequest{
					Url:  "https://shop.test/",
					Page: bad,
				})

				return err
			},
		},
		{
			field: "cookie request.redirects",
			parse: func(bad string) error {
				_, err := CookieRequest(&pb.CookieRequest{
					Url: "https://shop.test/",
					Redirects: []string{
						bad,
					},
				})

				return err
			},
		},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for _, bad := range []string{
				"http://user:secret@proxy.test:bad port/",
				"http://user:secret@proxy.test/\x7f",
			} {
				err := tc.parse(bad)
				if _, ok := errors.AsType[*InvalidError](err); !ok {
					t.Fatalf("%q: error %v, want an *InvalidError", bad, err)
				}

				if strings.Contains(err.Error(), "secret") {
					t.Errorf("%q: error %q holds the password", bad, err)
				}
			}
		})
	}
}
