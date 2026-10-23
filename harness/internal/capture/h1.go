package capture

// H1Request is one HTTP/1.1 request as the client sent it, casing and order
// intact.
type H1Request struct {
	// Conn numbers the connection it came on, from 1 in order of arrival:
	// browsers spread an HTTP/1.1 page over several.
	Conn int `json:"conn"`
	// Line is the request line ("GET / HTTP/1.1").
	Line string `json:"line"`
	// Headers are the header lines, verbatim ("User-Agent: ...").
	Headers []string `json:"headers"`
}
