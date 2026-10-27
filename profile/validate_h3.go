package profile

import (
	"fmt"
	"slices"
	"strings"
)

// QUIC limits: the varint maximum (RFC 9000, section 16), the connection ID
// length QUIC version 1 allows (section 17.2), the smallest datagram that
// may carry a client's Initial (section 14.1) and the largest UDP payload.
const (
	maxVarint        = 1<<62 - 1
	maxCIDLength     = 20
	minInitialSize   = 1200
	maxUDPPayload    = 65527
	quicVersion1     = 1
	h3GreaseMaxNBits = 57
)

// pseudoH3 are the pseudo-headers every HTTP/3 request carries.
var pseudoH3 = []string{
	":method",
	":scheme",
	":authority",
	":path",
}

// checkH3 checks the quic and http3 sections and tls.quic: present
// together, each value known and within its protocol's limits. tls.quic
// may come first: hellogen writes it before h3gen writes the others.
func (c *checker) checkH3(p *Profile) {
	if p.TLS.QUIC != nil {
		c.checkQUICHello(&p.TLS)
	}
	switch {
	case p.QUIC == nil && p.HTTP3 == nil:
		return
	case p.QUIC == nil:
		c.add("quic", "is required with http3")
		return
	case p.HTTP3 == nil:
		c.add("http3", "is required with quic")
		return
	}
	if p.TLS.QUIC == nil {
		c.add("tls.quic", "is required with quic")
	}
	c.checkQUIC(p.QUIC, p.TLS.Resumption != nil)
	if p.TLS.QUIC != nil && (p.TLS.QUIC.VersionNegotiation != nil) != p.QUIC.VersionNegotiation.Retry {
		c.add("tls.quic.version_negotiation", "is required with quic.version_negotiation.retry, and only then")
	}
	c.checkHTTP3(p)
}

func (c *checker) checkQUIC(q *QUIC, resumes bool) {
	const path = "quic"
	if q.Version != quicVersion1 {
		c.add(path+".version", "%d must be 1: QUIC version 1 is the only one implemented", q.Version)
	}
	if q.DatagramSize < minInitialSize || q.DatagramSize > maxUDPPayload {
		c.add(path+".datagram_size", "%d must be from %d (RFC 9000, section 14.1) to %d", q.DatagramSize, minInitialSize, maxUDPPayload)
	}
	if !slices.Contains([]string{PaddingScattered, PaddingTrailing, PaddingEnd}, q.Padding) {
		c.add(path+".padding", "%q must be %q, %q or %q", q.Padding, PaddingScattered, PaddingTrailing, PaddingEnd)
	}
	c.checkQUICCrypto(&q.Crypto)
	if !slices.Contains([]string{PacketNumbersShared, PacketNumbersRandomInitial, PacketNumbersPerSpace}, q.PacketNumbers) {
		c.add(path+".packet_numbers", "%q must be %q, %q or %q", q.PacketNumbers, PacketNumbersShared, PacketNumbersRandomInitial, PacketNumbersPerSpace)
	}
	if q.DCIDLength < 8 || q.DCIDLength > maxCIDLength {
		c.add(path+".dcid_length", "%d must be from 8 (RFC 9000, section 7.2) to %d", q.DCIDLength, maxCIDLength)
	}
	if q.DCIDLengthRule != "" && (q.DCIDLengthRule != DCIDLengthBiased || q.DCIDLength != 8) {
		c.add(path+".dcid_length_rule", "must be empty, or %q with dcid_length 8", DCIDLengthBiased)
	}
	if q.SCIDLength < 0 || q.SCIDLength > maxCIDLength {
		c.add(path+".scid_length", "%d must be from 0 to %d", q.SCIDLength, maxCIDLength)
	}
	switch {
	case q.NewConnectionIDs < 0:
		c.add(path+".new_connection_ids", "must not be negative")
	case q.NewConnectionIDs > 0 && q.SCIDLength == 0:
		c.add(path+".new_connection_ids", "needs a scid_length: a zero-length connection ID cannot be replaced (RFC 9000, section 5.1.1)")
	}
	c.checkTransportParameters(q)
	if q.AckDelay == AckDelayConstant {
		if r := q.AckDelayConstant; r == nil || r.Min < 0 || r.Max < r.Min {
			c.add(path+".ack_delay_constant", "is required with %q, 0 <= min <= max", AckDelayConstant)
		}
	} else {
		if q.AckDelay != AckDelayActual {
			c.add(path+".ack_delay", "%q must be %q or %q", q.AckDelay, AckDelayActual, AckDelayConstant)
		}
		if q.AckDelayConstant != nil {
			c.add(path+".ack_delay_constant", "applies only with ack_delay %q", AckDelayConstant)
		}
	}
	if !slices.Contains([]string{SpinBitZero, SpinBitRandom, SpinBitSpin}, q.SpinBit) {
		c.add(path+".spin_bit", "%q must be %q, %q or %q", q.SpinBit, SpinBitZero, SpinBitRandom, SpinBitSpin)
	}
	if q.ZeroRTT && !resumes {
		c.add(path+".zero_rtt", "needs tls.resumption: 0-RTT resumes a session")
	}
	if (q.AckOnlyLimit > 0) != (q.AckOnlyFrame != "") || q.AckOnlyLimit < 0 || q.AckOnlyFrame != "" && q.AckOnlyFrame != AckOnlyMaxData {
		c.add(path+".ack_only_frame", "and ack_only_limit go together: a positive limit and %q, or neither", AckOnlyMaxData)
	}
	c.checkQUICLoss(q)
	if k := q.KeepAlive; k.PeriodMillis <= 0 || !slices.Contains([]string{KeepAliveFromSent, KeepAliveFromIdle, KeepAliveFromReceived}, k.From) {
		c.add(path+".keep_alive", "needs a positive period_ms, and from %q, %q or %q", KeepAliveFromSent, KeepAliveFromIdle, KeepAliveFromReceived)
	}
	if a := q.Ack; a.Every < 1 || (a.WarmupPackets > 0) != (a.WarmupEvery > 0) || a.WarmupPackets < 0 || a.WarmupEvery < 0 {
		c.add(path+".ack", "needs every >= 1, and warmup_packets and warmup_every both positive or both absent")
	}
	if cr := q.Credit; cr.StreamDivisor < 1 || (cr.ConnectionDivisor > 0) == cr.ConnectionStreamWindow || cr.ConnectionDivisor < 0 {
		c.add(path+".credit", "needs stream_divisor >= 1, and either connection_divisor >= 1 or connection_stream_window")
	}
	vn := q.VersionNegotiation
	if vn.DowngradeError != 0 && !vn.Retry {
		c.add(path+".version_negotiation.downgrade_error", "applies only with retry")
	}
	if vn.Retry && len(otherVersions(q)) == 0 {
		c.add(path+".version_negotiation.retry", "needs version_information offering a version besides 1")
	}
}

// otherVersions are the versions besides 1 that q's version_information
// offers, GREASE left out.
func otherVersions(q *QUIC) []QUICVersion {
	var out []QUICVersion
	for _, tp := range q.TransportParameters {
		if tp.ID != TPVersionInformation {
			continue
		}
		for _, v := range tp.Versions {
			if v != quicVersion1 && v != GREASEVersion && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}

	return out
}

func (c *checker) checkQUICLoss(q *QUIC) {
	const path = "quic.loss"
	l := &q.Loss
	if l.InitialPTOMillis < 0 {
		c.add(path+".initial_pto_ms", "must not be negative")
	}
	if !slices.Contains([]string{"", ProbeFlight, ProbeOnePacket}, l.Probe) {
		c.add(path+".probe", "%q must be empty, %q or %q", l.Probe, ProbeFlight, ProbeOnePacket)
	}
	for name, g := range map[string]QUICGiveUp{
		"fresh":   l.Fresh,
		"resumed": l.Resumed,
	} {
		at := path + "." + name
		switch {
		case g.AfterMillis < 0:
			c.add(at+".after_ms", "must not be negative")
		case !slices.Contains([]string{"", CloseTransport, CloseApplication}, g.Close):
			c.add(at+".close", "%q must be empty, %q or %q", g.Close, CloseTransport, CloseApplication)
		case g.Close != "" && g.AfterMillis == 0:
			c.add(at+".close", "needs after_ms: a close at the idle timeout is never sent")
		case (g.Close == CloseTransport) != (g.Reason != ""):
			c.add(at+".reason", "is required with close %q, and only then", CloseTransport)
		case g.Close == "" && g.DatagramSize != 0, g.DatagramSize < 0, g.DatagramSize > q.DatagramSize:
			c.add(at+".datagram_size", "%d must be from 0 to datagram_size, and only with a close", g.DatagramSize)
		}
	}
	if l.AbandonClose != "" && l.AbandonClose != CloseZeroRTT {
		c.add(path+".abandon_close", "%q must be empty or %q", l.AbandonClose, CloseZeroRTT)
	}
	if d := l.PathDegrading; d != nil {
		at := path + ".path_degrading"
		if (d.AfterPTOs > 0) == (d.AfterMillis > 0) || d.AfterPTOs < 0 || d.AfterMillis < 0 {
			c.add(at, "needs exactly one of after_ptos and after_ms, positive")
		}
		if !slices.Contains([]string{PathMigrate, PathClose}, d.Action) {
			c.add(at+".action", "%q must be %q or %q", d.Action, PathMigrate, PathClose)
		}
		if d.CloseReason != "" && d.Action != PathClose {
			c.add(at+".close_reason", "applies only with action %q", PathClose)
		}
	}
}

func (c *checker) checkQUICCrypto(cr *QUICCrypto) {
	const path = "quic.crypto"
	shuffle := cr.Head != nil || cr.AddedFrames != nil || cr.Pings != nil
	switch cr.Split {
	case CryptoSplitShuffle:
		for name, r := range map[string]*Range{
			"head":         cr.Head,
			"added_frames": cr.AddedFrames,
			"pings":        cr.Pings,
		} {
			if r == nil || r.Min < 0 || r.Max < r.Min {
				c.add(path+"."+name, "is required with %q, 0 <= min <= max", CryptoSplitShuffle)
			}
		}
		if cr.FirstPacket != 0 {
			c.add(path+".first_packet", "applies only with %q", CryptoSplitOrdered)
		}
	case CryptoSplitSNICut, CryptoSplitOrdered:
		if shuffle {
			c.add(path, "head, added_frames and pings apply only with %q", CryptoSplitShuffle)
		}
		if (cr.Split == CryptoSplitOrdered) != (cr.FirstPacket > 0) {
			c.add(path+".first_packet", "is required with %q, and only then", CryptoSplitOrdered)
		}
	default:
		c.add(path+".split", "%q must be %q, %q or %q", cr.Split, CryptoSplitShuffle, CryptoSplitSNICut, CryptoSplitOrdered)
	}
}

func (c *checker) checkTransportParameters(q *QUIC) {
	const path = "quic.transport_parameters"
	if len(q.TransportParameters) == 0 {
		c.add(path, "is required")
	}
	order := q.TransportParameterOrder
	if !slices.Contains([]string{TransportParameterOrderFixed, TransportParameterOrderShuffled, TransportParameterOrderRotated}, order) {
		c.add("quic.transport_parameter_order", "%q must be %q, %q or %q", order, TransportParameterOrderFixed, TransportParameterOrderShuffled, TransportParameterOrderRotated)
	}
	seen := map[TransportParameterID]bool{}
	last := false
	for i, tp := range q.TransportParameters {
		at := fmt.Sprintf("%s[%d]", path, i)
		if seen[tp.ID] {
			c.add(at, "repeats %s", tp.ID)
		}
		seen[tp.ID] = true
		switch {
		case tp.Last:
			if order != TransportParameterOrderRotated {
				c.add(at+".last", "applies only with transport_parameter_order %q", TransportParameterOrderRotated)
			}
			last = true
		case last:
			c.add(at, "must come before the parameters marked last")
		}
		if tp.ID > maxVarint {
			c.add(at+".id", "exceeds 2^62-1")
		}
		if tp.ID != GREASETransportParameter && IsGREASETransportParameter(uint64(tp.ID)) && tp.Value == nil && len(tp.Data) == 0 {
			// A reserved ID with a value of its own is a real
			// parameter (min_ack_delay); one without is GREASE.
			c.add(at+".id", "%s is reserved: write \"GREASE\" for a random one", tp.ID)
		}
		c.checkTransportParameter(at, q, tp)
	}
	if !seen[TPInitialSourceConnectionID] {
		c.add(path, "needs initial_source_connection_id (0x0f): RFC 9000, section 7.3")
	}
}

func (c *checker) checkTransportParameter(at string, q *QUIC, tp TransportParameter) {
	generated := tp.ID == TPInitialSourceConnectionID || tp.ID == TPInitialRTT
	if tp.Value != nil && len(tp.Data) > 0 {
		c.add(at, "has both value and data")
	}
	if tp.Value != nil && *tp.Value > maxVarint {
		c.add(at+".value", "exceeds 2^62-1")
	}
	if generated && (tp.Value != nil || len(tp.Data) > 0 || len(tp.Versions) > 0) {
		c.add(at, "%s is generated per connection: it takes no value", tp.ID)
	}
	if (tp.ID == TPInitialRTT) != tp.Resumed {
		c.add(at+".resumed", "is for initial_rtt (0x3127), which only a resumed connection sends, and it alone")
	}
	switch tp.ID {
	case GREASETransportParameter:
		if tp.Value != nil || len(tp.Data) > 0 {
			c.add(at, "GREASE takes max_length, not a value")
		}
		if tp.MaxLength < 0 || tp.MaxLength > 255 {
			c.add(at+".max_length", "must be from 0 to 255")
		}
	case TPVersionInformation:
		if len(tp.Versions) < 2 {
			c.add(at+".versions", "needs the chosen version and at least one available version (RFC 9368, section 3)")
		} else if tp.Versions[0] != QUICVersion(q.Version) {
			c.add(at+".versions", "the chosen version %s must be quic.version", tp.Versions[0])
		}
	case TPMaxUDPPayloadSize:
		if tp.Value == nil || *tp.Value < minInitialSize {
			c.add(at+".value", "max_udp_payload_size must be at least 1200 (RFC 9000, section 18.2)")
		}
	case TPAckDelayExponent:
		if tp.Value == nil || *tp.Value > 20 {
			c.add(at+".value", "ack_delay_exponent must be at most 20 (RFC 9000, section 18.2)")
		}
	case TPMaxAckDelay:
		if tp.Value == nil || *tp.Value >= 1<<14 {
			c.add(at+".value", "max_ack_delay must be below 2^14 (RFC 9000, section 18.2)")
		}
	case TPActiveConnectionIDLimit:
		if tp.Value == nil || *tp.Value < 2 {
			c.add(at+".value", "active_connection_id_limit must be at least 2 (RFC 9000, section 18.2)")
		}
	default:
		// No bound on the rest's values.
	}
	if tp.ID != TPVersionInformation && (len(tp.Versions) > 0 || tp.ShuffleVersions) {
		c.add(at+".versions", "only version_information (0x11) has versions")
	}
	if tp.ID != GREASETransportParameter && tp.MaxLength != 0 {
		c.add(at+".max_length", "only a GREASE parameter has max_length")
	}
}

// checkQUICHello checks tls.quic against the hello it changes.
func (c *checker) checkQUICHello(t *TLS) {
	h := t.QUIC
	const path = "tls.quic"
	if len(h.CipherSuites) == 0 {
		c.add(path+".cipher_suites", "is required")
	}
	c.checkUnique(path+".cipher_suites", h.CipherSuites)
	if h.SessionIDLength != 0 {
		c.add(path+".session_id_length", "must be 0: a QUIC ClientHello has no session ID (RFC 9001, section 8.4)")
	}
	dropped := map[Code]bool{}
	for _, d := range h.Drop {
		switch {
		case dropped[d]:
			c.add(path+".drop", "lists %s twice", d)
		case d == ExtServerName:
			c.add(path+".drop", "must not drop server_name")
		case t.extension(d) == nil:
			c.add(path+".drop", "%s is not in the hello", d)
		}
		dropped[d] = true
	}
	replaced := map[Code]bool{}
	for i, e := range h.Replace {
		at := fmt.Sprintf("%s.replace[%d]", path, i)
		switch {
		case replaced[e.Type]:
			c.add(at, "replaces %s twice", e.Type)
		case e.Type == GREASE || generatedExtensions[e.Type]:
			c.add(at, "%s has no body to replace", e.Type)
		case t.extension(e.Type) == nil || dropped[e.Type]:
			c.add(at, "%s is not in the hello, or dropped", e.Type)
		}
		replaced[e.Type] = true
		c.checkExtension(at, &e)
	}
	present := func(typ Code) bool {
		return typ == ExtQUICTransportParameters || t.extension(typ) != nil && !dropped[typ]
	}
	switch h.ExtensionOrder {
	case ExtensionOrderFixed:
		if len(h.Last) > 0 {
			c.add(path+".last", "applies only to a shuffled order")
		}
		if b := h.TransportParametersBefore; b == nil || *b == ExtQUICTransportParameters || !present(*b) {
			c.add(path+".transport_parameters_before", "is required with a fixed order: an extension the hello sends")
		}
	case ExtensionOrderShuffled:
		if h.TransportParametersBefore != nil {
			c.add(path+".transport_parameters_before", "applies only to a fixed order")
		}
		c.checkUnique(path+".last", h.Last)
		for _, l := range h.Last {
			if !present(l) || l == GREASE || l == ExtPreSharedKey || l == ExtPadding {
				c.add(path+".last", "%s is not an extension the hello sends and may move", l)
			}
		}
	default:
		c.add(path+".extension_order", "%q must be %q or %q", h.ExtensionOrder, ExtensionOrderFixed, ExtensionOrderShuffled)
	}
	ech := t.extension(ExtEncryptedClientHello) != nil && !dropped[ExtEncryptedClientHello]
	switch {
	case t.Resumption != nil && ech:
		c.checkPayloadLengths(path+".resumed_ech_payload_lengths", h.ResumedECHPayloadLengths)
	case len(h.ResumedECHPayloadLengths) > 0:
		c.add(path+".resumed_ech_payload_lengths", "applies only to a hello that resumes and sends encrypted_client_hello")
	}
	if r := h.VersionNegotiation; r != nil {
		at := path + ".version_negotiation"
		gone := map[Code]bool{}
		for _, d := range r.Drop {
			if gone[d] || !present(d) || d == ExtServerName || d == ExtQUICTransportParameters {
				c.add(at+".drop", "%s is not an extension the full hello sends and may drop, or is listed twice", d)
			}
			gone[d] = true
		}
		seen := map[Code]bool{}
		for i, e := range r.Replace {
			eat := fmt.Sprintf("%s.replace[%d]", at, i)
			if seen[e.Type] || !present(e.Type) || gone[e.Type] || e.Type == GREASE || generatedExtensions[e.Type] {
				c.add(eat, "%s is not a body the retried hello sends, or is listed twice", e.Type)
			}
			seen[e.Type] = true
			c.checkExtension(eat, &e)
		}
		if !r.Padding && len(r.Drop) == 0 && len(r.Replace) == 0 {
			c.add(at, "changes nothing: leave it out")
		}
	}
}

func (c *checker) checkHTTP3(p *Profile) {
	h := p.HTTP3
	const path = "http3"
	ids := map[uint64]bool{}
	for i, s := range h.Settings {
		at := fmt.Sprintf("%s.settings[%d]", path, i)
		switch {
		case ids[s.ID]:
			c.add(at, "repeats setting %#x", s.ID)
		case isGreaseH3(s.ID):
			c.add(at, "%#x is reserved: use grease_setting", s.ID)
		// RFC 9114, section 7.2.4.1: HTTP/2's settings are reserved.
		case s.ID == 0x00 || s.ID == 0x02 || s.ID == 0x03 || s.ID == 0x04 || s.ID == 0x05:
			c.add(at, "%#x is reserved in HTTP/3 (RFC 9114, section 7.2.4.1)", s.ID)
		}
		ids[s.ID] = true
		if s.ID > maxVarint || s.Value > maxVarint {
			c.add(at, "exceeds 2^62-1")
		}
	}
	if g := h.GreaseSetting; g != nil {
		c.checkH3Grease(path+".grease_setting", g)
		if g.ValueBits < 1 || g.ValueBits > 62 || g.MaxLength != 0 {
			c.add(path+".grease_setting", "needs value_bits from 1 to 62, and no max_length")
		}
	}
	if g := h.GreaseFrame; g != nil {
		c.checkH3Grease(path+".grease_frame", g)
		if g.ValueBits != 0 || g.MaxLength < 0 {
			c.add(path+".grease_frame", "takes max_length, not value_bits")
		}
	}
	c.checkUniStreams(h)
	switch h.QPACK.Encoder {
	case "", QPACKIndexAll, QPACKNewNames:
	default:
		c.add(path+".qpack.encoder", "%q must be empty, %q or %q", h.QPACK.Encoder, QPACKIndexAll, QPACKNewNames)
	}
	if h.QPACK.Encoder == "" && h.QPACK.InsertsFirst {
		c.add(path+".qpack.inserts_first", "needs an encoder")
	}
	if h.QPACK.Huffman != HuffmanShorter && h.QPACK.Huffman != HuffmanAlways {
		c.add(path+".qpack.huffman", "%q must be %q or %q", h.QPACK.Huffman, HuffmanShorter, HuffmanAlways)
	}
	if h.MaxDataFrame < 0 || h.FirstDataFrame < 0 || h.FirstDataFrame > h.MaxDataFrame {
		c.add(path+".first_data_frame", "and max_data_frame must be 0 <= first_data_frame <= max_data_frame")
	}
	c.checkEarlyData(p)
	c.checkH3Headers(p)
	for level, m := range map[string]map[string]string{
		"high": h.High,
		"low":  h.Low,
	} {
		for kind, value := range m {
			at := path + "." + level + "." + kind
			k := p.Kinds[kind]
			switch {
			case k == nil:
				c.add(at, "is not a kind of the profile")
			case level == "high" && k.High != nil || level == "low" && k.Low != nil:
				c.add(at, "the kind's own %s applies: http3.%s is only for one HTTP/2 did not measure", level, level)
			}
			c.checkHeaderValue(at, value)
		}
	}
	d := h.Discovery
	if !slices.Contains([]string{SwitchNextRequest, SwitchOnAltSvc, SwitchNewConnection}, d.Switch) {
		c.add(path+".discovery.switch", "%q must be %q, %q or %q", d.Switch, SwitchNextRequest, SwitchOnAltSvc, SwitchNewConnection)
	}
	if r := d.RaceDelayMillis; d.RaceAfterGoAway != (r != nil) || r != nil && (r.Min < 0 || r.Max < r.Min) {
		c.add(path+".discovery.race_delay_ms", "is required with race_after_goaway, and only then: 0 <= min <= max")
	}
	c.checkFallback(p)
}

// badRange reports a range that is absent or not 0 <= min <= max.
func badRange(r *Range) bool {
	return r == nil || r.Min < 0 || r.Max < r.Min
}

func (c *checker) checkEarlyData(p *Profile) {
	const path = "http3.early_data"
	d := p.HTTP3.EarlyData
	switch {
	case p.QUIC.ZeroRTT && d == nil:
		c.add(path, "is required with quic.zero_rtt")
		return
	case d == nil:
		return
	case !p.QUIC.ZeroRTT:
		c.add(path, "applies only with quic.zero_rtt")
	}
	if len(d.Methods) == 0 {
		c.add(path+".methods", "is empty: list the methods sent in 0-RTT")
	}
	for i, m := range d.Methods {
		at := fmt.Sprintf("%s.methods[%d]", path, i)
		switch {
		case m == "" || strings.ContainsFunc(m, func(r rune) bool {
			return r < 'A' || r > 'Z'
		}):
			c.add(at, "%q is not an upper-case method name", m)
		case slices.Contains(d.Methods[:i], m):
			c.add(at, "repeats %q", m)
		}
	}
}

func (c *checker) checkFallback(p *Profile) {
	const path = "http3.discovery.fallback"
	d := &p.HTTP3.Discovery
	f := &d.Fallback
	race := f.Mode == FallbackRace
	switch f.Mode {
	case FallbackRace:
		if badRange(f.TCPDelayMillis) {
			c.add(path+".tcp_delay_ms", "is required with mode %q: 0 <= min <= max", FallbackRace)
		}
	case FallbackBackground:
		if f.TCPDelayMillis != nil {
			c.add(path+".tcp_delay_ms", "applies only with mode %q", FallbackRace)
		}
	default:
		c.add(path+".mode", "%q must be %q or %q", f.Mode, FallbackRace, FallbackBackground)
	}
	if f.ZeroRTTHolds && (!race || !p.QUIC.ZeroRTT) {
		c.add(path+".zero_rtt_holds", "needs mode %q and quic.zero_rtt", FallbackRace)
	}
	if f.Hold && (race || !d.RaceAfterGoAway) {
		c.add(path+".hold", "needs mode %q and race_after_goaway", FallbackBackground)
	}
	if r := f.RevalidateMillis; r != nil && (race || badRange(r)) {
		c.add(path+".revalidate_ms", "applies only with mode %q: 0 <= min <= max", FallbackBackground)
	}
	if f.BrokenAfter < 1 {
		c.add(path+".broken_after", "must be at least 1")
	}
	switch f.Pending {
	case PendingTCP:
		if f.ReconnectMillis != nil {
			c.add(path+".reconnect_ms", "applies only with pending %q", PendingQUIC)
		}
	case PendingQUIC:
		if badRange(f.ReconnectMillis) {
			c.add(path+".reconnect_ms", "is required with pending %q: 0 <= min <= max", PendingQUIC)
		}
	default:
		c.add(path+".pending", "%q must be %q or %q", f.Pending, PendingTCP, PendingQUIC)
	}
	if p.QUIC.Loss.AbandonClose != "" && !f.AbandonOnTCP {
		c.add("quic.loss.abandon_close", "applies only with %s.abandon_on_tcp", path)
	}
}

// isGreaseH3 reports a reserved HTTP/3 identifier (0x1f * N + 0x21).
func isGreaseH3(v uint64) bool {
	return v >= 0x21 && (v-0x21)%0x1f == 0
}

func (c *checker) checkH3Grease(path string, g *H3Grease) {
	if g.IDBits < 0 || g.IDBits > h3GreaseMaxNBits {
		c.add(path+".id_bits", "must be from 0 to %d: 0x1f * N + 0x21 must fit a variable-length integer", h3GreaseMaxNBits)
	}
}

func (c *checker) checkUniStreams(h *HTTP3) {
	const path = "http3.uni_streams"
	if len(h.UniStreams) == 0 || h.UniStreams[0].Type != H3StreamControl || h.UniStreams[0].Open != H3OpenAtHandshake {
		c.add(path, "must start with the control stream, opened at the handshake (RFC 9114, section 6.2.1)")
	}
	types := map[string]bool{}
	for i, s := range h.UniStreams {
		at := fmt.Sprintf("%s[%d]", path, i)
		if !slices.Contains([]string{H3StreamControl, H3StreamQPACKEncoder, H3StreamQPACKDecoder}, s.Type) {
			c.add(at+".type", "%q must be %q, %q or %q", s.Type, H3StreamControl, H3StreamQPACKEncoder, H3StreamQPACKDecoder)
		}
		if types[s.Type] {
			c.add(at+".type", "repeats %q (RFC 9114, section 6.2.1; RFC 9204, section 4.2)", s.Type)
		}
		types[s.Type] = true
		switch s.Open {
		case H3OpenAtHandshake, H3OpenNever:
		case H3OpenOnFirstInsert:
			if s.Type != H3StreamQPACKEncoder {
				c.add(at+".open", "%q applies only to the encoder stream", H3OpenOnFirstInsert)
			}
		default:
			c.add(at+".open", "%q must be %q, %q or %q", s.Open, H3OpenAtHandshake, H3OpenOnFirstInsert, H3OpenNever)
		}
	}
	if h.QPACK.Encoder != "" && !types[H3StreamQPACKEncoder] {
		c.add(path, "needs the encoder stream: qpack.encoder uses the dynamic table")
	}
}

func (c *checker) checkH3Headers(p *Profile) {
	hd := p.HTTP3.Headers
	if hd == nil {
		return
	}
	const path = "http3.headers"
	if len(hd.PseudoHeaderOrder) != len(pseudoH3) {
		c.add(path+".pseudo_header_order", "must list %v once each", pseudoH3)
	} else {
		for _, ph := range pseudoH3 {
			if !slices.Contains(hd.PseudoHeaderOrder, ph) {
				c.add(path+".pseudo_header_order", "must list %v once each", pseudoH3)
				break
			}
		}
	}
	for i, d := range hd.Drop {
		c.checkFieldName(fmt.Sprintf("%s.drop[%d]", path, i), d)
	}
	check := func(at, anchor string, list []Header) {
		if !slices.ContainsFunc(list, func(h Header) bool {
			return h.Name == anchor
		}) {
			c.add(at, "%q is not in the request's HTTP/2 list", anchor)
		}
	}
	if hd.AltUsedNavigation != "" {
		check(path+".alt_used_navigation", hd.AltUsedNavigation, p.Headers)
	}
	for kind, anchor := range hd.AltUsed {
		k := p.Kinds[kind]
		if k == nil {
			c.add(path+".alt_used."+kind, "is not a kind of the profile")
			continue
		}
		check(path+".alt_used."+kind, anchor, k.Headers)
	}
}
