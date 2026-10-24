// Package proxy is the capture proxy: HTTP (CONNECT and absolute-form) and
// SOCKS5 on one port. It records how a client asks for each connection and
// hands the connection on, so the capture server behind it records the
// rest as usual.
package proxy
