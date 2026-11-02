package main

import (
	"github.com/roachadam/mimicry/profile"
)

// deriveFailurePair derives what the failure pair (h3-blackhole,
// h3-broken) shows of a browser captured over a network, its only edge
// captures (Safari on an iPhone, decision 0051): the keep-alive period and
// start, the path-degrading close, close on GOAWAY, and the fallback's
// mode, abandonment and where pending requests go, which must be those
// the profile copied (q and h hold the copy). The fallback's timing (the
// race and reconnect delays, how many failures break an origin) stays
// copied: over Wi-Fi the server sees the phone's packets a path's delay
// late, which swamps it, so its checks, which need loopback, are not run.
// The probes and give-up (quic.loss but pathDegrading), 0-RTT and the
// dial delays are not measured.
func (s *edgeSet) deriveFailurePair(q *profile.QUIC, h *profile.HTTP3) error {
	e := &collector{}

	s.silenced = s.silencedConns(e)
	q.KeepAlive = s.keepAlive(e)
	q.Loss = profile.QUICLoss{
		PathDegrading: s.pathDegrading(q, e),
	}
	h.CloseOnGoAway = s.closeOnGoAway(e)

	fb := &h.Discovery.Fallback
	got := s.fallback(q, &h.Discovery, &collector{})

	if got.Mode != fb.Mode || got.AbandonOnTCP != fb.AbandonOnTCP || got.Pending != fb.Pending {
		edgeErr(e, s.set.name, "fallback mode %q, abandonOnTcp %v, pending %q; the copied rule's are %q, %v, %q",
			got.Mode, got.AbandonOnTCP, got.Pending, fb.Mode, fb.AbandonOnTCP, fb.Pending)
	}

	return e.err()
}
