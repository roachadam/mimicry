package quic

// mimicry: how a client connection returns flow-control credit and how
// often it acknowledges, as its profile's browser does (profile.QUIC.Credit
// and Ack). Not in upstream; see UPSTREAM.md.

// clientMsgQueue is how many messages (datagrams above all) a client
// connection's loop holds: a burst its browser reads whole before it
// acknowledges (QUICAck.Batched).
const clientMsgQueue = 64

// streamCreditDue reports whether a client stream's MAX_STREAM_DATA is
// due: added bytes were consumed since the last limit of a window-byte
// window. The profile says how much (QUICCredit.StreamDivisor); without
// it, upstream's eighth of the window.
func (c *Conn) streamCreditDue(window, added int64) bool {
	if c.client.p.Credit.StreamDivisor > 0 {
		return added >= window/int64(c.client.p.Credit.StreamDivisor)
	}
	return shouldUpdateFlowControl(window, added)
}

// clientReceived records where a client stream's in-order data ends after
// a STREAM frame. s.ingate must be held.
func (s *Stream) clientReceived() {
	if len(s.inset) == 0 || s.inset[0].start != 0 {
		return
	}
	if e := s.inset[0].end; len(s.inends) == 0 || e > s.inends[len(s.inends)-1] {
		s.inends = append(s.inends, e)
	}
}

// clientConsumed counts a client stream's data handed to its reader up to
// pos in the steps it arrived in, one STREAM frame at a time (inends), as
// a browser that consumes each packet as it comes: how much a Read takes
// at once changes nothing. The browsers' increases are the share plus
// less than a few packets (Chrome's MAX_DATA 7,864,457–7,871,535 on a
// 7,864,320 half window): a Read of tens of kilobytes counted at once
// would overshoot by that much. Each step is checked against the stream's
// rule, which sets the limit at the step that crosses it (inlimit), and
// kept for the connection's (increds, counted once ingate is released).
// s.ingate must be held.
func (s *Stream) clientConsumed(pos int64) {
	s.increds = s.increds[:0]
	i := 0
	for ; i < len(s.inends) && s.inends[i] <= pos; i++ {
		s.clientCredit(s.inends[i])
	}
	s.inends = s.inends[:copy(s.inends, s.inends[i:])]
	s.clientCredit(pos)
}

// clientCredit counts a client stream's data consumed up to end.
func (s *Stream) clientCredit(end int64) {
	if end <= s.incounted {
		return
	}
	s.increds = append(s.increds, end-s.incounted)
	s.incounted = end
	if s.insize != -1 && s.insize <= s.inwin {
		return
	}
	if limit := end + s.inmaxbuf; s.conn.streamCreditDue(s.inmaxbuf, limit-max(s.inwin, s.inlimit)) {
		s.inlimit = limit
		s.insendmax.setUnsent()
	}
}

// clientConnCreditDue reports whether a client's MAX_DATA is due after
// credit bytes consumed since the last: once they reach the window over
// QUICCredit.ConnectionDivisor. A client whose connection limit follows
// what it received (Safari) sends MAX_DATA with MAX_STREAM_DATA instead
// (appendWindowMaxData).
func (c *Conn) clientConnCreditDue(credit int64) bool {
	cr := c.client.p.Credit
	switch {
	case cr.ConnectionStreamWindow:
		return false
	case cr.ConnectionDivisor > 0:
		return credit >= c.config.maxConnReadBufferSize()/int64(cr.ConnectionDivisor)
	}
	return shouldUpdateFlowControl(c.config.maxConnReadBufferSize(), credit)
}

// streamWindowCredit reports whether the connection's limit is the bytes
// received plus the stream window (QUICCredit.ConnectionStreamWindow).
func (c *Conn) streamWindowCredit() bool {
	return c.client != nil && c.client.p.Credit.ConnectionStreamWindow
}

// appendWindowMaxData adds a MAX_DATA after a MAX_STREAM_DATA when the
// bytes received on the connection plus the stream window have passed
// its limit (Safari); one that does not fit goes in the next packet.
func (c *Conn) appendWindowMaxData(w *packetWriter, pnum packetNumber) {
	if !c.streamWindowCredit() {
		return
	}
	in := &c.streams.inflow
	limit := in.usedLimit + c.client.local.initialMaxStreamDataBidiLocal
	if limit <= in.sentLimit {
		return
	}
	in.newLimit = limit
	if !w.appendMaxDataFrame(limit) {
		in.sent.setUnsent()
		return
	}
	in.sentLimit = limit
	in.sent.setSent(pnum)
}

// clientAckEvery is after how many ack-eliciting 1-RTT packets a client
// acknowledges at once, having seen largest: the profile's warm-up rate,
// then its rate (QUICAck).
func (acks *ackState) clientAckEvery(largest packetNumber) int {
	if acks.warmupPackets > 0 && largest < packetNumber(acks.warmupPackets) {
		return acks.warmupEvery
	}
	return acks.every
}
