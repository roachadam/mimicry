package e2e

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http2"
	xhpack "golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// Every HTTP/2 connection the profile is checked against (every sample of
// every scenario, every connection of every page, the replayed real pages
// included), its requests sent again in order through mimicry's HTTP/2
// connection, each with the weight the browser gave it, has the browser's
// HEADERS priority on every request: none, or which earlier request it
// depends on (0 for none) and whether exclusively. For Chrome that is its
// chain (http2.chain_priorities): the newest stream still open at the
// nearest weight at or above the request's.
//
// A trace records what the client sent, not when each response ended, so
// the server here keeps each stream open exactly as long as the trace
// needs it: until the last request that depends on it went out, then ends
// it at once. Every stream gets its response headers when it opens (a
// stream whose response has started stays in the chain), and ends in one
// of two ways, alternately: a DATA frame with END_STREAM whose body the
// caller never reads, the next request going once a PING shows the client
// read it (the chain drops a stream at its END_STREAM, not when the body
// is read or closed); or HEADERS with END_STREAM, the next request going
// as soon as the caller has the response (the stream leaves the chain
// before its end reaches the caller, whatever the scheduling).
func TestChainedPrioritiesMatchBrowserTraces(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			for sc, traces := range scenarioTraces(t, p) {
				for i, tr := range traces {
					t.Run(sc.Name+" sample "+strconv.Itoa(i+1), func(t *testing.T) {
						t.Parallel()
						replayPriorities(t, p, tr)
					})
				}
			}
			for scenario, samples := range pageTraces(t, p) {
				for i, page := range samples {
					for k, tr := range page.H2 {
						t.Run(scenario+" sample "+strconv.Itoa(i+1)+" "+tr.Host+" "+strconv.Itoa(k), func(t *testing.T) {
							t.Parallel()
							replayPriorities(t, p, tr)
						})
					}
				}
			}
		})
	}
}

// replayPriorities sends tr's requests again through mimicry for p, the
// server ending each stream after the last request that depends on it, and
// compares each request's HEADERS priority with the browser's.
func replayPriorities(t *testing.T, p *profile.Profile, tr *h2trace.Trace) {
	t.Helper()
	var heads []h2trace.Frame
	for _, f := range tr.Frames {
		if f.Type == "HEADERS" {
			heads = append(heads, f)
		}
	}
	if len(heads) == 0 {
		return
	}
	paths := map[uint32]string{}
	for _, r := range tr.Requests {
		paths[r.Stream] = r.Path
	}
	// request maps a trace's stream to its request's index; last is, for
	// each request, the last request that depends on it (its own index
	// when none does).
	request := map[uint32]int{}
	last := make([]int, len(heads))
	for k, f := range heads {
		request[f.Stream] = k
		last[k] = k
		if f.Priority == nil || f.Priority.DependsOn == 0 {
			continue
		}
		parent, ok := request[f.Priority.DependsOn]
		if !ok {
			t.Fatalf("%s depends on stream %d, which no earlier request opened", paths[f.Stream], f.Priority.DependsOn)
		}
		last[parent] = k
	}
	want := func(k int) string {
		return describePriority(heads[k].Priority, func(stream uint32) (int, bool) {
			i, ok := request[stream]
			return i, ok
		})
	}

	pr, c := newChainPeer(t, p)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ids := make([]uint32, len(heads))
	mine := map[uint32]int{}
	results := make([]<-chan error, len(heads))
	open := map[int]bool{}
	for k, f := range heads {
		var weight uint8
		if f.Priority != nil {
			weight = f.Priority.Weight
		}
		results[k] = roundTripChain(ctx, c, k, weight)
		h := pr.headers(ctx)
		if h == nil {
			t.Fatalf("request %d (%s): no HEADERS from mimicry", k, paths[f.Stream])
		}
		ids[k] = h.StreamID
		mine[h.StreamID] = k
		var got *h2trace.Priority
		if h.HasPriority() {
			got = &h2trace.Priority{
				DependsOn: h.Priority.StreamDep,
				Exclusive: h.Priority.Exclusive,
				Weight:    h.Priority.Weight,
			}
		}
		if g, w := describePriority(got, func(stream uint32) (int, bool) {
			i, ok := mine[stream]
			return i, ok
		}), want(k); g != w {
			t.Errorf("request %d (%s): %s, the browser's %s", k, paths[f.Stream], g, w)
		}
		open[k] = true
		if k%2 == 0 {
			// Its response starts at once; the stream stays open.
			pr.respond(ids[k], false)
			if err := <-results[k]; err != nil {
				t.Fatalf("request %d: %v", k, err)
			}
		}
		// End every stream no later request depends on.
		synced := true
		for j := range k + 1 {
			if !open[j] || last[j] > k {
				continue
			}
			delete(open, j)
			if j%2 == 0 {
				pr.endData(ids[j])
				synced = false
				continue
			}
			pr.respond(ids[j], true)
			if err := <-results[j]; err != nil {
				t.Fatalf("request %d: %v", j, err)
			}
		}
		if !synced {
			pr.sync(ctx)
		}
		if t.Failed() {
			return
		}
	}
}

// describePriority describes a HEADERS priority by the request it depends
// on (index finds a stream's), so mimicry's and the browser's compare
// whatever their stream IDs.
func describePriority(p *h2trace.Priority, index func(stream uint32) (int, bool)) string {
	if p == nil {
		return "no priority"
	}
	parent := "nothing"
	if p.DependsOn != 0 {
		parent = "stream " + strconv.FormatUint(uint64(p.DependsOn), 10) + ", opened by no request"
		if i, ok := index(p.DependsOn); ok {
			parent = "request " + strconv.Itoa(i)
		}
	}
	return fmt.Sprintf("depends on %s, exclusive %t", parent, p.Exclusive)
}

// roundTripChain sends request k as a GET carrying weight (zero keeps the
// profile's) and delivers its error once it has the response headers. The
// body is never read.
func roundTripChain(ctx context.Context, c *h2.Conn, k int, weight uint8) <-chan error {
	done := make(chan error, 1)
	req, err := http.NewRequestWithContext(headerorder.WithPlan(ctx, &headerorder.Plan{
		Weight: weight,
	}), http.MethodGet, "https://chain.test/r"+strconv.Itoa(k), nil)
	if err != nil {
		done <- err
		return done
	}
	go func() {
		_, err := c.RoundTrip(req)
		done <- err
	}()
	return done
}

// chainPeer is the server end of one mimicry HTTP/2 connection, driven
// frame by frame: it reads every frame the client sends and keeps its
// HEADERS and PING ACKs.
type chainPeer struct {
	t      *testing.T
	fr     *http2.Framer
	frames chan http2.Frame
	enc    *xhpack.Encoder
	buf    bytes.Buffer
	pings  uint64
}

// newChainPeer opens a connection for p to a server peer over net.Pipe.
// The clock never moves: no credit timer or idle PING fires.
func newChainPeer(t *testing.T, p *profile.Profile) (*chainPeer, *h2.Conn) {
	t.Helper()
	client, server := net.Pipe()
	pr := &chainPeer{
		t:      t,
		fr:     http2.NewFramer(server, server),
		frames: make(chan http2.Frame, 64),
	}
	pr.fr.ReadMetaHeaders = xhpack.NewDecoder(4096, nil)
	pr.enc = xhpack.NewEncoder(&pr.buf)
	go func() {
		defer close(pr.frames)
		preface := make([]byte, len(http2.ClientPreface))
		if _, err := io.ReadFull(server, preface); err != nil {
			return
		}
		for {
			f, err := pr.fr.ReadFrame()
			if err != nil {
				return
			}
			switch f := f.(type) {
			case *http2.MetaHeadersFrame:
				pr.frames <- f
			case *http2.PingFrame:
				if f.IsAck() {
					pr.frames <- f
				}
			}
		}
	}()
	c, err := h2.New(client, p, clockwork.NewFakeClock())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		_ = server.Close()
	})
	return pr, c
}

// headers returns the next HEADERS the client sent, or nil.
func (p *chainPeer) headers(ctx context.Context) *http2.MetaHeadersFrame {
	for {
		select {
		case f, ok := <-p.frames:
			if !ok {
				return nil
			}
			if h, isHeaders := f.(*http2.MetaHeadersFrame); isHeaders {
				return h
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// respond sends response headers on stream, ending it if end.
func (p *chainPeer) respond(stream uint32, end bool) {
	p.t.Helper()
	p.buf.Reset()
	_ = p.enc.WriteField(xhpack.HeaderField{
		Name:  ":status",
		Value: "200",
	})
	if err := p.fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      stream,
		BlockFragment: p.buf.Bytes(),
		EndHeaders:    true,
		EndStream:     end,
	}); err != nil {
		p.t.Fatal(err)
	}
}

// endData ends stream with a one-byte DATA frame.
func (p *chainPeer) endData(stream uint32) {
	p.t.Helper()
	if err := p.fr.WriteData(stream, true, []byte("x")); err != nil {
		p.t.Fatal(err)
	}
}

// sync sends a PING and waits for its ACK: by then the client has read
// every frame sent before it.
func (p *chainPeer) sync(ctx context.Context) {
	p.t.Helper()
	p.pings++
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], p.pings)
	if err := p.fr.WritePing(false, data); err != nil {
		p.t.Fatal(err)
	}
	for {
		select {
		case f, ok := <-p.frames:
			if !ok {
				p.t.Fatal("the client closed the connection")
			}
			if ping, isPing := f.(*http2.PingFrame); isPing && ping.Data == data {
				return
			}
			p.t.Fatalf("unexpected %T before the PING ACK", f)
		case <-ctx.Done():
			p.t.Fatal("no PING ACK from the client")
		}
	}
}
