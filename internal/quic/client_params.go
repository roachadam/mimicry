package quic

// mimicry: a client's transport parameters as its profile lists them, and
// the server's, remembered with a ticket for 0-RTT. Not in upstream.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/roachadam/mimicry/internal/quic/quicwire"
	"github.com/roachadam/mimicry/profile"
)

// tpEntry is one encoded transport parameter.
type tpEntry struct {
	id    uint64
	value []byte
	last  bool
}

// marshalProfileParams encodes p's transport parameters for one
// connection: scid as initial_source_connection_id, GREASE drawn with rng,
// those sent only when resuming (resumed) included, rtt as initial_rtt
// (left out when zero), in the profile's order.
func marshalProfileParams(p *profile.QUIC, scid []byte, resumed bool, rtt time.Duration, version uint32, rng *rand.Rand) ([]byte, error) {
	var entries []tpEntry
	for i := range p.TransportParameters {
		tp := &p.TransportParameters[i]
		if tp.Resumed && !resumed {
			continue
		}
		e := tpEntry{
			id:   uint64(tp.ID),
			last: tp.Last,
		}
		switch {
		case tp.ID == profile.GREASETransportParameter:
			// 31 * N + 27 for a random N, the whole varint range (RFC 9000,
			// section 18.1).
			e.id = 27 + 31*rng.Uint64N((quicwire.MaxVarint-27)/31+1)
			e.value = randomBytes(rng, rng.IntN(tp.MaxLength+1))
		case tp.ID == profile.TPInitialSourceConnectionID:
			e.value = scid
		case tp.ID == profile.TPInitialRTT:
			if rtt <= 0 {
				continue
			}
			e.value = quicwire.AppendVarint(nil, uint64(rtt.Microseconds())) //nolint:gosec // rtt > 0
		case tp.ID == profile.TPVersionInformation:
			v, err := versionInformation(tp, version, rng)
			if err != nil {
				return nil, err
			}
			e.value = v
		case tp.Value != nil:
			if *tp.Value > quicwire.MaxVarint {
				return nil, fmt.Errorf("quic: transport parameter %s value %d is not a varint", tp.ID, *tp.Value)
			}
			e.value = quicwire.AppendVarint(nil, *tp.Value)
		default:
			e.value = tp.Data
		}
		entries = append(entries, e)
	}
	var movable, last []tpEntry
	for _, e := range entries {
		if e.last {
			last = append(last, e)
		} else {
			movable = append(movable, e)
		}
	}
	switch p.TransportParameterOrder {
	case profile.TransportParameterOrderFixed:
	case profile.TransportParameterOrderShuffled:
		rng.Shuffle(len(movable), func(i, j int) {
			movable[i], movable[j] = movable[j], movable[i]
		})
	case profile.TransportParameterOrderRotated:
		if len(movable) > 0 {
			k := rng.IntN(len(movable))
			movable = append(movable[k:], movable[:k]...)
		}
	default:
		return nil, fmt.Errorf("quic: transport parameter order %q", p.TransportParameterOrder)
	}
	var b []byte
	for _, e := range append(movable, last...) {
		b = quicwire.AppendVarint(b, e.id)
		b = quicwire.AppendVarintBytes(b, e.value)
	}
	return b, nil
}

// versionInformation encodes version_information (RFC 9368): the chosen
// version, then the available ones, GREASE drawn per connection. The
// chosen one is the version the connection starts in: v2 when it resumes
// a v2 connection's ticket (Firefox), the profile's otherwise.
func versionInformation(tp *profile.TransportParameter, version uint32, rng *rand.Rand) ([]byte, error) {
	if len(tp.Versions) == 0 {
		return nil, fmt.Errorf("quic: version_information without versions")
	}
	versions := make([]uint32, len(tp.Versions))
	for i, v := range tp.Versions {
		versions[i] = uint32(v)
		if v == profile.GREASEVersion {
			versions[i] = rng.Uint32()&0xf0f0f0f0 | 0x0a0a0a0a
		}
	}
	if version != quicVersion1 {
		versions[0] = version
	}
	if tp.ShuffleVersions {
		available := versions[1:]
		rng.Shuffle(len(available), func(i, j int) {
			available[i], available[j] = available[j], available[i]
		})
	}
	b := make([]byte, 0, 4*len(versions))
	for _, v := range versions {
		b = binary.BigEndian.AppendUint32(b, v)
	}
	return b, nil
}

// rememberedParamsPrefix marks the server's transport parameters in a
// ticket's SessionState.Extra.
var rememberedParamsPrefix = []byte("mimicry/quic-transport-parameters\x00")

// rememberedParams appends the server's transport parameters body to a
// ticket's Extra entries.
func rememberedParams(extra [][]byte, params []byte) [][]byte {
	return append(extra, append(append([]byte(nil), rememberedParamsPrefix...), params...))
}

// recallParams finds the server's transport parameters in a ticket's
// Extra entries.
func recallParams(extra [][]byte) (transportParameters, bool) {
	for _, e := range extra {
		if body, ok := bytes.CutPrefix(e, rememberedParamsPrefix); ok {
			p, err := unmarshalTransportParams(body)
			return p, err == nil
		}
	}
	return transportParameters{}, false
}

// applyRememberedParams applies the server's remembered transport
// parameters to 0-RTT (RFC 9000, section 7.4.1). active_connection_id_limit
// is the default until the server's own arrive, so a resumed connection
// issues one connection ID in 0-RTT (Firefox).
func (c *Conn) applyRememberedParams(p transportParameters) error {
	// The windows first, under tableMu: a stream may open as soon as
	// setMax allows it, and reads them then.
	c.streams.tableMu.Lock()
	c.streams.peerInitialMaxStreamDataRemote[bidiStream] = p.initialMaxStreamDataBidiRemote
	c.streams.peerInitialMaxStreamDataRemote[uniStream] = p.initialMaxStreamDataUni
	c.streams.tableMu.Unlock()
	c.streams.outflow.setMaxData(p.initialMaxData)
	c.streams.localLimit[bidiStream].setMax(p.initialMaxStreamsBidi)
	c.streams.localLimit[uniStream].setMax(p.initialMaxStreamsUni)
	c.streams.peerInitialMaxStreamDataBidiLocal = p.initialMaxStreamDataBidiLocal
	return c.connIDState.setPeerActiveConnIDLimit(c, defaultParamActiveConnIDLimit)
}
