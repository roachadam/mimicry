package h2trace

import (
	"slices"
	"testing"
)

func sample(firstUpdate uint32, rep Representation, weight uint8) *Trace {
	return &Trace{
		Frames: []Frame{
			{
				Type:      "WINDOW_UPDATE",
				Increment: 15663105,
			},
			{
				Type:   "HEADERS",
				Stream: 1,
				Priority: &Priority{
					Exclusive: true,
					Weight:    weight,
				},
			},
			{
				Type:   "DATA",
				Stream: 1,
				Length: 16375,
			},
			{
				Type:      "WINDOW_UPDATE",
				Stream:    1,
				Increment: firstUpdate,
				// Credit for data the server had sent.
				SentBefore: 8 << 20,
			},
		},
		Requests: []Request{
			{
				Stream: 1,
				Path:   "/",
				Instructions: []Instruction{
					{
						Rep:   rep,
						Index: 4,
						Name:  ":path",
						Value: "/",
					},
				},
			},
		},
	}
}

func features(vs []Variation) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Feature
	}
	return out
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name    string
		samples []*Trace
		want    []string
	}{
		{
			name: "identical samples agree",
			samples: []*Trace{
				sample(3145737, Indexed, 255),
				sample(3145737, Indexed, 255),
			},
		},
		{
			// Credit returned one frame later is timing, not behaviour.
			name: "measurements within a frame agree",
			samples: []*Trace{
				sample(3145737, Indexed, 255),
				sample(3145737+16384, Indexed, 255),
			},
		},
		{
			name: "measurements beyond the slack vary",
			samples: []*Trace{
				sample(3145737, Indexed, 255),
				sample(4194304, Indexed, 255),
			},
			want: []string{
				"window_update_median[/]",
			},
		},
		{
			name: "a different representation or priority varies",
			samples: []*Trace{
				sample(3145737, Indexed, 255),
				sample(3145737, Without, 219),
			},
			want: []string{
				"frames",
				"request[/].hpack",
				"request[/].priority",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := features(Compare(tt.samples)); !slices.Equal(got, tt.want) {
				t.Errorf("varying features %v, want %v", got, tt.want)
			}
		})
	}
}
