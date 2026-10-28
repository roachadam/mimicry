package headerorder

import "net/http"

// canonMax bounds a Canon: how many names it keeps, and how long one may
// be. A server sending more distinct names only costs CanonicalHeaderKey.
const (
	canonMax     = 64
	canonNameMax = 64
)

// Canon is http.CanonicalHeaderKey with a cache for the names one
// connection's responses repeat, which spares allocating the names
// net/http does not intern (Alt-Svc, Vary, Access-Control-...). The zero
// value is ready; it is not safe for concurrent use.
type Canon struct {
	names map[string]string
}

// Key returns http.CanonicalHeaderKey(name).
func (c *Canon) Key(name string) string {
	if key, ok := c.names[name]; ok {
		return key
	}

	key := http.CanonicalHeaderKey(name)
	if len(name) > canonNameMax || len(c.names) >= canonMax {
		return key
	}

	if c.names == nil {
		c.names = make(map[string]string, canonMax)
	}

	c.names[name] = key

	return key
}

// ResponseHeader collects a response's fields into an http.Header, as
// net/http's HTTP/2 client does: one array holds a value for every field,
// and each name's slice is capped at its first value, so that a second one
// (or the caller's append) copies instead of overwriting the next name's.
type ResponseHeader struct {
	h     http.Header
	vals  []string
	canon *Canon
}

// NewResponseHeader starts a header for n fields (an upper bound), their
// names canonicalized by canon.
func NewResponseHeader(canon *Canon, n int) ResponseHeader {
	return ResponseHeader{
		h:     make(http.Header, n),
		vals:  make([]string, n),
		canon: canon,
	}
}

// Add adds a field.
func (r *ResponseHeader) Add(name, value string) {
	key := r.canon.Key(name)

	vv := r.h[key]
	if vv != nil || len(r.vals) == 0 {
		r.h[key] = append(vv, value)

		return
	}

	vv, r.vals = r.vals[:1:1], r.vals[1:]
	vv[0] = value
	r.h[key] = vv
}

// Header returns the header the fields made.
func (r *ResponseHeader) Header() http.Header {
	return r.h
}

// StatusLine is http.Response's Status for a :status value and the code
// strconv.Atoi parsed it to: status, a space and http.StatusText(code). The
// common ones cost no allocation.
func StatusLine(status string, code int) string {
	// Three characters parsing to 100 or more are exactly code's digits:
	// a sign or a leading zero leaves two.
	if len(status) == 3 {
		switch code {
		case http.StatusOK:
			return "200 OK"
		case http.StatusNoContent:
			return "204 No Content"
		case http.StatusPartialContent:
			return "206 Partial Content"
		case http.StatusMovedPermanently:
			return "301 Moved Permanently"
		case http.StatusFound:
			return "302 Found"
		case http.StatusNotModified:
			return "304 Not Modified"
		case http.StatusTemporaryRedirect:
			return "307 Temporary Redirect"
		case http.StatusNotFound:
			return "404 Not Found"
		}
	}

	return status + " " + http.StatusText(code)
}
