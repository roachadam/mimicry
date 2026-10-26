// Package h1 speaks HTTP/1.1 over an established connection. With an
// HTTP/1.1 list (the profile's http1, or the request plan's), every header,
// Host and framing included, goes in that list's order and casing; headers
// it does not name keep the caller's spelling. Without one, Host and
// Connection come first, then the HTTP/2 list's order, names in canonical
// form unless the caller spelled them otherwise. A plan whose variant was
// measured over HTTP/2 only is refused (headerorder.ErrNotMeasured).
//
// A request is validated (method, target, Host, header names and values)
// before anything is written. A body of unknown length (ContentLength -1,
// or 0 with a body) is sent chunked; a sized body must be exactly
// ContentLength bytes. Informational responses are skipped, except 101,
// which is final and leaves the connection unreusable (Hijack hands it,
// with what the server sent after the 101, to a WebSocket). A response
// body closed before its end closes the connection rather than reading
// the rest.
// Conn.Usable checks, without blocking, that an idle connection was not
// closed by the peer and holds no unexpected bytes, before it is reused.
//
// A Writer builds request heads (Writer.Head) and a Conn writes with
// one, so the engine's plans (Engine.Plan) get the lines a Conn writes.
//
// Imports: profile, headerorder, golang.org/x/net/http/httpguts and the
// standard library.
package h1
