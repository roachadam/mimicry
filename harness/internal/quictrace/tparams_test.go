package quictrace

import (
	"reflect"
	"testing"
)

// TestParseTransportParameters decodes quic_transport_parameters bodies:
// RFC 9001 appendix A.2's ClientHello's, and one with what browsers add
// (a GREASE parameter, version_information, grease_quic_bit, Google's
// parameters), every parameter in order; malformed bodies are errors.
func TestParseTransportParameters(t *testing.T) {
	type tp = TransportParameter
	for _, tc := range []struct {
		name    string
		body    string
		want    []tp
		wantErr bool
	}{
		{
			name: "RFC 9001 appendix A.2",
			body: "0408ffffffffffffffff05048000ffff07048000ffff0801100104800075300901100f088394c8f03e51570806048000ffff",
			want: []tp{
				{ID: 0x04, Name: "initial_max_data", Length: 8, Value: new(uint64(1<<62 - 1))},
				{ID: 0x05, Name: "initial_max_stream_data_bidi_local", Length: 4, Value: new(uint64(65535))},
				{ID: 0x07, Name: "initial_max_stream_data_uni", Length: 4, Value: new(uint64(65535))},
				{ID: 0x08, Name: "initial_max_streams_bidi", Length: 1, Value: new(uint64(16))},
				{ID: 0x01, Name: "max_idle_timeout", Length: 4, Value: new(uint64(30000))},
				{ID: 0x09, Name: "initial_max_streams_uni", Length: 1, Value: new(uint64(16))},
				{ID: 0x0f, Name: "initial_source_connection_id", Length: 8},
				{ID: 0x06, Name: "initial_max_stream_data_bidi_remote", Length: 4, Value: new(uint64(65535))},
			},
		},
		{
			name: "browser extras",
			// GREASE id 31*5+27 = 0xb6 with 3 random bytes; version_information
			// (v1 chosen, v2 and v1 available); grease_quic_bit; google
			// connection options "ORBS"; max_datagram_frame_size 65536; an
			// unknown 0x173e holding one integer.
			body: "40b603a1b2c3" + "110c000000016b3343cf00000001" + "6ab200" + "71280443524f42" + "200480010000" + "573e0105",
			want: []tp{
				{ID: 0xb6, Grease: true, Length: 3, Hex: "a1b2c3"},
				{ID: 0x11, Name: "version_information", Length: 12, Versions: []string{"00000001", "6b3343cf", "00000001"}},
				{ID: 0x2ab2, Name: "grease_quic_bit", Length: 0},
				{ID: 0x3128, Name: "google_connection_options", Length: 4, Hex: "43524f42"},
				{ID: 0x20, Name: "max_datagram_frame_size", Length: 4, Value: new(uint64(65536))},
				{ID: 0x173e, Length: 1, Hex: "05", Value: new(uint64(5))},
			},
		},
		{
			name:    "value longer than the body",
			body:    "0104800075",
			wantErr: true,
		},
		{
			name:    "integer parameter of two integers",
			body:    "01021010",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTransportParameters(mustHex(t, tc.body))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v; want error %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}
