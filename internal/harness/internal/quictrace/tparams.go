package quictrace

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// transportParameterNames are the transport parameters browsers send (RFC
// 9000 section 18.2; RFC 9221; RFC 9287; RFC 9368; Google QUIC's;
// min_ack_delay: 0xff02de1a in ack-frequency drafts 01-02, 0xff04de1b later).
var transportParameterNames = map[uint64]string{
	0x00:       "original_destination_connection_id",
	0x01:       "max_idle_timeout",
	0x02:       "stateless_reset_token",
	0x03:       "max_udp_payload_size",
	0x04:       "initial_max_data",
	0x05:       "initial_max_stream_data_bidi_local",
	0x06:       "initial_max_stream_data_bidi_remote",
	0x07:       "initial_max_stream_data_uni",
	0x08:       "initial_max_streams_bidi",
	0x09:       "initial_max_streams_uni",
	0x0a:       "ack_delay_exponent",
	0x0b:       "max_ack_delay",
	0x0c:       "disable_active_migration",
	0x0d:       "preferred_address",
	0x0e:       "active_connection_id_limit",
	0x0f:       "initial_source_connection_id",
	0x10:       "retry_source_connection_id",
	0x11:       "version_information",
	0x20:       "max_datagram_frame_size",
	0x2ab2:     "grease_quic_bit",
	0x3127:     "initial_rtt",
	0x3128:     "google_connection_options",
	0x3129:     "user_agent_id",
	0x4752:     "google_version",
	0xff02de1a: "min_ack_delay",
	0xff04de1b: "min_ack_delay",
}

// varintParameters are the parameters whose value is one variable-length
// integer.
var varintParameters = map[uint64]bool{
	0x01:       true,
	0x03:       true,
	0x04:       true,
	0x05:       true,
	0x06:       true,
	0x07:       true,
	0x08:       true,
	0x09:       true,
	0x0a:       true,
	0x0b:       true,
	0x0e:       true,
	0x20:       true,
	0x3127:     true,
	0xff02de1a: true,
	0xff04de1b: true,
}

// connectionIDParameters carry connection IDs, random per connection.
var connectionIDParameters = map[uint64]bool{
	0x00: true,
	0x0f: true,
	0x10: true,
}

// IsGreaseTransportParameter reports a reserved transport parameter
// identifier (RFC 9000, section 18.1). Some are taken by extensions
// (min_ack_delay's 0xff02de1a), so ParseTransportParameters marks only
// unknown ones GREASE.
func IsGreaseTransportParameter(id uint64) bool {
	return id >= 27 && (id-27)%31 == 0
}

// ParseTransportParameters decodes a quic_transport_parameters (0x39) body,
// every parameter in order. Integer values are decoded, others kept as hex,
// connection IDs only by length.
func ParseTransportParameters(data []byte) ([]TransportParameter, error) {
	var out []TransportParameter

	r := &reader{
		b: data,
	}

	for len(r.b) > 0 {
		id := r.varint()
		value := r.bytes(r.varint())

		if r.err != nil {
			return out, fmt.Errorf("transport parameter %d: %w", len(out)+1, r.err)
		}

		p := TransportParameter{
			ID:     id,
			Name:   transportParameterNames[id],
			Grease: transportParameterNames[id] == "" && IsGreaseTransportParameter(id),
			Length: len(value),
		}
		v, n := varint(value)

		switch {
		case connectionIDParameters[id]:
		case varintParameters[id]:
			if n == 0 || n != len(value) {
				return out, fmt.Errorf("transport parameter %#x: %d bytes are not one integer", id, len(value))
			}

			p.Value = new(v)

		case id == 0x11:
			if len(value)%4 != 0 {
				return out, fmt.Errorf("version_information of %d bytes", len(value))
			}

			for i := 0; i < len(value); i += 4 {
				p.Versions = append(p.Versions, versionHex(binary.BigEndian.Uint32(value[i:])))
			}

		default:
			p.Hex = hex.EncodeToString(value)
			if !p.Grease && n > 0 && n == len(value) {
				p.Value = new(v)
			}
		}

		out = append(out, p)
	}

	return out, nil
}

// versionHex formats a QUIC version as 8 hex digits.
func versionHex(v uint32) string {
	return fmt.Sprintf("%08x", v)
}
