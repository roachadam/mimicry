package server

import (
	"context"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/qlogwriter"
)

// H3Early is the h3-early scenario: requests of every method as the first
// on a resumed QUIC connection, each after the last connection idled out,
// so the browser's 0-RTT decides where each goes. See H3EarlySteps.
var H3Early = &Scenario{
	Name: "h3-early",
	Doc:  "requests of unsafe methods first on a resumed QUIC connection (fetch POST, PUT and DELETE, an XHR POST, a GET and a POST together, a GET, HEAD and OPTIONS, a form POST navigation), each after the last connection idled out: which the browser sends in 0-RTT and which it holds for the handshake",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
			},
			runFor: 70 * time.Second,
		},
		steps: H3EarlySteps,
	},
	h3: &h3Server{
		idleTimeout: h3EarlyIdle,
	},
}

// H3TooEarly is the h3-too-early scenario: the h3-early page against a
// server that answers every request it received in 0-RTT with 425 Too
// Early (RFC 8470): whether and where the browser sends it again.
var H3TooEarly = &Scenario{
	Name: "h3-too-early",
	Doc:  "the h3-early page against a server answering every request that arrived in 0-RTT with 425 Too Early: whether each browser sends it again, on the same connection after the handshake or on a new one",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3Host,
			},
			runFor: 70 * time.Second,
		},
		steps: H3EarlySteps,
	},
	h3: &h3Server{
		idleTimeout: h3EarlyIdle,
		tooEarly:    true,
	},
}

// h3EarlyIdle is the h3-early server's max_idle_timeout: the smaller of
// it and the browser's ends a connection, silently on both sides, so
// steps h3EarlyGap apart each find none and resume. (A server's close
// does not do for Firefox: it sends the next request over TCP and
// resumes QUIC beside it.)
const (
	h3EarlyIdle = 3 * time.Second
	h3EarlyGap  = 5000
)

// H3EarlySteps lists the h3-early page's requests, each started on a
// fixed schedule (NoWait). The page at / learns h3-a's Alt-Svc; a fetch
// ends its HTTP/2 connection (GOAWAY: Safari's way to HTTP/3), the next
// goes over a new QUIC connection, whose session ticket the later ones
// resume with. Each step after it comes once the connection before has
// idled out, so it starts a resumed connection: a fetch POST, PUT and
// DELETE, an XHR POST, a GET and a POST made together (pair-get.so,
// pair-post.so), a GET, a HEAD and an OPTIONS fetch, and last a form POST
// navigation to form.so, whose page asks for /done.
func H3EarlySteps() []H3Step {
	with := func(s H3Step, method, body string) H3Step {
		s.Method, s.Body = method, body
		return s
	}
	goAway := h3Fetch(h3Host, "goaway.so", 1000)
	goAway.GoAwayTCP = true
	xhr := h3Fetch(h3Host, "xhr-post.so", h3EarlyGap)
	xhr.Kind = "xhr"
	form := h3Fetch(h3Host, "form.so", h3EarlyGap)
	form.Kind = "navigate"
	return []H3Step{
		h3Fetch(h3Host, "warm.so", 0),
		goAway,
		h3Fetch(h3Host, "fetch1.so", 1500),
		with(h3Fetch(h3Host, "post.so", h3EarlyGap), "POST", "a=1"),
		with(h3Fetch(h3Host, "put.so", h3EarlyGap), "PUT", "a=1"),
		with(h3Fetch(h3Host, "delete.so", h3EarlyGap), "DELETE", ""),
		with(xhr, "POST", "a=1"),
		h3Fetch(h3Host, "pair-get.so", h3EarlyGap),
		with(h3Fetch(h3Host, "pair-post.so", 0), "POST", "a=1"),
		h3Fetch(h3Host, "get.so", h3EarlyGap),
		with(h3Fetch(h3Host, "head.so", h3EarlyGap), "HEAD", ""),
		with(h3Fetch(h3Host, "options.so", h3EarlyGap), "OPTIONS", ""),
		with(form, "POST", "a=1"),
	}
}

// earlyKey keys a QUIC connection's earlyStreams in its context.
type earlyKey struct{}

// earlyStreams records, for one QUIC connection, whether each stream's
// first STREAM frame arrived in a 0-RTT packet, as quic-go handles the
// packets (its qlog trace), so the server knows which requests came in
// early data however its goroutines are scheduled.
type earlyStreams struct {
	mu    sync.Mutex
	cond  *sync.Cond
	first map[int64]bool
	done  bool
}

func newEarlyStreams() *earlyStreams {
	e := &earlyStreams{
		first: map[int64]bool{},
	}
	e.cond = sync.NewCond(&e.mu)
	return e
}

// earlyContext gives a new connection its earlyStreams (quic.Transport's
// ConnContext).
func earlyContext(ctx context.Context, _ *quic.ClientInfo) (context.Context, error) {
	return context.WithValue(ctx, earlyKey{}, newEarlyStreams()), nil
}

// earlyTracer is the connection's earlyStreams as its qlog trace
// (quic.Config's Tracer); ctx is the one earlyContext returned.
func earlyTracer(ctx context.Context, _ bool, _ quic.ConnectionID) qlogwriter.Trace {
	if e, ok := ctx.Value(earlyKey{}).(*earlyStreams); ok {
		return e
	}
	return nil
}

// earlyOf is conn's earlyStreams, nil without one: a value of the
// connection's own context, which earlyContext gave it.
func earlyOf(conn *quic.Conn) *earlyStreams {
	e, _ := conn.Context().Value(earlyKey{}).(*earlyStreams)
	return e
}

// AddProducer is the qlog trace's: the recorder is e itself.
func (e *earlyStreams) AddProducer() qlogwriter.Recorder {
	return e
}

// SupportsSchemas reports none, so quic-go's HTTP/3 layer records nothing
// here (this server has its own).
func (e *earlyStreams) SupportsSchemas(string) bool {
	return false
}

// RecordEvent notes the packet type of each stream's first STREAM frame.
func (e *earlyStreams) RecordEvent(ev qlogwriter.Event) {
	var p qlog.PacketReceived
	switch v := ev.(type) {
	case qlog.PacketReceived:
		p = v
	case *qlog.PacketReceived:
		p = *v
	default:
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, f := range p.Frames {
		s, ok := f.Frame.(*qlog.StreamFrame)
		if !ok {
			continue
		}
		if _, seen := e.first[int64(s.StreamID)]; !seen {
			e.first[int64(s.StreamID)] = p.Header.PacketType == qlog.PacketType0RTT
		}
	}
	e.cond.Broadcast()
}

// Close ends the trace: waiters stop waiting.
func (e *earlyStreams) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.done = true
	e.cond.Broadcast()
	return nil
}

// early reports whether stream's first frame came in 0-RTT. The request
// was read to its end, so the packet that carried its first frame was
// handled; quic-go traces it right after handing the frames over, so the
// wait is short, and bounded by earlyWait.
func (e *earlyStreams) early(stream int64) bool {
	timedOut := false
	stop := time.AfterFunc(earlyWait, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		timedOut = true
		e.cond.Broadcast()
	})
	defer stop.Stop()
	e.mu.Lock()
	defer e.mu.Unlock()
	for {
		if v, ok := e.first[stream]; ok {
			return v
		}
		if e.done || timedOut {
			return false
		}
		e.cond.Wait()
	}
}

// earlyWait bounds how long early waits for a stream's first packet to be
// traced.
const earlyWait = time.Second
