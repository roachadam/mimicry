// Package fingerprint compiles a profile once into an immutable plan, then fills
// in the per-connection randomness (GREASE values, a uniform extension shuffle,
// key shares) to produce the ClientHello for each connection.
//
// Extension shuffle (profile extension_order "shuffled"), as Chrome does it:
//   - a uniform permutation of the non-pinned extensions, drawn fresh for every
//     connection, never per client or per session; GREASE, padding and
//     pre_shared_key keep their positions;
//   - one permutation per handshake: a second ClientHello after a
//     HelloRetryRequest reuses it (uTLS resends our extension list as is).
//
// Known gap, HelloRetryRequest cookie: BoringSSL permutes its whole extension
// table once per handshake, so the cookie that appears only in the second
// ClientHello sits where that same permutation puts it. uTLS (fork patch 2)
// instead inserts the cookie at a random non-pinned position, which has a
// similar distribution but is not the same mechanism. To match exactly, the
// plan would pick the cookie's position as part of the permutation and the
// uTLS fork would need a hook to place it there. Deferred until a capture or
// the server-stack handshake tests show the difference matters; see
// HANDOFF.md, "Known gaps".
//
// Imports: profile and the standard library. It describes the ClientHello; it does
// not perform handshakes.
package fingerprint
