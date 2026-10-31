package quic

// mimicry: the STREAMS_BLOCKED a client connection sends while its requests
// wait for the server's limit on bidirectional streams, as its profile's
// browser does (profile.QUIC.StreamsBlocked). Not in upstream; see
// UPSTREAM.md.

import (
	"slices"

	"github.com/roachadam/mimicry/profile"
)

// streamsBlocked is the profile's STREAMS_BLOCKED rule with its state, for
// the bidirectional streams' localStreamLimits; guarded by their gate.
//
// Requests are counted from when they are made (Conn.ExpectStream), not
// from NewStream: HTTP/3 starts its requests one at a time, so at most one
// waits in NewStream however many wait for the limit. Streams go to the
// requests in the order made, so those that waited are the last in line.
type streamsBlocked struct {
	rule *profile.QUICStreamsBlocked
	// pending counts the requests made and not yet given a stream;
	// waiting, those of them made with the limit reached.
	pending, waiting int64
	// sent is the highest limit sent, so StreamsBlockedFirstRequest and
	// StreamsBlockedRaiseWaiting send none twice.
	sent int64
	// alone are the limits to send in packets of their own, in order.
	alone []int64
}

// newStreamsBlocked returns the state for rule, nil for none.
func newStreamsBlocked(rule *profile.QUICStreamsBlocked) *streamsBlocked {
	if rule == nil {
		return nil
	}

	return &streamsBlocked{
		rule: rule,
	}
}

// ExpectStream counts a request made on the connection, which opens a
// bidirectional stream with NewStream next. A request made when the
// requests before it take every stream the server allows may send
// STREAMS_BLOCKED by the profile's rule (profile.QUICStreamsBlocked).
// Every ExpectStream must be followed by one NewStream, which counts the
// request as given its stream, or as gone if it fails. Without a rule it
// does nothing.
func (c *Conn) ExpectStream() {
	if c.streams.localLimit[bidiStream].expect() {
		c.wake()
	}
}

// expect counts a request made (ExpectStream) and reports whether it
// queued a STREAMS_BLOCKED.
func (lim *localStreamLimits) expect() bool {
	if lim.blocked == nil {
		return false
	}

	lim.gate.lock()
	defer lim.unlock()

	b := lim.blocked
	ahead := lim.opened + b.pending
	b.pending++

	if lim.opened < 0 || ahead < lim.max {
		return false
	}

	b.waiting++
	if b.rule.AtRequest != profile.StreamsBlockedEveryRequest && lim.max <= b.sent {
		return false
	}

	b.sent = max(b.sent, lim.max)
	b.alone = append(b.alone, lim.max)

	return true
}

// openingBlocked is the STREAMS_BLOCKED a stream's opening carries: limit
// 0 for none; before its first STREAM frame or after it.
type openingBlocked struct {
	limit  int64
	before bool
}

// streamOpened counts the stream lim.opened just gave the first request in
// line, and returns the STREAMS_BLOCKED its opening carries. The gate must
// be held.
func (lim *localStreamLimits) streamOpened() openingBlocked {
	b := lim.blocked
	if b == nil || b.pending == 0 {
		return openingBlocked{}
	}

	waited := b.waiting == b.pending

	b.pending--
	if !waited {
		return openingBlocked{}
	}

	b.waiting--
	if lim.opened < lim.max {
		return openingBlocked{}
	}

	switch b.rule.AfterRaise {
	case profile.StreamsBlockedRaiseAlways:
		return openingBlocked{
			limit: lim.max,
		}
	case profile.StreamsBlockedRaiseWaiting:
		if b.waiting == 0 || lim.max <= b.sent {
			return openingBlocked{}
		}

		b.sent = lim.max

		return openingBlocked{
			limit:  lim.max,
			before: true,
		}
	}

	return openingBlocked{}
}

// streamFailed counts a request whose NewStream failed as gone. The gate
// must be held.
func (lim *localStreamLimits) streamFailed() {
	b := lim.blocked
	if b == nil || b.pending == 0 {
		return
	}

	b.pending--
	b.waiting = min(b.waiting, b.pending)
}

// appendStreamsBlocked appends the next STREAMS_BLOCKED queued for a
// packet of its own, if any, and reports whether one went. Called only
// with nothing else ack-eliciting in the packet; the caller adds nothing
// after it.
func (c *Conn) appendStreamsBlocked() bool {
	lim := &c.streams.localLimit[bidiStream]
	if lim.blocked == nil {
		return false
	}

	lim.gate.lock()
	defer lim.unlock()

	b := lim.blocked
	if len(b.alone) == 0 || !c.w.appendStreamsBlockedFrame(bidiStream, b.alone[0]) {
		return false
	}

	b.alone = slices.Delete(b.alone, 0, 1)

	return true
}

// streamsBlockedLost handles the loss of a packet carrying STREAMS_BLOCKED
// with limit, as upstream handles STREAM_DATA_BLOCKED's (RFC 9000, section
// 13.3): sent again, in a packet of its own, if the limit is still the
// server's and a request still waits for it. No capture saw a loss.
func (lim *localStreamLimits) streamsBlockedLost(limit int64) {
	if lim.blocked == nil {
		return
	}

	lim.gate.lock()
	defer lim.unlock()

	b := lim.blocked
	if limit == lim.max && lim.opened >= lim.max && b.pending > 0 && !slices.Contains(b.alone, limit) {
		b.alone = append(b.alone, limit)
	}
}
