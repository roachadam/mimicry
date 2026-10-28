package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	utls "github.com/roachadam/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/profiles"
)

// quicServer accepts h3 connections on every local address and answers
// nothing; with goAway it sends SETTINGS and GOAWAY on each. It returns its
// port and a channel signalled per accepted connection.
func quicServer(t *testing.T, goAway bool) (uint16, <-chan struct{}) {
	t.Helper()

	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", ":0")
	if err != nil {
		t.Fatal(err)
	}

	cert := serverCert.Load()

	ep, err := quic.NewEndpoint(pc, &quic.Config{
		TLSConfig: &utls.Config{
			Certificates: []utls.Certificate{{
				Certificate: cert.Certificate,
				PrivateKey:  cert.PrivateKey,
			}},
			NextProtos: []string{"h3"},
			MinVersion: utls.VersionTLS13,
		},
	})
	if err != nil {
		_ = pc.Close()

		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_ = ep.Close(ctx)
	})

	accepted := make(chan struct{}, 16)

	go func() {
		for {
			c, err := ep.Accept(context.Background())
			if err != nil {
				return
			}

			accepted <- struct{}{}

			if !goAway {
				continue
			}

			s, err := c.NewSendOnlyStream(context.Background())
			if err != nil {
				continue
			}

			// Stream type control, an empty SETTINGS, GOAWAY with ID 0.
			_, _ = s.Write([]byte{0x00, 0x04, 0x00, 0x07, 0x01, 0x00})
			_ = s.Flush()
		}
	}()

	return ep.LocalAddr().Port(), accepted
}

// answerThenWatchGoAway answers the first request once hold closes,
// failing if the client's GOAWAY comes first (within settle), then closes
// goaway when the client's GOAWAY arrives.
func answerThenWatchGoAway(headers, goaway chan<- struct{}, hold <-chan struct{}, settle time.Duration) func(context.Context, net.Conn) error {
	return func(ctx context.Context, conn net.Conn) error {
		tc, err := tlsServer(ctx, conn)
		if err != nil {
			return err
		}

		if _, err := io.ReadFull(tc, make([]byte, len(http2.ClientPreface))); err != nil {
			return err
		}

		out := make(chan []byte, 64)

		defer close(out)

		go func() {
			for b := range out {
				_, _ = tc.Write(b)
			}
		}()

		fr := http2.NewFramer(queueWriter(out), tc)

		fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)

		if err := fr.WriteSettings(); err != nil {
			return err
		}

		frames := make(chan http2.Frame, 64)

		go func() {
			defer close(frames)

			for {
				f, err := fr.ReadFrame()
				if err != nil {
					return
				}

				frames <- f
			}
		}()

		answered := false
		for f := range frames {
			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					if err := fr.WriteSettingsAck(); err != nil {
						return err
					}
				}

			case *http2.MetaHeadersFrame:
				close(headers)

				if hold != nil {
					<-hold
				}

				// A GOAWAY sent with the request in flight arrives
				// before the answer.
				timer := time.NewTimer(settle)

			settling:
				for {
					select {
					case g, ok := <-frames:
						if !ok {
							break settling
						}

						if _, ok := g.(*http2.GoAwayFrame); ok {
							timer.Stop()

							return errGoAwayBeforeAnswer
						}

					case <-timer.C:
						break settling
					}
				}

				if err := writeOK(fr, f.StreamID); err != nil {
					return err
				}

				answered = true

			case *http2.GoAwayFrame:
				if !answered {
					return errGoAwayBeforeAnswer
				}

				close(goaway)

				return nil
			}
		}

		return nil
	}
}

var errGoAwayBeforeAnswer = errors.New("the client's GOAWAY came before the answer to its request")

// An HTTP/2 connection pooled after the origin's QUIC connection came up
// still gets GOAWAY after its response (Firefox's goawayTcp; h3-broken
// capture). The TCP dial is held until QUIC is pooled.
func TestGoAwayReachesTCPConnectionEstablishedAfterQUIC(t *testing.T) {
	headers, goaway := make(chan struct{}), make(chan struct{})
	n := newFakeNet(t, answerThenWatchGoAway(headers, goaway, nil, 50*time.Millisecond))
	dial, dialing, release := blockingDial(n, nil)
	e := newTestEngine(t, options{
		profile: profiles.Firefox(),
		dial:    dial,
	})
	port, _ := quicServer(t, false)

	const target = "https://localhost/closed1.cs"

	done := make(chan error, 1)

	go func() {
		resp, err := get(t, e, target)
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		done <- err
	}()

	// The request goes on TCP (no Alt-Svc yet) and dials; the origin's
	// QUIC connection comes up meanwhile.
	<-dialing

	r, a := dialQUIC(t, e, target, port)
	if a.conn == nil {
		t.Fatal("the QUIC attempt failed")
	}

	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("request on the TCP connection: %v", err)
		}

	case <-time.After(5 * time.Second):
		// Refused by an early GOAWAY, it went on the QUIC connection,
		// which the server never answers.
		t.Fatal("no response to the request on the TCP connection")
	}

	select {
	case <-headers:
	default:
		t.Fatal("the request did not go on the TCP connection it opened")
	}

	select {
	case <-goaway:
	case <-time.After(5 * time.Second):
		t.Fatal("no GOAWAY on the TCP connection established after the QUIC connection came up")
	}

	if _, ok := e.pool.Shared(r.key); ok {
		t.Fatal("the replaced TCP connection is still pooled")
	}
}

// dialQUIC gives target's origin an alternative on port and dials it
// there, outside any request; it returns the origin's route and the
// attempt, ended.
func dialQUIC(t *testing.T, e *Engine, target string, port uint16) (route, *attempt) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	r, err := e.route(req)
	if err != nil {
		t.Fatal(err)
	}

	q := e.quic
	q.alts.Learn(r.key, r.host, []string{fmt.Sprintf(`h3=":%d"; ma=3600`, port)})
	q.mu.Lock()

	o := e.originState(r)

	o.known = true

	a := e.startAttempt(o, r, dialSpec{
		port: port,
	})
	q.mu.Unlock()
	<-a.done

	return r, a
}

// After the server's GOAWAY, Firefox's next request goes on TCP and the
// resumed QUIC attempt waits for its response however long (raceAfterGoaway;
// h3-broken and h3-discovery captures).
func TestRaceAfterGoAwayWaitsForTheTCPResponse(t *testing.T) {
	headers, goaway, hold := make(chan struct{}), make(chan struct{}), make(chan struct{})

	var releaseHold sync.Once

	answer := func() {
		releaseHold.Do(func() {
			close(hold)
		})
	}
	n := newFakeNet(t, answerThenWatchGoAway(headers, goaway, hold, 50*time.Millisecond))
	// Registered after newFakeNet's, so it runs before that waits for the
	// server.
	t.Cleanup(answer)

	e := newTestEngine(t, options{
		profile: profiles.Firefox(),
		dial:    n.dial,
	})
	port, accepted := quicServer(t, true)

	const target = "https://localhost/closed1.cs"

	_, first := dialQUIC(t, e, target, port)

	if first.conn == nil {
		t.Fatal("the first QUIC attempt failed")
	}

	<-accepted

	for deadline := time.Now().Add(5 * time.Second); !first.conn.GoneAway(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the server's HTTP/3 GOAWAY never arrived")
		}
	}

	done := make(chan error, 1)

	go func() {
		resp, err := get(t, e, target)
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		done <- err
	}()

	<-headers

	select {
	case <-accepted:
		t.Fatal("the racing QUIC connection started before the TCP request's response")
	case <-time.After(10 * time.Duration(e.profile.HTTP3.Discovery.RaceDelayMillis.Max) * time.Millisecond):
	}

	answer()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("request on the TCP connection: %v", err)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("no response to the request on the TCP connection")
	}

	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("no QUIC connection followed the TCP request")
	}

	select {
	case <-goaway:
	case <-time.After(5 * time.Second):
		t.Fatal("no GOAWAY on the race's TCP connection")
	}
}
