// Package ws is the WebSocket protocol (RFC 6455) after the handshake, as
// the profile's browser speaks it: every client frame final, masked with a
// fresh key, its length in the shortest form; permessage-deflate (RFC
// 7692) with the browser's compressor through internal/zdeflate, and its
// rules for messages it sends uncompressed (Compressor); frames cut into
// HTTP/2 DATA frames as the browser cuts them (profile.WebSocket H2Data);
// pings answered at once; the server's close echoed with its code and
// reason; and the transport ended after the close handshake as the
// browser ends it (profile.WebSocket CloseH1, CloseH2). It also makes and
// checks the handshake's key, accept key and extension answer. The
// transport underneath (an HTTP/1.1 connection or an HTTP/2 stream) is the
// caller's, behind Transport.
//
// Imports: profile, zdeflate, clockwork and the standard library.
package ws
