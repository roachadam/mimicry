package main

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// sampleGaps pin connections that differ from the rest of their browser's
// for timing or state reasons. Keys are conn.at name plus profile field,
// or an h3-discovery sample, host and "switch" for its switch rule or
// "raceAfterGoAway" for its dial after the server's close, so a
// pin applies to every inheriting profile; values say why.
var sampleGaps = map[string]string{ //nolint:gosec // pinned capture gaps, not credentials
	"chrome-155-macos.h3-discovery.local-roots: sample 2, QUIC connection 2: ackInitial":                         "Chrome acknowledged the server's Initial in 1 of its 12 completed full handshakes: quiche drops its Initial keys when it sends its first Handshake packet, and acknowledges an Initial only if one arrives before it has Handshake keys to send with (timing)",
	"chrome-155-macos.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":                             "Chrome's reconnection to www.h3-b.test after the 35 s idle carried no NEW_TOKEN token in 2 of 3 samples, though resumed with 0-RTT; its first connection there had received one (Chrome's token cache, not modelled)",
	"chrome-155-macos.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":                             "as sample 2, connection 5",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 1: ackInitial":                         "as chrome-155's sample 2, connection 2: 1 of 21 completed full handshakes acknowledged the server's Initial (timing)",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 1: ackBeforeFinished":                  "the same connection's ACK of the server's Initial left before its Finished (ackInitial)",
	"chrome-156-macos.h3-discovery.local-roots: sample 1, QUIC connection 4: priorityUpdate /h3/after-idle.so":   "on the second Mac the server read this PRIORITY_UPDATE 1 ms after its request's HEADERS (another datagram), where every other one, and all of a 2-sample check of Chrome 156 on another Mac, came no later: Chrome's packet flushing on the slower Mac (timing)",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 1: priorityUpdate /h3/second.so":       "as sample 1, connection 4",
	"chrome-156-macos.h3-discovery.local-roots: sample 3, QUIC connection 4: priorityUpdate /h3/after-idle.so":   "as sample 1, connection 4",
	"chrome-156-macos.h3-discovery.local-roots: sample 1, QUIC connection 3: tokens":                             "Chrome 156 presented the NEW_TOKEN token on 3 of 9 reconnections whose host had given it one (Chrome 155: 7 of 9), on this capture from the second Mac as on a second check on another Mac (3 of 6): which ones varies between samples (Chrome's token cache, not modelled)",
	"chrome-156-macos.h3-discovery.local-roots: sample 1, QUIC connection 5: tokens":                             "as sample 1, connection 3",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":                             "as sample 1, connection 3",
	"chrome-156-macos.h3-discovery.local-roots: sample 2, QUIC connection 4: tokens":                             "as sample 1, connection 3",
	"chrome-156-macos.h3-discovery.local-roots: sample 3, QUIC connection 4: tokens":                             "as sample 1, connection 3",
	"chrome-156-macos.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":                             "as sample 1, connection 3",
	"chrome-157-macos.h3-discovery.local-roots: sample 2, QUIC connection 2: ackInitial":                         "as chrome-156-macos's sample 2, connection 1: Chrome 157 acknowledged the server's Initial in 2 of its 21 completed full handshakes (this one and sample 3, connection 2): quiche acknowledges an Initial only if one arrives before it drops its Initial keys (timing)",
	"chrome-157-macos.h3-discovery.local-roots: sample 3, QUIC connection 2: ackInitial":                         "as sample 2, connection 2",
	"chrome-157-macos.h3-discovery.local-roots: sample 3, QUIC connection 1: priorityUpdate /h3/second.so":       "as chrome-156-macos's sample 1, connection 4: on the second Mac the server read this PRIORITY_UPDATE 1 ms after its request's HEADERS (another datagram), where every other one came no later (timing)",
	"chrome-157-macos.h3-page.local-roots: sample 3, QUIC connection 1: priorityUpdate /h3/image-offscreen.so":   "as h3-discovery.local-roots: sample 3, QUIC connection 1",
	"chrome-156-windows.h3-discovery.local-roots: sample 1, QUIC connection 3: tokens":                           "on Windows as on macOS, Chrome 156 presented the NEW_TOKEN token on 3 of 9 reconnections whose host had given it one (h3-early: 15 of 27, h3-too-early: 12 of 27), which ones varying between samples (Chrome's token cache, not modelled)",
	"chrome-156-windows.h3-discovery.local-roots: sample 1, QUIC connection 5: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 3, QUIC connection 3: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 3, QUIC connection 4: tokens":                           "as sample 1, connection 3",
	"chrome-156-windows.h3-discovery.local-roots: sample 1, QUIC connection 4: priorityUpdate /h3/after-idle.so": "as chrome-156-macos's sample 1, connection 4: on Windows too the server read one PRIORITY_UPDATE 1 ms after its request's HEADERS (another datagram), where every other in these captures came no later (timing)",
	"firefox-158-macos.h3-broken.local-roots: sample 3, QUIC connection 4: loss.cachedRtt":                       "Firefox's resumed attempt after the server's close first probed after 306 ms, the initial PTO, where the other two probed after 3 ms from their cached RTT; the same attempt also gave up later (loss.resumed): another path through necko/neqo, cause not captured",
	"firefox-158-macos.h3-broken.local-roots: sample 3, QUIC connection 4: loss.resumed":                         "Firefox closed this resumed attempt 9.2 s in, not 5.0 s; a TCP connection opened at 5.0 s as in the other samples but took a GOAWAY at once, and the held requests waited for the close (see loss.cachedRtt)",
	"firefox-158-macos.h3-broken.local-roots: sample 2: keepAlive":                                               "Firefox's keep-alive PING came 15.25 s after its idle timer restarted, 254 ms late (15.00–15.01 s in the other samples): a timer wake-up, not captured",
	"firefox-158-macos.h3-early.local-roots: sample 2: earlyData.heldWithFinished pair-post.so":                  "Firefox's held requests left in the datagram with its Finished in 32 of 36 cases; this one 0.17 ms after it, made 2.8 ms before: when necko hands a held request to neqo against when neqo writes the Finished (thread scheduling, not captured)",
	"firefox-158-macos.h3-early.local-roots: sample 3: earlyData.heldWithFinished form.so":                       "as sample 2's pair-post.so: 1.7 ms after the Finished (a form navigation, made 36 ms before)",
	"firefox-158-macos.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished post.so":                   "as h3-early sample 2's pair-post.so: 18.6 ms after the Finished, made 35 ms before",
	"firefox-158-macos.h3-too-early.local-roots: sample 2: earlyData.heldWithFinished form.so":                   "as h3-early sample 2's pair-post.so: 1.1 ms after the Finished (a form navigation, made 54 ms before)",
	"firefox-158-windows.h3-broken.local-roots: sample 1, QUIC connection 19: loss.cachedRtt":                    "in this sample Firefox on Windows opened 14 QUIC connections beside the page's and closed each 1–5 ms in (H3_NO_ERROR), and its resumed attempt after the server's close probed after 4.6 ms from a cached RTT, as on macOS; the other two (and 5 of 6 further samples, 2026-11-01) first probed 1.7–1.9 s after their first flight: necko/neqo state not captured",
	"firefox-158-windows.h3-broken.local-roots: sample 1, QUIC connection 19: loss.resumed":                      "the same attempt (see loss.cachedRtt) ended silently 4.5 s in, with no TCP connection to the host after the close; the others closed 9.8–10.2 s in (1 of 6 further samples ended silently the same way, one closed at 5.0 s)",
	"firefox-158-windows.h3-broken.local-roots: sample 1: hold":                                                  "the sample of loss.cachedRtt: no TCP connection to the closed host followed the server's close, so nothing held closed1.cs; the other two held it as on macOS",
	"firefox-158-windows.h3-broken.local-roots: sample 1: keepAlive":                                             "Firefox's keep-alive PING came 15.10 s after its idle timer restarted, 97 ms late (15.01 s in the other two; in 6 further samples 15.00–15.39 s, 3 beyond 50 ms): Firefox's timers on Windows wake late, not modelled",
	"firefox-158-windows.h3-broken.local-roots: sample 3, QUIC connection 4: loss.resumed":                       "Firefox closed this resumed attempt 10.2 s in, sample 2's at 9.8 s (6 further samples: 9.8, 9.8, 10.2, 10.3, 5.0 s and one silent): two give-up moments, cause not captured",
	"firefox-158-windows.h3-download.local-roots: sample 3: credit":                                              "one MAX_STREAM_DATA raised the limit by 3,221,633 bytes, 2.4% above 3 MiB, in the sample where Firefox opened and at once closed a QUIC connection beside the page's for each request after the download (7); 1 of 3 further samples raised one by 3.3%: when necko reads, not captured",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 2: newConnectionIDs":                "a side connection of the burst (see h3-discovery.local-roots: sample 1, QUIC connection 3: tokens): Firefox sends 3 NEW_CONNECTION_IDs in its first 1-RTT datagram and the 4th 0.5–1 ms later, in every connection, and this one closed before the 4th",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 3: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 3: tokens":                          "in this sample Firefox 159 opened: a burst of QUIC connections beside the page's, none carrying a request, each closed by Firefox 2–5 ms after its handshake (H3_NO_ERROR); the first of them resumed with 0-RTT and the token, and every later connection to the host in the sample, these and the page's own, started fresh: no session ticket and no token, though an earlier one had received a NEW_TOKEN before it started. Token use follows resumption in every connection (158 and 159), and the other samples reuse both as firefox-158-windows does (side connections' trigger not captured)",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 4: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 4: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 5: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 5: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 6: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 6: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 7: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 7: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 8: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 8: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 10: tokens":                         "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 11: newConnectionIDs":               "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 12: tokens":                         "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 2: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 3: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 3: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 4: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 4: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 5: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 6: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 6: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 7: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 7: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 8: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 8: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 9: newConnectionIDs":                "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 9: tokens":                          "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 11: tokens":                         "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 12: newConnectionIDs":               "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 13: tokens":                         "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 3: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 4: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 4: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 5: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 5: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 6: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 6: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 7: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 7: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 8: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 8: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 9: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 9: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 10: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 10: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 11: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 11: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 12: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 12: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 13: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 13: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 14: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 14: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 15: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 15: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 16: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 16: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 17: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 17: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 18: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 18: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 19: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 19: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 20: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 21: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 21: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 22: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 22: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 23: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 3: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 4: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 4: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 5: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 5: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 6: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 6: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 7: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 7: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 8: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 8: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 9: newConnectionIDs":                     "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 9: tokens":                               "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 10: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 10: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 11: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 11: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 12: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 12: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 13: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 14: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 15: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 15: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 16: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 16: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 17: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 17: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 18: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 18: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 19: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 19: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 20: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 21: newConnectionIDs":                    "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 21: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 22: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 23: tokens":                              "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 4: http3":                           "a side connection of the burst (see h3-discovery.local-roots: sample 1, QUIC connection 3: tokens), closed by Firefox before its control stream reached the server: its HTTP/3 layer is not measured",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 5: http3":                           "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, QUIC connection 8: http3":                           "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 4: http3":                           "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, QUIC connection 6: http3":                           "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 6: http3":                                "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 7: http3":                                "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-page.local-roots: sample 2, QUIC connection 8: http3":                                "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 8: http3":                                "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 12: http3":                               "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 16: http3":                               "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-page.local-roots: sample 3, QUIC connection 17: http3":                               "as h3-discovery.local-roots: sample 1, QUIC connection 4",
	"firefox-159-windows.h3-discovery.local-roots: sample 1, www.h3-a.test: raceAfterGoAway":                     "as firefox-159-macos's: in the burst's samples (h3-discovery.local-roots: sample 1, QUIC connection 3: tokens) Firefox answered the server's close with one QUIC connection and no TCP race; sample 2, without the burst, raced TCP 2 ms before QUIC as all three of firefox-158-windows's did",
	"firefox-159-windows.h3-discovery.local-roots: sample 3, www.h3-a.test: raceAfterGoAway":                     "as sample 1",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, www.h3-a.test: raceAfterGoAway":                       "in the burst's samples (see h3-discovery.local-roots: sample 1, QUIC connection 3: tokens) Firefox answered the server's close with one QUIC connection and no TCP race; sample 3, without the burst, raced TCP 6 ms before QUIC as all three of firefox-158-macos's did (side connections' trigger not captured)",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, www.h3-a.test: raceAfterGoAway":                       "as sample 1",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 6: http3":                             "a side connection of the burst (see h3-discovery.local-roots: sample 1, QUIC connection 3: tokens), closed by Firefox before its control stream reached the server: its HTTP/3 layer is not measured",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 8: http3":                             "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 9: http3":                             "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 3: http3":                             "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 6: http3":                             "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 7: http3":                             "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 8: http3":                             "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 9: http3":                             "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 6: http3":                                  "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 7: http3":                                  "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 11: http3":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 12: http3":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 15: http3":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 17: http3":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 18: http3":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 21: http3":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 6",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 2: newConnectionIDs":                  "a side connection of the burst (see h3-discovery.local-roots: sample 1, QUIC connection 3: tokens): Firefox sends 3 NEW_CONNECTION_IDs in its first 1-RTT datagram and the 4th 0.5–1 ms later, in every connection, and this one closed before the 4th",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 3: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 3: tokens":                            "in this sample Firefox 159 opened a burst of QUIC connections beside the page's, none carrying a request, each closed by Firefox 2–5 ms after its handshake (H3_NO_ERROR); the first of them resumed with 0-RTT and the token, and every later connection to the host in the sample, these and the page's own, started fresh: no session ticket and no token, though an earlier one had received a NEW_TOKEN before it started. Token use follows resumption in every connection (158 and 159), and the other samples reuse both as firefox-158-macos does (side connections' trigger not captured)",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 4: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 4: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 5: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 5: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 6: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 6: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 7: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 7: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 8: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 8: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 9: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 9: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 11: tokens":                           "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 12: newConnectionIDs":                 "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 1, QUIC connection 13: tokens":                           "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 2: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 3: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 4: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 4: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 5: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 6: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 6: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 7: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 7: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 8: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 8: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 9: newConnectionIDs":                  "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 9: tokens":                            "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 11: tokens":                           "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 12: newConnectionIDs":                 "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-discovery.local-roots: sample 2, QUIC connection 13: tokens":                           "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 3: newConnectionIDs":                       "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 4: newConnectionIDs":                       "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 4: tokens":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 5: newConnectionIDs":                       "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 5: tokens":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 6: newConnectionIDs":                       "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 6: tokens":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 7: newConnectionIDs":                       "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 7: tokens":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 8: newConnectionIDs":                       "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 8: tokens":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 9: newConnectionIDs":                       "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 9: tokens":                                 "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 10: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 10: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 11: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 11: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 12: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 12: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 13: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 13: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 14: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 14: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 15: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 15: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 16: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 16: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 17: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 17: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 18: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 18: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 19: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 19: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 20: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 21: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 21: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 22: newConnectionIDs":                      "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 22: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-macos.h3-page.local-roots: sample 1, QUIC connection 23: tokens":                                "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"safari-26-macos.h3-download: sample 1: credit":                                                              "Safari returns stream credit as its application reads (limit = bytes read + 2 MiB), at irregular points: increments 0.81–1.92 MB, median 1.60 MB, on this slower Mac (1 s transfer)",
	"safari-26-macos.h3-download: sample 2: credit":                                                              "Safari returns stream credit as its application reads, at irregular points: increments 0.95–1.32 MB, median 1.05 MB (window / 2)",
	"safari-26-macos.h3-download: sample 3: credit":                                                              "as sample 2: increments 0.90–1.36 MB, median 1.05 MB",
	"safari-27-macos.h3-download: sample 1: credit":                                                              "Safari returns stream credit as its application reads, at irregular points: increments 1.4 KB–2.10 MB, median 0.96 MB (window / 2.2)",
	"safari-27-macos.h3-download: sample 2: credit":                                                              "as sample 1: increments 62 B–2.10 MB, median 0.91 MB",
	"safari-27-macos.h3-download: sample 3: credit":                                                              "as sample 1: increments 63 B–2.10 MB, median 0.98 MB",
	"safari-27-macos.h3-vn: sample 1: tcpDelayMs vn-3.so":                                                        "after an immediate Version Negotiation Safari 27 dialled TCP 202 ms after its QUIC attempt, beyond the 100–129 ms of its races elsewhere (Safari 26: 92–106 ms, within its range); three samples cannot say why",
	"safari-27-macos.h3-vn: sample 2: tcpDelayMs vn-3.so":                                                        "as sample 1: 139 ms",
	"safari-27-ios.h3-discovery: sample 3, www.h3-b.test: switch":                                                "opened by hand, the page's idle spell lasted 69 s instead of 35 s (samples 1 and 2: 49 s and 35 s; timers held back on the phone, timing), so www.h3-b.test's HTTP/2 connection had been idle 78 s and was gone [INFERENCE: closed by the phone, unrecorded], and xafter-idle.cs went on a new connection, over QUIC: the new-connection rule, without the GOAWAY this check looks for; at 52 s idle (sample 1) the connection was still used",
	"chrome-157-ios.h3-broken: sample 2: keepAlive":                                                              "the server stopped answering before the 7.5 s PING, so the first PING this check counts came 4069 ms after the last datagram received; the PINGs' cadence is about 2000 ms in all three samples (5.5, 7.6, 9.5, 11.5, 13.5 s), and samples 1 and 3 give 2014 and 2016 ms",
	"firefox-159-ios.h3-page: sample 1, QUIC connection 1: datagram 3: Initial in 1038 bytes":                    "the iPhone's Initial probe, as in a blackholed attempt (iosProbes): the server's Initial left 1.8 ms after the client's pair (the server's clock) but reached Firefox on Wi-Fi too late, so 35 ms in it resent its ClientHello as a 1038-byte unpadded datagram and a 1200-byte one (packet numbers 2 and 3), then acknowledged; samples 2 and 3 and every other Initial are 1200 bytes",
	"firefox-159-ios.h3-broken: sample 3: before its attempt closed2.cs":                                         "closed2.cs reached the server on TCP 1 ms before the QUIC attempt it started, so it measures no wait for QUIC (h3gen would read background mode from it); the other 11 first requests after a GOAWAY or the server's close waited 52-146 ms for QUIC, as the copied race rule says (Chrome on the iPhone: the same, with TCP dialled first once)",
	// Linux, captured in WSL2 on the Windows PC (2026-11-05).
	"chrome-157-linux.h3-discovery.local-roots: sample 1, QUIC connection 3: tokens":                    "on Linux as on macOS and Windows, Chrome 157 presented its NEW_TOKEN token on only some reconnections whose host had given it one, which ones varying between samples (Chrome's token cache, not modelled)",
	"chrome-157-linux.h3-discovery.local-roots: sample 1, QUIC connection 5: tokens":                    "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"chrome-157-linux.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":                    "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"chrome-157-linux.h3-discovery.local-roots: sample 2, QUIC connection 4: tokens":                    "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"chrome-157-linux.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":                    "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"chrome-157-linux.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":                    "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"chrome-157-linux.h3-page.local-roots: sample 3, QUIC connection 3: ackBeforeFinished":              "the same connection's ACK of the server's Initial left before its Finished (ackInitial)",
	"chrome-157-linux.h3-page.local-roots: sample 2, QUIC connection 1: ackInitial":                     "as chrome-156-macos's sample 2, connection 1: Chrome acknowledged the server's Initial in 2 of its 12 completed full handshakes here (this one and sample 3, connection 3; 1 of 21 on macOS) and 1 of its 10 resumed ones (sample 3, connection 2): quiche acknowledges an Initial only if one arrives before it drops its Initial keys (timing)",
	"chrome-157-linux.h3-page.local-roots: sample 3, QUIC connection 2: ackInitial":                     "as h3-page.local-roots: sample 2, QUIC connection 1",
	"chrome-157-linux.h3-page.local-roots: sample 3, QUIC connection 3: ackInitial":                     "as h3-page.local-roots: sample 2, QUIC connection 1",
	"chrome-157-linux.h3-page.local-roots: sample 3, QUIC connection 2: resumedAckBeforeFinished":       "a resumed handshake whose ACK of the server's Initial (this connection's ackInitial) left before its Finished: the same timing, on a resumed connection",
	"chrome-157-linux.h3-page.local-roots: sample 3, QUIC connection 2: tokens":                         "as h3-discovery.local-roots: sample 1, QUIC connection 3",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 2: newConnectionIDs":         "as firefox-159-windows's h3-discovery sample 1, connection 2: a side connection of the burst (see this sample's tokens) closed after Firefox's first 3 NEW_CONNECTION_IDs, before the 4th it sends 0.5–1 ms later",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 3: newConnectionIDs":         "as h3-discovery.local-roots: sample 2, QUIC connection 2",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 4: newConnectionIDs":         "as h3-discovery.local-roots: sample 2, QUIC connection 2",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 5: newConnectionIDs":         "as h3-discovery.local-roots: sample 2, QUIC connection 2",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 6: newConnectionIDs":         "as h3-discovery.local-roots: sample 2, QUIC connection 2",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 7: newConnectionIDs":         "as h3-discovery.local-roots: sample 2, QUIC connection 2",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 10: newConnectionIDs":        "as h3-discovery.local-roots: sample 2, QUIC connection 2",
	"firefox-159-linux.h3-discovery.local-roots: sample 1, QUIC connection 4: resumedAckBeforeFinished": "Firefox's resumed handshake acknowledged the server's Initial before its Finished, the only resumed one in these captures to do so: timing, as Chrome's ackInitial",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":                   "as firefox-159-windows's h3-discovery sample 1, connection 3: in this sample Firefox opened a burst of QUIC connections beside the page's, none carrying a request, each closed within milliseconds of its handshake; later connections to the host started without the session ticket and token an earlier one had received (side connections' trigger not captured)",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 4: tokens":                   "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":                   "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 6: tokens":                   "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 7: tokens":                   "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 9: tokens":                   "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 11: tokens":                  "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 3: newConnectionIDs":              "as h3-discovery.local-roots: sample 2, QUIC connection 2",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 4: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 5: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 6: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 7: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 8: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 9: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 10: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 11: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 12: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 13: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 14: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 15: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 16: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 17: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 18: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 19: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 20: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 21: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 22: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 23: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 3: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 5: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 6: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 7: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 8: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 9: newConnectionIDs":              "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 10: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 11: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 12: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 13: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 14: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 15: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 16: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 17: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 18: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 19: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 20: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 21: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 22: newConnectionIDs":             "as h3-page.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 4: tokens":                        "as h3-discovery.local-roots: sample 2, QUIC connection 3: in samples 2 and 3 of h3-page Firefox opened 21 such connections beside the page's 2",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 5: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 6: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 7: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 8: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 9: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 10: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 11: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 12: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 13: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 14: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 15: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 16: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 17: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 18: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 19: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 21: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 22: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 23: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 4: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 6: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 7: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 8: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 9: tokens":                        "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 10: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 11: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 12: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 13: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 14: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 15: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 16: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 17: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 18: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 19: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 21: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 22: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 23: tokens":                       "as h3-page.local-roots: sample 2, QUIC connection 4",
	"chrome-157-linux.h3-page.local-roots: sample 3, QUIC connection 3: streamsWithFinished":            "the connection whose ACK of the server's Initial left before its Finished (ackInitial): its control stream followed in a later datagram (timing)",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 3: http3":                    "a side connection of the burst (see this sample's tokens) that Firefox closed before opening its control stream: no HTTP/3 to read",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 5: http3":                    "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 6: http3":                    "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, QUIC connection 7: http3":                    "as h3-discovery.local-roots: sample 2, QUIC connection 3",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 5: http3":                         "a side connection of the burst (see this sample's tokens) that Firefox closed before opening its control stream: no HTTP/3 to read",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 6: http3":                         "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 7: http3":                         "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 8: http3":                         "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 9: http3":                         "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 10: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 11: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 12: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 13: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 14: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 15: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 16: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 17: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 18: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 19: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 21: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 22: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 2, QUIC connection 23: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 6: http3":                         "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 7: http3":                         "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 8: http3":                         "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 9: http3":                         "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 10: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 11: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 12: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 13: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 15: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 16: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 17: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 18: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 19: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"firefox-159-linux.h3-page.local-roots: sample 3, QUIC connection 22: http3":                        "as h3-page.local-roots: sample 2, QUIC connection 5",
	"chrome-157-linux.h3-download.local-roots: sample 1: credit":                                        "one MAX_STREAM_DATA raised the limit by 3,210,697 bytes, 2.07% above 3 MiB; the others within 2%: when Chrome's reader drained the stream (firefox-158-windows shows the same, 2.4%), not captured",
	"chrome-157-linux.h3-streams-blocked.local-roots: sample 2: streamsBlocked.count":                   "17 STREAMS_BLOCKED where the bursts account for 15: Chrome sent the after-raise pair (limits 5 and 6) twice, the frames chrome-156-macos sends after some raises and not others (EVIDENCE.md, quic.streamsBlocked.afterRaise); the rule the bursts give is the other samples'",
	"firefox-159-linux.h3-discovery.local-roots: sample 2, www.h3-a.test: raceAfterGoAway":              "as firefox-159-macos's and firefox-159-windows's: in the burst sample (h3-discovery.local-roots: sample 2, QUIC connection 3: tokens) Firefox answered the server's close with one QUIC connection and no TCP race; samples 1 and 3, without the burst, raced TCP before QUIC",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.methods GET":                           "Firefox opened a side connection to the host 3.9 s in (QUIC connection 2: resumed with 0-RTT, no request, closed by Firefox) and on every later connection sent no GET or HEAD in 0-RTT, only OPTIONS; the six samples without it sent GET, HEAD and OPTIONS in 0-RTT, as firefox-159-macos and firefox-159-windows do (side connections' trigger not captured)",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.methods HEAD":                          "as h3-early.local-roots: sample 2",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.methods GET":                           "as h3-early.local-roots: sample 2 (its side connection 3.7 s in)",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.methods HEAD":                          "as h3-early.local-roots: sample 2 (its side connection 3.7 s in)",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.methods GET":                       "as h3-early.local-roots: sample 2 (its side connection 4.0 s in)",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.methods HEAD":                      "as h3-early.local-roots: sample 2 (its side connection 4.0 s in)",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so":               "in the sample with the side connection (see earlyData.methods GET), the held requests, GETs and HEADs among them, left in later datagrams than the Finished; in the six samples without it they left with it, as on macOS and Windows",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.heldWithFinished delete.so":            "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.heldWithFinished xhr-post.so":          "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.heldWithFinished pair-get.so":          "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.heldWithFinished get.so":               "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.heldWithFinished head.so":              "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 2: earlyData.heldWithFinished form.so":              "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.heldWithFinished post.so":              "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.heldWithFinished put.so":               "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.heldWithFinished delete.so":            "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.heldWithFinished xhr-post.so":          "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.heldWithFinished pair-get.so":          "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.heldWithFinished get.so":               "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.heldWithFinished head.so":              "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-early.local-roots: sample 3: earlyData.heldWithFinished form.so":              "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished put.so":           "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished delete.so":        "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished xhr-post.so":      "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished pair-get.so":      "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished get.so":           "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished head.so":          "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-too-early.local-roots: sample 1: earlyData.heldWithFinished form.so":          "as h3-early.local-roots: sample 2: earlyData.heldWithFinished put.so",
	"firefox-159-linux.h3-broken.local-roots: sample 5, QUIC connection 17: loss.resumed":               "as firefox-158-windows's sample 1: in the two samples with Firefox's burst of side connections (15 beside the page's; samples 5 and 6) the resumed attempt after the server's close ended silently, 4.5 and 3.1 s in; the four without the burst closed it 5.0 s in, as on macOS. A first 3-sample capture the same day: 2 burst samples silent, the other closed at 6.1 s",
	"firefox-159-linux.h3-broken.local-roots: sample 6, QUIC connection 17: loss.resumed":               "as sample 5, QUIC connection 17",
	"firefox-159-linux.h3-broken.local-roots: sample 5: hold":                                           "the burst's sample (see sample 5, QUIC connection 17: loss.resumed): closed1.cs was not held; the four samples without the burst held it, as firefox-159-macos does (firefox-158-windows's burst sample the same)",
	"firefox-159-linux.h3-broken.local-roots: sample 6: hold":                                           "as sample 5",
	"firefox-159-linux.h3-broken.local-roots: sample 2, QUIC connection 4: loss.resumed":                "Firefox closed its 0-RTT attempt 5.0 s in with the same Initial CONNECTION_CLOSE and 0-RTT packets as every other, padded to a 600-byte datagram, not 255: the only such pad in 10 closes on Linux, macOS and Windows, cause not captured",
	"firefox-159-linux.h3-download.local-roots: sample 2: credit":                                       "Firefox's first MAX_STREAM_DATA raised the 12 MiB initial window by 3,799,643 bytes, 21% above 3 MiB, where samples 1 and 3 raised it by 0.7% and 0.2% above: sent later, after more was read (when necko reads, as firefox-158-windows's sample 3), not captured",
	"firefox-159-linux.h3-download.local-roots: sample 3: credit":                                       "one MAX_STREAM_DATA raised the limit by 3,218,323 bytes, 2.3% above 3 MiB: as firefox-158-windows's sample 3 (2.4%)",
	"firefox-159-linux.h3-streams-blocked.local-roots: sample 1: streamsBlocked":                        "in the second burst streams 11 and 12 opened in one packet, behind one STREAMS_BLOCKED 13 and none for 12; in sample 3 they opened 4 ms apart, each in its own packet behind its own STREAMS_BLOCKED (12, then 13), the rule firefox-159-macos and firefox-159-windows show: the two openings packed together, timing",
	"firefox-159-linux.h3-streams-blocked.local-roots: sample 2: streamsBlocked":                        "as sample 1",
	// Edge 157 on macOS (2026-11-06).
	"edge-157-macos.h3-discovery.local-roots: sample 1, QUIC connection 3: secondInitialLength": "Edge sent this resumed connection's second Initial (with the NEW_TOKEN token) with its packet number in 1 byte, as Chrome does with a token; Edge put it in 2 bytes in 115 of 123 connections, token or not (38 of 46 with a token), so quic.packetNumbers is shared_second_long; what decides is not on the wire (not the scenario, timing, initial_rtt or hello size)",
	"edge-157-macos.h3-discovery.local-roots: sample 1, QUIC connection 2: ackInitial":          "as chrome-156-macos's sample 2, connection 1: Edge acknowledged the server's Initial in 4 of its 12 completed full handshakes (Chrome 157 on Linux 2 of 12): quiche acknowledges an Initial only if one arrives before it drops its Initial keys (timing)",
	"edge-157-macos.h3-page.local-roots: sample 1, QUIC connection 2: ackInitial":               "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"edge-157-macos.h3-page.local-roots: sample 2, QUIC connection 2: ackInitial":               "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"edge-157-macos.h3-page.local-roots: sample 3, QUIC connection 2: ackInitial":               "as h3-discovery.local-roots: sample 1, QUIC connection 2",
	"edge-157-macos.h3-discovery.local-roots: sample 1, QUIC connection 5: tokens":              "as chrome-156-macos's and chrome-156-windows's: Edge, like Chrome, presented its NEW_TOKEN token on only some reconnections whose host had given it one, which ones varying between samples (Chromium's token cache, not modelled)",
	"edge-157-macos.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":              "as sample 1, QUIC connection 5",
	"edge-157-macos.h3-discovery.local-roots: sample 2, QUIC connection 4: tokens":              "as sample 1, QUIC connection 5",
	"edge-157-macos.h3-discovery.local-roots: sample 2, QUIC connection 5: tokens":              "as sample 1, QUIC connection 5",
	"edge-157-macos.h3-discovery.local-roots: sample 3, QUIC connection 3: tokens":              "as sample 1, QUIC connection 5",
	// Edge 157 on Windows (2026-11-06, the PC's Edge policies lifted for the run).
	"edge-157-windows.h3-discovery.local-roots: sample 2, QUIC connection 5: secondInitialLength": "as edge-157-macos's: Edge on Windows sent this resumed connection's second Initial (with the NEW_TOKEN token) with its packet number in 1 byte; it used 2 bytes in 114 of 123 connections, token or not (all 9 one-byte ones with a token; macOS 115 and 8), so quic.packetNumbers is shared_second_long",
	"edge-157-windows.h3-discovery.local-roots: sample 1, QUIC connection 3: tokens":              "as edge-157-macos's: Edge on Windows, like Chrome, presented its NEW_TOKEN token on only some reconnections whose host had given it one, which ones varying between samples (Chromium's token cache, not modelled)",
	"edge-157-windows.h3-discovery.local-roots: sample 2, QUIC connection 3: tokens":              "as sample 1, QUIC connection 3",
	"edge-157-windows.h3-discovery.local-roots: sample 3, QUIC connection 3: tokens":              "as sample 1, QUIC connection 3",
	"edge-157-windows.h3-blackhole: sample 3, QUIC connection 1: probes":                          "Edge sent its first flight's two Initials 2.1 ms apart (1.9 ms in sample 1; the recorder on Windows steps about 0.5 ms), past the 2 ms that groups datagrams into one flight, so the second read as a probe; its real probes, 308, 920 and 2125 ms in, follow the others' (the gap stays 2 ms: Firefox's resumed probes from a cached RTT come about 3 ms apart)",
	"edge-157-windows.h3-download.local-roots: sample 1: credit":                                  "as chrome-157-linux's: one MAX_STREAM_DATA raised the limit by 3,211,264 bytes, 2.08% above 3 MiB, the others within 2%: when Edge's reader drained the stream, not captured",
	"edge-157-windows.h3-download.local-roots: sample 3: credit":                                  "as sample 1: 3,211,094 bytes, 2.08% above 3 MiB",
	// Brave 157 on macOS (2026-11-09, Brave 1.99.55).
	"brave-157-macos.h3-discovery.local-roots: sample 2, QUIC connection 3: secondInitialLength": "as edge-157-macos's: Brave sent this resumed connection's second Initial (with the NEW_TOKEN token) with its packet number in 1 byte; it put it in 2 bytes in 143 of 150 connections, token or not (all 7 one-byte ones with a token), so quic.packetNumbers is shared_second_long; what decides is not on the wire",
	"brave-157-macos.h3-discovery.local-roots: sample 2, QUIC connection 1: ackInitial":          "as chrome-156-macos's sample 2, connection 1: Brave acknowledged the server's Initial in 1 of the 21 full handshakes of its h3 captures: quiche acknowledges an Initial only if one arrives before it drops its Initial keys (timing)",
	"brave-157-macos.h3-discovery.local-roots: sample 2, QUIC connection 1: coalescedOrder":      "the late ACK of the server's Initial (ackInitial, pinned) went as Initial packet 5 at the head of the datagram that carried Handshake packet 3 and 1-RTT packet 4: numbered when built, after them, and put first as an Initial must be; the one connection that acknowledged an Initial",
	"brave-157-macos.h3-discovery.local-roots: sample 3, QUIC connection 3: tokens":              "as edge-157-macos's: Brave, like Chrome, presented its NEW_TOKEN token on only some reconnections whose host had given it one, which ones varying between samples (Chromium's token cache, not modelled)",
	"brave-157-macos.h3-discovery.local-roots: sample 3, QUIC connection 4: tokens":              "as sample 3, QUIC connection 3",
	"brave-157-macos.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":              "as sample 3, QUIC connection 3",
	// Brave 157 on Windows (2026-11-09, Brave 1.99.55).
	"brave-157-windows.h3-discovery.local-roots: sample 1, QUIC connection 5: secondInitialLength":         "as brave-157-macos's: Brave on Windows sent this resumed connection's second Initial (with the NEW_TOKEN token) with its packet number in 1 byte; it put it in 2 bytes in 138 of 150 connections, token or not (all 12 one-byte ones with a token), so quic.packetNumbers is shared_second_long",
	"brave-157-windows.h3-discovery.local-roots: sample 1, QUIC connection 4: tokens":                      "as brave-157-macos's: Brave on Windows, like Chrome, presented its NEW_TOKEN token on only some reconnections whose host had given it one, which ones varying between samples (Chromium's token cache, not modelled)",
	"brave-157-windows.h3-discovery.local-roots: sample 3, QUIC connection 4: tokens":                      "as sample 1, QUIC connection 4",
	"brave-157-windows.h3-discovery.local-roots: sample 3, QUIC connection 5: tokens":                      "as sample 1, QUIC connection 4",
	"brave-157-windows.h3-page.local-roots: sample 1, QUIC connection 1: priorityUpdate /h3/fetch-json.so": "as chrome-156-macos's: the server read this PRIORITY_UPDATE 1 ms after its request's HEADERS (another datagram), where every other one, and every other sample's, went with it",
	"brave-157-windows.h3-streams-blocked.local-roots: sample 2: streamsBlocked.count":                     "Brave on Windows sent 16 STREAMS_BLOCKED on this page, 2 more than the bursts' waiting requests account for (14, as in samples 1 and 3): sent again, not captured why",
	// Brave 157 on Linux (2026-11-09, Brave 1.99.55, WSL).
	"brave-157-linux.h3-discovery.local-roots: sample 1, QUIC connection 5: secondInitialLength":          "as brave-157-macos's: Brave on Linux sent this resumed connection's second Initial (with the NEW_TOKEN token) with its packet number in 1 byte; it put it in 2 bytes in 148 of 156 connections, token or not (all 8 one-byte ones with a token), so quic.packetNumbers is shared_second_long",
	"brave-157-linux.h3-discovery.local-roots: sample 1, QUIC connection 4: tokens":                       "as brave-157-macos's: Brave on Linux, like Chrome, presented its NEW_TOKEN token on only some reconnections whose host had given it one, which ones varying between samples (Chromium's token cache, not modelled)",
	"brave-157-linux.h3-discovery.local-roots: sample 2, QUIC connection 4: tokens":                       "as sample 1, QUIC connection 4",
	"brave-157-linux.h3-discovery.local-roots: sample 3, QUIC connection 1: priorityUpdate /h3/fetch2.so": "as brave-157-windows's h3-page: the server read this PRIORITY_UPDATE 1 ms after its request's HEADERS (another datagram), where every other one, and every other sample's, went with it",
	"brave-157-linux.h3-download.local-roots: sample 3: credit":                                           "as chrome-157-linux's: one MAX_STREAM_DATA raised the limit by 3,208,984 bytes, 2.01% above 3 MiB, the others within 2%: when Brave's reader drained the stream, not captured",
	"brave-157-linux.h3-broken.local-roots: sample 1: hold":                                               "Brave opened a second, resumed TCP connection to www.h3-b.test 9.2 s in, 4.0 s after its first, which carried no request until closed1.cs: when the 0-RTT attempt gave up at 53.2 s the fresh attempt raced that open connection, so no new TCP came with it (samples 2 and 3 opened theirs at 53.3 s, with the fresh attempt, as on macOS and Windows); why the early connection opened is not captured",
}

// conn is one captured QUIC connection.
type conn struct {
	// at names it: "<source>.<scenario>[.local-roots]: sample N, QUIC
	// connection M", source the profile whose capture it is.
	at string
	// scenario is "h3-discovery" or "h3-page"; sample counts from 0.
	scenario string
	sample   int
	page     *capture.Page
	c        *quictrace.Conn
	resumed  bool
	// done: the handshake completed (H3 is set).
	done bool
}

// captureSet is every QUIC connection of a profile's h3 captures, and the
// pinned gaps used so far.
type captureSet struct {
	// name is the profile's, p the profile.
	name  string
	p     *profile.Spec
	conns []*conn
	// pages are the h3-discovery captures' samples in which some QUIC
	// connection completed its handshake, with their file's name.
	discovery []discoveryPage
	usedGaps  map[string]bool
}

type discoveryPage struct {
	at   string
	page *capture.Page
}

// load reads the h3 captures p is checked against, as captured: h3gen
// measures the browser's field encoding, so nothing is rewritten.
func load(dir string, p *profile.Spec) (*captureSet, error) {
	files, err := server.H3ProfileCaptures(dir, p)
	if err != nil {
		return nil, err
	}

	set := &captureSet{
		name:     p.Name,
		p:        p,
		usedGaps: map[string]bool{},
	}
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f.Path), ".pagetrace.json.gz")
		scenario := f.Scenario

		pages, err := capture.LoadPages(f.Path)
		if err != nil {
			return nil, err
		}

		for si, page := range pages {
			anyDone := false
			for ci, c := range page.QUIC {
				cn := &conn{
					at:       fmt.Sprintf("%s: sample %d, QUIC connection %d", base, si+1, ci+1),
					scenario: scenario,
					sample:   si,
					page:     page,
					c:        c,
					resumed:  c.Resumed,
					done:     c.H3 != nil,
				}

				anyDone = anyDone || cn.done
				set.conns = append(set.conns, cn)
			}

			if scenario == "h3-discovery" && anyDone {
				set.discovery = append(set.discovery, discoveryPage{
					at:   fmt.Sprintf("%s: sample %d", base, si+1),
					page: page,
				})
			}
		}
	}

	return set, nil
}

// gap reports whether c's difference in feature is pinned, and marks the
// pin used.
func (s *captureSet) gap(c *conn, feature string) bool {
	return s.pinned(c.at + ": " + feature)
}

// pinned reports whether sampleGaps pins key, and marks the pin used.
func (s *captureSet) pinned(key string) bool {
	if _, ok := sampleGaps[key]; !ok {
		return false
	}

	s.usedGaps[key] = true

	return true
}

// unusedGaps fails for the pins of the captures this profile is checked
// against that no capture showed.
func (s *captureSet) unusedGaps() error {
	var errs []error
	for key := range sampleGaps {
		if s.pins(key) && !s.usedGaps[key] {
			errs = append(errs, fmt.Errorf("sampleGaps pins %q, which no longer appears: remove it", key))
		}
	}

	slices.SortFunc(errs, func(a, b error) int {
		return strings.Compare(a.Error(), b.Error())
	})

	return errors.Join(errs...)
}

// pins reports whether the sampleGaps key is about a capture the profile
// is checked against: one of the profile its scenario comes from
// (capture.Source).
func (s *captureSet) pins(key string) bool {
	file, _, _ := strings.Cut(key, ":")
	for _, name := range append([]string{s.name}, slices.Collect(maps.Values(s.p.CapturesFrom))...) {
		if !strings.HasPrefix(file, name+".") {
			continue
		}

		if sc, ok := capture.ScenarioOf(name, file+".pagetrace.json.gz"); ok && capture.Source(s.p, sc) == name {
			return true
		}
	}

	return false
}

// done are the connections that completed their handshake.
func (s *captureSet) done() []*conn {
	var out []*conn
	for _, c := range s.conns {
		if c.done {
			out = append(out, c)
		}
	}

	return out
}

// packet is one packet with its datagram.
type packet struct {
	datagram int
	d        *quictrace.Datagram
	p        *quictrace.Packet
}

// packets lists c's packets in wire order.
func (c *conn) packets() []packet {
	var out []packet
	for di := range c.c.Datagrams {
		d := &c.c.Datagrams[di]
		for pi := range d.Packets {
			out = append(out, packet{
				datagram: di,
				d:        d,
				p:        &d.Packets[pi],
			})
		}
	}

	return out
}

// hasFrame reports whether p carries a frame of type typ.
func hasFrame(p *quictrace.Packet, typ string) bool {
	return slices.ContainsFunc(p.Frames, func(f quictrace.Frame) bool {
		return f.Type == typ
	})
}

// cryptoInitials are c's Initial packets that carry CRYPTO frames: its
// ClientHello's.
func (c *conn) cryptoInitials() []packet {
	var out []packet
	for _, p := range c.packets() {
		if p.p.Type == quictrace.PacketInitial && hasFrame(p.p, quictrace.FrameCrypto) {
			out = append(out, p)
		}
	}

	return out
}

// helloLength is the length of c's ClientHello.
func (c *conn) helloLength() int {
	return len(c.c.ClientHelloBytes())
}

// collector gathers errors, each naming its connection.
type collector struct {
	errs []error
}

func (e *collector) add(c *conn, format string, args ...any) {
	e.errs = append(e.errs, fmt.Errorf("%s: %s", c.at, fmt.Sprintf(format, args...)))
}

func (e *collector) err() error {
	return errors.Join(e.errs...)
}

// tpID is a transport parameter's profile ID: GREASE for an unnamed
// reserved one.
func tpID(tp quictrace.TransportParameter) profile.TransportParameterID {
	if tp.Grease {
		return profile.GREASETransportParameter
	}

	return profile.TransportParameterID(tp.ID)
}
