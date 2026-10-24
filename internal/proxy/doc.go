// Package proxy connects through SOCKS5 and HTTP CONNECT proxies, bounded by the
// caller's context. A CONNECT answer's header is read up to 64 KiB
// (ErrAnswerTooLarge).
//
// Imports: the standard library only.
package proxy
