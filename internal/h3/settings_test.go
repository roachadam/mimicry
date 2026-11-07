package h3

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/roachadam/mimicry/internal/qpack"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// rememberedConn is the QPACK side of a connection resumed with 0-RTT
// under p, its encoder started from the remembered capacity and blocked
// streams and its first capacity instruction already sent with the 0-RTT
// requests. The QUIC test server takes no 0-RTT, so New cannot start one.
func rememberedConn(p profile.Profile, capacity, blocked uint64) *Conn {
	h := p.Spec().HTTP3

	c := &Conn{
		h3:  h,
		enc: qpack.NewEncoder(QPACKConfig(h)),
	}
	c.remember(capacity, blocked)
	c.enc.TakeCapacity()

	return c
}

// Once a resumed connection's SETTINGS arrive, Chrome sets the remembered
// capacity again before its next instructions (remembered_repeat);
// Firefox does not (remembered).
func TestResumedSettingsRepeatCapacity(t *testing.T) {
	tests := []struct {
		name    string
		profile profile.Profile
		repeat  bool
	}{
		{
			name:    "chrome",
			profile: profiles.ChromeMacOS(),
			repeat:  true,
		},
		{
			name:    "firefox",
			profile: profiles.FirefoxMacOS(),
			repeat:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := rememberedConn(tc.profile, 65536, 100)

			announce, err := c.peerSettings(65536, 100)
			if err != nil {
				t.Fatal(err)
			}

			got := slices.Concat(announce, c.enc.TakeCapacity())

			var want []byte
			if tc.repeat {
				once := qpack.NewEncoder(QPACKConfig(c.h3))
				once.SetPeerSettings(65536, 100)

				want = once.TakeCapacity()
			}

			if !bytes.Equal(got, want) {
				t.Fatalf("encoder stream after SETTINGS: %x, want %x", got, want)
			}
		})
	}
}

// A server that accepts 0-RTT and then lowers the remembered QPACK limits
// breaks RFC 9114, section 7.2.4.2: the connection closes with
// H3_SETTINGS_ERROR rather than encode against a shrunken table.
func TestResumedSettingsMustNotShrink(t *testing.T) {
	tests := []struct {
		name     string
		capacity uint64
		blocked  uint64
	}{
		{
			name:     "no table",
			capacity: 0,
			blocked:  100,
		},
		{
			name:     "smaller table",
			capacity: 4096,
			blocked:  100,
		},
		{
			name:     "fewer blocked streams",
			capacity: 65536,
			blocked:  0,
		},
	}

	for name, p := range map[string]profile.Profile{"chrome": profiles.ChromeMacOS(), "firefox": profiles.FirefoxMacOS()} {
		for _, tc := range tests {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				c := rememberedConn(p, 65536, 100)

				_, err := c.peerSettings(tc.capacity, tc.blocked)

				ce, ok := errors.AsType[*connError](err)
				if !ok || ce.code != codeSettingsError {
					t.Fatalf("err %v, want H3_SETTINGS_ERROR", err)
				}
			})
		}
	}
}
