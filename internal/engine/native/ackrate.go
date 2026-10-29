package native

import (
	"fmt"

	"github.com/roachadam/mimicry/profile"
)

// maxACKEvery bounds Config.HTTP3ACKEvery: past it a sender whose window
// holds fewer packets waits for the ACK timer (max_ack_delay) instead of
// the count.
const maxACKEvery = 64

// ackOverride returns the QUIC section connections dial with: q itself, or,
// with every set, a copy acknowledging every every-th ack-eliciting packet
// after the warm-up. Every other ACK rule stays the profile's: the warm-up,
// batching, max_ack_delay, the immediate ACK of a gap. The profile is
// shared and read-only, so the copy is shallow: only Ack differs.
func ackOverride(q *profile.QUIC, every int) (*profile.QUIC, error) {
	switch {
	case every == 0:
		return q, nil
	case q == nil:
		return nil, fmt.Errorf("mimicry: Config.HTTP3ACKEvery %d with a profile that has no QUIC section", every)
	case every < q.Ack.Every || every > maxACKEvery:
		return nil, fmt.Errorf("mimicry: Config.HTTP3ACKEvery %d is outside %d (the profile's) to %d", every, q.Ack.Every, maxACKEvery)
	}

	c := *q

	c.Ack.Every = every

	return &c, nil
}
