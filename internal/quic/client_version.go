package quic

// mimicry: QUIC v2 (RFC 9369) by compatible version negotiation (RFC 9368)
// for a client whose profile lists v2 in version_information. Upstream
// has no v2. Not in upstream; see UPSTREAM.md.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/roachadam/mimicry/profile"
)

// ErrVersionNegotiation is the error of a connection the server answered
// with a Version Negotiation packet that does not list its version (RFC
// 9000, section 6.2). Dial returns it at once as a *VersionNegotiationError;
// errors.Is matches it.
var ErrVersionNegotiation = errVersionNegotiation

// VersionNegotiationError is a Version Negotiation packet's answer.
type VersionNegotiationError struct {
	// Versions are the packet's Supported Versions, reserved ones
	// included.
	Versions []uint32
}

func (e *VersionNegotiationError) Error() string {
	return fmt.Sprintf("quic: server answered with Version Negotiation %#x", e.Versions)
}

// Is makes errors.Is(err, ErrVersionNegotiation) hold.
func (e *VersionNegotiationError) Is(target error) bool {
	return target == errVersionNegotiation
}

// RetryVersion is the version a profile's client starts a new connection
// in after err, a Version Negotiation packet (RFC 9368, section 2.1), when
// its profile retries (QUICVersionNegotiation.Retry): the first of its
// Available Versions the packet lists, other than the one it was in. The
// new connection's DialConfig sets AfterVersionNegotiation.
func RetryVersion(err error, p *profile.QUIC, from uint32) (uint32, bool) {
	var vn *VersionNegotiationError
	if !p.VersionNegotiation.Retry || !errors.As(err, &vn) {
		return 0, false
	}
	return chooseVersion(clientAvailable(p), vn.Versions, from)
}

// chooseVersion is the first of available, in its order, that offered
// lists, other than from.
func chooseVersion(available, offered []uint32, from uint32) (uint32, bool) {
	for _, v := range available {
		if v != from && slices.Contains(offered, v) {
			return v, true
		}
	}
	return 0, false
}

// clientAvailable is a profile's Available Versions, GREASE left out.
func clientAvailable(p *profile.QUIC) []uint32 {
	_, available := clientVersions(p)
	return available
}

// clientVersionNegotiation ends a client connection on a Version
// Negotiation packet that does not list its version, with the packet's
// versions. One for a connection that reacted to an earlier packet is
// ignored (RFC 9368, section 2.1).
func (c *Conn) clientVersionNegotiation(now time.Time, b []byte) {
	if c.client.afterVN {
		return
	}
	var versions []uint32
	for ; len(b) >= 4; b = b[4:] {
		v := binary.BigEndian.Uint32(b)
		if v == c.client.version {
			// "A client MUST discard a Version Negotiation packet that
			// lists the QUIC version selected by the client."
			return
		}
		versions = append(versions, v)
	}
	c.abortImmediately(now, &VersionNegotiationError{Versions: versions})
}

// errVersionNegotiationError is RFC 9368's VERSION_NEGOTIATION_ERROR, the
// code of a failed version_information check for a profile that names
// none (QUICVersionNegotiation.DowngradeError);
// errVersionNegotiationDraft is the draft's, which Firefox sends (capture
// h3-vn).
const (
	errVersionNegotiationError = transportError(0x11)
	errVersionNegotiationDraft = transportError(0x53f8)
)

// clientVersions is what a client's version_information says: whether it
// sends one and the Available Versions in it (GREASE left out: a server
// never chooses one).
func clientVersions(p *profile.QUIC) (sent bool, available []uint32) {
	for i := range p.TransportParameters {
		tp := &p.TransportParameters[i]
		if tp.ID != profile.TPVersionInformation || len(tp.Versions) == 0 {
			continue
		}
		for _, v := range tp.Versions[1:] {
			if v != profile.GREASEVersion {
				available = append(available, uint32(v))
			}
		}
		return true, available
	}
	return false, nil
}

// offers reports whether the client lists version v as available.
func (cc *clientState) offers(v uint32) bool {
	for _, a := range cc.available {
		if a == v {
			return true
		}
	}
	return false
}

// quicVersion is the connection's QUIC version: a client's negotiated one,
// version 1 for upstream's connections.
func (c *Conn) quicVersion() uint32 {
	if c.client != nil {
		return c.client.version
	}
	return quicVersion1
}

// clientUpgrade switches a client to v2 when the first packet of buf is
// the server's v2 Initial (RFC 9369, section 4.1: "the first long header
// Version field that differs from the original version"). It switches only
// if it lists v2, before the ServerHello, and on a packet the v2 Initial
// keys open, so a forged Version field does not move it.
func (c *Conn) clientUpgrade(buf []byte) {
	cc := c.client
	if cc.version != quicVersion1 || !cc.offers(quicVersion2) ||
		len(buf) < 5 || !isLongHeader(buf[0]) ||
		binary.BigEndian.Uint32(buf[1:5]) != quicVersion2 ||
		getPacketType(buf) != packetTypeInitial ||
		!c.keysInitial.canRead() || c.keysHandshake.canRead() {
		return
	}
	keys := initialKeysVersion(cc.initialCID, c.side, quicVersion2)
	// A copy: removing the protection works in place.
	if _, n := parseLongHeaderPacket(cloneBytes(buf), keys.r, c.acks[initialSpace].largestSeen()); n < 0 {
		return
	}
	cc.version = quicVersion2
	c.keysInitial = keys
}

// clientCheckVersionInfo validates the server's version_information
// (RFC 9368, section 4): well formed, its Chosen Version one the client
// offered and the version in use.
func (c *Conn) clientCheckVersionInfo(p transportParameters) error {
	cc := c.client
	if p.versionInfo == nil {
		// Allowed without a Version Negotiation packet (RFC 9368, section
		// 4); a client that changed versions learnt v2 from the server's
		// long headers, which an attacker could have forged.
		if cc.afterVN || cc.version != cc.originalVersion {
			return localTransportError{
				code:   cc.downgradeErr,
				reason: "no version_information after a version change",
			}
		}
		return nil
	}
	vi := p.versionInfo
	if len(vi) < 4 || len(vi)%4 != 0 {
		return localTransportError{code: errTransportParameter, reason: "malformed version_information"}
	}
	for b := vi; len(b) > 0; b = b[4:] {
		if binary.BigEndian.Uint32(b) == 0 {
			return localTransportError{code: errTransportParameter, reason: "version 0 in version_information"}
		}
	}
	chosen := binary.BigEndian.Uint32(vi)
	switch {
	case cc.sentVersionInfo && !cc.offers(chosen):
		return localTransportError{code: cc.downgradeErr, reason: "server chose a version the client did not offer"}
	case chosen != cc.version:
		return localTransportError{code: cc.downgradeErr, reason: "server's chosen version is not the version in use"}
	}
	if cc.afterVN {
		// The server's Available Versions must lead the client to the
		// version it reacted with: no forged packet downgraded it.
		var offered []uint32
		for b := vi[4:]; len(b) > 0; b = b[4:] {
			offered = append(offered, binary.BigEndian.Uint32(b))
		}
		if v, ok := chooseVersion(cc.available, append(offered, chosen), 0); len(offered) == 0 || !ok || v != cc.version {
			return localTransportError{code: cc.downgradeErr, reason: "server's available versions do not match the Version Negotiation"}
		}
	}
	return nil
}

// rememberedVersionPrefix marks a v2 connection's version in its tickets'
// SessionState.Extra.
var rememberedVersionPrefix = []byte("mimicry/quic-version\x00")

// rememberedVersion appends version to a ticket's Extra entries.
func rememberedVersion(extra [][]byte, version uint32) [][]byte {
	return append(extra, binary.BigEndian.AppendUint32(bytes.Clone(rememberedVersionPrefix), version))
}

// TicketVersion is the QUIC version of the connection that issued a
// ticket, from its SessionState.Extra: the version a connection resuming
// it starts in (DialConfig.Version). Firefox resumes a v2 connection's
// ticket in v2 from its first packet (capture h3-v2); tickets of version
// 1 connections, and any other, give 1.
func TicketVersion(extra [][]byte) uint32 {
	for _, e := range extra {
		if b, ok := bytes.CutPrefix(e, rememberedVersionPrefix); ok && len(b) == 4 {
			return binary.BigEndian.Uint32(b)
		}
	}
	return quicVersion1
}
