// Package dial is the single path from an address to a connected socket. Every
// step honours the context and its deadline. Today it dials TCP directly;
// proxies (internal/proxy) and resolver policy (internal/dns) plug in here, so
// no other package opens connections.
//
// Imports: dns, proxy and the standard library.
package dial
