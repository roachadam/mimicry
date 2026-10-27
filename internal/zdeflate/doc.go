// Package zdeflate is permessage-deflate (RFC 7692) as the browsers speak
// it. Writer compresses each message byte for byte as its browser's
// deflate library does, with a sync flush, at memLevel 8, the default
// strategy and the socket's window bits (9 to 15; every library here
// rejects a raw stream of 8), the context kept from message to message
// until Reset (deflateReset, for client_no_context_takeover). Reader
// inflates a peer's messages with compress/flate, keeping the same
// context.
//
// Writer ports three libraries:
//
//   - zlib 1.3.1 (Safari; deflate.c, trees.c): hash chains,
//     longest_match, deflate_fast and deflate_slow with zlib's
//     configuration table (levels 1-9), block splitting, Huffman tree
//     building. zlib 1.3.2 writes the same bytes.
//   - Chromium's zlib (Chrome; third_party/zlib, zlib 1.3.2 with
//     Chromium's changes): the same, but for its hash of 4 bytes
//     (insert_string.h) and what follows from it in fill_window and
//     longest_match, as built for x64 and arm64.
//   - zlib-rs 0.6.3 (Firefox 158, which vendors it unmodified behind its
//     zlib C API) at level 6, Firefox's Z_DEFAULT_COMPRESSION:
//     deflate_medium, its 4-byte multiplicative hash and longest_match,
//     fill_window, with zlib's trees, which zlib-rs keeps. deflate_medium
//     forgets its look-ahead match when the output buffer fills, so the
//     port also drives deflate as Firefox's WebSocketChannel does, into
//     4096-byte buffers. zlib-rs picks SIMD code by CPU only for
//     compare256 and slide_hash, which compute the same everywhere, so
//     x86 and arm64 Firefox write the same bytes.
//
// Licences: this package is an altered version of zlib, of Chromium's zlib
// and of zlib-rs, translated to Go and reduced to deflate at the settings
// above; it is not the original software. zlib is (C) 1995-2026
// Jean-loup Gailly and Mark Adler, zlib-rs (C) 2024 Trifecta Tech
// Foundation, both under the zlib licence; Chromium's changes are
// Copyright 2015 The Chromium Authors under a BSD licence. Their notices
// are kept, unaltered, in LICENSE-zlib, LICENSE-zlib-rs and
// LICENSE-chromium in this directory.
//
// Imports: the standard library.
package zdeflate
