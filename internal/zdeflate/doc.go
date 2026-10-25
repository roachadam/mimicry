// Package zdeflate is permessage-deflate (RFC 7692) as the browsers that
// use zlib speak it. Writer is a port of zlib 1.3.1's deflate (deflate.c,
// trees.c: hash chains, longest_match, deflate_fast and deflate_slow with
// zlib's configuration table, block splitting, Huffman tree building) at
// window bits 15, memLevel 8 and the default strategy, so each message is
// byte for byte what zlib's raw deflate with a sync flush produces, the
// compression context kept from message to message. Reader inflates a
// peer's messages with compress/flate, keeping the same context.
//
// Imports: the standard library.
package zdeflate
