// Package site is what the benchmark's servers serve: fixed incompressible
// payloads, a small JSON document, and a page of one HTML document and 30
// subresources over three hosts. The Go server serves it from memory and
// cmd/setup writes the same bytes as files for nginx, so both servers send
// identical responses.
package site

import (
	"math/rand/v2"
	"strconv"
	"sync"
)

// Payload sizes the servers serve under /bytes/{n}: the throughput bodies
// (1 KB, 100 KB, 10 MB) and the sizes the page uses. nginx serves only
// these; the Go server serves any size up to MaxPayload.
const (
	KB = 1 << 10
	MB = 1 << 20

	// MaxPayload is the largest /bytes/{n} the Go server serves.
	MaxPayload = 16 * MB
)

// Bodies are the throughput scenarios' response sizes.
var Bodies = []int{
	1 * KB,
	100 * KB,
	10 * MB,
}

// JSON is the small JSON document GET /json returns.
const JSON = `{"id":1842,"name":"benchmark","tags":["fast","small","json"],"active":true,"score":0.9183,"owner":{"id":77,"login":"bench"},"updated":"2026-10-27T12:00:00Z"}`

// EchoBody is the small JSON body the benchmarks POST to /echo.
const EchoBody = `{"query":"mimicry","page":2,"filters":{"lang":"go","stars":">100"},"session":"b7f3c2a1"}`

// Resource is one subresource of the page.
type Resource struct {
	// Host is the label before the server's domain: www, static or api.
	Host string
	// Path is the URL path, under /page/.
	Path string
	// Kind is how a browser loads it: style, script, image, font or fetch.
	Kind string
	// Size is the body's length in bytes.
	Size int
}

// ContentType returns the resource's Content-Type.
func (r Resource) ContentType() string {
	return contentTypes[r.Kind]
}

var contentTypes = map[string]string{
	"document": "text/html",
	"style":    "text/css",
	"script":   "text/javascript",
	"image":    "image/webp",
	"font":     "font/woff2",
	"fetch":    "application/json",
}

// Hosts are the page's host labels; the HTML is on the first.
var Hosts = []string{
	"www",
	"static",
	"api",
}

// HTML is the page's document.
var HTML = Resource{
	Host: "www",
	Path: "/page/index.html",
	Kind: "document",
	Size: 48 * KB,
}

// Subresources are the 30 requests the page makes after its HTML: 10 on
// www, 14 on static and 6 on api, about 1.5 MB in all.
func Subresources() []Resource {
	return subresources
}

var subresources = buildSubresources()

func buildSubresources() []Resource {
	type group struct {
		host  string
		kind  string
		ext   string
		sizes []int
	}

	groups := []group{
		{
			host:  "www",
			kind:  "style",
			ext:   "css",
			sizes: []int{24 * KB, 12 * KB},
		},
		{
			host:  "www",
			kind:  "script",
			ext:   "js",
			sizes: []int{180 * KB, 96 * KB, 40 * KB, 12 * KB},
		},
		{
			host:  "www",
			kind:  "image",
			ext:   "webp",
			sizes: []int{64 * KB, 32 * KB, 18 * KB, 6 * KB},
		},
		{
			host:  "static",
			kind:  "image",
			ext:   "webp",
			sizes: []int{120 * KB, 88 * KB, 72 * KB, 56 * KB, 44 * KB, 30 * KB, 22 * KB, 16 * KB, 8 * KB, 3 * KB},
		},
		{
			host:  "static",
			kind:  "font",
			ext:   "woff2",
			sizes: []int{46 * KB, 38 * KB},
		},
		{
			host:  "static",
			kind:  "script",
			ext:   "js",
			sizes: []int{140 * KB, 60 * KB},
		},
		{
			host:  "api",
			kind:  "fetch",
			ext:   "json",
			sizes: []int{6 * KB, 4 * KB, 3 * KB, 2 * KB, 2 * KB, 1 * KB},
		},
	}

	var out []Resource

	for _, g := range groups {
		for i, size := range g.sizes {
			out = append(out, Resource{
				Host: g.host,
				Path: "/page/" + g.host + "-" + g.kind + "-" + strconv.Itoa(i+1) + "." + g.ext,
				Kind: g.kind,
				Size: size,
			})
		}
	}

	return out
}

// Payload returns the first n bytes of the benchmark's fixed incompressible
// data (n <= MaxPayload). Every server and every run sends the same bytes.
// The slice is shared: never modify it.
func Payload(n int) []byte {
	return payload()[:n]
}

var payload = sync.OnceValue(func() []byte {
	var seed [32]byte

	copy(seed[:], "mimicry benchmark payload seed 1")

	src := rand.NewChaCha8(seed)
	b := make([]byte, MaxPayload)

	_, _ = src.Read(b)

	return b
})

// Body returns a resource's body: payload bytes at an offset of its own, so
// resources differ from each other.
func (r Resource) Body() []byte {
	off := 0
	for _, c := range r.Path {
		off = (off*31 + int(c)) % (MaxPayload / 2)
	}

	return payload()[off : off+r.Size]
}

// Resources are the HTML and its subresources, for servers to register.
func Resources() []Resource {
	return append([]Resource{HTML}, subresources...)
}
