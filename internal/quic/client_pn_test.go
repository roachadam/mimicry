package quic

// mimicry: packet number lengths per profile.QUIC.PacketNumbers.

import (
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// TestPacketNumberLengthBeforeAnyAck: with nothing acknowledged, a
// random_initial profile sizes a number to hold pnum + 1 (neqo), others by
// RFC 9000's appendix A.2; once something is acknowledged both follow A.2.
// A receiver that has seen no packet decodes every one.
func TestPacketNumberLengthBeforeAnyAck(t *testing.T) {
	for _, tc := range []struct {
		pnum     packetNumber
		maxAcked packetNumber
		neqo     int
		rfc      int
	}{
		{
			pnum:     0,
			maxAcked: -1,
			neqo:     1,
			rfc:      1,
		},
		{
			pnum:     126,
			maxAcked: -1,
			neqo:     1,
			rfc:      1,
		},
		{
			pnum:     127,
			maxAcked: -1,
			neqo:     1,
			rfc:      2,
		},
		{
			pnum:     254,
			maxAcked: -1,
			neqo:     1,
			rfc:      2,
		},
		{
			pnum:     255,
			maxAcked: -1,
			neqo:     2,
			rfc:      2,
		},
		{
			pnum:     1004,
			maxAcked: -1,
			neqo:     2,
			rfc:      2,
		},
		{
			pnum:     65534,
			maxAcked: -1,
			neqo:     2,
			rfc:      3,
		},
		{
			pnum:     65535,
			maxAcked: -1,
			neqo:     3,
			rfc:      3,
		},
		{
			pnum:     330,
			maxAcked: 200,
			neqo:     2,
			rfc:      2,
		},
		{
			pnum:     260,
			maxAcked: 200,
			neqo:     1,
			rfc:      1,
		},
	} {
		for _, rule := range []string{profile.PacketNumbersRandomInitial, profile.PacketNumbersPerSpace} {
			cc := &clientState{p: &profile.QUIC{PacketNumbers: rule}}
			want := tc.rfc

			if rule == profile.PacketNumbersRandomInitial {
				want = tc.neqo
			}

			against := cc.pnMaxAcked(tc.pnum, tc.maxAcked)
			if got := packetNumberLength(tc.pnum, against); got != want {
				t.Errorf("%s: %d with %d acknowledged in %d bytes, want %d", rule, tc.pnum, tc.maxAcked, got, want)
			}

			b := appendPacketNumber(nil, tc.pnum, against)

			var truncated packetNumber
			for _, c := range b {
				truncated = truncated<<8 | packetNumber(c)
			}

			if got := decodePacketNumber(tc.maxAcked, truncated, len(b)); got != tc.pnum {
				t.Errorf("%s: %d encoded in %d bytes decodes as %d", rule, tc.pnum, len(b), got)
			}
		}
	}
}
