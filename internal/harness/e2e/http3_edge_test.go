package e2e

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// h3PageHost is the h3 pages' own host (the server's h3Host), where / is.
const h3PageHost = "www.h3-a.test"

// Every request of the h3-blackhole, h3-broken, h3-vn, h3-v2 (where python3
// has aioquic), h3-early, h3-too-early and h3-streams-blocked pages through
// mimicry on the browser's schedule, against every sample:
//
//   - per request: its connections in order, its delay, and on a 0-RTT
//     connection how each stream went (earlyStream);
//   - per QUIC attempt: resumption, versions, the server's answer, how it
//     ended, path moves (checkPathMoves), give-up datagrams, its trigger;
//   - per host: the STREAMS_BLOCKED frames sent there, those sent alone
//     and their limits, and those sent with a stream, their limits and
//     place (compareStreamsBlockedWith); on h3-streams-blocked, each
//     burst's waits sorted (compareBurstWaits);
//   - per profile with quic.ack.afterRtt, over all its pages: the lone
//     packets acknowledged at once after a quiet spell (afterRTTRecord);
//   - per TCP connection: its trigger, its race against the QUIC attempt,
//     the GOAWAY sent, how many were declined.
//
// Times lie within the samples' range widened by edgeSlack, plus this
// process's stalls (compareTimes); a page whose race between QUIC and TCP
// the stalls decided (raceStall) runs again, up to edgeRuns times in all,
// as such a run compares nothing of mimicry's. Known differences are in
// h3EdgeGaps and h3EdgeSchedulingGaps. Each page runs twice: with
// Config.BrowserQUICFallback and Config.BrowserDialDelay ("browser"),
// where that is all that differs, and with the fast default ("fast"),
// whose differences besides are exactly h3EdgeFastGaps: requests that
// stop waiting on a QUIC attempt the server has not answered, and the TCP
// connections they open (a healthy page has none), and dials that start
// at once (checkDialDelays). Subtests run in parallel (about 75 s).
func TestHTTP3EdgeMatchesBrowser(t *testing.T) {
	dials := &dialRecord{}
	lone := &afterRTTRecord{}
	t.Cleanup(func() {
		dials.compare(t)
		lone.compare(t)
	})

	for _, name := range testProfiles(t) {
		for _, sc := range h3EdgeScenarios {
			for _, fast := range []bool{false, true} {
				mode := "browser"
				if fast {
					mode = "fast"
				}

				t.Run(name+"/"+sc.Name+"/"+mode, func(t *testing.T) {
					t.Parallel()

					if sc.NeedsAioquic() {
						if err := server.AioquicAvailable(t.Context()); err != nil {
							t.Skipf("%s needs aioquic: %v", sc.Name, err)
						}
					}

					p, err := loadProfile(name)
					if err != nil {
						t.Fatal(err)
					}

					if p.HTTP3 == nil || p.QUIC == nil {
						t.Fatal("the profile has no http3 or quic section")
					}

					want := h3Captures(t, p, sc)
					skip, unused := edgeGapsFor(t, capture.Source(p, sc.Name)+"/"+sc.Name, fast)
					config := func(cfg *mimicry.Config) {
						cfg.BrowserQUICFallback = !fast
						cfg.BrowserDialDelay = !fast
					}

					got := runH3Schedule(t, p, sc, want[0], skip, config)
					// A race this process's stalls decided measured the
					// stall: the page runs again, and the last run is
					// compared whatever happened in it.
					for run := 1; run < edgeRuns; run++ {
						why, stalled := raceStall(p, got)
						if !stalled {
							break
						}

						t.Logf("run %d: the process's stalls decided %s; running the page again", run, why)

						got = runH3Schedule(t, p, sc, want[0], skip, config)
					}

					compareEdge(t, p, got, want, skip)
					lone.add(name, p, got)
					dials.add(name, p, !fast, checkDialDelays(t, p, got, !fast))

					if sc == server.H3StreamsBlocked {
						compareBurstWaits(t, got, want, skip)
						compareStreamsBlockedWith(t, got, want, skip)
					}

					unused()
				})
			}
		}
	}
}

// h3EdgeScenarios are the scenarios TestHTTP3EdgeMatchesBrowser replays,
// each in the browser's mode and the fast one.
var h3EdgeScenarios = []*server.Scenario{server.H3Blackhole, server.H3Broken, server.H3VN, server.H3V2, server.H3Early, server.H3TooEarly, server.H3StreamsBlocked}

// edgeRuns bounds how often TestHTTP3EdgeMatchesBrowser runs a page whose
// race the process's stalls decided; the last run is compared as it is.
const edgeRuns = 3

// h3EdgeGaps are known differences, by profile, scenario and feature (as
// compareEdge labels it): the feature is not compared. A listed gap that
// no longer appears fails.
var h3EdgeGaps = func() map[string]string {
	gaps := map[string]string{
		"chrome-155-macos/h3-broken edge TCP www.h3-b.test #3 goaway":                       "Chrome on macOS moved h3-b's requests to a new TCP connection after 6–10 s idle on its last one (closed4–closed6, in every sample once the origin was broken), not captured why; mimicry keeps its HTTP/2 connection, as Chrome on Windows did in every sample",
		"chrome-155-macos/h3-broken edge TCP www.h3-b.test #3 after its trigger ms":         "as its goaway",
		"chrome-155-macos/h3-broken edge QUIC www.h3-b.test #2 Initial packet number steps": "as its datagrams: the browser's first resumed probe is 4 Initials (steps 4 1 1 1, mimicry's 4 alone); every other step, the skip before each probe and none before the close, matches. On Windows the samples disagree (4 Initials in 2, 2 in the other), so the step is not compared there",
		"firefox-158-macos/h3-broken edge QUIC www.h3-b.test #2 datagrams":                  "Firefox's resumed probes vary between samples (pairs every PTO from a ~3 ms cached RTT, the 3-packet resumed first flight not repeated, lone 40-B 0-RTT datagrams); quic.loss's flight probe replays all 3 first-flight packets per PTO, and the profile has no resumed-probe rule; give-up time and close match",
		"firefox-158-macos/h3-too-early edge TCP www.h3-a.test declined":                    "beside 3–4 of its 4 retries after 425 Too Early per sample (which ones varies), Firefox also opened a TCP connection to the host and closed it before any request; the retry itself goes on the QUIC connection, as mimicry's does. No profile field holds when Firefox opens these (report h3-early-2026-10-26)",
		"firefox-158-windows/h3-too-early edge TCP www.h3-a.test declined":                  "as firefox-158-macos's: beside its retries after 425 Too Early Firefox opened TCP connections to the host and closed them before any request (3 per sample on Windows, the unused opening connection left out); no profile field holds when Firefox opens these",
		"firefox-158-windows/h3-broken edge QUIC www.h3-b.test #2 datagrams":                "as firefox-158-macos's: Firefox's resumed probes vary between samples, and the profile has no resumed-probe rule; give-up time and close match",
		"firefox-158-windows/h3-broken edge TCP www.h3-a.test #2 after its trigger ms":      "not the network: Firefox on Windows's page made keep2.cs, this connection's trigger, 4.9 s after silent5.so where the page waits 2 s (every sample; its later timers 3.7–5.5 s late too, Firefox on macOS's on time), so the connection that carries the silenced requests came 3.9–4.4 s after it, not mimicry's 7.0 s; the connection itself starts when the silenced QUIC connection dies, as the requests' delays (compared) show",
	}

	// Chrome's on both platforms: the late resend of the silenced request
	// and the resumed attempt's probes.
	for _, name := range []string{"chrome-155-macos", "chrome-156-windows"} {
		for k, why := range map[string]string{
			"edge request /h3/silence.so sent":               "the server never answers the silenced request (the scenario drops it); Chrome sent it again over a new TCP connection 54–61 s later, when it closed its migrated connection with '85:Network blackhole detected' after keep-alive PINGs there went unanswered (report http3-edge-2026-10-25, corrections; Chrome on Windows the same, 60.5 s after the connection started), a rule no profile field holds; mimicry's waits on its QUIC connection",
			"edge request /h3/silence.so delay ms":           "as its sending",
			"edge TCP www.h3-a.test #2 goaway":               "the TCP connection that carried Chrome's late resend of silence.so",
			"edge TCP www.h3-a.test #2 trigger":              "as its goaway",
			"edge TCP www.h3-a.test #2 after its trigger ms": "as its goaway",
			"edge QUIC www.h3-b.test #2 datagrams":           "Chrome's resumed attempt probes first with its whole resumed hello, cut afresh (4 Initials; 2 in 1 of the 3 Windows samples), and sends lone 0-RTT datagrams later (+2.0/3.0 s); quic.loss's one_packet probe sends one Initial per PTO and says nothing of the 0-RTT ones (12–13 datagrams, Chrome 14–16; timing and give-up match)",
		} {
			gaps[name+"/h3-broken "+k] = why
		}
	}

	// Chrome on Linux: the late resend of the silenced request and the
	// resumed attempt's probes, as on macOS and Windows, and h3-b's
	// requests moved to a new TCP connection, as on macOS.
	for k, why := range map[string]string{
		"edge request /h3/silence.so sent":               "as chrome-156-windows's: Chrome on Linux sent the silenced request again over a new TCP connection 54.4 s after it made it (54410–54413 ms, 3 of 3 samples), when it closed its migrated connection with '85:Network blackhole detected' 60.4 s after the connection started, a rule no profile field holds; mimicry's waits on its QUIC connection",
		"edge request /h3/silence.so delay ms":           "as its sending",
		"edge TCP www.h3-a.test #2 goaway":               "the TCP connection that carried Chrome's late resend of silence.so",
		"edge TCP www.h3-a.test #2 trigger":              "as its goaway",
		"edge TCP www.h3-a.test #2 after its trigger ms": "as its goaway",
		"edge QUIC www.h3-b.test #2 datagrams":           "as chrome-156-windows's: Chrome on Linux's resumed attempt probes first with its whole resumed hello, cut afresh (2 Initials in 2 of 3 samples, 4 in the third), and sends a lone 0-RTT datagram 2.0 s in; quic.loss's one_packet probe sends one Initial per PTO and says nothing of the 0-RTT ones (12–13 datagrams, Chrome 14–16; timing and give-up match)",
		"edge TCP www.h3-b.test #3 goaway":               "as chrome-155-macos's: Chrome on Linux moved h3-b's requests to a new TCP connection after 8.0 s idle on its last one (closed5.cs, 3 of 3 samples; closed6.cs to another after 10.0 s in 1), not captured why; mimicry keeps its HTTP/2 connection",
		"edge TCP www.h3-b.test #3 trigger":              "as its goaway",
		"edge TCP www.h3-b.test #3 after its trigger ms": "as its goaway",
		"edge request /h3/closed5.cs sent":               "as its goaway",
	} {
		gaps["chrome-157-linux/h3-broken "+k] = why
	}

	// Edge on macOS: the late resend of the silenced request, as Chrome's
	// everywhere; the resumed attempt's probes, as Chrome on macOS's but
	// with a pair first; and h3-b's requests moved to new TCP connections,
	// as Chrome on macOS and Linux, here twice in every sample.
	for k, why := range map[string]string{
		"edge request /h3/silence.so sent":                       "as chrome-156-windows's: Edge sent the silenced request again over a new TCP connection 54.4 s after it made it (54424–54433 ms, 3 of 3 samples), when it closed its migrated connection with '85:Network blackhole detected' 60.4 s after the connection started, a rule no profile field holds; mimicry's waits on its QUIC connection",
		"edge request /h3/silence.so delay ms":                   "as its sending",
		"edge TCP www.h3-a.test #2 goaway":                       "the TCP connection that carried Edge's late resend of silence.so",
		"edge TCP www.h3-a.test #2 trigger":                      "as its goaway",
		"edge TCP www.h3-a.test #2 after its trigger ms":         "as its goaway",
		"edge QUIC www.h3-b.test #2 datagrams":                   "as chrome-155-macos's: Edge's resumed attempt probes first with its resumed hello cut afresh (2 Initials, 33–35 ms in; Chrome on macOS 4), and sends a lone 0-RTT datagram 2.0 s in; quic.loss's one_packet probe sends one Initial per PTO and says nothing of the 0-RTT ones (14 datagrams in 3 of 3 samples, mimicry's 12–13; timing and give-up match)",
		"edge QUIC www.h3-b.test #2 Initial packet number steps": "as its datagrams: the browser's first resumed probe is 2 Initials (steps 4 1, mimicry's 4 alone, 3 of 3 samples); every other step, the skip before each probe and none before the close, matches",
		"edge TCP www.h3-b.test #3 goaway":                       "as chrome-155-macos's: Edge moved h3-b's requests to a new TCP connection after 8.0 s idle on its last one (closed5.cs, 3 of 3 samples), not captured why; mimicry keeps its HTTP/2 connection",
		"edge TCP www.h3-b.test #3 trigger":                      "as its goaway",
		"edge TCP www.h3-b.test #3 after its trigger ms":         "as its goaway",
		"edge request /h3/closed5.cs sent":                       "as its goaway",
		"edge TCP www.h3-b.test #4 goaway":                       "as TCP www.h3-b.test #3's: Edge moved closed6.cs to another new TCP connection after 10.0 s idle on #3 (3 of 3 samples; Chrome on macOS in 1 of 3, on Linux in 1 of 3), not captured why; mimicry keeps its HTTP/2 connection",
		"edge TCP www.h3-b.test #4 trigger":                      "as its goaway",
		"edge TCP www.h3-b.test #4 after its trigger ms":         "as its goaway",
		"edge request /h3/closed6.cs sent":                       "as TCP www.h3-b.test #4's goaway",
	} {
		gaps["edge-157-macos/h3-broken "+k] = why
	}

	gaps["firefox-159-linux/h3-broken edge QUIC www.h3-b.test #2 datagrams"] = "as firefox-158-windows's: Firefox on Linux's resumed probes vary between samples (pairs every PTO from a ~3 ms cached RTT in 2 of 6; from 300 ms in 4, two or three Initials first; lone 40–42-B 0-RTT datagrams beside them; 14–26 datagrams in the 4 samples whose attempt ran to its 5.0 s close, mimicry's 31–37), and the profile has no resumed-probe rule; give-up time and close match"

	// Firefox on Linux's side connections on the h3-streams-blocked page,
	// in every sample: each one's start, close and trigger time, and its
	// trigger where the samples agree (the 3rd to 6th's differ, as does
	// whether the 7th resumed: not compared).
	const firefoxSide = "as firefox-159-macos's and firefox-159-windows's short-lived side connections (h3gen's sampleGaps; EVIDENCE.md, Not covered): in every Linux sample of this page (3 of 3) Firefox opened 6 QUIC connections beside the page's, none carrying a request, each within 4 ms of one of the page's fetches (fetch1.so, two in each burst, last.so; the first resumed with 0-RTT) and closed by Firefox 1–13 ms after it started (H3_NO_ERROR); mimicry opens none. firefox-158's captures of the page, which the macOS and Windows 159 profiles inherit, have none"
	for i := 2; i <= 7; i++ {
		k := fmt.Sprintf("firefox-159-linux/h3-streams-blocked edge QUIC www.h3-a.test #%d", i)

		gaps[k+" closed by"] = firefoxSide
		gaps[k+" after its trigger ms"] = firefoxSide

		if i < 7 {
			gaps[k] = firefoxSide
		}
	}

	gaps["firefox-159-linux/h3-streams-blocked edge QUIC www.h3-a.test #2 trigger"] = firefoxSide
	gaps["firefox-159-linux/h3-streams-blocked edge QUIC www.h3-a.test #7 trigger"] = firefoxSide

	// Safari's OPTIONS fetch: the h3-early pages make one, its kinds
	// captures none.
	for _, name := range []string{"safari-26-macos", "safari-27-macos"} {
		const noOptions = "Safari's profile has no OPTIONS fetch (none in its kinds captures; kindsgen does not read the h3-early pages), so mimicry refuses it (ErrNotMeasured) and its form.so navigation goes on QUIC #9, not #10"

		for _, sc := range []string{"h3-early", "h3-too-early"} {
			for _, k := range []string{
				"request /h3/options.so not measured",
				"request /h3/options.so sent",
				"request /h3/options.so streams",
				"request /h3/options.so delay ms",
				"request /h3/form.so sent",
				"request /h3/form.so streams",
				"QUIC www.h3-a.test #9 closed by",
				"QUIC www.h3-a.test #9 trigger",
				"QUIC www.h3-a.test #10",
				"QUIC www.h3-a.test #10 trigger",
				"QUIC www.h3-a.test #10 after its trigger ms",
			} {
				gaps[name+"/"+sc+" edge "+k] = noOptions
			}
		}
	}

	// The iPhone (decision 0051): its first blackholed attempt sends a
	// second Initial pair before TCP takes over, the first of it an
	// unpadded 1038-byte datagram; quic.loss is not measured for it. Its
	// reconnections after h3-broken's silence resume with 0-RTT, which is
	// not measured for it either. Chrome on the iPhone (decision 0053)
	// does the same, with one or two such pairs in every blackholed
	// attempt, and so does Firefox on the iPhone. Firefox's samples differ
	// on the first attempt's Initial packet number steps, so the test only
	// logs them for it and it has no pin for them; the others keep theirs.
	for _, name := range []string{"safari-26-ios", "chrome-157-ios", "firefox-159-ios"} {
		gaps[name+"/h3-blackhole edge QUIC www.h3-a.test #1 datagrams"] = iosProbes

		if name != "firefox-159-ios" {
			gaps[name+"/h3-blackhole edge QUIC www.h3-a.test #1 Initial packet number steps"] = iosProbes
		}

		for _, k := range []string{"QUIC www.h3-a.test #2", "QUIC www.h3-b.test #2", "QUIC www.h3-b.test #2 datagrams"} {
			gaps[name+"/h3-broken edge "+k] = iosZeroRTT
		}

		for _, r := range []string{"silence", "silent1", "silent2", "silent3", "silent4", "silent5", "silent6"} {
			gaps[name+"/h3-broken edge request /h3/"+r+".so sent"] = iosZeroRTT
			gaps[name+"/h3-broken edge request /h3/"+r+".so streams"] = iosZeroRTT
		}
	}

	gaps["chrome-157-ios/h3-blackhole edge QUIC www.h3-a.test #2 datagrams"] = iosProbes
	gaps["chrome-157-ios/h3-broken edge QUIC www.h3-b.test #3 datagrams"] = iosZeroRTT
	gaps["firefox-159-ios/h3-blackhole edge QUIC www.h3-a.test #2 datagrams"] = iosProbes
	gaps["firefox-159-ios/h3-blackhole edge QUIC www.h3-a.test #2 Initial packet number steps"] = iosProbes

	return gaps
}()

// iosProbes and iosZeroRTT are why the iPhone's failure pair differs
// (h3EdgeGaps).
const (
	iosProbes  = "the iPhone sent a second Initial pair 50–140 ms into its first blackholed attempt, before TCP took over (macOS Safari's was dropped first), the pair's first an unpadded Initial in a 1038-byte datagram (RFC 9000 asks 1200); quic.loss, which would hold the probes, is not measured for the iPhone, whose timing over Wi-Fi the captures cannot pin"
	iosZeroRTT = "the iPhone resumed its reconnections with 0-RTT, the waiting requests in it (as macOS Safari); 0-RTT is not measured for it (no h3-early, and its resumed Initials come unpadded, 1129 bytes), so mimicry reconnects with a full handshake"
)

// h3EdgeSchedulingGaps are known differences that show or not by this
// process's scheduling, keyed as h3EdgeGaps: skipped as those are, but
// not required to appear.
var h3EdgeSchedulingGaps = func() map[string]string {
	const (
		pair        = "Chrome on Windows made the pair's POST 4–6 ms after its GET, whose resumed connection had started at once and, on loopback, finished its handshake by then: the GET is the trigger and the POST went in 1-RTT (5 of 6 samples; on macOS it came within 3 ms of the Finished and was held for it). mimicry's replay makes both in the same millisecond, so its POST is held for the handshake (http3.earlyData) and is the later trigger as this process schedules the two"
		firefoxPair = "Firefox on Windows made the pair's POST 4 ms after its GET, whose resumed connection (the GET in its 0-RTT) sent its Finished 1.1–2.9 ms after it started: the POST went in 1-RTT after the Finished (6 of 6 samples; on macOS, the Finished 2.8–4.6 ms in, it went in the Finished's datagram in 4 of 6). mimicry's replay makes both in the same millisecond, so its POST is held for the handshake and goes with the Finished (http3.earlyData), and which of the two is the later trigger is this process's scheduling"
	)

	gaps := map[string]string{}
	for _, sc := range []string{"h3-early", "h3-too-early"} {
		gaps["chrome-156-windows/"+sc+" edge QUIC www.h3-a.test #6 trigger"] = pair
		gaps["chrome-156-windows/"+sc+" edge request /h3/pair-post.so streams"] = pair
		gaps["firefox-158-windows/"+sc+" edge QUIC www.h3-a.test #6 trigger"] = firefoxPair
		gaps["firefox-158-windows/"+sc+" edge request /h3/pair-post.so streams"] = firefoxPair
	}

	gaps["chrome-157-linux/h3-too-early edge QUIC www.h3-a.test #6 trigger"] = "as chrome-156-windows's: Chrome on Linux made the pair's POST 4–5 ms after its GET, whose resumed connection started 0–1 ms after it, so the GET is the trigger (3 of 3 samples; in h3-early the samples differ), and the POST went in 1-RTT in 1 sample and held for the Finished in 2. mimicry's replay makes both in the same millisecond, so its POST is held for the handshake (http3.earlyData) and is the later trigger as this process schedules the two"

	for _, name := range []string{"safari-26-macos", "safari-27-macos"} {
		for _, sc := range []string{"h3-early", "h3-too-early"} {
			gaps[name+"/"+sc+" edge QUIC www.h3-a.test #6 trigger"] = "Safari made the pair's GET and POST together (0–1 ms apart) 5–9 ms before its connection started, so the POST is the later trigger; mimicry's replay makes both in the same millisecond, before its resumedDialDelayMs dial, so which counts as the later is this process's scheduling"
		}
	}

	// The h3-streams-blocked bursts: the page makes each burst's six
	// fetches one after another (Chrome and Firefox 0–6 ms apart, Firefox
	// on Linux 3–14, Safari in the same millisecond), and every browser
	// gives the waiting ones streams in that order: two at once, two
	// after ~300 ms, two after ~600 ms. The replay starts the six
	// together, each on its own goroutine, so which of them waits is this
	// process's scheduling.
	// How many wait and how long is compared without the order
	// (compareBurstWaits), and each request still goes on the QUIC
	// connection (its "sent").
	const burstOrder = "the replay makes the burst's six requests together, so which of them waits for the server's stream limit is this process's scheduling (the waits themselves, sorted, are compared: compareBurstWaits); the browser made them in order and gave them streams in that order"
	for _, name := range []string{"chrome-156-macos", "chrome-157-linux", "edge-157-macos", "firefox-158-macos", "firefox-158-windows", "firefox-159-linux", "safari-27-macos"} {
		for _, burst := range []string{"a", "b"} {
			for i := 1; i <= 6; i++ {
				gaps[fmt.Sprintf("%s/h3-streams-blocked edge request /h3/%s%d.so delay ms", name, burst, i)] = burstOrder
			}
		}
	}

	// Firefox's STREAMS_BLOCKED rule, which this page shows, is
	// quic.streamsBlocked; what differs is how far each raise goes, by
	// whether the replay's first two requests of a burst share a packet.
	const firefoxBlocked = "the replay's request spacing, not the ACKs: Firefox sent each burst's first two requests 3.2–5.7 ms apart (Windows 3.7–5.2), so quic-go answered them in two packets, Firefox acknowledged each at once (quic.ack.afterRtt) and quic-go raised the limit by one per response; by quic.streamsBlocked's waiting rule Firefox sent one with each stream but a burst's last (6 7 8, 12 13 14; on Windows in 2 of 3 samples, the third's last two raises came in one batch and it opened both waiting streams in one packet with none: 6 7, 12 13). The replay makes the burst's requests together; when this process puts the first two in one packet, quic-go answers both in one and raises by two, and by the same rule mimicry sends one per raise (7, 13); when it does not, mimicry sends Firefox's. With the requests 5 ms apart it sends Firefox's every time (without afterRTT still 7, 13: tried 2026-11-02). The frames it sends alone match (5, 11)"
	for _, name := range []string{"firefox-158-macos", "firefox-158-windows"} {
		gaps[name+"/h3-streams-blocked edge QUIC www.h3-a.test STREAMS_BLOCKED with streams"] = firefoxBlocked
	}

	gaps["firefox-159-linux/h3-streams-blocked edge QUIC www.h3-a.test STREAMS_BLOCKED with streams"] = "as firefox-158-windows's: the replay's request spacing. Firefox on Linux sent each burst's first two requests 3–10 ms apart and one STREAMS_BLOCKED with each waiting stream but a burst's last (6 7 8 in 3 of 3 samples, 12 13 14 in 1; in the other 2, pinned in h3gen's sampleGaps, the second burst's first two waiting streams left in one packet behind one 13); when this process puts a burst's first two requests in one packet, quic-go raises the limit by two and mimicry sends one per raise (7, 13), else Firefox's"

	return gaps
}()

// h3EdgeFastGaps are how the fast default differs from the browser on the
// edge pages, keyed as h3EdgeGaps: each must appear, in fast mode only,
// and only on a page where QUIC fails (decision 0036).
var h3EdgeFastGaps = func() map[string]string {
	const (
		chromeHold = "fast fallback: Chrome holds closed1.cs in 0-RTT on its resumed attempt until it gives up (4.0 s, zeroRttHolds), then sends it on TCP; mimicry's goes on TCP once the server has not answered for the race delay (tcpDelayMs, about 305 ms; Edge's 313–315 ms)"
		chromeNext = "fast fallback: closed2.cs, made while Chrome's 0-RTT attempt is unanswered, waits for it with no TCP and triggers the fresh attempt and TCP after the give-up; mimicry's goes on the TCP connection closed1.cs opened, and the fresh attempt follows the next request after the give-up, closed3.cs"
		firefox    = "fast fallback: Firefox holds closed2.cs on its unanswered resumed attempt until it gives up (5 s, hold); mimicry's goes on TCP once the server has not answered for its first probe timeout (300 ms), opening the TCP connection closed3.cs would have"
		firefoxWin = "fast fallback: Firefox on Windows held closed2.cs and closed3.cs on its unanswered resumed attempt until it gave up (9.8–10.2 s, hold; 2 of 3 samples, the third pinned in h3gen), and the TCP connection then opened came 4.8–5.2 s after closed3.cs; mimicry's closed2.cs goes on TCP once the server has not answered for its first probe timeout (300 ms), opening that connection at once"
		safari     = "fast fallback: Safari's request waits out its 100 ms race delay after Version Negotiation ended its attempt; mimicry's goes on TCP at once"
	)

	gaps := map[string]string{
		"firefox-158-macos/h3-broken edge request /h3/closed2.cs delay ms":             firefox,
		"firefox-158-macos/h3-broken edge TCP www.h3-b.test #3 trigger":                firefox,
		"firefox-158-windows/h3-broken edge TCP www.h3-b.test #3 after its trigger ms": firefoxWin,
	}
	// Edge on macOS holds and follows as Chrome does (closed1.cs 4007–4009 ms,
	// closed2.cs 2005–2007 ms, 3 of 3 samples).
	for _, name := range []string{"chrome-155-macos", "chrome-156-windows", "chrome-157-linux", "edge-157-macos"} {
		for k, why := range map[string]string{
			"edge request /h3/closed1.cs delay ms":            chromeHold,
			"edge TCP www.h3-b.test #2 trigger":               chromeHold,
			"edge TCP www.h3-b.test #2 after its trigger ms":  chromeHold,
			"edge TCP www.h3-b.test #2 after QUIC #2 ms":      chromeHold,
			"edge TCP www.h3-b.test #2 after QUIC #3 ms":      chromeHold,
			"edge request /h3/closed2.cs delay ms":            chromeNext,
			"edge QUIC www.h3-b.test #3 trigger":              chromeNext,
			"edge QUIC www.h3-b.test #3 after its trigger ms": chromeNext,
		} {
			gaps[name+"/h3-broken "+k] = why
		}
	}

	for _, name := range []string{"safari-26-macos", "safari-27-macos"} {
		for _, k := range []string{
			"edge request /h3/vn-3.so delay ms",
			"edge TCP www.h3-a.test #2 after its trigger ms",
			"edge TCP www.h3-a.test #2 after QUIC #1 ms",
		} {
			gaps[name+"/h3-vn "+k] = safari
		}
	}

	return gaps
}()

// edgeGapsFor is gapsFor for h3EdgeGaps and h3EdgeSchedulingGaps, and with
// fast for h3EdgeFastGaps.
func edgeGapsFor(t *testing.T, name string, fast bool) (skip func(key string) bool, unused func()) {
	t.Helper()

	var mu sync.Mutex

	seen := map[string]bool{}

	skip = func(key string) bool {
		k := name + " " + key
		why, ok := h3EdgeGaps[k]

		if !ok && fast {
			why, ok = h3EdgeFastGaps[k]
		}

		if !ok {
			why, ok = h3EdgeSchedulingGaps[k]
		}

		if !ok {
			return false
		}

		mu.Lock()

		seen[k] = true
		mu.Unlock()
		t.Logf("known gap: %s: %s", key, why)

		return true
	}
	unused = func() {
		required := []map[string]string{h3EdgeGaps}
		if fast {
			required = append(required, h3EdgeFastGaps)
		}

		for _, gaps := range required {
			for k := range gaps {
				if strings.HasPrefix(k, name+" ") && !seen[k] {
					t.Errorf("listed gap %q no longer appears", k)
				}
			}
		}
	}

	return skip, unused
}

// runH3Schedule walks sc's page through mimicry as p, its Config changed
// by config: the navigation to /, each step after its pause (NoWait steps
// on time beside the others), then fetch('/done') where browser did.
// Unanswered requests are cancelled; an ErrNotMeasured step fails unless
// skip pins it.
func runH3Schedule(t *testing.T, p *profile.Spec, sc *server.Scenario, browser *capture.Page, skip func(string) bool, config func(*mimicry.Config)) *capture.Page {
	t.Helper()
	// Its times from a request on are net of the process's stalls.
	stalls.watch()

	s := startPageWith(t, p, sc, config)
	if s.quicReady != nil {
		// The browser's page loads long after aioquic is up; a datagram
		// sent earlier would be recorded when aioquic starts reading.
		select {
		case <-s.quicReady:
		case <-time.After(30 * time.Second):
			t.Fatal("aioquic did not start")
		}
	}

	page, err := url.Parse(s.url(h3PageHost, "/"))
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()

	s.send(http.MethodGet, page.String(), mimicry.Request{}, nil, "")

	ctx, cancel := context.WithCancel(t.Context())

	defer cancel()

	steps := h3Steps(sc)
	select {
	case <-time.After(scriptStart(browser, steps[0])):
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}

	// from is the page the step is made from, read when it is issued: a
	// navigation step replaces page while earlier steps are still sending.
	send := func(st server.H3Step, from *url.URL, target string) error {
		err := sendScheduled(ctx, s, st, from, target)
		if errors.Is(err, mimicry.ErrNotMeasured) && skip("edge request "+st.Path()+" not measured") {
			return nil
		}

		return err
	}

	var wg sync.WaitGroup

	for _, st := range steps {
		select {
		case <-time.After(time.Duration(st.PauseMillis) * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}

		target := s.url(st.Host, st.Path())
		if st.NoWait {
			target += "?t=" + strconv.FormatInt(time.Since(start).Milliseconds(), 10)
		}

		if st.Kind == "navigate" {
			// It ends the page's steps; the page it loads asks for /done.
			if err := send(st, page, target); err != nil {
				t.Fatalf("%s: %v", st.Label, err)
			}

			if page, err = url.Parse(target); err != nil {
				t.Fatal(err)
			}

			break
		}

		if !st.NoWait {
			if err := send(st, page, target); err != nil {
				t.Fatalf("%s: %v", st.Label, err)
			}

			continue
		}

		from := page
		wg.Go(func() {
			if err := send(st, from, target); err != nil && ctx.Err() == nil {
				t.Errorf("%s: %v", st.Label, err)
			}
		})
	}

	time.Sleep(h3ScriptTurn)

	done := doneHost(browser)
	s.send(http.MethodGet, s.url(done, "/done"), mimicry.Request{
		Kind:   mimicry.Fetch,
		From:   page,
		NoCORS: done != h3PageHost,
	}, nil, "")

	for {
		select {
		case c := <-s.captures:
			if c.Finished && c.Page != nil {
				cancel()
				wg.Wait()

				return c.Page
			}

		case <-time.After(10 * time.Second):
			t.Fatal("no page capture")
		}
	}
}

// scriptStart is how long after its request for / arrived the browser's
// page started its script (parse and script start, which mimicry has
// none of): when first, a NoWait step, was made, less its pause. A QUIC
// attempt / started (Firefox's, on its Alt-Svc) then starts before the
// page's first request, as the browser's did.
func scriptStart(browser *capture.Page, first server.H3Step) time.Duration {
	v := viewEdge(browser)

	var nav, step *edgeRequest

	for _, r := range v.requests {
		switch r.label {
		case "/":
			nav = r
		case first.Path():
			step = r
		}
	}

	if nav == nil || step == nil || step.delay < 0 {
		return 0
	}

	return max(0, time.Duration(step.issued-nav.issued-int64(first.PauseMillis))*time.Millisecond)
}

// sendScheduled makes st's request from page to target, on ctx, and reads
// its response.
func sendScheduled(ctx context.Context, s *pageSession, st server.H3Step, page *url.URL, target string) error {
	method, r, h := h3StepRequest(st, page)

	var body io.Reader

	if st.Body != "" {
		body = strings.NewReader(st.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}

	maps.Copy(req.Header, h)

	resp, err := s.client.Do(mimicry.With(req, r))
	if err != nil {
		return err
	}

	_, err = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	return err
}

// doneHost is the host the browser's page asked for /done (another than
// the page's where QUIC is not the capture server's own).
func doneHost(browser *capture.Page) string {
	for _, tr := range browser.H2 {
		for _, r := range tr.Requests {
			if r.Path == "/done" {
				return tr.Host
			}
		}
	}

	return h3PageHost
}

// edgeSlack is how far outside the browser's samples' range a time may
// lie: loopback handshakes, timers and a loaded machine's scheduling.
// hi may be negative (a race's signed gap).
func edgeSlack(hi int) int {
	return 50 + max(hi, -hi)/20
}

// raceWindow is how close a host's TCP connection and QUIC attempt must
// start to be compared as a race.
const raceWindow = 500

// triggerSlack is how far into a request's millisecond an event may come
// before it and still have been caused by it (its issue time is ?t= plus
// the sample's clock offset, which rounds).
const triggerSlack = 3

// edgeRequest is one request of a page, as the server saw it.
type edgeRequest struct {
	label string
	// issued is when the page made it, on the page's clock: its ?t= plus
	// the sample's clock offset, or without one its first arrival.
	issued int64
	// delay is its last arrival less issued; -1 without ?t=.
	delay int64
	// sent are the connections it went on, in order of arrival.
	sent []string
	// streams are its streams on QUIC connections that sent 0-RTT, in
	// order of arrival: the connection and how the stream went
	// (earlyStream.describe).
	streams []string
}

// edgeView is what compareEdge compares of a page.
type edgeView struct {
	requests []*edgeRequest
	// tcp are each host's HTTP/2 connections that carried requests, in
	// order; declined counts the others.
	tcp      map[string][]*h2trace.Trace
	declined map[string]int
	quic     map[string][]*quictrace.Conn
}

// arrival is one request's arrival on a connection.
type arrival struct {
	at   int64
	path string
	via  string
	// early is its stream, on a QUIC connection that sent 0-RTT; nil
	// otherwise.
	early *earlyStream
}

// earlyStream is a request stream on a QUIC connection that sent 0-RTT:
// its packet type (of its first STREAM frame), whether that went in the
// datagram with the client's Finished, when the Finished went (page
// clock, ms), and whether an earlier stream of the connection carried the
// same URL (its retry after 425 Too Early).
type earlyStream struct {
	packet       string
	withFinished bool
	finished     int64
	again        bool
}

// describe is how s went, for a request issued at issued (page clock):
// in 0-RTT; held for the handshake (in 1-RTT though made before the
// Finished went, within triggerSlack: the page's clock is in whole ms),
// in the Finished's datagram or after it; in 1-RTT, made later; or again
// after a 425, in its packet type.
func (s *earlyStream) describe(issued int64) string {
	switch {
	case s.again:
		return "again after 425, " + s.packet
	case s.packet == quictrace.Packet0RTT:
		return "0-RTT"
	case issued > s.finished+triggerSlack:
		return s.packet
	case s.withFinished:
		return "held, with the Finished"
	}

	return "held, after the Finished"
}

// viewEdge reads page's requests and connections.
func viewEdge(page *capture.Page) edgeView {
	v := edgeView{
		tcp:      map[string][]*h2trace.Trace{},
		declined: map[string]int{},
		quic:     map[string][]*quictrace.Conn{},
	}
	traces := slices.Clone(page.H2)
	slices.SortStableFunc(traces, func(a, b *h2trace.Trace) int {
		return int(a.Millis - b.Millis)
	})

	var arrivals []arrival
	for _, tr := range traces {
		if len(tr.Requests) == 0 {
			v.declined[tr.Host]++
			continue
		}

		v.tcp[tr.Host] = append(v.tcp[tr.Host], tr)

		via := fmt.Sprintf("h2 %s #%d", tr.Host, len(v.tcp[tr.Host]))

		for _, r := range tr.Requests {
			at := tr.Millis
			for _, f := range tr.Frames {
				if f.Type == "HEADERS" && f.Stream == r.Stream {
					at += f.Millis
					break
				}
			}

			arrivals = append(arrivals, arrival{
				at:   at,
				path: r.Path,
				via:  via,
			})
		}
	}

	for _, c := range page.QUIC {
		v.quic[c.Host] = append(v.quic[c.Host], c)

		via := fmt.Sprintf("h3 %s #%d", c.Host, len(v.quic[c.Host]))

		if c.EarlyData {
			via += " 0-RTT"
		}

		if c.H3 == nil {
			continue
		}

		finished, finishedAt := c.FinishedDatagram()
		starts := c.StreamStarts()
		reqs := slices.Clone(c.H3.Requests)
		slices.SortFunc(reqs, func(a, b quictrace.H3Request) int {
			return cmp.Compare(a.Stream, b.Stream)
		})

		seen := map[string]bool{}
		for _, r := range reqs {
			a := arrival{
				at:   r.Millis,
				path: r.Path,
				via:  via,
			}
			if s, ok := starts[r.Stream]; ok && c.EarlyData {
				a.early = &earlyStream{
					packet:       s.PacketType,
					withFinished: s.Datagram == finished,
					finished:     c.Millis + finishedAt/1000,
					again:        seen[r.Path],
				}
			}

			seen[r.Path] = true

			arrivals = append(arrivals, a)
		}
	}

	slices.SortStableFunc(arrivals, func(a, b arrival) int {
		return int(a.at - b.at)
	})

	byPath := map[string]*edgeRequest{}
	last := map[string]int64{}
	ts := map[string]int64{}
	early := map[string][]arrival{}

	for _, a := range arrivals {
		r := byPath[a.path]
		if r == nil {
			label, t := splitT(a.path)

			r = &edgeRequest{
				label:  label,
				issued: a.at,
				delay:  -1,
			}

			if t >= 0 {
				ts[a.path] = t
			}

			byPath[a.path] = r
			v.requests = append(v.requests, r)
		}

		if len(r.sent) == 0 || r.sent[len(r.sent)-1] != a.via {
			r.sent = append(r.sent, a.via)
		}

		last[a.path] = a.at
		if a.early != nil {
			early[a.path] = append(early[a.path], a)
		}
	}

	offset, found := int64(0), false
	for path, t := range ts {
		if d := last[path] - t; !found || d < offset {
			offset, found = d, true
		}
	}

	for path, t := range ts {
		r := byPath[path]

		r.issued = t + offset
		r.delay = last[path] - r.issued
	}

	for path, as := range early {
		r := byPath[path]
		for _, a := range as {
			r.streams = append(r.streams, a.via+": "+a.early.describe(r.issued))
		}
	}

	slices.SortStableFunc(v.requests, func(a, b *edgeRequest) int {
		return int(a.issued - b.issued)
	})

	return v
}

// splitT is path without its query, and its ?t= (-1 without).
func splitT(path string) (string, int64) {
	p, query, _ := strings.Cut(path, "?")
	q, err := url.ParseQuery(query)

	if err != nil || q.Get("t") == "" {
		return p, -1
	}

	t, err := strconv.ParseInt(q.Get("t"), 10, 64)
	if err != nil {
		return p, -1
	}

	return p, t
}

// trigger is the last request the page made at or before at: of those
// with ?t=, whose issue time is known, or else of the others (the
// navigation), whose arrival stands for it and so comes after the
// requests its response let the page make.
func (v edgeView) trigger(at int64) *edgeRequest {
	var timed, other *edgeRequest
	for _, r := range v.requests {
		switch {
		case r.issued > at+triggerSlack:
		case r.delay >= 0:
			timed = r
		default:
			other = r
		}
	}

	if timed != nil {
		return timed
	}

	return other
}

// outcome is what the server sent a QUIC attempt: "version negotiation",
// "handshake" (it got as far as the server's Handshake packets),
// "initial" or "nothing".
func outcome(c *quictrace.Conn) string {
	out := "nothing"
	for _, d := range c.Server {
		for _, p := range d.Packets {
			switch p.Type {
			case quictrace.PacketVN:
				return "version negotiation"
			case quictrace.PacketHandshake, quictrace.Packet1RTT:
				out = "handshake"
			case quictrace.PacketInitial:
				if out == "nothing" {
					out = "initial"
				}
			}
		}
	}

	return out
}

// edgeFeatures are what compareEdge compares: categories and counts
// (features), times in ms (ms) and, for a time from when the page made a
// request (a request's delay, a connection's start after its trigger),
// how long this process was stalled within it (stalled: stalledIn).
func edgeFeatures(page *capture.Page) (f features, ms, stalled map[string]int) {
	v := viewEdge(page)

	f = newFeatures()
	ms, stalled = map[string]int{}, map[string]int{}

	for _, r := range v.requests {
		if r.label == "/" || r.label == "/done" {
			continue
		}

		f.cat["request "+r.label+" sent"] = strings.Join(r.sent, ", ")
		if len(r.streams) > 0 {
			f.cat["request "+r.label+" streams"] = strings.Join(r.streams, "; ")
		}

		if r.delay >= 0 {
			k := "request " + r.label + " delay"

			ms[k], stalled[k] = int(r.delay), stalledIn(page, r.issued, r.delay)
		}
	}

	// The TCP connection that carried the page itself has no trigger: it
	// is the page's start.
	tcpTrigger := map[*h2trace.Trace]*edgeRequest{}
	for _, traces := range v.tcp {
		for _, tr := range traces {
			if !slices.ContainsFunc(tr.Requests, func(r h2trace.Request) bool {
				return r.Path == "/"
			}) {
				tcpTrigger[tr] = v.trigger(tr.Millis)
			}
		}
	}

	quicTrigger := map[*quictrace.Conn]*edgeRequest{}
	for host, conns := range v.quic {
		for i, c := range conns {
			k := fmt.Sprintf("QUIC %s #%d", host, i+1)
			version := c.Version

			if c.NegotiatedVersion != "" {
				version += " to " + c.NegotiatedVersion
			}

			f.cat[k] = fmt.Sprintf("resumed %v, version %s, server sent %s", c.Resumed, version, outcome(c))
			if c.Close != nil {
				f.cat[k+" closed by"] = c.Close.By
			}

			if tr := v.trigger(c.Millis); tr != nil {
				quicTrigger[c] = tr
				f.cat[k+" trigger"] = tr.label
				ms[k+" after its trigger"], stalled[k+" after its trigger"] = int(c.Millis-tr.issued), stalledIn(page, tr.issued, c.Millis-tr.issued)
			}

			if o := outcome(c); o != "handshake" && len(c.Datagrams) > 0 {
				f.num[k+" datagrams"] = len(c.Datagrams)
				f.cat[k+" Initial packet number steps"] = initialSteps(c)
				ms[k+" last datagram"] = int(c.Datagrams[len(c.Datagrams)-1].Micros / 1000)
			}

			if m, ok := movePath(c); ok {
				f.cat[k+" new path"] = "yes"
				f.num[k+" probes before new path"] = m.probes
				f.cat[k+" probe packet number steps"] = m.steps
			}
		}
	}

	// The STREAMS_BLOCKED frames sent to each host, over all its
	// connections (a browser's connection may be retired before use):
	// those sent in a packet without a request's STREAM frame (a request
	// made at the limit), and those sent with a stream's
	// (streamsBlockedWith: a waiting request's, once the limit was
	// raised).
	for host, conns := range v.quic {
		var alone, with []string
		for _, c := range conns {
			for _, d := range c.Datagrams {
				for _, pk := range d.Packets {
					for i, fr := range pk.Frames {
						if fr.Type != quictrace.FrameStreamsBlocked {
							continue
						}

						if w, ok := streamsBlockedWith(pk.Frames, i); ok {
							with = append(with, w)
						} else {
							alone = append(alone, streamsBlockedLimit(fr))
						}
					}
				}
			}
		}

		k := "QUIC " + host + " STREAMS_BLOCKED"
		if len(alone) > 0 {
			f.num[k+" alone"] = len(alone)
			f.cat[k+" alone limits"] = strings.Join(alone, " ")
		}

		if len(with) > 0 {
			f.cat[k+" with streams"] = strings.Join(with, ", ")
		}
	}

	for host, traces := range v.tcp {
		for j, tr := range traces {
			k := fmt.Sprintf("TCP %s #%d", host, j+1)
			if r := tcpTrigger[tr]; r != nil {
				f.cat[k+" trigger"] = r.label
				ms[k+" after its trigger"], stalled[k+" after its trigger"] = int(tr.Millis-r.issued), stalledIn(page, r.issued, tr.Millis-r.issued)
			}

			// A race: the request that started a QUIC attempt started
			// this connection too. The gap is signed (negative where the
			// TCP connection came first): the two start side by side, so
			// which arrives first by a few milliseconds is the client's
			// scheduling (Chrome's attempt leads its TCP connection by 2–4
			// ms; mimicry's hello is built while its TCP connects, and a
			// loaded machine can put either first), not a rule; the
			// order's weight is in the gap, held to the samples' range.
			for i, c := range v.quic[host] {
				if r := tcpTrigger[tr]; r == nil || quicTrigger[c] != r {
					continue
				}

				if d := tr.Millis - c.Millis; d >= -raceWindow && d <= raceWindow {
					ms[fmt.Sprintf("%s after QUIC #%d", k, i+1)] = int(d)
				}
			}

			goaway := "no"
			for _, fr := range tr.Frames {
				if fr.Type == "GOAWAY" {
					goaway = fr.ErrCode
				}
			}

			f.cat[k+" goaway"] = goaway
		}
	}

	for host, n := range v.declined {
		f.num["TCP "+host+" declined"] = n
	}

	return f, ms, stalled
}

// streamsBlockedLimit is a STREAMS_BLOCKED frame's type and limit: "bidi 4"
// (frame type 0x16) or "uni 4" (0x17).
func streamsBlockedLimit(fr quictrace.Frame) string {
	kind := "bidi"
	if fr.Code == 0x17 {
		kind = "uni"
	}

	return fmt.Sprintf("%s %d", kind, fr.Max)
}

// streamsBlockedWith describes the STREAMS_BLOCKED frames[i] in a packet
// carrying a request's STREAM frame, by its limit and where it sits:
// "bidi 6 after stream 5" (right after the STREAM frame of the stream that
// took the limit up, the last before it), "bidi 6 before stream 5" (the
// first after it), else "bidi 6 with streams". False in a packet with no
// request's STREAM frame.
func streamsBlockedWith(frames []quictrace.Frame, i int) (string, bool) {
	fr := frames[i]
	l := streamsBlockedLimit(fr)

	var prev, next *uint64

	for j, f := range frames {
		if f.Type != quictrace.FrameStream || f.Stream == nil || *f.Stream%4 != 0 {
			continue
		}

		switch {
		case j < i:
			prev = f.Stream
		case next == nil:
			next = f.Stream
		}
	}

	switch {
	case prev == nil && next == nil:
		return "", false
	case fr.Max > 0 && prev != nil && *prev/4 == fr.Max-1:
		return fmt.Sprintf("%s after stream %d", l, fr.Max-1), true
	case fr.Max > 0 && next != nil && *next/4 == fr.Max-1:
		return fmt.Sprintf("%s before stream %d", l, fr.Max-1), true
	}

	return l + " with streams", true
}

// stalledIn is how long this process was stalled (stalls) within the d ms
// of page's clock starting at from; 0 for a capture read from a file (the
// browser's), which has no Start.
func stalledIn(page *capture.Page, from, d int64) int {
	if page.Start.IsZero() || d <= 0 {
		return 0
	}

	at := page.Start.Add(time.Duration(from) * time.Millisecond)

	return int(stalls.stalled(at, at.Add(time.Duration(d)*time.Millisecond)).Milliseconds())
}

// pathMove is how a QUIC connection moved to a new client address, in µs
// from the last request's first send: when, its probes on the old path and
// their packet-number steps (Chrome skips one), and its PTO as the probes
// show it (probePTO) and as RFC 9002 computes it from the trace (rttPTO).
type pathMove struct {
	after    int64
	probes   int
	steps    string
	probePTO float64
	rttPTO   float64
}

// initialSteps is how far each of c's Initial packets' numbers is from
// the one before: Chrome skips one before each probe (1 2 2 2 1, the last
// its close), the others none.
func initialSteps(c *quictrace.Conn) string {
	var (
		steps []string
		prev  uint64
	)

	first := true

	for _, d := range c.Datagrams {
		for _, p := range d.Packets {
			if p.Type != quictrace.PacketInitial {
				continue
			}

			if !first {
				steps = append(steps, strconv.FormatUint(p.PN-prev, 10))
			}

			prev, first = p.PN, false
		}
	}

	return strings.Join(steps, " ")
}

// movePath is c's move to a new path; false if it never moved, or moved
// with no request before.
func movePath(c *quictrace.Conn) (pathMove, bool) {
	moved := slices.IndexFunc(c.Datagrams, func(d quictrace.Datagram) bool {
		return d.Path != 0
	})
	if moved < 0 {
		return pathMove{}, false
	}

	// The last request's first send: the last client-initiated
	// bidirectional stream's first datagram.
	sent, seen := -1, map[uint64]bool{}
	for i, d := range c.Datagrams[:moved] {
		for _, p := range d.Packets {
			for _, f := range p.Frames {
				if f.Type == quictrace.FrameStream && f.Stream != nil && *f.Stream%4 == 0 && !seen[*f.Stream] {
					seen[*f.Stream] = true
					sent = i
				}
			}
		}
	}

	if sent < 0 {
		return pathMove{}, false
	}

	start := c.Datagrams[sent].Micros
	m := pathMove{
		after:  c.Datagrams[moved].Micros - start,
		rttPTO: tracePTO(c, start),
	}
	last := start

	var (
		prev  uint64
		steps []string
	)

	for i, d := range c.Datagrams[sent:moved] {
		probe := i > 0 && slices.ContainsFunc(d.Packets, func(p quictrace.Packet) bool {
			return slices.ContainsFunc(p.Frames, func(f quictrace.Frame) bool {
				return f.Type != quictrace.FrameAck && f.Type != quictrace.FramePadding && f.Type != quictrace.FrameConnectionClose
			})
		})
		for _, p := range d.Packets {
			if p.Type != quictrace.Packet1RTT {
				continue
			}

			if probe {
				steps = append(steps, strconv.FormatUint(p.PN-prev, 10))
				probe = false
				m.probes++

				last = d.Micros
			}

			prev = p.PN
		}
	}

	m.steps = strings.Join(steps, " ")
	// The k-th probe goes 1 + 2 + … + 2^(k-1) = 2^k - 1 PTOs after the
	// request (RFC 9002, section 6.2.1: the timer doubles on each); each
	// timer's lateness is spread over all of them.
	if m.probes > 0 {
		m.probePTO = float64(last-start) / float64(int(1)<<m.probes-1)
	}

	return m, true
}

// tracePTO is the least PTO (µs, no back-off) RFC 9002 allows c's client
// at before (sections 5.3, 6.2.1), from the trace's RTT samples. The trace
// only shows server packets to client ACKs, so the samples are the reverse
// round trip, without RFC 9002's guards (which only lengthen a sample). It is
// a floor: Chrome's probes show 31–34 ms against 27–28 here.
func tracePTO(c *quictrace.Conn, before int64) float64 {
	exponent := transportParameter(c.TransportParameters, tpAckDelayExponent, 3)
	sent := map[string]int64{}

	for _, d := range c.Server {
		for _, p := range d.Packets {
			sent[pnSpace(p.Type)+" "+strconv.FormatUint(p.PN, 10)] = d.Micros
		}
	}

	largest := map[string]uint64{}

	var smoothed, variation float64

	sampled := false

	for _, d := range c.Datagrams {
		if d.Micros >= before {
			break
		}

		for _, p := range d.Packets {
			space := pnSpace(p.Type)
			for _, f := range p.Frames {
				if l, ok := largest[space]; f.Type != quictrace.FrameAck || ok && f.Largest <= l {
					continue
				}

				largest[space] = f.Largest

				at, ok := sent[space+" "+strconv.FormatUint(f.Largest, 10)]

				if !ok {
					continue
				}

				latest := float64(d.Micros - at)
				if !sampled {
					smoothed, variation, sampled = latest, latest/2, true
					continue
				}

				adjusted := latest
				if delay := float64(f.Delay << exponent); delay < latest {
					adjusted -= delay
				}

				variation = 0.75*variation + 0.25*math.Abs(smoothed-adjusted)
				smoothed = 0.875*smoothed + 0.125*adjusted
			}
		}
	}

	if !sampled {
		// RFC 9002's initial RTT, 333 ms.
		smoothed, variation = 333000, 333000/2
	}

	maxAckDelay := float64(transportParameter(c.ServerTransportParameters, tpMaxAckDelay, 25) * 1000)

	return smoothed + max(4*variation, 1000) + maxAckDelay
}

// pnSpace is the packet number space of a packet type.
func pnSpace(typ string) string {
	if typ == quictrace.PacketInitial || typ == quictrace.PacketHandshake {
		return typ
	}

	return "application"
}

// checkPathMoves holds every connection of page that moved path to
// quic.loss.pathDegrading.afterPtos: the move comes that many of its own
// PTOs after its last request, within half a PTO (h3gen's rounding). The PTO
// is measured from its probes, floored by tracePTO, so the rule holds under
// load too.
func checkPathMoves(t *testing.T, p *profile.Spec, who string, page *capture.Page) {
	t.Helper()

	for i, c := range page.QUIC {
		m, ok := movePath(c)
		if !ok {
			continue
		}

		conn := fmt.Sprintf("%s: QUIC %s (connection %d)", who, c.Host, i+1)
		d := p.QUIC.Loss.PathDegrading

		switch {
		case d == nil || d.Action != profile.PathMigrate:
			t.Errorf("%s moved to a new path; the profile never migrates", conn)

			continue

		case m.probes == 0:
			t.Errorf("%s moved to a new path %.1f ms after its last request without a probe first", conn, float64(m.after)/1000)

			continue
		}

		ratio := float64(m.after) / m.probePTO
		report := fmt.Sprintf("%s moved to a new path %.1f ms after its last request: %.2f of its %.1f ms PTOs (%d probes; RFC 9002 on its trace's RTT samples: at least %.1f ms)", conn, float64(m.after)/1000, ratio, m.probePTO/1000, m.probes, m.rttPTO/1000)

		switch {
		case math.Abs(ratio-float64(d.AfterPTOs)) > 0.5:
			t.Errorf("%s; want %d", report, d.AfterPTOs)
		case m.probePTO < m.rttPTO-1000:
			t.Errorf("%s; its probes came sooner than its RTT allows", report)
		default:
			t.Log(report)
		}
	}
}

// compareEdge holds mimicry's page to every browser sample, the ClientHello
// and transport parameters of a connection retried after Version
// Negotiation to the browser's retries (Firefox's: another hello than its
// first flight's, tls.quic.versionNegotiation), each resumed connection's
// ClientHello to the browser's resumed ones (tls.quic.earlyDataBefore),
// mimicry's and every sample's moves to a new path to the profile's rule
// (checkPathMoves), and mimicry's held requests to requestsAfterConfirmed
// (checkHeldConfirmed). Times are not compared against samples captured
// over a network (Safari on an iPhone, its pages' driver set): the
// server's clock puts the phone's packets a path's delay late, which h3gen
// does not measure either (its ranges stay copied, decision 0051).
func compareEdge(t *testing.T, p *profile.Spec, got *capture.Page, want []*capture.Page, skip func(string) bool) {
	t.Helper()

	gf, gms, stalled := edgeFeatures(got)
	refs := make([]features, 0, len(want))
	refMS := make([]map[string]int, 0, len(want))

	for _, w := range want {
		f, ms, _ := edgeFeatures(w)

		refs = append(refs, f)
		refMS = append(refMS, ms)
	}

	compareFeatures(t, "edge", gf, refs, skip)

	if slices.ContainsFunc(want, func(w *capture.Page) bool {
		return w.Driver != ""
	}) {
		t.Logf("times not compared: the browser's samples were captured over a network (driver %s)", want[0].Driver)
	} else {
		compareTimes(t, gms, stalled, refMS, skip)
	}

	checkPathMoves(t, p, "mimicry", got)

	for i, w := range want {
		checkPathMoves(t, p, fmt.Sprintf("browser sample %d", i+1), w)
	}

	retries := make([]*quictrace.Conn, 0, len(want))
	for _, w := range want {
		retries = append(retries, vnRetries(w)...)
	}

	for _, c := range vnRetries(got) {
		t.Run("retry after Version Negotiation", func(t *testing.T) {
			if len(retries) == 0 {
				t.Fatal("the browser retried no connection after Version Negotiation")
			}

			compareHello(t, p, c, retries)
			compareFeatures(t, "transport parameters", paramFeatures(c), mapConns(retries, paramFeatures), skip)
		})
	}

	if len(retries) > 0 && len(vnRetries(got)) == 0 {
		t.Error("no connection retried after Version Negotiation; the browser retried")
	}

	// A resumed connection's hello (Safari resumes only on these pages):
	// each of mimicry's matches some resumed one of the browser's.
	resumed := make([]*quictrace.Conn, 0, len(want))
	for _, w := range want {
		resumed = append(resumed, resumedConns(w)...)
	}

	for _, c := range resumedConns(got) {
		t.Run("resumed hello", func(t *testing.T) {
			if len(resumed) == 0 {
				t.Fatal("the browser resumed no connection")
			}

			compareHello(t, p, c, resumed)
		})
	}

	checkHeldConfirmed(t, p, got)
}

// checkHeldConfirmed holds mimicry's requests held out of 0-RTT to
// http3.requestsAfterConfirmed: each one's stream reaches the server after
// the server's answer to the client's Finished (its first datagram after
// the Finished arrived: ACK and HANDSHAKE_DONE), as h3gen reads the
// browser's.
func checkHeldConfirmed(t *testing.T, p *profile.Spec, got *capture.Page) {
	t.Helper()

	h := p.HTTP3
	if !h.RequestsAfterConfirmed || h.EarlyData == nil {
		return
	}

	for _, c := range got.QUIC {
		if !c.EarlyData || c.H3 == nil {
			continue
		}

		finished, _ := c.FinishedDatagram()
		starts := c.StreamStarts()

		for _, r := range c.H3.Requests {
			s, ok := starts[r.Stream]
			if !ok || s.PacketType != quictrace.Packet1RTT || slices.Contains(h.EarlyData.Methods, r.Method) {
				continue
			}

			if !afterConfirmation(c, finished, s.Datagram) {
				t.Errorf("%s on QUIC %s left before the handshake was confirmed (requestsAfterConfirmed)", r.Path, c.Host)
			}
		}
	}
}

// afterConfirmation reports whether c's client datagram sent arrived
// after the server answered its Finished (datagram finished): after the
// server's first datagram sent once the Finished had arrived.
func afterConfirmation(c *quictrace.Conn, finished, sent int) bool {
	if finished < 0 || sent >= len(c.Datagrams) {
		return false
	}

	at := c.Datagrams[finished].Micros
	for _, d := range c.Server {
		if d.Micros > at {
			return c.Datagrams[sent].Micros > d.Micros
		}
	}

	return false
}

// dialClock is the browsers' dial delays' resolution, as h3gen reads them
// (dialClockMillis): the ?t= clock's rounding. dialWindow bounds how long
// after its trigger a dial was the trigger's (h3gen's dialWindowMillis);
// a later one followed something else (a reconnection).
const (
	dialClock  = 3
	dialWindow = 50
)

// checkDialDelays holds mimicry's QUIC dials to the profile's dial delays
// (http3.discovery.dialDelayMs, resumedDialDelayMs), each measured from
// the request to its host that triggered it, net of this process's
// stalls where an upper bound is held. With Config.BrowserDialDelay
// (browser): none starts sooner than the samples' least delay less the
// issue time's rounding (triggerSlack); their median, given 5, no sooner
// than the samples' median less triggerSlack; and some starts more than
// dialClock after its trigger, once the page has enough dials that none
// doing so would be a 1-in-10,000 draw from the samples. By default: the
// dials start at once, their median no more than dialClock +
// triggerSlack after the trigger, which tells a delay whose samples'
// median lies beyond that (Safari's) from none. The race detector slows
// mimicry's own dial setup (its hello and keys) by 5–12 ms, about the
// browsers' delay, so under it the default goes unchecked here; the
// modes' difference (dialRecord, from the delays returned) holds both
// with or without it.
func checkDialDelays(t *testing.T, p *profile.Spec, got *capture.Page, browser bool) [2][]int64 {
	t.Helper()

	d := p.HTTP3.Discovery
	v := viewEdge(got)
	samples := [2][]int{d.DialDelayMillis, d.ResumedDialDelayMillis}

	// Per kind (fresh, resumed): each dial's delay, raw and net of stalls.
	var raw, net [2][]int64

	for host, conns := range v.quic {
		for i, c := range conns {
			kind := 0
			if c.Resumed {
				kind = 1
			}

			s := samples[kind]

			tr := v.trigger(c.Millis)
			if len(s) == 0 || tr == nil || tr.delay < 0 || !slices.ContainsFunc(tr.sent, func(s string) bool {
				return strings.Contains(s, " "+host+" ")
			}) || slices.ContainsFunc(v.tcp[host], func(tcp *h2trace.Trace) bool {
				// TCP dialled first: Discovery.RaceAfterGoAway's dial, on
				// the TCP response (Firefox), as h3gen leaves it out.
				return tcp.Millis >= tr.issued && tcp.Millis < c.Millis
			}) {
				continue
			}

			delay := c.Millis - tr.issued
			if browser && delay < int64(s[0]-triggerSlack) {
				t.Errorf("QUIC %s #%d started %d ms after %s, before the profile's least dial delay (%d ms)", host, i+1, delay, tr.label, s[0])
			}

			if delay <= dialWindow {
				raw[kind] = append(raw[kind], delay)
				net[kind] = append(net[kind], delay-int64(stalledIn(got, tr.issued, delay)))
			}
		}
	}

	for kind, s := range samples {
		ds := net[kind]
		if len(s) == 0 || len(ds) == 0 {
			continue
		}

		slices.Sort(ds)

		if !browser {
			if m := ds[len(ds)/2]; !raceEnabled && m > dialClock+triggerSlack {
				t.Errorf("dials (resumed %v) held back by default: their median started %d ms after its trigger", kind == 1, m)
			}

			continue
		}

		// The chance that every dial drew no more than dialClock.
		under := float64(len(s)-len(slices.DeleteFunc(slices.Clone(s), func(v int) bool {
			return v <= dialClock
		}))) / float64(len(s))
		if ds[len(ds)-1] <= dialClock && math.Pow(under, float64(len(ds))) < 1e-4 {
			t.Errorf("none of %d dials (resumed %v) started more than %d ms after its trigger, net of stalls; the profile's delays are %v ms", len(ds), kind == 1, dialClock, s)
		}

		// A stall only lengthens a delay: the median, stalls in, holds
		// to the samples' less the issue time's rounding.
		rs := slices.Sorted(slices.Values(raw[kind]))
		if m := rs[len(rs)/2]; len(rs) >= 5 && m < int64(s[len(s)/2]-triggerSlack) {
			t.Errorf("the median of %d dials (resumed %v) started %d ms after its trigger, before the samples' median of %d ms", len(rs), kind == 1, m, s[len(s)/2])
		}
	}

	return raw
}

// dialRecord gathers each profile's dial delays (checkDialDelays), per
// mode and kind, across the edge pages, to compare the modes once every
// page has run (compare): mimicry's own dial setup takes about as long as
// the browsers' delay (more under the race detector), which the
// difference of the two modes' medians cancels.
type dialRecord struct {
	mu sync.Mutex
	// by is per profile name: samples, then each mode's (fast, browser)
	// delays by kind (fresh, resumed).
	by map[string]*profileDials
}

type profileDials struct {
	samples [2][]int
	delays  [2][2][]int64
}

// add records name's page's delays in its mode.
func (r *dialRecord) add(name string, p *profile.Spec, browser bool, raw [2][]int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.by == nil {
		r.by = map[string]*profileDials{}
	}

	pd := r.by[name]
	if pd == nil {
		d := p.HTTP3.Discovery

		pd = &profileDials{
			samples: [2][]int{d.DialDelayMillis, d.ResumedDialDelayMillis},
		}
		r.by[name] = pd
	}

	mode := 0
	if browser {
		mode = 1
	}

	for kind := range raw {
		pd.delays[mode][kind] = append(pd.delays[mode][kind], raw[kind]...)
	}
}

// compare holds, per profile and kind with samples and 8 dials or more in
// each mode, the browser mode's median delay above the fast one's by at
// least half the samples' median: the delay is applied with
// Config.BrowserDialDelay and not by default.
func (r *dialRecord) compare(t *testing.T) {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	median := func(vs []int64) int64 {
		vs = slices.Sorted(slices.Values(vs))
		return vs[len(vs)/2]
	}

	for _, name := range slices.Sorted(maps.Keys(r.by)) {
		pd := r.by[name]
		for kind, s := range pd.samples {
			fast, browser := pd.delays[0][kind], pd.delays[1][kind]
			if len(s) == 0 || len(fast) < 8 || len(browser) < 8 {
				continue
			}

			if diff, want := median(browser)-median(fast), int64(s[len(s)/2]/2); diff < want {
				t.Errorf("%s: the dials (resumed %v) start %d ms later with BrowserDialDelay than without (medians of %d and %d dials); the samples' median is %d ms, so at least %d", name, kind == 1, diff, len(browser), len(fast), s[len(s)/2], want)
			}
		}
	}
}

// resumedConns are page's QUIC connections that offered a session.
func resumedConns(page *capture.Page) []*quictrace.Conn {
	var out []*quictrace.Conn
	for _, c := range page.QUIC {
		if c.Resumed {
			out = append(out, c)
		}
	}

	return out
}

// compareBurstWaits holds the h3-streams-blocked page's bursts to the
// browser's without their order: each burst's delays (from the request
// made to its arrival) sorted, the k-th shortest of mimicry's to the range
// of the samples' k-th shortest. Which request of a burst waits is the
// replay's scheduling (h3EdgeSchedulingGaps); how many wait and for how
// long is the server's stream limit against the client's queueing. The
// samples put two at 0–3 ms, two at 284–330 ms and two at 572–632 ms in
// every burst.
//
// A wait may lie edgeSlack outside the samples' range (net of this
// process's stalls, as compareTimes), and later by burstACKSlack per
// response it waited out: quic-go raises the limit once the client has
// acknowledged the responses, which a client holds back up to its ACK
// delay when both arrive in one packet (Chrome 26–30 ms; whether they do is
// the server's timing). A client opening all six at once, or waiting a
// MAX_STREAMS more, is 300 ms out.
func compareBurstWaits(t *testing.T, got *capture.Page, want []*capture.Page, skip func(string) bool) {
	t.Helper()

	bursts := streamsBlockedBursts()
	gms, stalled := burstWaits(got, bursts)
	refs := make([]map[string]int, 0, len(want))

	for _, w := range want {
		ms, _ := burstWaits(w, bursts)

		refs = append(refs, ms)
	}

	wave := int(server.H3BlockedDelay.Milliseconds())
	for _, k := range slices.Sorted(maps.Keys(refs[0])) {
		label := "edge " + k + " ms"
		lo, hi := refs[0][k], refs[0][k]

		for _, r := range refs {
			v, ok := r[k]
			if !ok {
				t.Fatalf("%s: a browser sample has none", label)
			}

			lo, hi = min(lo, v), max(hi, v)
		}

		g, ok := gms[k]
		slack := edgeSlack(hi)
		late := hi + slack + (lo+wave/2)/wave*burstACKSlack

		switch {
		case skip(label):
		case !ok:
			t.Errorf("%s: none, the browser's %d..%d", label, lo, hi)
		case g < lo-slack || g > late && g-stalled[k] > late:
			t.Errorf("%s: %d (%d of it with the process stalled), the browser's %d..%d (from %d to %d)", label, g, stalled[k], lo, hi, lo-slack, late)
		default:
			t.Logf("%s: %d (browser %d..%d)", label, g, lo, hi)
		}
	}
}

// burstACKSlack is how much later than the browser's a wait in an
// h3-streams-blocked burst may come per response it waited out: the most a
// client held back its ACK of the responses that freed a stream (Chrome,
// 26–30 ms).
const burstACKSlack = 30

// afterRTTRecord holds every profile with quic.ack.afterRtt to it, over
// all its edge pages together: the lone packets the server sent
// quictrace.LateMicros or more after mimicry's previous ACK reached it are
// acknowledged at once (quictrace.LoneACKs), as Firefox acknowledged every
// one in the edge captures (70 of 70 on macOS, 76 of 76 on Windows;
// h3gen). A page holds only 1–9 such packets, so the count is over the
// profile's pages, and at least one must occur, or the rule went
// unexercised. A stall under -race can lift mimicry's smoothed RTT past
// the gap and hold one (1 of 2 once in 6 runs), so at most 5% (at least
// one) may wait; without the rule none goes at once. The ones sent sooner
// are not compared: whether they wait depends on the RTT.
type afterRTTRecord struct {
	mu    sync.Mutex
	byID  map[string]*quictrace.LoneACKs
	pages map[string]int
}

// add counts got's lone packets for name, when its profile has the rule.
func (r *afterRTTRecord) add(name string, p *profile.Spec, got *capture.Page) {
	if !p.QUIC.Ack.AfterRTT {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.byID == nil {
		r.byID = map[string]*quictrace.LoneACKs{}
		r.pages = map[string]int{}
	}

	r.pages[name]++

	n := r.byID[name]
	if n == nil {
		n = &quictrace.LoneACKs{}
		r.byID[name] = n
	}

	for _, c := range got.QUIC {
		n.Add(c)
	}
}

// compare reports each profile's count against the browser's.
func (r *afterRTTRecord) compare(t *testing.T) {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	for name, n := range r.byID {
		held := n.Late - n.LateAtOnce
		allowed := max(1, n.Late/20)

		switch {
		case n.Late == 0 && r.pages[name] < len(h3EdgeScenarios)*2:
			t.Logf("%s: quic.ack.afterRtt: no lone packet after a quiet spell in the %d edge pages run (of %d); not compared", name, r.pages[name], len(h3EdgeScenarios)*2)
		case n.Late == 0:
			t.Errorf("%s: quic.ack.afterRtt: no lone packet after a quiet spell in any edge page; the rule went unexercised", name)
		case held > allowed:
			t.Errorf("%s: quic.ack.afterRtt: %d of %d lone packets after a quiet spell acknowledged at once (at most %d may wait), the browser's every one", name, n.LateAtOnce, n.Late, allowed)
		default:
			t.Logf("%s: quic.ack.afterRtt: %d of %d lone packets after a quiet spell acknowledged at once", name, n.LateAtOnce, n.Late)
		}
	}
}

// compareStreamsBlockedWith holds the STREAMS_BLOCKED that mimicry's
// waiting requests sent with their streams to the samples' as sets, which
// also hold where the samples differ (compareFeatures compares the list
// whole where they agree): each one every sample sent last in its packet
// mimicry sends too ("with streams", the feature's own label), and each of
// mimicry's some sample sent, at most as often ("beyond the browser's",
// kept when the first is pinned). A browser's waiting streams leave in one
// packet per raise, ending with the frame for the limit the raise ended
// at. Chrome sent that one after the last stream of each raise (6, 8, 12,
// 14) in every sample, and one after the first of the two in 9 of 12
// raises (5, 7, 11, 13: the limit of the raise's first MAX_STREAMS
// frame); mimicry's first comes as its waiting request opens its stream
// before or after its loop has read the second frame
// (profile.StreamsBlockedRaiseAlways).
func compareStreamsBlockedWith(t *testing.T, got *capture.Page, want []*capture.Page, skip func(string) bool) {
	t.Helper()

	g, _ := streamsBlockedWithPackets(got)
	every, most := map[string]map[string]bool{}, map[string]map[string]int{}

	for i, w := range want {
		all, last := streamsBlockedWithPackets(w)
		for host, c := range all {
			if most[host] == nil {
				most[host] = map[string]int{}
			}

			for s, n := range c {
				most[host][s] = max(most[host][s], n)
			}
		}

		for host := range every {
			maps.DeleteFunc(every[host], func(s string, _ bool) bool {
				return last[host][s] == 0
			})
		}

		if i == 0 {
			for host, c := range last {
				every[host] = map[string]bool{}
				for s := range c {
					every[host][s] = true
				}
			}
		}
	}

	hosts := slices.Sorted(maps.Keys(g))
	for h := range most {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}

	for _, host := range hosts {
		var missing, beyond []string

		for _, s := range slices.Sorted(maps.Keys(every[host])) {
			if g[host][s] == 0 {
				missing = append(missing, fmt.Sprintf("no %s, which every browser sample sent last in its packet", s))
			}
		}

		for _, s := range slices.Sorted(maps.Keys(g[host])) {
			if g[host][s] > most[host][s] {
				beyond = append(beyond, fmt.Sprintf("%s %d times, the browser's at most %d", s, g[host][s], most[host][s]))
			}
		}

		label := "edge QUIC " + host + " STREAMS_BLOCKED with streams"
		mine := strings.Join(slices.Sorted(maps.Keys(g[host])), ", ")

		for l, diffs := range map[string][]string{
			label:                            missing,
			label + ", beyond the browser's": beyond,
		} {
			if len(diffs) > 0 && !skip(l) {
				t.Errorf("%s: %s (mimicry's: %s)", l, strings.Join(diffs, "; "), mine)
			}
		}

		t.Logf("%s: %s", label, mine)
	}
}

// streamsBlockedWithPackets counts the STREAMS_BLOCKED that page's
// requests sent with their streams (streamsBlockedWith), by host: all of
// them, and those last in their packet.
func streamsBlockedWithPackets(page *capture.Page) (all, last map[string]map[string]int) {
	all, last = map[string]map[string]int{}, map[string]map[string]int{}

	for host, conns := range viewEdge(page).quic {
		for _, c := range conns {
			for _, d := range c.Datagrams {
				for _, pk := range d.Packets {
					end := ""

					for i, fr := range pk.Frames {
						if fr.Type != quictrace.FrameStreamsBlocked {
							continue
						}

						if w, ok := streamsBlockedWith(pk.Frames, i); ok {
							if all[host] == nil {
								all[host], last[host] = map[string]int{}, map[string]int{}
							}

							all[host][w]++

							end = w
						}
					}

					if end != "" {
						last[host][end]++
					}
				}
			}
		}
	}

	return all, last
}

// streamsBlockedBursts are the h3-streams-blocked page's bursts, each its
// requests' paths: a step whose response is held back starts one when it
// has a pause, and the held-back steps after it without one join it.
func streamsBlockedBursts() map[string][]string {
	bursts := map[string][]string{}

	var name string
	for _, st := range server.H3StreamsBlockedSteps() {
		if st.Delay == 0 {
			continue
		}

		if st.PauseMillis > 0 {
			name = fmt.Sprintf("burst %d", len(bursts)+1)
		}

		bursts[name] = append(bursts[name], st.Path())
	}

	return bursts
}

// burstWaits are page's delays of each burst's requests, sorted: "burst 1
// wait 1" the shortest. stalled is how long this process was stalled
// within each (0 for the browser's).
func burstWaits(page *capture.Page, bursts map[string][]string) (ms, stalled map[string]int) {
	_, delays, stalls := edgeFeatures(page)

	ms, stalled = map[string]int{}, map[string]int{}

	type wait struct {
		ms, stalled int
	}

	for name, paths := range bursts {
		var waits []wait
		for _, path := range paths {
			k := "request " + path + " delay"
			if d, ok := delays[k]; ok {
				waits = append(waits, wait{
					ms:      d,
					stalled: stalls[k],
				})
			}
		}

		slices.SortFunc(waits, func(a, b wait) int {
			return a.ms - b.ms
		})

		for i, w := range waits {
			k := fmt.Sprintf("%s wait %d", name, i+1)

			ms[k], stalled[k] = w.ms, w.stalled
		}
	}

	return ms, stalled
}

// vnRetries are page's QUIC connections that follow one the server
// answered with Version Negotiation, to the same host.
func vnRetries(page *capture.Page) []*quictrace.Conn {
	var out []*quictrace.Conn
	for i, c := range page.QUIC {
		if i > 0 && page.QUIC[i-1].Host == c.Host && outcome(page.QUIC[i-1]) == "version negotiation" && c.Version != page.QUIC[i-1].Version {
			out = append(out, c)
		}
	}

	return out
}

// compareTimes holds each of got's times to the refs' range widened by
// edgeSlack: present where every ref has it, absent where none does. A
// stall only lengthens a time, so one above the range passes if it is in
// range less the time this process was stalled within it (stalled), and
// one below never does.
func compareTimes(t *testing.T, got, stalled map[string]int, refs []map[string]int, skip func(string) bool) {
	t.Helper()

	keys := map[string]bool{}
	for k := range got {
		keys[k] = true
	}

	for _, r := range refs {
		for k := range r {
			keys[k] = true
		}
	}

	for _, k := range slices.Sorted(maps.Keys(keys)) {
		lo, hi, in := 0, 0, 0
		for _, r := range refs {
			v, ok := r[k]
			if !ok {
				continue
			}

			if in == 0 || v < lo {
				lo = v
			}

			if in == 0 || v > hi {
				hi = v
			}

			in++
		}

		g, ok := got[k]
		slack := edgeSlack(hi)
		label := "edge " + k + " ms"
		under := ok && g < lo-slack
		over := ok && g > hi+slack && g-stalled[k] > hi+slack

		switch {
		case (in == 0 || !ok && in == len(refs) || under || over) && skip(label):
		case in == 0:
			t.Errorf("%s: %d, the browser has none", label, g)
		case !ok && in == len(refs):
			t.Errorf("%s: none, the browser's %d..%d", label, lo, hi)
		case under || over:
			t.Errorf("%s: %d (%d of it with the process stalled), the browser's %d..%d (± %d)", label, g, stalled[k], lo, hi, slack)
		case ok && g > hi+slack:
			t.Logf("%s: %d, %d of it with the process stalled (browser %d..%d)", label, g, stalled[k], lo, hi)
		case ok:
			t.Logf("%s: %d (browser %d..%d)", label, g, lo, hi)
		}
	}
}
