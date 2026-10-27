// Package e2e proves the profiles against the real browsers: it replays each
// browser's captures (harness/testdata/captures) through mimicry and fails
// on any difference not pinned as a known gap. Profile accuracy is proven
// here, not by unit tests that restate a profile's values.
//
// Each test checks the profiles MIMICRY_PROFILES selects (testprofiles,
// decision 0035) against its browser's captures: its own or, for a scenario
// its captures_from names, an earlier version's (TestInheritedProfile proves
// that path with a synthetic profile). Known differences are pinned in gap
// tables (sampleGaps, partitionGaps, h3Gaps, missingCaptures and the rest),
// keyed by the profile that took the capture and each listed in
// docs/backlog.md.
//
// What each test checks:
//
//   - TestProfilesMatchCaptures: mimicry's request to the capture server has
//     no differences from the browser's capture (TLS, HTTP/2 preface and
//     first request, headers).
//   - TestHPACKReproducesBrowserBlocks: re-encoding the browser's traced
//     requests with the profile's http2.hpack reproduces every header block
//     byte for byte, in every sample of every scenario and on every
//     connection of every replayed real page. http2.hpack cannot be
//     captured; this is what keeps it honest. A failure names the first
//     instruction that differs.
//   - TestConnectionMatchesBrowserTrace: on one connection, mimicry's first
//     header block and largest upload DATA frame match every sample of the
//     browser's trace, and every WINDOW_UPDATE credit increment it sends
//     lies within the range the browser's own increments span (± one frame).
//     Ranges, not single values, because credit timing varies between a
//     browser's own connections.
//   - TestCookiesMatchBrowserTrace: cookies set by the server come back from
//     mimicry, through its cookie jar, in the browser's order and HPACK
//     representation.
//   - TestKindsMatchBrowserTraces: every request of the kinds page (over
//     HTTP/2, HTTP/1.1 and plain HTTP, the last with the page's navigation
//     too) and its CORS preflights, made through mimicry as its kind from
//     its page: header lines (names, order, values), HTTP/2 weight, which
//     requests share a connection, and cookies as the browser sent them.
//     Where the browser's own samples differ, any form it sent passes and
//     the variance is logged. The only other tolerances are pinned tables:
//     raceCookieLabels (a label whose browser sample lacks a cookie mimicry
//     already holds, one direction only) and partitionGaps (Firefox's known
//     connection differences, label by label); any other difference fails,
//     and so does a listed gap that no longer appears.
//   - Request plans (planEvery, checkPlans in plan_test.go): the replays of
//     the kinds, headers-order, cors, cors-edge, redirects2, nav-redirect,
//     cookies, cookies-edge and h3 pages plan every request with
//     Transport.Plan just before sending it (redirect hops included). Each
//     plan's lines for the protocol the server saw are what it received
//     (HTTP/1.1 verbatim, HTTP/2 and HTTP/3 fields decoded), its preflight's
//     too; a protocol decided before sending is the one used; Plan fails
//     exactly when RoundTrip fails before sending (a *CORSError aside); Plan
//     leaves the jar unchanged; and every request the server received was
//     planned. Plan shares RoundTrip's code down to the connections' head
//     builders (decision 0034): a change on the send path alone fails this,
//     one on the shared path cannot drift.
//   - TestCookiesScenarioMatchesBrowser: every probe of the cookies page,
//     made through mimicry from its page, has the browser's full header
//     lines and HTTP/2 weight, and so its cookies in its order.
//   - TestCookiesEdgeMatchesBrowser, TestCookiesEdge2MatchesBrowser: every
//     step of the cookies-edge and cookies-edge2 pages, walked through
//     mimicry as the browser walked them (navigations and same-origin
//     fetches, http:// steps over plain HTTP where the browser used it,
//     cookies-edge2's on its plain-only second port), sends each sample's
//     Cookie header exactly: prefixes, nameless cookies, order, SameSite
//     across schemes, loopback, the per-site limit.
//   - TestHeadersOrderMatchesBrowser: every request of the headers-order
//     page, made through mimicry as the page's fetch() or XHR, is the
//     browser's: where each header the page set goes among the browser's own
//     (its header table, or the lists' slot), one line for a name set twice,
//     the page's spelling over HTTP/1.1 for XHR and lowercase for fetch(),
//     and the same preflights.
//   - TestCORSMatchesBrowser: the cors page's fetches through mimicry send
//     the browser's preflights and requests, connection by connection and in
//     order: when a preflight is sent or cached (Max-Age, the 5 s default,
//     replace or merge), no request after a refused preflight (a
//     *CORSError), a redirect's second preflight.
//   - TestCORSEdgeMatchesBrowser: the cors-edge page's fetches through
//     mimicry follow redirects as the browser did, connection by connection
//     and in order (method, path, body length, Origin): no next request
//     after a cross-site redirect without Access-Control-Allow-Origin (a
//     redirect's *CORSError), PUT/DELETE keeping method and body on 301/302,
//     GET on 303 and for POST (Safari keeps its Origin:
//     redirects.method_change_origin), 20 redirects at most.
//   - TestRedirects2MatchesBrowser: the redirects2 page through mimicry,
//     step by step (markup images, new Image(), XHR, crossorigin img/script,
//     fetch() with PUT/POST/HEAD/PATCH and credentials, form POSTs, the
//     25-redirect navigation), sends every request the browser did,
//     connection by connection, with its header lines and weight: CORS check
//     on every CORS-mode redirect, 307/308 keeping bodies, Firefox's and
//     Safari's preflight of a preflighted PATCH's GET hop
//     (redirects.hop_preflight), the navigation limit
//     (redirects.navigation_limit: Chrome 19). Listed gaps: navigations to
//     another site compared apart from their connection (no top-level-site
//     partition), Chrome's error-page reload, layout-driven image
//     priorities, Firefox's www.redir-c.test connections.
//   - TestNavRedirectsMatchBrowser: the four typed navigations whose start
//     URL redirects (within the origin, apex to www, to another site, http
//     to https) through mimicry, request for request; and the navigations of
//     nav-redirect-page (by script and form, redirected to another origin:
//     same-origin and same-site URLs to another site, www to static and apex
//     to www, another site back to the page's, a form GET and a form POST
//     answered 303), nav-redirect-form (form POSTs by script answered 301 to
//     308 between every pair of relations; Origin: null on a 307/308 by
//     redirects.navigation_origin) and nav-redirect-click (the same forms
//     and links clicked: Sec-Fetch-User on every hop), made from the page as
//     redirects2's are, a click's with Sec-Fetch-User and a script's
//     without. Safari 26's clicks were never captured (missingCaptures).
//   - TestResumptionMatchesBrowser: on the resume page, mimicry opens and
//     resumes as many connections per host as the browser did, and every
//     resumed ClientHello has no differences from the browser's.
//   - TestConnectionsMatchBrowser: the connections page through mimicry
//     opens the browser's connections (hosts, protocols, requests per
//     connection, Chrome's preconnect pinned as the one known difference),
//     coalesces the same hosts onto the same HTTP/2 connections (Firefox's
//     declined connections included), and spreads the 10 parallel HTTP/1.1
//     requests over 6 connections as the browser does.
//   - TestProxyMatchesBrowser: through the capture proxy, mimicry's CONNECT
//     lines, SOCKS5 bytes, credential flow (challenge first or every time,
//     retry on the same connection, unasked when the browser does) and
//     forwarded plain-HTTP requests (line for line) are the browser's, and
//     the tunnelled ClientHello is the profile's. A missing capture fails
//     unless missingCaptures lists it (Safari 26's three proxy-credential
//     captures, proxy-websocket-auth included, never taken); a listed one
//     that exists fails too.
//   - TestUnmeasuredProxyIsAnError: any proxy for a profile without a proxy
//     section, credentials without connect_auth, and a WebSocket through a
//     proxy without proxy.websocket, with credentials where
//     proxy.websocket.credentials is unset, or ws:// through SOCKS5, are
//     errors.
//   - TestHTTP1MatchesBrowserTrace: over HTTP/1.1, mimicry's two navigations
//     are the browser's header lines exactly, casing, order and values, in
//     every sample of the http1 scenario (authority normalised): / as a
//     typed URL, then /nav sent through mimicry as a navigation from the
//     page, with the page's cookies from the profile's own jar. Only
//     Sec-Fetch-User is suppressed (the page navigates by script, without
//     user activation).
//   - TestWebSocketsMatchBrowser: every socket of the websocket and
//     websocket-h2 pages, and of the proxy-websocket, proxy-websocket-auth
//     and proxy-websocket-socks5 pages through the capture proxy, opened
//     through mimicry from its page as the browser walked them, against
//     every sample: transport (an HTTP/1.1 connection of its own with the
//     browser's TLS hello, or an extended CONNECT stream on the connection
//     that carried the page's request to the host), what reached the proxy
//     for it (the CONNECT lines, status and credentials or SOCKS5 bytes of
//     its tunnel, the page's own for a stream, or nothing for a handshake
//     sent whole), handshake (header lines verbatim; HTTP/2 fields,
//     pseudo-header order, HEADERS flags, priority, stream WINDOW_UPDATE),
//     every client frame (FIN, RSV, opcode, mask, length form, payload,
//     compressed bytes included: varied text of every size, empty and
//     incompressible messages, client_no_context_takeover,
//     client_max_window_bits 8 to 15), the pong, HTTP/2 DATA packing (frames
//     that fit one DATA exactly; larger ones within the cap, header alone
//     where the browser's is), close frames, and who ends the transport, how
//     and how soon after the last frame. A ws:// socket from the https://
//     page is refused before anything is sent; a socket the browser fails
//     after its handshake (Safari's 8-bit window) fails to open, its
//     transport ended as the browser's; a step the page skips in a browser
//     it hangs (Firefox's 8-bit window) must fail to open. Listed variance:
//     Chrome fragments its 70 KiB message in some samples (mimicry never
//     does; a Chrome sample's frames also pass with its split messages
//     joined, so one sample suffices); mimicry's cross-site stream shares
//     the cross-site host's own connection (no top-level-site partition,
//     logged).
//   - TestDeflateReproducesBrowserMessages: replays every captured message
//     through the profile's compressor (ws.Compressor) and names the first
//     differing byte.
//   - TestHTTP3MatchesBrowser: the h3-discovery and h3-page pages walked
//     through mimicry from their pages as the browser walked them, pauses
//     and the 35 s idle included (about a minute; the eight profile ×
//     scenario subtests run in parallel), against every sample (Chrome's and
//     Firefox's -h3-local-roots captures, Safari's unforced): each request's
//     transport (HTTP/2 or HTTP/3, which connection of its host, 0-RTT),
//     whether a host's first QUIC connection started with its TCP one
//     (Firefox's switch on Alt-Svc), the HTTP/2 connections the browser sent
//     GOAWAY on, and per QUIC connection its first flight (what it sent
//     before its first ACK or its first probe, the first datagram repeating
//     CRYPTO or STREAM data or a lone PING, so a loaded machine's late
//     server leaves probes out: datagram sizes, padding, packets, header
//     bytes, token), ClientHello (diff.Compare), transport parameters, 0-RTT
//     packets and NEW_CONNECTION_IDs (retransmissions counted once), credit
//     frames and close, its unidirectional streams, SETTINGS, control
//     frames, every QPACK instruction, and per request its header list,
//     field lines, frames, PRIORITY_UPDATE and end. Features the samples
//     agree on must match; ones they differ on are logged; numbers lie
//     within the samples' range. What a browser draws per connection (packet
//     numbers, DCID length, the CRYPTO cuts with their PINGs and padding, a
//     cut's unusable last byte included, transport parameter order, GREASE
//     and ECH lengths, initial_rtt, ACK delays (net of the time the test
//     process was stalled, which stalls measures), the spin bit, Safari's
//     request after HANDSHAKE_DONE) or does by timing (whether its reply to
//     the server's Initial carries a padded 1-RTT packet: each datagram with
//     an Initial packet is held to where the profile pads it instead) is
//     never compared with the samples' few draws: quicRuleViolations
//     (http3_rules_test.go) holds mimicry's connections to the rule the
//     profile states, the rules subtest holds every connection of the
//     browser's h3 captures to the same rule (a rule the browser contradicts
//     fails), and compareDraws fails a draw repeated on every connection of
//     a page. Known differences are pinned in h3Gaps (a pinned gap that
//     stops appearing fails).
//   - TestHTTP3EdgeMatchesBrowser: the h3-blackhole, h3-broken, h3-vn, h3-v2
//     (where python3 has aioquic), h3-early and h3-too-early pages through
//     mimicry on the browser's schedule (each step at its time, whatever the
//     earlier ones take; the 24 profile × scenario subtests run in parallel,
//     about 75 s), against every sample: each request's connections in order
//     and its delay, and on a QUIC connection that sent 0-RTT each of its
//     streams (in 0-RTT, held for the handshake with the client's Finished
//     or after it, or sent again after a 425 Too Early, in which packet
//     type); per QUIC attempt whether it resumed, its versions, what the
//     server answered, how it ended, its datagrams and give-up time where
//     the server never answered, when it started after its trigger; per TCP
//     connection its trigger, the race against the QUIC attempt the same
//     request started (one signed gap: which of two connections started side
//     by side arrives first is scheduling) and the GOAWAY sent; the hello
//     and transport parameters of a connection retried after Version
//     Negotiation (Firefox's own hello). A connection that moves to a new
//     path (Chrome) sends the browser's number of probes first and moves
//     quic.loss.path_degrading.after_ptos of its own PTOs (measured from
//     those probes, no shorter than RFC 9002 allows on the trace's RTT
//     samples) after its last request, within half a PTO; every browser
//     sample is held to the same rule, so it holds under -race and load.
//     Times lie within the samples' range widened by edgeSlack, one of
//     mimicry's from a request on above it by no more than the test process
//     was stalled within it (stalls; a stall only lengthens a time); known
//     differences are pinned in h3EdgeGaps, a step mimicry refuses
//     (ErrNotMeasured: Safari's OPTIONS fetch) included, and in
//     h3EdgeSchedulingGaps those the test process's scheduling decides (not
//     required to appear).
//   - TestHTTP3DownloadMatchesBrowser: the h3-download page through mimicry:
//     during the 50 MiB response the client never starts a key update and
//     follows every one of the server's, acknowledges every quic.ack.every
//     packets (Chrome: exactly 10 in at least 95% of its ACKs) or at least
//     that many where the profile batches (98%), sends no ACK_FREQUENCY or
//     IMMEDIATE_ACK, with every browser sample held to the same rules; each
//     of its MAX_STREAM_DATA increases, and of its MAX_DATA ones where the
//     profile raises it by a share of the connection window (Chrome,
//     Firefox), lies within the range of the browser's own (± creditSlack,
//     one packet: a client counts consumption in the steps its packets
//     arrived in, so where the share falls inside the crossing packet is
//     chance). Where the connection limit follows what arrived
//     (connection_stream_window, Safari) the increases are timing, so the
//     rule is checked on mimicry and every browser sample alike, against
//     what the client's ACKs acknowledged (server data packets less the
//     connection's mean overhead, the recorder not opening their payloads;
//     the estimate's error is bounded by the bytes those packets carried
//     beyond the least overhead, 4.6–5.8 KB, plus a packet): no MAX_DATA
//     runs ahead of what the next ACK acknowledged by more than the stream
//     window (grown where the client's MAX_STREAM_DATA ran further, Safari
//     26's first sample), and the median raise is at least what was
//     acknowledged by then plus the window (Safari 27 sends a quarter to a
//     third of its limits from older counts). The requests after the
//     download go on its connection. The ACK shares, how far a batching
//     client's ACKs advance on average (machine timing, not checked) and the
//     credit ranges are logged.
//   - TestQPACKReproducesBrowserSections: re-encoding every browser's HTTP/3
//     field lists with the profile's http3.qpack reproduces every field
//     section and encoder stream instruction byte for byte, on every
//     connection of every h3 capture.
//   - TestHTTP3Pins: Config.Pins holds QUIC handshakes as it holds TCP ones:
//     an origin whose pins name only the TCP server's key never moves to
//     HTTP/3; one whose pins name both moves.
//   - TestQUICInterop: every profile against quic-go's HTTP/3 server through
//     a seeded relay (harness/internal/udprelay), one table of rows run in
//     parallel (about 20 s): handshake, 10 MB download, 2 MB upload,
//     concurrent streams, Retry, resumption and 0-RTT accepted, rejected and
//     answered 425 Too Early (sent again on the same connection: where the
//     profile resumes), ChaCha20 (a child process with AES off), server key
//     update, the anti-amplification limit, loss, corruption and blackholes
//     in the handshake and in transfers, a small MTU, port and address
//     rebinding, GOAWAY, idle and server closes, stream resets, tiny flow
//     control windows. Every response is checked byte for byte and every row
//     against its time limit. Rows that impair the handshake hold a profile
//     whose browser drops its QUIC attempt when TCP connects (Safari) to
//     that race instead: an attempt that needs a probe loses, every request
//     goes over TCP within the race, and the origin is given up after
//     broken_after attempts. It is the one table for QUIC failures no
//     capture reaches; a fix there adds a row that fails without it.
//   - TestCapturesFromNamesCapturedScenarios: every built-in profile's
//     captures_from names known scenarios other than the default page, each
//     held by a profile with captures of it; TestInheritedProfile proves
//     inheritance itself with a synthetic profile (testprofiles.Inheriting).
//
// Behaviour that depends on time (Chrome's 5 s credit return, its idle PING)
// is not tested here: it is tested in internal/h2 with a fake clock against
// a raw HTTP/2 peer; the captures show it, the unit tests pin it.
//
// Imports (tests only; the package has no other code): mimicry's public
// packages and the internal packages whose wire formats the tests decode or
// re-encode (h3, hpack, qpack, quic/quicwire, ws, testutil), the harness's
// internal packages, quic-go and golang.org/x/net.
package e2e
